package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/pki"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func logCoverageService(t *testing.T, server *Server, runtime model.Runtime, name string) model.Service {
	t.Helper()
	now := time.Now().UTC()
	service := model.Service{ID: name, RuntimeID: runtime.ID, RuntimeObjectID: "docker-" + name, ServiceKey: name,
		Name: name, ExternalID: name, Namespace: "fixture", Status: model.ServiceAvailable,
		Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := server.store.UpsertService(t.Context(), &service); err != nil {
		t.Fatal(err)
	}
	return service
}

func coverageDockerPath(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/v") {
		if index := strings.Index(r.URL.Path[1:], "/"); index >= 0 {
			return r.URL.Path[index+1:]
		}
	}
	return r.URL.Path
}

func coverageDockerPing(w http.ResponseWriter, r *http.Request) bool {
	if coverageDockerPath(r) != "/_ping" {
		return false
	}
	w.Header().Set("API-Version", "1.52")
	w.WriteHeader(http.StatusOK)
	return true
}

// The production Store intentionally disallows managing a Swarm with SQLite.
// Seed only the API fixture's runtime so these tests exercise the HTTP boundary
// and real Docker SDK without requiring a database server or a Docker daemon.
func coverageRuntimeSnapshot(t *testing.T, server *Server, runtime model.Runtime) {
	t.Helper()
	if _, err := server.store.(*storage.SQLStore).DB().ExecContext(t.Context(),
		`UPDATE runtimes SET runtime_type=?,endpoint=?,cluster_id=?,enabled=?,management_enabled=?,credential_ref=? WHERE id=?`,
		runtime.Type, runtime.Endpoint, runtime.ClusterID, runtime.Enabled, runtime.ManagementEnabled, runtime.CredentialRef, runtime.ID); err != nil {
		t.Fatal(err)
	}
}

func coverageLogFrame(stream byte, data string) []byte {
	frame := make([]byte, 8, 8+len(data))
	frame[0] = stream
	binary.BigEndian.PutUint32(frame[4:], uint32(len(data)))
	return append(frame, data...)
}

func TestCoverageServiceLogsUsesDockerSDKAndFlushesFinalLines(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(map[bool]string{false: "multiplexed", true: "tty"}[tty], func(t *testing.T) {
			server, handler, runtime, _ := managedRuntimeFixture(t)
			service := logCoverageService(t, server, runtime, "web")
			var logRequests atomic.Int32
			docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if coverageDockerPing(w, r) {
					return
				}
				switch coverageDockerPath(r) {
				case "/services/" + service.RuntimeObjectID:
					_ = json.NewEncoder(w).Encode(swarm.Service{ID: service.RuntimeObjectID,
						Spec: swarm.ServiceSpec{TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{TTY: tty}}}})
				case "/services/" + service.RuntimeObjectID + "/logs":
					logRequests.Add(1)
					query := r.URL.Query()
					if query.Get("stdout") != "1" || query.Get("stderr") != "1" || query.Get("follow") != "1" || query.Get("timestamps") != "1" || query.Get("tail") != "37" {
						t.Errorf("log request lost follow, stream or tail policy: %v", query)
					}
					if tty {
						_, _ = io.WriteString(w, "ready\nlast line")
					} else {
						_, _ = w.Write(coverageLogFrame(1, "ready\nlast line"))
						_, _ = w.Write(coverageLogFrame(2, "warning\nlast error"))
					}
				default:
					t.Errorf("unexpected Docker request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer docker.Close()
			runtime.Type, runtime.Endpoint = model.RuntimeSwarm, docker.URL
			coverageRuntimeSnapshot(t, server, runtime)
			response := managementRequest(t, handler, http.MethodGet, "/services/"+service.ID+"/logs?tail=37", nil, nil)
			body := response.Body.String()
			if response.Code != http.StatusOK || logRequests.Load() != 1 || !strings.Contains(body, `"text":"last line"`) || !strings.Contains(body, `"stream":"stdout"`) {
				t.Fatalf("log stream did not preserve final unterminated stdout: %d %s", response.Code, body)
			}
			if !tty && (!strings.Contains(body, `"text":"last error"`) || !strings.Contains(body, `"stream":"stderr"`)) {
				t.Fatalf("stderr was merged or dropped: %s", body)
			}
			if strings.Contains(body, "event: error") || response.Header().Get("Cache-Control") != "no-store, no-transform" || response.Header().Get("X-Accel-Buffering") != "no" {
				t.Fatalf("log stream caching or completion contract changed: %v %s", response.Header(), body)
			}
		})
	}
}

