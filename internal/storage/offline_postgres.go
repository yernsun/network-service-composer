package storage

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// OpenOfflinePostgres retains a caller-owned, authenticated transport (for
// example the bootstrap SSH relay) for every pooled connection. It is for the
// offline executor, which never starts LISTEN workers or background controllers.
func OpenOfflinePostgres(config *pgx.ConnConfig) (*SQLStore, error) {
	if config == nil {
		return nil, fmt.Errorf("offline PostgreSQL connection configuration is required")
	}
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	return &SQLStore{db: db, dialect: Postgres, dsn: config.ConnString()}, nil
}
