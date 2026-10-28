package recovery

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestRecoveryDecryptsArchivedControlCategoriesAndPreservesOrdinaryJSON(t *testing.T) {
	store, ring, rt := recoveryFixture(t)
	ctx, now := context.Background(), time.Now().UTC()
	cfg := config.Defaults()
	cfg.Management.Enabled = true
	owner, err := core.New(store, cfg, ring).CreateProjectWithEnvironment(ctx, core.ProjectInput{Name: "recovery history owner", Slug: "recovery-history", DefaultRuntimeID: rt.ID}, "recovery-owner")
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{"variables", "variable-version", "environment-compose", "environment-review", "release-snapshot", "application-restore", "application-source-draft", "application-draft-receipt", "node-report", "dns-provider-profile"}
	for _, kind := range kinds {
		key := "restored-" + kind
		ciphertext, err := ring.Encrypt([]byte(`{"archive":"private fixture"}`), []byte("control:"+key))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(map[string][]byte{"ciphertext": ciphertext})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: key, Kind: kind, ParentID: owner.ID, Payload: raw, CreatedAt: now, UpdatedAt: now}}}); err != nil {
			t.Fatal(err)
		}
	}
	for i, raw := range []string{`[]`, `"ordinary scalar"`, `{"ciphertext":3}`} {
		if err := store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: "ordinary-" + strings.Repeat("x", i+1), Kind: "ordinary", Payload: json.RawMessage(raw), CreatedAt: now, UpdatedAt: now}}}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := storage.RecoveryFingerprint(ctx, store.DB())
	if err != nil {
		t.Fatal(err)
	}
	counts, err := CheckSecrets(ctx, store, ring)
	if err != nil || counts["control_document"] != len(kinds) {
		t.Fatal("archived encrypted category omitted or ordinary JSON misclassified", counts, err)
	}
	after, err := storage.RecoveryFingerprint(ctx, store.DB())
	if err != nil || after != before {
		t.Fatal("secret check rewrote restored archives", err)
	}
}

func TestRecoverySecretInspectionRefusesMissingRequiredMaterialAndIncompleteRestores(t *testing.T) {
	for _, scenario := range []string{"missing revision compose", "missing revision environment", "missing revision resolved", "missing account table", "missing control table"} {
		t.Run(scenario, func(t *testing.T) {
			store, plan, reader, _ := runningApplicationFixture(t)
			ctx := context.Background()
			if strings.HasPrefix(scenario, "missing revision ") {
				op, err := store.GetDeploymentOperation(ctx, plan.Items[0].ID)
				if err != nil {
					t.Fatal(err)
				}
				column := "encrypted_" + strings.TrimPrefix(scenario, "missing revision ")
				if _, err = store.DB().ExecContext(ctx, "UPDATE application_revisions SET "+column+"=? WHERE id=?", []byte{}, op.RevisionID); err != nil {
					t.Fatal(err)
				}
			} else {
				table := "acme_accounts"
				if scenario == "missing control table" {
					table = "control_documents"
				}
				if _, err := store.DB().ExecContext(ctx, "DROP TABLE "+table); err != nil {
					t.Fatal(err)
				}
			}
			if counts, err := CheckSecrets(ctx, store, reader.Keys); err == nil || counts != nil {
				t.Fatal("incomplete restored material was reported as verified", counts, err)
			}
			// A failed read cannot replace missing data with empty ciphertext or
			// claim an interrupted Application succeeded.
			op, err := store.GetDeploymentOperation(ctx, plan.Items[0].ID)
			if err != nil || string(op.Status) != "RUNNING" || op.Revision != plan.Items[0].Revision {
				t.Fatal("secret refusal changed interrupted history", err)
			}
		})
	}
}

func TestRecoveryRejectsMalformedKnownAndUnknownControlDocumentsWithoutRewritingThem(t *testing.T) {
	for _, candidate := range []struct{ kind, raw string }{{"variables", `[]`}, {"variables", `{"ciphertext":3}`}, {"ordinary", `{`}} {
		t.Run(candidate.kind+candidate.raw, func(t *testing.T) {
			store, ring, rt := recoveryFixture(t)
			ctx, now := context.Background(), time.Now().UTC()
			cfg := config.Defaults()
			cfg.Management.Enabled = true
			owner, err := core.New(store, cfg, ring).CreateProjectWithEnvironment(ctx, core.ProjectInput{Name: "recovery history owner", Slug: "recovery-history", DefaultRuntimeID: rt.ID}, "recovery-owner")
			if err != nil {
				t.Fatal(err)
			}
			doc := storage.ControlDocument{ID: "unreadable-restored-document", Kind: candidate.kind, ParentID: owner.ID, Payload: json.RawMessage(candidate.raw), CreatedAt: now, UpdatedAt: now}
			if err := store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: doc}}); err != nil {
				t.Fatal(err)
			}
			if _, err := CheckSecrets(ctx, store, ring); err == nil {
				t.Fatal("malformed restored material verified")
			}
			after, err := store.GetControlDocument(ctx, doc.ID)
			if err != nil || string(after.Payload) != candidate.raw || after.Revision != 1 {
				t.Fatal("failed check replaced unreadable evidence", err)
			}
		})
	}
}

func TestRecoveryDecryptsNotificationReceiverWithoutExposingIt(t *testing.T) {
	store, ring, _ := recoveryFixture(t)
	cfg := config.Defaults()
	owner := core.New(store, cfg, ring)
	endpoint, secret := "https://receiver.example.test/hook?token=private-recovery-token", "private-notification-signing-secret"
	_, err := owner.SaveNotificationSettings(context.Background(), core.NotificationSettingsInput{Enabled: true, Events: []string{"images"}, Endpoint: &endpoint, Secret: &secret}, 0)
	if err != nil {
		t.Fatal(err)
	}
	counts, err := CheckSecrets(context.Background(), store, ring)
	if err != nil || counts["control_document"] != 1 {
		t.Fatal("notification receiver excluded from recovery", counts, err)
	}
}