func TestCoverageServiceLogsRejectsInvalidRequestsAndUnknownDockerIdentity(t *testing.T) {
	server, handler, runtime, _ := managedRuntimeFixture(t)
	service := logCoverageService(t, server, runtime, "web")
	for _, tail := range []string{"bad", "0", "1001"} {
		response := managementRequest(t, handler, http.MethodGet, "/services/"+service.ID+"/logs?tail="+tail, nil, nil)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid tail admitted: %s %d", tail, response.Code)
		}
	}
	if response := managementRequest(t, handler, http.MethodGet, "/services/missing/logs", nil, nil); response.Code != http.StatusNotFound {
		t.Fatalf("unknown service admitted: %d", response.Code)
	}
	for _, scenario := range []string{"foreign-service", "inspect-unavailable", "logs-unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			var logs atomic.Int32
			docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if coverageDockerPing(w, r) {
					return
				}
				if strings.HasSuffix(r.URL.Path, "/logs") {
					logs.Add(1)
					http.Error(w, "private daemon diagnostics", http.StatusServiceUnavailable)
					return
				}
				if scenario == "inspect-unavailable" {
					http.Error(w, "private daemon diagnostics", http.StatusServiceUnavailable)
					return
				}
				identity := service.RuntimeObjectID
				if scenario == "foreign-service" {
					identity = "foreign-service"
				}
				_ = json.NewEncoder(w).Encode(swarm.Service{ID: identity})
			}))
			defer docker.Close()
			runtime.Type, runtime.Endpoint = model.RuntimeSwarm, docker.URL
			coverageRuntimeSnapshot(t, server, runtime)
			response := managementRequest(t, handler, http.MethodGet, "/services/"+service.ID+"/logs", nil, nil)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "event: unavailable") || strings.Contains(response.Body.String(), "private daemon") {
				t.Fatalf("unknown log evidence leaked or appeared healthy: %d %s", response.Code, response.Body)
			}
			if scenario != "logs-unavailable" && logs.Load() != 0 {
				t.Fatal("logs read before verifying Docker identity")
			}
		})
	}
	for index := 0; index < cap(serviceLogSlots); index++ {
		serviceLogSlots <- struct{}{}
	}
	response := managementRequest(t, handler, http.MethodGet, "/services/"+service.ID+"/logs", nil, nil)
	for index := 0; index < cap(serviceLogSlots); index++ {
		<-serviceLogSlots
	}
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("exhausted stream slots did not reject load: %d", response.Code)
	}
}

func TestCoverageDockerLogReaderClosesHTTPRequestWhenClientDisconnects(t *testing.T) {
	server, _, runtime, _ := managedRuntimeFixture(t)
	closed := make(chan struct{})
	docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if coverageDockerPing(w, r) {
			return
		}
		if strings.HasSuffix(r.URL.Path, "/logs") {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(closed)
			return
		}
		_ = json.NewEncoder(w).Encode(swarm.Service{ID: "docker-web"})
	}))
	defer docker.Close()
	runtime.Endpoint = docker.URL
	stream, err := server.openDockerServiceLogs(t.Context(), runtime, "docker-web", 200, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("closing logs left Docker request and client alive")
	}
}

func coveragePeerCredentials(t *testing.T, ca, caKey, directory, identity string) (config.ControllerInternalConfig, *tls.Config) {
	t.Helper()
	uri, err := controlplane.IdentityURI(identity)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile := filepath.Join(directory, identity+".pem"), filepath.Join(directory, identity+".key")
	if err := pki.Issue(ca, caKey, certFile, keyFile, pki.CertificateOptions{CommonName: identity, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, URIs: []*url.URL{uri}, Server: true, Client: true, Duration: time.Hour}); err != nil {
		t.Fatal(err)
	}
	cfg, err := controlplane.InternalTLS(ca, certFile, keyFile, identity)
	if err != nil {
		t.Fatal(err)
	}
	return config.ControllerInternalConfig{CAFile: ca, CertFile: certFile, KeyFile: keyFile}, cfg
}

