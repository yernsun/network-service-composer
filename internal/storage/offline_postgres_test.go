package storage

import (
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestOfflinePostgresRetainsTransportWithoutGlobalRegistration(t *testing.T) {
	if _, err := OpenOfflinePostgres(nil); err == nil {
		t.Fatal("missing transport configuration accepted")
	}
	cfg, err := pgx.ParseConfig("postgres://fixture@127.0.0.1/fixture?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenOfflinePostgres(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.Dialect() != Postgres || store.DB().Stats().OpenConnections != 0 || store.DB().Stats().MaxOpenConnections != 10 {
		t.Fatal("offline store opened an unsolicited connection")
	}
}
