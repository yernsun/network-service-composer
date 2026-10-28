package core

import (
	"context"
	"testing"

	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestEnvironmentCatalogReviewPinsImmutableVersion(t *testing.T) {
	c, _, env := environmentBoundaryFixture(t)
	ctx := context.Background()
	if err := c.EnsureBuiltinCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	versions, err := c.store.ListCatalogAppVersions(ctx, "", "", storage.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var versionID string
	for _, value := range versions {
		if value.AppKey == "nginx" {
			versionID = value.ID
			break
		}
	}
	if versionID == "" {
		t.Fatal("built-in catalog missing nginx")
	}
	if _, err := c.ReviewEnvironmentInput(ctx, env.ID, ComposeValidationInput{CatalogAppVersionID: versionID, Compose: "services: {}"}); err == nil {
		t.Fatal("catalog content override accepted")
	}
	review, err := c.ReviewEnvironmentInput(ctx, env.ID, ComposeValidationInput{CatalogAppVersionID: versionID})
	if err != nil {
		t.Fatal("catalog review failed", err)
	}
	id, ok := review["preflight_id"].(string)
	if !ok || id == "" {
		t.Fatal("catalog review missing frozen ID")
	}
	op, err := c.DeployReviewedEnvironment(ctx, env.ID, id, "author", "catalog-install", env.Revision)
	if err != nil {
		t.Fatal("catalog deployment was not queued", err)
	}
	app, err := c.store.GetApplication(ctx, op.ApplicationID)
	if err != nil || app.CatalogAppVersionID != versionID {
		t.Fatal("installed application lost catalog provenance", err)
	}
	version, err := c.store.GetCatalogAppVersion(ctx, versionID)
	if err != nil {
		t.Fatal(err)
	}
	version.Withdrawn = true
	c.store = &applicationCatalogStore{Store: c.store, version: version}
	if _, err = c.ReviewEnvironmentInput(ctx, env.ID, ComposeValidationInput{CatalogAppVersionID: versionID}); err == nil {
		t.Fatal("withdrawn catalog version reviewed")
	}
}
