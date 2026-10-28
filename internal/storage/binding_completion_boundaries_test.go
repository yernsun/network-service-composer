package storage

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func bindingCompletionSteps(t *testing.T, dialect Dialect, detach bool) []transactionBoundaryStep {
	var steps []transactionBoundaryStep
	if detach {
		steps = []transactionBoundaryStep{boundaryRead("SELECT rt.pool_id,sv.managed", []string{"pool", "managed"}, "pool", true), boundaryRead("SELECT COUNT(*) FROM routes", []string{"count"}, int64(0))}
	} else {
		steps = []transactionBoundaryStep{boundaryRead("SELECT rt.pool_id,r.route_status", []string{"pool", "status", "service", "dns", "managed"}, "pool", string(model.RoutePending), "service", "demo_web", true)}
	}
	steps = append(steps, boundaryPlaneSteps(false)...)
	steps = append(steps, boundaryEmpty("FROM services"))
	if detach {
		steps = append(steps, boundaryWrite("UPDATE services SET dns_status"))
	} else {
		steps = append(steps, boundaryWrite("UPDATE services SET dns_name"))
	}
	steps = append(steps, boundaryEmpty("FROM services"))
	if !detach {
		steps = append(steps, boundaryRead("SELECT dns_status,dns_last_error", []string{"status", "error"}, string(model.DNSReady), ""), boundaryWrite("UPDATE routes SET route_status"))
	}
	steps = append(steps, boundaryPlaneSteps(true)...)
	now := time.Now()
	service := model.Service{ID: "service", RuntimeID: "runtime", ServiceKey: "demo/web", DNSName: "demo_web", DiscoveredPort: 8080, CreatedAt: now, UpdatedAt: now}
	steps = append(steps, boundaryRead("FROM services", strings.Split(serviceColumns, ","), snapshotRow(t, serviceArgs(&service))...))
	steps = append(steps, boundaryPoolSteps(dialect)...)
	steps = append(steps, boundaryRead("SELECT probe_revision FROM gateway_pools", []string{"revision"}, int64(3)), boundaryWrite("UPDATE gateway_pools SET probe_revision"))
	if dialect == Postgres {
		steps = append(steps, boundaryWrite("pg_notify"))
	}
	return steps
}

func TestBindingCompletionFailuresPreserveAtomicState(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		for _, detach := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/detach=%t", dialect, detach), func(t *testing.T) {
				steps := bindingCompletionSteps(t, dialect, detach)
				for fault := -1; fault <= len(steps); fault++ {
					t.Run(fmt.Sprint(fault), func(t *testing.T) {
						runTransactionBoundary(t, dialect, steps, fault, func(s *SQLStore) error {
							if detach {
								return s.CompleteApplicationDomainDetach(context.Background(), "application", "service", time.Now())
							}
							return s.SetApplicationRouteStatusByID(context.Background(), "application", "route", model.RouteActive, "", time.Now())
						})
					})
				}
			})
		}
	}
}

