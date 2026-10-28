package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/recovery"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func recoveryPrepare(args []string) error {
	set := flag.NewFlagSet("recovery-prepare", flag.ContinueOnError)
	path := set.String("config", "/etc/nsc/config.yaml", "isolated recovery configuration")
	output := set.String("output", "", "new private review plan file")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *output == "" {
		return errors.New("a new private output file is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store, cfg, _, err := openRecovery(ctx, *path)
	if err != nil {
		return err
	}
	defer store.Close()
	unlock, err := recoveryOfflineLock(ctx, store, cfg)
	if err != nil {
		return err
	}
	defer unlock()
	// Reserve the path before changing the database. Never overwrite an older
	// review or follow an existing symlink; the plan contains private identities.
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	plan, err := recovery.Prepare(ctx, store)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(file).Encode(plan); err != nil {
		return fmt.Errorf("recovery barrier is retained; review file could not be written: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "prepared", "held_items": len(plan.Items), "barrier_revision": plan.BarrierRevision, "fingerprint": plan.Fingerprint})
}

func recoveryRelease(args []string) error {
	set := flag.NewFlagSet("recovery-release", flag.ContinueOnError)
	path := set.String("config", "/etc/nsc/config.yaml", "isolated recovery configuration")
	input := set.String("review", "", "operator-reviewed preparation plan")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *input == "" {
		return errors.New("an operator-reviewed recovery plan is required")
	}
	raw, err := os.ReadFile(*input)
	if err != nil {
		return err
	}
	var review recovery.Plan
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&review); err != nil {
		return errors.New("invalid recovery review JSON")
	}
	if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("recovery review must contain exactly one JSON document")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store, cfg, ring, err := openRecovery(ctx, *path)
	if err != nil {
		return err
	}
	defer store.Close()
	unlock, err := recoveryOfflineLock(ctx, store, cfg)
	if err != nil {
		return err
	}
	defer unlock()
	reconciler := recovery.LiveReconciler{Store: store, Keys: ring, Config: cfg}
	if err = recovery.Release(ctx, store, review, reconciler.Validate); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "reviewed", "activation": "explicit normal-mode restart required"})
}

func recoveryOfflineLock(ctx context.Context, store *storage.SQLStore, cfg config.Config) (func(), error) {
	if store.Dialect() == storage.SQLite {
		held, err := lease.AcquireSQLiteProcessLock(cfg.Storage.DSN)
		if err != nil {
			return nil, err
		}
		return func() { _ = held.Close() }, nil
	}
	held, err := controlplane.TryOfflineLock(ctx, lease.New(store.DB(), store.Dialect()))
	if err != nil {
		return nil, fmt.Errorf("recovery command requires the active Controller stopped: %w", err)
	}
	return func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = held.Release(closeCtx)
	}, nil
}

func openRecovery(ctx context.Context, path string) (*storage.SQLStore, config.Config, *security.Keyring, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, cfg, nil, err
	}
	if !cfg.Controller.RecoveryMode || cfg.Storage.AutoMigrate {
		return nil, cfg, nil, errors.New("recovery preparation/release require controller.recovery_mode=true and storage.auto_migrate=false; stop all Controllers first")
	}
	store, err := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		return nil, cfg, nil, err
	}
	if err = store.ValidateRecoverySchema(ctx); err != nil {
		store.Close()
		return nil, cfg, nil, err
	}
	ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
	if err == nil {
		_, err = recovery.CheckSecrets(ctx, store, ring)
	}
	if err != nil {
		store.Close()
		return nil, cfg, nil, err
	}
	return store, cfg, ring, nil
}
