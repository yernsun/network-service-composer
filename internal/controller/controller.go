package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/yernsun/network-service-composer/internal/agent"
	"github.com/yernsun/network-service-composer/internal/api"
	certificatex "github.com/yernsun/network-service-composer/internal/certificate"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/deployment"
	"github.com/yernsun/network-service-composer/internal/discovery"
	"github.com/yernsun/network-service-composer/internal/dockerresource"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/gateway"
	"github.com/yernsun/network-service-composer/internal/lease"
	metricsx "github.com/yernsun/network-service-composer/internal/metrics"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	runtimex "github.com/yernsun/network-service-composer/internal/runtime"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type Background interface{ Run(context.Context) error }

type Controller struct {
	config           config.Config
	store            *storage.SQLStore
	core             *core.Core
	metrics          *metricsx.Metrics
	logger           *slog.Logger
	ready            atomic.Bool
	schedulerTerm    atomic.Int64
	background       []Background
	processLock      *lease.ProcessLock
	gatewayRPC       *gateway.Server
	election         *controlplane.Election
	internalTLS      *tls.Config
	localGateway     *agent.Agent
	nodeReports      *nodecontrol.Reports
	localCollector   *nodecontrol.Collector
	deploymentWorker *deployment.Worker
}

func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (*Controller, error) {
	if logger == nil {
		logger = slog.Default()
	}
	var internalTLS *tls.Config
	var err error
	if !cfg.Controller.RecoveryMode {
		internalTLS, err = leadershipConfig(&cfg)
		if err != nil {
			return nil, fmt.Errorf("controller leadership configuration: %w", err)
		}
	} else if cfg.Storage.AutoMigrate {
		return nil, errors.New("recovery mode forbids automatic migrations")
	}
	var processLock *lease.ProcessLock
	if cfg.Storage.Driver == string(storage.SQLite) {
		processLock, err = lease.AcquireSQLiteProcessLock(cfg.Storage.DSN)
		if err != nil {
			return nil, err
		}
	}
	store, err := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		_ = processLock.Close()
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = store.Close()
			_ = processLock.Close()
		}
	}()
	if err = store.Ping(ctx); err != nil {
		return nil, fmt.Errorf("connect storage: %w", err)
	}
	prepared, err := store.RecoveryPrepared(ctx)
	if err != nil {
		return nil, err
	}
	if prepared && !cfg.Controller.RecoveryMode {
		return nil, errors.New("restored database is held for recovery review; use recovery mode")
	}
	if cfg.Controller.RecoveryMode && !prepared {
		return nil, errors.New("recovery mode requires recovery-prepare before startup")
	}
	if cfg.Controller.RecoveryMode {
		if err = store.ValidateRecoverySchema(ctx); err != nil {
			return nil, err
		}
	}
	if cfg.Storage.AutoMigrate {
		if err = store.Migrate(ctx); err != nil {
			return nil, err
		}
	}
	if store.Dialect() == storage.SQLite && !cfg.Controller.RecoveryMode {
		after := ""
		for {
			runtimes, listErr := store.ListRuntimes(ctx, storage.Page{After: after, Limit: 200})
			if listErr != nil {
				return nil, fmt.Errorf("validate SQLite deployment: %w", listErr)
			}
			for _, runtime := range runtimes {
				if runtime.Type == model.RuntimeSwarm {
					return nil, fmt.Errorf("SQLite supports Compose runtimes only; Swarm runtime %s is configured", runtime.ID)
				}
			}
			if len(runtimes) < 200 {
				break
			}
			after = runtimes[len(runtimes)-1].ID
		}
	}
	ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
	if err != nil {
		return nil, err
	}
	application := core.New(store, cfg, ring)
	if cfg.Controller.RecoveryMode {
		ok = true
		return &Controller{config: cfg, store: store, core: application, metrics: metricsx.New(), logger: logger, processLock: processLock}, nil
	}
	if err = application.EnsureBuiltinCatalog(ctx); err != nil {
		return nil, fmt.Errorf("initialize built-in application catalog: %w", err)
	}
	value := &Controller{config: cfg, store: store, core: application, metrics: metricsx.New(), logger: logger, processLock: processLock}
	application.HAController = value.haController
	broker := gateway.NewBroker()
	value.gatewayRPC = gateway.NewServer(application, broker, gateway.ServerOptions{
		OfflineTimeout: cfg.Controller.Gateway.OfflineTimeout.Std(), ReconcileDelay: cfg.Controller.Gateway.Reconcile.Debounce.Std(),
		ReconcileMaxWait: cfg.Controller.Gateway.Reconcile.MaxDelay.Std(),
	}, value.metrics, logger)
	factory := runtimex.ProviderFactory(func(_ context.Context, rt model.Runtime, network string) (runtimex.Provider, error) {
		if value.nodeReports != nil {
			return &nodecontrol.Provider{Reports: value.nodeReports, Runtime: rt, Network: network, Readiness: cfg.Controller.Endpoint.Readiness}, nil
		}
		var files engine.TLSFiles
		if rt.CredentialRef != "" {
			credential, exists := cfg.Credentials[rt.CredentialRef]
			if !exists {
				return nil, fmt.Errorf("Docker credential_ref %q is not configured", rt.CredentialRef)
			}
			files = engine.TLSFiles{CA: credential.CAFile, Certificate: credential.CertFile, Key: credential.KeyFile}
		}
		return engine.New(rt, network, cfg.Controller.Endpoint.Readiness, files)
	})
	locker := lease.New(store.DB(), store.Dialect())
	value.internalTLS = internalTLS
	value.election = &controlplane.Election{
		Identity: controlplane.Identity{ID: cfg.Controller.InstanceID, URL: cfg.Controller.Internal.URL, GRPCAddress: cfg.Controller.Internal.GRPCAddress},
		Locker:   locker, Store: controlplane.SQLState{Store: store}, Logger: logger,
	}
	if err := value.configureNodes(store, ring, locker); err != nil {
		return nil, err
	}
	if cfg.Controller.SystemProject.RuntimeID != "" || cfg.Controller.SystemProject.ClusterID != "" || len(cfg.Controller.SystemProject.Services) > 0 {
		if cfg.Controller.SystemProject.RuntimeID == "" || cfg.Controller.SystemProject.ClusterID == "" || len(cfg.Controller.SystemProject.Services) == 0 {
			return nil, fmt.Errorf("incomplete Gateway system project registration")
		}
		if value.nodeReports == nil {
			return nil, fmt.Errorf("Gateway system project requires authenticated node reporting")
		}
		value.AddBackground(systemProjectReconciler{controller: value})
	}
	if cfg.Controller.LocalGateway {
		if err := cfg.ValidateAgent(); err != nil {
			return nil, err
		}
		value.localGateway = agent.New(cfg, "unified-controller", logger)
		value.localGateway.SetLeaderStore(value.election.Store)
	}
	if value.localCollector == nil {
		value.AddBackground(application.Cluster)
	}
	value.AddBackground(application, core.CommunityMonitor{Core: application}, core.NotificationWorker{Core: application})
	value.AddBackground(discovery.New(store, factory, locker, cfg.Controller, value.metrics, logger, broker))
	value.deploymentWorker = deployment.NewWorker(store, locker, ring, cfg, value.metrics, logger)
	if value.nodeReports != nil {
		verifier, err := execution.NewSQLVerifier(store, cfg, value.nodeReports, ring)
		if err != nil {
			return nil, err
		}
		value.deploymentWorker.ConfigureExecution(verifier, internalTLS)
	}
	value.AddBackground(value.deploymentWorker)
	value.AddBackground(core.ProjectDeletionWorker{Core: application, Locker: locker})
	value.AddBackground(certificatex.NewWorker(store, locker, ring, cfg, value.metrics, logger))
	value.AddBackground(gateway.NewMonitor(store, cfg.Controller.Gateway.OfflineTimeout.Std(), broker, logger))
	value.AddBackground(metricsx.NewStateReporter(store, value.metrics, 15*time.Second, logger))
	if store.Dialect() == storage.Postgres {
		value.AddBackground(poolChangeListener{store: store, broker: broker})
	}
	ok = true
	return value, nil
}

