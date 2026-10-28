package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type issuedProjectKey struct {
	core.OpenAPIKey
	Token string `json:"token"`
}

func issueProjectKey(t *testing.T, h http.Handler, owner, name, project string, scopes ...string) issuedProjectKey {
	t.Helper()
	return responseData[issuedProjectKey](t, managementRequest(t, h, http.MethodPost, "/users/"+owner+"/api-keys", map[string]any{
		"name": name, "project_id": project, "scopes": scopes,
	}, map[string]string{"X-NSC-Reauthenticate": "test-admin-token"}), http.StatusCreated)
}

func bearerUserRequest(t *testing.T, h http.Handler, token, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := managementRequestBody(t, method, "/api/v1"+path, body)
	r.Header.Set("Authorization", "Bearer "+token)
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSelfPasswordChangeRefreshesSessionAndRetainsProjectKeys(t *testing.T) {
	s, h, runtime, _ := managedRuntimeFixture(t)
	s.limiter.SetLimit(100000)
	s.limiter.SetBurst(100000)
	ctx := context.Background()
	project, err := s.core.CreateProject(ctx, "Account project", "account-project")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.core.CreateEnvironment(ctx, project.ID, core.EnvironmentInput{Name: "prod", RuntimeID: runtime.ID}); err != nil {
		t.Fatal(err)
	}
	admin, err := s.core.Authenticate(ctx, "admin", "test-admin-token", "")
	if err != nil {
		t.Fatal(err)
	}
	key := issueProjectKey(t, h, admin.ID, "account-key", project.ID, "services.read")
	cookie, csrf := browserLogin(t, h)
	r := managementRequestBody(t, "POST", "/api/v1/me/password", map[string]string{"current_password": "test-admin-token", "new_password": "new-admin-password-123456"})
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	result := responseData[map[string]any](t, w, 200)
	newCSRF, _ := result["csrf_token"].(string)
	if newCSRF == "" || newCSRF == csrf || len(w.Result().Cookies()) != 1 {
		t.Fatal("browser session was not refreshed")
	}
	if got := browserRequest(h, "GET", "/api/v1/auth/session", "", cookie, ""); !strings.Contains(got.Body.String(), `"authenticated":false`) {
		t.Fatal("old browser session survived")
	}
	if got := browserRequest(h, "GET", "/api/v1/users", "", w.Result().Cookies()[0], ""); got.Code != 200 {
		t.Fatalf("new session invalid: %d", got.Code)
	}
	if _, err = s.core.Authenticate(ctx, "admin", "test-admin-token", ""); err == nil {
		t.Fatal("old password survived")
	}
	if _, err = s.core.Authenticate(ctx, "admin", "new-admin-password-123456", ""); err != nil {
		t.Fatal(err)
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, "GET", "/projects", nil, nil); got.Code != 200 {
		t.Fatalf("password change revoked project key: %d", got.Code)
	}
}

func TestProjectAPIKeyOwnerRoleFutureEnvironmentAndReviewedDeployment(t *testing.T) {
	s, h, runtime, pool := managedRuntimeFixture(t)
	s.limiter.SetLimit(100000)
	s.limiter.SetBurst(100000)
	ctx := context.Background()
	project, err := s.core.CreateProject(ctx, "Scoped API", "scoped-api")
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.core.CreateEnvironment(ctx, project.ID, core.EnvironmentInput{Name: "prod", RuntimeID: runtime.ID})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := s.core.CreateProject(ctx, "Foreign API", "foreign-api")
	if err != nil {
		t.Fatal(err)
	}
	foreignEnv, err := s.core.CreateEnvironment(ctx, foreign.ID, core.EnvironmentInput{Name: "prod", RuntimeID: runtime.ID})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := s.core.SaveUser(ctx, "", core.UserInput{Username: "project-viewer", Role: "viewer", Enabled: true, Password: "viewer-password-123456"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	viewerToken, err := s.core.RotateUserToken(ctx, viewer.ID, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	path := "/users/" + viewer.ID + "/api-keys"
	if got := managementRequest(t, h, "POST", path, map[string]any{"name": "missing-auth", "project_id": project.ID, "scopes": []string{"services.read"}}, nil); got.Code != 403 {
		t.Fatalf("key issued without password: %d", got.Code)
	}
	if got := bearerUserRequest(t, h, viewerToken, "POST", path, map[string]any{"name": "elevate", "project_id": project.ID, "scopes": []string{"services.create"}}, map[string]string{"X-NSC-Reauthenticate": "viewer-password-123456"}); got.Code != 403 {
		t.Fatalf("viewer self-elevated: %d %s", got.Code, got.Body)
	}
	self := responseData[issuedProjectKey](t, bearerUserRequest(t, h, viewerToken, "POST", path, map[string]any{"name": "self-read", "project_id": project.ID, "scopes": []string{"services.read"}}, map[string]string{"X-NSC-Reauthenticate": "viewer-password-123456"}), 201)
	if got := bearerUserRequest(t, h, viewerToken, "GET", "/users/user_bootstrap/api-keys", nil, nil); got.Code != 404 {
		t.Fatalf("viewer listed another user's keys: %d", got.Code)
	}
	key := issueProjectKey(t, h, viewer.ID, "release-bot", project.ID, "services.read", "services.create", "services.deploy")
	if got := bearerUserRequest(t, h, viewerToken, "POST", "/projects", map[string]any{}, nil); got.Code != 403 {
		t.Fatalf("viewer Bearer gained console writes: %d", got.Code)
	}
	listed := responseData[[]core.OpenAPIKey](t, bearerUserRequest(t, h, viewerToken, "GET", path, nil, nil), 200)
	if len(listed) != 2 || strings.Contains(fmt.Sprint(listed), key.Token) {
		t.Fatal("listed keys leaked secret or owner list was wrong")
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, "GET", "/projects/"+foreign.ID, nil, nil); got.Code != 404 {
		t.Fatal("foreign project readable", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, "GET", "/environments/"+foreignEnv.ID, nil, nil); got.Code != 404 {
		t.Fatal("foreign environment readable", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, "GET", "/projects", nil, nil); got.Code != 200 || strings.Contains(got.Body.String(), foreign.ID) {
		t.Fatal("project discovery leaked another project", got.Code)
	}
	foreignApp, _, _, err := s.core.InstallApplication(ctx, core.InstallApplicationInput{ApplicationPreflightInput: core.ApplicationPreflightInput{RuntimeID: runtime.ID, ComposeYAML: "services:\n  other:\n    image: example.test/other:fixed\n"}, Name: "Other", ProjectName: "foreign-workload", IdempotencyKey: "foreign-install"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.core.CreateEnvironment(ctx, foreign.ID, core.EnvironmentInput{Name: "app", RuntimeID: runtime.ID, ApplicationID: foreignApp.ID}); err != nil {
		t.Fatal(err)
	}
	foreignServices, err := s.store.ListServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: foreignApp.ID}, storage.Page{Limit: 10})
	if err != nil || len(foreignServices) != 1 {
		t.Fatalf("foreign service fixture: %v %+v", err, foreignServices)
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, "GET", "/services/"+foreignServices[0].ID, nil, nil); got.Code != 404 {
		t.Fatal("foreign service readable", got.Code)
	}
	future, err := s.core.CreateEnvironment(ctx, project.ID, core.EnvironmentInput{Name: "future", RuntimeID: runtime.ID})
	if err != nil {
		t.Fatal(err)
	}
	futureResponse := openAPIDeploymentRequest(t, h, key.Token, "GET", "/environments/"+future.ID, nil, nil)
	if futureResponse.Code != 200 || futureResponse.Header().Get("ETag") != fmt.Sprintf(`"rev-%d"`, future.Revision) {
		t.Fatal("future environment or its ETag inaccessible", futureResponse.Code)
	}
	var discovered struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(futureResponse.Body.Bytes(), &discovered); err != nil || len(discovered.Data) != 2 || discovered.Data["id"] != future.ID {
		t.Fatalf("environment discovery exposed fields beyond ID and revision: %+v %v", discovered.Data, err)
	}
	var environments struct {
		Data []map[string]any `json:"data"`
	}
	listResponse := openAPIDeploymentRequest(t, h, key.Token, "GET", "/projects/"+project.ID+"/environments", nil, nil)
	if err := json.Unmarshal(listResponse.Body.Bytes(), &environments); err != nil || listResponse.Code != 200 || len(environments.Data) != 2 {
		t.Fatalf("project environment discovery failed: %d %+v %v", listResponse.Code, environments.Data, err)
	}
	for _, item := range environments.Data {
		if len(item) != 2 || item["id"] == "" || item["revision"] == nil {
			t.Fatalf("environment collection exposed runtime or credential fields: %+v", item)
		}
	}
	legacy, legacyToken, err := s.core.IssueOpenAPIKey(ctx, "legacy", env.ID, "user_bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ProjectID != "" {
		t.Fatal("legacy key acquired project")
	}
	if got := openAPIDeploymentRequest(t, h, legacyToken, "GET", "/projects", nil, nil); got.Code != 404 {
		t.Fatal("legacy key gained new endpoint", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, legacyToken, "GET", "/environments/"+future.ID, nil, nil); got.Code != 404 {
		t.Fatal("legacy key gained future environment", got.Code)
	}
	legacyEnvironment := openAPIDeploymentRequest(t, h, legacyToken, "GET", "/environments/"+env.ID, nil, nil)
	if err := json.Unmarshal(legacyEnvironment.Body.Bytes(), &discovered); err != nil || legacyEnvironment.Code != 200 || discovered.Data["runtime_id"] != runtime.ID {
		t.Fatalf("legacy environment response regressed: %d %+v %v", legacyEnvironment.Code, discovered.Data, err)
	}
	if got := openAPIDeploymentRequest(t, h, self.Token, "POST", "/environments/"+env.ID+"/preflight", map[string]string{"compose_yaml": "services: {web: {image: example.test/web:fixed}}"}, nil); got.Code != 404 {
		t.Fatal("read key created preflight", got.Code)
	}
	image := "example.test/web@sha256:" + strings.Repeat("a", 64)
	compose := "services:\n  web:\n    image: " + image + "\n  worker:\n    image: " + image + "\n"
	createOnly := issueProjectKey(t, h, viewer.ID, "create-only", project.ID, "services.create")
	if got := openAPIDeploymentRequest(t, h, self.Token, "POST", "/environments/"+env.ID+"/service-preflight", map[string]any{"service_mode": "create", "compose_yaml": compose}, nil); got.Code != 404 {
		t.Fatal("read key reached incremental service preflight", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, createOnly.Token, "POST", "/environments/"+env.ID+"/service-preflight", map[string]any{"compose_yaml": compose}, nil); got.Code != 400 {
		t.Fatal("incremental preflight accepted missing service mode", got.Code)
	}
	if got := managementRequest(t, h, "POST", "/environments/"+env.ID+"/service-preflight", map[string]any{"compose_yaml": compose}, nil); got.Code != 400 {
		t.Fatal("console incremental preflight mapped missing service mode to server failure", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, createOnly.Token, "POST", "/environments/"+env.ID+"/service-preflight", map[string]any{"service_mode": "create", "compose_yaml": compose}, nil); got.Code != 400 {
		t.Fatal("Compose runtime accepted Swarm-only incremental preflight", got.Code)
	}
	createReview := responseData[map[string]any](t, openAPIDeploymentRequest(t, h, createOnly.Token, "POST", "/environments/"+env.ID+"/preflight", map[string]string{"compose_yaml": compose}, nil), 200)
	preSubmit := openAPIDeploymentRequest(t, h, createOnly.Token, "POST", "/environments/"+env.ID+"/deployments", map[string]any{"preflight_id": createReview["preflight_id"]}, map[string]string{"If-Match": `"rev-1"`, "Idempotency-Key": "no-deploy"})
	if preSubmit.Code != 404 {
		t.Fatal("create-only key deployed", preSubmit.Code)
	}
	preview := responseData[map[string]any](t, openAPIDeploymentRequest(t, h, key.Token, "POST", "/environments/"+env.ID+"/preflight", map[string]string{"compose_yaml": compose}, nil), 200)
	envResponse := openAPIDeploymentRequest(t, h, key.Token, "GET", "/environments/"+env.ID, nil, nil)
	operation := responseData[model.DeploymentOperation](t, openAPIDeploymentRequest(t, h, key.Token, "POST", "/environments/"+env.ID+"/deployments", map[string]any{"preflight_id": preview["preflight_id"]}, map[string]string{"If-Match": envResponse.Header().Get("ETag"), "Idempotency-Key": "viewer-create"}), 202)
	replayed := responseData[model.DeploymentOperation](t, openAPIDeploymentRequest(t, h, key.Token, "POST", "/environments/"+env.ID+"/deployments", map[string]any{"preflight_id": preview["preflight_id"]}, map[string]string{"If-Match": envResponse.Header().Get("ETag"), "Idempotency-Key": "viewer-create"}), 202)
	if replayed.ID != operation.ID {
		t.Fatal("project deployment retry duplicated operation")
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, "GET", "/operations/"+operation.ID, nil, nil); got.Code != 200 {
		t.Fatalf("project operation snapshot denied: %d %s", got.Code, got.Body)
	}
	if got := openAPIDeploymentRequest(t, h, self.Token, "GET", "/operations/"+operation.ID, nil, nil); got.Code != 200 {
		t.Fatal("project read key could not read owned operation", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, "GET", "/services", nil, nil); got.Code != 200 || !strings.Contains(got.Body.String(), "web") || strings.Contains(got.Body.String(), foreignServices[0].ID) {
		t.Fatal("service list failed", got.Code, got.Body)
	}
	firstPage := openAPIDeploymentRequest(t, h, key.Token, "GET", "/services?limit=1", nil, nil)
	if firstPage.Code != 200 {
		t.Fatal("first service page unavailable", firstPage.Code)
	}
	var page struct {
		Data       []model.Service `json:"data"`
		Pagination struct {
			NextCursor string `json:"next_cursor"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(firstPage.Body.Bytes(), &page); err != nil || len(page.Data) != 1 || page.Pagination.NextCursor == "" || page.Data[0].ID == foreignServices[0].ID {
		t.Fatalf("project service page leaked or omitted cursor: %+v %v", page, err)
	}
	secondPage := openAPIDeploymentRequest(t, h, key.Token, "GET", "/services?limit=1&cursor="+page.Pagination.NextCursor, nil, nil)
	if err := json.Unmarshal(secondPage.Body.Bytes(), &page); err != nil || secondPage.Code != 200 || len(page.Data) != 1 || page.Data[0].ID == foreignServices[0].ID {
		t.Fatalf("second service page leaked another project: %d %+v %v", secondPage.Code, page, err)
	}
	services, err := s.store.ListServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: operation.ApplicationID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 2 {
		t.Fatalf("deployment services: %v %+v", err, services)
	}
	serviceEdit := issueProjectKey(t, h, viewer.ID, "service-edit", project.ID, "services.edit")
	patch := map[string]any{"target_port_override": 8081}
	if got := openAPIDeploymentRequest(t, h, self.Token, "PATCH", "/services/"+services[1].ID, patch, map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, services[1].Revision)}); got.Code != 404 {
		t.Fatal("read key edited service", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, serviceEdit.Token, "PATCH", "/services/"+foreignServices[0].ID, patch, map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, foreignServices[0].Revision)}); got.Code != 404 {
		t.Fatal("project key edited foreign service", got.Code)
	}
	services[1] = responseData[model.Service](t, openAPIDeploymentRequest(t, h, serviceEdit.Token, "PATCH", "/services/"+services[1].ID, patch, map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, services[1].Revision)}), 200)
	if services[1].TargetPortOverride == nil || *services[1].TargetPortOverride != 8081 {
		t.Fatal("service edit did not persist")
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, "POST", "/services/"+services[0].ID+"/restart", nil, nil); got.Code != 404 {
		t.Fatal("key without operate reached restart", got.Code)
	}
	// Invalidate the immutable claim: current application ownership is insufficient.
	if _, err := s.store.(*storage.SQLStore).DB().ExecContext(ctx, "UPDATE deployment_operations SET scope_snapshot=0 WHERE id=?", operation.ID); err != nil {
		t.Fatal(err)
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, "GET", "/operations/"+operation.ID, nil, nil); got.Code != 404 {
		t.Fatal("operation without scope snapshot leaked", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, legacyToken, "GET", "/operations/"+operation.ID, nil, nil); got.Code != 200 {
		t.Fatal("legacy revision authorization changed", got.Code)
	}
	serviceKeys := []string{services[0].ServiceKey, services[1].ServiceKey}
	if err := s.store.(*storage.SQLStore).ActivateApplicationRevision(ctx, runtime.ID, operation.ApplicationID, operation.RevisionID, operation.RevisionID, serviceKeys, nil, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpsertGateway(ctx, &model.Gateway{ID: id.New(id.Gateway), PoolID: pool.ID, Name: "project-edge", Address: "192.0.2.44", Enabled: true, Metadata: model.Metadata{Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	draftCreate := issueProjectKey(t, h, viewer.ID, "draft-create", project.ID, "domains.create")
	draftPublish := issueProjectKey(t, h, viewer.ID, "draft-publish", project.ID, "domains.publish")
	domainRead := issueProjectKey(t, h, viewer.ID, "domain-read", project.ID, "domains.read")
	draftInput := core.DomainDraftInput{Domain: core.DomainConfigInput{Host: "project-key.example.test", EntryPoints: []string{core.EntryPointHTTP}, TLS: core.DomainTLSInput{Mode: "disabled"}}, ServiceID: services[0].ID, Route: core.RouteConfigInput{Path: "/", Backend: core.RouteBackendInput{Port: 8080, Protocol: "http"}}}
	draft := responseData[core.DomainDraft](t, openAPIDeploymentRequest(t, h, draftCreate.Token, "POST", "/environments/"+env.ID+"/domain-drafts", draftInput, nil), 201)
	if got := openAPIDeploymentRequest(t, h, draftCreate.Token, "POST", "/domain-drafts/"+draft.ID+"/publish", nil, map[string]string{"If-Match": `"rev-1"`}); got.Code != 404 {
		t.Fatal("draft creation key published", got.Code)
	}
	published := responseData[core.DomainDraft](t, openAPIDeploymentRequest(t, h, draftPublish.Token, "POST", "/domain-drafts/"+draft.ID+"/publish", nil, map[string]string{"If-Match": `"rev-1"`}), 202)
	if published.OperationID == "" || published.PublishedRouteID == "" {
		t.Fatal("domain publish receipt missing operation")
	}
	if got := openAPIDeploymentRequest(t, h, domainRead.Token, "GET", "/operations/"+published.OperationID, nil, nil); got.Code != 200 {
		t.Fatalf("domain operation scope not readable: %d %s", got.Code, got.Body)
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, "GET", "/operations/"+published.OperationID, nil, nil); got.Code != 404 {
		t.Fatal("service read scope accessed domain operation", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, domainRead.Token, "GET", "/services/"+services[0].ID+"/domain-bindings", nil, nil); got.Code != 200 {
		t.Fatal("domain read key could not list service bindings", got.Code)
	}
	operate := issueProjectKey(t, h, viewer.ID, "operate-only", project.ID, "services.operate")
	if got := openAPIDeploymentRequest(t, h, operate.Token, "POST", "/services/"+services[0].ID+"/restart", nil, map[string]string{"Idempotency-Key": "restart-once"}); got.Code != 202 {
		t.Fatalf("operate key could not restart: %d %s", got.Code, got.Body)
	}
	if got := openAPIDeploymentRequest(t, h, operate.Token, "POST", "/services/"+services[0].ID+"/scale", map[string]any{"replicas": 2, "persist": false}, map[string]string{"Idempotency-Key": "scale-temporary"}); got.Code != 202 {
		t.Fatalf("operate key could not temporarily scale: %d %s", got.Code, got.Body)
	}
	if got := openAPIDeploymentRequest(t, h, operate.Token, "POST", "/services/"+services[0].ID+"/scale", map[string]any{"replicas": 2, "persist": true}, map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, services[0].Revision), "Idempotency-Key": "scale-persist-denied"}); got.Code != 404 {
		t.Fatal("operate-only key persisted scale", got.Code)
	}
	fullOperate := issueProjectKey(t, h, viewer.ID, "operate-persist", project.ID, "services.operate", "services.edit", "services.deploy")
	services[1] = responseData[model.Service](t, openAPIDeploymentRequest(t, h, key.Token, "GET", "/services/"+services[1].ID, nil, nil), 200)
	if got := openAPIDeploymentRequest(t, h, fullOperate.Token, "POST", "/services/"+services[1].ID+"/scale", map[string]any{"replicas": 2, "persist": true}, map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, services[1].Revision), "Idempotency-Key": "scale-persist-allowed"}); got.Code != 202 {
		t.Fatalf("operate+edit+deploy key could not persist scale: %d %s", got.Code, got.Body)
	}
	domainCreate := issueProjectKey(t, h, viewer.ID, "domain-create-only", project.ID, "domains.create")
	if got := openAPIDeploymentRequest(t, h, domainCreate.Token, "POST", "/services/"+services[0].ID+"/domain-bindings", "{", nil); got.Code != 404 {
		t.Fatal("domain create without publish reached live binding", got.Code)
	}
	domainBoth := issueProjectKey(t, h, viewer.ID, "domain-live", project.ID, "domains.create", "domains.publish")
	if got := openAPIDeploymentRequest(t, h, domainBoth.Token, "POST", "/services/"+services[0].ID+"/domain-bindings", map[string]any{"domain_id": published.PublishedDomainID, "route": draftInput.Route}, nil); got.Code != 404 {
		t.Fatal("project key attached shared Domain", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, domainBoth.Token, "POST", "/services/"+services[0].ID+"/domain-bindings", "{", nil); got.Code != 400 {
		t.Fatal("authorized live binding was not reached", got.Code)
	}
	bindingPath := "/services/" + services[0].ID + "/domain-bindings"
	bindingInput := core.CreateDomainBindingInput{
		Domain: &core.DomainConfigInput{Host: "project-binding.example.test", EntryPoints: []string{core.EntryPointHTTP}, TLS: core.DomainTLSInput{Mode: "disabled"}},
		Route:  draftInput.Route,
	}
	type bindingReceipt struct {
		Binding   model.DomainBinding       `json:"binding"`
		Operation model.DeploymentOperation `json:"operation"`
	}
	createdResponse := openAPIDeploymentRequest(t, h, domainBoth.Token, "POST", bindingPath, bindingInput, map[string]string{"Idempotency-Key": "new-project-binding"})
	createdBinding := responseData[bindingReceipt](t, createdResponse, 202)
	if createdBinding.Binding.Route.ID == "" || createdBinding.Operation.ID == "" || createdResponse.Header().Get("Location") != "/openapi/v1/operations/"+createdBinding.Operation.ID {
		t.Fatal("project binding did not return its queued operation")
	}
	if got := openAPIDeploymentRequest(t, h, domainRead.Token, "GET", "/operations/"+createdBinding.Operation.ID, nil, nil); got.Code != 200 {
		t.Fatal("domain read key could not read binding operation", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, domainRead.Token, "GET", bindingPath, nil, nil); got.Code != 200 || !strings.Contains(got.Body.String(), createdBinding.Binding.Route.ID) {
		t.Fatal("created binding was not listed", got.Code)
	}
	updatePath := bindingPath + "/" + createdBinding.Binding.Route.ID
	editPublish := issueProjectKey(t, h, viewer.ID, "domain-edit-live", project.ID, "domains.edit", "domains.publish")
	if got := openAPIDeploymentRequest(t, h, domainRead.Token, "PATCH", updatePath, map[string]any{"route": map[string]any{"path": "/api/"}}, map[string]string{"If-Match": `"rev-1"`}); got.Code != 404 {
		t.Fatal("domain read key edited a binding", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, editPublish.Token, "PATCH", updatePath, map[string]any{"domain": map[string]any{"host": "forbidden.example.test"}}, map[string]string{"If-Match": `"rev-1"`}); got.Code != 404 {
		t.Fatal("project key modified shared Domain", got.Code)
	}
	updatedBinding := responseData[model.DomainBinding](t, openAPIDeploymentRequest(t, h, editPublish.Token, "PATCH", updatePath, map[string]any{"route": map[string]any{"path": "/api/"}}, map[string]string{"If-Match": `"rev-1"`}), 200)
	if updatedBinding.Route.Path != "/api/" || updatedBinding.Domain.ID != createdBinding.Binding.Domain.ID {
		t.Fatal("binding edit changed unrelated Domain state")
	}
	deletePublish := issueProjectKey(t, h, viewer.ID, "domain-delete-live", project.ID, "domains.delete", "domains.publish")
	if got := openAPIDeploymentRequest(t, h, draftCreate.Token, "DELETE", updatePath, nil, map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, updatedBinding.Route.Revision)}); got.Code != 404 {
		t.Fatal("draft create scope deleted live binding", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, deletePublish.Token, "DELETE", updatePath, nil, map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, updatedBinding.Route.Revision), "Idempotency-Key": "delete-project-binding"}); got.Code != 204 {
		t.Fatalf("project binding delete failed: %d %s", got.Code, got.Body)
	}
	if route, err := s.store.GetRoute(ctx, createdBinding.Binding.Route.ID); err != nil || route.Enabled {
		t.Fatal("deleted binding remained enabled", err)
	}
	updatedCompose := "services:\n  web:\n    image: " + image + "\n    environment: {MODE: test}\n  worker:\n    image: " + image + "\n"
	if got := openAPIDeploymentRequest(t, h, key.Token, "POST", "/environments/"+env.ID+"/preflight", map[string]string{"compose_yaml": updatedCompose}, nil); got.Code != 404 {
		t.Fatalf("create key edited existing service: %d %s", got.Code, got.Body)
	}
	editOnly := issueProjectKey(t, h, viewer.ID, "edit-only", project.ID, "services.edit")
	editReview := responseData[map[string]any](t, openAPIDeploymentRequest(t, h, editOnly.Token, "POST", "/environments/"+env.ID+"/preflight", map[string]string{"compose_yaml": updatedCompose}, nil), 200)
	envResponse = openAPIDeploymentRequest(t, h, editOnly.Token, "GET", "/environments/"+env.ID, nil, nil)
	if got := openAPIDeploymentRequest(t, h, editOnly.Token, "POST", "/environments/"+env.ID+"/deployments", map[string]any{"preflight_id": editReview["preflight_id"]}, map[string]string{"If-Match": envResponse.Header().Get("ETag"), "Idempotency-Key": "edit-no-deploy"}); got.Code != 404 {
		t.Fatal("edit-only key deployed", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, editOnly.Token, "POST", "/environments/"+env.ID+"/preflight", map[string]string{"compose_yaml": "services:\n  web:\n    image: " + image + "\n"}, nil); got.Code != 400 {
		t.Fatalf("candidate deleting worker accepted: %d %s", got.Code, got.Body)
	}
	editDeploy := issueProjectKey(t, h, viewer.ID, "edit-deploy", project.ID, "services.edit", "services.deploy")
	if got := openAPIDeploymentRequest(t, h, editDeploy.Token, "POST", "/environments/"+env.ID+"/deployments", map[string]any{"preflight_id": editReview["preflight_id"]}, map[string]string{"If-Match": envResponse.Header().Get("ETag"), "Idempotency-Key": "edit-confirm"}); got.Code != 202 {
		t.Fatalf("edit+deploy denied: %d %s", got.Code, got.Body)
	}
	// An administrator can reset a viewer password without revoking project keys.
	viewer, err = s.core.User(ctx, viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err = s.core.SaveUser(ctx, viewer.ID, core.UserInput{Username: viewer.Username, Role: "viewer", Enabled: true, Password: "reset-password-123456"}, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, "GET", "/projects", nil, nil); got.Code != 200 {
		t.Fatal("reset revoked project key", got.Code)
	}
	if got := bearerUserRequest(t, h, viewerToken, "GET", path, nil, nil); got.Code != 401 {
		t.Fatal("legacy user token survived password reset", got.Code)
	}
	viewer, err = s.core.SaveUser(ctx, viewer.ID, core.UserInput{Username: viewer.Username, Role: "viewer", Enabled: false}, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, "GET", "/projects", nil, nil); got.Code != 401 {
		t.Fatal("disabled owner key remained active", got.Code)
	}
	viewer, err = s.core.SaveUser(ctx, viewer.ID, core.UserInput{Username: viewer.Username, Role: "viewer", Enabled: true}, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, "GET", "/projects", nil, nil); got.Code != 401 {
		t.Fatal("reenabling restored revoked key", got.Code)
	}
	fresh := issueProjectKey(t, h, viewer.ID, "role-change", project.ID, "services.read")
	viewer, err = s.core.SaveUser(ctx, viewer.ID, core.UserInput{Username: viewer.Username, Role: "administrator", Enabled: true}, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if got := openAPIDeploymentRequest(t, h, fresh.Token, "GET", "/projects", nil, nil); got.Code != 401 {
		t.Fatal("role change retained key", got.Code)
	}
}

func TestProjectAPIKeyVariableAndDomainDraftScopes(t *testing.T) {
	s, h, runtime, _ := managedRuntimeFixture(t)
	s.limiter.SetLimit(100000)
	s.limiter.SetBurst(100000)
	ctx := context.Background()
	project, err := s.core.CreateProject(ctx, "Scope matrix", "scope-matrix")
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.core.CreateEnvironment(ctx, project.ID, core.EnvironmentInput{Name: "prod", RuntimeID: runtime.ID})
	if err != nil {
		t.Fatal(err)
	}
	owner := "user_bootstrap"
	read := issueProjectKey(t, h, owner, "read-vars", project.ID, "project_variables.read", "environment_variables.read", "domains.read")
	edit := issueProjectKey(t, h, owner, "edit-vars", project.ID, "project_variables.edit", "environment_variables.edit", "domains.edit")
	create := issueProjectKey(t, h, owner, "create-domains", project.ID, "domains.create")
	remove := issueProjectKey(t, h, owner, "delete-domains", project.ID, "domains.delete")
	publish := issueProjectKey(t, h, owner, "publish-domains", project.ID, "domains.publish")
	secret := "fixture-private-value"
	if _, err = s.core.SaveVariables(ctx, project.ID, core.VariableInput{Values: []core.Variable{{Key: "PRIVATE", Value: &secret, Secret: true}}}, 0); err != nil {
		t.Fatal(err)
	}
	projectVars := "/projects/" + project.ID + "/variables"
	envVars := "/environments/" + env.ID + "/variables"
	if got := openAPIDeploymentRequest(t, h, read.Token, "GET", projectVars, nil, nil); got.Code != 200 || strings.Contains(got.Body.String(), secret) {
		t.Fatalf("project secret not redacted: %d %s", got.Code, got.Body)
	}
	if got := openAPIDeploymentRequest(t, h, read.Token, "GET", envVars, nil, nil); got.Code != 200 {
		t.Fatal("environment variables read denied", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, read.Token, "PUT", envVars, map[string]any{"values": []any{}}, map[string]string{"If-Match": `"rev-0"`}); got.Code != 404 {
		t.Fatal("read key wrote variables", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, edit.Token, "GET", projectVars, nil, nil); got.Code != 404 {
		t.Fatal("edit key read variables", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, edit.Token, "PUT", projectVars, map[string]any{"values": []any{map[string]any{"key": "PRIVATE", "value": secret, "secret": true}}}, map[string]string{"If-Match": `"rev-0"`}); got.Code != 412 {
		t.Fatal("stale project variables ETag accepted", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, edit.Token, "PUT", projectVars, map[string]any{"values": []any{map[string]any{"key": "PRIVATE", "value": secret, "secret": true}}}, map[string]string{"If-Match": `"rev-1"`}); got.Code != 200 || strings.Contains(got.Body.String(), secret) {
		t.Fatal("project variables edit failed or exposed secret", got.Code, got.Body)
	}
	if got := openAPIDeploymentRequest(t, h, edit.Token, "PUT", envVars, map[string]any{"values": []any{map[string]any{"key": "PUBLIC", "value": "ok"}}}, map[string]string{"If-Match": `"rev-0"`}); got.Code != 200 {
		t.Fatalf("edit key could not save variables: %d %s", got.Code, got.Body)
	}
	if got := openAPIDeploymentRequest(t, h, read.Token, "GET", "/environments/"+env.ID+"/effective-variables", nil, nil); got.Code != 200 || strings.Contains(got.Body.String(), secret) {
		t.Fatal("effective variables leaked secret", got.Code, got.Body)
	}
	input := core.DomainDraftInput{Domain: core.DomainConfigInput{Host: "scope.example.test", EntryPoints: []string{core.EntryPointHTTP}, TLS: core.DomainTLSInput{Mode: "disabled"}}, Route: core.RouteConfigInput{Path: "/", Backend: core.RouteBackendInput{Port: 8080, Protocol: "http"}}}
	base := "/environments/" + env.ID + "/domain-drafts"
	if got := openAPIDeploymentRequest(t, h, read.Token, "GET", base, nil, nil); got.Code != 404 {
		t.Fatal("read key accessed sensitive drafts", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, edit.Token, "POST", base, input, nil); got.Code != 404 {
		t.Fatal("edit key created draft", got.Code)
	}
	draft := responseData[core.DomainDraft](t, openAPIDeploymentRequest(t, h, create.Token, "POST", base, input, nil), 201)
	if got := openAPIDeploymentRequest(t, h, edit.Token, "GET", base, nil, nil); got.Code != 200 || !strings.Contains(got.Body.String(), draft.ID) {
		t.Fatal("edit key could not list sensitive drafts", got.Code)
	}
	path := "/domain-drafts/" + draft.ID
	if got := openAPIDeploymentRequest(t, h, create.Token, "GET", path, nil, nil); got.Code != 404 {
		t.Fatal("create key read sensitive draft", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, edit.Token, "GET", path, nil, nil); got.Code != 200 {
		t.Fatal("edit key could not read draft", got.Code)
	}
	input.Domain.Host = "edited-scope.example.test"
	draft = responseData[core.DomainDraft](t, openAPIDeploymentRequest(t, h, edit.Token, "PUT", path, input, map[string]string{"If-Match": `"rev-1"`}), 200)
	if got := openAPIDeploymentRequest(t, h, edit.Token, "POST", path+"/publish", nil, map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, draft.Revision)}); got.Code != 404 {
		t.Fatal("edit key published draft", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, publish.Token, "POST", path+"/publish", nil, map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, draft.Revision)}); got.Code != 400 {
		t.Fatalf("publish key did not reach validation: %d %s", got.Code, got.Body)
	}
	unchanged, err := s.core.GetDomainDraft(ctx, draft.ID)
	if err != nil || unchanged.Revision != draft.Revision || unchanged.Status != "draft" {
		t.Fatal("failed publish changed draft", err)
	}
	if got := openAPIDeploymentRequest(t, h, remove.Token, "DELETE", path, nil, map[string]string{"If-Match": `"rev-1"`}); got.Code != 412 {
		t.Fatal("stale ETag removed draft", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, remove.Token, "DELETE", path, nil, map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, draft.Revision)}); got.Code != 204 {
		t.Fatal("delete scope failed", got.Code, got.Body)
	}
}

func TestUserAPIKeyRevocationRequiresOwnerPasswordAndETag(t *testing.T) {
	s, h, runtime, _ := managedRuntimeFixture(t)
	ctx := context.Background()
	project, err := s.core.CreateProject(ctx, "Revoke API", "revoke-api")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.core.CreateEnvironment(ctx, project.ID, core.EnvironmentInput{Name: "prod", RuntimeID: runtime.ID}); err != nil {
		t.Fatal(err)
	}
	key := issueProjectKey(t, h, "user_bootstrap", "revoke-me", project.ID, "services.read")
	legacyList := responseData[[]core.OpenAPIKey](t, managementRequest(t, h, "GET", "/openapi-keys", nil, nil), 200)
	if len(legacyList) != 0 {
		t.Fatal("project key appeared in legacy key list")
	}
	if got := managementRequest(t, h, "DELETE", "/openapi-keys/"+key.ID, nil, map[string]string{"X-NSC-Reauthenticate": "test-admin-token", "If-Match": `"rev-1"`}); got.Code != 404 {
		t.Fatal("legacy endpoint revoked project key", got.Code)
	}
	path := "/users/user_bootstrap/api-keys/" + key.ID
	for _, tc := range []struct {
		password, etag string
		status         int
	}{{"", `"rev-1"`, 403}, {"test-admin-token", "", 428}, {"test-admin-token", `"rev-2"`, 412}} {
		got := managementRequest(t, h, "DELETE", path, nil, map[string]string{"X-NSC-Reauthenticate": tc.password, "If-Match": tc.etag})
		if got.Code != tc.status {
			t.Fatalf("revoke boundary: %d want %d", got.Code, tc.status)
		}
	}
	if got := managementRequest(t, h, "DELETE", path, nil, map[string]string{"X-NSC-Reauthenticate": "test-admin-token", "If-Match": `"rev-1"`}); got.Code != 204 {
		t.Fatal("revoke failed", got.Code)
	}
	if got := openAPIDeploymentRequest(t, h, key.Token, "GET", "/projects", nil, nil); got.Code != 401 {
		t.Fatal("revoked key still active", got.Code)
	}
	listed := responseData[[]core.OpenAPIKey](t, managementRequest(t, h, "GET", "/users/user_bootstrap/api-keys", nil, nil), 200)
	if len(listed) != 1 || listed[0].RevokedAt == nil {
		t.Fatal("revocation not persisted")
	}
	concurrent := issueProjectKey(t, h, "user_bootstrap", "concurrent", project.ID, "services.read")
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- s.core.RevokeUserAPIKey(ctx, "user_bootstrap", concurrent.ID, concurrent.Revision)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent ETag revocation committed %d times", winners)
	}
	if got := openAPIDeploymentRequest(t, h, concurrent.Token, "GET", "/projects", nil, nil); got.Code != 401 {
		t.Fatal("concurrent revoke left key active", got.Code)
	}
}
