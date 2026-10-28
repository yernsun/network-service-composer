package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/marketplace"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestStaticMarketAtomicLifecycle(t *testing.T) {
	c, _, _ := controlFixture(t)
	ctx := context.Background()
	source, err := c.CreateCatalogSource(ctx, CreateCatalogSourceInput{Name: "static", Type: model.CatalogManifest, URL: "https://market.example/market.yaml", Trusted: true})
	if err != nil {
		t.Fatal(err)
	}
	compose := "services:\n  web:\n    image: nginx:alpine\n    expose: [\"80\"]\n"
	doc := "# Test\n\nA local document."
	f := marketplace.File{Path: "compose.yaml", SHA256: marketplace.Digest([]byte(compose))}
	r := marketplace.File{Path: "README.md", SHA256: marketplace.Digest([]byte(doc))}
	m := marketplace.Manifest{SchemaVersion: 1, Marketplace: marketplace.Metadata{ID: "test", Name: marketplace.Text{"en": "Test"}, Endpoint: "https://market.example/", DefaultLocale: "en", Locales: []string{"en"}}, Apps: []marketplace.App{{ID: "demo", Name: marketplace.Text{"en": "Demo"}, Versions: []marketplace.Version{{Version: "1", Files: marketplace.Files{Compose: f, Readme: map[string]marketplace.File{"en": r}}}}}}}
	fetch := func(_ context.Context, u string) ([]byte, error) {
		switch u {
		case "https://market.example/compose.yaml":
			return []byte(compose), nil
		case "https://market.example/README.md":
			return []byte(doc), nil
		}
		return nil, fmt.Errorf("unexpected fetch %s", u)
	}
	analyze := func() ([]model.CatalogAppVersion, marketplace.Snapshot, error) {
		b, _ := json.Marshal(m)
		return c.analyzeMarket(ctx, source, b, fetch)
	}
	versions, snapshot, err := analyze()
	if err != nil {
		t.Fatal(err)
	}
	source.MarketJSON = marketplace.JSON(snapshot)
	now := c.now()
	source.LastSyncedAt = &now
	if err = c.store.SyncCatalogAppVersions(ctx, &source, versions, source.Revision); err != nil {
		t.Fatal(err)
	}
	source, _ = c.store.GetCatalogSource(ctx, source.ID)
	again, _, err := analyze()
	if err != nil {
		t.Fatal(err)
	}
	if err = c.store.SyncCatalogAppVersions(ctx, &source, again, source.Revision); err != nil {
		t.Fatal(err)
	}
	listed, _ := c.store.ListCatalogAppVersions(ctx, source.ID, "", storage.Page{Limit: 100})
	if len(listed) != 1 {
		t.Fatal("duplicate sync created versions")
	}
	source, _ = c.store.GetCatalogSource(ctx, source.ID)
	changed := append([]model.CatalogAppVersion{}, versions...)
	changed[0].MarketJSON = `{"changed":true}`
	if err = c.store.SyncCatalogAppVersions(ctx, &source, changed, source.Revision); err == nil {
		t.Fatal("immutable metadata overwritten")
	}
	stored, _ := c.store.GetCatalogAppVersion(ctx, listed[0].ID)
	if stored.Withdrawn {
		t.Fatal("failed transaction withdrew old version")
	}
	if err = c.store.SyncCatalogAppVersions(ctx, &source, nil, source.Revision); err != nil {
		t.Fatal(err)
	}
	stored, _ = c.store.GetCatalogAppVersion(ctx, listed[0].ID)
	if !stored.Withdrawn {
		t.Fatal("removed version not withdrawn")
	}
	if _, err = c.resolveCatalogInput(ctx, ApplicationPreflightInput{CatalogAppVersionID: stored.ID}); err == nil {
		t.Fatal("withdrawn version installed")
	}
	m.Marketplace.Endpoint = "https://other.example/"
	if _, _, err = analyze(); err == nil || !strings.Contains(err.Error(), "approved_endpoint") {
		t.Fatal("endpoint change not blocked")
	}
	m.Marketplace.Endpoint = "https://market.example/"
	m.Apps[0].Versions[0].Files.Compose.SHA256 = strings.Repeat("0", 64)
	if _, _, err = analyze(); err == nil {
		t.Fatal("bad checksum accepted")
	}
}

