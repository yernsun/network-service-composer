package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/gateway"
	"github.com/yernsun/network-service-composer/internal/recovery"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// RecoveryEvidence is captured from live Gateway reads immediately before the
// backup. It is data, never a source of configuration or write authority.
type RecoveryEvidence struct {
	GatewayID       string `json:"gateway_id"`
	PoolID          string `json:"pool_id"`
	AppliedRevision int64  `json:"applied_revision"`
	ModelDigest     string `json:"model_digest"`
}

type recoveryPool struct {
	ID           string `json:"id"`
	Revision     int64  `json:"revision"`
	ModelDigest  string `json:"model_digest"`
	Certificates int    `json:"certificates"`
}

func recoveryCheck(args []string) error {
	set := flag.NewFlagSet("recovery-check", flag.ContinueOnError)
	path := set.String("config", "/etc/nsc/config.yaml", "isolated Controller config")
	evidencePath := set.String("evidence", "", "live Gateway evidence JSON file")
	if err := set.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	var evidence []RecoveryEvidence
	if *evidencePath != "" {
		raw, err := os.ReadFile(*evidencePath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &evidence); err != nil {
			return errors.New("invalid Gateway evidence JSON")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store, err := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Ping(ctx); err != nil {
		return err
	}
	if err := store.ValidateRecoverySchema(ctx); err != nil {
		return err
	}
	keyring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
	if err != nil {
		return err
	}
	application := core.New(store, cfg, keyring)
	counts, err := recovery.CheckSecrets(ctx, store, keyring)
	if err != nil {
		return err
	}
	result := struct {
		Status                string         `json:"status"`
		Pools                 []recoveryPool `json:"pools"`
		Gateways              int            `json:"gateways"`
		DecryptedCertificates int            `json:"decrypted_certificates"`
		EncryptedValues       map[string]int `json:"decrypted_values"`
		GatewayEvidence       string         `json:"gateway_evidence"`
		KeyVerification       string         `json:"key_verification"`
	}{Status: "verified", Pools: []recoveryPool{}, DecryptedCertificates: counts["certificate"], EncryptedValues: counts, GatewayEvidence: "not_supplied", KeyVerification: "no_encrypted_material"}
	for _, count := range counts {
		if count > 0 {
			result.KeyVerification = "verified"
			break
		}
	}
	byPool := map[string]recoveryPool{}
	for after := ""; ; {
		pools, err := store.ListPools(ctx, storage.Page{After: after, Limit: 200})
		if err != nil {
			return err
		}
		for _, pool := range pools {
			snapshot, err := application.BuildGatewaySnapshot(ctx, pool.ID)
			if err != nil {
				return fmt.Errorf("restore cannot decrypt or build pool %s: %w", pool.ID, err)
			}
			encoded, err := gateway.EncodeSnapshot(snapshot, gateway.DefaultChunkSize)
			if err != nil {
				return err
			}
			value := recoveryPool{ID: pool.ID, Revision: pool.DesiredRevision, ModelDigest: encoded.Header.ModelDigest, Certificates: len(snapshot.Certificates)}
			byPool[pool.ID] = value
			result.Pools = append(result.Pools, value)
		}
		if len(pools) < 200 {
			break
		}
		after = pools[len(pools)-1].ID
	}
	for _, live := range evidence {
		if live.GatewayID == "" || live.PoolID == "" || live.AppliedRevision < 1 || live.ModelDigest == "" {
			return errors.New("incomplete live Gateway evidence")
		}
		pool, ok := byPool[live.PoolID]
		if !ok || pool.Revision < live.AppliedRevision || pool.Revision == live.AppliedRevision && pool.ModelDigest != live.ModelDigest {
			return fmt.Errorf("restored pool %s is older than or differs from live Gateway %s", live.PoolID, live.GatewayID)
		}
		stored, err := store.GetGateway(ctx, live.GatewayID)
		if err != nil || stored.PoolID != live.PoolID || stored.AppliedRevision > pool.Revision {
			return fmt.Errorf("restored Gateway %s has incompatible revision evidence", live.GatewayID)
		}
		result.Gateways++
	}
	if len(evidence) > 0 {
		result.GatewayEvidence = "verified"
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
