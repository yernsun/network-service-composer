package recovery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func recoveryFixture(t *testing.T) (*storage.SQLStore, *security.Keyring, model.Runtime) {
	t.Helper()
	store, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "restored.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx := context.Background()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	ring, err := security.NewKeyring("backup", map[string]string{"backup": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))})
	if err != nil {
		t.Fatal(err)
	}
	c := core.New(store, config.Defaults(), ring)
	pool, err := c.CreatePool(ctx, core.CreatePoolInput{Name: "isolated recovery", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	runtime := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "isolated runtime", Type: model.RuntimeDocker, Endpoint: "unix:///fixture-unavailable/docker.sock", Enabled: true, ManagementEnabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateRuntime(ctx, &runtime); err != nil {
		t.Fatal(err)
	}
	return store, ring, runtime
}

func recoveryOperation(t *testing.T, s *storage.SQLStore, ring *security.Keyring, runtime model.Runtime, created time.Time) model.DeploymentOperation {
	t.Helper()
	v := model.DeploymentOperation{ID: id.New(id.Operation), RuntimeID: runtime.ID, Type: "VOLUME_CREATE", Status: model.OperationQueued, Revision: 1, CreatedAt: created, UpdatedAt: created}
	var err error
	v.EncryptedInput, err = ring.Encrypt([]byte(`{"name":"fixture-preserved"}`), []byte("deployment-operation:"+v.ID))
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.CreateDeploymentOperation(context.Background(), &v)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRecoveryPreparationHoldsQueuedAndInterruptedWorkAcrossStartup(t *testing.T) {
	store, ring, runtime := recoveryFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Hour)
	interrupted := recoveryOperation(t, store, ring, runtime, now)
	claimed, err := store.ClaimDeploymentOperation(ctx, "old-controller", now, now.Add(-time.Minute))
	if err != nil || claimed.ID != interrupted.ID {
		t.Fatal("fixture claim", err)
	}
	queued := recoveryOperation(t, store, ring, runtime, now.Add(time.Second))
	plan, err := Prepare(ctx, store)
	if err != nil || len(plan.Items) != 2 {
		t.Fatal("restore was not inventoried", plan, err)
	}
	if _, err = store.ClaimDeploymentOperation(ctx, "new-controller", time.Now(), time.Now().Add(-time.Minute)); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("restored work was automatically reclaimed", err)
	}
	preserved, err := store.GetDeploymentOperation(ctx, claimed.ID)
	if err != nil || preserved.Status != model.OperationRunning || preserved.Revision != claimed.Revision || preserved.Attempt != claimed.Attempt || string(preserved.EncryptedInput) != string(claimed.EncryptedInput) {
		t.Fatal("interrupted evidence was overwritten", err)
	}
	if retry, err := Prepare(ctx, store); err != nil || retry.Fingerprint != plan.Fingerprint {
		t.Fatal("lost review file cannot be recovered", err)
	}
	if err = Release(ctx, store, plan); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("unreviewed restore activated", err)
	}
	plan.ActivationReviewed = true
	for i := range plan.Items {
		if plan.Items[i].ID == queued.ID {
			plan.Items[i].Decision = "resume"
		}
	}
	if err = Release(ctx, store, plan); err != nil {
		t.Fatal(err)
	}
	if prepared, err := store.RecoveryPrepared(ctx); err != nil || prepared {
		t.Fatal("explicit review did not clear startup barrier", err)
	}
	next, err := store.ClaimDeploymentOperation(ctx, "new-controller", time.Now(), time.Now().Add(-time.Minute))
	if err != nil || next.ID != queued.ID {
		t.Fatal("approved work was not the only released operation", next.ID, err)
	}
	if held, err := storage.RecoveryHeld(ctx, store, "deployment", claimed.ID); err != nil || !held {
		t.Fatal("unknown task lost recovery hold", err)
	}
}

