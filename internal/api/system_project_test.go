package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func systemAPIFixture(t *testing.T) (*Server, http.Handler, core.Project, core.Environment, model.Service) {
	t.Helper()
	s, h, rt, _ := managedRuntimeFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	p := core.Project{Name: "Gateway", Slug: "gateway", System: true, DefaultRuntimeID: rt.ID}
	p.ID = "prj_system"
	e := core.Environment{ProjectID: p.ID, Name: "prod", System: true, RuntimeID: rt.ID, ProjectName: "gateway-prod"}
	e.ID = "env_system"
	writes := []storage.ControlWrite{}
	add := func(key, kind, parent string, value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		writes = append(writes, storage.ControlWrite{Document: storage.ControlDocument{ID: key, Kind: kind, ParentID: parent, Payload: raw, CreatedAt: now, UpdatedAt: now}})
	}
	add(p.ID, "project", "", p)
	add(e.ID, "environment", p.ID, e)
	add("runtime-object", "system-service", rt.ID, storage.SystemService{EnvironmentID: e.ID, ProjectID: p.ID, ClusterID: "cluster", System: true})
	if err := s.store.WriteControlDocuments(storage.WithSystemBootstrap(ctx), writes); err != nil {
		t.Fatal(err)
	}
	svc := model.Service{ID: "svc_system", RuntimeID: rt.ID, RuntimeObjectID: "runtime-object", ServiceKey: "nsc", ExternalID: "nsc", Name: "Controller", Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := s.store.UpsertService(ctx, &svc); err != nil {
		t.Fatal(err)
	}
	return s, h, p, e, svc
}
func TestSystemProjectAPIProtectionAndReadOnlyReferences(t *testing.T) {
	_, h, p, e, svc := systemAPIFixture(t)
	got := responseData[core.Project](t, managementRequest(t, h, "GET", "/projects/"+p.ID, nil, nil), 200)
	if !got.System || len(got.LifecycleOperations) != 0 {
		t.Fatal(got)
	}
	service := responseData[model.Service](t, managementRequest(t, h, "GET", "/services/"+svc.ID, nil, nil), 200)
	if !service.System || service.Environment == nil || service.Environment.ProjectID != p.ID || len(service.LifecycleOperations) != 0 {
		t.Fatal(service)
	}
	headers := map[string]string{"If-Match": "\"rev-1\"", "Idempotency-Key": "system-protection"}
	for _, test := range []struct {
		method, path string
		body         any
	}{
		{"PATCH", "/projects/" + p.ID, map[string]string{"name": "rename"}},
		{"POST", "/projects/" + p.ID + "/deletion-previews", map[string]bool{"force": false}},
		{"POST", "/projects/" + p.ID + "/deletion-previews", map[string]bool{"force": true}},
		{"DELETE", "/projects/" + p.ID, core.ProjectDeletionInput{PreviewID: "forged", ConfirmSlug: "gateway", Force: true}},
		{"DELETE", "/environments/" + e.ID, nil},
		{"PATCH", "/services/" + svc.ID, map[string]any{"enabled_override": false}},
		{"POST", "/services/" + svc.ID + "/operations", map[string]any{"action": "restart"}},
	} {
		response := managementRequest(t, h, test.method, test.path, test.body, headers)
		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d %s", test.method, test.path, response.Code, response.Body)
		}
	}
	responseData[core.Project](t, managementRequest(t, h, "GET", "/projects/"+p.ID, nil, nil), 200)
	responseData[model.Service](t, managementRequest(t, h, "GET", "/services/"+svc.ID, nil, nil), 200)
}
