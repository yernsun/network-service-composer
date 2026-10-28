package storage

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestApplicationActivationRejectsForeignOwnershipAndIncompleteRows(t *testing.T) {
	for _, stage := range []string{"foreign runtime", "foreign active revision", "foreign completed revision", "foreign service", "missing service", "invalid service row", "service stream failure", "invalid removed row", "removed stream failure", "invalid route row", "route stream failure", "application no affected rows", "application affected failure"} {
		t.Run(stage, func(t *testing.T) {
			steps := activationTransactionSteps(SQLite, true)
			stop := -1
			failure := errors.New("activation row stream failed")
			want := ErrConflict
			for i, step := range steps {
				if step.query != nil {
					q := step.query
					switch {
					case stage == "foreign runtime" && strings.Contains(q.contains, "SELECT a.runtime_id"):
						q.values[0][0] = "foreign"
						stop = i
					case stage == "foreign active revision" && strings.Contains(q.contains, "SELECT application_id,catalog_app_version_id"):
						q.values[0][0] = "foreign"
						stop = i
					case stage == "foreign completed revision" && strings.Contains(q.contains, "SELECT application_id FROM"):
						q.values[0][0] = "foreign"
						stop = i
					case strings.Contains(q.contains, "SELECT service_key,application_id"):
						switch stage {
						case "foreign service":
							q.values[0][1] = "foreign"
							stop = i
						case "missing service":
							q.values = nil
							stop = i
							want = ErrNotFound
						case "invalid service row":
							q.values[0][2] = "not boolean"
							stop = i
							want = nil
						case "service stream failure":
							q.rowErr = failure
							stop = i
							want = failure
						}
					case strings.Contains(q.contains, "SELECT id FROM services WHERE runtime_id="):
						switch stage {
						case "invalid removed row":
							q.values[0][0] = nil
							stop = i
							want = nil
						case "removed stream failure":
							q.rowErr = failure
							stop = i
							want = failure
						}
					case strings.Contains(q.contains, "FROM routes WHERE source="):
						switch stage {
						case "invalid route row":
							q.values = [][]driver.Value{{nil}}
							stop = i
							want = nil
						case "route stream failure":
							q.rowErr = failure
							stop = i
							want = failure
						}
					}
				} else if strings.Contains(step.write.contains, "UPDATE applications SET active_revision_id") {
					switch stage {
					case "application no affected rows":
						step.write.affected = 0
						stop = i
						want = ErrNotFound
					case "application affected failure":
						step.write.affectedErr = failure
						stop = i
						want = failure
					}
				}
				if stop >= 0 {
					break
				}
			}
			if stop < 0 {
				t.Fatal("fixture did not reach intended activation boundary")
			}
			script := &queryScript{t: t, transactions: true}
			for _, step := range steps[:stop+1] {
				if step.query != nil {
					script.replies = append(script.replies, step.query)
				} else {
					script.execs = append(script.execs, step.write)
				}
			}
			s := storeWithScript(t, script)
			e := s.ActivateApplicationRevision(context.Background(), "runtime", "application", "active", "candidate", []string{"demo/web"}, map[string]int{"demo/web": 2}, "", time.Now())
			if e == nil || want != nil && !errors.Is(e, want) || script.commits != 0 || script.rollbacks != 1 {
				t.Fatalf("activation failure lost ownership or committed partial state: %v commits=%d rollbacks=%d", e, script.commits, script.rollbacks)
			}
		})
	}
	for _, values := range []struct {
		active, completed string
		keys              []string
	}{{"active", "candidate", nil}, {"", "candidate", []string{"web"}}, {"active", "", []string{"web"}}} {
		s := storeWithScript(t, &queryScript{t: t})
		if e := s.ActivateApplicationRevision(context.Background(), "runtime", "application", values.active, values.completed, values.keys, nil, "", time.Now()); !errors.Is(e, ErrConflict) {
			t.Fatal("incomplete activation identity entered transaction", e)
		}
	}
}
