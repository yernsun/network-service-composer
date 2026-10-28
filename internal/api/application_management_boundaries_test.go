package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func managementRequest(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	data := ""
	if raw, ok := body.(string); ok {
		data = raw
	} else if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		data = string(encoded)
	}
	r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(data))
	r.Header.Set("Authorization", "Bearer test-admin-token")
	r.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func responseData[T any](t *testing.T, w *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if w.Code != status {
		t.Fatalf("HTTP %d, expected %d: %s", w.Code, status, w.Body.String())
	}
	var envelope struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}
func managedRuntimeFixture(t *testing.T) (*Server, http.Handler, model.Runtime, model.GatewayPool) {
	t.Helper()
	s, h := newTestServerWithConfig(t, func(c *config.Config) { c.Management.Enabled = true })
	ctx := context.Background()
	now := time.Now().UTC()
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "isolated", BackendNetwork: "nsc-fixture", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtime := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "isolated", Type: model.RuntimeDocker, Endpoint: "unix:///nonexistent/nsc-unit-fixture.sock", Enabled: true, ManagementEnabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.store.CreateRuntime(ctx, &runtime); err != nil {
		t.Fatal(err)
	}
	return s, h, runtime, pool
}
func TestApplicationHTTPInstallIsIdempotentAndKeepsExecutionQueued(t *testing.T) {
	s, h, runtime, pool := managedRuntimeFixture(t)
	ctx := context.Background()
	input := map[string]any{"runtime_id": runtime.ID, "compose_yaml": "services:\n  web:\n    image: nginx:1.29-alpine\n    expose: ['8080']\n"}
	preflight := managementRequest(t, h, "POST", "/applications/preflight", input, nil)
	if preflight.Code != 200 {
		t.Fatalf("preflight=%d: %s", preflight.Code, preflight.Body.String())
	}
	input["name"] = "Website"
	input["project_name"] = "website"
	type installed struct {
		Application model.Application         `json:"application"`
		Operation   model.DeploymentOperation `json:"operation"`
	}
	firstResponse := managementRequest(t, h, "POST", "/applications", input, map[string]string{"Idempotency-Key": "http-install"})
	first := responseData[installed](t, firstResponse, 202)
	if first.Operation.Status != model.OperationQueued || first.Operation.ApplicationID != first.Application.ID || firstResponse.Header().Get("Location") != "/api/v1/operations/"+first.Operation.ID {
		t.Fatalf("install did not identify queued work: %#v", first)
	}
	again := responseData[installed](t, managementRequest(t, h, "POST", "/applications", input, map[string]string{"Idempotency-Key": "http-install"}), 202)
	if first.Application.ID != again.Application.ID || first.Operation.ID != again.Operation.ID {
		t.Fatal("duplicate request created another application or operation")
	}
	appResponse := managementRequest(t, h, "GET", "/applications/"+first.Application.ID, nil, nil)
	app := responseData[model.Application](t, appResponse, 200)
	if app.ID != first.Application.ID || appResponse.Header().Get("ETag") == "" {
		t.Fatal("application cannot be recovered from its resource link")
	}
	op := responseData[model.DeploymentOperation](t, managementRequest(t, h, "GET", "/operations/"+first.Operation.ID, nil, nil), 200)
	if op.Status != model.OperationQueued {
		t.Fatal("reading task falsely reported completion")
	}
	for _, path := range []string{"/applications?runtime_id=" + runtime.ID, "/applications/" + app.ID + "/revisions", "/applications/" + app.ID + "/operations", "/operations?application_id=" + app.ID} {
		w := managementRequest(t, h, "GET", path, nil, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), app.ID) {
			t.Fatalf("queued application history unavailable at %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	revision := responseData[model.ApplicationRevision](t, managementRequest(t, h, "GET", "/applications/"+app.ID+"/revisions/"+op.RevisionID, nil, nil), 200)
	if revision.ApplicationID != app.ID {
		t.Fatal("revision scope mismatch")
	}
	foreign := managementRequest(t, h, "GET", "/applications/"+id.New(id.Application)+"/revisions/"+op.RevisionID, nil, nil)
	if foreign.Code != 404 {
		t.Fatalf("cross-application revision exposed: %d", foreign.Code)
	}
	upgrades := responseData[[]model.CatalogAppVersion](t, managementRequest(t, h, "GET", "/applications/"+app.ID+"/upgrades", nil, nil), 200)
	if len(upgrades) != 0 {
		t.Fatal("non-catalog application received unrelated upgrades")
	}
	actualPool, err := s.store.GetPool(ctx, pool.ID)
	if err != nil || actualPool.DesiredRevision != pool.DesiredRevision {
		t.Fatalf("preflight/install changed Gateway production target: %#v %v", actualPool, err)
	}
	operations, err := s.store.ListDeploymentOperations(ctx, app.ID, storage.Page{Limit: 20})
	if err != nil || len(operations) != 1 {
		t.Fatalf("idempotent install has %d operations: %v", len(operations), err)
	}
}

func TestManagementRejectsMalformedRequestsBeforeWriting(t *testing.T) {
	s, h, runtime, _ := managedRuntimeFixture(t)
	for _, tc := range []struct {
		method, path string
		precondition bool
	}{
		{"POST", "/services/missing/domain-bindings", false}, {"PATCH", "/services/missing/domain-bindings/missing", true},
		{"POST", "/services/missing/scale", false}, {"POST", "/services/missing/operations", false},
		{"POST", "/catalog/sources", false}, {"POST", "/applications/preflight", false}, {"POST", "/applications", false},
		{"POST", "/applications/missing/revisions/preflight", false}, {"POST", "/applications/missing/revisions", true}, {"POST", "/applications/missing/operations", false},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			headers := map[string]string{}
			if tc.precondition {
				headers["If-Match"] = `"rev-1"`
			}
			w := managementRequest(t, h, tc.method, tc.path, "{", headers)
			if w.Code != 400 {
				t.Fatalf("malformed request status=%d: %s", w.Code, w.Body.String())
			}
		})
	}
	for _, tc := range []struct{ method, path string }{{"PATCH", "/services/missing/domain-bindings/missing"}, {"DELETE", "/services/missing/domain-bindings/missing"}, {"PATCH", "/catalog/sources/missing"}, {"POST", "/applications/missing/revisions"}} {
		w := managementRequest(t, h, tc.method, tc.path, `{}`, nil)
		if w.Code != 428 {
			t.Fatalf("missing concurrency precondition on %s=%d: %s", tc.path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/services/missing/domain-bindings", "/services/missing/restart", "/services/missing/scale", "/services/missing/operations", "/applications/preflight", "/applications", "/applications/missing/revisions/preflight", "/applications/missing/revisions", "/applications/missing/operations"} {
		w := managementRequest(t, h, "POST", path, `{}`, map[string]string{"If-Match": `"rev-1"`})
		if w.Code < 400 || w.Code >= 500 {
			t.Fatalf("invalid business request %s=%d: %s", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/catalog/sources/missing", "/catalog/versions/missing", "/applications/missing", "/applications/missing/upgrades", "/applications/missing/revisions/missing", "/operations/missing"} {
		w := managementRequest(t, h, "GET", path, nil, nil)
		if w.Code != 404 {
			t.Fatalf("missing resource %s=%d: %s", path, w.Code, w.Body.String())
		}
	}
	apps, err := s.store.ListApplications(context.Background(), runtime.ID, storage.Page{Limit: 10})
	if err != nil || len(apps) != 0 {
		t.Fatalf("rejected requests created applications: %#v %v", apps, err)
	}
	ops, err := s.store.ListDeploymentOperations(context.Background(), "", storage.Page{Limit: 10})
	if err != nil || len(ops) != 0 {
		t.Fatalf("rejected requests queued work: %#v %v", ops, err)
	}
}
