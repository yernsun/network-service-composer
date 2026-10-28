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

func TestProbeLeadershipAndCertificateCleanupFailuresRollback(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		for _, mode := range []string{"select leader", "retain leader", "disabled pool", "cleanup"} {
			t.Run(string(dialect)+"/"+mode, func(t *testing.T) {
				var steps []transactionBoundaryStep
				var run func(*SQLStore) error
				if mode == "cleanup" {
					steps = []transactionBoundaryStep{boundaryWrite("DELETE FROM acme_challenges"), boundaryWrite("DELETE FROM certificates")}
					run = func(s *SQLStore) error {
						v, e := s.GarbageCollect(context.Background(), time.Now(), 0)
						if e == nil && (v.Certificates != 1 || v.ExpiredChallenges != 1) {
							t.Fatal("cleanup count changed", v)
						}
						return e
					}
				} else {
					leader := "old"
					if mode == "retain leader" {
						leader = "selected"
					}
					now := time.Now().UTC()
					steps = []transactionBoundaryStep{boundaryRead("FROM gateway_pools WHERE id", strings.Split(poolColumns, ","), "pool", "pool", "network", "", mode != "disabled pool", int64(1), int64(2), leader, int64(4), "", nil, int64(1), now, now)}
					if mode != "disabled pool" {
						steps = append(steps, boundaryRead("SELECT id FROM gateways", []string{"id"}, "selected"))
					}
					if mode != "retain leader" {
						steps = append(steps, boundaryWrite("UPDATE gateway_pools SET probe_gateway_id"), boundaryWrite("UPDATE service_dns_probes SET success_count"))
					}
					run = func(s *SQLStore) error {
						v, e := s.SelectProbeLeader(context.Background(), "pool", time.Minute)
						if e == nil {
							want := "selected"
							epoch := int64(5)
							if mode == "disabled pool" {
								want = ""
							}
							if mode == "retain leader" {
								epoch = 4
							}
							if v.ProbeGatewayID != want || v.ProbeEpoch != epoch {
								t.Fatal("leadership fence incorrect", v)
							}
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
func TestCertificateCleanupCannotReportCountsAfterDriverFailure(t *testing.T) {
	for _, first := range []bool{false, true} {
		t.Run(fmt.Sprint(first), func(t *testing.T) {
			failure := errors.New("affected count unavailable")
			a, b := &execReply{contains: "DELETE FROM acme_challenges", affected: 2}, &execReply{contains: "DELETE FROM certificates", affected: 3}
			if first {
				a.affectedErr = failure
			} else {
				b.affectedErr = failure
			}
			script := &queryScript{t: t, transactions: true, execs: []*execReply{a, b}}
			s := storeWithScript(t, script)
			v, e := s.GarbageCollect(context.Background(), time.Now(), time.Hour)
			if !errors.Is(e, failure) || v != (GCResult{}) || script.commits != 0 || script.rollbacks != 1 {
				t.Fatal("cleanup falsely confirmed", v, e)
			}
		})
	}
}
func TestCertificateReplacementFailuresPreserveDistributedMaterial(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		for _, mode := range []string{"same", "new key", "new PEM", "withdraw"} {
			t.Run(string(dialect)+"/"+mode, func(t *testing.T) {
				now := time.Now().UTC()
				old := model.Certificate{ID: "cert", PoolID: "pool", Status: model.CertificateActive, CertificatePEM: "original certificate", EncryptedPrivateKey: []byte("original encrypted key"), Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
				next := old
				if mode == "new key" {
					next.EncryptedPrivateKey = []byte("new encrypted key")
				}
				if mode == "new PEM" {
					next.CertificatePEM = "new certificate"
				}
				if mode == "withdraw" {
					next.Status = model.CertificateRevoked
				}
				steps := []transactionBoundaryStep{boundaryRead("FROM certificates WHERE id", strings.Split(certificateColumns, ","), snapshotRow(t, certificateArgs(&old))...), boundaryRead("SELECT d.host FROM certificate_domains", []string{"host"}, "example.test"), boundaryWrite("UPDATE certificates SET provider")}
				if mode != "same" {
					steps = append(steps, boundaryPoolSteps(dialect)...)
				}
				for fault := -1; fault <= len(steps); fault++ {
					t.Run(fmt.Sprint(fault), func(t *testing.T) {
						failure := errors.New("certificate persistence unavailable")
						script := &queryScript{t: t, transactions: true}
						for i, step := range steps {
							if step.query != nil {
								q := *step.query
								if i == fault {
									q.err = failure
								}
								script.replies = append(script.replies, &q)
							} else {
								w := *step.write
								if i == fault {
									w.err = failure
								}
								script.execs = append(script.execs, &w)
							}
							if i == fault {
								break
							}
						}
						if fault == len(steps) {
							script.commitErr = failure
						}
						s := storeWithScript(t, script)
						s.dialect = dialect
						v := next
						e := s.UpdateCertificate(context.Background(), &v, 1)
						if fault < 0 {
							if e != nil || script.commits != 1 {
								t.Fatal("certificate update not committed", e)
							}
						} else {
							if !errors.Is(e, failure) {
								t.Fatal("original failure hidden", e)
							}
							if fault < len(steps) && script.commits != 0 {
								t.Fatal("partial replacement committed")
							}
							if fault >= 2 && fault < len(steps) && script.rollbacks != 1 {
								t.Fatal("partial replacement not rolled back")
							}
						}
					})
				}
			})
		}
	}
}
