package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func openAPIDeploymentRequest(t *testing.T, h http.Handler, token, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := managementRequestBody(t, method, "/openapi/v1"+path, body)
	if token != "" {
		request.Header.Set("X-NSC-API-Key", token)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	return response
}

func managementRequestBody(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	var raw string
	if value, ok := body.(string); ok {
		raw = value
	} else if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		raw = string(encoded)
	}
	request := httptest.NewRequest(method, path, strings.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestOpenAPIDeploymentKeysAreIsolatedFromConsoleAndEnvironment(t *testing.T) {
	s, h, runtime, _ := managedRuntimeFixture(t)
	s.limiter.SetLimit(100000)
	s.limiter.SetBurst(100000)
	project := responseData[core.Project](t, managementRequest(t, h, http.MethodPost, "/projects", map[string]any{
		"name": "API deployment", "slug": "api-deployment", "default_runtime_id": runtime.ID,
		"initial_environment": map[string]any{"name": "prod", "kind": "production"},
	}, map[string]string{"Idempotency-Key": "api-project"}), http.StatusCreated)
	environmentID := project.InitialEnvironmentID
	if environmentID == "" {
		t.Fatal("project did not create an environment")
	}
	other := responseData[core.Environment](t, managementRequest(t, h, http.MethodPost, "/projects/"+project.ID+"/environments", map[string]string{"name": "stage"}, nil), http.StatusCreated)

	keyInput := map[string]string{"name": "pipeline", "environment_id": environmentID}
	if got := managementRequest(t, h, http.MethodPost, "/openapi-keys", keyInput, nil); got.Code != http.StatusForbidden {
		t.Fatalf("key issued without reauthentication: %d %s", got.Code, got.Body.String())
	}
	if got := managementRequest(t, h, http.MethodPost, "/openapi-keys", "{", map[string]string{"X-NSC-Reauthenticate": "test-admin-token"}); got.Code != http.StatusBadRequest {
		t.Fatal("malformed key request accepted", got.Code)
	}
	if got := managementRequest(t, h, http.MethodPost, "/openapi-keys", map[string]string{"name": "missing", "environment_id": "env_missing"}, map[string]string{"X-NSC-Reauthenticate": "test-admin-token"}); got.Code != http.StatusNotFound {
		t.Fatal("key issued for missing environment", got.Code)
	}
	type issuedKey struct {
		core.OpenAPIKey
		Token string `json:"token"`
	}
	issue := managementRequest(t, h, http.MethodPost, "/openapi-keys", keyInput, map[string]string{"X-NSC-Reauthenticate": "test-admin-token"})
	key := responseData[issuedKey](t, issue, http.StatusCreated)
	if key.Token == "" || key.EnvironmentID != environmentID || issue.Header().Get("ETag") != `"rev-1"` {
		t.Fatal("invalid issued key metadata")
	}
	if got := managementRequest(t, h, http.MethodPost, "/openapi-keys", keyInput, map[string]string{"X-NSC-Reauthenticate": "test-admin-token"}); got.Code != http.StatusConflict || strings.Contains(got.Body.String(), key.Token) {
		t.Fatalf("duplicate key name accepted or token disclosed: %d %s", got.Code, got.Body.String())
	}
	listed := responseData[[]core.OpenAPIKey](t, managementRequest(t, h, http.MethodGet, "/openapi-keys", nil, nil), http.StatusOK)
	if len(listed) != 1 || listed[0].ID != key.ID || strings.Contains(fmt.Sprint(listed), key.Token) {
		t.Fatal("key listing leaked or omitted metadata")
	}
	viewer, err := s.core.SaveUser(context.Background(), "", core.UserInput{Username: "api-viewer", Role: "viewer", Enabled: true, Password: "viewer-password-123456"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	viewerToken, err := s.core.RotateUserToken(context.Background(), viewer.ID, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if got := managementRequest(t, h, http.MethodGet, "/openapi-keys", nil, map[string]string{"Authorization": "Bearer " + viewerToken}); got.Code != http.StatusForbidden {
		t.Fatal("viewer listed deployment keys", got.Code)
	}

	resource := "/environments/" + environmentID
	if got := openAPIDeploymentRequest(t, h, "", http.MethodGet, resource, nil, nil); got.Code != http.StatusUnauthorized {
		t.Fatal("anonymous deployment API read", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, "test-admin-token", http.MethodGet, resource, nil, nil); got.Code != http.StatusUnauthorized {
		t.Fatal("user token accepted as API key", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, "nsc_api_"+strings.Repeat("0", 64), http.MethodGet, resource, nil, nil); got.Code != http.StatusUnauthorized {
		t.Fatal("unissued API key accepted", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, http.MethodGet, resource, nil, map[string]string{"Authorization": "Bearer test-admin-token"}); got.Code != http.StatusUnauthorized {
		t.Fatal("mixed credentials accepted", got.Code)
	}
	cookie, _ := browserLogin(t, h)
	if got := openAPIDeploymentRequest(t, h, key.Token, http.MethodGet, resource, nil, map[string]string{"Cookie": cookie.String()}); got.Code != http.StatusUnauthorized {
		t.Fatal("console cookie mixed with deployment key", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, http.MethodGet, "/environments/"+other.ID, nil, nil); got.Code != http.StatusNotFound {
		t.Fatal("key read another environment", got.Code)
	}
	consoleRequest := httptest.NewRequest(http.MethodGet, "/api/v1/environments/"+environmentID, nil)
	consoleRequest.Header.Set("X-NSC-API-Key", key.Token)
	consoleResponse := httptest.NewRecorder()
	h.ServeHTTP(consoleResponse, consoleRequest)
	if consoleResponse.Code != http.StatusUnauthorized {
		t.Fatal("deployment key accessed console API", consoleResponse.Code)
	}
	envResponse := openAPIDeploymentRequest(t, h, key.Token, http.MethodGet, resource, nil, nil)
	env := responseData[core.Environment](t, envResponse, http.StatusOK)
	if env.ID != environmentID || envResponse.Header().Get("ETag") == "" || envResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("environment ETag or cache boundary missing")
	}

	image := "example.test/web@sha256:" + strings.Repeat("a", 64)
	firstCompose := "services:\n  web:\n    image: " + image + "\n"
	preview := responseData[map[string]any](t, openAPIDeploymentRequest(t, h, key.Token, http.MethodPost, resource+"/preflight", map[string]string{"compose_yaml": firstCompose}, nil), http.StatusOK)
	preflightID, _ := preview["preflight_id"].(string)
	if preflightID == "" {
		t.Fatal("preflight did not return a review ID")
	}
	if operations, err := s.store.ListDeploymentOperations(context.Background(), "", storage.Page{Limit: 10}); err != nil || len(operations) != 0 {
		t.Fatal("preflight queued a deployment", err, operations)
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, http.MethodPost, "/environments/"+other.ID+"/deployments", map[string]string{"preflight_id": preflightID}, map[string]string{"If-Match": envResponse.Header().Get("ETag"), "Idempotency-Key": "wrong-environment"}); got.Code != http.StatusNotFound {
		t.Fatal("key submitted to another environment", got.Code)
	}
	confirmHeaders := map[string]string{"If-Match": envResponse.Header().Get("ETag"), "Idempotency-Key": "create-web"}
	firstResponse := openAPIDeploymentRequest(t, h, key.Token, http.MethodPost, resource+"/deployments", map[string]string{"preflight_id": preflightID}, confirmHeaders)
	first := responseData[model.DeploymentOperation](t, firstResponse, http.StatusAccepted)
	if first.Status != model.OperationQueued || firstResponse.Header().Get("Location") != "/openapi/v1/operations/"+first.ID {
		t.Fatal("deployment did not return queued operation", firstResponse.Code, firstResponse.Header())
	}
	again := responseData[model.DeploymentOperation](t, openAPIDeploymentRequest(t, h, key.Token, http.MethodPost, resource+"/deployments", map[string]string{"preflight_id": preflightID}, confirmHeaders), http.StatusAccepted)
	if again.ID != first.ID {
		t.Fatal("idempotent replay created another deployment")
	}
	status := responseData[model.DeploymentOperation](t, openAPIDeploymentRequest(t, h, key.Token, http.MethodGet, "/operations/"+first.ID, nil, nil), http.StatusOK)
	if status.Status != model.OperationQueued {
		t.Fatal("queued work reported as complete")
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, http.MethodGet, "/operations/"+id.New(id.Operation), nil, nil); got.Code != http.StatusNotFound {
		t.Fatal("missing operation read", got.Code)
	}
	bare := model.DeploymentOperation{ID: id.New(id.Operation), RuntimeID: runtime.ID, Type: "FIXTURE", Status: model.OperationQueued, Metadata: model.Metadata{Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}}
	if _, err := s.store.CreateDeploymentOperation(context.Background(), &bare); err != nil {
		t.Fatal(err)
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, http.MethodGet, "/operations/"+bare.ID, nil, nil); got.Code != http.StatusNotFound {
		t.Fatal("operation without environment revision was disclosed", got.Code)
	}

	// Simulate only the isolated worker completion so the HTTP update flow can
	// review an active baseline without a live Docker daemon.
	sqlStore := s.store.(*storage.SQLStore)
	services, err := s.store.ListAllServicesFiltered(context.Background(), storage.ServiceFilter{RuntimeID: runtime.ID, ApplicationID: first.ApplicationID})
	if err != nil || len(services) != 1 {
		t.Fatal("install did not create its managed Service placeholder", err, services)
	}
	if err := sqlStore.ActivateApplicationRevision(context.Background(), runtime.ID, first.ApplicationID, first.RevisionID, first.RevisionID, []string{services[0].ServiceKey}, nil, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlStore.DB().ExecContext(context.Background(), "UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?", first.ID); err != nil {
		t.Fatal(err)
	}
	envResponse = openAPIDeploymentRequest(t, h, key.Token, http.MethodGet, resource, nil, nil)
	env = responseData[core.Environment](t, envResponse, http.StatusOK)
	updatedCompose := "services:\n  web:\n    image: " + image + "\n    environment:\n      MODE: production\n"
	updateReview := responseData[map[string]any](t, openAPIDeploymentRequest(t, h, key.Token, http.MethodPost, resource+"/preflight", map[string]string{"compose_yaml": updatedCompose}, nil), http.StatusOK)
	updateID, _ := updateReview["preflight_id"].(string)
	if updateID == "" {
		t.Fatal("update review missing")
	}
	update := responseData[model.DeploymentOperation](t, openAPIDeploymentRequest(t, h, key.Token, http.MethodPost, resource+"/deployments", map[string]string{"preflight_id": updateID}, map[string]string{"If-Match": envResponse.Header().Get("ETag"), "Idempotency-Key": "update-web"}), http.StatusAccepted)
	if update.RevisionID == first.RevisionID || update.Status != model.OperationQueued {
		t.Fatal("update did not create a queued revision")
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, http.MethodPost, resource+"/service-preflight", map[string]string{"service_mode": "update", "compose_yaml": updatedCompose}, nil); got.Code != http.StatusBadRequest {
		t.Fatalf("Compose runtime accepted Swarm-only service update: %d %s", got.Code, got.Body.String())
	}

	otherKey, otherToken, err := s.core.IssueOpenAPIKey(context.Background(), "other", other.ID, "user_bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	if otherKey.EnvironmentID == environmentID {
		t.Fatal("fixture scope error")
	}
	if got := openAPIDeploymentRequest(t, h, otherToken, http.MethodGet, "/operations/"+first.ID, nil, nil); got.Code != http.StatusNotFound {
		t.Fatal("foreign key read operation", got.Code)
	}
	if got := managementRequest(t, h, http.MethodDelete, "/openapi-keys/"+key.ID, nil, map[string]string{"If-Match": `"rev-2"`, "X-NSC-Reauthenticate": "test-admin-token"}); got.Code != http.StatusPreconditionFailed {
		t.Fatal("stale key revocation succeeded", got.Code)
	}
	if got := managementRequest(t, h, http.MethodDelete, "/openapi-keys/"+key.ID, nil, map[string]string{"If-Match": `"rev-1"`}); got.Code != http.StatusForbidden {
		t.Fatal("key revoked without reauthentication", got.Code)
	}
	if got := managementRequest(t, h, http.MethodDelete, "/openapi-keys/"+key.ID, nil, map[string]string{"X-NSC-Reauthenticate": "test-admin-token"}); got.Code != http.StatusPreconditionRequired {
		t.Fatal("key revoked without ETag", got.Code)
	}
	if got := managementRequest(t, h, http.MethodDelete, "/openapi-keys/"+key.ID, nil, map[string]string{"If-Match": `"rev-1"`, "X-NSC-Reauthenticate": "test-admin-token"}); got.Code != http.StatusNoContent {
		t.Fatal("key revocation failed", got.Code, got.Body.String())
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, http.MethodGet, resource, nil, nil); got.Code != http.StatusUnauthorized || strings.Contains(got.Body.String(), key.Token) {
		t.Fatal("revoked key remained active or was disclosed", got.Code)
	}
}
