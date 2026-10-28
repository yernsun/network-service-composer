package storage

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
)

func TestActivationClaimGuardLocksSupportedStoreRowsAndFailsClosed(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		for _, outcome := range []string{"current", "superseded", "unavailable"} {
			t.Run(string(dialect)+"/"+outcome, func(t *testing.T) {
				query := "SELECT id FROM deployment_operations"
				if dialect != SQLite {
					query = "FOR UPDATE"
				}
				reply := &queryReply{contains: query, columns: []string{"id"}, values: [][]driver.Value{{"operation"}}}
				if outcome == "superseded" {
					reply.values = nil
				}
				if outcome == "unavailable" {
					reply.err = errors.New("database observation unavailable")
				}
				script := &queryScript{t: t, transactions: true, replies: []*queryReply{reply}}
				s := storeWithScript(t, script)
				s.dialect = dialect
				tx, err := s.DB().BeginTx(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				ctx := WithDeploymentOperation(context.Background(), model.DeploymentOperation{ID: "operation", WorkerID: "worker", Attempt: 2})
				err = s.guardDeploymentOperationTx(ctx, tx, "application")
				if outcome == "current" && err != nil || outcome != "current" && !errors.Is(err, ErrOperationOwnership) {
					t.Fatalf("ownership result: %v", err)
				}
				if outcome == "unavailable" && !strings.Contains(err.Error(), "unavailable") {
					t.Fatal("observation failure was hidden")
				}
			})
		}
	}
}
