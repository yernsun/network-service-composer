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

func snapshotPoolReply(revision int64, enabled bool) *queryReply {
	now := time.Unix(100, 0).UTC()
	return &queryReply{contains: "FROM gateway_pools", columns: strings.Split(poolColumns, ","), values: [][]driver.Value{{"pool", "entry", "backend", "challenge:80", enabled, revision, int64(3), "gateway", int64(2), "", nil, int64(1), now, now}}}
}
func snapshotRow(t *testing.T, values []any) []driver.Value {
	t.Helper()
	row := make([]driver.Value, len(values))
	for i, value := range values {
		v, e := driver.DefaultParameterConverter.ConvertValue(value)
		if e != nil {
			t.Fatal(e)
		}
		row[i] = v
	}
	return row
}
func snapshotReadReplies(t *testing.T) []*queryReply {
	now := time.Unix(100, 0).UTC()
	domain := model.Domain{ID: "domain", PoolID: "pool", Host: "example.test", Enabled: true, HTTPEnabled: true, CertificateID: "cert", CreatedAt: now, UpdatedAt: now}
	service := model.Service{ID: "service", RuntimeID: "runtime", ServiceKey: "demo/web", QualifiedName: "demo_web", DNSName: "demo_web", DiscoveredPort: 8080, DiscoveredProtocol: "http", DiscoveredEnabled: true, CreatedAt: now, UpdatedAt: now}
	return []*queryReply{snapshotPoolReply(7, true),
		{contains: "FROM control_documents", columns: []string{"id", "kind", "parent", "payload", "revision", "created", "updated"}, values: [][]driver.Value{{"active", "nginx-active", "pool", `{"snippets":[]}`, int64(1), now, now}}},
		{contains: "FROM domains", columns: strings.Split(domainColumns, ","), values: [][]driver.Value{snapshotRow(t, domainArgs(&domain))}},
		{contains: "FROM services", columns: strings.Split(serviceColumns, ","), values: [][]driver.Value{snapshotRow(t, serviceArgs(&service))}},
		{contains: "FROM routes r", columns: strings.Split(routeColumns, ","), values: [][]driver.Value{{"route", "domain", "service", "", "/", int64(10), false, int64(0), "", "", true, true, string(model.RouteActive), string(model.SourceAPI), "", int64(1), now, now}}},
		{contains: "FROM certificates", columns: []string{"id", "pem", "host"}, values: [][]driver.Value{{"cert", "certificate bytes", "example.test"}, {"cert", "certificate bytes", nil}, {"unreferenced", "excluded bytes", "other.test"}}}, snapshotPoolReply(7, true)}
}

func TestSnapshotReadFailuresNeverPublishPartialConfiguration(t *testing.T) {
	for fault := -1; fault < 7; fault++ {
		for _, kind := range []string{"query", "scan", "iteration"} {
			if fault < 0 && kind != "query" || kind == "iteration" && (fault < 2 || fault == 6) {
				continue
			}
			t.Run(fmt.Sprintf("%d/%s", fault, kind), func(t *testing.T) {
				replies := snapshotReadReplies(t)
				failure := errors.New("snapshot read interrupted")
				if fault >= 0 {
					q := replies[fault]
					switch kind {
					case "query":
						q.err = failure
					case "scan":
						q.columns = []string{"invalid"}
						q.values = [][]driver.Value{{nil}}
					case "iteration":
						q.rowErr = failure
					}
					replies = replies[:fault+1]
				}
				snapshot, e := scriptedStore(t, replies...).BuildSnapshot(context.Background(), "pool")
				if fault < 0 {
					if e != nil || snapshot.Revision != 7 || len(snapshot.Routes) != 1 || snapshot.Routes[0].TargetPort != 8080 || snapshot.Routes[0].UpstreamProtocol != "http" || len(snapshot.Services) != 1 || len(snapshot.Certificates) != 1 || len(snapshot.Certificates["cert"].Domains) != 1 || snapshot.Certificates["cert"].CertificatePEM != "certificate bytes" {
						t.Fatalf("incomplete snapshot: %+v %v", snapshot, e)
					}
					return
				}
				if e == nil {
					t.Fatal("partial database evidence accepted")
				}
				if kind != "scan" && !errors.Is(e, failure) {
					t.Fatal("original read failure lost", e)
				}
			})
		}
	}
}

