package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestRegistryPollingCreatesOneCandidateAndHonorsAutomaticPolicy(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "automatic"}[automatic], func(t *testing.T) {
			var content atomic.Value
			content.Store(`{"schemaVersion":2,"config":{"digest":"first"}}`)
			var responseStatus atomic.Int32
			responseStatus.Store(200)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v2/web/manifests/stable" {
					t.Errorf("unexpected registry request %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				w.Header().Set("Retry-After", "900")
				w.WriteHeader(int(responseStatus.Load()))
				_, _ = w.Write([]byte(content.Load().(string)))
			}))
			defer server.Close()
			credentialFile := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(credentialFile, []byte(`{"auths":{}}`), 0600); err != nil {
				t.Fatal(err)
			}
			compose := "services:\n  web:\n    image: " + strings.TrimPrefix(server.URL, "http://") + "/web:stable\n"
			c, env := activeImageEnvironment(t, compose, func(c *Core, in *EnvironmentInput) {
				c.config.Registries["fixture"] = config.RegistryCredential{ConfigFile: credentialFile, Insecure: true}
				in.RegistryCredentialRef = "fixture"
			})
			ctx := context.Background()
			now := c.now()
			c.now = func() time.Time { return now }
			policy, err := c.SaveImagePolicy(ctx, env.ID, ImagePolicy{Enabled: true, Poll: true, Automatic: automatic, IntervalSeconds: 60, Services: []string{"web"}}, 0)
			if err != nil {
				t.Fatal(err)
			}
			c.checkEnvironmentImages(ctx, env)
			initial, err := c.GetImageCheck(ctx, env.ID)
			if err != nil || initial.LastSuccess == nil || initial.CandidateID != "" {
				t.Fatalf("unchanged manifest produced candidate/failure: %#v %v", initial, err)
			}
			responseStatus.Store(429)
			if _, err := c.RequestImageCheck(ctx, env.ID); err != nil {
				t.Fatal(err)
			}
			c.checkEnvironmentImages(ctx, env)
			limited, err := c.GetImageCheck(ctx, env.ID)
			if err != nil || limited.Failures != 1 || limited.Error == "" || limited.NextCheck.Before(now.Add(15*time.Minute)) || limited.LastSuccess == nil {
				t.Fatalf("registry retry-after or prior success lost: %#v %v", limited, err)
			}
			responseStatus.Store(200)
			content.Store(`{"schemaVersion":2,"config":{"digest":"second"}}`)
			if _, err := c.RequestImageCheck(ctx, env.ID); err != nil {
				t.Fatal(err)
			}
			c.checkEnvironmentImages(ctx, env)
			state, err := c.GetImageCheck(ctx, env.ID)
			if err != nil || state.Error != "" || state.Failures != 0 || state.CandidateID == "" {
				t.Fatalf("changed manifest not recorded: %#v %v", state, err)
			}
			updates, err := c.ListImageUpdates(ctx, env.ID, storage.Page{Limit: 20})
			if err != nil || len(updates) != 1 || strings.Join(updates[0].Changed, ",") != "web" {
				t.Fatalf("candidate list=%#v %v", updates, err)
			}
			if automatic {
				if updates[0].Status != string(model.OperationQueued) || updates[0].OperationID == "" {
					t.Fatalf("automatic update not queued: %#v", updates[0])
				}
				op, err := c.store.GetDeploymentOperation(ctx, updates[0].OperationID)
				if err != nil {
					t.Fatal(err)
				}
				rev, err := c.store.GetApplicationRevision(ctx, op.RevisionID)
				if err != nil || rev.ConfigurationSource.UpdatePolicyRevision != policy.Revision {
					t.Fatalf("automatic operation lost authorized policy: %#v %v", rev, err)
				}
			} else if updates[0].Status != "AVAILABLE" || updates[0].OperationID != "" {
				t.Fatal("manual policy deployed without approval")
			}
			if _, err := c.RequestImageCheck(ctx, env.ID); err != nil {
				t.Fatal(err)
			}
			c.checkEnvironmentImages(ctx, env)
			again, err := c.ListImageUpdates(ctx, env.ID, storage.Page{Limit: 20})
			if err != nil || len(again) != 1 || again[0].ID != updates[0].ID || again[0].OperationID != updates[0].OperationID {
				t.Fatalf("repeat polling duplicated candidate/work: %#v %v", again, err)
			}
		})
	}
}
