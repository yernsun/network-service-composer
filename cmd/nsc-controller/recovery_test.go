package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/recovery"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestRecoveryCommandsSupportHTTPOnlyAndRequireExplicitReviewedActivation(t *testing.T) {
	cfg, path := commandConfigFixture(t)
	cfg.Storage.AutoMigrate = false
	cfg.Controller.RecoveryMode = true
	writeCommandConfig(t, cfg, path)
	store, err := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	output, err := commandOutput(t, "recovery-check", "--config", path)
	if err != nil || !strings.Contains(output, `"key_verification":"no_encrypted_material"`) || !strings.Contains(output, `"gateway_evidence":"not_supplied"`) {
		t.Fatal("HTTP-only restore rejected or missing evidence fabricated", output, err)
	}
	ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
	if err != nil {
		t.Fatal(err)
	}
	c := core.New(store, cfg, ring)
	if _, err = c.SaveDNSProfile(context.Background(), "private-profile", core.DNSProfileInput{Reference: "private-profile", Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: "private-restoration-token"}}, 0); err != nil {
		t.Fatal(err)
	}
	output, err = commandOutput(t, "recovery-check", "--config", path)
	if err != nil || !strings.Contains(output, `"key_verification":"verified"`) || strings.Contains(output, "private-restoration-token") {
		t.Fatal("profile key verification failed or leaked", output, err)
	}
	planFile := filepath.Join(t.TempDir(), "review.json")
	if output, err = commandOutput(t, "recovery-prepare", "--config", path, "--output", planFile); err != nil || !strings.Contains(output, `"status":"prepared"`) {
		t.Fatal(output, err)
	}
	info, err := os.Stat(planFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private recovery plan permissions", err)
	}
	if _, err = commandOutput(t, "recovery-prepare", "--config", path, "--output", planFile); err == nil {
		t.Fatal("existing recovery plan overwritten")
	}
	if _, err = commandOutput(t, "recovery-release", "--config", path, "--review", planFile); err == nil {
		t.Fatal("default held plan activated")
	}
	raw, err := os.ReadFile(planFile)
	if err != nil {
		t.Fatal(err)
	}
	var plan recovery.Plan
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	plan.ActivationReviewed = true
	raw, err = json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(planFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(planFile, append(raw, []byte(` {"activation_reviewed":true}`)...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = commandOutput(t, "recovery-release", "--config", path, "--review", planFile); err == nil {
		t.Fatal("ambiguous multi-document review activated recovery")
	}
	if err = os.WriteFile(planFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err = commandOutput(t, "recovery-release", "--config", path, "--review", planFile); err != nil || !strings.Contains(output, `"status":"reviewed"`) {
		t.Fatal("explicit review failed", output, err)
	}
	if held, err := store.RecoveryPrepared(context.Background()); err != nil || held {
		t.Fatal("review did not permit explicit normal restart", err)
	}
	cfg.Security.EncryptionKeys = map[string]string{"new": cfg.Security.EncryptionKeys["old"]}
	writeCommandConfig(t, cfg, path)
	if _, err = commandOutput(t, "recovery-check", "--config", path); err == nil {
		t.Fatal("wrong key recovered a non-certificate secret")
	}
}

func TestRecoveryCommandsRefuseLiveSQLiteOwnerWithoutChangingRestore(t *testing.T) {
	cfg, path := commandConfigFixture(t)
	cfg.Controller.RecoveryMode, cfg.Storage.AutoMigrate = true, false
	writeCommandConfig(t, cfg, path)
	store, err := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	plan, err := recovery.Prepare(context.Background(), store)
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
	held, err := lease.AcquireSQLiteProcessLock(cfg.Storage.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	output := filepath.Join(t.TempDir(), "blocked-review.json")
	for _, args := range [][]string{{"recovery-prepare", "--config", path, "--output", output}, {"recovery-release", "--config", path, "--review", review}} {
		if _, err = commandOutput(t, args...); err == nil || !strings.Contains(err.Error(), "already owned") {
			t.Fatal("live SQLite owner did not block recovery mutation", err)
		}
	}
	if _, err = os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("refused prepare created a review file", err)
	}
	after, err := storage.RecoveryFingerprint(context.Background(), store.DB())
	if err != nil || after != plan.Fingerprint {
		t.Fatal("refused command changed recovery state", err)
	}
	if prepared, err := store.RecoveryPrepared(context.Background()); err != nil || !prepared {
		t.Fatal("refused release removed barrier", err)
	}
}

func TestRecoveryCommandsRejectInvalidInputsWithoutStartingServices(t *testing.T) {
	cfg, path := commandConfigFixture(t)
	writeCommandConfig(t, cfg, path)
	badJSON := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(badJSON, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"recovery-check", "--bad"},
		{"recovery-check", "--config", filepath.Join(t.TempDir(), "missing")},
		{"recovery-check", "--config", path, "--evidence", badJSON},
		{"recovery-check", "--config", path, "--evidence", filepath.Join(t.TempDir(), "missing")},
		{"recovery-prepare", "--bad"},
		{"recovery-prepare", "--config", path},
		{"recovery-prepare", "--config", path, "--output", filepath.Join(t.TempDir(), "plan")},
		{"recovery-release", "--bad"},
		{"recovery-reconcile", "--bad"},
		{"recovery-reconcile", "--config", path},
		{"recovery-reconcile", "--config", path, "--operation", "held-operation", "--output", "new-output"},
		{"recovery-reconcile", "--config", path, "--operation", "held-operation", "--expected-engine", "original-engine"},
		{"recovery-release", "--config", path},
		{"recovery-release", "--config", path, "--review", badJSON},
		{"recovery-release", "--config", path, "--review", filepath.Join(t.TempDir(), "missing")},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := commandOutput(t, args...); err == nil {
				t.Fatal("invalid recovery command accepted")
			}
		})
	}
}
