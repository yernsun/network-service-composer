package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*/*.sql
var migrationFS embed.FS

func migrate(ctx context.Context, db *sql.DB, dialect Dialect) error {
	if err := goose.SetDialect(string(dialect)); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}
	goose.SetBaseFS(migrationFS)
	if err := goose.UpContext(ctx, db, "migrations/"+string(dialect)); err != nil {
		return fmt.Errorf("apply %s migrations: %w", dialect, err)
	}
	return nil
}