func TestOfficialStaticMarketPreflight(t *testing.T) {
	c, _, _ := controlFixture(t)
	ctx := context.Background()
	payload, err := builtinCatalogFiles.ReadFile("builtin/market.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m, err := marketplace.Decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	pin := func(f marketplace.File) marketplace.File {
		if f.Path == "" {
			return f
		}
		data, e := builtinCatalogFiles.ReadFile("builtin/" + f.Path)
		if e != nil {
			t.Fatal(e)
		}
		f.SHA256 = marketplace.Digest(data)
		return f
	}
	pinDocs := func(docs map[string]marketplace.Document) {
		for k, d := range docs {
			for l, f := range d.Files {
				d.Files[l] = pin(f)
			}
			docs[k] = d
		}
	}
	pinDocs(m.Marketplace.Docs)
	for ai := range m.Apps {
		for vi := range m.Apps[ai].Versions {
			v := &m.Apps[ai].Versions[vi]
			v.Files.Compose = pin(v.Files.Compose)
			v.Files.Swarm = pin(v.Files.Swarm)
			v.Files.EnvExample = pin(v.Files.EnvExample)
			for l, f := range v.Files.Readme {
				v.Files.Readme[l] = pin(f)
			}
			pinDocs(v.Files.Manuals)
		}
	}
	payload, _ = json.Marshal(m)
	source := model.CatalogSource{ID: "official-static", URL: m.Marketplace.Endpoint + "market.yaml", Type: model.CatalogManifest}
	fetch := func(_ context.Context, u string) ([]byte, error) {
		return builtinCatalogFiles.ReadFile("builtin/" + strings.TrimPrefix(u, m.Marketplace.Endpoint))
	}
	versions, _, err := c.analyzeMarket(ctx, source, payload, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 13 {
		t.Fatal("thirteen template versions required")
	}
	for _, v := range versions {
		t.Run(v.AppKey, func(t *testing.T) {
			manifest, e := applicationx.ParseManifest(v.ManifestJSON)
			if e != nil {
				t.Fatal(e)
			}
			vars := []applicationx.EnvironmentVariable{}
			for _, variable := range manifest.Variables {
				if !variable.Required {
					continue
				}
				value := "market-password-123456"
				switch variable.Key {
				case "PGADMIN_DEFAULT_EMAIL":
					value = "admin@example.com"
				case "PMA_HOST":
					value = "mysql"
				case "DATABASE_NETWORK":
					value = "database_default"
				}
				vars = append(vars, applicationx.EnvironmentVariable{Key: variable.Key, Value: &value, Secret: variable.Secret})
			}
			_, e = applicationx.Preflight(applicationx.PreflightInput{ComposeYAML: v.ComposeYAML, EnvExample: v.EnvExample, ManifestYAML: v.ManifestJSON, Backend: model.RuntimeDocker, Environment: applicationx.EnvironmentInput{Values: &vars}})
			if e != nil {
				t.Fatal(e)
			}
			if v.SwarmCompatible && v.SwarmYAML == "" && v.AppKey != "nginx" {
				t.Fatal("stateful Swarm install unexpectedly enabled")
			}
			var snapshot marketplace.Snapshot
			if e = json.Unmarshal([]byte(v.MarketJSON), &snapshot); e != nil {
				t.Fatal(e)
			}
			if len(snapshot.Documents["readme"].Files) != 8 || len(snapshot.Documents["operations"].Files) != 8 {
				t.Fatal("missing translations")
			}
		})
	}
}