func TestCoverageServiceLogProxyAuthenticatesExactPeerAndPreservesQuery(t *testing.T) {
	server, _, runtime, _ := managedRuntimeFixture(t)
	directory := t.TempDir()
	ca, caKey := filepath.Join(directory, "ca.pem"), filepath.Join(directory, "ca.key")
	if err := pki.InitCA(ca, caKey, "isolated log peers", time.Hour); err != nil {
		t.Fatal(err)
	}
	local, _ := coveragePeerCredentials(t, ca, caKey, directory, "local")
	_, peerTLS := coveragePeerCredentials(t, ca, caKey, directory, "peer")
	server.config.Controller.InstanceID = "local"
	server.config.Controller.Internal = local
	var calls atomic.Int32
	var status atomic.Int32
	status.Store(http.StatusOK)
	peer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		identity, err := controlplane.CertificateIdentity(r.TLS.PeerCertificates[0])
		if err != nil || identity != "local" || r.URL.Path != "/internal/v1/service-logs" || r.URL.Query().Get("runtime_id") != runtime.ID || r.URL.Query().Get("service_id") != "docker-web" || r.URL.Query().Get("tail") != "99" {
			t.Errorf("proxy changed peer identity or request: %s %v", identity, r.URL)
		}
		w.Header().Set("X-NSC-Log-TTY", "true")
		w.WriteHeader(int(status.Load()))
		_, _ = io.WriteString(w, "peer log\n")
	}))
	peer.TLS = peerTLS
	peer.StartTLS()
	defer peer.Close()
	runtime.Endpoint = "node://remote-node"
	server.config.Controller.Nodes = []config.ControllerNodeConfig{{NodeID: "different"}, {NodeID: "remote-node", InstanceID: "peer", URL: peer.URL}}
	stream, err := server.openServiceLogStream(t.Context(), runtime, "docker-web", 99)
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := io.ReadAll(stream)
	_ = stream.Close()
	if readErr != nil || string(raw) != "peer log\n" || !stream.tty || calls.Load() != 1 {
		t.Fatalf("authenticated log proxy lost stream framing: %q %v", raw, readErr)
	}
	if _, err := server.proxyServiceLogs(t.Context(), runtime, "docker-web", 99, "foreign", peer.URL); err == nil || calls.Load() != 1 {
		t.Fatal("another controller certificate gained configured peer authority")
	}
	if _, err := server.proxyServiceLogs(t.Context(), runtime, "docker-web", 99, "peer", "http://127.0.0.1:1"); err == nil || calls.Load() != 1 {
		t.Fatal("proxy accepted cleartext internal URL")
	}
	status.Store(http.StatusServiceUnavailable)
	if _, err := server.openServiceLogStream(t.Context(), runtime, "docker-web", 99); err == nil || calls.Load() != 2 {
		t.Fatal("unavailable authenticated peer returned a live stream")
	}
	runtime.Endpoint = "node://unconfigured"
	if _, err := server.openServiceLogStream(t.Context(), runtime, "docker-web", 99); err == nil {
		t.Fatal("unconfigured node was contacted")
	}
	server.config.Controller.Internal.KeyFile = filepath.Join(directory, "missing.key")
	if _, err := server.proxyServiceLogs(t.Context(), runtime, "docker-web", 99, "peer", peer.URL); err == nil {
		t.Fatal("missing client credential accepted")
	}
}

