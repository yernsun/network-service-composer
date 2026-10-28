package storage_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestMarketplaceAcrossDatabases(t *testing.T) {
	for _, test := range []struct{ driver, dsn string }{{"sqlite", "file:" + filepath.Join(t.TempDir(), "market.db") + "?_pragma=foreign_keys(1)&_time_format=sqlite"}, {"postgres", os.Getenv("NSC_TEST_POSTGRES_DSN")}, {"mysql", os.Getenv("NSC_TEST_MYSQL_DSN")}} {
		t.Run(test.driver, func(t *testing.T) {
			if test.dsn == "" {
				t.Skip("external database not configured")
			}
			ctx := context.Background()
			s, e := storage.Open(test.driver, test.dsn)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			if e = s.Migrate(ctx); e != nil {
				t.Fatal(e)
			}
			now := time.Now().UTC()
			source := model.CatalogSource{ID: id.New(id.CatalogSource), Name: id.New(id.CatalogSource), Type: model.CatalogManifest, URL: "https://market.example/market.yaml", Trusted: true, Enabled: true, MarketJSON: `{"documents":{}}`, Revision: 1, CreatedAt: now, UpdatedAt: now}
			if e = s.CreateCatalogSource(ctx, &source); e != nil {
				t.Fatal(e)
			}
			source.ApprovedEndpoint = "https://files.example/"
			if e = s.UpdateCatalogSource(ctx, &source, source.Revision); e != nil {
				t.Fatal(e)
			}
			source, e = s.GetCatalogSource(ctx, source.ID)
			if e != nil || source.ApprovedEndpoint != "https://files.example/" {
				t.Fatal(e)
			}
			version := model.CatalogAppVersion{ID: id.New(id.CatalogVersion), SourceID: source.ID, AppKey: "demo", Name: "Demo", Version: "1", ArtifactDigest: "a", ComposeSHA256: "b", ComposeYAML: "services: {}", MarketJSON: `{"contents":{"doc":"Русский / Español"}}`, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			source.LastSyncedAt = &now
			if e = s.SyncCatalogAppVersions(ctx, &source, []model.CatalogAppVersion{version}, source.Revision); e != nil {
				t.Fatal(e)
			}
			source, _ = s.GetCatalogSource(ctx, source.ID)
			stored, e := s.GetCatalogAppVersion(ctx, version.ID)
			if e != nil || stored.MarketJSON != version.MarketJSON {
				t.Fatalf("snapshot round trip: %v", e)
			}
			if e = s.SyncCatalogAppVersions(ctx, &source, nil, source.Revision); e != nil {
				t.Fatal(e)
			}
			stored, e = s.GetCatalogAppVersion(ctx, version.ID)
			if e != nil || !stored.Withdrawn {
				t.Fatal("withdrawal failed", e)
			}
		})
	}
}
