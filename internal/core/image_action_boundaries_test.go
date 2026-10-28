package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func imageCandidateFixture(t *testing.T, c *Core, env Environment, status string) ImageUpdate {
	t.Helper()
	ctx := context.Background()
	app, err := c.store.GetApplication(ctx, env.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	images := map[string]string{}
	for service, image := range rev.ConfigurationSource.Images {
		images[service] = image
	}
	images["web"] = "registry.example.test/web@sha256:" + strings.Repeat("c", 64)
	u := ImageUpdate{EnvironmentID: env.ID, BaseRevisionID: rev.ID, Images: images, Changed: []string{"web"}, Status: status}
	key := env.ID + ":candidate"
	if err := c.saveControl(ctx, key, "image-update", env.ID, u, 0); err != nil {
		t.Fatal(err)
	}
	u, d, err := readControl[ImageUpdate](ctx, c.store, key, "image-update")
	if err != nil {
		t.Fatal(err)
	}
	u.ControlDocument = d
	return u
}

func TestImageCandidateActionsRespectOwnershipAndRevision(t *testing.T) {
	for _, tc := range []struct {
		name, action, status string
		expected             int64
		mutate               func(*Core, *Environment, *ImageUpdate)
		want                 error
	}{
		{"stale decision", "deploy", "AVAILABLE", 2, nil, storage.ErrPrecondition},
		{"unknown action", "remove", "AVAILABLE", 1, nil, nil},
		{"ignore in-flight", "ignore", "SUBMITTING", 1, nil, storage.ErrConflict},
		{"deploy in-flight", "deploy", "SUBMITTING", 1, nil, storage.ErrConflict},
		{"retry in-flight", "retry", "SUBMITTING", 1, nil, storage.ErrConflict},
		{"deploy ignored candidate", "deploy", "IGNORED", 1, nil, storage.ErrConflict},
		{"ignore already queued", "ignore", "AVAILABLE", 1, func(_ *Core, _ *Environment, u *ImageUpdate) { u.OperationID = "existing" }, storage.ErrConflict},
		{"baseline changed", "deploy", "AVAILABLE", 1, func(_ *Core, _ *Environment, u *ImageUpdate) { u.BaseRevisionID = "older" }, storage.ErrPrecondition},
		{"management disabled", "deploy", "AVAILABLE", 1, func(c *Core, _ *Environment, _ *ImageUpdate) { c.config.Management.Enabled = false }, nil},
		{"application stopped", "deploy", "AVAILABLE", 1, func(c *Core, e *Environment, _ *ImageUpdate) {
			a, err := c.store.GetApplication(context.Background(), e.ApplicationID)
			if err != nil {
				t.Fatal(err)
			}
			a.Status = model.ApplicationStopped
			if err := c.store.UpdateApplication(context.Background(), &a, a.Revision); err != nil {
				t.Fatal(err)
			}
		}, nil},
		{"service intentionally stopped", "deploy", "AVAILABLE", 1, func(c *Core, e *Environment, _ *ImageUpdate) {
			a, err := c.store.GetApplication(context.Background(), e.ApplicationID)
			if err != nil {
				t.Fatal(err)
			}
			a.ReplicaOverrides = map[string]int{"web": 0}
			if err := c.store.UpdateApplication(context.Background(), &a, a.Revision); err != nil {
				t.Fatal(err)
			}
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, env := imageEnvironmentFixture(t)
			ctx := context.Background()
			u := imageCandidateFixture(t, c, env, tc.status)
			if tc.mutate != nil {
				tc.mutate(c, &env, &u)
				if err := c.saveControl(ctx, u.ID, "image-update", env.ID, u, 1); err != nil {
					t.Fatal(err)
				}
				u.Revision = 2
			}
			expected := tc.expected
			if tc.mutate != nil {
				expected = u.Revision
			}
			_, err := c.ImageUpdateAction(ctx, env.ID, u.ID, tc.action, "admin", expected)
			if err == nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("unsafe image action accepted or misdiagnosed: %v", err)
			}
			actual, _, readErr := readControl[ImageUpdate](ctx, c.store, u.ID, "image-update")
			if readErr != nil || actual.Status != u.Status || actual.OperationID != u.OperationID {
				t.Fatalf("rejected action changed candidate: %#v %v", actual, readErr)
			}
			ops, err := c.store.ListDeploymentOperations(ctx, env.ApplicationID, storage.Page{Limit: 10})
			if err != nil || len(ops) != 1 {
				t.Fatalf("rejected action queued work: %#v %v", ops, err)
			}
		})
	}
}

func TestImageCandidateIgnoreAndQueueHaveDistinctOutcomes(t *testing.T) {
	t.Run("ignore", func(t *testing.T) {
		c, env := imageEnvironmentFixture(t)
		ctx := context.Background()
		u := imageCandidateFixture(t, c, env, "AVAILABLE")
		ignored, err := c.ImageUpdateAction(ctx, env.ID, u.ID, "ignore", "admin", u.Revision)
		if err != nil || ignored.Status != "IGNORED" || ignored.OperationID != "" {
			t.Fatalf("ignore result=%#v %v", ignored, err)
		}
		stored, _, err := readControl[ImageUpdate](ctx, c.store, u.ID, "image-update")
		if err != nil || stored.Status != "IGNORED" {
			t.Fatal("ignore decision not durable")
		}
	})
	t.Run("queue", func(t *testing.T) {
		c, env := imageEnvironmentFixture(t)
		ctx := context.Background()
		u := imageCandidateFixture(t, c, env, "AVAILABLE")
		queued, err := c.ImageUpdateAction(ctx, env.ID, u.ID, "deploy", "admin", u.Revision)
		if err != nil || queued.Status != string(model.OperationQueued) || queued.OperationID == "" {
			t.Fatalf("image deployment not queued: %#v %v", queued, err)
		}
		op, err := c.store.GetDeploymentOperation(ctx, queued.OperationID)
		if err != nil || op.Status != model.OperationQueued || op.ApplicationID != env.ApplicationID {
			t.Fatalf("queued operation lost identity: %#v %v", op, err)
		}
		app, err := c.store.GetApplication(ctx, env.ApplicationID)
		if err != nil || app.ActiveRevisionID != u.BaseRevisionID || app.DesiredRevisionID != op.RevisionID {
			t.Fatalf("queued update falsely changed running revision: %#v %v", app, err)
		}
		updates, err := c.ListImageUpdates(ctx, env.ID, storage.Page{Limit: 10})
		if err != nil || len(updates) != 1 || updates[0].Status != string(model.OperationQueued) {
			t.Fatalf("candidate status not associated with task: %#v %v", updates, err)
		}
		if _, err := c.ImageUpdateAction(ctx, env.ID, u.ID, "retry", "admin", updates[0].Revision); err == nil {
			t.Fatal("queued image update retried concurrently")
		}
		if _, err := c.ImageUpdateAction(ctx, "missing", u.ID, "ignore", "admin", 1); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("unknown environment action=%v", err)
		}
		if _, err := c.ImageUpdateAction(ctx, env.ID, "missing", "ignore", "admin", 1); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("unknown candidate action=%v", err)
		}
		other, err := c.CreateEnvironment(ctx, env.ProjectID, EnvironmentInput{Name: "other", RuntimeID: env.RuntimeID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.ImageUpdateAction(ctx, other.ID, u.ID, "ignore", "admin", updates[0].Revision); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("cross-environment action accepted: %v", err)
		}
	})
}
