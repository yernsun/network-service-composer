package core

import (
	"context"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/cluster"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/runtime/engine/enginetest"
	"github.com/yernsun/network-service-composer/internal/storage"
	"strings"
	"testing"
	"time"
)

func TestIncrementalReviewFreezesScopeAndPreservesOriginalRevision(t *testing.T) {
	c, rt, _ := controlFixture(t)
	sql := c.store.(*storage.SQLStore)
	rt.Type = model.RuntimeSwarm
	c.store = networkRuntimeStore{Store: sql, runtime: rt}
	client := &enginetest.Client{System: system.Info{Swarm: swarm.Info{ControlAvailable: true}}}
	c.Cluster = cluster.NewWithFactory(c.store, c.config, func(model.Runtime, engine.TLSFiles) (engine.MonitoringClient, error) { return client, nil })
	ctx := context.Background()
	p, err := c.CreateProject(ctx, "Incremental", "incremental")
	if err != nil {
		t.Fatal(err)
	}
	env, err := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "prod", RuntimeID: rt.ID})
	if err != nil {
		t.Fatal(err)
	}
	image := "example.test/web@sha256:" + strings.Repeat("a", 64)
	original := "# keep source\nservices:\n  web:\n    image: " + image + "\n"
	preview, err := c.ReviewEnvironmentInput(ctx, env.ID, ComposeValidationInput{Compose: original, ServiceMode: "create"})
	if err != nil {
		t.Fatal(err)
	}
	if preview["network_name"] != "incremental" {
		t.Fatalf("project network default: %v", preview["network_name"])
	}
	op, err := c.DeployReviewedEnvironment(ctx, env.ID, preview["preflight_id"].(string), "test", "install", env.Revision)
	if err != nil {
		t.Fatal(err)
	}
	app, err := c.store.GetApplication(ctx, op.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	if err = sql.ActivateApplicationRevision(ctx, rt.ID, app.ID, op.RevisionID, op.RevisionID, []string{env.ProjectName + "_web"}, nil, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?", op.ID); err != nil {
		t.Fatal(err)
	}
	source, err := c.ApplicationSource(ctx, app.ID, op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	env, err = c.GetEnvironment(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	next := "services:\n  hello:\n    image: " + image + "\n    deploy: {replicas: 2}\n"
	review, err := c.ReviewEnvironmentInput(ctx, env.ID, ComposeValidationInput{Compose: next, ServiceMode: "create"})
	if err != nil {
		t.Fatal(err)
	}
	changes := review["service_changes"]
	if changes == nil {
		t.Fatal("missing scope diff")
	}
	added, err := c.DeployReviewedEnvironment(ctx, env.ID, review["preflight_id"].(string), "test", "append", env.Revision)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := c.store.GetApplicationRevision(ctx, added.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.ConfigurationSource.ServiceScope == nil || len(rev.ConfigurationSource.ServiceScope.Names) != 1 || rev.ConfigurationSource.ServiceScope.Names[0] != "hello" {
		t.Fatal("execution scope expanded")
	}
	merged, err := c.ApplicationSource(ctx, app.ID, rev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(merged.ComposeYAML, "web:") || !strings.Contains(merged.ComposeYAML, "hello:") || !strings.Contains(merged.ComposeYAML, "# keep source") {
		t.Fatal("aggregate source lost retained service")
	}
	preserved, err := c.ApplicationSource(ctx, app.ID, op.RevisionID)
	if err != nil || preserved.ComposeYAML != source.ComposeYAML {
		t.Fatal("historical source mutated")
	}
	if _, err = c.DeployReviewedEnvironment(ctx, env.ID, review["preflight_id"].(string), "test", "stale", env.Revision); err == nil {
		t.Fatal("stale review accepted")
	}
}

func TestIncrementalLegacyNetworkSurvivesWorkerAdmission(t *testing.T) {
	c, rt, pool := controlFixture(t)
	sql := c.store.(*storage.SQLStore)
	rt.Type = model.RuntimeSwarm
	c.store = networkRuntimeStore{Store: sql, runtime: rt}
	client := &enginetest.Client{System: system.Info{Swarm: swarm.Info{ControlAvailable: true}}}
	c.Cluster = cluster.NewWithFactory(c.store, c.config, func(model.Runtime, engine.TLSFiles) (engine.MonitoringClient, error) { return client, nil })
	ctx := context.Background()
	p, err := c.CreateProject(ctx, "Legacy", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	env, err := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "prod", RuntimeID: rt.ID})
	if err != nil {
		t.Fatal(err)
	}
	image := "example.test/web@sha256:" + strings.Repeat("a", 64)
	review, err := c.ReviewEnvironment(ctx, env.ID, "services: {old: {image: '"+image+"'}}")
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.DeployReviewedEnvironment(ctx, env.ID, review["preflight_id"].(string), "test", "legacy", env.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = sql.ActivateApplicationRevision(ctx, rt.ID, op.ApplicationID, op.RevisionID, op.RevisionID, []string{env.ProjectName + "_old"}, nil, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?", op.ID); err != nil {
		t.Fatal(err)
	}
	env, err = c.GetEnvironment(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	partial := "services: {hello: {image: '" + image + "'}}"
	if _, err = c.ReviewEnvironment(ctx, env.ID, partial); err == nil {
		t.Fatal("legacy full-list review accepted an implicit service removal")
	}
	app, err := c.store.GetApplication(ctx, op.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = c.CreateApplicationRevision(ctx, app.ID, CreateRevisionInput{ApplicationPreflightInput: ApplicationPreflightInput{ComposeYAML: partial}, IdempotencyKey: "unreviewed-removal"}, app.Revision); err == nil {
		t.Fatal("direct revision API accepted an implicit service removal")
	}
	review, err = c.ReviewEnvironmentInput(ctx, env.ID, ComposeValidationInput{Compose: partial, ServiceMode: "create"})
	if err != nil {
		t.Fatal(err)
	}
	op, err = c.DeployReviewedEnvironment(ctx, env.ID, review["preflight_id"].(string), "test", "add", env.Revision)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := c.store.GetApplicationRevision(ctx, op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := c.keyring.Decrypt(revision.EncryptedResolved, revisionAAD(revision.ID, "resolved"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := applicationx.Preflight(applicationx.PreflightInput{ComposeYAML: applicationx.EscapeForComposeCLI(string(raw)), Backend: rt.Type, ProtectedNetworks: []string{pool.BackendNetwork}})
	if err != nil {
		t.Fatalf("Worker cannot admit effective source: %v", err)
	}
	if len(result.Services) != 2 || !strings.Contains(string(raw), "name: "+env.ProjectName) || !strings.Contains(string(raw), "name: legacy") {
		t.Fatal("old/new network identity lost")
	}
}

func TestFailedInitialServiceCanBeReviewedWithoutInventingDeployedBaseline(t *testing.T) {
	c, rt, _ := controlFixture(t)
	sql := c.store.(*storage.SQLStore)
	rt.Type = model.RuntimeSwarm
	c.store = networkRuntimeStore{Store: sql, runtime: rt}
	client := &enginetest.Client{System: system.Info{Swarm: swarm.Info{ControlAvailable: true}}}
	c.Cluster = cluster.NewWithFactory(c.store, c.config, func(model.Runtime, engine.TLSFiles) (engine.MonitoringClient, error) { return client, nil })
	ctx := context.Background()
	project, err := c.CreateProject(ctx, "Retry", "retry")
	if err != nil {
		t.Fatal(err)
	}
	env, err := c.CreateEnvironment(ctx, project.ID, EnvironmentInput{Name: "prod", RuntimeID: rt.ID})
	if err != nil {
		t.Fatal(err)
	}
	raw := "services: {hello: {image: 'example.test/web@sha256:" + strings.Repeat("a", 64) + "'}}"
	review, err := c.ReviewEnvironmentInput(ctx, env.ID, ComposeValidationInput{Compose: raw, ServiceMode: "create"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := c.DeployReviewedEnvironment(ctx, env.ID, review["preflight_id"].(string), "test", "first", env.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='FAILED' WHERE id=?", first.ID); err != nil {
		t.Fatal(err)
	}
	source, err := c.ApplicationSource(ctx, first.ApplicationID, "")
	if err != nil || source.BaselineRevisionID != "" || source.RevisionID != first.RevisionID {
		t.Fatal("failed desired source presented as deployed", err)
	}
	env, err = c.GetEnvironment(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := c.ReviewEnvironmentInput(ctx, env.ID, ComposeValidationInput{Compose: raw, ServiceMode: "update"})
	if err != nil {
		t.Fatal("unchanged failed input cannot be retried", err)
	}
	second, err := c.DeployReviewedEnvironment(ctx, env.ID, retry["preflight_id"].(string), "test", "retry", env.Revision)
	if err != nil || second.ID == first.ID {
		t.Fatal("fresh review did not create distinct recovery operation", err)
	}
	prior, err := c.store.GetDeploymentOperation(ctx, first.ID)
	if err != nil || prior.Status != model.OperationFailed {
		t.Fatal("failure history changed", err)
	}
}
