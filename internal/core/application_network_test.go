package core

import (
	"context"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type networkRuntimeStore struct {
	storage.Store
	runtime model.Runtime
}

func (s networkRuntimeStore) GetRuntime(context.Context, string) (model.Runtime, error) {
	return s.runtime, nil
}
func (s networkRuntimeStore) Dialect() storage.Dialect { return storage.Postgres }

func TestApplicationNetworkRevisionInheritanceAndRestore(t *testing.T) {
	c, rt, _ := controlFixture(t)
	sql := c.store.(*storage.SQLStore)
	rt.Type = model.RuntimeSwarm
	c.store = networkRuntimeStore{Store: sql, runtime: rt}
	ctx := context.Background()
	in := ApplicationPreflightInput{RuntimeID: rt.ID, ProjectName: "demoapp", ComposeYAML: "services:\n  web:\n    image: web:fixed\n"}
	preview, err := c.PreflightApplication(ctx, in)
	if err != nil || preview.NetworkName != "demoapp" {
		t.Fatalf("default network: %+v %v", preview, err)
	}
	app, op, _, err := c.InstallApplication(ctx, InstallApplicationInput{ApplicationPreflightInput: in, ProjectName: "demoapp", Name: "demoapp"})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := c.store.GetApplicationRevision(ctx, op.RevisionID)
	if err != nil || initial.ConfigurationSource.NetworkName != "demoapp" {
		t.Fatalf("network not persisted: %+v %v", initial, err)
	}
	if _, err := sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?", op.ID); err != nil {
		t.Fatal(err)
	}
	app, _ = c.store.GetApplication(ctx, app.ID)
	input := CreateRevisionInput{ApplicationPreflightInput: ApplicationPreflightInput{NetworkName: "demoapp-dev"}}
	changed, err := c.PreflightApplicationRevision(ctx, app.ID, input.ApplicationPreflightInput)
	if err != nil || !changed.Diff.NetworksChanged || changed.NetworkName != input.NetworkName {
		t.Fatalf("network change missing from review: %+v %v", changed, err)
	}
	revision, _, _, err := c.CreateApplicationRevision(ctx, app.ID, input, app.Revision)
	if err != nil || revision.ConfigurationSource.NetworkName != "demoapp-dev" {
		t.Fatalf("network revision: %+v %v", revision, err)
	}
	in = ApplicationPreflightInput{ComposeYAML: "services:\n  web:\n    image: web:new\n"}
	if _, err := c.inheritRevisionContent(ctx, revision, &in); err != nil || in.NetworkName != "demoapp-dev" {
		t.Fatalf("Compose edits lost network: %+v %v", in, err)
	}
	in = ApplicationPreflightInput{ConfigurationSource: initial.ConfigurationSource}
	in.ConfigurationSource.RestoredFrom = initial.ID
	if _, err := c.inheritRevisionContent(ctx, revision, &in); err != nil {
		t.Fatal(err)
	}
	if err := prepareApplicationNetwork(&in, rt, app.ProjectName, "nsc-ingress"); err != nil || in.NetworkName != "demoapp" {
		t.Fatalf("restore inherited newer network: %+v %v", in, err)
	}
}

func TestApplicationNetworkValidation(t *testing.T) {
	for _, test := range []struct {
		runtime              model.RuntimeType
		name, restored, want string
		failure              bool
	}{
		{model.RuntimeSwarm, "", "", "demoapp", false},
		{model.RuntimeSwarm, " custom ", "", "custom", false},
		{model.RuntimeSwarm, "", "legacy", "", false},
		{model.RuntimeSwarm, "nsc-ingress", "", "", true},
		{model.RuntimeDocker, "custom", "", "", true},
		{model.RuntimeDocker, "", "", "", false},
	} {
		input := ApplicationPreflightInput{NetworkName: test.name, ComposeYAML: "services: {}", ConfigurationSource: model.DeploymentSource{RestoredFrom: test.restored}}
		err := prepareApplicationNetwork(&input, model.Runtime{Type: test.runtime}, "demoapp", "nsc-ingress")
		if (err != nil) != test.failure || err == nil && input.NetworkName != test.want {
			t.Fatalf("%+v: %+v %v", test, input, err)
		}
	}
}

func TestEnvironmentReviewFreezesApplicationNetwork(t *testing.T) {
	c, rt, _ := controlFixture(t)
	rt.Type = model.RuntimeSwarm
	c.store = networkRuntimeStore{Store: c.store, runtime: rt}
	ctx := context.Background()
	project, err := c.CreateProject(ctx, "Review", "review")
	if err != nil {
		t.Fatal(err)
	}
	env, err := c.CreateEnvironment(ctx, project.ID, EnvironmentInput{Name: "trial", RuntimeID: rt.ID})
	if err != nil {
		t.Fatal(err)
	}
	compose := "services:\n  web:\n    image: example.test/web@sha256:" + strings.Repeat("a", 64) + "\n"
	for _, name := range []string{"", "explicit-network"} {
		preview, err := c.ReviewEnvironment(ctx, env.ID, compose, name)
		if err != nil {
			t.Fatal(err)
		}
		want := name
		if want == "" {
			want = env.ProjectName
		}
		var review environmentReview
		if _, err := c.readEncrypted(ctx, preview["preflight_id"].(string), "environment-review", &review); err != nil {
			t.Fatal(err)
		}
		if preview["network_name"] != want || review.Input.NetworkName != want || review.Source.NetworkName != want {
			t.Fatalf("review did not freeze effective network %s: %+v %+v", want, preview, review)
		}
	}
}
