package install

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func consoleFixture(t *testing.T) (*Installer, *consoleMode) {
	t.Helper()
	i, _ := installerFixture(t, 1)
	i.Config.Nodes[0].Address = "127.0.0.1"
	i.Record.ConfigDigest = bootstrap.Digest(i.Config)
	infrastructure(t, i)
	if err := i.initializeDatabase(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := i.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error {
		return store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: storage.ControlDocument{ID: i.Record.Nodes["one"].SandboxID + ":sandbox-status", Kind: "sandbox-status", ParentID: i.Record.PoolID, Payload: json.RawMessage(`{}`), CreatedAt: timeNow(), UpdatedAt: timeNow()}}})
	}); err != nil {
		t.Fatal(err)
	}
	sharedMode := &consoleMode{}
	sharedMode.Store("")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode := sharedMode.Load()
		if r.URL.Path == "/login" {
			if mode == "login" {
				w.WriteHeader(503)
			} else {
				w.WriteHeader(200)
			}
			return
		}
		if mode == "denied" {
			w.WriteHeader(403)
			w.Write([]byte(`{"secret":"must-not-escape"}`))
			return
		}
		if mode == "malformed" {
			w.Write([]byte("malformed"))
			return
		}
		if mode == "redirect" {
			w.Header().Set("Location", "https://untrusted.example.invalid")
			w.WriteHeader(302)
			return
		}
		if mode == "malformed-envelope" {
			w.Write([]byte(`{"data":`))
			return
		}
		var value any
		switch {
		case r.URL.Path == "/api/v1/auth/login":
			if mode == "session-http-error" {
				w.WriteHeader(401)
				return
			}
			if mode != "session-cookie-missing" {
				name := "nsc_session"
				if mode == "session-cookie-foreign" {
					name = "foreign"
				}
				http.SetCookie(w, &http.Cookie{Name: name, Value: "isolated-fixture-session", Secure: mode != "session-cookie-insecure", HttpOnly: mode != "session-cookie-readable"})
			}
			role := "administrator"
			if mode == "session-viewer" {
				role = "viewer"
			}
			value = map[string]any{"authenticated": mode != "session-guest", "role": role}
		case r.URL.Path == "/api/v1/system/ha":
			state := "READY"
			if mode == "ha" {
				state = "UNKNOWN"
			}
			value = core.HAOverview{Layers: []core.HALayer{{Name: "controller", State: state, Evidence: []core.HAEvidence{{State: state}}}, {Name: "swarm_quorum", State: state}}}
		case strings.HasPrefix(r.URL.Path, "/api/v1/applications"):
			apps := []model.Application{}
			for _, key := range []string{"controller", "postgresql", "nginx", "nginx-sandbox"} {
				if mode == "apps" && key == "postgresql" {
					continue
				}
				apps = append(apps, model.Application{ID: key, RuntimeID: i.Record.RuntimeID, System: true, SystemKey: key})
			}
			if mode == "application-foreign-runtime" {
				apps[0].RuntimeID = "foreign"
			}
			if mode == "application-managed" {
				apps[0].Managed = true
			}
			if mode == "application-observed" {
				apps[0].System = false
			}
			if mode == "application-unknown-component" {
				apps[0].SystemKey = "external"
			}
			if strings.HasPrefix(mode, "pagination") && (r.URL.Query().Get("after") == "" || mode == "pagination-stalled") {
				ordinary := make([]model.Application, 200)
				for n := range ordinary {
					ordinary[n] = model.Application{ID: fmt.Sprintf("ordinary-%03d", n), RuntimeID: i.Record.RuntimeID}
				}
				if mode == "pagination-many-system" {
					copy(ordinary, apps)
				}
				if mode == "pagination-empty-cursor" {
					ordinary[199].ID = ""
				}
				apps = ordinary
			}
			value = apps
		case r.URL.Path == "/api/v1/services":
			key := r.URL.Query().Get("application_id")
			if mode == "services-denied" {
				w.WriteHeader(403)
				return
			}
			service := model.Service{RuntimeID: i.Record.RuntimeID, ApplicationID: key, RuntimeObjectID: i.seedRecord().Components[key].ID, System: true}
			switch mode {
			case "service-foreign-runtime":
				service.RuntimeID = "foreign"
			case "service-foreign-application":
				service.ApplicationID = "foreign"
			case "service-foreign-object":
				service.RuntimeObjectID = "foreign"
			case "service-observed":
				service.System = false
			case "service-managed":
				service.Managed = true
			}
			value = []model.Service{service}
		case strings.HasSuffix(r.URL.Path, "/evidence"):
			at := time.Now()
			if mode == "stale" {
				at = at.Add(-time.Hour)
			}
			if mode == "future" {
				at = at.Add(time.Hour)
			}
			evidence := model.GatewayEvidence{ConfirmedRevision: 1, LoadingConfirmed: mode != "loading", ConfigurationState: "CONFIRMED", StartupState: "CONFIRMED", ObservedAt: at, ReceivedAt: at, ModelDigest: "model-digest", RenderedDigest: "rendered-digest"}
			switch mode {
			case "revision-mismatch":
				evidence.ConfirmedRevision = 2
			case "configuration-unknown":
				evidence.ConfigurationState = "UNKNOWN"
			case "startup-unknown":
				evidence.StartupState = "UNKNOWN"
			case "received-stale":
				evidence.ReceivedAt = at.Add(-time.Hour)
			case "model-empty":
				evidence.ModelDigest = ""
			case "model-mismatch":
				evidence.ModelDigest = "other"
			case "rendered-empty":
				evidence.RenderedDigest = ""
			case "rendered-mismatch":
				evidence.RenderedDigest = "other"
			}
			value = evidence
		case strings.HasPrefix(r.URL.Path, "/api/v1/gateways/"):
			at := time.Now()
			revision := int64(1)
			if mode == "zero" {
				revision = 0
			}
			gateway := model.Gateway{ID: i.Record.Nodes["one"].GatewayID, PoolID: i.Record.PoolID, Status: model.GatewayReady, DesiredRevision: revision, AppliedRevision: revision, LastSeenAt: &at, ModelDigest: "model-digest", RenderedDigest: "rendered-digest"}
			switch mode {
			case "gateway-id":
				gateway.ID = "foreign"
				gateway.Status = model.GatewayOffline
			case "gateway-foreign-pool":
				gateway.PoolID = "foreign"
			case "gateway-offline":
				gateway.Status = model.GatewayOffline
			case "gateway-lag":
				gateway.AppliedRevision = 0
			case "gateway-no-heartbeat":
				gateway.LastSeenAt = nil
			case "gateway-old-heartbeat":
				at = at.Add(-time.Hour)
			}
			value = gateway
		case strings.HasSuffix(r.URL.Path, "/draft"):
			revision := int64(1)
			if mode == "validation-draft-changed" {
				revision = 2
			}
			value = map[string]int64{"revision": revision}
		case strings.HasSuffix(r.URL.Path, "/validations"):
			if r.Header.Get("If-Match") != `"rev-1"` {
				t.Error("validation is not bound to draft revision")
			}
			value = map[string]string{"id": "validation-receipt"}
		case strings.HasSuffix(r.URL.Path, "/validations/validation-receipt"):
			status := "SUCCEEDED"
			switch mode {
			case "validation":
				status = "FAILED"
			case "validation-pending":
				status = "RUNNING"
			case "validation-expired":
				status = "EXPIRED"
			case "validation-cancelled":
				status = "CANCELLED"
			}
			validation := core.NginxValidation{ControlDocument: storage.ControlDocument{ID: "validation-receipt"}, PoolID: i.Record.PoolID, DraftRevision: 1, SandboxID: i.Record.Nodes["one"].SandboxID, RendererVersion: nginxx.RendererVersion, ExpiresAt: timeNow().Add(time.Minute), Status: status, Results: map[string]core.NginxValidationResult{i.Record.Nodes["one"].SandboxID: {Status: "SUCCEEDED", RenderedDigest: "sandbox-rendered"}}}
			switch mode {
			case "validation-foreign-id":
				validation.ID = "foreign"
			case "validation-foreign-pool":
				validation.PoolID = "foreign"
			case "validation-foreign-sandbox":
				validation.SandboxID = "foreign"
			case "validation-renderer":
				validation.RendererVersion = "foreign"
			case "validation-source":
				validation.DraftRevision = 2
			case "validation-no-result":
				validation.Results = map[string]core.NginxValidationResult{}
			case "validation-result-pending":
				validation.Results[validation.SandboxID] = core.NginxValidationResult{Status: "RUNNING"}
			case "validation-no-digest":
				validation.Results[validation.SandboxID] = core.NginxValidationResult{Status: "SUCCEEDED"}
			case "validation-outdated":
				validation.ExpiresAt = timeNow().Add(-time.Minute)
			}
			value = validation
		default:
			t.Error("unexpected console request", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": value})
	})
	server := httptest.NewUnstartedServer(handler)
	pair, err := tls.LoadX509KeyPair(i.material("console.crt"), i.material("console.key"))
	if err != nil {
		t.Fatal(err)
	}
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	i.Config.ConsolePort, _ = strconv.Atoi(port)
	return i, sharedMode
}

