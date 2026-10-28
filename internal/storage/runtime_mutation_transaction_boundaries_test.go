package storage

import (
	"context"
	"errors"
	"fmt"
	"github.com/yernsun/network-service-composer/internal/model"
	"strings"
	"testing"
	"time"
)

func runtimeBoundaryRead(v model.Runtime) transactionBoundaryStep {
	return boundaryRead("FROM runtimes WHERE id", strings.Split(runtimeColumns, ","), v.ID, v.PoolID, v.Name, string(v.Type), v.Endpoint, "", "", v.Enabled, v.ManagementEnabled, string(v.Status), int64(0), int64(0), nil, "", int64(1), v.CreatedAt, v.UpdatedAt)
}
func TestRuntimeMutationAndPoolRevisionFailuresAreAtomic(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		for _, action := range []string{"disable", "move drained", "rename", "bump"} {
			t.Run(string(dialect)+"/"+action, func(t *testing.T) {
				now := time.Now().UTC()
				current := model.Runtime{ID: "runtime", PoolID: "old-pool", Name: "original", Type: model.RuntimeDocker, Enabled: true, ManagementEnabled: true, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
				next := current
				var steps []transactionBoundaryStep
				var run func(*SQLStore) error
				switch action {
				case "bump":
					steps = boundaryPoolSteps(dialect)
					run = func(s *SQLStore) error {
						rev, e := s.BumpPoolRevision(context.Background(), current.PoolID)
						if e == nil && rev != 8 {
							t.Fatalf("unexpected target revision %d", rev)
						}
						return e
					}
				default:
					if action == "move drained" {
						current.Enabled = false
						next.Enabled = false
						next.PoolID = "new-pool"
					}
					if action == "disable" {
						next.Enabled = false
					}
					if action == "rename" {
						next.Name = "renamed"
					}
					steps = []transactionBoundaryStep{runtimeBoundaryRead(current)}
					if action == "move drained" {
						steps = append(steps, boundaryRead("SELECT COUNT(*) FROM services", []string{"count"}, int64(0)))
					}
					steps = append(steps, boundaryWrite("UPDATE runtimes SET pool_id"))
					if action == "disable" {
						steps = append(steps, boundaryPoolSteps(dialect)...)
					}
					run = func(s *SQLStore) error { v := next; return s.UpdateRuntime(context.Background(), &v, 1) }
				}
				for fault := -1; fault <= len(steps); fault++ {
					t.Run(fmt.Sprint(fault), func(t *testing.T) { runTransactionBoundary(t, dialect, steps, fault, run) })
				}
			})
		}
	}
}
func TestRuntimeMoveCannotReassignLiveWorkload(t *testing.T) {
	for _, tc := range []struct {
		name      string
		count     int64
		was, next bool
	}{
		{"services remain", 1, false, false}, {"old enabled", 0, true, false}, {"new enabled", 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := model.Runtime{ID: "runtime", PoolID: "old", Type: model.RuntimeDocker, Enabled: tc.was}
			script := &queryScript{t: t, transactions: true, replies: []*queryReply{runtimeBoundaryRead(current).query, boundaryRead("SELECT COUNT(*) FROM services", []string{"count"}, tc.count).query}}
			s := storeWithScript(t, script)
			next := current
			next.PoolID = "new"
			next.Enabled = tc.next
			if e := s.UpdateRuntime(context.Background(), &next, 1); !errors.Is(e, ErrConflict) {
				t.Fatal("unsafe pool move accepted", e)
			}
			if script.commits != 0 || script.rollbacks != 1 {
				t.Fatal("unsafe move committed", script.commits, script.rollbacks)
			}
		})
	}
}
