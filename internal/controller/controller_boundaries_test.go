package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/pki"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func controllerConfigFixture(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Storage.Driver = "sqlite"
	cfg.Storage.DSN = "file:" + filepath.Join(t.TempDir(), "controller.db") + "?_pragma=foreign_keys(1)&_time_format=sqlite"
	cfg.Storage.AutoMigrate = true
	cfg.Security.PrimaryKeyID = "fixture"
	cfg.Security.EncryptionKeys = map[string]string{"fixture": base64.StdEncoding.EncodeToString(make([]byte, 32))}
	cfg.Security.AdminToken = "isolated-controller-admin-token"
	cfg.Server.HTTPListen = "127.0.0.1:0"
	cfg.Server.GRPCListen = ""
	cfg.Metrics.Listen = "127.0.0.1:0"
	return cfg
}
func controllerTLSFixture(t *testing.T, cfg *config.Config) {
	t.Helper()
	dir := t.TempDir()
	ca, key := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	if e := pki.InitCA(ca, key, "isolated test CA", time.Hour); e != nil {
		t.Fatal(e)
	}
	cfg.Server.GRPCTLSCertFile = filepath.Join(dir, "server.pem")
	cfg.Server.GRPCTLSKeyFile = filepath.Join(dir, "server.key")
	cfg.Server.GRPCClientCAFile = ca
	if e := pki.Issue(ca, key, cfg.Server.GRPCTLSCertFile, cfg.Server.GRPCTLSKeyFile, pki.CertificateOptions{CommonName: "localhost", IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, Server: true, Duration: time.Hour}); e != nil {
		t.Fatal(e)
	}
}
func TestControllerStartupFailuresReleaseDatabaseOwnership(t *testing.T) {
	for _, stage := range []string{"invalid driver", "memory database", "cancelled", "unmigrated", "invalid encryption key", "missing directory", "duplicate owner"} {
		t.Run(stage, func(t *testing.T) {
			cfg := controllerConfigFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch stage {
			case "invalid driver":
				cfg.Storage.Driver = "unknown"
			case "memory database":
				cfg.Storage.DSN = ":memory:"
			case "cancelled":
				cancel()
			case "unmigrated":
				cfg.Storage.AutoMigrate = false
			case "invalid encryption key":
				cfg.Security.EncryptionKeys["fixture"] = "invalid"
			case "missing directory":
				cfg.Storage.DSN = filepath.Join(t.TempDir(), "missing", "db")
			case "duplicate owner":
				lock, e := lease.AcquireSQLiteProcessLock(cfg.Storage.DSN)
				if e != nil {
					t.Fatal(e)
				}
				defer lock.Close()
			}
			c, e := New(ctx, cfg, nil)
			if e == nil || c != nil {
				t.Fatal("unsafe controller startup accepted")
			}
			if stage == "cancelled" || stage == "unmigrated" || stage == "invalid encryption key" {
				lock, e := lease.AcquireSQLiteProcessLock(cfg.Storage.DSN)
				if e != nil {
					t.Fatal("failed startup retained exclusive ownership", e)
				}
				if e = lock.Close(); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}
func TestControllerGatewayTLSRequiresVerifiedClients(t *testing.T) {
	cfg := controllerConfigFixture(t)
	controllerTLSFixture(t, &cfg)
	good, e := gatewayTLSConfig(cfg)
	if e != nil || good.MinVersion != tls.VersionTLS13 || good.ClientAuth != tls.RequireAndVerifyClientCert || len(good.Certificates) != 1 || good.ClientCAs == nil {
		t.Fatal("gateway TLS weakened", e)
	}
	for _, stage := range []string{"certificate", "key", "CA missing", "CA invalid"} {
		t.Run(stage, func(t *testing.T) {
			bad := cfg
			switch stage {
			case "certificate":
				bad.Server.GRPCTLSCertFile = "missing"
			case "key":
				bad.Server.GRPCTLSKeyFile = "missing"
			case "CA missing":
				bad.Server.GRPCClientCAFile = "missing"
			case "CA invalid":
				bad.Server.GRPCClientCAFile = filepath.Join(t.TempDir(), "invalid")
				if e := os.WriteFile(bad.Server.GRPCClientCAFile, []byte("not a CA"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if value, e := gatewayTLSConfig(bad); e == nil || value != nil {
				t.Fatal("invalid trust material accepted")
			}
		})
	}
}

type controllerBackgroundFunc func(context.Context) error

func (f controllerBackgroundFunc) Run(ctx context.Context) error { return f(ctx) }
func controllerAddress(t *testing.T) string {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := l.Addr().String()
	if e = l.Close(); e != nil {
		t.Fatal(e)
	}
	return address
}

func TestControllerRunReadinessAndGracefulWorkerShutdown(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP", true: "TLS and gateway RPC"}[secure], func(t *testing.T) {
			cfg := controllerConfigFixture(t)
			cfg.Server.HTTPListen = controllerAddress(t)
			scheme := "http"
			transport := &http.Transport{Proxy: nil}
			defer transport.CloseIdleConnections()
			if secure {
				controllerTLSFixture(t, &cfg)
				cfg.Server.HTTPTLSCertFile = cfg.Server.GRPCTLSCertFile
				cfg.Server.HTTPTLSKeyFile = cfg.Server.GRPCTLSKeyFile
				cfg.Server.GRPCListen = "127.0.0.1:0"
				scheme = "https"
				raw, e := os.ReadFile(cfg.Server.GRPCClientCAFile)
				if e != nil {
					t.Fatal(e)
				}
				roots := x509.NewCertPool()
				if !roots.AppendCertsFromPEM(raw) {
					t.Fatal("fixture CA invalid")
				}
				transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
			}
			c, e := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = c.store.Close(); _ = c.processLock.Close() })
			if c.Core() == nil || c.Store() == nil || c.Metrics() == nil || len(c.background) < 8 {
				t.Fatal("controller did not initialize service dependencies")
			}
			stopped := make(chan struct{})
			started := make(chan struct{})
			c.background = nil
			c.AddBackground(controllerBackgroundFunc(func(ctx context.Context) error { close(started); <-ctx.Done(); close(stopped); return ctx.Err() }))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- c.Run(ctx) }()
			client := &http.Client{Transport: transport, Timeout: 100 * time.Millisecond}
			ready := false
			for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
				r, e := client.Get(scheme + "://" + cfg.Server.HTTPListen + "/readyz")
				if e == nil {
					body, _ := io.ReadAll(r.Body)
					r.Body.Close()
					if r.StatusCode == 200 && strings.Contains(string(body), "ready") {
						ready = true
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("leader worker did not start")
			}
			cancel()
			select {
			case e = <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("controller did not shut down")
			}
			if e != nil || !ready || c.ready.Load() {
				t.Fatalf("readiness/shutdown mismatch ready=%t current=%t error=%v", ready, c.ready.Load(), e)
			}
			select {
			case <-stopped:
			default:
				t.Fatal("shutdown returned before workers stopped")
			}
			if e = c.store.Ping(context.Background()); e == nil {
				t.Fatal("shutdown left database open")
			}
			lock, e := lease.AcquireSQLiteProcessLock(cfg.Storage.DSN)
			if e != nil {
				t.Fatal("shutdown retained database ownership", e)
			}
			_ = lock.Close()
		})
	}
}

func TestControllerRunPropagatesListenerAndWorkerFailures(t *testing.T) {
	failure := errors.New("background validation failed")
	for _, stage := range []string{"worker", "HTTP", "metrics", "HTTPS certificate", "gateway TLS", "gateway listener", "identity store"} {
		t.Run(stage, func(t *testing.T) {
			cfg := controllerConfigFixture(t)
			c, e := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if e != nil {
				t.Fatal(e)
			}
			defer c.store.Close()
			defer c.processLock.Close()
			c.background = nil
			switch stage {
			case "worker":
				c.AddBackground(controllerBackgroundFunc(func(context.Context) error { return failure }))
			case "HTTP":
				c.config.Server.HTTPListen = "invalid address"
			case "metrics":
				c.config.Metrics.Listen = "invalid address"
			case "HTTPS certificate":
				c.config.Server.HTTPTLSCertFile = "missing"
				c.config.Server.HTTPTLSKeyFile = "missing"
			case "gateway TLS":
				c.config.Server.GRPCListen = "127.0.0.1:0"
			case "gateway listener":
				controllerTLSFixture(t, &c.config)
				c.config.Server.GRPCListen = "invalid address"
			case "identity store":
				_ = c.store.Close()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			e = c.Run(ctx)
			if e == nil || errors.Is(e, context.DeadlineExceeded) || c.ready.Load() {
				t.Fatalf("service failure not reported: %v", e)
			}
			if stage == "worker" && !errors.Is(e, failure) {
				t.Fatal("worker failure lost", e)
			}
		})
	}
}

func TestPoolChangeListenerStopsOnCancellation(t *testing.T) {
	cfg := controllerConfigFixture(t)
	db, e := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = (poolChangeListener{store: db}).Run(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal("listener did not preserve cancellation", e)
	}
}
