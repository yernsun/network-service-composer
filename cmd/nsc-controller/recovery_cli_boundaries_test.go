package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/recovery"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func recoveryCLIStoreFixture(t *testing.T) (config.Config, string, *storage.SQLStore) {
	t.Helper()
	cfg, path := commandConfigFixture(t)
	cfg.Controller.RecoveryMode, cfg.Storage.AutoMigrate = true, false
	writeCommandConfig(t, cfg, path)
	store, err := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = store.ValidateRecoverySchema(context.Background()); err != nil {
		t.Fatal("fixture does not have the supported recovery schema", err)
	}
	return cfg, path, store
}

// The review fingerprint excludes recovery bookkeeping. Include every control
// document's private payload and metadata in the refusal invariant as well.
func recoveryCLIState(t *testing.T, store *storage.SQLStore) string {
	t.Helper()
	fingerprint, err := storage.RecoveryFingerprint(context.Background(), store.DB())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.DB().QueryContext(context.Background(), "SELECT id FROM control_documents ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	state := []any{fingerprint}
	for _, id := range ids {
		doc, err := store.GetControlDocument(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		state = append(state, []any{doc.ID, doc.Kind, doc.ParentID, doc.Payload, doc.Revision, doc.CreatedAt, doc.UpdatedAt})
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestRecoveryReconcileRequiresPreparationOnSupportedSchema(t *testing.T) {
	_, path, store := recoveryCLIStoreFixture(t)
	before := recoveryCLIState(t, store)
	output := filepath.Join(t.TempDir(), "unprepared.json")
	result, err := commandOutput(t, "recovery-reconcile", "--config", path, "--operation", "restored-interrupted-operation", "--expected-engine", "original-engine", "--output", output)
	if !errors.Is(err, storage.ErrNotFound) || result != "" {
		t.Fatal("unprepared restore reached reconciliation or emitted evidence", err)
	}
	if _, err = os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("unprepared reconciliation created output", err)
	}
	if recoveryCLIState(t, store) != before {
		t.Fatal("unprepared reconciliation changed the restored database")
	}
}

func TestRecoveryReconcileRejectsScalarBarrierWithoutChangingProtection(t *testing.T) {
	_, path, store := recoveryCLIStoreFixture(t)
	if _, err := recovery.Prepare(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	doc, err := store.GetControlDocument(context.Background(), storage.RecoveryBarrierID)
	if err != nil {
		t.Fatal(err)
	}
	doc.Payload = json.RawMessage(`true`)
	if err = store.WriteControlDocuments(context.Background(), []storage.ControlWrite{{Document: doc, Expected: doc.Revision}}); err != nil {
		t.Fatal(err)
	}
	before := recoveryCLIState(t, store)
	output := filepath.Join(t.TempDir(), "invalid-barrier.json")
	result, err := commandOutput(t, "recovery-reconcile", "--config", path, "--operation", "restored-interrupted-operation", "--expected-engine", "original-engine", "--output", output)
	if err == nil || !strings.Contains(err.Error(), "cannot unmarshal bool") || result != "" {
		t.Fatal("scalar barrier was accepted as an operator plan", err)
	}
	if _, err = os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("malformed barrier created reconciliation output", err)
	}
	if prepared, err := store.RecoveryPrepared(context.Background()); err != nil || !prepared || recoveryCLIState(t, store) != before {
		t.Fatal("invalid barrier lost protection or changed restored state", err)
	}
}

func TestRecoveryMutationCommandsRequireHistoricalEncryptionKeys(t *testing.T) {
	cfg, path, store := recoveryCLIStoreFixture(t)
	oldRing, err := security.NewKeyring("old", cfg.Security.EncryptionKeys)
	if err != nil {
		t.Fatal(err)
	}
	secret := "private-historical-restoration-token"
	application := core.New(store, cfg, oldRing)
	if _, err = application.SaveDNSProfile(context.Background(), "historical-provider", core.DNSProfileInput{Reference: "historical-provider", Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: secret}}, 0); err != nil {
		t.Fatal(err)
	}
	// The primary key is valid and current, but restored envelopes still need
	// the historical key. Verify that material with the complete keyring first.
	if result, err := commandOutput(t, "recovery-check", "--config", path); err != nil || !strings.Contains(result, `"key_verification":"verified"`) || strings.Contains(result, secret) {
		t.Fatal("historical encrypted fixture failed complete-key verification", err)
	}
	reviewPath := filepath.Join(t.TempDir(), "review.json")
	if _, err = commandOutput(t, "recovery-prepare", "--config", path, "--output", reviewPath); err != nil {
		t.Fatal(err)
	}
	reviewRaw, err := os.ReadFile(reviewPath)
	if err != nil {
		t.Fatal(err)
	}
	var plan recovery.Plan
	if err = json.Unmarshal(reviewRaw, &plan); err != nil {
		t.Fatal(err)
	}
	plan.ActivationReviewed = true
	reviewRaw, err = json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(reviewPath, reviewRaw, 0600); err != nil {
		t.Fatal(err)
	}
	delete(cfg.Security.EncryptionKeys, "old")
	writeCommandConfig(t, cfg, path)
	before := recoveryCLIState(t, store)
	for _, command := range []string{"recovery-prepare", "recovery-release", "recovery-reconcile"} {
		t.Run(command, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "must-not-exist.json")
			args := []string{command, "--config", path}
			switch command {
			case "recovery-prepare":
				args = append(args, "--output", output)
			case "recovery-release":
				args = append(args, "--review", reviewPath)
			case "recovery-reconcile":
				args = append(args, "--operation", "restored-interrupted-operation", "--expected-engine", "original-engine", "--output", output)
			}
			result, err := commandOutput(t, args...)
			if err == nil || !strings.Contains(err.Error(), `encryption key "old" is unavailable`) || result != "" || strings.Contains(err.Error(), secret) {
				t.Fatal("missing historical key did not prevent recovery mutation", err)
			}
			if _, err = os.Lstat(output); !os.IsNotExist(err) {
				t.Fatal("key refusal created a private output", err)
			}
			if recoveryCLIState(t, store) != before {
				t.Fatal("key refusal changed recovery protection or permanent state")
			}
			after, err := os.ReadFile(reviewPath)
			if err != nil || string(after) != string(reviewRaw) {
				t.Fatal("key refusal changed the operator review", err)
			}
		})
	}
}

