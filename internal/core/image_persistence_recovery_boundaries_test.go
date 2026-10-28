package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"strings"
	"testing"
	"time"
)

func TestImagePollingCannotOverwriteUnknownOrNewerCheck(t *testing.T) {
	for _, mode := range []string{"policy read", "initial state read", "lease write", "application read", "final state read", "new worker", "long interval failure"} {
		t.Run(mode, func(t *testing.T) {
			c, v := imageEnvironmentFixture(t)
			ctx := context.Background()
			base := c.store
			now := c.now()
			c.now = func() time.Time { return now }
			policy := ImagePolicy{Enabled: true, Poll: true, Services: []string{"web"}, IntervalSeconds: 86400}
			if mode == "long interval failure" {
				policy.Services = []string{"removed"}
			}
			if e := c.saveControl(ctx, v.ID+":image-policy", "image-policy", v.ID, policy, 0); e != nil {
				t.Fatal(e)
			}
			failure := errors.New("image check persistence unavailable")
			checkReads := 0
			wrapped := &imageControlStore{Store: base}
			wrapped.read = func(ctx context.Context, key string) (storage.ControlDocument, error) {
				if mode == "policy read" && strings.HasSuffix(key, ":image-policy") {
					return storage.ControlDocument{}, failure
				}
				if strings.HasSuffix(key, ":image-check") {
					checkReads++
					if (mode == "initial state read" && checkReads == 1) || (mode == "final state read" && checkReads == 2) {
						return storage.ControlDocument{}, failure
					}
					if mode == "new worker" && checkReads == 2 {
						d, e := base.GetControlDocument(ctx, key)
						if e != nil {
							return d, e
						}
						var s ImageCheck
						if e = json.Unmarshal(d.Payload, &s); e != nil {
							return d, e
						}
						s.Worker = "newer worker"
						d.Payload, _ = json.Marshal(s)
						return d, nil
					}
				}
				return base.GetControlDocument(ctx, key)
			}
			wrapped.write = func(ctx context.Context, w []storage.ControlWrite) error {
				if mode == "lease write" {
					return failure
				}
				return base.WriteControlDocuments(ctx, w)
			}
			if mode == "application read" {
				wrapped.Store = &restoreBoundaryStore{nginxBoundaryStore: &nginxBoundaryStore{Store: base, method: "application", failure: failure}}
			}
			c.store = wrapped
			c.checkEnvironmentImages(ctx, v)
			c.store = base
			state, e := c.GetImageCheck(ctx, v.ID)
			if e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "policy read", "initial state read", "lease write":
				if state.LastChecked != nil || state.Worker != "" {
					t.Fatal("failed precondition created successful check", state)
				}
			case "final state read", "new worker":
				if state.LastChecked != nil || state.Worker == "" || state.LeaseUntil.IsZero() {
					t.Fatal("unverified ownership was overwritten", state)
				}
			default:
				if state.LastChecked == nil || state.LastSuccess != nil || state.Error == "" || state.Failures != 1 || state.Worker != "" {
					t.Fatal("failed check reported success", state)
				}
				if state.NextCheck.Before(now.Add(24 * time.Hour)) {
					t.Fatal("failure shortened configured polling delay")
				}
			}
			updates, e := base.ListControlDocuments(ctx, "image-update", v.ID, storage.Page{Limit: 10})
			if e != nil || len(updates) != 0 {
				t.Fatal("failed observation created update", e)
			}
		})
	}
}

type imageRecoveryPageStore struct {
	storage.Store
	mode   string
	pages  []string
	writes []storage.ControlWrite
	now    time.Time
}

func (s *imageRecoveryPageStore) ListControlDocuments(_ context.Context, kind, parent string, p storage.Page) ([]storage.ControlDocument, error) {
	s.pages = append(s.pages, p.After)
	if s.mode == "list error" {
		return nil, errors.New("candidate list unavailable")
	}
	if p.After != "" {
		return nil, nil
	}
	var out []storage.ControlDocument
	for i := 0; i < 200; i++ {
		u := ImageUpdate{EnvironmentID: parent, Status: "SUBMITTING"}
		raw, _ := json.Marshal(u)
		at := s.now.Add(-3 * time.Minute)
		if i == 0 {
			raw = []byte("invalid")
		}
		if i == 1 {
			at = s.now
		}
		if i == 2 {
			u.Status = "AVAILABLE"
			raw, _ = json.Marshal(u)
		}
		out = append(out, storage.ControlDocument{ID: fmt.Sprintf("candidate-%03d", i), Kind: kind, ParentID: parent, Revision: 2, UpdatedAt: at, Payload: raw})
	}
	return out, nil
}
func (s *imageRecoveryPageStore) GetDeploymentOperationByIdempotency(_ context.Context, runtime, key string) (model.DeploymentOperation, error) {
	if s.mode == "lookup error" {
		return model.DeploymentOperation{}, errors.New("operation result unavailable")
	}
	if s.mode == "not found" {
		return model.DeploymentOperation{}, storage.ErrNotFound
	}
	return model.DeploymentOperation{ID: "accepted", Status: model.OperationRunning, ErrorMessage: ""}, nil
}
func (s *imageRecoveryPageStore) WriteControlDocuments(_ context.Context, w []storage.ControlWrite) error {
	s.writes = append(s.writes, w...)
	return nil
}
func TestImageSubmissionRecoveryPreservesUnknownOutcomesAndPaginates(t *testing.T) {
	for _, mode := range []string{"list error", "lookup error", "not found", "accepted"} {
		t.Run(mode, func(t *testing.T) {
			c, v := imageEnvironmentFixture(t)
			f := &imageRecoveryPageStore{Store: c.store, mode: mode, now: c.now()}
			c.store = f
			c.recoverImageSubmissions(context.Background(), v)
			if mode == "list error" || mode == "lookup error" {
				if len(f.writes) != 0 {
					t.Fatal("unknown result changed candidate")
				}
			} else {
				if len(f.writes) != 197 {
					t.Fatal("recovery altered fresh or malformed candidate", len(f.writes))
				}
				for _, w := range f.writes {
					var u ImageUpdate
					if e := json.Unmarshal(w.Document.Payload, &u); e != nil {
						t.Fatal(e)
					}
					if mode == "accepted" && (u.OperationID != "accepted" || u.Status != string(model.OperationRunning)) {
						t.Fatal("accepted operation identity lost", u)
					}
					if mode == "not found" && (u.OperationID != "" || u.Status != "FAILED" || u.Error == "") {
						t.Fatal("missing operation not quarantined", u)
					}
				}
			}
			if mode != "list error" && (len(f.pages) != 2 || f.pages[1] != "candidate-199") {
				t.Fatal("candidate pagination incomplete", f.pages)
			}
		})
	}
}
