package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/recovery"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// Dedicated, explicitly configured recovery databases avoid sharing mutable
// fingerprints or scheduler locks with other packages' conformance fixtures.
// Default harness runs do not create or discover external database resources.
func TestRecoveryCommandsOnOwnedExternalStoresRequireOfflineReviewedActivation(t *testing.T) {
	for _, fixture := range []struct{ driver, variable string }{{"postgres", "NSC_TEST_RECOVERY_POSTGRES_DSN"}, {"mysql", "NSC_TEST_RECOVERY_MYSQL_DSN"}} {
		t.Run(fixture.driver, func(t *testing.T) {
			dsn := os.Getenv(fixture.variable)
			if dsn == "" {
				t.Skip("explicit isolated recovery database fixture required")
			}
			cfg, path := commandConfigFixture(t)
			cfg.Storage.Driver, cfg.Storage.DSN, cfg.Storage.HA, cfg.Storage.AutoMigrate = fixture.driver, dsn, true, false
			cfg.Controller.RecoveryMode = true
			writeCommandConfig(t, cfg, path)
			store, err := storage.Open(fixture.driver, dsn)
			if err != nil {
				t.Fatal("open isolated recovery database failed")
			}
			defer store.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err = store.Migrate(ctx); err != nil {
				t.Fatal("migrate isolated recovery fixture failed")
			}
			// Exercise the actual dialect's claim filter as well as its advisory
			// lock. A protected interrupted result and an unapproved queue entry
			// must survive releasing a different, explicitly reviewed item.
			now := time.Now().UTC().Add(-time.Hour)
			pool := model.GatewayPool{ID: id.New(id.Pool), Name: "isolated recovery claim", Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			pool.Name += " " + pool.ID
			if err = store.CreatePool(ctx, &pool); err != nil {
				t.Fatal(err)
			}
			runtime := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "isolated recovery runtime", Type: model.RuntimeDocker, Endpoint: "unix:///fixture-unavailable/docker.sock", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
			runtime.Name += " " + runtime.ID
			if err = store.CreateRuntime(ctx, &runtime); err != nil {
				t.Fatal(err)
			}
			newOperation := func(created time.Time) model.DeploymentOperation {
				value := model.DeploymentOperation{ID: id.New(id.Operation), RuntimeID: runtime.ID, Type: "VOLUME_CREATE", Status: model.OperationQueued, Revision: 1, CreatedAt: created, UpdatedAt: created}
				result, err := store.CreateDeploymentOperation(ctx, &value)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			interrupted := newOperation(now)
			claimed, err := store.ClaimDeploymentOperation(ctx, "restored-native-owner", now, now.Add(-time.Minute))
			if err != nil || claimed.ID != interrupted.ID {
				t.Fatal("native interrupted fixture claim", err)
			}
			approved, unapproved := newOperation(now.Add(time.Second)), newOperation(now.Add(2*time.Second))
			baseline, err := storage.RecoveryFingerprint(ctx, store.DB())
			if err != nil {
				t.Fatal(err)
			}
			locker := lease.New(store.DB(), store.Dialect())
			owner, err := controlplane.TryOfflineLock(ctx, locker)
			if err != nil {
				t.Fatal(err)
			}
			blocked := filepath.Join(t.TempDir(), "blocked-plan.json")
			if _, err = commandOutput(t, "recovery-prepare", "--config", path, "--output", blocked); !errors.Is(err, lease.ErrBusy) {
				t.Fatal("online scheduler did not block maintenance", err)
			}
			if _, err = os.Stat(blocked); !os.IsNotExist(err) {
				t.Fatal("refused maintenance created output", err)
			}
			current, err := storage.RecoveryFingerprint(ctx, store.DB())
			if err != nil || current != baseline {
				t.Fatal("refused maintenance changed restored database", err)
			}
			if err = owner.Release(ctx); err != nil {
				t.Fatal(err)
			}
			planFile := filepath.Join(t.TempDir(), "prepared.json")
			if _, err = commandOutput(t, "recovery-prepare", "--config", path, "--output", planFile); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(planFile)
			if err != nil {
				t.Fatal(err)
			}
			var plan recovery.Plan
			if err = json.Unmarshal(raw, &plan); err != nil {
				t.Fatal(err)
			}
			if _, err = store.ClaimDeploymentOperation(ctx, "recovered-native-owner", time.Now(), time.Now().Add(-time.Minute)); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("native claim bypassed preparation barrier", err)
			}
			plan.ActivationReviewed = true
			for i := range plan.Items {
				if plan.Items[i].ID == approved.ID {
					plan.Items[i].Decision = "resume"
				}
			}
			raw, err = json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(planFile, raw, 0600); err != nil {
				t.Fatal(err)
			}
			owner, err = controlplane.TryOfflineLock(ctx, locker)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = commandOutput(t, "recovery-release", "--config", path, "--review", planFile); !errors.Is(err, lease.ErrBusy) {
				t.Fatal("active owner allowed barrier release", err)
			}
			if prepared, err := store.RecoveryPrepared(ctx); err != nil || !prepared {
				t.Fatal("refused release lost protection", err)
			}
			if err = owner.Release(ctx); err != nil {
				t.Fatal(err)
			}
			if out, err := commandOutput(t, "recovery-release", "--config", path, "--review", planFile); err != nil || !strings.Contains(out, `"status":"reviewed"`) {
				t.Fatal("offline reviewed activation failed", out, err)
			}
			if prepared, err := store.RecoveryPrepared(ctx); err != nil || prepared {
				t.Fatal("explicit reviewed activation retained global barrier", err)
			}
			next, err := store.ClaimDeploymentOperation(ctx, "recovered-native-owner", time.Now(), time.Now().Add(-time.Minute))
			if err != nil || next.ID != approved.ID {
				t.Fatal("native claim did not select only reviewed queue entry", err)
			}
			for _, original := range []model.DeploymentOperation{claimed, unapproved} {
				value, err := store.GetDeploymentOperation(ctx, original.ID)
				if err != nil || value.Status != original.Status || value.Revision != original.Revision || value.Attempt != original.Attempt {
					t.Fatal("native selective release rewrote retained outcome", err)
				}
				if held, err := storage.RecoveryHeld(ctx, store, "deployment", original.ID); err != nil || !held {
					t.Fatal("native selective release cleared an unapproved hold", err)
				}
			}
		})
	}
}
