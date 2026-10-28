// recovery-fixture seeds and inspects only explicitly isolated E2E databases.
// It is verification tooling, not a Controller maintenance or operator API.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type operationEvidence struct {
	Status      model.DeploymentOperationStatus `json:"status"`
	Revision    int64                           `json:"revision"`
	Attempt     int                             `json:"attempt"`
	InputDigest string                          `json:"input_digest"`
	Held        bool                            `json:"held"`
}

type metadata struct {
	PoolID                 string   `json:"pool_id"`
	ApprovedOperationID    string   `json:"approved_operation_id"`
	InterruptedOperationID string   `json:"interrupted_operation_id"`
	OperationIDs           []string `json:"operation_ids"`
}

func main() {
	if err := execute(); err != nil {
		fmt.Fprintln(os.Stderr, "isolated recovery fixture failed:", err)
		os.Exit(1)
	}
}

func execute() error {
	path := flag.String("config", "", "isolated JSON/YAML config")
	action := flag.String("action", "inspect", "seed, inspect or claim-approved")
	metadataPath := flag.String("metadata", "", "private fixture metadata")
	runtimeEndpoint := flag.String("runtime-endpoint", "", "isolated nested daemon endpoint for seed-runtime")
	containerID := flag.String("container", "", "owned nested workload ID for seed-runtime")
	flag.Parse()
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	store, err := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		return err
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if *action == "seed" {
		return seed(ctx, store, cfg, *metadataPath)
	}
	if *action == "seed-runtime" {
		return seedRuntime(ctx, store, cfg, *metadataPath, *runtimeEndpoint, *containerID)
	}
	raw, err := os.ReadFile(*metadataPath)
	if err != nil {
		return err
	}
	var meta metadata
	if err = json.Unmarshal(raw, &meta); err != nil {
		return err
	}
	if *action == "claim-approved" {
		operation, err := store.ClaimDeploymentOperation(ctx, "fixture-reviewed", time.Now().UTC(), time.Now().UTC().Add(-time.Minute))
		if err != nil {
			return err
		}
		if operation.ID != meta.ApprovedOperationID {
			return errors.New("claim selected an unapproved restored operation")
		}
	} else if *action != "inspect" {
		return errors.New("unknown fixture action")
	}
	fingerprint, err := storage.RecoveryFingerprint(ctx, store.DB())
	if err != nil {
		return err
	}
	operations := map[string]operationEvidence{}
	for _, key := range meta.OperationIDs {
		operation, err := store.GetDeploymentOperation(ctx, key)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(operation.EncryptedInput)
		held, err := storage.RecoveryHeld(ctx, store, "deployment", key)
		if err != nil {
			return err
		}
		operations[key] = operationEvidence{Status: operation.Status, Revision: operation.Revision, Attempt: operation.Attempt,
			InputDigest: hex.EncodeToString(digest[:]), Held: held}
	}
	prepared, err := store.RecoveryPrepared(ctx)
	if err != nil {
		return err
	}
	var business string
	if err = store.DB().QueryRowContext(ctx, "SELECT value FROM fixture_business_data WHERE id='preserved'").Scan(&business); err != nil {
		return err
	}
	if business != "fixture-application-row-before-backup" {
		return errors.New("fixture application data was not restored")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"fingerprint": fingerprint, "prepared": prepared,
		"operations": operations, "fixture_business_rows_preserved": true})
}

