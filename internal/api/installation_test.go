package api

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type installationAPIProvider struct {
	err   error
	calls int
	input bootstrap.ExecuteRequest
	plan  bootstrap.PlanRequest
}

func (p *installationAPIProvider) View(context.Context) (bootstrap.Installation, error) {
	p.calls++
	return bootstrap.Installation{ID: "installation-fixture", Revision: 3, State: bootstrap.Succeeded, AllowedOperations: []bootstrap.Action{bootstrap.Verify}, Drift: []string{}}, p.err
}
func (p *installationAPIProvider) Plan(_ context.Context, input bootstrap.PlanRequest) (bootstrap.Plan, error) {
	p.calls++
	p.plan = input
	return bootstrap.Plan{Hash: "approved-plan", Revision: 3, Action: input.Action}, p.err
}
func (p *installationAPIProvider) Start(_ context.Context, input bootstrap.ExecuteRequest) (bootstrap.MaintenanceJob, error) {
	p.calls++
	p.input = input
	return bootstrap.MaintenanceJob{ID: "maintenance-fixture", State: bootstrap.Pending}, p.err
}
func (p *installationAPIProvider) Operation(_ context.Context, id string) (bootstrap.MaintenanceJob, error) {
	p.calls++
	return bootstrap.MaintenanceJob{ID: id, Revision: 4, State: bootstrap.Unknown}, p.err
}
func TestInstallationAPIApprovalBoundariesAndAdministratorOnly(t *testing.T) {
	server, handler, _, _ := managedRuntimeFixture(t)
	provider := &installationAPIProvider{}
	server.core.Installation = provider
	response := managementRequest(t, handler, "GET", "/system/installation", nil, nil)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(response.Code, response.Body.String())
	}
	response = managementRequest(t, handler, "POST", "/system/installation/plans", map[string]any{"action": "repair", "revision": 3, "components": []string{"controller"}}, nil)
	if response.Code != 201 || provider.plan.Action != bootstrap.Repair {
		t.Fatal(response.Code, response.Body.String())
	}
	response = managementRequest(t, handler, "POST", "/system/installation/operations", map[string]any{"plan_hash": "approved-plan", "revision": 3}, map[string]string{"Idempotency-Key": "exact-idempotency"})
	if response.Code != 202 || provider.input.IdempotencyKey != "exact-idempotency" || provider.input.Actor == "" || response.Header().Get("Location") != "/api/v1/system/installation/operations/maintenance-fixture" {
		t.Fatal(response.Code, response.Body.String(), provider.input)
	}
	response = managementRequest(t, handler, "GET", "/system/installation/operations/maintenance-fixture", nil, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "UNKNOWN") {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, body := range []map[string]any{{"action": "repair", "revision": 3, "shell": "rm"}, {"action": "repair", "revision": 3, "release": map[string]any{"arbitrary": "value"}}} {
		response = managementRequest(t, handler, "POST", "/system/installation/plans", body, nil)
		if response.Code != 400 {
			t.Fatal("untyped plan fields accepted", response.Code)
		}
	}
	response = managementRequest(t, handler, "POST", "/system/installation/operations", map[string]any{"plan_hash": "approved-plan", "revision": 3, "actor": "forged"}, nil)
	if response.Code != 400 {
		t.Fatal("caller overrode trusted actor")
	}
	for _, err := range []error{&bootstrap.MaintenanceError{Code: "stale_plan"}, storage.ErrNotFound, errors.New("private driver failure")} {
		provider.err = err
		response = managementRequest(t, handler, "GET", "/system/installation", nil, nil)
		if response.Code < 400 || strings.Contains(response.Body.String(), "private driver failure") {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	provider.err = nil
	viewer, err := server.core.SaveUser(t.Context(), "", core.UserInput{Username: "installation-viewer", Role: "viewer", Enabled: true, Password: rand.Text()}, 0)
	if err != nil {
		t.Fatal(err)
	}
	token, err := server.core.RotateUserToken(t.Context(), viewer.ID, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	before := provider.calls
	for _, path := range []string{"/system/installation", "/system/installation/operations/maintenance-fixture"} {
		request := httptest.NewRequest("GET", "/api/v1"+path, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		reply := httptest.NewRecorder()
		handler.ServeHTTP(reply, request)
		if reply.Code != 403 {
			t.Fatal("non-administrator read system maintenance", reply.Code)
		}
	}
	if provider.calls != before {
		t.Fatal("unauthorized request reached provider")
	}
	router := chi.NewRouter()
	server.registerInstallation(router)
	reply := httptest.NewRecorder()
	router.ServeHTTP(reply, httptest.NewRequest(http.MethodGet, "/system/installation", nil))
	if reply.Code != 403 {
		t.Fatal("missing principal accepted", reply.Code)
	}
}
func TestInstallationUnmanagedAndUnavailableProviderRemainExplicit(t *testing.T) {
	server, handler, _, _ := managedRuntimeFixture(t)
	response := managementRequest(t, handler, "GET", "/system/installation", nil, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "awaiting_adoption") {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, entry := range []struct {
		method, path string
		body         any
	}{{"POST", "/system/installation/plans", map[string]any{"action": "verify", "revision": 1}}, {"POST", "/system/installation/operations", map[string]any{"plan_hash": "old", "revision": 1}}, {"GET", "/system/installation/operations/old", nil}} {
		response = managementRequest(t, handler, entry.method, entry.path, entry.body, nil)
		if response.Code != 409 {
			t.Fatal("unprepared installation offered mutations", response.Code, response.Body.String())
		}
	}
	server.core.Installation = &installationAPIProvider{err: &bootstrap.MaintenanceError{Code: "credential_unconfirmed"}}
}

func TestInventoryCannotClaimManagementWhenInstallationOwnershipIsCorrupt(t *testing.T) {
	server, handler, runtime, _ := managedRuntimeFixture(t)
	input := map[string]any{"runtime_id": runtime.ID, "name": "isolated", "project_name": "isolated", "compose_yaml": "services:\n  web:\n    image: nginx:1.29-alpine\n    expose: ['8080']\n"}
	result := responseData[struct {
		Application model.Application `json:"application"`
	}](t, managementRequest(t, handler, "POST", "/applications", input, map[string]string{"Idempotency-Key": "installation-inventory-fixture"}), 202)
	service := model.Service{ID: "service-installation-fixture", RuntimeID: runtime.ID, Name: "isolated", ServiceKey: "isolated"}
	if err := server.store.UpsertService(t.Context(), &service); err != nil {
		t.Fatal(err)
	}
	document := storage.ControlDocument{ID: bootstrap.InstallationDocumentID, Kind: bootstrap.InstallationDocumentKind, ParentID: "invalid", Payload: []byte("{")}
	if err := server.store.WriteControlDocuments(storage.WithSystemBootstrap(t.Context()), []storage.ControlWrite{{Document: document}}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/applications", "/applications/" + result.Application.ID, "/services", "/services/" + service.ID} {
		response := managementRequest(t, handler, "GET", path, nil, nil)
		if response.Code < 400 || strings.Contains(response.Body.String(), `"management_mode":"system"`) {
			t.Fatal("corrupt ownership displayed as a successful managed inventory", path, response.Code)
		}
	}
}
