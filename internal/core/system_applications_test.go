package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestGatewaySystemApplicationsMigrationAndProtection(t *testing.T) {
	c, reg, objects := systemFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, object := range []string{"controller-object", "database-object"} {
		svc := model.Service{ID: "svc-" + object, RuntimeID: reg.RuntimeID, RuntimeObjectID: object, ServiceKey: object, ExternalID: object, Name: object, RunningInstances: 1, LastSeenAt: &now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
		if e := c.store.UpsertService(ctx, &svc); e != nil {
			t.Fatal(e)
		}
	}
	p, e := c.EnsureGatewayProject(ctx, reg, reg.ClusterID, objects)
	if e != nil {
		t.Fatal(e)
	}
	defs := []config.SystemApplicationConfig{{Key: "controller", Name: "Controller", RuntimeID: reg.RuntimeID, Environment: "prod", ObjectIDs: []string{"controller-object"}}, {Key: "postgresql", Name: "PostgreSQL", RuntimeID: reg.RuntimeID, Environment: "prod", ObjectIDs: []string{"database-object"}}}
	evidence := map[string]map[string]bool{reg.RuntimeID: objects}
	for i := 0; i < 2; i++ {
		if e = c.ReconcileGatewayApplications(ctx, p, reg.ClusterID, defs, evidence); e != nil {
			t.Fatal(e)
		}
	}
	apps, e := c.store.ListApplications(ctx, "", storage.Page{Limit: 200})
	if e != nil || len(apps) != 2 {
		t.Fatal(apps, e)
	}
	c.config.Management.Enabled = true
	for _, a := range apps {
		if !a.System || a.Environment == nil || a.Environment.ID != p.InitialEnvironmentID || a.Managed || a.ActiveRevisionID != "" || a.DesiredRevisionID != "" {
			t.Fatal(a)
		}
		if e = c.store.CheckProjectResource(ctx, a.ID); !errors.Is(e, storage.ErrSystemProtected) {
			t.Fatal(e)
		}
		for _, action := range []string{"START", "STOP", "DOWN", "REDEPLOY"} {
			if _, e = c.QueueApplicationOperation(ctx, a.ID, action, "fixture"); !errors.Is(e, storage.ErrSystemProtected) {
				t.Fatal(action, e)
			}
		}
		if _, _, _, e = c.CreateApplicationRevision(ctx, a.ID, CreateRevisionInput{}, a.Revision); !errors.Is(e, storage.ErrSystemProtected) {
			t.Fatal(e)
		}
		if _, e = c.ApplicationSource(ctx, a.ID, ""); !errors.Is(e, storage.ErrNotFound) {
			t.Fatal(e)
		}
		if e = c.store.SetApplicationStatus(ctx, a.ID, model.ApplicationStopped, "", now); !errors.Is(e, storage.ErrSystemProtected) {
			t.Fatal(e)
		}
	}
	services, e := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{RuntimeID: reg.RuntimeID})
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range services {
		if s.ApplicationID == "" || s.ID != "svc-"+s.RuntimeObjectID {
			t.Fatal(s)
		}
	}
	if _, e = c.EnsureGatewayProject(ctx, reg, reg.ClusterID, objects); e != nil {
		t.Fatal("restart after migration", e)
	}
	// A stale/missing object must not erase prior membership or create half an app.
	bad := append([]config.SystemApplicationConfig{}, defs...)
	bad = append(bad, config.SystemApplicationConfig{Key: "nginx", Name: "NGINX", Environment: "prod", RuntimeID: reg.RuntimeID, ObjectIDs: []string{"missing"}})
	if e = c.ReconcileGatewayApplications(ctx, p, reg.ClusterID, bad, evidence); e == nil {
		t.Fatal("missing evidence accepted")
	}
	after, e := c.store.ListApplications(ctx, "", storage.Page{Limit: 200})
	if e != nil || len(after) != 2 {
		t.Fatal(after, e)
	}
	// An actual discovery snapshot can replace a Docker object, but must retain
	// the application membership and stable NSC service identity.
	epoch, e := c.store.NextObservation(ctx, reg.RuntimeID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot := model.RuntimeSnapshot{RuntimeID: reg.RuntimeID, Observation: epoch, ObservedAt: now.Add(time.Second), Services: []model.DiscoveredService{{ServiceKey: "controller-object", ExternalID: "controller-object", RuntimeObjectID: "controller-replacement", Name: "Controller updated", Protocol: "http", TargetPort: 8080, DesiredInstances: 1}}}
	if _, _, e = c.store.ApplyRuntimeSnapshot(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	changed, e := c.store.GetService(ctx, "svc-controller-object")
	if e != nil || changed.ApplicationID == "" || changed.RuntimeObjectID != "controller-replacement" {
		t.Fatal(changed, e)
	}
	defs[0].ObjectIDs = []string{"controller-replacement"}
	objects["controller-replacement"] = true
	if e = c.ReconcileGatewayApplications(ctx, p, reg.ClusterID, defs, evidence); e != nil {
		t.Fatal(e)
	}
	// Expired observations remain unknown even if the last stored status ran.
	doc, e := c.store.GetControlDocument(ctx, changed.ApplicationID)
	if e != nil {
		t.Fatal(e)
	}
	var metadata map[string]any
	if e = json.Unmarshal(doc.Payload, &metadata); e != nil {
		t.Fatal(e)
	}
	metadata["observed_at"] = now.Add(-2 * time.Minute)
	doc.Payload, _ = json.Marshal(metadata)
	if e = c.store.WriteControlDocuments(storage.WithSystemBootstrap(ctx), []storage.ControlWrite{{Document: doc, Expected: doc.Revision}}); e != nil {
		t.Fatal(e)
	}
	observed, e := c.store.GetApplication(ctx, changed.ApplicationID)
	if e != nil || observed.Status != model.ApplicationUnknown {
		t.Fatal(observed, e)
	}
}

func TestGatewaySystemApplicationRequiresPrivilegedTransaction(t *testing.T) {
	c, reg, objects := systemFixture(t)
	ctx := context.Background()
	p, e := c.EnsureGatewayProject(ctx, reg, reg.ClusterID, objects)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.store.ReconcileSystemApplications(ctx, p.ID, nil, nil); !errors.Is(e, storage.ErrSystemProtected) {
		t.Fatal(e)
	}
}

func TestGatewaySystemApplicationsCrossRuntimeAndAtomicFailure(t *testing.T) {
	c, reg, objects := systemFixture(t)
	ctx := context.Background()
	p, e := c.EnsureGatewayProject(ctx, reg, reg.ClusterID, objects)
	if e != nil {
		t.Fatal(e)
	}
	original, e := c.store.GetRuntime(ctx, reg.RuntimeID)
	if e != nil {
		t.Fatal(e)
	}
	rt := original
	rt.ID = "docker-node"
	rt.Name = "node"
	rt.Type = model.RuntimeDocker
	rt.Endpoint = "node://local"
	if e = c.store.CreateRuntime(ctx, &rt); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	defs := []config.SystemApplicationConfig{}
	evidence := map[string]map[string]bool{rt.ID: {}}
	for _, v := range []struct{ key, name, env string }{{"nginx", "NGINX", "prod"}, {"nginx-sandbox", "NGINX Sandbox", "sandbox"}} {
		svc := model.Service{ID: "svc-" + v.key, RuntimeID: rt.ID, ServiceKey: "compose/" + v.key, ExternalID: "compose/" + v.key, Name: v.name, RunningInstances: 1, LastSeenAt: &now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
		if e = c.store.UpsertService(ctx, &svc); e != nil {
			t.Fatal(e)
		}
		ep := model.Endpoint{ID: "ep-" + v.key, ServiceID: svc.ID, ExternalID: "container-" + v.key, RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, ReadyState: model.ReadyStarting, LastSeenAt: now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
		if e = c.store.UpsertEndpoint(ctx, &ep); e != nil {
			t.Fatal(e)
		}
		defs = append(defs, config.SystemApplicationConfig{Key: v.key, Name: v.name, Environment: v.env, RuntimeID: rt.ID, ObjectIDs: []string{ep.ExternalID}})
		evidence[rt.ID][ep.ExternalID] = true
	}
	if e = c.ReconcileGatewayApplications(ctx, p, "wrong-cluster", defs, evidence); !errors.Is(e, storage.ErrConflict) {
		t.Fatal(e)
	}
	// Failure on the last object cannot create the first application or Sandbox.
	evidence[rt.ID]["container-nginx-sandbox"] = false
	if e = c.ReconcileGatewayApplications(ctx, p, reg.ClusterID, defs, evidence); e == nil {
		t.Fatal("incomplete evidence accepted")
	}
	apps, e := c.store.ListApplications(ctx, "", storage.Page{Limit: 200})
	if e != nil || len(apps) != 0 {
		t.Fatal(apps, e)
	}
	envs, e := c.allProjectEnvironments(ctx, p.ID)
	if e != nil || len(envs) != 1 {
		t.Fatal(envs, e)
	}
	evidence[rt.ID]["container-nginx-sandbox"] = true
	sibling := model.Endpoint{ID: "ep-nginx-second", ServiceID: "svc-nginx", ExternalID: "container-nginx-second", RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, ReadyState: model.ReadyStarting, LastSeenAt: now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if e = c.store.UpsertEndpoint(ctx, &sibling); e != nil {
		t.Fatal(e)
	}
	if e = c.ReconcileGatewayApplications(ctx, p, reg.ClusterID, defs, evidence); e == nil {
		t.Fatal("unregistered sibling was adopted")
	}
	defs[0].ObjectIDs = append(defs[0].ObjectIDs, sibling.ExternalID)
	evidence[rt.ID][sibling.ExternalID] = true
	if e = c.ReconcileGatewayApplications(ctx, p, reg.ClusterID, defs, evidence); e != nil {
		t.Fatal(e)
	}
	apps, e = c.store.ListApplications(ctx, "", storage.Page{Limit: 200})
	if e != nil || len(apps) != 2 {
		t.Fatal(apps, e)
	}
	for _, a := range apps {
		if a.RuntimeID != rt.ID || !a.System || a.Environment == nil {
			t.Fatal(a)
		}
		if a.SystemKey == "nginx" && a.Environment.ID != p.InitialEnvironmentID {
			t.Fatal(a)
		}
	}
	prod, e := c.GetEnvironment(ctx, p.InitialEnvironmentID)
	if e != nil || prod.RuntimeID != reg.RuntimeID {
		t.Fatal(prod, e)
	}
	refs, e := c.store.ServiceEnvironments(ctx, []string{"svc-nginx", "svc-nginx-sandbox"})
	if e != nil || len(refs) != 2 || refs["svc-nginx"].ID == refs["svc-nginx-sandbox"].ID {
		t.Fatal(refs, e)
	}
	if e = c.validateApplicationProjectClaim(ctx, rt, "compose", nil, true); !errors.Is(e, storage.ErrSystemProtected) {
		t.Fatal("ordinary adoption bypass", e)
	}
	for _, sid := range []string{"svc-nginx", "svc-nginx-sandbox"} {
		if e = c.store.CheckProjectResource(ctx, sid); !errors.Is(e, storage.ErrSystemProtected) {
			t.Fatal(e)
		}
	}
}

func TestGatewaySystemApplicationTransactionRollback(t *testing.T) {
	c, reg, objects := systemFixture(t)
	ctx := context.Background()
	p, e := c.EnsureGatewayProject(ctx, reg, reg.ClusterID, objects)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	svc := model.Service{ID: "svc-rollback", RuntimeID: reg.RuntimeID, ServiceKey: "rollback", ExternalID: "rollback", Name: "rollback", Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if e = c.store.UpsertService(ctx, &svc); e != nil {
		t.Fatal(e)
	}
	app := model.Application{ID: "app-rollback", RuntimeID: reg.RuntimeID, Name: "Controller", ProjectName: "gateway-controller", System: true, Status: model.ApplicationUnknown, Environment: &model.EnvironmentReference{ID: p.InitialEnvironmentID, ProjectID: p.ID}, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	stale := storage.SystemApplicationRegistration{Application: app, ServiceIDs: []string{svc.ID}, ServiceRevisions: map[string]int64{svc.ID: svc.Revision + 1}}
	if e = c.store.ReconcileSystemApplications(storage.WithSystemBootstrap(ctx), p.ID, []storage.SystemApplicationRegistration{stale}, nil); !errors.Is(e, storage.ErrPrecondition) {
		t.Fatal(e)
	}
	// A conflicting document fails after insertion and association inside the transaction.
	doc, e := c.store.GetControlDocument(ctx, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	e = c.store.ReconcileSystemApplications(storage.WithSystemBootstrap(ctx), p.ID, []storage.SystemApplicationRegistration{{Application: app, ServiceIDs: []string{svc.ID}}}, []storage.ControlWrite{{Document: doc}})
	if e == nil {
		t.Fatal("conflicting write succeeded")
	}
	if _, e = c.store.GetApplication(ctx, app.ID); !errors.Is(e, storage.ErrNotFound) {
		t.Fatal(e)
	}
	saved, e := c.store.GetService(ctx, svc.ID)
	if e != nil || saved.ApplicationID != "" {
		t.Fatal(saved, e)
	}
}

func TestSystemApplicationReplacementDetachesOnlyRetiredMembership(t *testing.T) {
	c, reg, objects := systemFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	old := model.Service{ID: "svc-old", RuntimeID: reg.RuntimeID, RuntimeObjectID: "controller-object", ServiceKey: "old", ExternalID: "old", Name: "old", Status: model.ServiceAvailable, LastSeenAt: &now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	must(c.store.UpsertService(ctx, &old))
	p, err := c.EnsureGatewayProject(ctx, reg, reg.ClusterID, objects)
	must(err)
	defs := []config.SystemApplicationConfig{{Key: "controller", Name: "Controller", RuntimeID: reg.RuntimeID, Environment: "prod", ObjectIDs: []string{"controller-object"}}}
	evidence := map[string]map[string]bool{reg.RuntimeID: objects}
	must(c.ReconcileGatewayApplications(ctx, p, reg.ClusterID, defs, evidence))
	old, err = c.store.GetService(ctx, old.ID)
	must(err)
	appID := old.ApplicationID
	// Discovery, not a name-based database cleanup, confirms retirement.
	epoch, err := c.store.NextObservation(ctx, reg.RuntimeID)
	must(err)
	_, _, err = c.store.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: reg.RuntimeID, Observation: epoch, ObservedAt: now, Services: []model.DiscoveredService{{ServiceKey: "new", ExternalID: "new", RuntimeObjectID: "new-object", Name: "new", Protocol: "http", TargetPort: 8080}}})
	must(err)
	old, err = c.store.GetService(ctx, old.ID)
	must(err)
	if old.Status != model.ServiceRemoved || old.ApplicationID != appID {
		t.Fatal(old)
	}
	// A failed replacement keeps membership and historical protection intact.
	defs[0].ObjectIDs = []string{"missing-object"}
	if err = c.ReconcileGatewayApplications(ctx, p, reg.ClusterID, defs, evidence); err == nil {
		t.Fatal("missing evidence accepted")
	}
	old, err = c.store.GetService(ctx, old.ID)
	must(err)
	if old.ApplicationID != appID {
		t.Fatal("failed replacement detached history")
	}
	// An existing stopped object and an unexpectedly missing managed service keep ownership.
	for _, v := range []struct {
		id      string
		managed bool
		status  model.ServiceStatus
	}{{"svc-current", false, model.ServiceUnavailable}, {"svc-managed", true, model.ServiceRemoved}} {
		svc := model.Service{ID: v.id, RuntimeID: reg.RuntimeID, ServiceKey: v.id, ExternalID: v.id, Name: v.id, ApplicationID: appID, Managed: v.managed, Status: v.status, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
		must(c.store.UpsertService(storage.WithSystemBootstrap(ctx), &svc))
	}
	app, err := c.store.GetApplication(ctx, appID)
	must(err)
	newServices, err := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{RuntimeID: reg.RuntimeID, Search: "new"})
	must(err)
	if len(newServices) != 1 {
		t.Fatal(newServices)
	}
	plan := storage.SystemApplicationRegistration{Application: app, ServiceIDs: []string{newServices[0].ID}, ServiceRevisions: map[string]int64{newServices[0].ID: newServices[0].Revision + 100}}
	if err = c.store.ReconcileSystemApplications(storage.WithSystemBootstrap(ctx), p.ID, []storage.SystemApplicationRegistration{plan}, nil); err == nil {
		t.Fatal("stale replacement accepted")
	}
	old, err = c.store.GetService(ctx, old.ID)
	must(err)
	if old.ApplicationID != appID {
		t.Fatal("transaction failure detached history")
	}
	defs[0].ObjectIDs = []string{"new-object"}
	objects["new-object"] = true
	must(c.ReconcileGatewayApplications(ctx, p, reg.ClusterID, defs, evidence))
	old, err = c.store.GetService(ctx, old.ID)
	must(err)
	if old.ApplicationID != "" || old.Status != model.ServiceRemoved {
		t.Fatal(old)
	}
	claim, err := c.store.GetControlDocument(ctx, "controller-object")
	must(err)
	if claim.Kind != "system-service" || claim.ParentID != reg.RuntimeID {
		t.Fatal("retired object lost protection", claim)
	}
	for _, sid := range []string{"svc-current", "svc-managed"} {
		v, e := c.store.GetService(ctx, sid)
		must(e)
		if v.ApplicationID != appID {
			t.Fatal(v)
		}
	}
	services, err := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: appID})
	must(err)
	if len(services) != 3 {
		t.Fatal(services)
	}
}
