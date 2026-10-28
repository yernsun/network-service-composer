package core

import (
	"context"
	"errors"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
	"testing"
)

type previewSelectionStore struct {
	storage.Store
	pool         string
	snapshot     model.Snapshot
	failSnapshot bool
	failRoute    bool
	routes       int
	failure      error
}

func (s *previewSelectionStore) BuildSnapshot(context.Context, string) (model.Snapshot, error) {
	if s.failSnapshot {
		return model.Snapshot{}, s.failure
	}
	return s.snapshot, nil
}
func (s *previewSelectionStore) GetDomain(_ context.Context, key string) (model.Domain, error) {
	return model.Domain{ID: key, PoolID: s.pool}, nil
}
func (s *previewSelectionStore) GetRoute(context.Context, string) (model.Route, error) {
	s.routes++
	if s.failRoute && s.routes == 2 {
		return model.Route{}, s.failure
	}
	return model.Route{ID: "route-z", DomainID: "z"}, nil
}
func TestNginxPreviewSelectionAndRenderFailureStayReadOnly(t *testing.T) {
	for _, stage := range []string{"default first domain", "explicit domain", "server focus", "location focus", "HTTP focus", "explicit overrides focus", "negative focus", "large focus", "snapshot failure", "route lookup failure", "active render failure", "candidate render failure"} {
		t.Run(stage, func(t *testing.T) {
			c, _, pool := controlFixture(t)
			base := c.store
			failure := errors.New("isolated preview failure")
			store := &previewSelectionStore{Store: base, pool: pool.ID, failure: failure, snapshot: model.Snapshot{PoolID: pool.ID, Revision: 9, Domains: []model.SnapshotDomain{{ID: "z", Host: "z.example"}, {ID: "a", Host: "a.example"}}, Routes: []model.SnapshotRoute{{ID: "rz", Host: "z.example"}, {ID: "ra", Host: "a.example"}}, Snippets: []model.ConfigSnippet{{Scope: "http", Content: "gzip off;"}}}}
			c.store = store
			in := NginxPreviewInput{Snippets: []model.ConfigSnippet{{Scope: "http", Content: "gzip on;"}}}
			focus := 0
			wantDomain := "a"
			switch stage {
			case "explicit domain":
				in.DomainID = "z"
				wantDomain = "z"
			case "server focus":
				in.FocusIndex = &focus
				in.Snippets[0].Scope = "server"
				in.Snippets[0].ResourceID = "z"
				wantDomain = "z"
			case "location focus", "route lookup failure":
				in.FocusIndex = &focus
				in.Snippets[0].Scope = "location"
				in.Snippets[0].ResourceID = "route-z"
				wantDomain = "z"
				store.failRoute = stage == "route lookup failure"
			case "HTTP focus":
				in.FocusIndex = &focus
			case "explicit overrides focus":
				in.FocusIndex = &focus
				in.DomainID = "a"
				in.Snippets[0].Scope = "server"
				in.Snippets[0].ResourceID = "z"
			case "negative focus":
				focus = -1
				in.FocusIndex = &focus
			case "large focus":
				focus = 1
				in.FocusIndex = &focus
			case "snapshot failure":
				store.failSnapshot = true
			}
			seen := []model.Snapshot{}
			result, e := c.PreviewNginx(context.Background(), pool.ID, in, func(s model.Snapshot) (nginxx.PreviewDocument, error) {
				seen = append(seen, s)
				if stage == "active render failure" && len(seen) == 1 || stage == "candidate render failure" && len(seen) == 2 {
					return nginxx.PreviewDocument{}, failure
				}
				return nginxx.PreviewDocument{Content: s.Snippets[0].Content}, nil
			})
			invalidFocus := stage == "negative focus" || stage == "large focus"
			failed := invalidFocus || store.failSnapshot || store.failRoute || stage == "active render failure" || stage == "candidate render failure"
			if failed {
				if e == nil {
					t.Fatal("failed preview accepted")
				}
				if !invalidFocus && !errors.Is(e, failure) {
					t.Fatal("preview hid original failure", e)
				}
			} else {
				if e != nil || result.DomainID != wantDomain || len(result.Domains) != 2 || result.Domains[0].ID != "a" || len(seen) != 2 || result.Active.Content != "gzip off;" || result.Candidate.Content != "gzip on;" {
					t.Fatalf("preview selection/content mismatch: %+v %v", result, e)
				}
			}
			for _, snap := range seen {
				if len(snap.Domains) != 1 || snap.Domains[0].ID != wantDomain || len(snap.Routes) != 1 || snap.Routes[0].Host != snap.Domains[0].Host || snap.Revision != 9 {
					t.Fatalf("cross-domain preview content leaked: %+v", snap)
				}
			}
			after, e := base.GetPool(context.Background(), pool.ID)
			if e != nil || after.DesiredRevision != pool.DesiredRevision {
				t.Fatal("preview changed production target", e)
			}
			draft, e := base.GetControlDocument(context.Background(), pool.ID+":nginx-draft")
			if !errors.Is(e, storage.ErrNotFound) || draft.Revision != 0 {
				t.Fatal("preview persisted a draft", e)
			}
		})
	}
}
