package storage

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
)

func catalogBoundaryFixture(t *testing.T, kind model.CatalogSourceType) (*SQLStore, model.CatalogSource, model.CatalogAppVersion) {
	t.Helper()
	s := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	source := model.CatalogSource{ID: id.New(id.CatalogSource), Name: "boundary", Type: kind, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	v := model.CatalogAppVersion{ID: id.New(id.CatalogVersion), SourceID: source.ID, AppKey: "web", Name: "Web", Version: "1", Services: []string{"web"}, CompatibilityIssues: []string{"requires a network"}, ComposeCompatible: true, SwarmCompatible: true, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := s.CreateCatalogSource(context.Background(), &source); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertCatalogAppVersion(context.Background(), &v); err != nil {
		t.Fatal(err)
	}
	return s, source, v
}

func TestEveryCatalogArtifactFieldIsImmutable(t *testing.T) {
	changes := map[string]func(*model.CatalogAppVersion){
		"market":          func(v *model.CatalogAppVersion) { v.MarketJSON = `{"docs":{}}` },
		"name":            func(v *model.CatalogAppVersion) { v.Name = "renamed" },
		"description":     func(v *model.CatalogAppVersion) { v.Description = "changed" },
		"commit":          func(v *model.CatalogAppVersion) { v.Commit = "another commit" },
		"artifact":        func(v *model.CatalogAppVersion) { v.ArtifactDigest = "new digest" },
		"compose digest":  func(v *model.CatalogAppVersion) { v.ComposeSHA256 = "new digest" },
		"manifest":        func(v *model.CatalogAppVersion) { v.ManifestJSON = "{}" },
		"compose":         func(v *model.CatalogAppVersion) { v.ComposeYAML = "services: {}" },
		"environment":     func(v *model.CatalogAppVersion) { v.EnvExample = "PORT=80" },
		"readme":          func(v *model.CatalogAppVersion) { v.README = "Updated" },
		"icon":            func(v *model.CatalogAppVersion) { v.IconURL = "https://example.test/icon.png" },
		"services":        func(v *model.CatalogAppVersion) { v.Services = []string{"worker"} },
		"compose support": func(v *model.CatalogAppVersion) { v.ComposeCompatible = false },
		"swarm support":   func(v *model.CatalogAppVersion) { v.SwarmCompatible = false },
		"compatibility":   func(v *model.CatalogAppVersion) { v.CompatibilityIssues = []string{"unsupported device"} },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			s, source, original := catalogBoundaryFixture(t, model.CatalogManifest)
			before, err := s.GetCatalogAppVersion(context.Background(), original.ID)
			if err != nil {
				t.Fatal(err)
			}
			candidate := original
			candidate.ID = id.New(id.CatalogVersion)
			change(&candidate)
			for _, sync := range []bool{false, true} {
				if sync {
					err = s.SyncCatalogAppVersions(context.Background(), &source, []model.CatalogAppVersion{candidate}, 1)
				} else {
					err = s.UpsertCatalogAppVersion(context.Background(), &candidate)
				}
				if !errors.Is(err, ErrConflict) {
					t.Fatalf("changed %s accepted (sync=%t): %v", name, sync, err)
				}
				stored, readErr := s.GetCatalogAppVersion(context.Background(), original.ID)
				if readErr != nil || !reflect.DeepEqual(before, stored) {
					t.Fatalf("failed mutation changed artifact: %+v, %v", stored, readErr)
				}
			}
		})
	}
}

func TestBuiltinDocumentationEnrichmentIsOneTime(t *testing.T) {
	s, _, original := catalogBoundaryFixture(t, model.CatalogBuiltin)
	candidate := original
	candidate.ID = id.New(id.CatalogVersion)
	candidate.MarketJSON = `{"documents":{"en":"README.md"}}`
	if err := s.UpsertCatalogAppVersion(context.Background(), &candidate); err != nil {
		t.Fatal(err)
	}
	if candidate.ID != original.ID {
		t.Fatal("enrichment changed artifact identity")
	}
	stored, err := s.GetCatalogAppVersion(context.Background(), original.ID)
	if err != nil || stored.MarketJSON != candidate.MarketJSON || stored.ComposeSHA256 != original.ComposeSHA256 || stored.README != original.README {
		t.Fatalf("enrichment changed installation content: %+v %v", stored, err)
	}
	candidate.MarketJSON = `{"documents":{"en":"replacement.md"}}`
	if err = s.UpsertCatalogAppVersion(context.Background(), &candidate); !errors.Is(err, ErrConflict) {
		t.Fatalf("second enrichment accepted: %v", err)
	}
}

