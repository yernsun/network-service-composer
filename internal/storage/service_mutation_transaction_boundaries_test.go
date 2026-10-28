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

func TestServiceOverridesFailAtomicallyAcrossConfigurationAndProbeVersions(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		for _, mode := range []string{"unchanged", "port", "readiness"} {
			t.Run(string(dialect)+"/"+mode, func(t *testing.T) {
				now := time.Now().UTC()
				old := model.Service{ID: "service", RuntimeID: "runtime", DiscoveredPort: 8080, DiscoveredEnabled: true, DiscoveredProtocol: "http", CreatedAt: now, UpdatedAt: now}
				next := old
				if mode == "port" {
					p := 8443
					next.TargetPortOverride = &p
				}
				if mode == "readiness" {
					next.ReadinessOverride = &model.ReadinessPolicy{Mode: model.ReadinessHTTP, Path: "/ready"}
				}
				steps := []transactionBoundaryStep{boundaryRead("FROM services WHERE id", strings.Split(serviceColumns, ","), snapshotRow(t, serviceArgs(&old))...), boundaryRead("SELECT r.pool_id FROM runtimes", []string{"pool"}, "pool"), boundaryWrite("UPDATE services SET protocol_override")}
				if mode == "port" {
					steps = append(steps, boundaryPoolSteps(dialect)...)
				}
				if mode != "unchanged" {
					steps = append(steps, boundaryRead("SELECT probe_revision FROM gateway_pools", []string{"revision"}, int64(5)), boundaryWrite("UPDATE gateway_pools SET probe_revision"))
					if dialect == Postgres {
						steps = append(steps, boundaryWrite("pg_notify"))
					}
				}
				for fault := -1; fault <= len(steps); fault++ {
					t.Run(fmt.Sprint(fault), func(t *testing.T) {
						runTransactionBoundary(t, dialect, steps, fault, func(s *SQLStore) error { v := next; return s.UpdateService(context.Background(), &v, 1) })
					})
				}
			})
		}
	}
}
func TestEndpointRediscoveryPreservesProbeEvidenceAndDrainsStoppedReplica(t *testing.T) {
	for _, mode := range []string{"running", "stopped ready", "stopped unready", "insert race", "read failure", "write failure"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			old := model.Endpoint{ID: "stable", ServiceID: "service", ExternalID: "container", RuntimeState: model.EndpointRunning, ReadyState: model.Ready, SuccessCount: 4, FailureCount: 2, LastProbeSequence: 11, FirstSeenAt: now.Add(-time.Hour), ReadySince: &now, CreatedAt: now, UpdatedAt: now}
			if mode == "stopped unready" {
				old.ReadyState = model.NotReady
			}
			q := boundaryRead("FROM service_instances WHERE service_id", strings.Split(endpointColumns, ","), snapshotRow(t, endpointArgs(&old))...).query
			script := &queryScript{t: t, transactions: true, replies: []*queryReply{q}, execs: []*execReply{{contains: "UPDATE service_instances SET address", affected: 1}}}
			failure := errors.New("endpoint storage unavailable")
			if mode == "insert race" {
				script.replies = append([]*queryReply{{contains: "FROM service_instances WHERE service_id", columns: strings.Split(endpointColumns, ",")}}, q)
				script.execs = append([]*execReply{{contains: "INSERT INTO service_instances", err: errors.New("unique endpoint identity")}}, script.execs...)
			}
			if mode == "read failure" {
				q.err = failure
				script.execs = nil
			}
			if mode == "write failure" {
				script.execs[0].err = failure
			}
			s := storeWithScript(t, script)
			next := model.Endpoint{ID: "new observation", ServiceID: "service", ExternalID: "container", RuntimeState: model.EndpointRunning, UpdatedAt: now}
			if strings.HasPrefix(mode, "stopped") {
				next.RuntimeState = model.EndpointDead
			}
			e := s.UpsertEndpoint(context.Background(), &next)
			if strings.HasSuffix(mode, "failure") {
				if !errors.Is(e, failure) {
					t.Fatal("rediscovery failure hidden", e)
				}
				return
			}
			if e != nil || next.ID != "stable" || next.SuccessCount != 4 || next.FailureCount != 2 || next.LastProbeSequence != 11 || !next.FirstSeenAt.Equal(old.FirstSeenAt) || next.ReadySince == nil {
				t.Fatal("rediscovery lost probe history", next, e)
			}
			if mode == "stopped ready" && (next.ReadyState != model.Draining || next.DrainSince == nil || !next.DrainSince.Equal(now)) {
				t.Fatal("stopped ready replica not drained", next)
			}
			if mode == "stopped unready" && next.DrainSince != nil {
				t.Fatal("unready replica falsely recorded as draining")
			}
			if mode == "insert race" && script.execCalls != 2 {
				t.Fatal("insert race did not recover stable identity")
			}
		})
	}
}
func TestObservationAndProbeRetriesAreBoundedAndCancelable(t *testing.T) {
	for _, kind := range []string{"snapshot", "probe"} {
		for _, mode := range []string{"retry then missing", "retry exhausted", "cancel during delay"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				script := &queryScript{t: t, transactions: true}
				query := "SELECT pool_id,observation_epoch,applied_observation,enabled"
				if kind == "probe" {
					query = "SELECT payload,kind FROM control_documents"
				}
				failures := 1
				if mode == "retry exhausted" {
					failures = 4
				}
				for i := 0; i < failures; i++ {
					script.replies = append(script.replies, &queryReply{contains: query, err: errors.New("database is locked")})
				}
				if mode == "retry then missing" {
					final := &queryReply{contains: query, columns: []string{"missing"}}
					if kind == "probe" {
						final.err = ErrNotFound
					}
					script.replies = append(script.replies, final)
				}
				s := storeWithScript(t, script)
				ctx := context.Background()
				cancel := func() {}
				if mode == "cancel during delay" {
					ctx, cancel = context.WithTimeout(ctx, 2*time.Millisecond)
				}
				defer cancel()
				var e error
				if kind == "snapshot" {
					_, _, e = s.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: "runtime", Observation: 1})
				} else {
					_, e = s.UpdateServiceProbe(ctx, model.ProbeResult{PoolID: "pool", ServiceID: "service"})
				}
				if mode == "retry exhausted" {
					if e == nil || !strings.Contains(e.Error(), "locked") || script.calls != 4 {
						t.Fatal("retry exhaustion not preserved", e, script.calls)
					}
				} else if mode == "cancel during delay" {
					if !errors.Is(e, context.DeadlineExceeded) || script.calls != 1 {
						t.Fatal("canceled retry continued", e, script.calls)
					}
				} else if !errors.Is(e, ErrNotFound) || script.calls != 2 {
					t.Fatal("retry did not expose final result", e, script.calls)
				}
				if script.commits != 0 {
					t.Fatal("failed retry committed")
				}
			})
		}
	}
}
