package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func controlFixture(t *testing.T) (*Core, model.Runtime, model.GatewayPool) {
	t.Helper()
	s, e := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "control.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	if e = s.Migrate(context.Background()); e != nil {
		t.Fatal(e)
	}
	cfg := config.Defaults()
	cfg.Management.Enabled = true
	ring, e := security.NewKeyring("v1", map[string]string{"v1": base64.StdEncoding.EncodeToString(make([]byte, 32))})
	if e != nil {
		t.Fatal(e)
	}
	c := New(s, cfg, ring)
	p, e := c.CreatePool(context.Background(), CreatePoolInput{Name: "control", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080"})
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	r := model.Runtime{ID: id.New(id.Runtime), PoolID: p.ID, Name: "docker", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, ManagementEnabled: true, Status: model.RuntimeOnline, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if e = s.CreateRuntime(context.Background(), &r); e != nil {
		t.Fatal(e)
	}
	return c, r, p
}
func pointer(s string) *string { return &s }
func TestVariableLayersSecretsAndOptimisticHistory(t *testing.T) {
	c, rt, _ := controlFixture(t)
	ctx := context.Background()
	p, e := c.CreateProject(ctx, "Example", "example")
	if e != nil {
		t.Fatal(e)
	}
	dev, e := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "dev", RuntimeID: rt.ID})
	if e != nil {
		t.Fatal(e)
	}
	prod, e := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "prod", RuntimeID: rt.ID})
	if e != nil {
		t.Fatal(e)
	}
	if dev.ProjectName == prod.ProjectName {
		t.Fatal("environments share a deployment name")
	}
	public, e := c.SaveVariables(ctx, p.ID, VariableInput{Values: []Variable{{Key: "TOKEN", Value: pointer("private-value"), Secret: true}, {Key: "EMPTY", Value: pointer("default")}, {Key: "MASKED", Value: pointer("parent")}}}, 0)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range public.Values {
		if v.Key == "TOKEN" && v.Value != nil {
			t.Fatal("secret returned")
		}
	}
	if _, e = c.SaveVariables(ctx, p.ID, VariableInput{Values: []Variable{}}, 0); !errors.Is(e, storage.ErrPrecondition) {
		t.Fatalf("stale write: %v", e)
	}
	_, e = c.SaveVariables(ctx, dev.ID, VariableInput{Values: []Variable{{Key: "TOKEN", Value: pointer("override")}, {Key: "EMPTY", Value: pointer("")}, {Key: "MASKED", Mask: true}}}, 0)
	if e != nil {
		t.Fatal(e)
	}
	common, _ := c.variables(ctx, p.ID)
	local, _ := c.variables(ctx, dev.ID)
	merged, preview := MergeVariables(common.Values, local.Values)
	byKey := map[string]int{}
	for i, v := range merged {
		byKey[v.Key] = i
	}
	if *merged[byKey["EMPTY"]].Value != "" || !merged[byKey["MASKED"]].Clear || !merged[byKey["TOKEN"]].Secret {
		t.Fatalf("bad merge: %#v", merged)
	}
	for _, v := range preview {
		if v.Key == "TOKEN" && v.Value != nil {
			t.Fatal("inherited secret exposed")
		}
	}
	docs, e := c.store.ListControlDocuments(ctx, "variable-version", p.ID, storage.Page{Limit: 50})
	if e != nil || len(docs) != 1 {
		t.Fatal(docs, e)
	}
	if strings.Contains(string(docs[0].Payload), "private-value") {
		t.Fatal("plaintext secret persisted")
	}
	_, e = c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "dev", RuntimeID: rt.ID})
	if !errors.Is(e, storage.ErrConflict) {
		t.Fatalf("duplicate environment: %v", e)
	}
}
func TestEnvironmentInstallCommitsLinkageAndPinnedSource(t *testing.T) {
	c, rt, _ := controlFixture(t)
	ctx := context.Background()
	p, _ := c.CreateProject(ctx, "App", "app")
	v, e := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "test", RuntimeID: rt.ID})
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.SaveVariables(ctx, p.ID, VariableInput{Values: []Variable{{Key: "VALUE", Value: pointer("one")}}}, 0)
	if e != nil {
		t.Fatal(e)
	}
	compose := "services:\n  web:\n    image: registry.example.com/demo@sha256:" + strings.Repeat("a", 64) + "\n    environment:\n      VALUE: ${VALUE}\n"
	op, e := c.DeployEnvironment(ctx, v.ID, compose, "test", "deploy-key", v.Revision)
	if e != nil {
		t.Fatal(e)
	}
	linked, e := c.GetEnvironment(ctx, v.ID)
	if e != nil || linked.ApplicationID != op.ApplicationID || linked.Revision != v.Revision+1 {
		t.Fatal(linked, e)
	}
	rev, e := c.store.GetApplicationRevision(ctx, op.RevisionID)
	if e != nil || rev.ConfigurationSource.PublicVariablesVersion != 1 || len(rev.ConfigurationSource.Images) != 1 {
		t.Fatal(rev, e)
	}
	raw, _, e := c.keyring.Decrypt(rev.EncryptedResolved, revisionAAD(rev.ID, "resolved"))
	if e != nil || !strings.Contains(string(raw), "@sha256:") {
		t.Fatal(string(raw), e)
	}
	_, e = c.DeployEnvironment(ctx, v.ID, compose, "test", "different-key", v.Revision)
	if !errors.Is(e, storage.ErrPrecondition) {
		t.Fatalf("stale environment deploy: %v", e)
	}
}
func TestWebhookSignatureReplayAndSharedQueue(t *testing.T) {
	c, rt, _ := controlFixture(t)
	ctx := context.Background()
	p, _ := c.CreateProject(ctx, "Hooks", "hooks")
	v, _ := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "dev", RuntimeID: rt.ID})
	if e := c.saveControl(ctx, v.ID+":image-policy", "image-policy", v.ID, ImagePolicy{Enabled: true, Webhook: true, IntervalSeconds: 300, Services: []string{"web"}}, 0); e != nil {
		t.Fatal(e)
	}
	h, secret, e := c.RotateHook(ctx, v.ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	body := []byte(`{"services":["web"]}`)
	timestamp := strconv.FormatInt(c.now().Unix(), 10)
	sign := func(event string) string {
		m := hmac.New(sha256.New, []byte(secret))
		m.Write([]byte(timestamp + "." + event + "."))
		m.Write(body)
		return hex.EncodeToString(m.Sum(nil))
	}
	if _, e = c.ReceiveImageHook(ctx, h.HookID, timestamp, "first", "bad", body); e == nil {
		t.Fatal("invalid signature accepted")
	}
	check, e := c.ReceiveImageHook(ctx, h.HookID, timestamp, "first", sign("first"), body)
	if e != nil || !check.Requested {
		t.Fatal(check, e)
	}
	if _, e = c.ReceiveImageHook(ctx, h.HookID, timestamp, "first", sign("first"), body); !errors.Is(e, storage.ErrConflict) {
		t.Fatalf("replay: %v", e)
	}
	next, e := c.ReceiveImageHook(ctx, h.HookID, timestamp, "second", sign("second"), body)
	if e != nil || next.ID != check.ID {
		t.Fatal(next, e)
	}
	if _, _, e = c.RotateHook(ctx, v.ID, 1); e != nil {
		t.Fatal(e)
	}
	if _, e = c.ReceiveImageHook(ctx, h.HookID, timestamp, "third", sign("third"), body); e == nil {
		t.Fatal("rotated secret accepted")
	}
}
func TestNginxValidationPublishConflictAndSandboxIdentity(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	now := c.now()
	g := model.Gateway{ID: id.New(id.Gateway), PoolID: pool.ID, Name: "production", Enabled: true, Status: model.GatewayReady, RendererVersion: nginxx.RendererVersion, LastSeenAt: &now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if e := c.store.UpsertGateway(ctx, &g); e != nil {
		t.Fatal(e)
	}
	draft, e := c.SaveNginxDraft(ctx, pool.ID, NginxConfiguration{Snippets: []model.ConfigSnippet{{Scope: "http", Content: "client_max_body_size 20m;"}}, Description: "body size"}, "test", 0)
	if e != nil {
		t.Fatal(e)
	}
	task, e := c.CreateNginxValidation(ctx, pool.ID, draft.Revision)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.PublishNginx(ctx, pool.ID, task.ID, "test", "publish", draft.Revision); e == nil {
		t.Fatal("unvalidated configuration published")
	}
	pending, snapshot, e := c.NextNginxValidation(ctx, pool.ID, g.ID)
	if e != nil || pending == nil || len(snapshot.Snippets) != 1 {
		t.Fatal(pending, snapshot, e)
	}
	if e = c.FinishNginxValidation(ctx, pool.ID, task.ID, g.ID, NginxValidationResult{Status: "SUCCEEDED", RenderedDigest: strings.Repeat("b", 64)}); e != nil {
		t.Fatal(e)
	}
	release, e := c.PublishNginx(ctx, pool.ID, task.ID, "test", "publish", draft.Revision)
	if e != nil {
		t.Fatal(e)
	}
	retry, e := c.PublishNginx(ctx, pool.ID, task.ID, "test", "publish", draft.Revision)
	if e != nil || retry.ID != release.ID {
		t.Fatal(retry, e)
	}
	actual, e := c.store.BuildSnapshot(ctx, pool.ID)
	if e != nil || actual.Revision != release.ConfigRevision || len(actual.Snippets) != 1 {
		t.Fatal(actual, e)
	}
	if _, e = c.SaveSandbox(ctx, pool.ID, SandboxSettings{GatewayID: g.ID}, 1); e == nil {
		t.Fatal("production gateway converted to sandbox")
	}
	sb := id.New(id.Gateway)
	settings, e := c.SaveSandbox(ctx, pool.ID, SandboxSettings{GatewayID: sb, Checks: []SandboxCheck{}}, 1)
	if e != nil {
		t.Fatal(e)
	}
	mode, e := c.GatewayMode(ctx, sb, pool.ID)
	if e != nil || mode != "sandbox" {
		t.Fatal(mode, e)
	}
	if _, e = c.CreateNginxValidation(ctx, pool.ID, draft.Revision+1); e == nil {
		t.Fatal("offline sandbox allowed")
	}
	raw, _ := json.Marshal(settings)
	if !strings.Contains(string(raw), sb) {
		t.Fatal("sandbox metadata missing")
	}
}

func TestReviewedEnvironmentFreezesAndRejectsNewVariables(t *testing.T) {
	c, rt, _ := controlFixture(t)
	ctx := context.Background()
	p, _ := c.CreateProject(ctx, "Reviewed", "reviewed")
	env, _ := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "dev", RuntimeID: rt.ID})
	_, e := c.SaveVariables(ctx, p.ID, VariableInput{Values: []Variable{{Key: "TOKEN", Value: pointer("secret-one"), Secret: true}}}, 0)
	if e != nil {
		t.Fatal(e)
	}
	compose := "services:\n  web:\n    image: example.test/app@sha256:" + strings.Repeat("a", 64) + "\n    environment:\n      TOKEN: ${TOKEN}\n"
	review, e := c.ReviewEnvironment(ctx, env.ID, compose)
	if e != nil {
		t.Fatal(e)
	}
	reviewID := review["preflight_id"].(string)
	raw, _ := json.Marshal(review)
	if strings.Contains(string(raw), "secret-one") {
		t.Fatal("review exposed secret")
	}
	_, e = c.SaveVariables(ctx, p.ID, VariableInput{Values: []Variable{{Key: "TOKEN", Value: pointer("secret-two"), Secret: true}}}, 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.DeployReviewedEnvironment(ctx, env.ID, reviewID, "test", "stale-review", env.Revision); !errors.Is(e, storage.ErrPrecondition) {
		t.Fatalf("changed variables accepted: %v", e)
	}
	review, e = c.ReviewEnvironment(ctx, env.ID, compose)
	if e != nil {
		t.Fatal(e)
	}
	reviewID = review["preflight_id"].(string)
	op, e := c.DeployReviewedEnvironment(ctx, env.ID, reviewID, "test", "reviewed-install", env.Revision)
	if e != nil {
		t.Fatal(e)
	}
	retry, e := c.DeployReviewedEnvironment(ctx, env.ID, reviewID, "test", "reviewed-install", env.Revision)
	if e != nil || retry.ID != op.ID {
		t.Fatal(retry, e)
	}
	revision, e := c.store.GetApplicationRevision(ctx, op.RevisionID)
	if e != nil || revision.ConfigurationSource.PublicVariablesVersion != 2 {
		t.Fatal(revision, e)
	}
	if _, e = c.ReencryptControlSecrets(ctx); e != nil {
		t.Fatal(e)
	}
	variables, e := c.GetVariables(ctx, p.ID)
	if e != nil || variables.Revision != 2 {
		t.Fatal("key rotation changed configuration revision", e)
	}
}
func TestPublicSecretNeverExposedByEnvironmentLayer(t *testing.T) {
	c, rt, _ := controlFixture(t)
	ctx := context.Background()
	p, _ := c.CreateProject(ctx, "Secret", "secret")
	env, _ := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "dev", RuntimeID: rt.ID})
	c.SaveVariables(ctx, p.ID, VariableInput{Values: []Variable{{Key: "TOKEN", Value: pointer("public-secret"), Secret: true}}}, 0)
	got, e := c.SaveVariables(ctx, env.ID, VariableInput{Values: []Variable{{Key: "TOKEN", Value: pointer("local-secret")}}}, 0)
	if e != nil {
		t.Fatal(e)
	}
	if !got.Values[0].Secret || got.Values[0].Value != nil {
		t.Fatal("inherited secret returned in plaintext")
	}
}
func TestInterruptedImageSubmissionIsQuarantined(t *testing.T) {
	c, rt, _ := controlFixture(t)
	ctx := context.Background()
	p, _ := c.CreateProject(ctx, "Recovery", "recovery")
	env, _ := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "dev", RuntimeID: rt.ID})
	u := ImageUpdate{EnvironmentID: env.ID, Status: "SUBMITTING"}
	if e := c.saveControl(ctx, "img_interrupted", "image-update", env.ID, u, 0); e != nil {
		t.Fatal(e)
	}
	now := c.now()
	c.now = func() time.Time { return now.Add(3 * time.Minute) }
	c.recoverImageSubmissions(ctx, env)
	values, e := c.ListImageUpdates(ctx, env.ID, storage.Page{Limit: 50})
	if e != nil || len(values) != 1 || values[0].Status != "FAILED" {
		t.Fatal(values, e)
	}
}
func TestExplicitEnvironmentResources(t *testing.T) {
	resources := isolationResources("services:\n  web:\n    image: example.test/web:fixed\n    ports: [\"127.0.0.1:8080:80\"]\nvolumes:\n  isolated: {}\n  named:\n    name: shared-data\nnetworks:\n  shared:\n    external: true\n")
	if len(resources) != 3 {
		t.Fatal(resources)
	}
}
