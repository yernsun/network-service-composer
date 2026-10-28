package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/storage"
)

const maxServiceLogLine = 64 << 10

var serviceLogSlots = make(chan struct{}, 32)

type serviceLogStream struct {
	io.ReadCloser
	tty bool
}

func (s *Server) serviceLogs(w http.ResponseWriter, r *http.Request) {
	user, ok := requestUser(r)
	if !ok || user.Role != "administrator" {
		s.writeProblem(w, r, http.StatusForbidden, "auth.read_only", nil, "")
		return
	}
	select {
	case serviceLogSlots <- struct{}{}:
		defer func() { <-serviceLogSlots }()
	default:
		s.writeProblem(w, r, http.StatusTooManyRequests, "service.logs_unavailable", nil, "")
		return
	}
	tail := 200
	if raw := r.URL.Query().Get("tail"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 1000 {
			s.writeProblem(w, r, http.StatusUnprocessableEntity, "validation.filter_invalid", nil, "")
			return
		}
		tail = value
	}
	service, err := s.store.GetService(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	runtime, err := s.store.GetRuntime(r.Context(), service.RuntimeID)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	if runtime.Type != model.RuntimeSwarm || !runtime.Enabled || service.RuntimeObjectID == "" {
		s.writeProblem(w, r, http.StatusUnprocessableEntity, "service.logs_unavailable", nil, "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	stream, err := s.openServiceLogStream(ctx, runtime, service.RuntimeObjectID, tail)
	if err != nil {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store, no-transform")
		w.Header().Set("X-Accel-Buffering", "no")
		_ = sseWrite(w, "unavailable", []byte(`{"code":"service.logs_unavailable"}`))
		return
	}
	if binding := s.localLogBinding(runtime); binding != nil {
		go s.watchLogNodeOwnership(ctx, cancel, *binding)
	}
	serveServiceLogStream(ctx, w, stream)
}

func serveServiceLogStream(ctx context.Context, w http.ResponseWriter, stream serviceLogStream) {
	defer stream.Close()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Minute))
	_, _ = io.WriteString(w, ": connected\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	lines := make(chan serviceLogLine, 64)
	done := make(chan error, 1)
	go func() {
		out := &serviceLogWriter{stream: "stdout", lines: lines, ctx: ctx}
		errOut := &serviceLogWriter{stream: "stderr", lines: lines, ctx: ctx}
		var err error
		if stream.tty {
			_, err = io.Copy(out, stream)
		} else {
			_, err = stdcopy.StdCopy(out, errOut, stream)
		}
		out.finish()
		errOut.finish()
		done <- err
	}()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case line := <-lines:
			data, _ := json.Marshal(line)
			if err := sseWrite(w, "log", data); err != nil {
				return
			}
		case err := <-done:
			for len(lines) > 0 {
				data, _ := json.Marshal(<-lines)
				if sseWrite(w, "log", data) != nil {
					return
				}
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				_ = sseWrite(w, "error", []byte(`{"code":"service.logs_interrupted"}`))
			}
			return
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if err := http.NewResponseController(w).Flush(); err != nil {
				return
			}
		}
	}
}

type serviceLogLine struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}
type serviceLogWriter struct {
	stream  string
	lines   chan<- serviceLogLine
	ctx     context.Context
	pending []byte
}

func (w *serviceLogWriter) Write(p []byte) (int, error) {
	length := len(p)
	for len(p) > 0 {
		if index := bytes.IndexByte(p, '\n'); index >= 0 {
			if len(w.pending)+index > maxServiceLogLine {
				return 0, fmt.Errorf("service log line exceeds limit")
			}
			w.pending = append(w.pending, p[:index]...)
			if err := w.emit(); err != nil {
				return 0, err
			}
			p = p[index+1:]
		} else {
			w.pending = append(w.pending, p...)
			p = nil
		}
		if len(w.pending) > maxServiceLogLine {
			w.pending = nil
			return 0, fmt.Errorf("service log line exceeds limit")
		}
	}
	return length, nil
}
func (w *serviceLogWriter) emit() error {
	line := serviceLogLine{Stream: w.stream, Text: string(w.pending)}
	w.pending = nil
	select {
	case w.lines <- line:
		return nil
	case <-w.ctx.Done():
		return w.ctx.Err()
	}
}
func (w *serviceLogWriter) finish() {
	if len(w.pending) > 0 {
		_ = w.emit()
	}
}

