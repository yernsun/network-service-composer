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

func systemApplicationsAPIFixture(t *testing.T) (*Server, http.Handler, core.Project, core.Environment) {
	t.Helper()
	s, h, p, prod, svc := systemAPIFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	sandbox := prod
	sandbox.ID = "env_sandbox"
	sandbox.Name = "sandbox"
	sandbox.ProjectName = "gateway-sandbox"
	sandbox.Kind = "sandbox"
	writes := []storage.ControlWrite{}
	add := func(key, kind, parent string, value any) {
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		writes = append(writes, storage.ControlWrite{Document: storage.ControlDocument{ID: key, Kind: kind, ParentID: parent, Payload: raw, CreatedAt: now, UpdatedAt: now}})
	}
	add(sandbox.ID, "environment", p.ID, sandbox)
	plans := []storage.SystemApplicationRegistration{}
	for _, v := range []struct{ key, name string }{{"controller", "Controller"}, {"postgresql", "PostgreSQL"}, {"nginx", "NGINX"}, {"nginx-sandbox", "NGINX Sandbox"}} {
		env := prod
		if v.key == "nginx-sandbox" {
			env = sandbox
		}
		ref := model.EnvironmentReference{ID: env.ID, Name: env.Name, ProjectID: p.ID, ProjectName: env.ProjectName, System: true}
		app := model.Application{ID: "app_" + v.key, RuntimeID: prod.RuntimeID, Name: v.name, ProjectName: "gateway-" + v.key, System: true, Environment: &ref, SystemKey: v.key, Status: model.ApplicationRunning, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
		service := svc
		if v.key != "controller" {
			service.ID = "svc_" + v.key
			service.Name = v.name
			service.ServiceKey = v.key
			service.ExternalID = v.key
			service.RuntimeObjectID = v.key
			if e := s.store.UpsertService(ctx, &service); e != nil {
				t.Fatal(e)
			}
		}
		add(app.ID, "system-application", env.ID, map[string]any{"system": true, "key": v.key, "environment": ref, "observed_at": now})
		add("application-environment:"+app.ID, "application-environment", app.ID, map[string]string{"environment_id": env.ID})
		plans = append(plans, storage.SystemApplicationRegistration{Application: app, ServiceIDs: []string{service.ID}})
	}
	if e := s.store.ReconcileSystemApplications(storage.WithSystemBootstrap(ctx), p.ID, plans, writes); e != nil {
		t.Fatal(e)
	}
	return s, h, p, prod
}

func TestSystemApplicationAPIListsRealAssociationsAndRejectsOperations(t *testing.T) {
	_, h, _, _ := systemApplicationsAPIFixture(t)
	apps := responseData[[]model.Application](t, managementRequest(t, h, "GET", "/applications", nil, nil), 200)
	if len(apps) != 4 {
		t.Fatal(apps)
	}
	for _, app := range apps {
		if !app.System || app.Managed || app.Environment == nil || len(app.LifecycleOperations) > 0 {
			t.Fatal(app)
		}
		detail := responseData[model.Application](t, managementRequest(t, h, "GET", "/applications/"+app.ID, nil, nil), 200)
		if detail.SystemKey != app.SystemKey {
			t.Fatal(detail)
		}
		for _, action := range []string{"START", "STOP", "DOWN", "REDEPLOY"} {
			r := managementRequest(t, h, "POST", "/applications/"+app.ID+"/operations", map[string]string{"action": action}, map[string]string{"Idempotency-Key": "fixture-" + app.ID + action})
			if r.Code != 403 {
				t.Fatalf("%s %s: %d %s", app.ID, action, r.Code, r.Body)
			}
			var problem map[string]any
			if e := json.Unmarshal(r.Body.Bytes(), &problem); e != nil {
				t.Fatal(e)
			}
			if problem["code"] != "system.project_protected" {
				t.Fatal(problem)
			}
		}
		revisions := responseData[[]model.ApplicationRevision](t, managementRequest(t, h, "GET", "/applications/"+app.ID+"/revisions", nil, nil), 200)
		if len(revisions) != 0 {
			t.Fatal(revisions)
		}
	}
	services := responseData[[]model.Service](t, managementRequest(t, h, "GET", "/services", nil, nil), 200)
	for _, svc := range services {
		if svc.ApplicationID == "" || svc.Environment == nil || !svc.System {
			t.Fatal(svc)
		}
	}
}
