package recovery

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestRecoveryPreparationRetainsExistingUnknownHoldWithoutRewritingIt(t *testing.T) {
	store, ring, rt := recoveryFixture(t)
	ctx, now := context.Background(), time.Now().UTC()
	op := recoveryOperation(t, store, ring, rt, now)
	original := storage.ControlDocument{ID: storage.RecoveryHoldID("deployment", op.ID), Kind: "recovery-hold", Payload: json.RawMessage(`{"state":"UNKNOWN","original_evidence":"retained"}`), CreatedAt: now, UpdatedAt: now}
	if err := store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: original}}); err != nil {
		t.Fatal(err)
	}
	plan, err := Prepare(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	plan.ActivationReviewed = true
	if err = Release(ctx, store, plan); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetControlDocument(ctx, original.ID)
	if err != nil || after.Revision != 1 || string(after.Payload) != string(original.Payload) {
		t.Fatal("default hold review rewrote previous unknown evidence", err)
	}
}

func TestRecoveryRejectsMalformedOrIncompatiblePersistedBarrierWithoutRebaselining(t *testing.T) {
	for _, scenario := range []string{"malformed JSON", "unknown format", "changed fingerprint"} {
		t.Run(scenario, func(t *testing.T) {
			store, _, _ := recoveryFixture(t)
			ctx := context.Background()
			plan, err := Prepare(ctx, store)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := store.GetControlDocument(ctx, storage.RecoveryBarrierID)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "malformed JSON":
				doc.Payload = json.RawMessage(`{`)
			case "unknown format":
				plan.Format++
				doc.Payload, err = json.Marshal(plan)
			case "changed fingerprint":
				plan.Fingerprint = "unknown-backup"
				doc.Payload, err = json.Marshal(plan)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: doc, Expected: doc.Revision}}); err != nil {
				t.Fatal(err)
			}
			if _, err = Prepare(ctx, store); err == nil {
				t.Fatal("corrupt existing barrier silently rebaselined")
			}
			if scenario == "malformed JSON" {
				plan.ActivationReviewed = true
				if err = Release(ctx, store, plan); err == nil {
					t.Fatal("corrupt barrier released")
				}
			}
			after, err := store.GetControlDocument(ctx, storage.RecoveryBarrierID)
			if err != nil || string(after.Payload) != string(doc.Payload) {
				t.Fatal("refusal overwrote barrier evidence", err)
			}
		})
	}
}

func TestRecoveryReviewMissingHoldRollsBackEarlierApprovals(t *testing.T) {
	store, ring, rt := recoveryFixture(t)
	ctx, now := context.Background(), time.Now().UTC()
	recoveryOperation(t, store, ring, rt, now)
	recoveryOperation(t, store, ring, rt, now.Add(time.Second))
	plan, err := Prepare(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	last, err := store.GetControlDocument(ctx, storage.RecoveryHoldID(plan.Items[1].Kind, plan.Items[1].ID))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: last, Expected: last.Revision, Remove: true}}); err != nil {
		t.Fatal(err)
	}
	plan.ActivationReviewed = true
	for i := range plan.Items {
		plan.Items[i].Decision = "resume"
	}
	if err = Release(ctx, store, plan); err == nil {
		t.Fatal("missing hold permitted partial approval")
	}
	if _, err = store.GetControlDocument(ctx, storage.RecoveryHoldID(plan.Items[0].Kind, plan.Items[0].ID)); err != nil {
		t.Fatal("failed review partially unlocked an earlier item", err)
	}
	if prepared, err := store.RecoveryPrepared(ctx); err != nil || !prepared {
		t.Fatal("failed review removed barrier", err)
	}
	if reviews, err := store.ListControlDocuments(ctx, "recovery-review", "", storage.Page{Limit: 200}); err != nil || len(reviews) != 0 {
		t.Fatal("failed review archived a misleading success", err)
	}
}