func sseWrite(w http.ResponseWriter, event string, data []byte) error {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

func (s *Server) openServiceLogStream(ctx context.Context, runtime model.Runtime, id string, tail int) (serviceLogStream, error) {
	if strings.HasPrefix(runtime.Endpoint, "node://") {
		node := strings.TrimPrefix(runtime.Endpoint, "node://")
		for _, binding := range s.config.Controller.Nodes {
			if binding.NodeID != node {
				continue
			}
			if binding.InstanceID == s.config.Controller.InstanceID {
				return s.openDockerServiceLogs(ctx, runtime, id, tail, &binding)
			}
			return s.proxyServiceLogs(ctx, runtime, id, tail, binding.InstanceID, binding.URL)
		}
		return serviceLogStream{}, errors.New("unconfigured runtime node")
	}
	return s.openDockerServiceLogs(ctx, runtime, id, tail, nil)
}

func (s *Server) openDockerServiceLogs(ctx context.Context, runtime model.Runtime, id string, tail int, local *config.ControllerNodeConfig) (serviceLogStream, error) {
	endpoint := runtime.Endpoint
	if local != nil {
		if err := s.verifyLocalLogNode(ctx, *local); err != nil {
			return serviceLogStream{}, err
		}
		endpoint = "unix:///var/run/docker.sock"
	}
	var files engine.TLSFiles
	if runtime.CredentialRef != "" {
		credential, ok := s.config.Credentials[runtime.CredentialRef]
		if !ok {
			return serviceLogStream{}, errors.New("Docker credentials unavailable")
		}
		files = engine.TLSFiles{CA: credential.CAFile, Certificate: credential.CertFile, Key: credential.KeyFile}
	}
	cli, err := engine.NewManagementClient(model.Runtime{Endpoint: endpoint}, files)
	if err != nil {
		return serviceLogStream{}, err
	}
	closeClient := true
	defer func() {
		if closeClient {
			_ = cli.Close()
		}
	}()
	if local != nil {
		info, e := cli.Info(ctx, client.InfoOptions{})
		if e != nil || info.Info.ID != local.EngineID || info.Info.Swarm.NodeID != local.NodeID || info.Info.Swarm.Cluster == nil || info.Info.Swarm.Cluster.ID != local.ClusterID || !info.Info.Swarm.ControlAvailable {
			return serviceLogStream{}, errors.New("local Docker is not the bound Swarm manager")
		}
	}
	service, err := cli.ServiceInspect(ctx, id, client.ServiceInspectOptions{})
	if err != nil || service.Service.ID != id {
		return serviceLogStream{}, errors.New("Docker service identity unavailable")
	}
	tty := service.Service.Spec.TaskTemplate.ContainerSpec != nil && service.Service.Spec.TaskTemplate.ContainerSpec.TTY
	stream, err := cli.ServiceLogs(ctx, id, client.ServiceLogsOptions{ShowStdout: true, ShowStderr: true, Timestamps: true, Follow: true, Tail: strconv.Itoa(tail)})
	if err != nil {
		return serviceLogStream{}, err
	}
	closeClient = false
	return serviceLogStream{ReadCloser: &closeWithDockerClient{ReadCloser: stream, client: cli}, tty: tty}, nil
}

type closeWithDockerClient struct {
	io.ReadCloser
	client *client.Client
}

func (v *closeWithDockerClient) Close() error {
	err := v.ReadCloser.Close()
	_ = v.client.Close()
	return err
}

func (s *Server) verifyLocalLogNode(ctx context.Context, binding config.ControllerNodeConfig) error {
	store, ok := s.store.(*storage.SQLStore)
	if !ok {
		return errors.New("node ownership store unavailable")
	}
	doc, err := s.store.GetControlDocument(ctx, "node-session:"+binding.NodeID)
	if err != nil {
		return err
	}
	var session nodecontrol.Session
	if doc.Kind != "node-session" || doc.ParentID != binding.NodeID || json.Unmarshal(doc.Payload, &session) != nil || session.Owner <= 0 {
		return nodecontrol.ErrIdentity
	}
	return lease.CheckOwner(ctx, store.DB(), store.Dialect(), nodecontrol.LockKey(binding.NodeID), session.Owner)
}

func (s *Server) localLogBinding(runtime model.Runtime) *config.ControllerNodeConfig {
	for _, binding := range s.config.Controller.Nodes {
		if binding.InstanceID == s.config.Controller.InstanceID && runtime.Endpoint == "node://"+binding.NodeID {
			copy := binding
			return &copy
		}
	}
	return nil
}

func (s *Server) watchLogNodeOwnership(ctx context.Context, cancel context.CancelFunc, binding config.ControllerNodeConfig) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check, stop := context.WithTimeout(ctx, 3*time.Second)
			err := s.verifyLocalLogNode(check, binding)
			stop()
			if err != nil {
				cancel()
				return
			}
		}
	}
}

