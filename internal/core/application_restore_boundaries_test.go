package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func restoreBoundaryFixture(t *testing.T) (*Core, model.Application, string) {
	t.Helper()
	c, rt, _ := controlFixture(t)
	ctx := context.Background()
	in := ApplicationPreflightInput{RuntimeID: rt.ID, ComposeYAML: "services:\n  web:\n    image: example.test/app@sha256:" + strings.Repeat("a", 64) + "\n"}
	a, op, _, err := c.InstallApplication(ctx, InstallApplicationInput{ApplicationPreflightInput: in, Name: "restore", ProjectName: "restore", IdempotencyKey: "initial", Actor: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	old := op.RevisionID
	for i := 0; i < 2; i++ {
		if _, err = c.store.(*storage.SQLStore).DB().ExecContext(ctx, `UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?`, op.ID); err != nil {
			t.Fatal(err)
		}
		a, err = c.store.GetApplication(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		a.ActiveRevisionID, a.Status = op.RevisionID, model.ApplicationRunning
		if err = c.store.UpdateApplication(ctx, &a, a.Revision); err != nil {
			t.Fatal(err)
		}
		a, err = c.store.GetApplication(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			in.ComposeYAML = strings.ReplaceAll(in.ComposeYAML, strings.Repeat("a", 64), strings.Repeat("b", 64))
			_, op, _, err = c.CreateApplicationRevision(ctx, a.ID, CreateRevisionInput{ApplicationPreflightInput: in, IdempotencyKey: "second", Actor: "owner"}, a.Revision)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return c, a, old
}

type restoreBoundaryStore struct {
	*nginxBoundaryStore
	old      string
	mutate   func(*model.ApplicationRevision)
	external bool
}

func (s *restoreBoundaryStore) GetApplication(ctx context.Context, key string) (model.Application, error) {
	if err := s.fail("application", key); err != nil {
		return model.Application{}, err
	}
	a, err := s.Store.GetApplication(ctx, key)
	if s.external {
		a.Managed = false
	}
	return a, err
}
func (s *restoreBoundaryStore) GetApplicationRevision(ctx context.Context, key string) (model.ApplicationRevision, error) {
	if err := s.fail("revision", key); err != nil {
		return model.ApplicationRevision{}, err
	}
	r, err := s.Store.GetApplicationRevision(ctx, key)
	if err == nil && key == s.old && s.mutate != nil {
		s.mutate(&r)
	}
	return r, err
}

func TestApplicationRestoreReviewRejectsUnusableHistory(t *testing.T) {
	for _, name := range []string{"application read", "revision read", "external", "foreign", "compose ciphertext", "environment ciphertext", "resolved ciphertext", "invalid environment", "invalid compose", "invalid resolved", "mutable image", "review write"} {
		t.Run(name, func(t *testing.T) {
			c, a, old := restoreBoundaryFixture(t)
			base := c.store
			fault := &restoreBoundaryStore{nginxBoundaryStore: &nginxBoundaryStore{Store: base, failure: errors.New("restore storage unavailable")}, old: old}
			cipher := func(kind, value string) []byte {
				b, err := c.keyring.Encrypt([]byte(value), revisionAAD(old, kind))
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			switch name {
			case "application read":
				fault.method = "application"
			case "revision read":
				fault.method, fault.key = "revision", old
			case "review write":
				fault.method = "write"
			case "external":
				fault.external = true
			case "foreign":
				fault.mutate = func(r *model.ApplicationRevision) { r.ApplicationID = "another-application" }
			case "compose ciphertext":
				fault.mutate = func(r *model.ApplicationRevision) { r.EncryptedCompose = []byte("corrupt") }
			case "environment ciphertext":
				fault.mutate = func(r *model.ApplicationRevision) { r.EncryptedEnvironment = []byte("corrupt") }
			case "resolved ciphertext":
				fault.mutate = func(r *model.ApplicationRevision) { r.EncryptedResolved = []byte("corrupt") }
			case "invalid environment":
				fault.mutate = func(r *model.ApplicationRevision) { r.EncryptedEnvironment = cipher("environment", "INVALID LINE") }
			case "invalid compose":
				fault.mutate = func(r *model.ApplicationRevision) { r.EncryptedCompose = cipher("compose", "services: [") }
			case "invalid resolved":
				fault.mutate = func(r *model.ApplicationRevision) { r.EncryptedResolved = cipher("resolved", "services: [") }
			case "mutable image":
				fault.mutate = func(r *model.ApplicationRevision) {
					r.EncryptedResolved = cipher("resolved", "services:\n  web:\n    image: example.test/app:latest\n")
					r.ConfigurationSource.Images = nil
				}
			}
			c.store = fault
			if _, err := c.ReviewApplicationRestore(context.Background(), a.ID, old); err == nil {
				t.Fatal("unusable historical revision accepted")
			}
			docs, err := base.ListControlDocuments(context.Background(), "application-restore", a.ID, storage.Page{})
			if err != nil || len(docs) != 0 {
				t.Fatalf("failed review persisted: %+v %v", docs, err)
			}
			actual, err := base.GetApplication(context.Background(), a.ID)
			if err != nil || actual.Revision != a.Revision || actual.ActiveRevisionID != a.ActiveRevisionID {
				t.Fatal("review changed running application", err)
			}
		})
	}
}

func TestApplicationRestoreRequiresCurrentAcknowledgedReview(t *testing.T) {
	for _, name := range []string{"empty key", "blank key", "no acknowledgement", "missing review", "foreign application", "stale baseline", "expired", "expiry boundary", "source missing", "review unavailable", "externally managed"} {
		t.Run(name, func(t *testing.T) {
			c, a, old := restoreBoundaryFixture(t)
			ctx := context.Background()
			r, err := c.ReviewApplicationRestore(ctx, a.ID, old)
			if err != nil {
				t.Fatal(err)
			}
			base := c.store
			app, review, key, expected, ack := a.ID, r.ID, "restore", r.BaseRevision, true
			switch name {
			case "empty key":
				key = ""
			case "blank key":
				key = "  "
			case "no acknowledgement":
				ack = false
			case "missing review":
				review = "missing"
			case "foreign application":
				app = "foreign"
			case "stale baseline":
				expected++
			case "expired":
				c.now = func() time.Time { return r.ExpiresAt.Add(time.Second) }
			case "expiry boundary":
				c.now = func() time.Time { return r.ExpiresAt }
			case "source missing":
				c.store = &restoreBoundaryStore{nginxBoundaryStore: &nginxBoundaryStore{Store: base, method: "revision", key: old, failure: storage.ErrNotFound}}
			case "review unavailable":
				c.store = &nginxBoundaryStore{Store: base, method: "document", key: r.ID, failure: errors.New("unavailable")}
			case "externally managed":
				c.store = &restoreBoundaryStore{nginxBoundaryStore: &nginxBoundaryStore{Store: base}, external: true}
			}
			op, err := c.RestoreApplication(ctx, app, review, "owner", key, expected, ack)
			if err == nil || op.ID != "" {
				t.Fatalf("unsafe restoration queued: %+v %v", op, err)
			}
			actual, err := base.GetApplication(ctx, a.ID)
			if err != nil || actual.Revision != a.Revision || actual.DesiredRevisionID != a.DesiredRevisionID {
				t.Fatal("rejected restore changed target", err)
			}
		})
	}
}
