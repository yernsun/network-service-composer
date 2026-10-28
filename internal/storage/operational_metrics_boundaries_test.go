package storage

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"github.com/yernsun/network-service-composer/internal/model"
	"testing"
	"time"
)

func TestOperationalMetricsRetainEvidenceAndPropagatePartialReadFailures(t *testing.T) {
	now := time.Now().UTC()
	steps := []transactionBoundaryStep{
		boundaryRead("SELECT COUNT(*) FROM services WHERE status<>", []string{"count"}, int64(5)),
		boundaryRead("configuration_error<>''", []string{"count"}, int64(1)),
		boundaryRead("SELECT COUNT(*) FROM service_instances WHERE ready_state<>", []string{"count"}, int64(8)),
		boundaryRead("SELECT COUNT(*) FROM service_instances WHERE runtime_state=", []string{"count"}, int64(4)),
		boundaryRead("SELECT dns_status,COUNT(*)", []string{"status", "count"}, string(model.DNSReady), int64(3)),
		boundaryRead("SELECT runtime_type,COUNT(*)", []string{"type", "count"}, string(model.RuntimeDocker), int64(2)),
		boundaryRead("SELECT id,desired_revision,probe_revision", []string{"id", "revision", "probe"}, "pool", int64(7), int64(9)),
		boundaryRead("SELECT pool_id,MIN(applied_revision)", []string{"pool", "revision"}, "pool", int64(5)),
		boundaryRead("SELECT p.id,COALESCE", []string{"pool", "revision"}, "pool", int64(8)),
		boundaryRead("SELECT status,COUNT(*) FROM certificates", []string{"status", "count"}, string(model.CertificateRenewing), int64(2)),
		boundaryRead("SELECT pool_id,not_after", []string{"pool", "not_after"}, "pool", now.Add(-time.Hour)),
		boundaryRead("SELECT status,COUNT(*) FROM deployment_operations", []string{"status", "count"}, string(model.OperationFailed), int64(2)),
		boundaryRead("SELECT CASE WHEN last_error=", []string{"status", "count"}, "error", int64(1)),
	}
	// Multiple certificates in one pool retain the earliest expiry, including an expired active certificate.
	steps[10].query.values = append(steps[10].query.values, []driver.Value{"pool", now.Add(time.Hour)})
	for fault := -1; fault < len(steps); fault++ {
		for _, stage := range []string{"query", "scan", "iteration"} {
			if fault < 0 && stage != "query" || fault >= 0 && fault < 4 && stage == "iteration" {
				continue
			}
			t.Run(fmt.Sprintf("%d/%s", fault, stage), func(t *testing.T) {
				failure := errors.New("metrics query interrupted")
				script := &queryScript{t: t}
				for i, step := range steps {
					q := *step.query
					if i == fault {
						switch stage {
						case "query":
							q.err = failure
						case "scan":
							q.columns = []string{"invalid"}
							q.values = [][]driver.Value{{"not a count"}}
						case "iteration":
							q.rowErr = failure
						}
					}
					script.replies = append(script.replies, &q)
					if i == fault {
						break
					}
				}
				s := storeWithScript(t, script)
				v, e := s.ReadOperationalMetrics(context.Background(), now)
				if fault < 0 {
					if e != nil || v.Services != 5 || v.InvalidServices != 1 || v.Endpoints != 8 || v.ReadyEndpoints != 4 || v.PoolDesired["pool"] != 7 || v.PoolApplied["pool"] != 5 || v.PoolProbeDesired["pool"] != 9 || v.PoolProbeApplied["pool"] != 8 || v.CertificateExpiry["pool"] != -3600 || v.Certificates[model.CertificateRenewing] != 2 || v.DeploymentOperations[model.OperationFailed] != 2 || v.CatalogSources["error"] != 1 || v.RuntimeOnline[model.RuntimeDocker] != 2 || v.DNSStatuses[model.DNSReady] != 3 {
						t.Fatalf("operational evidence mismatch: %+v %v", v, e)
					}
					return
				}
				if e == nil {
					t.Fatal("partial metrics read presented as successful")
				}
				if stage != "scan" && !errors.Is(e, failure) {
					t.Fatal("metrics failure cause lost", e)
				}
			})
		}
	}
}