func TestRecoveryPrepareRefusesOutputSymlinksBeforeCreatingBarrier(t *testing.T) {
	for _, existingTarget := range []bool{true, false} {
		name := "dangling target"
		if existingTarget {
			name = "existing private target"
		}
		t.Run(name, func(t *testing.T) {
			_, path, store := recoveryCLIStoreFixture(t)
			dir := t.TempDir()
			target, output := filepath.Join(dir, "private.json"), filepath.Join(dir, "review.json")
			original := []byte("private operator notes\n")
			if existingTarget {
				if err := os.WriteFile(target, original, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, output); err != nil {
				t.Fatal(err)
			}
			before := recoveryCLIState(t, store)
			result, err := commandOutput(t, "recovery-prepare", "--config", path, "--output", output)
			if !errors.Is(err, os.ErrExist) || result != "" {
				t.Fatal("prepare followed or replaced a symlink", err)
			}
			link, err := os.Readlink(output)
			if err != nil || link != target {
				t.Fatal("prepare replaced the output symlink", err)
			}
			if existingTarget {
				raw, err := os.ReadFile(target)
				if err != nil || string(raw) != string(original) {
					t.Fatal("prepare overwrote the private symlink target", err)
				}
			} else if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatal("prepare created a dangling symlink target", err)
			}
			if prepared, err := store.RecoveryPrepared(context.Background()); err != nil || prepared || recoveryCLIState(t, store) != before {
				t.Fatal("refused output prepared or mutated the restore", err)
			}
		})
	}
}
