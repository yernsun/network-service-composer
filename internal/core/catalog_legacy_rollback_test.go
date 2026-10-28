package core

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/marketplace"
	"github.com/yernsun/network-service-composer/internal/model"
)

func legacyNGINXCatalogSnapshotForRollback(t *testing.T, c *Core, sourceID string) model.CatalogAppVersion {
	t.Helper()
	compose, err := builtinCatalogFiles.ReadFile("builtin/nginx/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// These immutable installation inputs are the original Controller's NGINX
	// snapshot. Its initializer never attached a marketplace document snapshot.
	files := map[string]string{
		"docker/docker-compose.yml": string(compose),
		"docker/.env.example":       "",
		"nsc-app.yaml":              "name: NGINX\nversion: 1.31.5\ndescription: Minimal NGINX web server\n",
		"README.md":                 "# NGINX\n\nA minimal built-in application for validating managed deployments.\n",
	}
	artifact := catalogArtifactDigest(files["docker/docker-compose.yml"], "", files["nsc-app.yaml"], files["README.md"])
	version, err := c.catalogVersionFromFiles(sourceID, "nginx", "NGINX", "Minimal NGINX web server", "1.31.5", "builtin", artifact, "", files, c.now())
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func TestBuiltinCatalogPreservesLegacyNGINXForBinaryRollback(t *testing.T) {
	c, _, _ := controlFixture(t)
	ctx := context.Background()
	now := c.now()
	source := model.CatalogSource{ID: id.New(id.CatalogSource), Name: builtinCatalogName, Type: model.CatalogBuiltin,
		Trusted: true, Enabled: true, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := c.store.CreateCatalogSource(ctx, &source); err != nil {
		t.Fatal(err)
	}
	legacy := legacyNGINXCatalogSnapshotForRollback(t, c, source.ID)
	if err := c.store.UpsertCatalogAppVersion(ctx, &legacy); err != nil {
		t.Fatal(err)
	}
	before, err := c.store.GetCatalogAppVersion(ctx, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.MarketJSON != "{}" {
		t.Fatalf("legacy NGINX unexpectedly contains marketplace metadata: %q", before.MarketJSON)
	}
	for range 2 {
		if err := c.EnsureBuiltinCatalog(ctx); err != nil {
			t.Fatal(err)
		}
		after, err := c.store.GetCatalogAppVersion(ctx, legacy.ID)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("candidate bootstrap rewrote the historical NGINX snapshot: before=%+v after=%+v err=%v", before, after, err)
		}
	}
	// Replay the original binary's empty MarketJSON request after the candidate
	// initialized the catalog; rollback must still accept that immutable version.
	replay := legacyNGINXCatalogSnapshotForRollback(t, c, source.ID)
	if err := c.store.UpsertCatalogAppVersion(ctx, &replay); err != nil {
		t.Fatal("original Controller cannot reinitialize its NGINX snapshot", err)
	}
	after, err := c.store.GetCatalogAppVersion(ctx, legacy.ID)
	if err != nil || replay.ID != before.ID || !reflect.DeepEqual(before, after) {
		t.Fatalf("legacy replay replaced the historical snapshot: replay=%+v after=%+v err=%v", replay, after, err)
	}

	manifestRaw, err := builtinCatalogFiles.ReadFile("builtin/market.yaml")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := marketplace.Decode(manifestRaw)
	if err != nil || len(manifest.Apps) != 7 {
		t.Fatalf("static market lost the seven official applications: %d %v", len(manifest.Apps), err)
	}
	snapshots, _, err := builtinMarketSnapshotsFrom(builtinCatalogFiles)
	if err != nil {
		t.Fatal(err)
	}
	var nginx marketplace.Snapshot
	if err := json.Unmarshal([]byte(snapshots["nginx"]), &nginx); err != nil {
		t.Fatal("static NGINX marketplace snapshot unavailable", err)
	}
	for _, key := range []string{"readme", "operations"} {
		file := nginx.Documents[key].Files[manifest.Marketplace.DefaultLocale]
		if file.Path == "" || nginx.Contents[file.Path] == "" {
			t.Fatalf("static NGINX %s document missing", key)
		}
	}
}
