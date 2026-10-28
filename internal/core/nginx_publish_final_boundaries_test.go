package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type publicationFinalStore struct {
	*nginxBoundaryStore
	stage string
	lists int
}

func (s *publicationFinalStore) ListGateways(ctx context.Context, p storage.Page) ([]model.Gateway, error) {
	s.lists++
	values, e := s.nginxBoundaryStore.ListGateways(ctx, p)
	if s.stage == "membership removed" && s.lists == 2 || s.stage == "empty validation" {
		return nil, e
	}
	return values, e
}
func (s *publicationFinalStore) BuildSnapshot(ctx context.Context, pool string) (model.Snapshot, error) {
	v, e := s.Store.BuildSnapshot(ctx, pool)
	if s.stage == "snapshot changed" {
		v.Revision++
	}
	return v, e
}
func (s *publicationFinalStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if strings.HasSuffix(key, ":nginx-release-head") && s.stage == "corrupt head" {
		return storage.ControlDocument{ID: key, Revision: 1, Payload: json.RawMessage("{")}, nil
	}
	return s.nginxBoundaryStore.GetControlDocument(ctx, key)
}

func TestNginxPublicationFinalChecksPreservePriorTarget(t *testing.T) {
	for _, stage := range []string{"membership removed", "empty validation", "snapshot changed", "snapshot encryption", "corrupt head", "missing prior task", "prior gateway read"} {
		t.Run(stage, func(t *testing.T) {
			c, p, g, v := nginxValidationFixture(t, false)
			ctx := context.Background()
			finishValidationFixture(t, c, p.ID, g, v)
			base := c.store
			if stage == "empty validation" {
				actual, e := c.GetNginxValidation(ctx, p.ID, v.ID)
				if e != nil {
					t.Fatal(e)
				}
				actual.Results = map[string]NginxValidationResult{}
				if e = c.saveControl(ctx, v.ID, "nginx-validation", p.ID, actual, actual.Revision); e != nil {
					t.Fatal(e)
				}
			}
			if stage == "missing prior task" || stage == "prior gateway read" {
				old := NginxRelease{PoolID: p.ID, Status: "SUCCEEDED", Targets: []NginxReleaseTarget{{GatewayID: g.ID, Status: "SUCCEEDED"}}}
				if stage == "prior gateway read" {
					if e := c.saveControl(ctx, "previous", "nginx-release", p.ID, old, 0); e != nil {
						t.Fatal(e)
					}
				}
				if e := c.saveControl(ctx, p.ID+":nginx-release-head", "nginx-release-head", p.ID, map[string]string{"release_id": "previous"}, 0); e != nil {
					t.Fatal(e)
				}
			}
			wrapped := &publicationFinalStore{nginxBoundaryStore: &nginxBoundaryStore{Store: base}, stage: stage}
			c.store = wrapped
			failure := errors.New("previous gateway evidence unavailable")
			if stage == "prior gateway read" {
				wrapped.method = "gateway"
				wrapped.nth = 2
				wrapped.failure = failure
			}
			if stage == "snapshot encryption" {
				c.keyring = &security.Keyring{}
			}
			_, e := c.PublishNginx(ctx, p.ID, v.ID, "owner", "candidate", v.DraftRevision)
			if e == nil {
				t.Fatal("unsafe final publication accepted")
			}
			if stage == "prior gateway read" && !errors.Is(e, failure) {
				t.Fatal("previous target read failure hidden", e)
			}
			pool, e := base.GetPool(ctx, p.ID)
			if e != nil || pool.DesiredRevision != v.BaseRevision {
				t.Fatal("failed publication changed target", e)
			}
			versions, e := base.ListControlDocuments(ctx, "nginx-version", p.ID, storage.Page{Limit: 10})
			if e != nil || len(versions) != 0 {
				t.Fatal("failed publication created effective version", e)
			}
		})
	}
}

func TestNginxCompletedPriorReleaseAllowsNewPublication(t *testing.T) {
	c, p, g, v := nginxValidationFixture(t, false)
	ctx := context.Background()
	finishValidationFixture(t, c, p.ID, g, v)
	finished := time.Now().Add(-time.Minute)
	prior := NginxRelease{PoolID: p.ID, Status: "SUCCEEDED", FinishedAt: &finished, Targets: []NginxReleaseTarget{{GatewayID: g.ID, Status: "SUCCEEDED"}}}
	if e := c.saveControl(ctx, "previous", "nginx-release", p.ID, prior, 0); e != nil {
		t.Fatal(e)
	}
	if e := c.saveControl(ctx, p.ID+":nginx-release-head", "nginx-release-head", p.ID, map[string]string{"release_id": "previous"}, 0); e != nil {
		t.Fatal(e)
	}
	release, e := c.PublishNginx(ctx, p.ID, v.ID, "owner", "next", v.DraftRevision)
	if e != nil || release.ID == "previous" {
		t.Fatal("completed history blocked new publication", e)
	}
	old, e := c.GetNginxRelease(ctx, p.ID, "previous")
	if e != nil || old.Status != "SUCCEEDED" || old.FinishedAt == nil || !old.FinishedAt.Equal(finished) {
		t.Fatal("new publication rewrote completed history", e)
	}
}
