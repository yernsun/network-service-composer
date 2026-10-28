package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yernsun/network-service-composer/internal/controller"
	"github.com/yernsun/network-service-composer/internal/recovery"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestRecoveryCommandsAndInspectionRejectIncompatibleSchemaWithoutChangingBarrier(t *testing.T) {
	for _, scenario := range []string{"older", "future", "missing journal"} {
		t.Run(scenario, func(t *testing.T) {
			cfg, path := commandConfigFixture(t)
			cfg.Controller.RecoveryMode, cfg.Storage.AutoMigrate = true, false
			writeCommandConfig(t, cfg, path)
			store, err := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			ctx := context.Background()
			if err = store.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			plan, err := recovery.Prepare(ctx, store)
			if err != nil {
				t.Fatal(err)
			}
			plan.ActivationReviewed = true
			raw, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			review := filepath.Join(t.TempDir(), "review.json")
			if err = os.WriteFile(review, raw, 0600); err != nil {
				t.Fatal(err)
			}
			var latest int64
			if err = store.DB().QueryRowContext(ctx, "SELECT MAX(version_id) FROM goose_db_version").Scan(&latest); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "older":
				_, err = store.DB().ExecContext(ctx, "INSERT INTO goose_db_version(version_id,is_applied) VALUES (?,false)", latest)
			case "future":
				_, err = store.DB().ExecContext(ctx, "INSERT INTO goose_db_version(version_id,is_applied) VALUES (?,true)", latest+100)
			case "missing journal":
				_, err = store.DB().ExecContext(ctx, "DROP TABLE goose_db_version")
			}
			if err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(t.TempDir(), "must-not-exist.json")
			for _, args := range [][]string{{"recovery-check", "--config", path}, {"recovery-prepare", "--config", path, "--output", output}, {"recovery-release", "--config", path, "--review", review}, {"recovery-reconcile", "--config", path, "--operation", "held-operation", "--expected-engine", "original-engine", "--output", output}} {
				if _, err = commandOutput(t, args...); err == nil {
					t.Fatal("unsupported schema reached recovery command", args[0])
				}
			}
			if _, err = os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("schema refusal created output", err)
			}
			if inspection, err := controller.New(ctx, cfg, nil); err == nil || inspection != nil {
				t.Fatal("incompatible protected inspection started", err)
			}
			if prepared, err := store.RecoveryPrepared(ctx); err != nil || !prepared {
				t.Fatal("schema refusal removed protection", err)
			}
			after, err := storage.RecoveryFingerprint(ctx, store.DB())
			if err != nil || after != plan.Fingerprint {
				t.Fatal("schema refusal mutated permanent restored state", err)
			}
		})
	}
}
