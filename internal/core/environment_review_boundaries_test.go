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

type reviewBoundaryStore struct {
	storage.Store
	operation    *model.DeploymentOperation
	operationErr error
	revision     *model.ApplicationRevision
	revisionErr  error
	documentID   string
	documentErr  error
}

func (s *reviewBoundaryStore) GetDeploymentOperationByIdempotency(ctx context.Context, runtime, key string) (model.DeploymentOperation, error) {
	if s.operationErr != nil {
		return model.DeploymentOperation{}, s.operationErr
	}
	if s.operation != nil {
		return *s.operation, nil
	}
	return s.Store.GetDeploymentOperationByIdempotency(ctx, runtime, key)
}
func (s *reviewBoundaryStore) GetApplicationRevision(ctx context.Context, key string) (model.ApplicationRevision, error) {
	if s.revisionErr != nil {
		return model.ApplicationRevision{}, s.revisionErr
	}
	if s.revision != nil {
		return *s.revision, nil
	}
	return s.Store.GetApplicationRevision(ctx, key)
}
func (s *reviewBoundaryStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if key == s.documentID && s.documentErr != nil {
		return storage.ControlDocument{}, s.documentErr
	}
	return s.Store.GetControlDocument(ctx, key)
}
func TestReviewedEnvironmentRejectsInvalidAuthorizationAndReviewEvidence(t *testing.T) {
	for _, mode := range []string{"environment unavailable", "management disabled", "missing review", "missing idempotency", "idempotency unavailable", "missing review record", "foreign kind", "foreign environment", "corrupt encryption", "expired", "review version mismatch", "current version mismatch", "retry revision unavailable", "retry different review", "retry foreign environment", "verified retry"} {
		t.Run(mode, func(t *testing.T) {
			c, _, env := environmentBoundaryFixture(t)
			ctx := context.Background()
			base := c.store
			failure := errors.New("review dependency unavailable")
			reviewID, idem := "review-fixture", "review-request"
			expected := env.Revision
			review := environmentReview{EnvironmentRevision: env.Revision, ExpiresAt: c.now().Add(time.Minute), Input: ApplicationPreflightInput{RuntimeID: env.RuntimeID, ComposeYAML: "services:\n  web:\n    image: example.test/web@sha256:" + strings.Repeat("a", 64) + "\n"}, Source: model.DeploymentSource{EnvironmentID: env.ID, PreflightID: reviewID}}
			kind, parent := "environment-review", env.ID
			store := &reviewBoundaryStore{Store: base}
			want := error(nil)
			switch mode {
			case "environment unavailable":
				store.documentID = env.ID
				store.documentErr = failure
				want = failure
			case "management disabled":
				c.config.Management.Enabled = false
			case "missing review":
				reviewID = ""
			case "missing idempotency":
				idem = ""
			case "idempotency unavailable":
				store.operationErr = failure
				want = failure
			case "missing review record":
				store.documentID = reviewID
				store.documentErr = storage.ErrNotFound
				want = storage.ErrNotFound
			case "foreign kind":
				kind = "variables"
				want = storage.ErrNotFound
			case "foreign environment":
				parent = "other"
				want = storage.ErrNotFound
			case "expired":
				review.ExpiresAt = c.now()
				want = storage.ErrPrecondition
			case "review version mismatch":
				review.EnvironmentRevision++
				want = storage.ErrPrecondition
			case "current version mismatch":
				expected++
				review.EnvironmentRevision = expected
				want = storage.ErrPrecondition
			}
			w, e := c.encryptedWrite("review-fixture", kind, parent, review, 0)
			if e != nil {
				t.Fatal(e)
			}
			if mode == "corrupt encryption" {
				w.Document.Payload = []byte(`{"ciphertext":"invalid"}`)
			}
			if e = base.WriteControlDocuments(ctx, []storage.ControlWrite{w}); e != nil {
				t.Fatal(e)
			}
			if strings.HasPrefix(mode, "retry") || mode == "verified retry" {
				store.operation = &model.DeploymentOperation{ID: "accepted", RuntimeID: env.RuntimeID, RevisionID: "accepted-revision", Status: model.OperationQueued}
				store.revision = &model.ApplicationRevision{ID: "accepted-revision", ConfigurationSource: review.Source}
				switch mode {
				case "retry revision unavailable":
					store.revisionErr = failure
					want = failure
				case "retry different review":
					store.revision.ConfigurationSource.PreflightID = "different"
					want = storage.ErrConflict
				case "retry foreign environment":
					store.revision.ConfigurationSource.EnvironmentID = "other"
					want = storage.ErrConflict
				}
			}
			c.store = store
			op, e := c.DeployReviewedEnvironment(ctx, env.ID, reviewID, "administrator", idem, expected)
			if mode == "verified retry" {
				if e != nil || op.ID != "accepted" {
					t.Fatal("verified retry lost accepted task", op, e)
				}
			} else if e == nil || want != nil && !errors.Is(e, want) {
				t.Fatal("invalid review accepted or dependency error hidden", op, e)
			}
			operations, e := base.ListDeploymentOperations(ctx, "", storage.Page{})
			if e != nil || len(operations) != 0 {
				t.Fatal("rejected/retried review created a new deployment", operations, e)
			}
			current, e := base.GetControlDocument(ctx, env.ID)
			if e != nil || current.Revision != env.Revision {
				t.Fatal("review failure changed environment", current, e)
			}
		})
	}
}