func TestVerificationRequiresFreshPositiveLoadedEvidence(t *testing.T) {
	i, mode := consoleFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Server", "nginx"); w.WriteHeader(404) }))
	t.Cleanup(server.Close)
	i.GatewayClient = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}}, Timeout: time.Second}
	if err := i.verifyHandoff(t.Context()); err != nil {
		t.Fatal("complete handoff rejected", err)
	}
	var err error
	for _, scenario := range []string{"ha", "apps", "zero", "loading", "stale", "future", "denied", "malformed", "login"} {
		mode.Store(scenario)
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		if err = i.verifyHandoff(ctx); err == nil {
			t.Fatal("incomplete handoff passed", scenario)
		}
		cancel()
	}
	mode.Store("denied")
	if err = i.api(t.Context(), "GET", "/system/ha", nil, nil, nil); err == nil || strings.Contains(err.Error(), "must-not-escape") {
		t.Fatal("private API diagnostics escaped", err)
	}
	mode.Store("")
	if err = i.api(t.Context(), "GET", "/system/ha", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = i.api(t.Context(), "POST", "/system/ha", make(chan int), nil, nil); err == nil {
		t.Fatal("invalid request data accepted")
	}
	if err = i.Verify(t.Context(), errorWriter{}); err == nil {
		t.Fatal("output error discarded")
	}
	if err = os.Remove(i.material("console.crt")); err != nil {
		t.Fatal(err)
	}
	if _, err = i.apiClient(); err == nil {
		t.Fatal("missing trust accepted")
	}
	os.WriteFile(i.material("console.crt"), []byte("invalid"), 0o600)
	if _, err = i.apiClient(); err == nil {
		t.Fatal("invalid trust accepted")
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("output unavailable") }

func TestSandboxValidationReceiptAndTerminalFailure(t *testing.T) {
	i, mode := consoleFixture(t)
	if err := i.verifyValidation(t.Context()); err == nil {
		t.Fatal("missing validation receipt accepted")
	}
	receipt, err := i.createValidation(t.Context())
	if err != nil || receipt.ID != "validation-receipt" {
		t.Fatal(err)
	}
	i.Record.Resources["validation:bootstrap"] = receipt
	if err = i.verifyValidation(t.Context()); err != nil {
		t.Fatal(err)
	}
	mode.Store("validation")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err = i.verifyValidation(ctx); err == nil {
		t.Fatal("failed validation accepted")
	}
	mode.Store("denied")
	ctx2, cancel2 := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel2()
	if _, err = i.Apply(ctx2, bootstrap.Step{Key: "controller-ready"}); err == nil {
		t.Fatal("failed read-only verification accepted")
	}
	if _, err = i.Reconcile(t.Context(), bootstrap.Step{Key: "controller-ready", State: bootstrap.Unknown}); err == nil {
		t.Fatal("interrupted verification silently claimed success")
	}
	if _, err = i.Reconcile(t.Context(), bootstrap.Step{Key: "validation-ready", State: bootstrap.Unknown}); err == nil {
		t.Fatal("interrupted validation silently claimed success")
	}
}

func TestRotatedVerificationCredentialIsTransientAndPrivate(t *testing.T) {
	i, _ := installerFixture(t, 1)
	if err := i.makeMaterials(); err != nil {
		t.Fatal(err)
	}
	initial, err := i.apiCredential()
	if err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(t.TempDir(), "current-admin")
	if err = os.WriteFile(current, []byte("isolated-current-password-after-rotation"), 0600); err != nil {
		t.Fatal(err)
	}
	i.CredentialFile = current
	actual, err := i.apiCredential()
	if err != nil || string(actual) == string(initial) {
		t.Fatal("current credential not selected", err)
	}
	saved, _ := os.ReadFile(i.material("admin-password"))
	if string(saved) != string(initial) {
		t.Fatal("original recovery material changed")
	}
	os.Chmod(current, 0644)
	if _, err = i.apiCredential(); err == nil {
		t.Fatal("public verification credential accepted")
	}
}

type consoleMode struct{ value atomic.Value }

func (m *consoleMode) Store(value string) { m.value.Store(value) }
func (m *consoleMode) Load() string       { return m.value.Load().(string) }
