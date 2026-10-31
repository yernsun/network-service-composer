package install

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// A trusted TLS server can still return wrong or stale evidence. Exercise each
// independent required proof: transport authentication alone never means loaded.
func TestAuthenticatedButIncompleteEvidenceNeverCompletesInstallation(t *testing.T) {
	i, mode := consoleFixture(t)
	for _, scenario := range []string{"gateway-id", "gateway-foreign-pool", "gateway-offline", "gateway-lag", "gateway-no-heartbeat", "gateway-old-heartbeat", "revision-mismatch", "configuration-unknown", "startup-unknown", "received-stale", "model-empty", "model-mismatch", "rendered-empty", "rendered-mismatch", "session-viewer", "session-guest", "session-cookie-missing", "session-cookie-insecure", "session-cookie-foreign", "session-cookie-readable", "session-http-error", "validation-pending", "validation-expired", "validation-cancelled", "validation-foreign-id", "validation-foreign-pool", "validation-foreign-sandbox", "validation-renderer", "validation-source", "validation-no-result", "validation-result-pending", "validation-no-digest", "validation-outdated", "validation-draft-changed"} {
		t.Run(scenario, func(t *testing.T) {
			mode.Store(scenario)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Millisecond)
			defer cancel()
			var err error
			switch {
			case strings.HasPrefix(scenario, "session-"):
				err = i.verifyLogin(ctx)
			case strings.HasPrefix(scenario, "validation-"):
				i.Record.Resources["validation:bootstrap"] = bootstrap.Resource{ID: "validation-receipt", SpecDigest: i.validationDigest(1)}
				err = i.verifyValidation(ctx)
			default:
				err = i.verifyGateways(ctx)
			}
			if err == nil {
				t.Fatal("incomplete authenticated proof accepted")
			}
		})
	}
	mode.Store("")
	if err := i.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error {
		id := i.Record.Nodes["one"].SandboxID + ":sandbox-status"
		old, err := store.GetControlDocument(t.Context(), id)
		if err != nil {
			return err
		}
		old.UpdatedAt = timeNow().Add(-time.Hour)
		return store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: old, Expected: old.Revision}})
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if err := i.verifyGateways(ctx); err == nil {
		t.Fatal("stale Sandbox heartbeat accepted")
	}
}

func TestHandoffRejectsWrongHTTPOriginAndReportsOnlyVerifiedState(t *testing.T) {
	i, _ := consoleFixture(t)
	status := 200
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "external")
		w.WriteHeader(status)
	}))
	defer origin.Close()
	i.GatewayClient = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, origin.Listener.Addr().String())
	}}}
	for _, value := range []int{200, 404} {
		status = value
		if err := i.verifyHandoff(t.Context()); err == nil {
			t.Fatal("external HTTP origin reported as Gateway")
		}
	}
	origin.Close()
	if err := i.verifyHandoff(t.Context()); err == nil {
		t.Fatal("unreachable Gateway access reported successful")
	}
	i.GatewayClient = nil
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if err := i.verifyHandoff(ctx); err == nil {
		t.Fatal("unreachable default Gateway access succeeded")
	}
	if err := i.Handoff(errorWriter{}); err == nil {
		t.Fatal("handoff output failure ignored")
	}
	os.WriteFile(i.material("console.crt"), []byte("not a certificate"), 0600)
	if err := i.Handoff(&strings.Builder{}); err == nil {
		t.Fatal("invalid certificate fingerprint printed")
	}
	os.Remove(i.material("console.crt"))
	if err := i.Handoff(&strings.Builder{}); err == nil {
		t.Fatal("missing certificate accepted")
	}
}

