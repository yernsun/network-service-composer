package api

import (
	"errors"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEntrypointHTTPProtectsMutationsAndKeepsReadsUncached(t *testing.T) {
	server, handler, runtime, _ := managedRuntimeFixture(t)
	_, op, _, err := server.core.InstallApplication(t.Context(), core.InstallApplicationInput{ApplicationPreflightInput: core.ApplicationPreflightInput{RuntimeID: runtime.ID, ComposeYAML: "services:\n  web:\n    image: example.test/web:fixed\n", ConfigurationSource: model.DeploymentSource{Entrypoint: &model.ServiceEntrypoint{Service: "web", Host: "http-entry.example.test", Port: 8080, GatewayID: "fixture"}}}, Name: "Entrypoint", ProjectName: "entry-api", IdempotencyKey: "entry-api"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/operations/" + op.ID + "/entrypoint"
	response := managementRequest(t, handler, http.MethodGet, path, nil, nil)
	status := responseData[core.ServiceEntrypointStatus](t, response, http.StatusOK)
	if status.Status != "PENDING" || status.IntentID == "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(status, response.Header())
	}
	if _, err = server.store.GetControlDocument(t.Context(), status.IntentID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("GET bootstrapped intent state", err)
	}
	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/api/v1"+path, nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatal("anonymous ingress read", anonymous.Code)
	}
	user, err := server.core.SaveUser(t.Context(), "", core.UserInput{Username: "ingress-viewer", Role: "viewer", Enabled: true, Password: "ingress-password-fixture"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	token, err := server.core.RotateUserToken(t.Context(), user.ID, user.Revision)
	if err != nil {
		t.Fatal(err)
	}
	viewer := managementRequest(t, handler, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + token})
	responseData[core.ServiceEntrypointStatus](t, viewer, http.StatusOK)
	denied := managementRequest(t, handler, http.MethodPost, path+"/retry", map[string]string{}, map[string]string{"Authorization": "Bearer " + token, "If-Match": `"rev-0"`, "Idempotency-Key": "viewer-retry"})
	if denied.Code != http.StatusForbidden {
		t.Fatal("viewer ingress mutation", denied.Code)
	}
	for _, secret := range []string{token, "ingress-password-fixture", "encrypted_input", "encryption_key", "unix:///"} {
		if strings.Contains(viewer.Body.String(), secret) {
			t.Fatal("confidential ingress read", secret)
		}
	}
}
