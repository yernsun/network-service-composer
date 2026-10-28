package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/recovery"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// The CLI uses the actual SDK and ownership inspector against this bounded
// read-only engine protocol fixture. A destructive request fails the test.
func TestRecoveryReconcileCommandCapturesAndRechecksOwnedRuntimeWithoutExecutingWork(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	cfg, path := commandConfigFixture(t)
	cfg.Controller.RecoveryMode, cfg.Storage.AutoMigrate = true, false
	writeCommandConfig(t, cfg, path)
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			t.Errorf("recovery issued runtime mutation: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("API-Version", "1.54")
		if r.URL.Path == "/_ping" {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var result any
		switch {
		case strings.HasSuffix(r.URL.Path, "/info"):
			result = map[string]any{"ID": "original-command-engine", "Swarm": map[string]any{}}
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			if r.URL.Query().Get("all") != "1" || !strings.Contains(r.URL.Query().Get("filters"), "com.docker.compose.project=recovery-command") {
				t.Error("unbounded runtime inspection")
			}
			result = []any{map[string]any{"Id": "owned-command-container"}}
		case strings.HasSuffix(r.URL.Path, "/containers/owned-command-container/json"):
			result = map[string]any{"Id": "owned-command-container", "Config": map[string]any{"Labels": map[string]string{"com.docker.compose.project": "recovery-command", "com.docker.compose.service": "web"}}, "State": map[string]any{"Running": true}, "Mounts": []any{}}
		default:
			t.Errorf("unexpected runtime read: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Error(err)
		}
	}))
	defer engine.Close()
	store, err := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
	if err != nil {
		t.Fatal(err)
	}
	c := core.New(store, cfg, ring)
	pool, err := c.CreatePool(ctx, core.CreatePoolInput{Name: "command recovery", BackendNetwork: "fixture", ChallengeUpstream: "http://controller:8080"})
	if err != nil {
		t.Fatal(err)
	}
	rt := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "command runtime", Type: model.RuntimeDocker, Endpoint: engine.URL, Enabled: true, ManagementEnabled: true, Status: model.RuntimeOnline, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err = store.CreateRuntime(ctx, &rt); err != nil {
		t.Fatal(err)
	}
	app := model.Application{ID: id.New(id.Application), RuntimeID: rt.ID, Name: "command app", ProjectName: "recovery-command", Status: model.ApplicationRunning, Managed: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	revision := model.ApplicationRevision{ID: id.New(id.ApplicationRevision), ApplicationID: app.ID, Sequence: 1, EncryptionKeyID: cfg.Security.PrimaryKeyID, CreatedAt: now}
	for field, target := range map[string]*[]byte{"compose": &revision.EncryptedCompose, "environment": &revision.EncryptedEnvironment, "resolved": &revision.EncryptedResolved} {
		*target, err = ring.Encrypt([]byte(`{}`), []byte("application-revision:"+revision.ID+":"+field))
		if err != nil {
			t.Fatal(err)
		}
	}
	op := model.DeploymentOperation{ID: id.New(id.Operation), ApplicationID: app.ID, RuntimeID: rt.ID, RevisionID: revision.ID, Type: "REDEPLOY", Status: model.OperationQueued, Revision: 1, CreatedAt: now, UpdatedAt: now}
	op.EncryptedInput, err = ring.Encrypt([]byte(`{"action":"redeploy"}`), []byte("deployment-operation:"+op.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateApplication(ctx, &app, &revision, &op); err != nil {
		t.Fatal(err)
	}
	service := model.Service{ID: id.New(id.Service), ApplicationID: app.ID, RuntimeID: rt.ID, ServiceKey: app.ProjectName + "/web", Name: "web", Namespace: app.ProjectName, Managed: true, RuntimeObjectID: "owned-command-container", ExternalID: "owned-command-container", Status: model.ServiceAvailable, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err = store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDeploymentOperation(ctx, "original-owner", now, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	prepared := filepath.Join(t.TempDir(), "prepared.json")
	if _, err = commandOutput(t, "recovery-prepare", "--config", path, "--output", prepared); err != nil {
		t.Fatal(err)
	}
	before, err := storage.RecoveryFingerprint(ctx, store.DB())
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "reconciled.json")
	for _, candidate := range [][]string{
		{"--operation", op.ID, "--expected-engine", "replacement-engine"},
		{"--operation", "missing-held-operation", "--expected-engine", "original-command-engine"},
	} {
		args := append([]string{"recovery-reconcile", "--config", path, "--output", output}, candidate...)
		if _, err = commandOutput(t, args...); err == nil {
			t.Fatal("unverified operation produced reconciliation")
		}
	}
	args := []string{"recovery-reconcile", "--config", path, "--operation", op.ID, "--expected-engine", "original-command-engine", "--review", prepared, "--output", output}
	preparedRaw, err := os.ReadFile(prepared)
	if err != nil {
		t.Fatal(err)
	}
	var originalPlan recovery.Plan
	if err = json.Unmarshal(preparedRaw, &originalPlan); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"missing config", "missing review", "malformed review", "unknown format", "changed barrier version", "changed fingerprint"} {
		invalidArgs := append([]string(nil), args...)
		if scenario == "missing config" {
			invalidArgs[2] = filepath.Join(t.TempDir(), "missing.yaml")
		} else {
			badFile := filepath.Join(t.TempDir(), "bad-review.json")
			if scenario != "missing review" {
				bad := originalPlan
				switch scenario {
				case "unknown format":
					bad.Format++
				case "changed barrier version":
					bad.BarrierRevision++
				case "changed fingerprint":
					bad.Fingerprint = "changed-baseline"
				}
				raw, err := json.Marshal(bad)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "malformed review" {
					raw = []byte(`{`)
				}
				if err = os.WriteFile(badFile, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			invalidArgs[8] = badFile
		}
		if _, err = commandOutput(t, invalidArgs...); err == nil {
			t.Fatal("invalid reconcile preparation produced evidence", scenario)
		}
	}
	if result, err := commandOutput(t, args...); err != nil || !strings.Contains(result, `"status":"observed"`) {
		t.Fatal("fresh capture failed", result, err)
	}
	if _, err = commandOutput(t, args...); err == nil {
		t.Fatal("old reconciliation file overwritten")
	}
	after, err := storage.RecoveryFingerprint(ctx, store.DB())
	if err != nil || after != before {
		t.Fatal("read-only capture mutated the restore", err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var plan recovery.Plan
	if err = json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.ActivationReviewed || len(plan.Items) != 1 || plan.Items[0].Decision != "reconcile" || plan.Items[0].Reconciliation == nil {
		t.Fatal("capture auto-approved activation")
	}
	plan.ActivationReviewed = true
	raw, err = json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(output, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = commandOutput(t, "recovery-release", "--config", path, "--review", output); err != nil {
		t.Fatal("fresh runtime recheck refused reviewed capture", err)
	}
	preserved, err := store.GetDeploymentOperation(ctx, op.ID)
	if err != nil || preserved.Status != model.OperationRunning || preserved.Attempt != claimed.Attempt || preserved.Revision != claimed.Revision {
		t.Fatal("release guessed operation outcome", err)
	}
}
