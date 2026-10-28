package core

import (
	"context"
	"errors"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type imageOperationBoundaryStore struct {
	*restoreBoundaryStore
	operations []model.DeploymentOperation
	operation  *model.DeploymentOperation
}

func (s *imageOperationBoundaryStore) ListDeploymentOperations(ctx context.Context, key string, p storage.Page) ([]model.DeploymentOperation, error) {
	if e := s.fail("operations", key); e != nil {
		return nil, e
	}
	if s.operations != nil {
		return s.operations, nil
	}
	return s.Store.ListDeploymentOperations(ctx, key, p)
}
func (s *imageOperationBoundaryStore) GetDeploymentOperation(ctx context.Context, key string) (model.DeploymentOperation, error) {
	if e := s.fail("operation", key); e != nil {
		return model.DeploymentOperation{}, e
	}
	if s.operation != nil {
		return *s.operation, nil
	}
	return s.Store.GetDeploymentOperation(ctx, key)
}

func TestImageControlReadAndWriteFailuresPreserveState(t *testing.T) {
	for _, tc := range []struct {
		name, method, key string
		run               func(context.Context, *Core, Environment) error
	}{
		{"policy environment", "document", "env_", func(ctx context.Context, c *Core, v Environment) error { _, e := c.GetImagePolicy(ctx, v.ID); return e }},
		{"save environment", "document", "env_", func(ctx context.Context, c *Core, v Environment) error {
			_, e := c.SaveImagePolicy(ctx, v.ID, ImagePolicy{}, 0)
			return e
		}},
		{"check environment", "document", "env_", func(ctx context.Context, c *Core, v Environment) error { _, e := c.GetImageCheck(ctx, v.ID); return e }},
		{"request state", "document", ":image-check", func(ctx context.Context, c *Core, v Environment) error {
			_, e := c.RequestImageCheck(ctx, v.ID)
			return e
		}},
		{"list environment", "document", "env_", func(ctx context.Context, c *Core, v Environment) error {
			_, e := c.ListImageUpdates(ctx, v.ID, storage.Page{Limit: 10})
			return e
		}},
		{"list candidates", "documents", "image-update", func(ctx context.Context, c *Core, v Environment) error {
			_, e := c.ListImageUpdates(ctx, v.ID, storage.Page{Limit: 10})
			return e
		}},
		{"hook environment", "document", "env_", func(ctx context.Context, c *Core, v Environment) error {
			_, _, e := c.RotateHook(ctx, v.ID, 0)
			return e
		}},
		{"policy application", "application", "", func(ctx context.Context, c *Core, v Environment) error {
			_, e := c.SaveImagePolicy(ctx, v.ID, ImagePolicy{Enabled: true, Poll: true, Services: []string{"web"}}, 0)
			return e
		}},
		{"policy revision", "revision", "", func(ctx context.Context, c *Core, v Environment) error {
			_, e := c.SaveImagePolicy(ctx, v.ID, ImagePolicy{Enabled: true, Poll: true, Services: []string{"web"}}, 0)
			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, v := imageEnvironmentFixture(t)
			base := c.store
			failure := errors.New("image persistence unavailable")
			c.store = &restoreBoundaryStore{nginxBoundaryStore: &nginxBoundaryStore{Store: base, method: tc.method, key: tc.key, failure: failure}}
			if e := tc.run(context.Background(), c, v); !errors.Is(e, failure) {
				t.Fatalf("lost cause: %v", e)
			}
			docs, e := base.ListControlDocuments(context.Background(), "image-policy", v.ID, storage.Page{Limit: 10})
			if e != nil || len(docs) != 0 {
				t.Fatal("failed read authorized image policy", e)
			}
		})
	}
}

func TestImageCandidateStorageAndHistoryFailuresDoNotQueue(t *testing.T) {
	for _, stage := range []string{"application", "revision", "operations", "candidate write", "compose ciphertext", "environment ciphertext", "environment syntax", "resolved ciphertext", "resolved syntax", "mutable image", "different pinned image", "automatic policy read", "automatic disabled", "automatic manual"} {
		t.Run(stage, func(t *testing.T) {
			c, v := imageEnvironmentFixture(t)
			ctx := context.Background()
			u := imageCandidateFixture(t, c, v, "AVAILABLE")
			base := c.store
			failure := errors.New("candidate database unavailable")
			f := &nginxBoundaryStore{Store: base, failure: failure}
			wrapped := &imageOperationBoundaryStore{restoreBoundaryStore: &restoreBoundaryStore{nginxBoundaryStore: f, old: u.BaseRevisionID}}
			c.store = wrapped
			actor := "owner"
			switch stage {
			case "application", "revision", "operations":
				f.method = stage
			case "candidate write":
				f.method = "write"
			case "automatic policy read":
				actor = "image-updater"
				f.method = "document"
				f.key = ":image-policy"
			case "automatic disabled":
				actor = "image-updater"
			case "automatic manual":
				actor = "image-updater"
				if _, e := c.SaveImagePolicy(ctx, v.ID, ImagePolicy{Enabled: true, Poll: true, Automatic: false, Services: []string{"web"}}, 0); e != nil {
					t.Fatal(e)
				}
			default:
				wrapped.mutate = func(r *model.ApplicationRevision) {
					switch stage {
					case "compose ciphertext":
						r.EncryptedCompose = []byte("invalid")
					case "environment ciphertext":
						r.EncryptedEnvironment = []byte("invalid")
					case "resolved ciphertext":
						r.EncryptedResolved = []byte("invalid")
					case "different pinned image":
						r.ConfigurationSource.Images = map[string]string{}
					default:
						raw, field := "not: [valid", "resolved"
						if stage == "environment syntax" {
							raw, field = "=invalid", "environment"
						}
						if stage == "mutable image" {
							raw = "services:\n  web:\n    image: example.test/web:latest\n"
						}
						encrypted, e := c.keyring.Encrypt([]byte(raw), revisionAAD(r.ID, field))
						if e != nil {
							t.Fatal(e)
						}
						if field == "environment" {
							r.EncryptedEnvironment = encrypted
						} else {
							r.EncryptedResolved = encrypted
						}
					}
				}
			}
			_, e := c.ImageUpdateAction(ctx, v.ID, u.ID, "deploy", actor, u.Revision)
			if e == nil {
				t.Fatal("unverified image candidate queued")
			}
			if f.method != "" && !errors.Is(e, failure) {
				t.Fatalf("lost storage failure: %v", e)
			}
			current, _, e := readControl[ImageUpdate](ctx, base, u.ID, "image-update")
			if e != nil || current.Status != "AVAILABLE" || current.OperationID != "" {
				t.Fatal("failed preparation changed candidate", e)
			}
			ops, e := base.ListDeploymentOperations(ctx, v.ApplicationID, storage.Page{Limit: 10})
			if e != nil || len(ops) != 1 {
				t.Fatal("failed preparation queued operation", e)
			}
		})
	}
}

func TestImageCandidateRetryDistinguishesPriorOperationStates(t *testing.T) {
	for _, stage := range []string{"missing", "queued", "running", "succeeded", "failed", "deploy again", "active queued", "active running"} {
		t.Run(stage, func(t *testing.T) {
			c, v := imageEnvironmentFixture(t)
			ctx := context.Background()
			u := imageCandidateFixture(t, c, v, "AVAILABLE")
			base := c.store
			u.OperationID = "prior"
			if e := c.saveControl(ctx, u.ID, "image-update", v.ID, u, u.Revision); e != nil {
				t.Fatal(e)
			}
			u.Revision++
			f := &nginxBoundaryStore{Store: base}
			w := &imageOperationBoundaryStore{restoreBoundaryStore: &restoreBoundaryStore{nginxBoundaryStore: f}, operation: &model.DeploymentOperation{ID: "prior", Status: model.OperationFailed}}
			action := "retry"
			switch stage {
			case "missing":
				f.method = "operation"
				f.failure = storage.ErrNotFound
			case "queued":
				w.operation.Status = model.OperationQueued
			case "running":
				w.operation.Status = model.OperationRunning
			case "succeeded":
				w.operation.Status = model.OperationSucceeded
			case "deploy again":
				action = "deploy"
			case "active queued":
				w.operations = []model.DeploymentOperation{{Status: model.OperationQueued}}
			case "active running":
				w.operations = []model.DeploymentOperation{{Status: model.OperationRunning}}
			}
			c.store = w
			got, e := c.ImageUpdateAction(ctx, v.ID, u.ID, action, "owner", u.Revision)
			if stage == "failed" {
				if e != nil || got.OperationID == "prior" || got.OperationID == "" || got.Status != "QUEUED" {
					t.Fatalf("failed attempt did not create tracked retry: %+v %v", got, e)
				}
				return
			}
			if e == nil {
				t.Fatal("unsafe retry accepted")
			}
			want := storage.ErrConflict
			if stage == "missing" {
				want = storage.ErrNotFound
			}
			if !errors.Is(e, want) {
				t.Fatal(e)
			}
			current, _, e := readControl[ImageUpdate](ctx, base, u.ID, "image-update")
			if e != nil || current.OperationID != "prior" || current.Status != "AVAILABLE" {
				t.Fatal("rejected retry overwrote prior result", e)
			}
		})
	}
}
