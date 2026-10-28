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

func TestRuntimeObservationFailuresRollBackDiscoveryState(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		for _, operation := range []string{"offline", "empty snapshot", "removed service", "removed managed service", "already removed"} {
			t.Run(string(dialect)+"/"+operation, func(t *testing.T) {
				now := time.Now()
				steps := []transactionBoundaryStep{}
				var run func(*SQLStore) error
				if operation == "offline" {
					steps = []transactionBoundaryStep{boundaryRead("SELECT observation_epoch,applied_observation,enabled", []string{"issued", "applied", "enabled"}, int64(2), int64(1), true), boundaryWrite("UPDATE runtimes SET status"), boundaryWrite("UPDATE service_instances SET stale_since")}
					run = func(s *SQLStore) error {
						_, e := s.MarkRuntimeOffline(context.Background(), "runtime", 2, "disconnected", now)
						return e
					}
				} else {
					services := boundaryEmpty("FROM services WHERE runtime_id=")
					if operation != "empty snapshot" {
						v := model.Service{ID: "service", RuntimeID: "runtime", ServiceKey: "demo/web", Managed: operation == "removed managed service", CreatedAt: now, UpdatedAt: now}
						if operation == "already removed" {
							v.Status = model.ServiceRemoved
						}
						services = boundaryRead("FROM services WHERE runtime_id=", strings.Split(serviceColumns, ","), snapshotRow(t, serviceArgs(&v))...)
					}
					steps = []transactionBoundaryStep{boundaryRead("SELECT pool_id,observation_epoch,applied_observation,enabled", []string{"pool", "issued", "applied", "enabled"}, "pool", int64(2), int64(1), true), services, boundaryEmpty("FROM service_instances"), boundaryEmpty("FROM routes r JOIN services"), boundaryEmpty("FROM domains WHERE pool_id=")}
					steps = append(steps, boundaryPlaneSteps(false)...)
					steps = append(steps, boundaryEmpty("FROM services"), boundaryEmpty("SELECT parent_id FROM control_documents WHERE kind IN"))
					if operation == "removed service" || operation == "removed managed service" {
						steps = append(steps, boundaryWrite("UPDATE services SET status"), boundaryWrite("UPDATE service_instances SET ready_state"), boundaryWrite("UPDATE routes SET enabled"))
					}
					steps = append(steps, boundaryEmpty("FROM services"), boundaryEmpty("FROM routes r"))
					steps = append(steps, boundaryPlaneSteps(false)...)
					steps = append(steps, boundaryEmpty("FROM services"), boundaryWrite("INSERT INTO control_documents"), boundaryWrite("UPDATE runtimes SET status"), boundaryRead("SELECT desired_revision FROM gateway_pools", []string{"revision"}, int64(7)))
					run = func(s *SQLStore) error {
						changed, revision, e := s.applyRuntimeSnapshotOnce(context.Background(), model.RuntimeSnapshot{RuntimeID: "runtime", Observation: 2, ObservedAt: now})
						if e == nil && (changed || revision != 7) {
							t.Fatal("metadata-only observation published configuration", changed, revision)
						}
						return e
					}
				}
				for fault := -1; fault <= len(steps); fault++ {
					t.Run(fmt.Sprint(fault), func(t *testing.T) { runTransactionBoundary(t, dialect, steps, fault, run) })
				}
			})
		}
	}
}