func TestCoverageInternalServiceLogsScopesAuthenticatedPeerAndService(t *testing.T) {
	server, _, runtime, _ := managedRuntimeFixture(t)
	runtime.Type, runtime.Endpoint = model.RuntimeSwarm, "node://data-node"
	coverageRuntimeSnapshot(t, server, runtime)
	service := logCoverageService(t, server, runtime, "web")
	// Scope validation must follow every service page; the owned target sorts
	// after the first 200 rows and cannot be mistaken for an unknown object.
	for index := 0; index < 200; index++ {
		logCoverageService(t, server, runtime, fmt.Sprintf("page-%03d", index))
	}
	server.config.Controller.InstanceID = "local"
	server.config.Controller.Nodes = []config.ControllerNodeConfig{{NodeID: "remote-node", InstanceID: "peer"}}
	uri, _ := controlplane.IdentityURI("peer")
	cert := &x509.Certificate{URIs: []*url.URL{uri}}
	request := func(method, peer, query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/internal/v1/service-logs?"+query, nil)
		peerURI, _ := controlplane.IdentityURI(peer)
		peerCert := &x509.Certificate{URIs: []*url.URL{peerURI}}
		r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{cert}}, PeerCertificates: []*x509.Certificate{peerCert}}
		w := httptest.NewRecorder()
		server.InternalServiceLogs().ServeHTTP(w, r)
		return w
	}
	base := "runtime_id=" + runtime.ID + "&service_id=" + service.RuntimeObjectID
	for _, scenario := range []struct {
		method, peer, query string
		status              int
	}{
		{http.MethodPost, "peer", base + "&tail=20", http.StatusForbidden},
		{http.MethodGet, "unknown", base + "&tail=20", http.StatusForbidden},
		{http.MethodGet, "peer", "runtime_id=missing&tail=20", http.StatusNotFound},
		{http.MethodGet, "peer", "runtime_id=" + runtime.ID + "&service_id=foreign&tail=20", http.StatusNotFound},
		{http.MethodGet, "peer", base + "&tail=bad", http.StatusBadRequest},
		{http.MethodGet, "peer", base + "&tail=0", http.StatusBadRequest},
		{http.MethodGet, "peer", base + "&tail=1001", http.StatusBadRequest},
		{http.MethodGet, "peer", base + "&tail=20", http.StatusForbidden},
	} {
		response := request(scenario.method, scenario.peer, scenario.query)
		if response.Code != scenario.status || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("internal scope boundary: %s got %d want %d", scenario.query, response.Code, scenario.status)
		}
	}
	server.config.Controller.Nodes = append(server.config.Controller.Nodes, config.ControllerNodeConfig{NodeID: "data-node", InstanceID: "local"})
	if response := request(http.MethodGet, "peer", base+"&tail=20"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("node without current ownership opened host Docker stream: %d", response.Code)
	}
	badIdentity := httptest.NewRequest(http.MethodGet, "/internal/v1/service-logs?"+base, nil)
	badIdentity.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{cert}}, PeerCertificates: []*x509.Certificate{{}}}
	response := httptest.NewRecorder()
	server.InternalServiceLogs().ServeHTTP(response, badIdentity)
	if response.Code != http.StatusForbidden {
		t.Fatal("authenticated certificate without controller identity accepted")
	}
	runtime.Enabled = false
	coverageRuntimeSnapshot(t, server, runtime)
	if response := request(http.MethodGet, "peer", base+"&tail=20"); response.Code != http.StatusNotFound {
		t.Fatal("disabled runtime still allowed peer log access")
	}
}

type coverageNonOwnershipStore struct{ storage.Store }

