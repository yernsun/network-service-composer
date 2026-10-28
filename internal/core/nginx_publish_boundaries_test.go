package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func finishValidationFixture(t *testing.T, c *Core, pool string, g model.Gateway, v NginxValidation) {
	t.Helper()
	ctx := context.Background()
	for _, gateway := range []string{v.SandboxID, g.ID} {
		if gateway == "" {
			continue
		}
		if task, _, err := c.NextNginxValidation(ctx, pool, gateway); err != nil || task == nil {
			t.Fatalf("claim validation: %+v %v", task, err)
		}
		if err := c.FinishNginxValidation(ctx, pool, v.ID, gateway, NginxValidationResult{Status: "SUCCEEDED", RenderedDigest: strings.Repeat("b", 64)}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNginxPublishRechecksReviewedDependencies(t *testing.T) {
	for _, kind := range []string{"idempotency missing", "draft stale", "draft changed", "management disabled", "settings changed", "gateway offline", "new gateway", "replacement gateway", "startup risk", "external changes"} {
		t.Run(kind, func(t *testing.T) {
			c, pool, g, v := nginxValidationFixture(t, false)
			ctx := context.Background()
			finishValidationFixture(t, c, pool.ID, g, v)
			expected := v.DraftRevision
			idem := "publish"
			switch kind {
			case "idempotency missing":
				idem = ""
			case "draft stale":
				expected++
			case "draft changed":
				d, err := c.SaveNginxDraft(ctx, pool.ID, NginxConfiguration{Snippets: []model.ConfigSnippet{{Scope: "http", Content: "gzip off;"}}}, "another editor", expected)
				if err != nil {
					t.Fatal(err)
				}
				expected = d.Revision
			case "management disabled":
				c.config.Management.Enabled = false
			case "settings changed":
				if _, err := c.SaveSandbox(ctx, pool.ID, SandboxSettings{}, 0); err != nil {
					t.Fatal(err)
				}
			case "gateway offline":
				old := c.now().Add(-time.Hour)
				g.LastSeenAt = &old
				if err := c.store.UpsertGateway(ctx, &g); err != nil {
					t.Fatal(err)
				}
			case "new gateway", "replacement gateway":
				if kind == "replacement gateway" {
					g.Enabled = false
					if err := c.store.UpdateGateway(ctx, &g, g.Revision); err != nil {
						t.Fatal(err)
					}
				}
				g.ID = id.New(id.Gateway)
				g.Name = "unvalidated"
				g.Enabled = true
				if err := c.store.UpsertGateway(ctx, &g); err != nil {
					t.Fatal(err)
				}
			case "startup risk", "external changes":
				evidence := GatewayEvidence{ObservedAt: c.now(), RefreshIntervalMillis: 5000, StartupState: "RISK"}
				if kind == "external changes" {
					evidence.StartupState = "CONFIRMED"
					evidence.DiskDigest = "external"
					evidence.RenderedDigest = "confirmed"
				}
				if err := c.RecordGatewayEvidence(ctx, g.ID, evidence); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.PublishNginx(ctx, pool.ID, v.ID, "reviewer", idem, expected); err == nil {
				t.Fatal("unsafe publication accepted")
			}
			p, err := c.store.GetPool(ctx, pool.ID)
			if err != nil || p.DesiredRevision != v.BaseRevision {
				t.Fatalf("rejected publish changed production: %+v %v", p, err)
			}
			versions, err := c.ListNginxVersions(ctx, pool.ID, storage.Page{})
			if err != nil || len(versions) != 0 {
				t.Fatalf("rejected publication created version: %+v %v", versions, err)
			}
		})
	}
}

func TestNginxPublishStorageFailureDoesNotChangeProduction(t *testing.T) {
	failure := errors.New("publication store interrupted")
	ctx := context.Background()
	for _, test := range []struct {
		method, key string
		nth         int
	}{
		{"document", "ngrel_", 0}, {"document", "ngval", 0}, {"document", "nginx-draft", 0}, {"document", ":sandbox", 0}, {"document", "nginx-active", 0}, {"document", ":evidence", 0}, {"document", "nginx-release-head", 0},
		{"pool", "", 2}, {"pool", "", 3}, {"pool", "", 4},
		{"gateways", "", 1}, {"gateways", "", 2}, {"gateway", "", 0}, {"snapshot", "", 0}, {"publish", "", 0},
	} {
		t.Run(test.method+test.key+string(rune('0'+test.nth)), func(t *testing.T) {
			c, pool, g, v := nginxValidationFixture(t, false)
			finishValidationFixture(t, c, pool.ID, g, v)
			original := c.store
			c.store = &nginxBoundaryStore{Store: original, method: test.method, key: test.key, nth: test.nth, failure: failure}
			if _, err := c.PublishNginx(ctx, pool.ID, v.ID, "reviewer", "publish", v.DraftRevision); !errors.Is(err, failure) {
				t.Fatalf("publication failure hidden: %v", err)
			}
			p, err := original.GetPool(ctx, pool.ID)
			if err != nil || p.DesiredRevision != v.BaseRevision {
				t.Fatalf("failed publication changed target: %+v %v", p, err)
			}
			if _, err = original.GetControlDocument(ctx, pool.ID+":nginx-release-head"); !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("failed publication replaced head: %v", err)
			}
		})
	}
}

func TestNginxSandboxPublicationAndIdempotencyScope(t *testing.T) {
	c, pool, g, v := nginxValidationFixture(t, true)
	ctx := context.Background()
	finishValidationFixture(t, c, pool.ID, g, v)
	release, err := c.PublishNginx(ctx, pool.ID, v.ID, "reviewer", "publish", v.DraftRevision)
	if err != nil {
		t.Fatal(err)
	}
	if len(release.Targets) != 1 || release.Targets[0].GatewayID != g.ID {
		t.Fatalf("sandbox included in production deployment: %+v", release)
	}
	if _, err = c.PublishNginx(ctx, pool.ID, "another-validation", "reviewer", "publish", v.DraftRevision); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("idempotency reused for different validation: %v", err)
	}
	if _, err = c.GetNginxRelease(ctx, "foreign", release.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("release escaped pool: %v", err)
	}
	if _, err = c.GetNginxValidation(ctx, "foreign", v.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("validation escaped pool: %v", err)
	}
	versions, err := c.ListNginxVersions(ctx, pool.ID, storage.Page{})
	if err != nil || len(versions) != 1 || versions[0].VersionID != release.VersionID {
		t.Fatalf("published version unavailable: %+v %v", versions, err)
	}
	// The historical deployment remains readable when its gateway record goes
	// away; it must not silently invent a replacement from current membership.
	if _, err = c.store.(*storage.SQLStore).DB().Exec("DELETE FROM gateways WHERE id=?", g.ID); err != nil {
		t.Fatal(err)
	}
	historical, err := c.GetNginxRelease(ctx, pool.ID, release.ID)
	if err != nil || len(historical.Targets) != 1 || len(historical.Gateways) != 0 {
		t.Fatalf("historical target rewritten: %+v %v", historical, err)
	}
}

func TestNginxPreviousUncertainReleaseBlocksNewPublication(t *testing.T) {
	for _, status := range []string{"RUNNING", "UNKNOWN"} {
		t.Run(status, func(t *testing.T) {
			c, pool, g, v := nginxValidationFixture(t, false)
			ctx := context.Background()
			finishValidationFixture(t, c, pool.ID, g, v)
			previous := NginxRelease{PoolID: pool.ID, Targets: []NginxReleaseTarget{{GatewayID: g.ID, Status: status}}, Status: "PUBLISHING"}
			previous.ID = id.New("ngrel")
			if err := c.saveControl(ctx, previous.ID, "nginx-release", pool.ID, previous, 0); err != nil {
				t.Fatal(err)
			}
			if err := c.saveControl(ctx, pool.ID+":nginx-release-head", "nginx-release-head", pool.ID, map[string]string{"release_id": previous.ID}, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := c.PublishNginx(ctx, pool.ID, v.ID, "reviewer", "new", v.DraftRevision); err == nil {
				t.Fatal("new publication overwrote uncertain task")
			}
			head, _, err := readControl[map[string]string](ctx, c.store, pool.ID+":nginx-release-head", "nginx-release-head")
			if err != nil || head["release_id"] != previous.ID {
				t.Fatalf("uncertain task lost: %+v %v", head, err)
			}
		})
	}
}
