package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func imageEnvironmentFixture(t *testing.T) (*Core, Environment) {
	t.Helper()
	compose := "services:\n  web:\n    image: registry.example.test/web@sha256:" + strings.Repeat("a", 64) + "\n  api:\n    image: registry.example.test/api@sha256:" + strings.Repeat("b", 64) + "\n"
	return activeImageEnvironment(t, compose, nil)
}

func activeImageEnvironment(t *testing.T, compose string, setup func(*Core, *EnvironmentInput)) (*Core, Environment) {
	t.Helper()
	c, rt, _ := controlFixture(t)
	ctx := context.Background()
	p, err := c.CreateProject(ctx, "Images", "images")
	if err != nil {
		t.Fatal(err)
	}
	input := EnvironmentInput{Name: "dev", RuntimeID: rt.ID}
	if setup != nil {
		setup(c, &input)
	}
	env, err := c.CreateEnvironment(ctx, p.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.DeployEnvironment(ctx, env.ID, compose, "fixture", "install", env.Revision)
	if err != nil {
		t.Fatal(err)
	}
	// Seed the state an executor has confirmed; the test never contacts Docker.
	if _, err := c.store.(*storage.SQLStore).DB().ExecContext(ctx, `UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?`, op.ID); err != nil {
		t.Fatal(err)
	}
	app, err := c.store.GetApplication(ctx, op.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	app.ActiveRevisionID = op.RevisionID
	app.DesiredRevisionID = op.RevisionID
	app.Status = model.ApplicationRunning
	if err := c.store.UpdateApplication(ctx, &app, app.Revision); err != nil {
		t.Fatal(err)
	}
	env, err = c.GetEnvironment(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	return c, env
}

func TestImagePolicyDefaultsAndValidationBoundaries(t *testing.T) {
	c, env := imageEnvironmentFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		automatic bool
	}{{"dev", false}, {"test", false}, {"prod", false}} {
		v := env
		if tc.name != "dev" {
			var err error
			v, err = c.CreateEnvironment(ctx, env.ProjectID, EnvironmentInput{Name: tc.name, RuntimeID: env.RuntimeID})
			if err != nil {
				t.Fatal(err)
			}
		}
		p, err := c.GetImagePolicy(ctx, v.ID)
		if err != nil || p.Enabled || p.Automatic != tc.automatic || p.IntervalSeconds != 300 || p.Revision != 0 {
			t.Fatalf("default policy for %s=%#v %v", tc.name, p, err)
		}
	}
	for _, p := range []ImagePolicy{
		{IntervalSeconds: 59}, {IntervalSeconds: 86401},
		{Enabled: true, IntervalSeconds: 300, Services: []string{"web"}},
		{Enabled: true, Poll: true, IntervalSeconds: 300},
		{Enabled: true, Poll: true, IntervalSeconds: 300, Services: []string{"web", "web"}},
		{Enabled: true, Webhook: true, IntervalSeconds: 300, Services: []string{"missing"}},
	} {
		if _, err := c.SaveImagePolicy(ctx, env.ID, p, 0); err == nil {
			t.Fatalf("unsafe policy accepted: %#v", p)
		}
	}
	current, err := c.GetImagePolicy(ctx, env.ID)
	if err != nil || current.Revision != 0 {
		t.Fatal("rejected policy persisted")
	}
	for _, interval := range []int{0, 60, 86400} {
		p, err := c.SaveImagePolicy(ctx, env.ID, ImagePolicy{Enabled: true, Poll: true, Webhook: true, IntervalSeconds: interval, Services: []string{"web", "api"}}, current.Revision)
		if err != nil {
			t.Fatal(err)
		}
		want := interval
		if want == 0 {
			want = 300
		}
		if p.IntervalSeconds != want || strings.Join(p.Services, ",") != "api,web" || p.Revision != current.Revision+1 {
			t.Fatalf("saved policy not normalized/versioned: %#v", p)
		}
		current = p
	}
	if _, err := c.SaveImagePolicy(ctx, env.ID, ImagePolicy{}, 1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale policy overwrite=%v", err)
	}
	app, err := c.store.GetApplication(ctx, env.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	app.Managed = false
	if err := c.store.UpdateApplication(ctx, &app, app.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SaveImagePolicy(ctx, env.ID, ImagePolicy{Enabled: true, Poll: true, Services: []string{"web"}}, current.Revision); err == nil {
		t.Fatal("external application acquired automatic image updates")
	}
}

func TestImagePolicyPreservesExplicitAutomationAndNormalizesLegacyEmptyServices(t *testing.T) {
	c, env := imageEnvironmentFixture(t)
	ctx := context.Background()
	// An explicit persisted choice survives the new manual-only defaults.
	value, err := c.SaveImagePolicy(ctx, env.ID, ImagePolicy{Automatic: true}, 0)
	if err != nil || !value.Automatic || value.Enabled || value.Services == nil {
		t.Fatalf("saved opt-in or empty-service contract lost: %+v %v", value, err)
	}
	value, err = c.GetImagePolicy(ctx, env.ID)
	if err != nil || !value.Automatic || value.Services == nil || len(value.Services) != 0 {
		t.Fatalf("legacy null services were not normalized: %+v %v", value, err)
	}
}

func TestImageWebhookAuthenticationBodyAndQueueBoundaries(t *testing.T) {
	c, env := imageEnvironmentFixture(t)
	ctx := context.Background()
	if _, err := c.GetHook(ctx, "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("unknown hook environment=%v", err)
	}
	empty, err := c.GetHook(ctx, env.ID)
	if err != nil || empty.HookID != "" || empty.EnvironmentID != env.ID {
		t.Fatalf("unconfigured hook=%#v %v", empty, err)
	}
	if _, err := c.ReceiveImageHook(ctx, strings.TrimPrefix(env.ID, "env_"), "0", "event", "", nil); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("disabled hook visible: %v", err)
	}
	policy, err := c.SaveImagePolicy(ctx, env.ID, ImagePolicy{Enabled: true, Poll: true, Services: []string{"web"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReceiveImageHook(ctx, strings.TrimPrefix(env.ID, "env_"), "0", "event", "", nil); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("poll-only hook visible: %v", err)
	}
	policy.Webhook = true
	if _, err := c.SaveImagePolicy(ctx, env.ID, policy, policy.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReceiveImageHook(ctx, strings.TrimPrefix(env.ID, "env_"), "0", "event", "", nil); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing hook secret=%v", err)
	}
	hook, secret, err := c.RotateHook(ctx, env.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	public, err := json.Marshal(hook)
	if err != nil || strings.Contains(string(public), secret) || len(hook.EncryptedSecret) != 0 {
		t.Fatal("hook read exposed secret")
	}
	if _, _, err := c.RotateHook(ctx, env.ID, 0); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("stale secret rotation=%v", err)
	}
	now := c.now()
	c.now = func() time.Time { return now }
	sign := func(timestamp, event string, body []byte) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(timestamp + "." + event + "."))
		mac.Write(body)
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	for _, tc := range []struct{ name, timestamp, event, body, signature string }{
		{"bad timestamp", "not-time", "event", "", ""},
		{"old timestamp", strconv.FormatInt(now.Add(-6*time.Minute).Unix(), 10), "event", "", ""},
		{"future timestamp", strconv.FormatInt(now.Add(6*time.Minute).Unix(), 10), "event", "", ""},
		{"empty event", timestamp, "", "", ""}, {"long event", timestamp, strings.Repeat("e", 129), "", ""},
		{"invalid signature encoding", timestamp, "event", "", "not-hex"}, {"wrong signature", timestamp, "event", "", strings.Repeat("0", 64)},
		{"unknown field", timestamp, "unknown-field", `{"extra":true}`, "sign"},
		{"trailing JSON", timestamp, "trailing", `{} {}`, "sign"},
		{"invalid JSON", timestamp, "invalid", `{`, "sign"},
		{"untracked service", timestamp, "untracked", `{"services":["api"]}`, "sign"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signature := tc.signature
			if signature == "sign" {
				signature = sign(tc.timestamp, tc.event, []byte(tc.body))
			}
			if _, err := c.ReceiveImageHook(ctx, hook.HookID, tc.timestamp, tc.event, signature, []byte(tc.body)); err == nil {
				t.Fatal("invalid webhook accepted")
			}
			state, err := c.GetImageCheck(ctx, env.ID)
			if err != nil || state.Requested || state.Revision != 0 {
				t.Fatalf("rejected hook changed request queue: %#v %v", state, err)
			}
		})
	}
	for i, body := range []string{"", `{}`, `{"services":["web"]}`} {
		event := "valid-" + strconv.Itoa(i)
		state, err := c.ReceiveImageHook(ctx, hook.HookID, timestamp, event, sign(timestamp, event, []byte(body)), []byte(body))
		if err != nil || !state.Requested {
			t.Fatalf("valid signed hook not queued: %#v %v", state, err)
		}
	}
	before, err := c.GetImageCheck(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	requested, err := c.RequestImageCheck(ctx, env.ID)
	if err != nil || !requested.Requested || requested.ID != before.ID {
		t.Fatalf("manual check did not share durable queue: %#v %v", requested, err)
	}
	c.config.Management.Enabled = false
	if _, err := c.RequestImageCheck(ctx, env.ID); err == nil {
		t.Fatal("disabled management accepted check")
	}
	if _, _, err := c.RotateHook(ctx, env.ID, hook.Revision); err == nil {
		t.Fatal("disabled management rotated hook")
	}
}
