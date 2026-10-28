package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/gateway"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestRecoveryCheckBindsStoredGatewayEvidenceAndRejectsMissingOrNewerState(t *testing.T) {
	cfg, path := commandConfigFixture(t)
	cfg.Storage.AutoMigrate = false
	writeCommandConfig(t, cfg, path)
	store, err := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, now := context.Background(), time.Now().UTC()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
	if err != nil {
		t.Fatal(err)
	}
	c := core.New(store, cfg, ring)
	pool, err := c.CreatePool(ctx, core.CreatePoolInput{Name: "gateway evidence fixture", BackendNetwork: "fixture", ChallengeUpstream: "http://controller:8080"})
	if err != nil {
		t.Fatal(err)
	}
	domain := model.Domain{ID: id.New(id.Domain), PoolID: pool.ID, Host: "evidence.example.test", TLSMode: model.TLSDisabled, Enabled: true, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err = store.CreateDomain(ctx, &domain); err != nil {
		t.Fatal(err)
	}
	pool, err = store.GetPool(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := c.BuildGatewaySnapshot(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := gateway.EncodeSnapshot(snapshot, gateway.DefaultChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	stored := model.Gateway{ID: id.New(id.Gateway), PoolID: pool.ID, Name: "stored evidence only", Enabled: true, AppliedRevision: pool.DesiredRevision, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err = store.UpsertGateway(ctx, &stored); err != nil {
		t.Fatal(err)
	}
	evidence := RecoveryEvidence{GatewayID: stored.ID, PoolID: pool.ID, AppliedRevision: pool.DesiredRevision, ModelDigest: encoded.Header.ModelDigest}
	evidenceFile := filepath.Join(t.TempDir(), "evidence.json")
	check := func(value RecoveryEvidence) (string, error) {
		raw, err := json.Marshal([]RecoveryEvidence{value})
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(evidenceFile, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return commandOutput(t, "recovery-check", "--config", path, "--evidence", evidenceFile)
	}
	before, err := storage.RecoveryFingerprint(ctx, store.DB())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := check(evidence); err != nil || !strings.Contains(out, `"gateway_evidence":"verified"`) {
		t.Fatal("matching stored evidence rejected", out, err)
	}
	for _, scenario := range []string{"missing gateway", "missing pool", "zero revision", "missing digest", "newer live revision", "same revision different digest", "unknown gateway", "unknown pool"} {
		bad := evidence
		switch scenario {
		case "missing gateway":
			bad.GatewayID = ""
		case "missing pool":
			bad.PoolID = ""
		case "zero revision":
			bad.AppliedRevision = 0
		case "missing digest":
			bad.ModelDigest = ""
		case "newer live revision":
			bad.AppliedRevision++
		case "same revision different digest":
			bad.ModelDigest = strings.Repeat("0", 64)
		case "unknown gateway":
			bad.GatewayID = "missing-gateway"
		case "unknown pool":
			bad.PoolID = "missing-pool"
		}
		if _, err = check(bad); err == nil {
			t.Fatal("unverified recovery evidence accepted", scenario)
		}
	}
	after, err := storage.RecoveryFingerprint(ctx, store.DB())
	if err != nil || after != before {
		t.Fatal("Gateway evidence checks changed restored state", err)
	}
}
