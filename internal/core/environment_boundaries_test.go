package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/storage"
)

func environmentBoundaryFixture(t *testing.T) (*Core, Project, Environment) {
	t.Helper()
	c, rt, _ := controlFixture(t)
	p, err := c.CreateProject(context.Background(), "Project", "project")
	if err != nil {
		t.Fatal(err)
	}
	e, err := c.CreateEnvironment(context.Background(), p.ID, EnvironmentInput{Name: "test", RuntimeID: rt.ID})
	if err != nil {
		t.Fatal(err)
	}
	return c, p, e
}

func TestVariableEditRejectsInvalidOrAmbiguousValues(t *testing.T) {
	for _, name := range []string{"raw and values", "malformed raw", "invalid key", "duplicate key", "missing new value", "masked previous value", "stale", "disabled", "unknown owner"} {
		t.Run(name, func(t *testing.T) {
			c, p, _ := environmentBoundaryFixture(t)
			ctx := context.Background()
			value := "private-secret"
			initial, err := c.SaveVariables(ctx, p.ID, VariableInput{Values: []Variable{{Key: "TOKEN", Value: &value, Secret: true}, {Key: "MASK", Mask: true}}}, 0)
			if err != nil {
				t.Fatal(err)
			}
			in := VariableInput{Values: []Variable{{Key: "TOKEN"}}}
			owner, expected := p.ID, initial.Revision
			switch name {
			case "raw and values":
				raw := "A=1"
				in.Raw = &raw
			case "malformed raw":
				raw := "INVALID"
				in = VariableInput{Raw: &raw}
			case "invalid key":
				in.Values[0].Key = "1INVALID"
			case "duplicate key":
				in.Values = append(in.Values, in.Values[0])
			case "missing new value":
				in.Values[0].Key = "NEW"
			case "masked previous value":
				in.Values[0].Key = "MASK"
			case "stale":
				expected++
			case "disabled":
				c.config.Management.Enabled = false
			case "unknown owner":
				owner = "prj_missing"
			}
			if _, err := c.SaveVariables(ctx, owner, in, expected); err == nil {
				t.Fatal("invalid edit accepted")
			}
			actual, err := c.variables(ctx, p.ID)
			if err != nil || actual.Revision != initial.Revision || len(actual.Values) != 2 || actual.Values[1].Value == nil || *actual.Values[1].Value != value {
				t.Fatalf("invalid edit lost preserved values: %+v %v", actual, err)
			}
		})
	}
}

func TestEnvironmentReadsAndWritesPropagateStorageFailure(t *testing.T) {
	failure := errors.New("environment storage unavailable")
	for _, test := range []struct {
		name, method, key string
		nth               int
		run               func(context.Context, *Core, Project, Environment) error
	}{
		{"project write", "write", "", 0, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, err := c.CreateProject(ctx, "Another", "another")
			return err
		}},
		{"project reread", "document", "", 0, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, err := c.CreateProject(ctx, "Another", "another")
			return err
		}},
		{"projects list", "documents", "project", 0, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, err := c.ListProjects(ctx, storage.Page{})
			return err
		}},
		{"environments list", "documents", "environment", 0, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, err := c.ListEnvironments(ctx, p.ID, storage.Page{})
			return err
		}},
		{"environment parent", "document", "prj_", 0, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, err := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "another", RuntimeID: e.RuntimeID})
			return err
		}},
		{"environment commit", "write", "", 0, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, err := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "another", RuntimeID: e.RuntimeID})
			return err
		}},
		{"variables old read", "document", ":variables", 0, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, err := c.SaveVariables(ctx, p.ID, VariableInput{}, 0)
			return err
		}},
		{"variables parent reread", "document", "env_", 2, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, err := c.SaveVariables(ctx, e.ID, VariableInput{}, 0)
			return err
		}},
		{"variables inherited", "document", "prj_", 0, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, err := c.SaveVariables(ctx, e.ID, VariableInput{}, 0)
			return err
		}},
		{"variables commit", "write", "", 0, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, err := c.SaveVariables(ctx, p.ID, VariableInput{}, 0)
			return err
		}},
		{"public variables inherited", "document", "prj_", 0, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, err := c.GetVariables(ctx, e.ID)
			return err
		}},
		{"public variables reread", "document", "env_", 3, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, err := c.GetVariables(ctx, e.ID)
			return err
		}},
		{"effective environment", "document", "env_", 1, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, err := c.EffectiveVariables(ctx, e.ID)
			return err
		}},
		{"effective public", "document", "prj_", 0, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, err := c.EffectiveVariables(ctx, e.ID)
			return err
		}},
		{"effective local", "document", ":variables", 2, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, err := c.EffectiveVariables(ctx, e.ID)
			return err
		}},
		{"preflight missing environment", "document", "env_", 1, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, _, err := c.EnvironmentPreflightInput(ctx, e.ID, "services: {}")
			return err
		}},
		{"preflight public", "document", "prj_", 0, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, _, err := c.EnvironmentPreflightInput(ctx, e.ID, "services: {}")
			return err
		}},
		{"preflight local", "document", ":variables", 2, func(ctx context.Context, c *Core, p Project, e Environment) error {
			_, _, err := c.EnvironmentPreflightInput(ctx, e.ID, "services: {}")
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, p, e := environmentBoundaryFixture(t)
			c.store = &nginxBoundaryStore{Store: c.store, method: test.method, key: test.key, nth: test.nth, failure: failure}
			if err := test.run(context.Background(), c, p, e); !errors.Is(err, failure) {
				t.Fatalf("storage failure became successful or unrelated result: %v", err)
			}
		})
	}
}