func TestVerificationNeverTrustsMalformedBodiesOrMissingPrivateCredentials(t *testing.T) {
	i, mode := consoleFixture(t)
	mode.Store("malformed-envelope")
	var result any
	if err := i.api(t.Context(), "GET", "/system/ha", nil, &result, nil); err == nil {
		t.Fatal("malformed envelope accepted")
	}
	mode.Store("redirect")
	if err := i.api(t.Context(), "GET", "/system/ha", nil, &result, nil); err == nil {
		t.Fatal("redirect followed with administrator credential")
	}
	mode.Store("")
	os.Remove(i.material("admin-password"))
	if err := i.verifyLogin(t.Context()); err == nil {
		t.Fatal("missing login credential accepted")
	}
	if err := i.api(t.Context(), "GET", "/system/ha", nil, nil, nil); err == nil {
		t.Fatal("missing API credential accepted")
	}
	os.Remove(i.material("console.crt"))
	if err := i.verifyLogin(t.Context()); err == nil {
		t.Fatal("missing login trust accepted")
	}
	if err := i.api(t.Context(), "GET", "/system/ha", nil, nil, nil); err == nil {
		t.Fatal("missing API trust accepted")
	}
}

func TestPreviouslyCompletedHealthChecksRequireFreshEvidenceOnResume(t *testing.T) {
	i, mode := consoleFixture(t)
	for _, key := range []string{"controller-ready", "gateways-ready", "validation:bootstrap", "validation-ready"} {
		r, err := i.Apply(t.Context(), bootstrap.Step{Key: key})
		if err != nil {
			t.Fatal(err)
		}
		i.Record.Resources[key] = r
		if key == "validation:bootstrap" {
			continue
		}
		if err = i.Check(t.Context(), bootstrap.Step{Key: key, Receipt: &r, State: bootstrap.Succeeded}); err != nil {
			t.Fatal("fresh receipt refused", err)
		}
		mode.Store("denied")
		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
		err = i.Check(ctx, bootstrap.Step{Key: key, Receipt: &r, State: bootstrap.Succeeded})
		cancel()
		if err == nil {
			t.Fatal("stale cached health receipt trusted on resume", key)
		}
		mode.Store("")
	}
}

func TestSystemApplicationPaginationAndExactServiceOwnership(t *testing.T) {
	i, mode := consoleFixture(t)
	for _, success := range []string{"pagination", "pagination-many-system"} {
		mode.Store(success)
		if err := i.verifyController(t.Context()); err != nil {
			t.Fatal("valid paged system registration refused", err)
		}
	}
	for _, failure := range []string{"pagination-stalled", "pagination-empty-cursor", "application-foreign-runtime", "application-managed", "application-observed", "application-unknown-component", "service-foreign-runtime", "service-foreign-application", "service-foreign-object", "service-observed", "service-managed", "services-denied"} {
		mode.Store(failure)
		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
		err := i.verifyController(ctx)
		cancel()
		if err == nil {
			t.Fatal("unverified application/service binding accepted", failure)
		}
	}
}

func TestCorruptAndMissingSandboxIdentityEvidenceCannotBeHealthy(t *testing.T) {
	i, _ := consoleFixture(t)
	for _, scenario := range []string{"kind", "pool", "missing"} {
		if err := i.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error {
			raw := store.(*unitSeedStore).DB()
			id := i.Record.Nodes["one"].SandboxID + ":sandbox-status"
			query := "UPDATE control_documents SET kind='sandbox-status',parent_id=? WHERE id=?"
			if _, err := raw.Exec(query, i.Record.PoolID, id); err != nil {
				return err
			}
			switch scenario {
			case "kind":
				_, err := raw.Exec("UPDATE control_documents SET kind='foreign' WHERE id=?", id)
				return err
			case "pool":
				_, err := raw.Exec("UPDATE control_documents SET parent_id='foreign' WHERE id=?", id)
				return err
			default:
				_, err := raw.Exec("DELETE FROM control_documents WHERE id=?", id)
				return err
			}
		}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
		err := i.verifyGateways(ctx)
		cancel()
		if err == nil {
			t.Fatal("invalid Sandbox ownership evidence accepted", scenario)
		}
	}
}
