package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestSandboxChecksRejectUnsafeRequestsAtomically(t *testing.T) {
	for _, kind := range []string{"identity", "too many", "missing route", "wildcard", "method", "scheme", "relative", "authority", "newline", "backslash", "low status", "high status", "foreign pool", "foreign role", "production role", "disabled"} {
		t.Run(kind, func(t *testing.T) {
			c, service, input := externalBindingFixture(t)
			ctx := context.Background()
			binding, _, err := c.CreateDomainBinding(ctx, service.ID, input)
			if err != nil {
				t.Fatal(err)
			}
			pool := binding.Domain.PoolID
			gateway := id.New(id.Gateway)
			settings := SandboxSettings{GatewayID: gateway, Checks: []SandboxCheck{{RouteID: binding.Route.ID, Path: "/health", ExpectedStatus: 200}}}
			switch kind {
			case "identity":
				settings.GatewayID = "invalid"
			case "too many":
				settings.Checks = make([]SandboxCheck, 51)
			case "missing route":
				settings.Checks[0].RouteID = id.New(id.Route)
			case "wildcard":
				if _, err = c.store.(*storage.SQLStore).DB().Exec("UPDATE domains SET host=? WHERE id=?", "*.example.test", binding.Domain.ID); err != nil {
					t.Fatal(err)
				}
			case "method":
				settings.Checks[0].Method = "POST"
			case "scheme":
				settings.Checks[0].Scheme = "ftp"
			case "relative":
				settings.Checks[0].Path = "health"
			case "authority":
				settings.Checks[0].Path = "//outside.test/"
			case "newline":
				settings.Checks[0].Path = "/health\r\nHost:outside.test"
			case "backslash":
				settings.Checks[0].Path = "/\\outside.test/"
			case "low status":
				settings.Checks[0].ExpectedStatus = 99
			case "high status":
				settings.Checks[0].ExpectedStatus = 600
			case "foreign pool":
				p, e := c.CreatePool(ctx, CreatePoolInput{Name: "foreign", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080"})
				if e != nil {
					t.Fatal(e)
				}
				pool = p.ID
			case "foreign role", "production role":
				parent, mode := pool, "production"
				if kind == "foreign role" {
					parent = "foreign"
					mode = "sandbox"
				}
				if err = c.saveControl(ctx, gateway+":role", "gateway-role", parent, map[string]string{"mode": mode}, 0); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				c.config.Management.Enabled = false
			}
			if _, err = c.SaveSandbox(ctx, pool, settings, 0); err == nil {
				t.Fatal("unsafe sandbox saved")
			}
			if _, err = c.store.GetControlDocument(ctx, pool+":sandbox"); !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("rejected settings persisted: %v", err)
			}
			if kind != "foreign role" && kind != "production role" {
				if _, err = c.store.GetControlDocument(ctx, gateway+":role"); !errors.Is(err, storage.ErrNotFound) {
					t.Fatalf("failed setup claimed gateway identity: %v", err)
				}
			}
		})
	}
}

func TestSandboxCanonicalChecksAndIdentityReuse(t *testing.T) {
	c, service, input := externalBindingFixture(t)
	ctx := context.Background()
	binding, _, err := c.CreateDomainBinding(ctx, service.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	pool := binding.Domain.PoolID
	gateway := id.New(id.Gateway)
	settings := SandboxSettings{GatewayID: gateway, Checks: []SandboxCheck{
		{RouteID: binding.Route.ID, Host: "untrusted.test", Path: "/health", ExpectedStatus: 100},
		{RouteID: binding.Route.ID, Method: "HEAD", Scheme: "https", Path: "/health?ready=1", ExpectedStatus: 599},
		{RouteID: binding.Route.ID, Method: "GET", Scheme: "http3", Path: "/", ExpectedStatus: 200},
	}}
	got, err := c.SaveSandbox(ctx, pool, settings, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Checks[0].Method != "GET" || got.Checks[0].Scheme != "http" || got.Checks[0].Host != binding.Domain.Host || got.Checks[2].Scheme != "http3" {
		t.Fatalf("check identity/defaults: %+v", got)
	}
	updated, err := c.SaveSandbox(ctx, pool, got, got.Revision)
	if err != nil || updated.Revision <= got.Revision {
		t.Fatalf("same dedicated identity cannot be reused: %+v %v", updated, err)
	}
	if _, err = c.SaveSandbox(ctx, pool, got, got.Revision); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale settings overwritten: %v", err)
	}
	mode, err := c.GatewayMode(ctx, gateway, pool)
	if err != nil || mode != "sandbox" {
		t.Fatalf("sandbox identity lost: %s %v", mode, err)
	}
	if _, err = c.GatewayMode(ctx, gateway, "foreign"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("sandbox identity escaped pool: %v", err)
	}
	mode, err = c.GatewayMode(ctx, id.New(id.Gateway), pool)
	if err != nil || mode != "production" {
		t.Fatalf("unclaimed identity default: %s %v", mode, err)
	}
	for range 2 {
		if err = c.SandboxHeartbeat(ctx, gateway, pool); err != nil {
			t.Fatal(err)
		}
	}
	status, err := c.store.GetControlDocument(ctx, gateway+":sandbox-status")
	if err != nil || status.Revision != 2 {
		t.Fatalf("heartbeat lost: %+v %v", status, err)
	}
}

func TestNginxDraftScopeOrderingRestoreAndIndependentProduction(t *testing.T) {
	c, service, input := externalBindingFixture(t)
	ctx := context.Background()
	binding, _, err := c.CreateDomainBinding(ctx, service.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	pool := binding.Domain.PoolID
	before, err := c.store.GetPool(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	in := NginxConfiguration{Snippets: []model.ConfigSnippet{
		{Scope: "server", ResourceID: binding.Domain.ID, Content: "gzip on;"},
		{Scope: "location", ResourceID: binding.Route.ID, Content: "proxy_read_timeout 30s;"},
		{Scope: "http", Content: "gzip on;"},
	}, Description: "bounded scopes"}
	draft, err := c.SaveNginxDraft(ctx, pool, in, "reviewer", 0)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Snippets[0].Scope != "http" || draft.Snippets[1].Scope != "location" || draft.Snippets[2].Scope != "server" || draft.Actor != "reviewer" {
		t.Fatalf("draft ordering/actor: %+v", draft)
	}
	after, err := c.store.GetPool(ctx, pool)
	if err != nil || after.DesiredRevision != before.DesiredRevision {
		t.Fatalf("save published draft: %+v %v", after, err)
	}
	version := id.New("ngver")
	if err = c.saveControl(ctx, version, "nginx-version", pool, in, 0); err != nil {
		t.Fatal(err)
	}
	versions, err := c.ListNginxVersions(ctx, pool, storage.Page{})
	if err != nil || len(versions) != 1 || versions[0].Description != in.Description {
		t.Fatalf("version list: %+v %v", versions, err)
	}
	if _, err = c.RestoreNginxVersion(ctx, "foreign", version, "reviewer", draft.Revision); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("foreign restore accepted: %v", err)
	}
	if _, err = c.RestoreNginxVersion(ctx, pool, "missing", "reviewer", draft.Revision); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing restore accepted: %v", err)
	}
	restored, err := c.RestoreNginxVersion(ctx, pool, version, "restorer", draft.Revision)
	if err != nil || restored.ParentVersion != version || restored.Actor != "restorer" {
		t.Fatalf("restore provenance lost: %+v %v", restored, err)
	}
	after, err = c.store.GetPool(ctx, pool)
	if err != nil || after.DesiredRevision != before.DesiredRevision {
		t.Fatalf("restore bypassed publication: %+v %v", after, err)
	}
	if err = c.InvalidateNginxValidation(ctx, pool); err != nil {
		t.Fatalf("empty validation invalidation: %v", err)
	}
}

func TestNginxValidationRequiresLiveCompatibleTargets(t *testing.T) {
	for _, kind := range []string{"offline", "disabled", "unseen", "foreign", "old renderer", "sandbox stale"} {
		t.Run(kind, func(t *testing.T) {
			c, pool, g, v := nginxValidationFixture(t, kind == "sandbox stale")
			ctx := context.Background()
			switch kind {
			case "offline":
				past := c.now().Add(-time.Hour)
				g.LastSeenAt = &past
			case "disabled":
				g.Enabled = false
			case "unseen":
				g.LastSeenAt = nil
			case "foreign":
				p, err := c.CreatePool(ctx, CreatePoolInput{Name: "foreign", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080"})
				if err != nil {
					t.Fatal(err)
				}
				g.PoolID = p.ID
			case "old renderer":
				g.RendererVersion = "old"
			case "sandbox stale":
				later := c.now().Add(16 * time.Second)
				c.now = func() time.Time { return later }
				g.LastSeenAt = &later
			}
			var err error
			switch kind {
			case "disabled":
				err = c.store.UpdateGateway(ctx, &g, g.Revision)
			case "foreign":
				// Seed a gateway enrolled in a different pool; heartbeat upserts
				// intentionally cannot change enrollment ownership.
				_, err = c.store.(*storage.SQLStore).DB().Exec("UPDATE gateways SET pool_id=? WHERE id=?", g.PoolID, g.ID)
			default:
				err = c.store.UpsertGateway(ctx, &g)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.CreateNginxValidation(ctx, pool.ID, v.DraftRevision); err == nil {
				t.Fatal("unavailable target validated")
			}
			head, _, err := readControl[map[string]string](ctx, c.store, pool.ID+":nginx-validation-head", "nginx-validation-head")
			if err != nil || head["task_id"] != v.ID {
				t.Fatalf("rejected validation changed active task: %+v %v", head, err)
			}
		})
	}
}