func TestEnvironmentIdentityClaimsAndCopyScope(t *testing.T) {
	c, p, e := environmentBoundaryFixture(t)
	ctx := context.Background()
	for _, input := range []struct{ name, slug string }{{"", "empty"}, {" ", "blank"}, {"Name", "INVALID"}, {"Name", strings.Repeat("a", 49)}} {
		if _, err := c.CreateProject(ctx, input.name, input.slug); err == nil {
			t.Fatal("invalid project identity accepted")
		}
	}
	if _, err := c.CreateProject(ctx, "Duplicate", p.Slug); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("slug claimed twice", err)
	}
	for _, in := range []EnvironmentInput{{Name: "INVALID", RuntimeID: e.RuntimeID}, {Name: "another", RuntimeID: "missing"}, {Name: e.Name, RuntimeID: e.RuntimeID}, {Name: "copy", RuntimeID: e.RuntimeID, CopyEnvironmentID: "missing"}} {
		if _, err := c.CreateEnvironment(ctx, p.ID, in); err == nil {
			t.Fatal("invalid environment accepted")
		}
	}
	p2, err := c.CreateProject(ctx, "Other", "other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateEnvironment(ctx, p2.ID, EnvironmentInput{Name: "copy", RuntimeID: e.RuntimeID, CopyEnvironmentID: e.ID}); err == nil {
		t.Fatal("cross-project copy accepted")
	}
	compose := "services:\n  web:\n    image: nginx:alpine\n"
	w, err := c.encryptedWrite(e.ID+":compose", "environment-compose", e.ID, compose, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{w}); err != nil {
		t.Fatal(err)
	}
	copy, err := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "copy", RuntimeID: e.RuntimeID, CopyEnvironmentID: e.ID})
	if err != nil {
		t.Fatal(err)
	}
	actual, err := c.environmentCompose(ctx, copy)
	if err != nil || actual != compose || copy.ApplicationID != "" || copy.ProjectName == e.ProjectName {
		t.Fatal("copy changed application ownership or lost compose", err)
	}
	longProject, err := c.CreateProject(ctx, "Long", strings.Repeat("a", 48))
	if err != nil {
		t.Fatal(err)
	}
	long, err := c.CreateEnvironment(ctx, longProject.ID, EnvironmentInput{Name: strings.Repeat("b", 40), RuntimeID: e.RuntimeID})
	if err != nil {
		t.Fatal(err)
	}
	if len(long.ProjectName) > 63 || !strings.HasSuffix(long.ProjectName, long.ID[len(long.ID)-8:]) {
		t.Fatalf("unsafe generated project name %q", long.ProjectName)
	}
}
