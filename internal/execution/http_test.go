package execution

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/pki"
)

func TestExecutionInternalEndpointRejectsCommandAndAuthorityInjection(t *testing.T) {
	for _, scenario := range []string{"no-tls", "unverified", "gateway", "method", "command", "trailing", "oversize", "term", "target", "operation", "peer"} {
		t.Run(scenario, func(t *testing.T) {
			f := newExecutionFixture(t)
			if err := Save(t.Context(), f.store, Record{Reference: f.ref, State: "SELECTED", SelectedAt: f.now}, f.now); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int64
			s := &Service{Verifier: f.v, Locker: f.locks, Execute: func(context.Context, model.DeploymentOperation, model.Runtime) error { calls.Add(1); return nil }}
			request := Request{OperationID: f.op.ID, Attempt: f.op.Attempt, LeaderTerm: 3, Target: f.ref.Target.Identity()}
			method := http.MethodPost
			want := http.StatusBadRequest
			switch scenario {
			case "method":
				method = http.MethodGet
				want = http.StatusMethodNotAllowed
			case "no-tls", "unverified", "gateway":
				want = http.StatusForbidden
			case "term":
				request.LeaderTerm--
				want = http.StatusConflict
			case "target":
				request.Target.NodeID = "node-b"
				want = http.StatusConflict
			case "operation":
				request.OperationID = "missing"
				want = http.StatusConflict
			case "peer":
				want = http.StatusConflict
			}
			body, _ := json.Marshal(request)
			text := string(body)
			switch scenario {
			case "command":
				text = strings.TrimSuffix(text, "}") + `,"command":["docker","rm","anything"]}`
			case "trailing":
				text += " {}"
			case "oversize":
				text = strings.Repeat(" ", 9<<10) + text
			}
			r := httptest.NewRequest(method, InternalPath, strings.NewReader(text))
			uri, _ := controlplane.IdentityURI("controller-a")
			if scenario == "peer" {
				uri, _ = controlplane.IdentityURI("controller-c")
			}
			if scenario == "gateway" {
				uri, _ = url.Parse("spiffe://nsc.local/gateway-pools/pool/gateways/gateway")
			}
			cert := &x509.Certificate{URIs: []*url.URL{uri}}
			r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert}}}
			if scenario == "no-tls" {
				r.TLS = nil
			} else if scenario == "unverified" {
				r.TLS.VerifiedChains = nil
			}
			response := httptest.NewRecorder()
			s.Handler(t.Context()).ServeHTTP(response, r)
			s.Wait()
			if response.Code != want || calls.Load() != 0 || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("unsafe internal execution request", response.Code, want, calls.Load())
			}
		})
	}
}

