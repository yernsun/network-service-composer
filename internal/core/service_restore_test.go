package core

import (
	"context"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/cluster"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/runtime/engine/enginetest"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestServiceRestoreReviewSelectsOnlyTarget(t *testing.T) {
	c, _, env := environmentBoundaryFixture(t)
	client := &enginetest.Client{System: system.Info{}}
	c.Cluster = cluster.NewWithFactory(c.store, c.config, func(model.Runtime, engine.TLSFiles) (engine.MonitoringClient, error) { return client, nil })
	ctx := context.Background()
	compose := "services:\n  web:\n    image: example.test/web@sha256:" + strings.Repeat("a", 64) +
		"\n  api:\n    image: example.test/api@sha256:" + strings.Repeat("b", 64) + "\n"
	review, err := c.ReviewEnvironment(ctx, env.ID, compose)
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.DeployReviewedEnvironment(ctx, env.ID, review["preflight_id"].(string), "author", "install-two", env.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.store.(*storage.SQLStore).DB().ExecContext(ctx, `UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?`, op.ID); err != nil {
		t.Fatal(err)
	}
	app, err := c.store.GetApplication(ctx, op.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	app.ActiveRevisionID, app.Status = op.RevisionID, model.ApplicationRunning
	if err = c.store.UpdateApplication(ctx, &app, app.Revision); err != nil {
		t.Fatal(err)
	}
	app, err = c.store.GetApplication(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	services, err := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
	if err != nil || len(services) != 2 {
		t.Fatal("expected two owned services", err)
	}
	var target, other model.Service
	for _, service := range services {
		if service.Name == "web" {
			target = service
		} else {
			other = service
		}
	}
	if target.ID == "" || other.ID == "" {
		t.Fatal("service identities missing")
	}
	changed := "services:\n  web:\n    image: example.test/web@sha256:" + strings.Repeat("c", 64) +
		"\n  api:\n    image: example.test/api@sha256:" + strings.Repeat("d", 64) + "\n"
	secondReview, err := c.ReviewEnvironment(ctx, env.ID, changed)
	if err != nil {
		t.Fatal(err)
	}
	env, err = c.GetEnvironment(ctx, env.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.DeployReviewedEnvironment(ctx, env.ID, secondReview["preflight_id"].(string), "author", "install-changed", env.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.store.(*storage.SQLStore).DB().ExecContext(ctx, `UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?`, second.ID); err != nil {
		t.Fatal(err)
	}
	app, err = c.store.GetApplication(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	app.ActiveRevisionID, app.Status = second.RevisionID, model.ApplicationRunning
	if err = c.store.UpdateApplication(ctx, &app, app.Revision); err != nil {
		t.Fatal(err)
	}
	app, err = c.store.GetApplication(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := c.ReviewServiceRestore(ctx, target.ID, op.RevisionID)
	if err != nil {
		t.Fatal("service review failed", err)
	}
	if !strings.Contains(selected.Preview.ResolvedYAML, strings.Repeat("d", 64)) || strings.Contains(selected.Preview.ResolvedYAML, strings.Repeat("b", 64)) {
		t.Fatalf("restore preview replaced an unrelated service image: %s", selected.Preview.ResolvedYAML)
	}
	var saved savedApplicationRestore
	if _, err = c.readEncrypted(ctx, selected.ID, "application-restore", &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Source == nil || saved.ServiceID != target.ID || saved.Source.ServiceScope == nil || len(saved.Source.ServiceScope.Names) != 1 || saved.Source.ServiceScope.Names[0] != "web" ||
		!strings.Contains(saved.Input.ComposeYAML, strings.Repeat("a", 64)) ||
		!strings.Contains(saved.Input.ComposeYAML, strings.Repeat("d", 64)) ||
		strings.Contains(saved.Input.ComposeYAML, strings.Repeat("b", 64)) {
		t.Fatal("service review did not preserve current Compose source and target identity")
	}
	if _, err = c.RestoreService(ctx, other.ID, selected.ID, "author", "foreign-service", app.Revision, true); err == nil {
		t.Fatal("foreign service accepted target review")
	}
	if _, err = c.RestoreService(ctx, target.ID, selected.ID, "author", "no-data-ack", app.Revision, false); err == nil {
		t.Fatal("missing data boundary acknowledgement accepted")
	}
	restored, err := c.RestoreService(ctx, target.ID, selected.ID, "author", "restore-web", app.Revision, true)
	if err != nil {
		t.Fatal("selected service restore failed", err)
	}
	revision, err := c.store.GetApplicationRevision(ctx, restored.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _, err := c.keyring.Decrypt(revision.EncryptedResolved, revisionAAD(revision.ID, "resolved"))
	if err != nil || !strings.Contains(string(resolved), strings.Repeat("d", 64)) || strings.Contains(string(resolved), strings.Repeat("b", 64)) {
		t.Fatal("published revision changed unrelated service image", err)
	}
	if _, err = c.store.(*storage.SQLStore).DB().ExecContext(ctx, `UPDATE services SET revision=revision+1 WHERE id=?`, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.RestoreService(ctx, target.ID, selected.ID, "author", "stale-web", app.Revision, true); err == nil {
		t.Fatal("stale service review was accepted")
	}
}