func TestSnapshotRetriesRequireOneStablePoolRevision(t *testing.T) {
	for _, stable := range []bool{false, true} {
		t.Run(fmt.Sprint(stable), func(t *testing.T) {
			var replies []*queryReply
			for i := 0; i < 5; i++ {
				revision := int64(i + 1)
				replies = append(replies, snapshotPoolReply(revision, false), &queryReply{contains: "FROM control_documents", columns: []string{"empty"}})
				last := revision + 1
				if stable && i == 4 {
					last = revision
				}
				replies = append(replies, snapshotPoolReply(last, false))
			}
			snapshot, e := scriptedStore(t, replies...).BuildSnapshot(context.Background(), "pool")
			if stable {
				if e != nil || snapshot.Revision != 5 || len(snapshot.Routes) != 0 {
					t.Fatal("stable disabled pool snapshot lost", snapshot, e)
				}
			} else if !errors.Is(e, ErrConflict) || snapshot.PoolID != "" {
				t.Fatal("continually changing snapshot accepted", snapshot, e)
			}
		})
	}
}

func TestSnapshotRejectsCorruptActiveConfiguration(t *testing.T) {
	replies := snapshotReadReplies(t)[:2]
	replies[1].values[0][3] = "{"
	v, e := scriptedStore(t, replies...).BuildSnapshot(context.Background(), "pool")
	if e == nil || v.PoolID != "" {
		t.Fatal("corrupt active configuration accepted", v, e)
	}
}

func TestProbePlanReadFailuresPreserveUnknownState(t *testing.T) {
	for fault := 0; fault < 2; fault++ {
		for _, kind := range []string{"query", "scan", "iteration"} {
			if fault == 0 && kind == "iteration" {
				continue
			}
			t.Run(fmt.Sprintf("%d/%s", fault, kind), func(t *testing.T) {
				replies := []*queryReply{snapshotPoolReply(7, true), {contains: "FROM services", columns: strings.Split(serviceColumns, ",")}}
				failure := errors.New("probe inventory unavailable")
				q := replies[fault]
				switch kind {
				case "query":
					q.err = failure
				case "scan":
					q.columns = []string{"invalid"}
					q.values = [][]driver.Value{{nil}}
				case "iteration":
					q.rowErr = failure
				}
				v, e := scriptedStore(t, replies[:fault+1]...).BuildProbePlan(context.Background(), "pool")
				if e == nil || len(v.Targets) != 0 {
					t.Fatal("failed inventory presented as usable probe plan", v, e)
				}
				if kind != "scan" && !errors.Is(e, failure) {
					t.Fatal(e)
				}
			})
		}
	}
	v, e := scriptedStore(t, snapshotPoolReply(7, false)).BuildProbePlan(context.Background(), "pool")
	if e != nil || len(v.Targets) != 0 || v.Epoch != 2 || v.Revision != 3 {
		t.Fatal("disabled pool emitted probes or lost identity", v, e)
	}
}

func TestSnapshotHistoryFailureRetainsPreviousRecoveryRecords(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%t", dialect, existing), func(t *testing.T) {
				steps := []transactionBoundaryStep{boundaryRead("SELECT id FROM gateway_pools", []string{"id"}, "pool")}
				if existing {
					steps = append(steps, boundaryRead("SELECT sha256 FROM pool_snapshots", []string{"sha"}, "digest"), boundaryWrite("UPDATE pool_snapshots SET public_payload"))
				} else {
					steps = append(steps, boundaryEmpty("SELECT sha256 FROM pool_snapshots"), boundaryWrite("INSERT INTO pool_snapshots"))
				}
				steps = append(steps, boundaryWrite("UPDATE gateway_pools SET last_snapshot_sha256"), boundaryRead("SELECT id FROM pool_snapshots", []string{"id"}, "newest"))
				steps[4].query.values = append(steps[4].query.values, []driver.Value{"second"}, []driver.Value{"third"}, []driver.Value{"oldest"})
				steps = append(steps, boundaryWrite("DELETE FROM pool_snapshots"))
				for fault := -1; fault <= len(steps); fault++ {
					t.Run(fmt.Sprint(fault), func(t *testing.T) {
						runTransactionBoundary(t, dialect, steps, fault, func(s *SQLStore) error {
							return s.RecordSnapshot(context.Background(), "pool", 7, "digest", []byte("public config"), time.Now())
						})
					})
				}
			})
		}
	}
}