func TestRecoveryReviewRejectsDriftAndInterruptedApprovalWithoutClearingHolds(t *testing.T) {
	for _, change := range []string{"input", "version", "extra item", "invalid decision", "interrupted", "barrier version", "creation time"} {
		t.Run(change, func(t *testing.T) {
			store, ring, runtime := recoveryFixture(t)
			ctx := context.Background()
			op := recoveryOperation(t, store, ring, runtime, time.Now().UTC())
			if change == "interrupted" {
				var err error
				op, err = store.ClaimDeploymentOperation(ctx, "old", time.Now(), time.Now().Add(-time.Hour))
				if err != nil {
					t.Fatal(err)
				}
			}
			plan, err := Prepare(ctx, store)
			if err != nil {
				t.Fatal(err)
			}
			plan.ActivationReviewed = true
			plan.Items[0].Decision = "resume"
			switch change {
			case "input":
				plan.Items[0].InputDigest = "different"
			case "version":
				runtime.Name = "changed after backup"
				if err = store.UpdateRuntime(ctx, &runtime, runtime.Revision); err != nil {
					t.Fatal(err)
				}
			case "extra item":
				plan.Items = append(plan.Items, plan.Items[0])
			case "invalid decision":
				plan.Items[0].Decision = "guess"
			case "barrier version":
				plan.BarrierRevision++
			case "creation time":
				plan.CreatedAt = plan.CreatedAt.Add(time.Minute)
			}
			if err = Release(ctx, store, plan); err == nil {
				t.Fatal("incompatible review activated")
			}
			if held, err := storage.RecoveryHeld(ctx, store, "deployment", op.ID); err != nil || !held {
				t.Fatal("failed review cleared protection", err)
			}
			preserved, err := store.GetDeploymentOperation(ctx, op.ID)
			if err != nil || preserved.Status != op.Status || preserved.Revision != op.Revision {
				t.Fatal("review rewrote operation outcome", err)
			}
		})
	}
}

