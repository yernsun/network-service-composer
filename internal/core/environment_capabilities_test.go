package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestEnvironmentCapabilitiesDoNotInventLiveReadiness(t *testing.T) {
	ctx := context.Background()
	for _, scenario := range []string{"ready", "fixed digest", "unmanaged", "no active revision", "no application", "management disabled", "runtime disabled", "system", "unpinned", "wrong key", "missing application", "missing runtime"} {
		t.Run(scenario, func(t *testing.T) {
			c, env := imageEnvironmentFixture(t)
			app, err := c.store.GetApplication(ctx, env.ApplicationID)
			if err != nil {
				t.Fatal(err)
			}
			revision, err := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
			if err != nil {
				t.Fatal(err)
			}
			revision.ConfigurationSource.TaggedImages = map[string]string{"web": "registry.example.test/web:stable", "api": "registry.example.test/api:stable"}
			want := "BASELINE_READY"
			switch scenario {
			case "fixed digest":
				revision.ConfigurationSource.TaggedImages = map[string]string{"web": revision.ConfigurationSource.Images["web"]}
				want = "tracked_tag_required"
			case "unmanaged":
				app.Managed = false
				want = "adoption_required"
			case "no active revision":
				app.ActiveRevisionID = ""
				want = "baseline_required"
			case "no application":
				env.ApplicationID = ""
				want = "application_required"
			case "management disabled":
				c.config.Management.Enabled = false
				want = "management.disabled"
			case "runtime disabled":
				runtime, _ := c.store.GetRuntime(ctx, env.RuntimeID)
				runtime.Enabled = false
				if err := c.store.UpdateRuntime(ctx, &runtime, runtime.Revision); err != nil {
					t.Fatal(err)
				}
				want = "management.runtime_disabled"
			case "system":
				env.System = true
				want = "system_resource"
			case "unpinned":
				revision.ConfigurationSource.Images = nil
				want = "baseline_required"
			case "wrong key":
				if _, err := c.store.(*storage.SQLStore).DB().ExecContext(ctx, `UPDATE application_revisions SET encrypted_resolved=? WHERE id=?`, []byte("broken-envelope"), revision.ID); err != nil {
					t.Fatal(err)
				}
			case "missing application":
				env.ApplicationID = "app_missing"
			case "missing runtime":
				env.RuntimeID = "rt_missing"
			}
			if err := c.store.UpdateApplication(ctx, &app, app.Revision); err != nil {
				t.Fatal(err)
			}
			source, _ := json.Marshal(revision.ConfigurationSource)
			if _, err := c.store.(*storage.SQLStore).DB().ExecContext(ctx, `UPDATE application_revisions SET configuration_source=? WHERE id=?`, string(source), revision.ID); err != nil {
				t.Fatal(err)
			}
			if err := c.saveControl(ctx, env.ID, "environment", env.ProjectID, env, env.Revision); err != nil {
				t.Fatal(err)
			}
			before, _ := c.store.ListDeploymentOperations(ctx, app.ID, storage.Page{})
			result, err := c.GetEnvironmentCapabilities(ctx, env.ID)
			if scenario == "wrong key" || strings.HasPrefix(scenario, "missing ") {
				if err == nil {
					t.Fatal("broken dependency reported as configured", result)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if want == "BASELINE_READY" {
				if result.ImageUpdates.State != want || result.Management.State != "CONFIGURED" || len(result.Services) != 2 || result.Services[0].Name != "api" || result.BaseRevision != revision.ID {
					t.Fatalf("baseline: %+v", result)
				}
			} else if result.ImageUpdates.State != "BLOCKED" || result.ImageUpdates.Reason != want {
				t.Fatalf("reason=%s: %+v", want, result)
			}
			after, _ := c.store.ListDeploymentOperations(ctx, app.ID, storage.Page{})
			if len(after) != len(before) {
				t.Fatal("read created a deployment")
			}
		})
	}
	c, _, _ := environmentBoundaryFixture(t)
	if _, err := c.GetEnvironmentCapabilities(ctx, "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
}
