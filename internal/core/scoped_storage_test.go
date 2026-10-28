package core

import (
	"strings"
	"testing"
)

func TestProjectReviewRejectsHostStorageAndFreezesRestriction(t *testing.T) {
	c, rt, _ := controlFixture(t)
	ctx := t.Context()
	p, err := c.CreateProject(ctx, "Scoped storage", "scoped-storage")
	if err != nil {
		t.Fatal(err)
	}
	env, err := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "test", RuntimeID: rt.ID})
	if err != nil {
		t.Fatal(err)
	}
	compose := "services:\n  web:\n    image: example.test/app@sha256:" + strings.Repeat("a", 64) + "\n    volumes: [/srv/application:/data:ro]\n"
	if _, err := c.ReviewEnvironmentInput(ctx, env.ID, ComposeValidationInput{Compose: compose}, true); err == nil {
		t.Fatal("legacy environment key review permitted host access")
	}
	if _, err := c.ReviewEnvironmentInputScoped(ctx, env.ID, ComposeValidationInput{Compose: compose}, []string{"services.create", "services.deploy"}); err == nil || !strings.Contains(err.Error(), "project API keys") {
		t.Fatalf("project review permitted host access: %v", err)
	}
	compose = strings.ReplaceAll(compose, "[/srv/application:/data:ro]", "[data:/data]") + "volumes:\n  data: {}\n"
	consoleReview, err := c.ReviewEnvironmentInput(ctx, env.ID, ComposeValidationInput{Compose: compose})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeployReviewedEnvironment(ctx, env.ID, consoleReview["preflight_id"].(string), "fixture", "unrestricted-review", env.Revision, true); err == nil {
		t.Fatal("deployment key accepted a review without frozen host restriction")
	}
	if _, err := c.ReviewEnvironmentInputScoped(ctx, env.ID, ComposeValidationInput{Compose: compose}, nil); err == nil {
		t.Fatal("nil project scopes became console authority")
	}
	review, err := c.ReviewEnvironmentInputScoped(ctx, env.ID, ComposeValidationInput{Compose: compose}, []string{"services.create", "services.deploy"})
	if err != nil {
		t.Fatal(err)
	}
	var frozen environmentReview
	if _, err := c.readEncrypted(ctx, review["preflight_id"].(string), "environment-review", &frozen); err != nil {
		t.Fatal(err)
	}
	if !frozen.Source.RestrictedHostAccess {
		t.Fatal("host access policy missing from immutable review")
	}
	op, err := c.DeployReviewedEnvironmentScoped(ctx, env.ID, review["preflight_id"].(string), "fixture", "restricted-storage", env.Revision, []string{"services.create", "services.deploy"})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := c.store.GetApplicationRevision(ctx, op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if !revision.ConfigurationSource.RestrictedHostAccess {
		t.Fatal("review lost host access policy when queuing execution")
	}
}
