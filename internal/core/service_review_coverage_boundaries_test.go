package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/cluster"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/runtime/engine/enginetest"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type serviceReviewBoundaryFixture struct {
	core       *Core
	sql        *storage.SQLStore
	env        Environment
	project    Project
	runtime    model.Runtime
	client     *enginetest.Client
	op         model.DeploymentOperation
	initial    string
	runtimeEnv string
}

func serviceReviewBoundarySetup(t *testing.T) serviceReviewBoundaryFixture {
	t.Helper()
	c, rt, _ := controlFixture(t)
	sql := c.store.(*storage.SQLStore)
	rt.Type = model.RuntimeSwarm
	c.store = networkRuntimeStore{Store: sql, runtime: rt}
	client := &enginetest.Client{System: system.Info{Swarm: swarm.Info{ControlAvailable: true}}}
	c.Cluster = cluster.NewWithFactory(c.store, c.config, func(model.Runtime, engine.TLSFiles) (engine.MonitoringClient, error) { return client, nil })
	ctx := t.Context()
	p, err := c.CreateProject(ctx, "Service review", "service-review")
	if err != nil {
		t.Fatal(err)
	}
	env, err := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "prod", RuntimeID: rt.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.SaveVariables(ctx, p.ID, VariableInput{Values: []Variable{{Key: "TOKEN", Value: pointer("retained-private-token"), Secret: true}}}, 0); err != nil {
		t.Fatal(err)
	}
	initial := "services:\n  web:\n    image: example.test/web@sha256:" + strings.Repeat("a", 64) + "\n  api:\n    image: example.test/api@sha256:" + strings.Repeat("b", 64) + "\n    environment: {TOKEN: '${TOKEN}'}\n"
	review, err := c.ReviewEnvironmentInput(ctx, env.ID, ComposeValidationInput{Compose: initial, ServiceMode: "create"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.DeployReviewedEnvironment(ctx, env.ID, review["preflight_id"].(string), "fixture", "install-review", env.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = sql.ActivateApplicationRevision(ctx, rt.ID, op.ApplicationID, op.RevisionID, op.RevisionID, []string{env.ProjectName + "_web", env.ProjectName + "_api"}, nil, "", c.now()); err != nil {
		t.Fatal(err)
	}
	if _, err = sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?", op.ID); err != nil {
		t.Fatal(err)
	}
	env, err = c.GetEnvironment(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"web", "api"} {
		client.Services = append(client.Services, swarm.Service{ID: "docker-" + name, Meta: swarm.Meta{Version: swarm.Version{Index: 4}}, Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: env.ProjectName + "_" + name, Labels: map[string]string{"com.docker.stack.namespace": env.ProjectName}}}})
	}
	source, err := c.ApplicationSource(ctx, op.ApplicationID, op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	return serviceReviewBoundaryFixture{c, sql, env, p, rt, client, op, initial, source.RuntimeEnv}
}

func (f serviceReviewBoundaryFixture) candidate() string {
	return "services:\n  web:\n    image: example.test/web@sha256:" + strings.Repeat("c", 64) + "\n"
}

func TestServiceReviewPermissionsFreezeAddedAndUpdatedScope(t *testing.T) {
	f := serviceReviewBoundarySetup(t)
	ctx := t.Context()
	raw := f.candidate() + "  worker:\n    image: example.test/worker@sha256:" + strings.Repeat("d", 64) + "\n"
	input := ComposeValidationInput{Compose: raw, ServiceMode: "update", RuntimeEnv: f.runtimeEnv}
	for _, scopes := range [][]string{{"services.create"}, {"services.edit"}, {}} {
		if _, err := f.core.ReviewEnvironmentInputScoped(ctx, f.env.ID, input, scopes); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("partial permissions accepted: %v", err)
		}
	}
	scopes := []string{"services.create", "services.edit", "services.deploy"}
	review, err := f.core.ReviewEnvironmentInputScoped(ctx, f.env.ID, input, scopes)
	if err != nil {
		t.Fatal(err)
	}
	id := review["preflight_id"].(string)
	if _, err = f.core.DeployReviewedEnvironmentScoped(ctx, f.env.ID, id, "fixture", "missing-permission", f.env.Revision, []string{"services.deploy", "services.edit"}); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deployment dropped frozen permissions: %v", err)
	}
	if _, err = f.core.DeployReviewedEnvironmentScoped(ctx, f.env.ID, id, "fixture", "missing-deploy", f.env.Revision, []string{"services.edit", "services.create"}); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing deploy permission accepted: %v", err)
	}
	before, err := f.core.ApplicationSource(ctx, f.op.ApplicationID, f.op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	op, err := f.core.DeployReviewedEnvironmentScoped(ctx, f.env.ID, id, "fixture", "allowed-scope", f.env.Revision, scopes)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := f.core.store.GetApplicationRevision(ctx, op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if scope := rev.ConfigurationSource.ServiceScope; scope == nil || len(scope.Names) != 2 || scope.Mode != "update" {
		t.Fatalf("incorrect execution scope: %+v", scope)
	}
	if !rev.ConfigurationSource.RestrictedHostAccess {
		t.Fatal("scoped host-access restriction was not frozen for execution")
	}
	merged, err := f.core.ApplicationSource(ctx, f.op.ApplicationID, op.RevisionID)
	if err != nil || !strings.Contains(merged.ComposeYAML, "api:") || !strings.Contains(merged.ComposeYAML, "worker:") {
		t.Fatalf("retained service disappeared: %v", err)
	}
	historical, err := f.core.ApplicationSource(ctx, f.op.ApplicationID, f.op.RevisionID)
	if err != nil || historical.ComposeYAML != before.ComposeYAML || historical.RuntimeEnv != before.RuntimeEnv {
		t.Fatal("incremental review changed immutable history", err)
	}
}

func TestServiceReviewRejectsStaleManagerVariablesAndReplicaEvidence(t *testing.T) {
	for _, scenario := range []string{"manager service version", "manager replacement", "manager unavailable", "public variables", "environment variables", "replica override", "new desired revision"} {
		t.Run(scenario, func(t *testing.T) {
			f := serviceReviewBoundarySetup(t)
			ctx := t.Context()
			review, err := f.core.ReviewEnvironmentInput(ctx, f.env.ID, ComposeValidationInput{Compose: f.candidate(), ServiceMode: "update", RuntimeEnv: f.runtimeEnv})
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "manager service version":
				f.client.Services[0].Version.Index++
			case "manager replacement":
				f.client.Services[0].ID = "externally-replaced-web"
			case "manager unavailable":
				f.client.Failure = errors.New("manager observation unavailable")
			case "public variables":
				v, e := f.core.variables(ctx, f.project.ID)
				if e != nil {
					t.Fatal(e)
				}
				_, err = f.core.SaveVariables(ctx, f.project.ID, VariableInput{Values: []Variable{{Key: "TOKEN", Value: pointer("changed-token"), Secret: true}}}, v.Revision)
			case "environment variables":
				_, err = f.core.SaveVariables(ctx, f.env.ID, VariableInput{Values: []Variable{{Key: "NEW", Value: pointer("value")}}}, 0)
			case "replica override":
				err = f.sql.SetApplicationReplicaOverride(ctx, f.op.ApplicationID, "web", 3, f.core.now())
			case "new desired revision":
				_, err = f.core.DeployReviewedEnvironment(ctx, f.env.ID, review["preflight_id"].(string), "fixture", "first-claim", f.env.Revision)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, e := f.core.store.ListDeploymentOperations(ctx, f.op.ApplicationID, storage.Page{Limit: 20})
			if e != nil {
				t.Fatal(e)
			}
			if _, err = f.core.DeployReviewedEnvironment(ctx, f.env.ID, review["preflight_id"].(string), "fixture", "stale-input", f.env.Revision); err == nil {
				t.Fatal("stale reviewed evidence accepted")
			}
			after, e := f.core.store.ListDeploymentOperations(ctx, f.op.ApplicationID, storage.Page{Limit: 20})
			if e != nil || len(after) != len(before) {
				t.Fatalf("rejected review queued mutation: %d -> %d %v", len(before), len(after), e)
			}
			app, e := f.core.store.GetApplication(ctx, f.op.ApplicationID)
			if e != nil || app.ActiveRevisionID != f.op.RevisionID {
				t.Fatal("stale review changed accepted revision", e)
			}
		})
	}
}

func TestServiceReviewRejectsForeignDockerCollisionAndUnscopedOmission(t *testing.T) {
	f := serviceReviewBoundarySetup(t)
	ctx := t.Context()
	f.client.Services = append(f.client.Services, swarm.Service{ID: "external-worker", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: f.env.ProjectName + "_worker", Labels: map[string]string{"com.docker.stack.namespace": "foreign"}}}})
	if _, err := f.core.ReviewEnvironmentInput(ctx, f.env.ID, ComposeValidationInput{Compose: "services: {worker: {image: 'example.test/worker@sha256:" + strings.Repeat("d", 64) + "'}}", ServiceMode: "create"}); err == nil {
		t.Fatal("unobserved foreign service was claimed")
	}
	app, err := f.core.store.GetApplication(ctx, f.op.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.core.validateUnscopedSwarmRevision(ctx, app, f.runtime, model.DeploymentSource{}, f.candidate()); err == nil {
		t.Fatal("whole-stack update bypassed service review")
	}
	if _, err = f.core.ReviewEnvironmentInput(ctx, f.env.ID, ComposeValidationInput{Compose: f.initial, ServiceMode: "update", RuntimeEnv: f.runtimeEnv}); err == nil {
		t.Fatal("unchanged configuration generated a deploy scope")
	}
	operations, err := f.core.store.ListDeploymentOperations(ctx, app.ID, storage.Page{Limit: 10})
	if err != nil || len(operations) != 1 {
		t.Fatalf("rejected collisions wrote deployment history: %d %v", len(operations), err)
	}
}
