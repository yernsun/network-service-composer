package storage

import (
	"context"
	"database/sql/driver"
	"fmt"
	"github.com/yernsun/network-service-composer/internal/model"
	"testing"
	"time"
)

func serviceProbeBoundaryRead() (transactionBoundaryStep, model.ProbeResult) {
	policy := normalizedDNSProbePolicy(model.ReadinessPolicy{Mode: model.ReadinessHTTP, Path: "/health", Scheme: "http", SuccessThreshold: 1, FailureThreshold: 1})
	values := []driver.Value{"web.internal", string(model.DNSUnreachable), "last failure", true, int64(8080), nil, string(policy.Mode), policy.Path, policy.Scheme, int64(policy.Interval), int64(policy.Timeout), int64(policy.SuccessThreshold), int64(policy.FailureThreshold), int64(0), int64(0), false, nil, int64(3), int64(5), "pool", "", true, nil, true, true}
	columns := make([]string, len(values))
	for i := range columns {
		columns[i] = fmt.Sprint(i)
	}
	result := model.ProbeResult{PoolID: "pool", ServiceID: "service", Epoch: 3, Revision: 5, Sequence: 2, TargetToken: serviceDNSProbeToken(policy, "web.internal", 8080), Success: true, ObservedAt: time.Now().UTC()}
	return boundaryRead("FROM services s JOIN runtimes", columns, values...), result
}

func serviceProbeLockSteps() []transactionBoundaryStep {
	return []transactionBoundaryStep{
		boundaryEmpty("SELECT payload,kind FROM control_documents"),
		boundaryEmpty("SELECT kind,parent_id,payload FROM control_documents"),
		boundaryEmpty("kind='application-environment'"),
		boundaryRead("SELECT application_id,runtime_id,runtime_object_id FROM services", []string{"application", "runtime", "object"}, nil, "runtime", "object"),
		boundaryEmpty("kind='system-service'"),
		boundaryRead("SELECT id FROM services WHERE id=", []string{"id"}, "service"),
	}
}
func TestServiceProbeStorageFailuresPreserveLastConfirmedState(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		for _, existing := range []bool{false, true} {
			for _, success := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/existing=%t/success=%t", dialect, existing, success), func(t *testing.T) {
					read, result := serviceProbeBoundaryRead()
					result.Success = success
					result.Error = "latest failure"
					if !success {
						read.query.values[0][1] = string(model.DNSReady)
						read.query.values[0][2] = ""
					}
					state := boundaryEmpty("FROM service_dns_probes")
					write := boundaryWrite("INSERT INTO service_dns_probes")
					if existing {
						state = boundaryRead("FROM service_dns_probes", []string{"success", "failure", "sequence", "token"}, int64(0), int64(0), int64(1), result.TargetToken)
						write = boundaryWrite("UPDATE service_dns_probes SET")
					}
					steps := append(serviceProbeLockSteps(), read, state, write, boundaryWrite("UPDATE services SET dns_status"))
					for fault := -1; fault <= len(steps); fault++ {
						t.Run(fmt.Sprint(fault), func(t *testing.T) {
							runTransactionBoundary(t, dialect, steps, fault, func(s *SQLStore) error {
								changed, e := s.UpdateServiceProbe(context.Background(), result)
								if e == nil && !changed {
									t.Error("accepted threshold result did not change DNS state")
								}
								if e != nil && changed {
									t.Error("failed transaction reported a committed DNS transition")
								}
								return e
							})
						})
					}
				})
			}
		}
	}
}
func TestServiceProbeRejectsStaleForeignAndDisabledEvidence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		change    func(*queryReply, *model.ProbeResult)
		malformed bool
	}{
		{"no service", func(_ *queryReply, r *model.ProbeResult) { r.ServiceID = "" }, false},
		{"old epoch", func(_ *queryReply, r *model.ProbeResult) { r.Epoch-- }, false},
		{"old revision", func(_ *queryReply, r *model.ProbeResult) { r.Revision-- }, false},
		{"foreign pool", func(_ *queryReply, r *model.ProbeResult) { r.PoolID = "foreign" }, false},
		{"missing target", func(_ *queryReply, r *model.ProbeResult) { r.TargetToken = "" }, false},
		{"old target", func(_ *queryReply, r *model.ProbeResult) { r.TargetToken = "stale" }, false},
		{"changed port", func(q *queryReply, _ *model.ProbeResult) { q.values[0][5] = int64(9090) }, false},
		{"invalid definition", func(q *queryReply, _ *model.ProbeResult) { q.values[0][20] = "invalid" }, false},
		{"conflicting DNS", func(q *queryReply, _ *model.ProbeResult) { q.values[0][1] = string(model.DNSConflict) }, false},
		{"disabled service", func(q *queryReply, _ *model.ProbeResult) { q.values[0][22] = false }, false},
		{"disabled runtime", func(q *queryReply, _ *model.ProbeResult) { q.values[0][23] = false }, false},
		{"disabled pool", func(q *queryReply, _ *model.ProbeResult) { q.values[0][24] = false }, false},
		{"corrupt readiness", func(q *queryReply, _ *model.ProbeResult) { q.values[0][16] = "{" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read, result := serviceProbeBoundaryRead()
			tc.change(read.query, &result)
			script := &queryScript{t: t, transactions: true}
			if result.ServiceID != "" {
				for _, step := range serviceProbeLockSteps() {
					script.replies = append(script.replies, step.query)
				}
				script.replies = append(script.replies, read.query)
			}
			store := storeWithScript(t, script)
			changed, e := store.UpdateServiceProbe(context.Background(), result)
			if changed || (e != nil) != tc.malformed || script.commits != 0 || script.rollbacks != 1 || script.execCalls != 0 {
				t.Fatalf("untrusted evidence changed state: changed=%t err=%v commits=%d", changed, e, script.commits)
			}
		})
	}
}