func lifecycleSweepSteps(t *testing.T, retained bool) []transactionBoundaryStep {
	now := time.Now()
	endpoint := model.Endpoint{ID: "endpoint", ServiceID: "service", ExternalID: "task", CreatedAt: now, UpdatedAt: now}
	steps := []transactionBoundaryStep{boundaryRead("SELECT pool_id,observation_epoch,applied_observation", []string{"pool", "issued", "applied"}, "pool", int64(2), int64(2))}
	if !retained {
		steps = append(steps, boundaryRead("FROM service_instances", strings.Split(endpointColumns, ","), snapshotRow(t, endpointArgs(&endpoint))...), boundaryWrite("UPDATE service_instances SET ready_state"), boundaryWrite("UPDATE service_instances SET ready_state"), boundaryWrite("DELETE FROM service_instances"))
	}
	steps = append(steps, boundaryRead("SELECT id FROM services WHERE runtime_id=", []string{"id"}, "service"), boundaryRead("SELECT desired_instances,running_instances", []string{"desired", "running"}, int64(1), int64(1)), boundaryRead("SELECT COALESCE(SUM", []string{"running", "ready"}, int64(0), int64(0)), boundaryWrite("UPDATE services SET desired_instances"), boundaryRead("SELECT desired_revision FROM gateway_pools", []string{"revision"}, int64(7)))
	return steps
}
func TestEndpointLifecycleFailuresPreservePriorReadiness(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		for _, retained := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/retained=%t", dialect, retained), func(t *testing.T) {
				steps := lifecycleSweepSteps(t, retained)
				for fault := -1; fault <= len(steps); fault++ {
					t.Run(fmt.Sprint(fault), func(t *testing.T) {
						runTransactionBoundary(t, dialect, steps, fault, func(s *SQLStore) error {
							policy := "drain-after-timeout"
							drain, retention := time.Minute, time.Hour
							if retained {
								policy = "retain-ready"
								drain = -1
								retention = 0
							}
							changed, revision, e := s.SweepEndpointLifecycle(context.Background(), "runtime", 2, policy, time.Now(), time.Minute, drain, retention)
							if e == nil && (changed || revision != 7) {
								t.Fatal("endpoint lifecycle caused gateway publication", changed, revision)
							}
							return e
						})
					})
				}
			})
		}
	}
}

func TestDiscoveryStateReadFailureCannotMasqueradeAsEmptyInventory(t *testing.T) {
	for index := 0; index < 4; index++ {
		for _, stage := range []string{"query", "scan", "iteration"} {
			t.Run(fmt.Sprintf("%d/%s", index, stage), func(t *testing.T) {
				queries := []string{"FROM services", "FROM service_instances", "FROM routes r", "FROM domains"}
				script := &queryScript{t: t, transactions: true}
				failure := errors.New("inventory rows interrupted")
				for i := 0; i <= index; i++ {
					q := &queryReply{contains: queries[i], columns: []string{"unused"}}
					if i == index {
						switch stage {
						case "query":
							q.err = failure
						case "scan":
							q.values = [][]driver.Value{{nil}}
						case "iteration":
							q.rowErr = failure
						}
					}
					script.replies = append(script.replies, q)
				}
				s := storeWithScript(t, script)
				tx, e := s.db.BeginTx(context.Background(), nil)
				if e != nil {
					t.Fatal(e)
				}
				_, e = loadDiscoveryStateTx(context.Background(), s, tx, "runtime", "pool")
				tx.Rollback()
				if e == nil || script.commits != 0 || script.rollbacks != 1 {
					t.Fatal("failed inventory accepted", e)
				}
				if stage != "scan" && !errors.Is(e, failure) {
					t.Fatal(e)
				}
			})
		}
	}
}

func TestDiscoveryConflictReadInterruptionDoesNotResolveRoutes(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			failure := errors.New("route conflict evidence interrupted")
			q := &queryReply{contains: "FROM routes r", columns: []string{"id", "domain", "path", "priority", "enabled", "error"}, rowErr: failure}
			if partial {
				q.values = [][]driver.Value{{"route", "domain", "/", int64(1), false, discoveryRouteConflictPrefix + " previous duplicate"}}
			}
			script := &queryScript{t: t, transactions: true, replies: []*queryReply{q}}
			s := storeWithScript(t, script)
			ctx := context.Background()
			tx, e := s.db.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			e = resolveDiscoveryRouteConflictsTx(ctx, s, tx, "pool", time.Now())
			tx.Rollback()
			if !errors.Is(e, failure) || script.execCalls != 0 || script.commits != 0 {
				t.Fatal("partial conflict scan treated as complete", e)
			}
		})
	}
}