func seedRuntime(ctx context.Context, store *storage.SQLStore, cfg config.Config, path, endpoint, containerID string) error {
	if endpoint == "" || containerID == "" {
		return errors.New("isolated runtime endpoint and owned container identity are required")
	}
	ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
	if err != nil {
		return err
	}
	c := core.New(store, cfg, ring)
	if err = c.BootstrapIdentity(ctx); err != nil {
		return err
	}
	pool, err := c.CreatePool(ctx, core.CreatePoolInput{Name: "runtime reconciliation fixture", BackendNetwork: "fixture-ingress",
		ChallengeUpstream: "http://controller.example.test:8080"})
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	runtime := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "owned isolated engine", Type: model.RuntimeDocker,
		Endpoint: endpoint, Enabled: true, ManagementEnabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateRuntime(ctx, &runtime); err != nil {
		return err
	}
	app := model.Application{ID: id.New(id.Application), RuntimeID: runtime.ID, Name: "reconciliation app", ProjectName: "recovery-owned",
		Status: model.ApplicationRunning, Managed: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	revision := model.ApplicationRevision{ID: id.New(id.ApplicationRevision), ApplicationID: app.ID, Sequence: 1,
		EncryptionKeyID: cfg.Security.PrimaryKeyID, CreatedAt: now}
	for _, item := range []struct {
		suffix string
		target *[]byte
	}{{"compose", &revision.EncryptedCompose},
		{"environment", &revision.EncryptedEnvironment}, {"resolved", &revision.EncryptedResolved}} {
		*item.target, err = ring.Encrypt([]byte("{}"), []byte("application-revision:"+revision.ID+":"+item.suffix))
		if err != nil {
			return err
		}
	}
	op := model.DeploymentOperation{ID: id.New(id.Operation), ApplicationID: app.ID, RuntimeID: runtime.ID, RevisionID: revision.ID,
		Type: "REDEPLOY", Status: model.OperationQueued, Revision: 1, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}
	op.EncryptedInput, err = ring.Encrypt([]byte(`{"action":"redeploy"}`), []byte("deployment-operation:"+op.ID))
	if err != nil {
		return err
	}
	if err = store.CreateApplication(ctx, &app, &revision, &op); err != nil {
		return err
	}
	service := model.Service{ID: id.New(id.Service), ApplicationID: app.ID, RuntimeID: runtime.ID, Name: "web", Namespace: app.ProjectName,
		ServiceKey: app.ProjectName + "/web", RuntimeObjectID: containerID, ExternalID: containerID, Managed: true,
		Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.UpsertService(ctx, &service); err != nil {
		return err
	}
	if _, err = store.ClaimDeploymentOperation(ctx, "fixture-interrupted", now.Add(-time.Hour), now.Add(-2*time.Hour)); err != nil {
		return err
	}
	if _, err = store.DB().ExecContext(ctx, "CREATE TABLE fixture_business_data (id VARCHAR(100) PRIMARY KEY, value VARCHAR(200) NOT NULL)"); err != nil {
		return err
	}
	if _, err = store.DB().ExecContext(ctx, "INSERT INTO fixture_business_data VALUES ('preserved','fixture-application-row-before-backup')"); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(metadata{PoolID: pool.ID, ApprovedOperationID: op.ID, InterruptedOperationID: op.ID,
		OperationIDs: []string{op.ID}})
}

func seed(ctx context.Context, store *storage.SQLStore, cfg config.Config, path string) (err error) {
	stage := "keyring"
	defer func() {
		if err != nil {
			err = fmt.Errorf("seed %s: %w", stage, err)
		}
	}()
	ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
	if err != nil {
		return err
	}
	c := core.New(store, cfg, ring)
	stage = "persisted identity"
	if err = c.BootstrapIdentity(ctx); err != nil {
		return err
	}
	stage = "HTTP-only pool"
	pool, err := c.CreatePool(ctx, core.CreatePoolInput{Name: "HTTP-only recovery fixture", BackendNetwork: "fixture-ingress", ChallengeUpstream: "http://controller.example.test:8080"})
	if err != nil {
		return err
	}
	stage = "HTTP-only domain"
	if _, err = c.CreateDomain(ctx, core.CreateDomainInput{PoolID: pool.ID, DomainConfigInput: core.DomainConfigInput{
		Host: "recovery.example.test", EntryPoints: []string{"http"}, TLS: core.DomainTLSInput{Mode: model.TLSDisabled}}}); err != nil {
		return err
	}
	stage = "DNS profile"
	if _, err = c.SaveDNSProfile(ctx, "recovery-fixture", core.DNSProfileInput{Reference: "recovery-fixture", Provider: "cloudflare",
		Credentials: config.DNSProviderCredentials{APIToken: "fixture-encrypted-provider-material"}}, 0); err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	stage = "runtime"
	runtime := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "isolated unavailable runtime", Type: model.RuntimeDocker,
		Endpoint: "unix:///fixture-unavailable/docker.sock", Enabled: true, ManagementEnabled: true, Status: model.RuntimeOnline,
		Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateRuntime(ctx, &runtime); err != nil {
		return err
	}
	meta := metadata{PoolID: pool.ID}
	stage = "operation history"
	for index := 0; index < 3; index++ {
		created := now.Add(-time.Hour).Add(time.Duration(index) * time.Second)
		operation := model.DeploymentOperation{ID: id.New(id.Operation), RuntimeID: runtime.ID, Type: "VOLUME_CREATE",
			Status: model.OperationQueued, Revision: 1, CreatedAt: created, UpdatedAt: created}
		operation.EncryptedInput, err = ring.Encrypt([]byte(`{"name":"fixture-preserved"}`), []byte("deployment-operation:"+operation.ID))
		if err != nil {
			return err
		}
		if _, err = store.CreateDeploymentOperation(ctx, &operation); err != nil {
			return err
		}
		meta.OperationIDs = append(meta.OperationIDs, operation.ID)
		if index == 0 {
			claimed, err := store.ClaimDeploymentOperation(ctx, "fixture-prebackup", created, created.Add(-time.Minute))
			if err != nil || claimed.ID != operation.ID {
				return fmt.Errorf("fixture interrupted claim: %w", err)
			}
			meta.InterruptedOperationID = operation.ID
		} else if index == 1 {
			meta.ApprovedOperationID = operation.ID
		}
	}
	account := model.ACMEAccount{ID: id.New(id.Account), DirectoryURL: "https://ca.example.test/directory", Email: "ops@example.test",
		EncryptionKeyID: cfg.Security.PrimaryKeyID, CreatedAt: now, UpdatedAt: now}
	stage = "ACME account"
	account.EncryptedPrivateKey, err = ring.Encrypt([]byte("fixture-encrypted-account-material"), []byte(account.ID))
	if err != nil {
		return err
	}
	if err = store.UpsertACMEAccount(ctx, &account); err != nil {
		return err
	}
	application := model.Application{ID: id.New(id.Application), RuntimeID: runtime.ID, Name: "recovered application",
		ProjectName: "recovered-app", Status: model.ApplicationPending, Managed: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	stage = "Application revision"
	revision := model.ApplicationRevision{ID: id.New(id.ApplicationRevision), ApplicationID: application.ID, Sequence: 1,
		EncryptionKeyID: cfg.Security.PrimaryKeyID, CreatedAt: now}
	for _, item := range []struct {
		suffix, plaintext string
		target            *[]byte
	}{
		{"compose", "services: {}", &revision.EncryptedCompose}, {"environment", "{}", &revision.EncryptedEnvironment},
		{"resolved", "services: {}", &revision.EncryptedResolved},
	} {
		*item.target, err = ring.Encrypt([]byte(item.plaintext), []byte("application-revision:"+revision.ID+":"+item.suffix))
		if err != nil {
			return err
		}
	}
	appOperation := model.DeploymentOperation{ID: id.New(id.Operation), ApplicationID: application.ID, RuntimeID: runtime.ID,
		Type: "INSTALL", Status: model.OperationQueued, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateApplication(ctx, &application, &revision, &appOperation); err != nil {
		return err
	}
	meta.OperationIDs = append(meta.OperationIDs, appOperation.ID)
	stage = "variable and history envelopes"
	projectID := id.New("proj")
	projectJSON, err := json.Marshal(map[string]string{"name": "fixture project", "slug": "fixture-project", "default_runtime_id": runtime.ID})
	if err != nil {
		return err
	}
	if err = store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: projectID,
		Kind: "project", Payload: projectJSON, CreatedAt: now, UpdatedAt: now}}}); err != nil {
		return err
	}
	for _, item := range []struct{ key, kind string }{{projectID + ":variables", "variables"}, {projectID + ":variable-history", "variable-version"}} {
		encrypted, err := ring.Encrypt([]byte(`{"values":[{"key":"FIXTURE_SECRET","value":"fixture-material","secret":true}]}`), []byte("control:"+item.key))
		if err != nil {
			return err
		}
		raw, err := json.Marshal(map[string]any{"ciphertext": encrypted, "key_id": cfg.Security.PrimaryKeyID})
		if err != nil {
			return err
		}
		if err = store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: item.key,
			Kind: item.kind, ParentID: projectID, Payload: raw, CreatedAt: now, UpdatedAt: now}}}); err != nil {
			return err
		}
	}
	stage = "hook envelope"
	hook, err := ring.Encrypt([]byte("fixture-hook-material"), []byte("image-hook:fixture"))
	if err != nil {
		return err
	}
	hookJSON, err := json.Marshal(map[string][]byte{"encrypted_secret": hook})
	if err != nil {
		return err
	}
	if err = store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: "fixture:hook",
		Kind: "hook", Payload: hookJSON, CreatedAt: now, UpdatedAt: now}}}); err != nil {
		return err
	}
	stage = "fixture business rows"
	if _, err = store.DB().ExecContext(ctx, "CREATE TABLE fixture_business_data (id VARCHAR(100) PRIMARY KEY, value VARCHAR(200) NOT NULL)"); err != nil {
		return err
	}
	if _, err = store.DB().ExecContext(ctx, "INSERT INTO fixture_business_data VALUES ('preserved','fixture-application-row-before-backup')"); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(meta)
}
