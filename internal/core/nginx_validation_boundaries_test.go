package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func nginxValidationFixture(t *testing.T, sandbox bool) (*Core, model.GatewayPool, model.Gateway, NginxValidation) {
	t.Helper()
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	now := c.now()
	g := model.Gateway{ID: id.New(id.Gateway), PoolID: pool.ID, Name: "production", Enabled: true, RendererVersion: nginxx.RendererVersion, LastSeenAt: &now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := c.store.UpsertGateway(ctx, &g); err != nil {
		t.Fatal(err)
	}
	if sandbox {
		settings, err := c.SaveSandbox(ctx, pool.ID, SandboxSettings{GatewayID: id.New(id.Gateway)}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.SandboxHeartbeat(ctx, settings.GatewayID, pool.ID); err != nil {
			t.Fatal(err)
		}
	}
	draft, err := c.SaveNginxDraft(ctx, pool.ID, NginxConfiguration{Snippets: []model.ConfigSnippet{{Scope: "http", Content: "gzip on;"}}}, "reviewer", 0)
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.CreateNginxValidation(ctx, pool.ID, draft.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return c, pool, g, v
}

func TestNginxSandboxMustValidateBeforeProduction(t *testing.T) {
	c, pool, g, v := nginxValidationFixture(t, true)
	ctx := context.Background()
	if task, snapshot, err := c.NextNginxValidation(ctx, pool.ID, g.ID); err != nil || task != nil || snapshot != nil {
		t.Fatalf("production bypassed sandbox: %+v %+v %v", task, snapshot, err)
	}
	task, snapshot, err := c.NextNginxValidation(ctx, pool.ID, v.SandboxID)
	if err != nil || task == nil || snapshot == nil || snapshot.Revision != v.SandboxRevision || len(snapshot.Snippets) != 1 {
		t.Fatalf("sandbox candidate: %+v %+v %v", task, snapshot, err)
	}
	if err = c.FinishNginxValidation(ctx, pool.ID, v.ID, v.SandboxID, NginxValidationResult{Status: "SUCCEEDED", RenderedDigest: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	task, snapshot, err = c.NextNginxValidation(ctx, pool.ID, g.ID)
	if err != nil || task == nil || snapshot == nil || snapshot.Revision != v.BaseRevision+1 {
		t.Fatalf("production candidate: %+v %+v %v", task, snapshot, err)
	}
	if again, _, err := c.NextNginxValidation(ctx, pool.ID, g.ID); err != nil || again != nil {
		t.Fatalf("running check delivered twice: %+v %v", again, err)
	}
	if err = c.FinishNginxValidation(ctx, pool.ID, v.ID, g.ID, NginxValidationResult{Status: "FAILED", Error: "candidate syntax failed"}); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetNginxValidation(ctx, pool.ID, v.ID)
	if err != nil || got.Status != "FAILED" || got.Results[v.SandboxID].Status != "SUCCEEDED" {
		t.Fatalf("partial validation was hidden: %+v %v", got, err)
	}
	if _, err = c.PublishNginx(ctx, pool.ID, v.ID, "operator", "failed", v.DraftRevision); err == nil {
		t.Fatal("failed validation published")
	}
	actual, err := c.store.GetPool(ctx, pool.ID)
	if err != nil || actual.DesiredRevision != v.BaseRevision {
		t.Fatalf("validation changed production target: %+v %v", actual, err)
	}
}

func TestNginxValidationStaleCancelledAndUnclaimedResults(t *testing.T) {
	for _, state := range []string{"pending", "unknown gateway", "invalid result", "cancelled", "expired", "superseded", "reconnected", "pool changed"} {
		t.Run(state, func(t *testing.T) {
			c, pool, g, v := nginxValidationFixture(t, false)
			ctx := context.Background()
			if state != "pending" {
				if _, _, err := c.NextNginxValidation(ctx, pool.ID, g.ID); err != nil {
					t.Fatal(err)
				}
			}
			want := storage.ErrPrecondition
			gateway := g.ID
			result := NginxValidationResult{Status: "SUCCEEDED"}
			switch state {
			case "unknown gateway":
				gateway = id.New(id.Gateway)
			case "invalid result":
				result.Status = "UNKNOWN"
				want = storage.ErrConflict
			case "cancelled":
				current, err := c.GetNginxValidation(ctx, pool.ID, v.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err = c.CancelNginxValidation(ctx, pool.ID, v.ID, current.Revision); err != nil {
					t.Fatal(err)
				}
			case "expired":
				future := v.ExpiresAt
				c.now = func() time.Time { return future }
			case "superseded":
				if _, err := c.CreateNginxValidation(ctx, pool.ID, v.DraftRevision); err != nil {
					t.Fatal(err)
				}
			case "reconnected":
				if err := c.InvalidateNginxValidation(ctx, pool.ID); err != nil {
					t.Fatal(err)
				}
			case "pool changed":
				p, err := c.store.GetPool(ctx, pool.ID)
				if err != nil {
					t.Fatal(err)
				}
				p.DesiredRevision++
				if _, err = c.store.(*storage.SQLStore).DB().Exec("UPDATE gateway_pools SET desired_revision=? WHERE id=?", p.DesiredRevision, p.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.FinishNginxValidation(ctx, pool.ID, v.ID, gateway, result); !errors.Is(err, want) {
				t.Fatalf("untrusted completion accepted: %v", err)
			}
			got, err := c.GetNginxValidation(ctx, pool.ID, v.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status == "SUCCEEDED" || got.Results[g.ID].Status == "SUCCEEDED" {
				t.Fatalf("untrusted evidence became success: %+v", got)
			}
			if state == "cancelled" && got.Status != "CANCELLED" {
				t.Fatalf("cancel lost: %+v", got)
			}
			if state == "expired" || state == "superseded" || state == "reconnected" || state == "pool changed" {
				if got.Status != "EXPIRED" {
					t.Fatalf("stale evidence remained current: %+v", got)
				}
			}
		})
	}
}

func TestNginxDraftRejectsUnsupportedOrForeignScopes(t *testing.T) {
	for _, kind := range []string{"disabled management", "disabled pool", "missing pool", "too many", "duplicate", "invalid directive", "HTTP resource", "missing server", "foreign server", "missing location", "foreign location"} {
		t.Run(kind, func(t *testing.T) {
			c, service, input := externalBindingFixture(t)
			ctx := context.Background()
			binding, _, err := c.CreateDomainBinding(ctx, service.ID, input)
			if err != nil {
				t.Fatal(err)
			}
			pool := binding.Domain.PoolID
			in := NginxConfiguration{Snippets: []model.ConfigSnippet{{Scope: "http", Content: "gzip on;"}}}
			switch kind {
			case "disabled management":
				c.config.Management.Enabled = false
			case "disabled pool":
				p, e := c.store.GetPool(ctx, pool)
				if e != nil {
					t.Fatal(e)
				}
				p.Enabled = false
				if e = c.store.UpdatePool(ctx, &p, p.Revision); e != nil {
					t.Fatal(e)
				}
			case "missing pool":
				pool = id.New(id.Pool)
			case "too many":
				in.Snippets = make([]model.ConfigSnippet, 1001)
			case "duplicate":
				in.Snippets = append(in.Snippets, in.Snippets[0])
			case "invalid directive":
				in.Snippets[0].Content = "listen 80;"
			case "HTTP resource":
				in.Snippets[0].ResourceID = binding.Domain.ID
			case "missing server":
				in.Snippets = []model.ConfigSnippet{{Scope: "server", ResourceID: id.New(id.Domain), Content: "gzip on;"}}
			case "missing location":
				in.Snippets = []model.ConfigSnippet{{Scope: "location", ResourceID: id.New(id.Route), Content: "proxy_read_timeout 30s;"}}
			case "foreign server", "foreign location":
				p, e := c.CreatePool(ctx, CreatePoolInput{Name: "foreign", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080"})
				if e != nil {
					t.Fatal(e)
				}
				pool = p.ID
				if kind == "foreign server" {
					in.Snippets = []model.ConfigSnippet{{Scope: "server", ResourceID: binding.Domain.ID, Content: "gzip on;"}}
				} else {
					in.Snippets = []model.ConfigSnippet{{Scope: "location", ResourceID: binding.Route.ID, Content: "proxy_read_timeout 30s;"}}
				}
			}
			if _, err = c.SaveNginxDraft(ctx, pool, in, "reviewer", 0); err == nil {
				t.Fatal("unsafe scope saved")
			}
			if _, err = c.store.GetControlDocument(ctx, pool+":nginx-draft"); !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("rejected draft persisted: %v", err)
			}
		})
	}
}