func TestCoverageLocalServiceLogsRequireWellFormedOwnershipAndCredentials(t *testing.T) {
	server, _, runtime, _ := managedRuntimeFixture(t)
	binding := config.ControllerNodeConfig{NodeID: "data-node", InstanceID: "local"}
	if err := server.verifyLocalLogNode(t.Context(), binding); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing session was not rejected: %v", err)
	}
	db := server.store.(*storage.SQLStore).DB()
	now := time.Now().UTC()
	for _, doc := range []struct {
		kind, parent, payload string
		valid                 bool
	}{
		{"wrong", binding.NodeID, `{"owner":1}`, false},
		{"node-session", "another-node", `{"owner":1}`, false},
		{"node-session", binding.NodeID, `{`, false},
		{"node-session", binding.NodeID, `{"owner":0}`, false},
		{"node-session", binding.NodeID, `{"owner":1}`, true},
	} {
		if _, err := db.ExecContext(t.Context(), `INSERT INTO control_documents(id,kind,parent_id,payload,revision,created_at,updated_at) VALUES(?,?,?,?,1,?,?) ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,parent_id=excluded.parent_id,payload=excluded.payload`, "node-session:"+binding.NodeID, doc.kind, doc.parent, doc.payload, now, now); err != nil {
			t.Fatal(err)
		}
		err := server.verifyLocalLogNode(t.Context(), binding)
		if (err == nil) != doc.valid {
			t.Fatalf("ownership proof %s %s admitted=%t: %v", doc.kind, doc.parent, err == nil, err)
		}
	}
	server.config.Controller.InstanceID = "local"
	server.config.Controller.Nodes = []config.ControllerNodeConfig{{NodeID: "other", InstanceID: "local"}, binding}
	runtime.Endpoint = "node://data-node"
	if got := server.localLogBinding(runtime); got == nil || got.NodeID != binding.NodeID {
		t.Fatal("local ownership watcher did not bind the selected node")
	}
	runtime.Endpoint = "node://remote-node"
	if got := server.localLogBinding(runtime); got != nil {
		t.Fatal("local watcher claimed another node")
	}
	runtime.Endpoint, runtime.CredentialRef = "unix:///nonexistent/nsc-fixture.sock", "missing"
	if _, err := server.openDockerServiceLogs(t.Context(), runtime, "web", 20, nil); err == nil {
		t.Fatal("missing Docker credential admitted")
	}
	server.config.Credentials["missing"] = config.DockerCredential{CAFile: "missing-ca"}
	if _, err := server.openDockerServiceLogs(t.Context(), runtime, "web", 20, nil); err == nil {
		t.Fatal("incomplete Docker TLS credential admitted")
	}
	runtime.Endpoint = "node://data-node"
	if _, err := server.openServiceLogStream(t.Context(), runtime, "web", 20); err == nil {
		t.Fatal("bound local node without complete credentials opened Docker")
	}
	server.store = coverageNonOwnershipStore{Store: server.store}
	if err := server.verifyLocalLogNode(t.Context(), binding); err == nil {
		t.Fatal("store without independent ownership verification admitted local logs")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	server.watchLogNodeOwnership(ctx, cancel, binding)
}

func TestCoverageServiceLogOwnershipWatcherCancelsLostNodeSession(t *testing.T) {
	server, _, _, _ := managedRuntimeFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan struct{})
	go func() {
		server.watchLogNodeOwnership(ctx, cancel, config.ControllerNodeConfig{NodeID: "lost-node"})
		close(finished)
	}()
	select {
	case <-ctx.Done():
	case <-time.After(8 * time.Second):
		t.Fatal("missing node session did not terminate the authorized log stream")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("ownership watcher leaked after losing authority")
	}
}

type coverageFailLogResponse struct{ header http.Header }

func (w coverageFailLogResponse) Header() http.Header       { return w.header }
func (w coverageFailLogResponse) WriteHeader(int)           {}
func (w coverageFailLogResponse) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (w coverageFailLogResponse) Flush()                    {}

func TestCoverageServiceLogSSEReportsMalformedFramesAndClosesFailedConsumer(t *testing.T) {
	for _, data := range [][]byte{coverageLogFrame(3, "invalid stream\n"), coverageLogFrame(1, strings.Repeat("x", maxServiceLogLine+1)+"\n")} {
		reader := &trackedLogReader{ReadCloser: io.NopCloser(bytes.NewReader(data)), closed: make(chan struct{})}
		response := httptest.NewRecorder()
		serveServiceLogStream(t.Context(), response, serviceLogStream{ReadCloser: reader})
		if !strings.Contains(response.Body.String(), "event: error") || !strings.Contains(response.Body.String(), "service.logs_interrupted") {
			t.Fatalf("malformed framing appeared complete: %s", response.Body)
		}
		select {
		case <-reader.closed:
		default:
			t.Fatal("failed frame reader remained open")
		}
	}
	reader := &trackedLogReader{ReadCloser: io.NopCloser(strings.NewReader("ready\n")), closed: make(chan struct{})}
	serveServiceLogStream(t.Context(), coverageFailLogResponse{header: make(http.Header)}, serviceLogStream{ReadCloser: reader, tty: true})
	select {
	case <-reader.closed:
	default:
		t.Fatal("failed client left stream open")
	}
}
