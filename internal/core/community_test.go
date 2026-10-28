package core

import (
	"context"
	"encoding/json"
	"errors"
	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestIdentityRevocationAndLastAdministrator(t *testing.T) {
	c, _, _ := controlFixture(t)
	c.config.Security.AdminUsername, c.config.Security.AdminToken = "owner", "installation-token-123456"
	ctx := context.Background()
	if err := c.BootstrapIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := c.Authenticate(ctx, "owner", c.config.Security.AdminToken, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.SaveUser(ctx, u.ID, UserInput{Username: "owner", Role: "viewer", Enabled: true}, u.Revision); err == nil {
		t.Fatal("last administrator demoted")
	}
	viewer, err := c.SaveUser(ctx, "", UserInput{Username: "reader", Role: "viewer", Enabled: true, Password: "reader-password-123456"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.SaveUser(ctx, "", UserInput{Username: "READER", Role: "viewer", Enabled: true, Password: "reader-password-123456"}, 0); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("duplicate username", err)
	}
	token, err := c.RotateUserToken(ctx, viewer.ID, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err = c.Authenticate(ctx, "", "", token)
	if err != nil || viewer.Role != "viewer" {
		t.Fatal(viewer, err)
	}
	_, err = c.SaveUser(ctx, viewer.ID, UserInput{Username: "reader", Role: "viewer", Enabled: false}, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Authenticate(ctx, "", "", token); err == nil {
		t.Fatal("disabled token accepted")
	}
	_, err = c.SaveUser(ctx, u.ID, UserInput{Username: "owner", Role: "administrator", Enabled: true, Password: "replacement-password-123456"}, u.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.BootstrapIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Authenticate(ctx, "", "", c.config.Security.AdminToken); err == nil {
		t.Fatal("restart restored old token")
	}
	doc, err := c.store.GetControlDocument(ctx, identityKey)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc.Payload), "replacement-password") || strings.Contains(string(doc.Payload), token) {
		t.Fatal("credentials stored in plaintext")
	}
	users, _ := c.Users(ctx)
	raw, _ := json.Marshal(users)
	if strings.Contains(string(raw), "hash") {
		t.Fatal("credential hashes exposed")
	}
}

func releaseFixture(t *testing.T, count int) (*Core, NginxRelease) {
	t.Helper()
	c, _, pool := controlFixture(t)
	ctx, now := context.Background(), c.now()
	for i := 0; i < count; i++ {
		g := model.Gateway{ID: id.New(id.Gateway), PoolID: pool.ID, Enabled: true, Status: model.GatewayReady, RendererVersion: nginxx.RendererVersion, LastSeenAt: &now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
		if err := c.store.UpsertGateway(ctx, &g); err != nil {
			t.Fatal(err)
		}
	}
	draft, err := c.SaveNginxDraft(ctx, pool.ID, NginxConfiguration{Snippets: []model.ConfigSnippet{{Scope: "http", Content: "client_max_body_size 20m;"}}}, "test", 0)
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.CreateNginxValidation(ctx, pool.ID, draft.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for gateway := range v.Results {
		if _, _, err = c.NextNginxValidation(ctx, pool.ID, gateway); err != nil {
			t.Fatal(err)
		}
		if err = c.FinishNginxValidation(ctx, pool.ID, v.ID, gateway, NginxValidationResult{Status: "SUCCEEDED", RenderedDigest: strings.Repeat("b", 64)}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := c.PublishNginx(ctx, pool.ID, v.ID, "test", "release", draft.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return c, r
}

func TestReleaseSerialFailureAndFrozenTargets(t *testing.T) {
	c, r := releaseFixture(t, 3)
	ctx := context.Background()
	claim := func(i int) *model.Snapshot {
		t.Helper()
		s, managed, err := c.ClaimNginxRelease(ctx, r.PoolID, r.Targets[i].GatewayID, r.ConfigRevision)
		if err != nil || !managed {
			t.Fatal(managed, err)
		}
		return s
	}
	if claim(1) != nil {
		t.Fatal("second target started before first")
	}
	if s := claim(0); s == nil || len(s.Snippets) != 1 {
		t.Fatal("missing frozen candidate")
	}
	if claim(0) != nil || claim(1) != nil {
		t.Fatal("duplicate or parallel dispatch")
	}
	if err := c.RecordNginxReleaseResult(ctx, r.PoolID, r.Targets[0].GatewayID, r.ConfigRevision, "SUCCEEDED", "digest", "rendered", ""); err != nil {
		t.Fatal(err)
	}
	if claim(1) == nil {
		t.Fatal("second target not released")
	}
	if err := c.RecordNginxReleaseResult(ctx, r.PoolID, r.Targets[1].GatewayID, r.ConfigRevision, "FAILED", "", "", "candidate rejected"); err != nil {
		t.Fatal(err)
	}
	if claim(2) != nil {
		t.Fatal("failure did not stop third target")
	}
	now := c.now()
	g := model.Gateway{ID: id.New(id.Gateway), PoolID: r.PoolID, Enabled: true, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := c.store.UpsertGateway(ctx, &g); err != nil {
		t.Fatal(err)
	}
	if err := c.store.AckGateway(ctx, r.Targets[1].GatewayID, r.ConfigRevision+100, "newer", "renderer", "rendered", ""); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetNginxRelease(ctx, r.PoolID, r.ID)
	if err != nil || got.Status != "PARTIAL" || len(got.Targets) != 3 || got.Targets[2].Status != "PENDING" {
		t.Fatal(got, err)
	}
}

func TestReleaseUnknownAndHistoricalSuccess(t *testing.T) {
	c, r := releaseFixture(t, 1)
	ctx := context.Background()
	if _, _, err := c.ClaimNginxRelease(ctx, r.PoolID, r.Targets[0].GatewayID, r.ConfigRevision); err != nil {
		t.Fatal(err)
	}
	now := c.now()
	c.now = func() time.Time { return now.Add(3 * time.Minute) }
	got, err := c.GetNginxRelease(ctx, r.PoolID, r.ID)
	if err != nil || got.Status != "UNKNOWN" {
		t.Fatal(got, err)
	}
	if s, _, err := c.ClaimNginxRelease(ctx, r.PoolID, r.Targets[0].GatewayID, r.ConfigRevision); err != nil || s != nil {
		t.Fatal("unknown retried", err)
	}
	if err := c.RecordNginxReleaseResult(ctx, r.PoolID, r.Targets[0].GatewayID, r.ConfigRevision, "SUCCEEDED", "digest", "rendered", ""); err != nil {
		t.Fatal(err)
	}
	got, err = c.GetNginxRelease(ctx, r.PoolID, r.ID)
	if err != nil || got.Status != "SUCCEEDED" || got.FinishedAt == nil {
		t.Fatal(got, err)
	}
	if err := c.store.AckGateway(ctx, r.Targets[0].GatewayID, r.ConfigRevision+1, "different", "renderer", "rendered", "subsequent failure"); err != nil {
		t.Fatal(err)
	}
	got, err = c.GetNginxRelease(ctx, r.PoolID, r.ID)
	if err != nil || got.Status != "SUCCEEDED" {
		t.Fatal("history rewritten", got, err)
	}
}

func TestEvidenceExpiresAndAcknowledgementDoesNotResolve(t *testing.T) {
	c, r := releaseFixture(t, 1)
	ctx := context.Background()
	gateway := r.Targets[0].GatewayID
	now := c.now()
	c.now = func() time.Time { return now }
	e := GatewayEvidence{ConfirmedRevision: r.ConfigRevision, LoadingConfirmed: true, StartupState: "CONFIRMED", RenderedDigest: "same", DiskDigest: "same", ObservedAt: now, RefreshIntervalMillis: 1000}
	if err := c.RecordGatewayEvidence(ctx, gateway, e); err != nil {
		t.Fatal(err)
	}
	fresh, err := c.GatewayEvidence(ctx, gateway)
	if err != nil || !fresh.LoadingConfirmed || fresh.LastConfirmedAt == nil {
		t.Fatal(fresh, err)
	}
	c.now = func() time.Time { return now.Add(3 * time.Second) }
	stale, err := c.GatewayEvidence(ctx, gateway)
	if err != nil || stale.LoadingConfirmed || stale.ConfigurationState != "UNKNOWN" || !stale.LastConfirmedAt.Equal(now) {
		t.Fatal(stale, err)
	}
	// Repeating an old observation must not extend its validity.
	if err = c.RecordGatewayEvidence(ctx, gateway, e); err != nil {
		t.Fatal(err)
	}
	stale, err = c.GatewayEvidence(ctx, gateway)
	if err != nil || stale.LoadingConfirmed {
		t.Fatal(stale, err)
	}
	if err = c.RefreshCommunityIssues(ctx); err != nil {
		t.Fatal(err)
	}
	issues, err := c.Issues(ctx, storage.Page{Limit: 200})
	if err != nil || len(issues) == 0 {
		t.Fatal(issues, err)
	}
	var problem Issue
	for _, issue := range issues {
		if issue.Code == "gateway_unknown" {
			problem = issue
		}
	}
	if problem.ID == "" {
		t.Fatal("missing persistent unknown issue")
	}
	if _, err = c.AcknowledgeIssue(ctx, problem.ID, problem.Revision); err != nil {
		t.Fatal(err)
	}
	if err = c.RefreshCommunityIssues(ctx); err != nil {
		t.Fatal(err)
	}
	issues, err = c.Issues(ctx, storage.Page{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.ID == problem.ID && (!issue.Active || !issue.Acknowledged || issue.Occurrences != 1) {
			t.Fatal("acknowledgement hid or duplicated the problem", issue)
		}
	}
}

func TestExternalApplicationCannotQueueUpdates(t *testing.T) {
	c, rt, _ := controlFixture(t)
	ctx := context.Background()
	app := model.Application{ID: id.New(id.Application), RuntimeID: rt.ID, Name: "external", ProjectName: "external", Status: model.ApplicationRunning, Managed: false, Metadata: model.Metadata{Revision: 1, CreatedAt: c.now(), UpdatedAt: c.now()}}
	// CreateApplication is an install transaction; insert an observation fixture.
	s := c.store.(*storage.SQLStore)
	_, err := s.DB().ExecContext(ctx, `INSERT INTO applications(id,runtime_id,name,project_name,status,managed,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, app.ID, rt.ID, app.Name, app.ProjectName, app.Status, false, 1, c.now(), c.now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.QueueApplicationOperation(ctx, app.ID, "STOP", "external-stop"); err == nil {
		t.Fatal("external application stop allowed")
	}
	if _, _, _, err = c.CreateApplicationRevision(ctx, app.ID, CreateRevisionInput{}, 1); err == nil {
		t.Fatal("external application update allowed")
	}
}

func TestApplicationRestorePreservesSecretsAndCreatesRevision(t *testing.T) {
	c, rt, _ := controlFixture(t)
	ctx := context.Background()
	value := "private-original-secret"
	vars := []applicationx.EnvironmentVariable{{Key: "TOKEN", Value: &value, Secret: true}}
	in := ApplicationPreflightInput{RuntimeID: rt.ID, ComposeYAML: "services:\n  web:\n    image: example.test/app@sha256:" + strings.Repeat("a", 64) + "\n    environment:\n      TOKEN: ${TOKEN}\n", Environment: applicationx.EnvironmentInput{Values: &vars}}
	app, op, _, err := c.InstallApplication(ctx, InstallApplicationInput{ApplicationPreflightInput: in, Name: "restore", ProjectName: "restore", IdempotencyKey: "initial", Actor: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	old := op.RevisionID
	finish := func(op model.DeploymentOperation) {
		t.Helper()
		s := c.store.(*storage.SQLStore)
		if _, e := s.DB().ExecContext(ctx, `UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?`, op.ID); e != nil {
			t.Fatal(e)
		}
		a, e := c.store.GetApplication(ctx, app.ID)
		if e != nil {
			t.Fatal(e)
		}
		a.ActiveRevisionID = op.RevisionID
		a.Status = model.ApplicationRunning
		if e = c.store.UpdateApplication(ctx, &a, a.Revision); e != nil {
			t.Fatal(e)
		}
	}
	finish(op)
	current, err := c.store.GetApplication(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	value = "private-updated-secret"
	_, op, _, err = c.CreateApplicationRevision(ctx, app.ID, CreateRevisionInput{ApplicationPreflightInput: in, IdempotencyKey: "second", Actor: "owner"}, current.Revision)
	if err != nil {
		t.Fatal(err)
	}
	finish(op)
	review, err := c.ReviewApplicationRestore(ctx, app.ID, old)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(review)
	if strings.Contains(string(public), "private-original-secret") || strings.Contains(string(public), "private-updated-secret") {
		t.Fatal("restore preview leaked secret")
	}
	if _, err = c.RestoreApplication(ctx, app.ID, review.ID, "owner", "restore", review.BaseRevision, false); err == nil {
		t.Fatal("data boundary not confirmed")
	}
	if _, err = c.ReencryptControlSecrets(ctx); err != nil {
		t.Fatal(err)
	}
	op, err = c.RestoreApplication(ctx, app.ID, review.ID, "owner", "restore", review.BaseRevision, true)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := c.store.GetApplicationRevision(ctx, op.RevisionID)
	if err != nil || revision.ID == old || revision.ConfigurationSource.RestoredFrom != old {
		t.Fatal(revision, err)
	}
	plain, _, err := c.keyring.Decrypt(revision.EncryptedEnvironment, revisionAAD(revision.ID, "environment"))
	if err != nil || !strings.Contains(string(plain), "private-original-secret") {
		t.Fatal("historical environment not restored", err)
	}
}
