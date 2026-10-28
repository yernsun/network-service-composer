package recovery

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestRecoveryDeletionReleasePreservesMalformedOrChangedInterruptedHistory(t *testing.T) {
	for _, scenario := range []string{"malformed attempts", "changed revision", "changed raw input", "no attempts"} {
		t.Run(scenario, func(t *testing.T) {
			store, _, _ := recoveryFixture(t)
			ctx, now := context.Background(), time.Now().UTC()
			task := core.ProjectDeletion{ProjectID: "restored-project", Status: "RUNNING", Preview: core.ProjectDeletionPreview{Applications: []core.DeletionApplication{{ID: "restored-app", CompletedObjects: []string{"already-removed"}}}}}
			raw, err := json.Marshal(task)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "malformed attempts" {
				raw = []byte(`{"project_id":"restored-project","status":"RUNNING","attempts":"unreadable"}`)
			}
			doc := storage.ControlDocument{ID: "interrupted-deletion", Kind: "project-deletion", ParentID: task.ProjectID, Payload: raw, CreatedAt: now, UpdatedAt: now}
			if err = store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: doc}}); err != nil {
				t.Fatal(err)
			}
			plan, err := Prepare(ctx, store)
			if err != nil {
				t.Fatal(err)
			}
			doc, err = store.GetControlDocument(ctx, doc.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "changed revision" {
				if err = store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: doc, Expected: doc.Revision}}); err != nil {
					t.Fatal(err)
				}
			} else if scenario == "changed raw input" {
				if _, err = store.DB().ExecContext(ctx, "UPDATE control_documents SET payload=? WHERE id=?", []byte(`{"status":"RUNNING","phase":"changed outside review"}`), doc.ID); err != nil {
					t.Fatal(err)
				}
			}
			baseline, err := storage.RecoveryFingerprint(ctx, store.DB())
			if err != nil {
				t.Fatal(err)
			}
			plan.ActivationReviewed, plan.Items[0].Decision = true, "defer"
			err = Release(ctx, store, plan)
			if scenario == "no attempts" {
				if err != nil {
					t.Fatal("interrupted history without an attempt could not be deferred", err)
				}
				archived, err := store.GetControlDocument(ctx, "recovery:before:"+doc.ID+":1")
				if err != nil || string(archived.Payload) != string(raw) {
					t.Fatal("deferral did not retain original no-attempt history", err)
				}
				updated, err := store.GetControlDocument(ctx, doc.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal(updated.Payload, &task); err != nil || task.Status != "UNKNOWN" || len(task.Attempts) != 0 || len(task.Preview.Applications[0].CompletedObjects) != 1 {
					t.Fatal("deferral invented an attempt or lost completed history", err)
				}
				return
			}
			if err == nil {
				t.Fatal("unreviewed or malformed deletion evidence was deferred")
			}
			current, err := storage.RecoveryFingerprint(ctx, store.DB())
			if err != nil || current != baseline {
				t.Fatal("refusal changed interrupted history", err)
			}
			if held, err := storage.RecoveryHeld(ctx, store, "project-deletion", doc.ID); err != nil || !held {
				t.Fatal("refusal removed interrupted deletion protection", err)
			}
		})
	}
}

func TestRecoveryReleaseRefusesUnknownFormatsAndMisappliedReconciliationDecisions(t *testing.T) {
	for _, scenario := range []string{"review format", "persisted format", "certificate reconcile", "certificate defer", "queued reconcile"} {
		t.Run(scenario, func(t *testing.T) {
			store, ring, rt := recoveryFixture(t)
			ctx, now := context.Background(), time.Now().UTC()
			if scenario == "certificate reconcile" || scenario == "certificate defer" {
				certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: rt.PoolID, Provider: "acme", Status: model.CertificatePending, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
				if err := store.CreateCertificate(ctx, &certificate); err != nil {
					t.Fatal(err)
				}
			} else {
				recoveryOperation(t, store, ring, rt, now)
			}
			plan, err := Prepare(ctx, store)
			if err != nil {
				t.Fatal(err)
			}
			plan.ActivationReviewed = true
			switch scenario {
			case "review format":
				plan.Format++
			case "persisted format":
				doc, err := store.GetControlDocument(ctx, storage.RecoveryBarrierID)
				if err != nil {
					t.Fatal(err)
				}
				persisted := plan
				persisted.Format++
				doc.Payload, err = json.Marshal(persisted)
				if err != nil {
					t.Fatal(err)
				}
				if err = store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: doc, Expected: doc.Revision}}); err != nil {
					t.Fatal(err)
				}
			case "certificate defer":
				plan.Items[0].Decision = "defer"
			default:
				plan.Items[0].Decision = "reconcile"
			}
			if err = Release(ctx, store, plan, func(context.Context, Item, *Reconciliation) error {
				t.Fatal("inapplicable validator was invoked")
				return nil
			}); err == nil {
				t.Fatal("unsupported review contract permitted release")
			}
			if held, err := storage.RecoveryHeld(ctx, store, plan.Items[0].Kind, plan.Items[0].ID); err != nil || !held {
				t.Fatal("unsupported decision removed protection", err)
			}
		})
	}
}
