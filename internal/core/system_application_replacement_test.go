package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestGatewayNGINXMovesStoppedStandaloneToFourSwarmTasks(t *testing.T) {
	c, reg, objects := systemFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	project, err := c.EnsureGatewayProject(ctx, reg, reg.ClusterID, objects)
	must(err)
	rt, err := c.store.GetRuntime(ctx, reg.RuntimeID)
	must(err)
	oldRuntime := rt
	oldRuntime.ID = "previous-docker"
	oldRuntime.Name = "previous"
	oldRuntime.Type = model.RuntimeDocker
	oldRuntime.Endpoint = "node://fixture"
	must(c.store.CreateRuntime(ctx, &oldRuntime))
	old := model.Service{ID: "old-nginx", RuntimeID: oldRuntime.ID, ServiceKey: "old", ExternalID: "old", RuntimeObjectID: "old-container", Name: "gateway", RunningInstances: 1, LastSeenAt: &now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	must(c.store.UpsertService(ctx, &old))
	endpoint := model.Endpoint{ID: "old-endpoint", ServiceID: old.ID, ExternalID: "old-container", RuntimeState: model.EndpointRunning, ReadyState: model.Ready, LastSeenAt: now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	must(c.store.UpsertEndpoint(storage.WithSystemBootstrap(ctx), &endpoint))
	definition := config.SystemApplicationConfig{Key: "nginx", Name: "NGINX", Environment: "prod", RuntimeID: oldRuntime.ID, ObjectIDs: []string{endpoint.ExternalID}}
	evidence := map[string]map[string]bool{oldRuntime.ID: {endpoint.ExternalID: true}, rt.ID: {"new-global": true}}
	must(c.ReconcileGatewayApplications(ctx, project, reg.ClusterID, []config.SystemApplicationConfig{definition}, evidence))
	old, err = c.store.GetService(ctx, old.ID)
	must(err)
	appID := old.ApplicationID
	current := model.Service{ID: "new-nginx", RuntimeID: rt.ID, ServiceKey: "nsc_gateway", ExternalID: "nsc_gateway", RuntimeObjectID: "new-global", Name: "nsc_gateway", DesiredInstances: 4, RunningInstances: 4, ReadyInstances: 4, LastSeenAt: &now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	must(c.store.UpsertService(ctx, &current))
	definition.RuntimeID = rt.ID
	definition.ObjectIDs = []string{"new-global"}
	if err = c.ReconcileGatewayApplications(ctx, project, reg.ClusterID, []config.SystemApplicationConfig{definition}, evidence); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("implicit runtime takeover: %v", err)
	}
	definition.ReplacesRuntimeID = oldRuntime.ID
	definition.ReplacesObjectIDs = []string{endpoint.ExternalID}
	stopped := map[string]map[string]bool{oldRuntime.ID: {endpoint.ExternalID: true}}
	// Even a contradictory stopped claim cannot transfer a still-running inventory row.
	if err = c.ReconcileGatewayApplicationsWithRetirements(ctx, project, reg.ClusterID, []config.SystemApplicationConfig{definition}, evidence, stopped); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("running service transferred: %v", err)
	}
	old.RunningInstances = 0
	must(c.store.UpsertService(storage.WithSystemBootstrap(ctx), &old))
	endpoint.RuntimeState = model.EndpointDead
	endpoint.ReadyState = model.NotReady
	must(c.store.UpsertEndpoint(storage.WithSystemBootstrap(ctx), &endpoint))
	if err = c.ReconcileGatewayApplications(ctx, project, reg.ClusterID, []config.SystemApplicationConfig{definition}, evidence); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("missing live stop evidence accepted: %v", err)
	}
	nextApp, err := c.store.GetApplication(ctx, appID)
	must(err)
	nextApp.RuntimeID = rt.ID
	replacement, err := c.systemApplicationReplacement(ctx, nextApp, definition, stopped)
	must(err)
	replacement.ExpectedRevision++
	err = c.store.ReconcileSystemApplications(storage.WithSystemBootstrap(ctx), project.ID, []storage.SystemApplicationRegistration{{Application: nextApp, ServiceIDs: []string{current.ID}, Replacement: replacement}}, nil)
	if !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale application transfer: %v", err)
	}
	kept, err := c.store.GetService(ctx, old.ID)
	must(err)
	if kept.ApplicationID != appID {
		t.Fatal("failed transfer detached old membership")
	}
	must(c.ReconcileGatewayApplicationsWithRetirements(ctx, project, reg.ClusterID, []config.SystemApplicationConfig{definition}, evidence, stopped))
	must(c.ReconcileGatewayApplicationsWithRetirements(ctx, project, reg.ClusterID, []config.SystemApplicationConfig{definition}, evidence, stopped))
	app, err := c.store.GetApplication(ctx, appID)
	must(err)
	if app.ID != appID || app.RuntimeID != rt.ID || !app.System || app.Status != model.ApplicationRunning {
		t.Fatalf("wrong application after transfer: %+v", app)
	}
	linked, err := c.store.GetService(ctx, current.ID)
	must(err)
	kept, err = c.store.GetService(ctx, old.ID)
	must(err)
	if linked.ApplicationID != appID || linked.RunningInstances != 4 || kept.ApplicationID != "" || kept.RuntimeObjectID != "old-container" {
		t.Fatal("incorrect four-task membership or lost history")
	}
	for _, key := range []string{app.ID, linked.ID, kept.ID} {
		if err = c.store.CheckProjectResource(ctx, key); !errors.Is(err, storage.ErrSystemProtected) {
			t.Fatalf("protection lost for %s: %v", key, err)
		}
	}
	_, err = c.store.GetControlDocument(ctx, "system-application-retirement:"+app.ID+":"+oldRuntime.ID)
	must(err)
}
