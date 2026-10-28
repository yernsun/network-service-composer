package storage

import (
	"context"
	"database/sql/driver"
	"errors"
	"github.com/yernsun/network-service-composer/internal/model"
	"strings"
	"testing"
	"time"
)

func TestCatalogDocumentationEnrichmentFailurePreservesArtifactIdentity(t *testing.T) {
	failure := errors.New("isolated enrichment failure")
	for _, stage := range []string{"source lookup", "enrichment write", "success"} {
		t.Run(stage, func(t *testing.T) {
			now := time.Now().UTC()
			existing := model.CatalogAppVersion{ID: "version", SourceID: "source", AppKey: "web", Version: "1", MarketJSON: "{}", Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			candidate := existing
			candidate.ID = "candidate"
			candidate.MarketJSON = `{"documents":{"readme":{}}}`
			source := &queryReply{contains: "FROM catalog_sources", columns: strings.Split(catalogSourceColumns, ",")}
			script := &queryScript{t: t, replies: []*queryReply{{contains: "SELECT id FROM catalog_app_versions", columns: []string{"id"}, values: [][]driver.Value{{"version"}}}, {contains: "FROM catalog_app_versions", columns: strings.Split(catalogVersionColumns, ","), values: [][]driver.Value{snapshotRow(t, catalogVersionArgs(&existing))}}, source}}
			if stage == "source lookup" {
				source.err = failure
			} else {
				source.values = [][]driver.Value{{"source", "built-in", string(model.CatalogBuiltin), "", "", true, true, nil, "", int64(1), now, now, "", "{}"}}
				write := &execReply{contains: "UPDATE catalog_app_versions SET market_json", affected: 1}
				if stage == "enrichment write" {
					write.err = failure
				}
				script.execs = []*execReply{write}
			}
			s := storeWithScript(t, script)
			e := s.UpsertCatalogAppVersion(context.Background(), &candidate)
			if stage == "success" {
				if e != nil || candidate.ID != existing.ID || candidate.Revision != 1 {
					t.Fatalf("metadata enrichment changed artifact identity: %+v %v", candidate, e)
				}
			} else if !errors.Is(e, failure) || candidate.ID != "candidate" {
				t.Fatalf("failed enrichment claimed accepted artifact: %+v %v", candidate, e)
			}
		})
	}
}
func TestApplicationRevisionBindingFailureRollsBackDesiredVersion(t *testing.T) {
	failure := errors.New("environment binding unavailable")
	script := &queryScript{t: t, transactions: true, execs: []*execReply{{contains: "UPDATE applications", affected: 1}}, replies: []*queryReply{{contains: "kind='image-policy'", err: failure}}}
	revision := &model.ApplicationRevision{ID: "new", ApplicationID: "app", ConfigurationSource: model.DeploymentSource{EnvironmentID: "environment", UpdatePolicyRevision: 1}}
	s := storeWithScript(t, script)
	e := s.CreateApplicationRevision(context.Background(), revision, &model.DeploymentOperation{ID: "operation"}, 1, time.Now().UTC())
	if !errors.Is(e, failure) || script.commits != 0 || script.rollbacks != 1 {
		t.Fatalf("failed binding committed application target: %v", e)
	}
}

func TestCatalogVersionEqualityIncludesSourceAndApplicationIdentity(t *testing.T) {
	original := model.CatalogAppVersion{SourceID: "source", AppKey: "app", Version: "1"}
	for _, candidate := range []model.CatalogAppVersion{{SourceID: "other", AppKey: "app", Version: "1"}, {SourceID: "source", AppKey: "other", Version: "1"}} {
		if sameCatalogVersion(original, candidate) {
			t.Fatal("same version text conflated unrelated catalog identities")
		}
	}
}