func TestCatalogSyncDatabaseFailuresDoNotCommitIndex(t *testing.T) {
	failure := errors.New("catalog database interrupted")
	for _, kind := range []model.CatalogSourceType{model.CatalogGit, model.CatalogManifest} {
		for _, stage := range []string{"source", "missing source", "stale", "withdraw", "foreign", "lookup", "insert", "restore", "source update", "affected", "precondition", "commit", "success"} {
			if kind != model.CatalogManifest && (stage == "withdraw" || stage == "restore") {
				continue
			}
			t.Run(string(kind)+"/"+stage, func(t *testing.T) {
				script := &queryScript{t: t, transactions: true}
				source := model.CatalogSource{ID: "source", Type: kind, Revision: 1}
				version := model.CatalogAppVersion{ID: "version", SourceID: "source", AppKey: "web", Version: "1"}
				read := &queryReply{contains: "SELECT revision FROM catalog_sources", columns: []string{"revision"}, values: [][]driver.Value{{int64(1)}}}
				script.replies = []*queryReply{read}
				want := failure
				switch stage {
				case "source":
					read.err = failure
				case "missing source":
					read.values = nil
					want = ErrNotFound
				case "stale":
					read.values = [][]driver.Value{{int64(2)}}
					want = ErrPrecondition
				default:
					if kind == model.CatalogManifest {
						script.execs = append(script.execs, &execReply{contains: "withdrawn=TRUE", affected: 1})
					}
					if stage == "withdraw" {
						script.execs[0].err = failure
						break
					}
					if stage == "foreign" {
						version.SourceID = "foreign"
						want = ErrConflict
						break
					}
					lookup := &queryReply{contains: "SELECT id FROM catalog_app_versions", columns: []string{"id"}}
					script.replies = append(script.replies, lookup)
					if stage == "lookup" {
						lookup.err = failure
						break
					}
					script.execs = append(script.execs, &execReply{contains: "INSERT INTO catalog_app_versions", affected: 1})
					if stage == "insert" {
						script.execs[len(script.execs)-1].err = failure
						break
					}
					if kind == model.CatalogManifest {
						script.execs = append(script.execs, &execReply{contains: "withdrawn=FALSE", affected: 1})
					}
					if stage == "restore" {
						script.execs[len(script.execs)-1].err = failure
						break
					}
					write := &execReply{contains: "UPDATE catalog_sources", affected: 1}
					script.execs = append(script.execs, write)
					switch stage {
					case "source update":
						write.err = failure
					case "affected":
						write.affectedErr = failure
					case "precondition":
						write.affected = 0
						want = ErrPrecondition
					case "commit":
						script.commitErr = failure
					case "success":
						want = nil
					}
				}
				store := storeWithScript(t, script)
				err := store.SyncCatalogAppVersions(context.Background(), &source, []model.CatalogAppVersion{version}, 1)
				if !errors.Is(err, want) {
					t.Fatalf("got %v want %v", err, want)
				}
				if stage == "success" || stage == "commit" {
					if script.commits != 1 {
						t.Fatal("missing commit attempt")
					}
				} else if script.commits != 0 || script.rollbacks != 1 {
					t.Fatal("partial catalog was not rolled back")
				}
			})
		}
	}
}

func TestCatalogStoredJSONCorruptionIsReported(t *testing.T) {
	for _, column := range []string{"services_json", "compatibility_issues"} {
		t.Run(column, func(t *testing.T) {
			s, source, v := catalogBoundaryFixture(t, model.CatalogGit)
			if _, err := s.DB().Exec("UPDATE catalog_app_versions SET "+column+"=? WHERE id=?", "{", v.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetCatalogAppVersion(context.Background(), v.ID); err == nil {
				t.Fatal("corrupt artifact accepted by detail")
			}
			if _, err := s.ListCatalogAppVersions(context.Background(), source.ID, "web", Page{}); err == nil {
				t.Fatal("corrupt artifact accepted by list")
			}
			if err := s.UpsertCatalogAppVersion(context.Background(), &v); err == nil {
				t.Fatal("resync hid corrupt existing artifact")
			}
			if err := s.SyncCatalogAppVersions(context.Background(), &source, []model.CatalogAppVersion{v}, 1); err == nil || !strings.Contains(err.Error(), "unexpected end") {
				t.Fatalf("sync hid corruption: %v", err)
			}
		})
	}
}
