package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoverySchemaRequiresCurrentJournalWithoutCreatingOrChangingIt(t *testing.T) {
	for _, scenario := range []string{"current", "older", "future", "future hidden behind current", "missing historical migration", "historical migration rolled back", "rolled back future", "empty journal", "malformed version", "missing journal", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			store, err := Open("sqlite", "file:"+filepath.Join(t.TempDir(), "schema.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			ctx := context.Background()
			if scenario != "missing journal" {
				if err = store.Migrate(ctx); err != nil {
					t.Fatal(err)
				}
			}
			var latest int64
			if scenario != "missing journal" {
				if err = store.DB().QueryRowContext(ctx, "SELECT MAX(version_id) FROM goose_db_version").Scan(&latest); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "older":
				_, err = store.DB().ExecContext(ctx, "INSERT INTO goose_db_version(version_id,is_applied) VALUES (?,false),(?,false)", latest, latest)
			case "future":
				_, err = store.DB().ExecContext(ctx, "INSERT INTO goose_db_version(version_id,is_applied) VALUES (?,true)", latest+100)
			case "rolled back future":
				_, err = store.DB().ExecContext(ctx, "INSERT INTO goose_db_version(version_id,is_applied) VALUES (?,true),(?,false),(?,false)", latest+100, latest+100, latest+100)
			case "future hidden behind current":
				_, err = store.DB().ExecContext(ctx, "INSERT INTO goose_db_version(version_id,is_applied) VALUES (?,true),(?,true)", latest+100, latest)
			case "missing historical migration":
				_, err = store.DB().ExecContext(ctx, "DELETE FROM goose_db_version WHERE version_id=(SELECT MIN(version_id) FROM goose_db_version WHERE version_id>0)")
			case "historical migration rolled back":
				_, err = store.DB().ExecContext(ctx, "INSERT INTO goose_db_version(version_id,is_applied) SELECT MIN(version_id),false FROM goose_db_version WHERE version_id>0")
			case "empty journal":
				_, err = store.DB().ExecContext(ctx, "DELETE FROM goose_db_version")
			case "malformed version":
				_, err = store.DB().ExecContext(ctx, "INSERT INTO goose_db_version(version_id,is_applied) VALUES ('corrupt',true)")
			}
			if err != nil {
				t.Fatal(err)
			}
			var before int
			if err = store.DB().QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&before); err != nil {
				t.Fatal(err)
			}
			checkCtx := ctx
			if scenario == "cancelled" {
				var cancel context.CancelFunc
				checkCtx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err = store.ValidateRecoverySchema(checkCtx)
			allowed := scenario == "current" || scenario == "rolled back future"
			if allowed && err != nil || !allowed && err == nil {
				t.Fatal("unsupported schema accepted or compatible schema rejected", scenario, err)
			}
			if scenario == "older" || scenario == "future" {
				if !strings.Contains(err.Error(), "incompatible") {
					t.Fatal("version mismatch was not explicit", err)
				}
			}
			var after int
			if err = store.DB().QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&after); err != nil || after != before {
				t.Fatal("schema inspection changed database tables", err)
			}
		})
	}
}