func (s *Server) proxyServiceLogs(ctx context.Context, runtime model.Runtime, id string, tail int, peer, origin string) (serviceLogStream, error) {
	tlsConfig, err := controlplane.InternalTLS(s.config.Controller.Internal.CAFile, s.config.Controller.Internal.CertFile, s.config.Controller.Internal.KeyFile, s.config.Controller.InstanceID)
	if err != nil {
		return serviceLogStream{}, err
	}
	verify := tlsConfig.VerifyConnection
	tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
		if err := verify(state); err != nil {
			return err
		}
		actual, err := controlplane.CertificateIdentity(state.PeerCertificates[0])
		if err != nil || actual != peer {
			return nodecontrol.ErrIdentity
		}
		return nil
	}
	u, err := url.Parse(origin)
	if err != nil || controlplane.ValidateInternalURL(origin) != nil {
		return serviceLogStream{}, nodecontrol.ErrIdentity
	}
	u.Path = "/internal/v1/service-logs"
	q := u.Query()
	q.Set("runtime_id", runtime.ID)
	q.Set("service_id", id)
	q.Set("tail", strconv.Itoa(tail))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return serviceLogStream{}, err
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig, DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 15 * time.Second}
	response, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		transport.CloseIdleConnections()
		return serviceLogStream{}, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		transport.CloseIdleConnections()
		return serviceLogStream{}, errors.New("node service logs unavailable")
	}
	return serviceLogStream{ReadCloser: &closeWithTransport{ReadCloser: response.Body, transport: transport}, tty: response.Header.Get("X-NSC-Log-TTY") == "true"}, nil
}

type closeWithTransport struct {
	io.ReadCloser
	transport *http.Transport
}

func (v *closeWithTransport) Close() error {
	err := v.ReadCloser.Close()
	v.transport.CloseIdleConnections()
	return err
}

// InternalServiceLogs is mounted only on the mutual-TLS Controller listener.
func (s *Server) InternalServiceLogs() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet || r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		peer, err := controlplane.CertificateIdentity(r.TLS.PeerCertificates[0])
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		known := false
		for _, binding := range s.config.Controller.Nodes {
			if binding.InstanceID == peer {
				known = true
				break
			}
		}
		if !known {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		runtime, err := s.store.GetRuntime(r.Context(), r.URL.Query().Get("runtime_id"))
		if err != nil || runtime.Type != model.RuntimeSwarm || !runtime.Enabled {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		id := r.URL.Query().Get("service_id")
		found := false
		for after := ""; !found; {
			services, e := s.store.ListServicesFiltered(r.Context(), storage.ServiceFilter{RuntimeID: runtime.ID}, storage.Page{After: after, Limit: 200})
			if e != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			for _, value := range services {
				if value.RuntimeObjectID == id {
					found = true
					break
				}
			}
			if len(services) < 200 {
				break
			}
			after = services[len(services)-1].ID
		}
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		tail, err := strconv.Atoi(r.URL.Query().Get("tail"))
		if err != nil || tail < 1 || tail > 1000 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var self *config.ControllerNodeConfig
		for _, binding := range s.config.Controller.Nodes {
			if binding.InstanceID == s.config.Controller.InstanceID && runtime.Endpoint == "node://"+binding.NodeID {
				self = &binding
				break
			}
		}
		if self == nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
		defer cancel()
		stream, err := s.openDockerServiceLogs(ctx, runtime, id, tail, self)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		defer stream.Close()
		go s.watchLogNodeOwnership(ctx, cancel, *self)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-NSC-Log-TTY", strconv.FormatBool(stream.tty))
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Minute))
		_, _ = io.Copy(w, stream)
	})
}