func TestRecoveryDeletionInventoryRefusesMalformedTasksAndOnlyDefersInterruptedWork(t *testing.T) {
	for _, state := range []string{"malformed", "QUEUED", "COMPLETE"} {
		t.Run(state, func(t *testing.T) {
			store, _, _ := recoveryFixture(t)
			ctx, now := context.Background(), time.Now().UTC()
			payload, err := json.Marshal(core.ProjectDeletion{ProjectID: "restore-project", Status: state})
			if err != nil {
				t.Fatal(err)
			}
			if state == "malformed" {
				payload = []byte(`{"status":3}`)
			}
			doc := storage.ControlDocument{ID: "restored-deletion", Kind: "project-deletion", ParentID: "restore-project", Payload: payload, CreatedAt: now, UpdatedAt: now}
			if err = store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: doc}}); err != nil {
				t.Fatal(err)
			}
			plan, err := Prepare(ctx, store)
			if state == "malformed" {
				if err == nil {
					t.Fatal("unreadable deletion status verified")
				}
				if prepared, err := store.RecoveryPrepared(ctx); err != nil || prepared {
					t.Fatal("failed inventory persisted a partial barrier", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			plan.ActivationReviewed = true
			if state == "COMPLETE" {
				if len(plan.Items) != 0 {
					t.Fatal("terminal history became executable work")
				}
				if err = Release(ctx, store, plan); err != nil {
					t.Fatal(err)
				}
				return
			}
			plan.Items[0].Decision = "defer"
			if err = Release(ctx, store, plan); err == nil {
				t.Fatal("queued deletion was misclassified as interrupted")
			}
			plan.Items[0].Decision = "resume"
			if err = Release(ctx, store, plan); err != nil {
				t.Fatal("reviewed queued deletion was not releasable", err)
			}
		})
	}
}

func TestRecoveryRunningReleaseRequiresExactlyOneLiveValidator(t *testing.T) {
	store, plan, _, _ := runningApplicationFixture(t)
	ctx := context.Background()
	plan.ActivationReviewed = true
	plan.Items[0].Decision = "reconcile"
	if err := Release(ctx, store, plan); err == nil {
		t.Fatal("reconcile decision replaced live validation")
	}
	if err := Release(ctx, store, plan, func(context.Context, Item, *Reconciliation) error { return nil }, func(context.Context, Item, *Reconciliation) error { return nil }); err == nil {
		t.Fatal("ambiguous validators permitted release")
	}
	if held, err := storage.RecoveryHeld(ctx, store, "deployment", plan.Items[0].ID); err != nil || !held {
		t.Fatal("unvalidated running work lost hold", err)
	}
}

func TestRecoveryReviewWriteFailureRollsBackProtectionAndArchives(t *testing.T) {
	for _, phase := range []string{"hold deletion", "review archive", "barrier deletion"} {
		t.Run(phase, func(t *testing.T) {
			store, ring, rt := recoveryFixture(t)
			ctx := context.Background()
			op := recoveryOperation(t, store, ring, rt, time.Now().UTC())
			plan, err := Prepare(ctx, store)
			if err != nil {
				t.Fatal(err)
			}
			before, err := store.ListControlDocuments(ctx, "", "", storage.Page{Limit: 200})
			if err != nil {
				t.Fatal(err)
			}
			trigger := "CREATE TRIGGER refuse_review BEFORE DELETE ON control_documents WHEN OLD.kind='recovery-hold' BEGIN SELECT RAISE(ABORT,'fixture write refusal'); END"
			if phase == "review archive" {
				trigger = "CREATE TRIGGER refuse_review BEFORE INSERT ON control_documents WHEN NEW.kind='recovery-review' BEGIN SELECT RAISE(ABORT,'fixture write refusal'); END"
			} else if phase == "barrier deletion" {
				trigger = "CREATE TRIGGER refuse_review BEFORE DELETE ON control_documents WHEN OLD.kind='recovery-barrier' BEGIN SELECT RAISE(ABORT,'fixture write refusal'); END"
			}
			if _, err = store.DB().ExecContext(ctx, trigger); err != nil {
				t.Fatal(err)
			}
			plan.ActivationReviewed, plan.Items[0].Decision = true, "resume"
			if err = Release(ctx, store, plan); err == nil {
				t.Fatal("database write refusal permitted partial activation")
			}
			after, err := store.ListControlDocuments(ctx, "", "", storage.Page{Limit: 200})
			if err != nil {
				t.Fatal(err)
			}
			expected, _ := json.Marshal(before)
			actual, _ := json.Marshal(after)
			if string(actual) != string(expected) {
				t.Fatal("failed transaction changed barrier, holds or review history")
			}
			if _, err = store.ClaimDeploymentOperation(ctx, "after-failed-review", time.Now(), time.Now().Add(-time.Minute)); err == nil {
				t.Fatal("failed transaction made restored work executable")
			}
			if _, err = store.DB().ExecContext(ctx, "DROP TRIGGER refuse_review"); err != nil {
				t.Fatal(err)
			}
			if err = Release(ctx, store, plan); err != nil {
				t.Fatal("intact review could not be retried after storage recovered", err)
			}
			if next, err := store.ClaimDeploymentOperation(ctx, "after-reviewed-retry", time.Now(), time.Now().Add(-time.Minute)); err != nil || next.ID != op.ID {
				t.Fatal("reviewed retry did not release the original item", err)
			}
		})
	}
}
