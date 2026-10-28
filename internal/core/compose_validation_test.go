package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/composecheck"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestComposePreviewDiagnosticsRedactSecretsFromEveryNodeField(t *testing.T) {
	secret := "MarkerSecret12345"
	node := composecheck.Node{
		ID: secret, Name: secret, Role: secret, State: secret,
		Availability: secret, OS: secret, Arch: secret, IP: secret,
		Labels: map[string]string{secret: secret}, EngineLabels: map[string]string{secret: secret},
	}
	result := composecheck.Result{
		Diagnostics: []composecheck.Diagnostic{{ID: secret, Code: "POLICY", Pointer: secret, Service: secret, Resource: secret, Reason: secret, Path: []string{secret}}},
		Services:    []composecheck.Service{{Name: secret, Mounts: []composecheck.Mount{{Key: secret, Source: secret, Target: secret}}, Constraints: []string{secret}, MatchedNodes: []composecheck.Node{node}}},
		Nodes:       []composecheck.Node{node},
	}
	redactDeploymentValidation(&result, map[string]string{"TOKEN": secret}, []string{"TOKEN"})
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || !strings.Contains(string(encoded), "[redacted]") || result.Diagnostics[0].Code != "POLICY" {
		t.Fatalf("preview validation exposed a secret or damaged its machine code: %s", encoded)
	}
}

func TestPublishedPortsResolveBeforeAdmissionAndCannotBypassReview(t *testing.T) {
	c, rt, _ := controlFixture(t)
	ctx := context.Background()
	for _, backend := range []model.RuntimeType{model.RuntimeDocker, model.RuntimeSwarm} {
		rt.Type = backend
		c.store = networkRuntimeStore{Store: c.store, runtime: rt}
		for _, port := range []string{"13306", "65536"} {
			variables := []applicationx.EnvironmentVariable{{Key: "MYSQL_PORT", Value: &port}}
			in := ApplicationPreflightInput{RuntimeID: rt.ID, ProjectName: "ports-test", ComposeYAML: "services:\n  db:\n    image: mysql:8.4\n    ports: [{target: 3306, published: '${MYSQL_PORT}'}]\n", Environment: applicationx.EnvironmentInput{Values: &variables}}
			preview, err := c.PreflightApplication(ctx, in)
			if port == "13306" {
				if err != nil || !preview.Validation.Deployable || !strings.Contains(preview.ResolvedYAML, port) {
					t.Fatalf("resolved mapping rejected: %+v %v", preview.Validation, err)
				}
				continue
			}
			if err == nil || !strings.Contains(err.Error(), "COMPOSE_PORT_INVALID") {
				t.Fatalf("invalid resolved port admitted: %v", err)
			}
			if _, _, _, err := c.InstallApplication(ctx, InstallApplicationInput{ApplicationPreflightInput: in, ProjectName: in.ProjectName}); err == nil || !strings.Contains(err.Error(), "COMPOSE_PORT_INVALID") {
				t.Fatalf("direct install bypassed port validation: %v", err)
			}
		}
	}
}

func TestComposePlanCannotBeBypassedAndPersistsInRevision(t *testing.T) {
	c, rt, _ := controlFixture(t)
	rt.Type = model.RuntimeSwarm
	c.store = networkRuntimeStore{Store: c.store, runtime: rt}
	ctx := context.Background()
	raw := "services:\n  db:\n    image: mysql:8.4\n    volumes: [data:/var/lib/mysql]\nvolumes:\n  data: {}\n"
	in := ApplicationPreflightInput{RuntimeID: rt.ID, ProjectName: "storage-test", ComposeYAML: raw}
	if _, err := c.PreflightApplication(ctx, in); err == nil || !strings.Contains(err.Error(), "SWARM_LOCAL_VOLUME_WITHOUT_PLACEMENT") {
		t.Fatalf("preview admitted missing placement: %v", err)
	}
	if _, _, _, err := c.InstallApplication(ctx, InstallApplicationInput{ApplicationPreflightInput: in, ProjectName: in.ProjectName}); err == nil {
		t.Fatal("direct install bypassed placement")
	}
	m := composecheck.Analyze(raw, composecheck.Options{}).Services[0].Mounts[0]
	in.DeploymentPlan = &composecheck.Plan{Storage: []composecheck.Declaration{{Resource: m.Key, Fingerprint: m.Fingerprint, Capability: "shared", Confirmed: true}}}
	app, op, _, err := c.InstallApplication(ctx, InstallApplicationInput{ApplicationPreflightInput: in, ProjectName: in.ProjectName})
	if err != nil {
		t.Fatal(err)
	}
	rev, err := c.store.GetApplicationRevision(ctx, op.RevisionID)
	if err != nil || len(rev.ConfigurationSource.DeploymentPlan.Storage) != 1 {
		t.Fatalf("declaration not persisted: %+v %v", rev, err)
	}

	_ = app
}

func TestOldApplicationMayStopButNotRedeployUnrestrictedLocalStorage(t *testing.T) {
	c, rt, _ := controlFixture(t)
	ctx := context.Background()
	raw := "services:\n  db:\n    image: mysql:8.4\n    volumes: [data:/var/lib/mysql]\nvolumes:\n  data: {}\n"
	app, op, _, err := c.InstallApplication(ctx, InstallApplicationInput{ApplicationPreflightInput: ApplicationPreflightInput{RuntimeID: rt.ID, ComposeYAML: raw}, ProjectName: "old-storage"})
	if err != nil {
		t.Fatal(err)
	}
	sql := c.store.(*storage.SQLStore)
	if _, err = sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?", op.ID); err != nil {
		t.Fatal(err)
	}
	rt.Type = model.RuntimeSwarm
	c.store = networkRuntimeStore{Store: c.store, runtime: rt}
	if _, err = c.QueueApplicationOperation(ctx, app.ID, "REDEPLOY", "reject-local"); err == nil {
		t.Fatal("old revision silently exempted")
	}
	if _, err = c.QueueApplicationOperation(ctx, app.ID, "STOP", "allow-stop"); err != nil {
		t.Fatalf("stop blocked by storage admission: %v", err)
	}
}
