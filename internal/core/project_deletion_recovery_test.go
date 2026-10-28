package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestProjectDeletionRecoveryHoldPreservesEvidenceBeforeLeaseOrRuntimeCalls(t *testing.T) {
	for _, barrier := range []bool{false, true} {
		t.Run(map[bool]string{false: "individual hold", true: "global barrier"}[barrier], func(t *testing.T) {
			c, p, _, _, runtime, worker := projectDeletionFixture(t)
			ctx := context.Background()
			preview, err := c.PreviewProjectDeletion(ctx, p.ID, true)
			if err != nil {
				t.Fatal(err)
			}
			task, err := c.DeleteProject(ctx, p.ID, ProjectDeletionInput{PreviewID: preview.ID, Force: true, ConfirmSlug: p.Slug}, preview.ProjectRevision, "restore-delete", "admin")
			if err != nil {
				t.Fatal(err)
			}
			before, err := c.store.GetControlDocument(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			key := storage.RecoveryHoldID("project-deletion", task.ID)
			if barrier {
				key = storage.RecoveryBarrierID
			}
			now := time.Now().UTC()
			if err = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: key, Kind: "recovery-hold", Payload: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}}}); err != nil {
				t.Fatal(err)
			}
			// A nil locker makes an accidental lease or runtime invocation fail.
			worker.Locker = nil
			if err = worker.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if err = worker.execute(ctx, task.ID, nil); err != nil {
				t.Fatal(err)
			}
			after, err := c.store.GetControlDocument(ctx, task.ID)
			if err != nil || after.Revision != before.Revision || string(after.Payload) != string(before.Payload) || runtime.removed != 0 {
				t.Fatal("held deletion changed original evidence or removed resources", err)
			}
			c.config.Controller.RecoveryMode = true
			worker.Core.store = nil
			if err = worker.RunOnce(ctx); err != nil {
				t.Fatal("recovery mode called the normal worker store", err)
			}
		})
	}
}

func TestProjectDeletionRecoveryGuardFailsClosedWhenStoreUnavailable(t *testing.T) {
	c, _, _, _, runtime, worker := projectDeletionFixture(t)
	store := c.store.(*storage.SQLStore)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := worker.execute(context.Background(), "retained-delete", nil); err == nil || errors.Is(err, storage.ErrNotFound) || runtime.removed != 0 {
		t.Fatal("unknown recovery protection allowed deletion", err)
	}
}