func TestBindingCompletionRechecksOwnershipAndDesiredState(t *testing.T) {
	for _, stage := range []string{"already active", "active commit fails", "invalid transition", "external service", "missing DNS", "external detach", "new binding wins", "detach commit fails", "activation DNS conflict", "activation DNS conflict without detail", "concurrent route update"} {
		t.Run(stage, func(t *testing.T) {
			now := time.Now()
			script := &queryScript{t: t, transactions: true}
			failure := errors.New("completion commit unavailable")
			detach := strings.Contains(stage, "detach") || stage == "new binding wins"
			commit := false
			wantConflict := false
			if detach {
				managed := stage != "external detach"
				script.replies = []*queryReply{boundaryRead("SELECT rt.pool_id,sv.managed", []string{"pool", "managed"}, "pool", managed).query}
				if managed {
					script.replies = append(script.replies, boundaryRead("SELECT COUNT(*) FROM routes", []string{"count"}, int64(1)).query)
					commit = true
					if stage == "detach commit fails" {
						script.commitErr = failure
					}
				} else {
					wantConflict = true
				}
			} else {
				current := model.RoutePending
				managed := true
				dns := "demo_web"
				switch stage {
				case "already active", "active commit fails":
					current = model.RouteActive
					commit = true
					if stage == "active commit fails" {
						script.commitErr = failure
					}
				case "invalid transition":
					current = model.RouteDisabled
					wantConflict = true
				case "external service":
					managed = false
					wantConflict = true
				case "missing DNS":
					dns = "  "
					wantConflict = true
				}
				script.replies = []*queryReply{boundaryRead("SELECT rt.pool_id,r.route_status", []string{"pool", "status", "service", "dns", "managed"}, "pool", string(current), "service", dns, managed).query}
				if current == model.RoutePending {
					for _, s := range boundaryPlaneSteps(false) {
						script.replies = append(script.replies, s.query)
					}
					script.replies = append(script.replies, boundaryEmpty("FROM services").query)
					if !wantConflict {
						script.execs = append(script.execs, boundaryWrite("UPDATE services SET dns_name").write)
						script.replies = append(script.replies, boundaryEmpty("FROM services").query)
						dnsStatus := model.DNSConflict
						detail := "duplicate service identity"
						if stage == "activation DNS conflict without detail" {
							detail = ""
						}
						if stage == "concurrent route update" {
							dnsStatus = model.DNSReady
							detail = ""
						}
						script.replies = append(script.replies, boundaryRead("SELECT dns_status,dns_last_error", []string{"status", "error"}, string(dnsStatus), detail).query)
						write := boundaryWrite("UPDATE routes SET route_status").write
						if stage == "concurrent route update" {
							write.affected = 0
						} else {
							commit = true
							for _, s := range boundaryPlaneSteps(false) {
								script.replies = append(script.replies, s.query)
							}
							script.replies = append(script.replies, boundaryEmpty("FROM services").query)
						}
						script.execs = append(script.execs, write)
						wantConflict = true
					}
				}
			}
			store := storeWithScript(t, script)
			var e error
			if detach {
				e = store.CompleteApplicationDomainDetach(context.Background(), "application", "service", now)
			} else {
				e = store.SetApplicationRouteStatusByID(context.Background(), "application", "route", model.RouteActive, "", now)
			}
			if script.commitErr != nil {
				if !errors.Is(e, failure) {
					t.Fatal("commit failure hidden", e)
				}
			} else if stage == "concurrent route update" {
				if !errors.Is(e, ErrNotFound) {
					t.Fatal("missing route was overwritten", e)
				}
			} else if wantConflict {
				if !errors.Is(e, ErrConflict) {
					t.Fatal("ownership/state conflict hidden", e)
				}
			} else if e != nil {
				t.Fatal(e)
			}
			if commit {
				if script.commits != 1 {
					t.Fatal("completion did not commit accepted state")
				}
			} else if script.commits != 0 || script.rollbacks != 1 {
				t.Fatal("rejected completion changed state")
			}
		})
	}
}

func TestBindingCompletionRowErrorsNeverCommit(t *testing.T) {
	for _, detach := range []bool{false, true} {
		steps := bindingCompletionSteps(t, SQLite, detach)
		for index, step := range steps {
			if step.query == nil {
				continue
			}
			for _, stage := range []string{"scan", "iteration"} {
				if stage == "iteration" && !strings.Contains(step.query.contains, "FROM services") && !strings.Contains(step.query.contains, "FROM domains") && !strings.Contains(step.query.contains, "FROM routes r JOIN") {
					continue
				}
				t.Run(fmt.Sprintf("detach=%t/%d/%s", detach, index, stage), func(t *testing.T) {
					script := &queryScript{t: t, transactions: true}
					failure := errors.New("binding rows interrupted")
					for i, s := range steps {
						if s.query != nil {
							q := *s.query
							if i == index {
								if stage == "scan" {
									q.columns = []string{"invalid"}
									q.values = [][]driver.Value{{nil}}
								} else {
									q.rowErr = failure
								}
							}
							script.replies = append(script.replies, &q)
						} else {
							w := *s.write
							script.execs = append(script.execs, &w)
						}
						if i == index {
							break
						}
					}
					store := storeWithScript(t, script)
					var e error
					if detach {
						e = store.CompleteApplicationDomainDetach(context.Background(), "application", "service", time.Now())
					} else {
						e = store.SetApplicationRouteStatusByID(context.Background(), "application", "route", model.RouteActive, "", time.Now())
					}
					if e == nil || script.commits != 0 || script.rollbacks != 1 {
						t.Fatal("partial row failure changed binding", e)
					}
					if stage == "iteration" && !errors.Is(e, failure) {
						t.Fatal(e)
					}
				})
			}
		}
	}
}
