package storage_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestControlDocumentsAcrossDatabases(t *testing.T) {
	for _, test := range []struct{ driver, dsn string }{{"sqlite", "file:" + filepath.Join(t.TempDir(), "control.db") + "?_pragma=foreign_keys(1)&_time_format=sqlite"}, {"postgres", os.Getenv("NSC_TEST_POSTGRES_DSN")}, {"mysql", os.Getenv("NSC_TEST_MYSQL_DSN")}} {
		t.Run(test.driver, func(t *testing.T) {
			if test.dsn == "" {
				t.Skip("external test database is not configured")
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
			pool := model.GatewayPool{ID: id.New(id.Pool), Name: "control", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
			if e = s.CreatePool(ctx, &pool); e != nil {
				t.Fatal(e)
			}
			document := storage.ControlDocument{ID: id.New("control"), Kind: "fixture", ParentID: pool.ID, Payload: json.RawMessage(`{"value":"first"}`), CreatedAt: now, UpdatedAt: now}
			if e = s.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: document}}); e != nil {
				t.Fatal(e)
			}
			history := document
			history.ID = id.New("history")
			history.Kind = "fixture-history"
			document.Payload = json.RawMessage(`{"value":"stale"}`)
			if e = s.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: history}, {Document: document, Expected: 9}}); !errors.Is(e, storage.ErrPrecondition) {
				t.Fatal(e)
			}
			if _, e = s.GetControlDocument(ctx, history.ID); !errors.Is(e, storage.ErrNotFound) {
				t.Fatal("failed transaction retained history", e)
			}
			document.Kind = "nginx-active"
			document.ID = pool.ID + ":nginx-active"
			document.Payload = json.RawMessage(`{"snippets":[{"scope":"http","content":"gzip on;"}]}`)
			if e = s.PublishNginxDocuments(ctx, pool.ID, 0, []storage.ControlWrite{{Document: document}}); e != nil {
				t.Fatal(e)
			}
			actual, e := s.GetPool(ctx, pool.ID)
			if e != nil || actual.DesiredRevision != 1 {
				t.Fatal(actual, e)
			}
			if e = s.PublishNginxDocuments(ctx, pool.ID, 0, []storage.ControlWrite{{Document: document, Expected: 1}}); !errors.Is(e, storage.ErrPrecondition) {
				t.Fatal("stale pool publish", e)
			}
			rows, e := s.ListControlDocuments(ctx, "fixture", pool.ID, storage.Page{Limit: 50})
			if e != nil || len(rows) != 1 {
				t.Fatal(rows, e)
			}
		})
	}
}