func executionPeerTLS(t *testing.T) func(string) *tls.Config {
	t.Helper()
	dir := t.TempDir()
	ca, key := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	if err := pki.InitCA(ca, key, "isolated execution CA", time.Hour); err != nil {
		t.Fatal(err)
	}
	return func(id string) *tls.Config {
		t.Helper()
		identity, err := controlplane.IdentityURI(id)
		if err != nil {
			t.Fatal(err)
		}
		cert, private := filepath.Join(dir, id+".pem"), filepath.Join(dir, id+".key")
		if err = pki.Issue(ca, key, cert, private, pki.CertificateOptions{CommonName: id, URIs: []*url.URL{identity}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, Client: true, Server: true, Duration: time.Hour}); err != nil {
			t.Fatal(err)
		}
		cfg, err := controlplane.InternalTLS(ca, cert, private, id)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
}

func TestExecutionMTLSAcceptanceReturnsBeforeWorkAndRetriesDoNotReplay(t *testing.T) {
	f := newExecutionFixture(t)
	if err := Save(t.Context(), f.store, Record{Reference: f.ref, State: "SELECTED", SelectedAt: f.now}, f.now); err != nil {
		t.Fatal(err)
	}
	peer := executionPeerTLS(t)
	serverTLS, clientTLS := peer("controller-a"), peer("scheduler")
	// The scheduler certificate must be the durable Leader, even when it was
	// signed by the same CA as the selected node.
	var calls atomic.Int64
	entered, release := make(chan struct{}), make(chan struct{})
	s := &Service{Verifier: f.v, Locker: f.locks, Execute: func(ctx context.Context, op model.DeploymentOperation, _ model.Runtime) error {
		calls.Add(1)
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := MarkCommand(ctx); err != nil {
			return err
		}
		op.Status = model.OperationSucceeded
		return f.store.FinishDeploymentOperation(ctx, &op, f.now)
	}}
	server := httptest.NewUnstartedServer(s.Handler(t.Context()))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()
	request := Request{OperationID: f.op.ID, Attempt: f.op.Attempt, LeaderTerm: 3, Target: f.ref.Target.Identity()}
	binding := config.ControllerNodeConfig{InstanceID: "controller-a", URL: server.URL}
	if err := Dispatch(t.Context(), clientTLS, binding, request); !errors.Is(err, ErrUnknown) {
		close(release)
		s.Wait()
		t.Fatal("non-Leader certificate dispatched work", err)
	}
	clientTLS = serverTLS.Clone()
	if err := Dispatch(t.Context(), clientTLS, binding, request); err != nil {
		close(release)
		s.Wait()
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		s.Wait()
		t.Fatal("accepted operation did not run")
	}
	if err := Dispatch(t.Context(), clientTLS, binding, request); err != nil {
		close(release)
		s.Wait()
		t.Fatal("duplicate request did not observe existing acceptance", err)
	}
	close(release)
	s.Wait()
	if calls.Load() != 1 {
		t.Fatal("HTTP retry executed operation twice", calls.Load())
	}
	if _, err := f.store.GetDeploymentOperation(t.Context(), f.op.ID); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionTransportRejectsForeignServerAndRedirectWithoutProxy(t *testing.T) {
	peer := executionPeerTLS(t)
	clientTLS, serverTLS := peer("leader"), peer("controller-a")
	var calls atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "https://127.0.0.1:1/forbidden")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	request := Request{OperationID: "operation", Attempt: 1, LeaderTerm: 1}
	if err := Dispatch(t.Context(), clientTLS, config.ControllerNodeConfig{InstanceID: "controller-a", URL: server.URL}, request); !errors.Is(err, ErrUnknown) || calls.Load() != 1 {
		t.Fatal("redirect or proxy was followed", calls.Load(), err)
	}
	if err := Dispatch(t.Context(), clientTLS, config.ControllerNodeConfig{InstanceID: "foreign", URL: server.URL}, request); !errors.Is(err, ErrUnknown) || calls.Load() != 1 {
		t.Fatal("foreign node certificate accepted", calls.Load(), err)
	}
	if err := Dispatch(t.Context(), nil, config.ControllerNodeConfig{InstanceID: "controller-a", URL: server.URL}, request); !errors.Is(err, ErrFenced) {
		t.Fatal(err)
	}
}

func TestExecutionTransportFailsClosedBeforeSendingToUnprovisionedTargets(t *testing.T) {
	peer := executionPeerTLS(t)
	trusted := peer("leader")
	for _, tc := range []struct {
		name    string
		tls     *tls.Config
		binding config.ControllerNodeConfig
	}{
		{"missing-verifier", &tls.Config{}, config.ControllerNodeConfig{InstanceID: "node", URL: "https://node.example.test"}},
		{"missing-identity", trusted, config.ControllerNodeConfig{URL: "https://node.example.test"}},
		{"cleartext", trusted, config.ControllerNodeConfig{InstanceID: "node", URL: "http://node.example.test"}},
		{"invalid-address", trusted, config.ControllerNodeConfig{InstanceID: "node", URL: "https://user:secret@node.example.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Dispatch(t.Context(), tc.tls, tc.binding, Request{}); !errors.Is(err, ErrFenced) {
				t.Fatal("unprovisioned request reached transport", err)
			}
		})
	}
	var received atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1); w.WriteHeader(202) }))
	server.TLS = peer("controller-a")
	server.StartTLS()
	defer server.Close()
	reject := trusted.Clone()
	reject.VerifyConnection = func(tls.ConnectionState) error { return ErrFenced }
	if err := Dispatch(t.Context(), reject, config.ControllerNodeConfig{InstanceID: "controller-a", URL: server.URL}, Request{OperationID: "private-reference"}); !errors.Is(err, ErrUnknown) || received.Load() != 0 {
		t.Fatal("base certificate rejection leaked operation reference", received.Load(), err)
	}
}