type poolChangeListener struct {
	store  *storage.SQLStore
	broker *gateway.Broker
}

func (l poolChangeListener) Run(ctx context.Context) error {
	return l.store.ListenPoolChanges(ctx, l.broker.Publish)
}

func (c *Controller) Core() *core.Core           { return c.core }
func (c *Controller) Store() *storage.SQLStore   { return c.store }
func (c *Controller) Metrics() *metricsx.Metrics { return c.metrics }
func (c *Controller) AddBackground(values ...Background) {
	c.background = append(c.background, values...)
}

func (c *Controller) Run(ctx context.Context) error {
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	apiServer, err := api.New(c.core, c.config, c.logger, c.isReady)
	if err != nil {
		return err
	}
	forwarder := controlplane.Forwarder{Election: c.election, TLS: c.internalTLS, Unavailable: apiServer.UnavailableHandler()}
	handler := apiServer.Handler()
	if !c.config.Controller.RecoveryMode {
		handler = forwarder.Public(handler)
	}
	httpServer := &http.Server{Addr: c.config.Server.HTTPListen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: c.config.Server.ReadTimeout.Std(), WriteTimeout: c.config.Server.WriteTimeout.Std(), IdleTimeout: 60 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	metricsServer := &http.Server{Addr: c.config.Metrics.Listen, Handler: promhttp.HandlerFor(c.metrics.Registry, promhttp.HandlerOpts{}), ReadHeaderTimeout: 3 * time.Second}
	errorsCh := make(chan error, len(c.background)+4)
	var internalServer *http.Server
	var internalListener net.Listener
	if c.internalTLS != nil {
		internalListener, err = net.Listen("tcp", c.config.Controller.Internal.Listen)
		if err != nil {
			return fmt.Errorf("listen for internal controller traffic: %w", err)
		}
		defer internalListener.Close()
		internalHandler := http.NewServeMux()
		internalHandler.Handle("/", forwarder.Internal(handler))
		internalHandler.Handle(controlplane.ReadyPath, forwarder.ReadyHandler(func(term int64) bool { return c.ready.Load() && c.schedulerTerm.Load() == term }))
		if c.nodeReports != nil {
			internalHandler.Handle(execution.InternalPath, c.deploymentWorker.InternalExecutionHandler(runCtx))
			internalHandler.Handle("/internal/v1/service-logs", apiServer.InternalServiceLogs())
			internalHandler.Handle("/internal/v1/docker-resources", (dockerresource.Manager{Store: c.store, Config: c.config, Reports: c.nodeReports}).InternalHandler())
			internalHandler.Handle("/internal/v1/volumes", c.core.Volumes().InternalHandler())
			internalHandler.Handle("/internal/v1/node-sessions", c.nodeReports.Handler())
			internalHandler.Handle("/internal/v1/node-observations", c.nodeReports.Handler())
			internalHandler.Handle("/internal/v1/node-events", c.nodeReports.Handler())
		}
		internalServer = &http.Server{Handler: internalHandler, TLSConfig: c.internalTLS, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: c.config.Server.ReadTimeout.Std(), WriteTimeout: c.config.Server.WriteTimeout.Std(), IdleTimeout: 30 * time.Second}
	}
	var grpcServer *grpc.Server
	var grpcListener net.Listener
	if c.config.Server.GRPCListen != "" && !c.config.Controller.RecoveryMode {
		tlsConfig, tlsErr := gatewayTLSConfig(c.config)
		if tlsErr != nil {
			return tlsErr
		}
		grpcListener, err = net.Listen("tcp", c.config.Server.GRPCListen)
		if err != nil {
			return fmt.Errorf("listen for gateway gRPC: %w", err)
		}
		grpcServer = grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsConfig)), grpc.StreamInterceptor(c.leaderStream), grpc.MaxRecvMsgSize(gateway.MaxCompressedSize+gateway.DefaultChunkSize), grpc.MaxSendMsgSize(gateway.MaxCompressedSize+gateway.DefaultChunkSize))
		c.gatewayRPC.Register(grpcServer)
	}
	var background sync.WaitGroup
	if c.localCollector != nil {
		for _, worker := range []Background{c.localCollector, c.core.Cluster} {
			background.Add(1)
			go func(worker Background) {
				defer background.Done()
				if err := worker.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
					errorsCh <- err
				}
			}(worker)
		}
	}
	if c.localGateway != nil {
		background.Add(1)
		go func() {
			defer background.Done()
			if err := c.localGateway.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
				errorsCh <- err
			}
		}()
	}
	if !c.config.Controller.RecoveryMode {
		background.Add(1)
		go func() {
			defer background.Done()
			if err := c.election.Run(runCtx, c.runLeader); err != nil && !errors.Is(err, context.Canceled) {
				errorsCh <- err
			}
		}()
	}
	if internalServer != nil {
		go func() {
			if err := internalServer.Serve(tls.NewListener(internalListener, c.internalTLS)); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errorsCh <- err
			}
		}()
	}
	go func() {
		c.logger.Info("HTTP API listening", "address", httpServer.Addr)
		var err error
		if c.config.Server.HTTPTLSCertFile != "" && c.config.Server.HTTPTLSKeyFile != "" {
			err = httpServer.ListenAndServeTLS(c.config.Server.HTTPTLSCertFile, c.config.Server.HTTPTLSKeyFile)
		} else {
			err = httpServer.ListenAndServe()
		}
		if !errors.Is(err, http.ErrServerClosed) {
			errorsCh <- err
		}
	}()
	if grpcServer != nil {
		go func() {
			c.logger.Info("gateway gRPC listening", "address", grpcListener.Addr().String())
			if serveErr := grpcServer.Serve(grpcListener); serveErr != nil {
				errorsCh <- serveErr
			}
		}()
	}
	go func() {
		c.logger.Info("metrics listening", "address", metricsServer.Addr)
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errorsCh <- err
		}
	}()
	c.ready.Store(true)
	select {
	case <-ctx.Done():
	case err = <-errorsCh:
	}
	c.ready.Store(false)
	runCancel()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = metricsServer.Shutdown(shutdownCtx)
	_ = httpServer.Shutdown(shutdownCtx)
	if internalServer != nil {
		_ = internalServer.Shutdown(shutdownCtx)
	}
	if grpcServer != nil {
		stopped := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-shutdownCtx.Done():
			grpcServer.Stop()
		}
	}
	workersStopped := make(chan struct{})
	go func() {
		background.Wait()
		if c.deploymentWorker != nil {
			c.deploymentWorker.WaitExecutors()
		}
		close(workersStopped)
	}()
	select {
	case <-workersStopped:
	case <-shutdownCtx.Done():
		if err == nil {
			err = fmt.Errorf("background workers did not stop before the shutdown deadline: %w", shutdownCtx.Err())
		}
	}
	closeErr := c.store.Close()
	lockErr := c.processLock.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return lockErr
}

func gatewayTLSConfig(cfg config.Config) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(cfg.Server.GRPCTLSCertFile, cfg.Server.GRPCTLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load controller TLS key pair: %w", err)
	}
	caPEM, err := os.ReadFile(cfg.Server.GRPCClientCAFile)
	if err != nil {
		return nil, fmt.Errorf("read gateway client CA: %w", err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("gateway client CA file contains no certificates")
	}
	return &tls.Config{Certificates: []tls.Certificate{certificate}, ClientCAs: clientCAs, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS13}, nil
}