func TestRecoveryInventoryFreezesAllACMEStatesAndMissingManagedDomains(t *testing.T) {
	store, ring, runtime := recoveryFixture(t)
	ctx, now := context.Background(), time.Now().UTC()
	for _, status := range []model.CertificateStatus{model.CertificatePending, model.CertificateAuthorizing, model.CertificateValidating, model.CertificateRenewing, model.CertificateActive, model.CertificateError} {
		certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: runtime.PoolID, Provider: "acme", Status: status, PrimaryDomain: "restore.example.test", Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
		var err error
		certificate.EncryptedPrivateKey, err = ring.Encrypt([]byte("unit envelope fixture"), []byte(certificate.ID))
		if err != nil {
			t.Fatal(err)
		}
		if err = store.CreateCertificate(ctx, &certificate); err != nil {
			t.Fatal(err)
		}
		if err = store.PutChallenge(ctx, storage.Challenge{ID: id.New(id.Challenge), CertificateID: certificate.ID, Domain: certificate.PrimaryDomain, Type: string(model.ACMEHTTP01), Token: certificate.ID, Status: "PENDING", ExpiresAt: now.Add(-time.Hour), CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	imported := model.Certificate{ID: id.New(id.Certificate), PoolID: runtime.PoolID, Provider: "custom", Status: model.CertificateActive, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	var err error
	imported.EncryptedPrivateKey, err = ring.Encrypt([]byte("unit imported envelope fixture"), []byte(imported.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateCertificate(ctx, &imported); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []model.TLSMode{model.TLSManaged, model.TLSDisabled} {
		domain := model.Domain{ID: id.New(id.Domain), PoolID: runtime.PoolID, Host: string(mode) + ".example.test", TLSMode: mode, Enabled: true, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
		if err := store.CreateDomain(ctx, &domain); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := Prepare(ctx, store)
	if err != nil || len(plan.Items) != 7 {
		t.Fatal("ACME or missing-domain recovery work omitted", len(plan.Items), err)
	}
	for _, item := range plan.Items {
		if held, err := storage.RecoveryHeld(ctx, store, item.Kind, item.ID); err != nil || !held {
			t.Fatal("restored ACME work lacks hold", err)
		}
	}
	if pending, err := store.ListPendingCertificates(ctx, now.Add(time.Hour), 200); err != nil || len(pending) != 0 {
		t.Fatal("pending certificate claim bypassed global hold", pending, err)
	}
	for i, item := range plan.Items {
		if item.State == string(model.CertificateRenewing) || item.State == string(model.CertificateAuthorizing) || item.State == string(model.CertificateValidating) {
			plan.ActivationReviewed, plan.Items[i].Decision = true, "resume"
			if err = Release(ctx, store, plan); err == nil {
				t.Fatal("provider-side unknown renewal automatically resumed")
			}
			plan.Items[i].Decision = "hold"
		}
	}
	if err = Release(ctx, store, plan); err != nil {
		t.Fatal(err)
	}
	if pending, err := store.ListPendingCertificates(ctx, now.Add(time.Hour), 200); err != nil || len(pending) != 0 {
		t.Fatal("certificate hold disappeared after normal activation", pending, err)
	}
	counts, err := CheckSecrets(ctx, store, ring)
	if err != nil || counts["certificate"] != 7 {
		t.Fatal("certificate envelopes omitted from restored-secret verification", counts, err)
	}
	if _, err = store.GarbageCollect(ctx, now.Add(72*time.Hour), time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, item := range plan.Items {
		if item.Kind == "certificate" {
			if _, err = store.GetCertificate(ctx, item.ID); err != nil {
				t.Fatal("GC discarded held certificate evidence", err)
			}
		}
	}
	var preservedChallenges int
	if err = store.DB().QueryRowContext(ctx, "SELECT count(*) FROM acme_challenges").Scan(&preservedChallenges); err != nil || preservedChallenges != 6 {
		t.Fatal("GC discarded provider-side unknown challenge history", preservedChallenges, err)
	}
}

func TestRecoveryDecryptsEveryEnvelopeWithoutRequiringCertificates(t *testing.T) {
	store, ring, runtime := recoveryFixture(t)
	ctx := context.Background()
	counts, err := CheckSecrets(ctx, store, ring)
	if err != nil || counts["certificate"] != 0 {
		t.Fatal("HTTP-only database rejected", counts, err)
	}
	c := core.New(store, config.Defaults(), ring)
	if _, err = c.SaveDNSProfile(ctx, "restore-dns", core.DNSProfileInput{Reference: "restore-dns", Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: "preserved-dns-token"}}, 0); err != nil {
		t.Fatal(err)
	}
	op := recoveryOperation(t, store, ring, runtime, time.Now().UTC())
	account := model.ACMEAccount{ID: "account-backup", DirectoryURL: "https://ca.example.test/directory", Email: "ops@example.test", EncryptionKeyID: "backup", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	account.EncryptedPrivateKey, err = ring.Encrypt([]byte("preserved-account-key"), []byte(account.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpsertACMEAccount(ctx, &account); err != nil {
		t.Fatal(err)
	}
	application := model.Application{ID: id.New(id.Application), RuntimeID: runtime.ID, Name: "recovery app", ProjectName: "recovery-app", Status: model.ApplicationPending, Managed: true, Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	revision := model.ApplicationRevision{ID: id.New(id.ApplicationRevision), ApplicationID: application.ID, Sequence: 1, EncryptionKeyID: "backup", CreatedAt: time.Now().UTC()}
	revision.EncryptedCompose, err = ring.Encrypt([]byte("services: {}"), []byte("application-revision:"+revision.ID+":compose"))
	if err != nil {
		t.Fatal(err)
	}
	revision.EncryptedEnvironment, err = ring.Encrypt([]byte("{}"), []byte("application-revision:"+revision.ID+":environment"))
	if err != nil {
		t.Fatal(err)
	}
	revision.EncryptedResolved, err = ring.Encrypt([]byte("services: {}"), []byte("application-revision:"+revision.ID+":resolved"))
	if err != nil {
		t.Fatal(err)
	}
	appOperation := model.DeploymentOperation{ID: id.New(id.Operation), ApplicationID: application.ID, RuntimeID: runtime.ID, Type: "INSTALL", Status: model.OperationQueued, Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err = store.CreateApplication(ctx, &application, &revision, &appOperation); err != nil {
		t.Fatal(err)
	}
	hookSecret, err := ring.Encrypt([]byte("preserved-hook-token"), []byte("image-hook:env-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	hookRaw, err := json.Marshal(map[string][]byte{"encrypted_secret": hookSecret})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: "env-fixture:hook", Kind: "hook", Payload: hookRaw, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}}}); err != nil {
		t.Fatal(err)
	}
	counts, err = CheckSecrets(ctx, store, ring)
	if err != nil || counts["control_document"] != 1 || counts["hook"] != 1 || counts["operation"] != 1 || counts["application_revision"] != 3 || counts["acme_account"] != 1 {
		t.Fatal("encrypted category omitted", counts, err)
	}
	wrong, err := security.NewKeyring("backup", map[string]string{"backup": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("w", 32)))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CheckSecrets(ctx, store, wrong); err == nil || strings.Contains(err.Error(), "preserved") {
		t.Fatal("wrong backup key accepted or plaintext exposed", err)
	}
	for _, candidate := range []struct{ name, table, column, id string }{
		{"operation", "deployment_operations", "encrypted_input", op.ID},
		{"revision", "application_revisions", "encrypted_environment", revision.ID},
		{"account", "acme_accounts", "encrypted_private_key", account.ID},
		{"control", "control_documents", "payload", "dns-provider-profile:restore-dns"},
		{"hook", "control_documents", "payload", "env-fixture:hook"},
	} {
		t.Run(candidate.name, func(t *testing.T) {
			query := "UPDATE " + candidate.table + " SET " + candidate.column + "=? WHERE id=?"
			var original []byte
			if err := store.DB().QueryRowContext(ctx, "SELECT "+candidate.column+" FROM "+candidate.table+" WHERE id=?", candidate.id).Scan(&original); err != nil {
				t.Fatal(err)
			}
			damaged := []byte{1}
			if candidate.table == "control_documents" {
				damaged = []byte("{}")
			}
			if _, err := store.DB().ExecContext(ctx, query, damaged, candidate.id); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := store.DB().ExecContext(ctx, query, original, candidate.id); err != nil {
					t.Fatal(err)
				}
			}()
			if _, err := CheckSecrets(ctx, store, ring); err == nil {
				t.Fatal("corrupt recoverable material accepted")
			}
		})
	}
}

func TestReviewedDeletionDeferralPreservesCompletedHistoryAndAllowsFreshResume(t *testing.T) {
	store, _, _ := recoveryFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	task := core.ProjectDeletion{ProjectID: "restored-project", Status: "RUNNING", Phase: "remove", Attempts: []core.DeletionAttempt{{Number: 1, Actor: "old-controller", StartedAt: now, Status: "RUNNING"}}, Preview: core.ProjectDeletionPreview{Applications: []core.DeletionApplication{{ID: "restored-app", CompletedObjects: []string{"already-removed-container"}, Status: "RUNNING"}}}}
	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	doc := storage.ControlDocument{ID: "restored-deletion", Kind: "project-deletion", ParentID: task.ProjectID, Payload: raw, CreatedAt: now, UpdatedAt: now}
	if err = store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: doc}}); err != nil {
		t.Fatal(err)
	}
	plan, err := Prepare(ctx, store)
	if err != nil || len(plan.Items) != 1 {
		t.Fatal(plan, err)
	}
	plan.ActivationReviewed = true
	plan.Items[0].Decision = "defer"
	if err = Release(ctx, store, plan); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetControlDocument(ctx, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	var deferred core.ProjectDeletion
	if err = json.Unmarshal(updated.Payload, &deferred); err != nil {
		t.Fatal(err)
	}
	if deferred.Status != "UNKNOWN" || deferred.ErrorCode != "project.deletion.interrupted" || deferred.Attempts[0].Status != "UNKNOWN" || len(deferred.Preview.Applications[0].CompletedObjects) != 1 || deferred.Preview.Applications[0].CompletedObjects[0] != "already-removed-container" {
		t.Fatal("deferral lost original completion/unknown evidence")
	}
	archived, err := store.GetControlDocument(ctx, "recovery:before:"+doc.ID+":1")
	if err != nil || string(archived.Payload) != string(raw) {
		t.Fatal("original interrupted state was not archived", err)
	}
	if held, err := storage.RecoveryHeld(ctx, store, "project-deletion", doc.ID); err != nil || held {
		t.Fatal("existing fresh-preview resume path remains blocked", err)
	}
	if err = Release(ctx, store, plan); err == nil {
		t.Fatal("deletion deferral could be applied twice")
	}
}

func TestRecoveryInventoryIncludesLaterQueuePagesAndCanceledReadsKeepProtection(t *testing.T) {
	store, ring, runtime := recoveryFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 201; i++ {
		recoveryOperation(t, store, ring, runtime, now.Add(time.Duration(i)*time.Second))
	}
	plan, err := Prepare(ctx, store)
	if err != nil || len(plan.Items) != 201 {
		t.Fatal("later queue page omitted from recovery hold inventory", len(plan.Items), err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = Prepare(canceled, store); err == nil {
		t.Fatal("unavailable preparation returned usable evidence")
	}
	plan.ActivationReviewed = true
	if err = Release(canceled, store, plan); err == nil {
		t.Fatal("canceled review cleared recovery protection")
	}
	if _, err = CheckSecrets(canceled, store, ring); err == nil {
		t.Fatal("canceled decryption check returned verification")
	}
	for _, item := range plan.Items {
		if held, err := storage.RecoveryHeld(ctx, store, item.Kind, item.ID); err != nil || !held {
			t.Fatal("unverified later-page work lost its hold", err)
		}
	}
}
