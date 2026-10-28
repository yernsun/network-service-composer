package metrics

import (
	"bytes"
	"context"
	"errors"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"log/slog"
	"testing"
	"time"
)

type metricsBoundaryStore struct {
	storage.Store
	state   storage.OperationalMetrics
	failure error
	calls   int
	cancel  context.CancelFunc
}

func (s *metricsBoundaryStore) ReadOperationalMetrics(_ context.Context, now time.Time) (storage.OperationalMetrics, error) {
	s.calls++
	if s.cancel != nil && s.calls == 2 {
		s.cancel()
	}
	return s.state, s.failure
}
func metricValue(t *testing.T, m *Metrics, name, label, value string) (float64, bool) {
	t.Helper()
	families, e := m.Registry.Gather()
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, metric := range f.Metric {
			match := label == ""
			for _, l := range metric.Label {
				if l.GetName() == label && l.GetValue() == value {
					match = true
				}
			}
			if match {
				return metric.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}
func TestStateReporterPreservesFailuresAndRemovesStaleSeries(t *testing.T) {
	store := &metricsBoundaryStore{state: storage.OperationalMetrics{Services: 5, InvalidServices: 2, Endpoints: 8, ReadyEndpoints: 6, RuntimeOnline: map[model.RuntimeType]int{model.RuntimeDocker: 2}, PoolDesired: map[string]int64{"old": 7, "ahead": 2}, PoolApplied: map[string]int64{"old": 5, "ahead": 4}, PoolProbeDesired: map[string]int64{"old": 8, "ahead": 1}, PoolProbeApplied: map[string]int64{"old": 3, "ahead": 2}, DNSStatuses: map[model.DNSStatus]int{model.DNSReady: 3}, Certificates: map[model.CertificateStatus]int{model.CertificateActive: 2}, CertificateExpiry: map[string]float64{"old": -5}, DeploymentOperations: map[model.DeploymentOperationStatus]int{model.OperationQueued: 3}, CatalogSources: map[string]int{"error": 2}}}
	m := New()
	var log bytes.Buffer
	r := NewStateReporter(store, m, 0, slog.New(slog.NewTextHandler(&log, nil)))
	if r.interval != 15*time.Second {
		t.Fatal("default refresh interval lost")
	}
	r.refresh(context.Background())
	checks := []struct {
		name, label, value string
		want               float64
	}{
		{"nsc_services_total", "", "", 5}, {"nsc_services_invalid", "", "", 2}, {"nsc_endpoints_total", "", "", 8}, {"nsc_endpoints_ready", "", "", 6},
		{"nsc_runtime_up", "runtime_type", string(model.RuntimeDocker), 1}, {"nsc_runtime_up", "runtime_type", string(model.RuntimeSwarm), 0},
		{"nsc_gateway_config_revision_lag", "pool", "old", 2}, {"nsc_gateway_config_revision_lag", "pool", "ahead", 0}, {"nsc_gateway_probe_revision_lag", "pool", "old", 5}, {"nsc_gateway_probe_revision_lag", "pool", "ahead", 0},
		{"nsc_service_dns_status", "status", string(model.DNSReady), 3}, {"nsc_certificates_total", "status", string(model.CertificateActive), 2}, {"nsc_certificate_expiry_seconds", "pool", "old", -5}, {"nsc_deployment_queue_depth", "", "", 3}, {"nsc_catalog_sources", "status", "error", 2},
	}
	for _, c := range checks {
		if got, ok := metricValue(t, m, c.name, c.label, c.value); !ok || got != c.want {
			t.Fatalf("%s[%s]=%v exists=%v want=%v", c.name, c.value, got, ok, c.want)
		}
	}
	store.state = storage.OperationalMetrics{}
	store.failure = errors.New("isolated metrics failure")
	r.refresh(context.Background())
	if got, _ := metricValue(t, m, "nsc_services_total", "", ""); got != 5 || log.Len() == 0 {
		t.Fatal("read failure reset last known state or was not recorded")
	}
	log.Reset()
	store.failure = context.Canceled
	r.refresh(context.Background())
	if log.Len() != 0 {
		t.Fatal("expected cancellation logged as storage failure")
	}
	store.failure = nil
	r.refresh(context.Background())
	if got, _ := metricValue(t, m, "nsc_services_total", "", ""); got != 0 {
		t.Fatal("successful empty inventory kept stale totals")
	}
	for _, c := range []struct{ name, label, value string }{{"nsc_gateway_config_revision", "pool", "old"}, {"nsc_certificate_expiry_seconds", "pool", "old"}, {"nsc_deployment_operations", "status", string(model.OperationQueued)}, {"nsc_catalog_sources", "status", "error"}} {
		if _, ok := metricValue(t, m, c.name, c.label, c.value); ok {
			t.Fatalf("removed resource metric retained: %s", c.name)
		}
	}
}
func TestStateReporterLifecycleAndDisabledMetrics(t *testing.T) {
	disabledStore := &metricsBoundaryStore{}
	NewStateReporter(disabledStore, nil, time.Millisecond, nil).refresh(context.Background())
	if disabledStore.calls != 0 {
		t.Fatal("disabled reporting queried store")
	}
	for _, immediate := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		store := &metricsBoundaryStore{cancel: cancel}
		if immediate {
			cancel()
		}
		r := NewStateReporter(store, New(), time.Millisecond, nil)
		if e := r.Run(ctx); !errors.Is(e, context.Canceled) {
			t.Fatal("reporter lost cancellation", e)
		}
		if !immediate && store.calls < 2 {
			t.Fatal("periodic refresh did not run")
		}
		cancel()
	}
}

func TestCollectionEvidenceDoesNotRefreshSuccessOnFailure(t *testing.T) {
	store := &metricsBoundaryStore{}
	m := New()
	reporter := NewStateReporter(store, m, time.Second, nil)
	if got, _ := metricValue(t, m, "nsc_operational_collection_success_seconds", "", ""); got != 0 {
		t.Fatal("fabricated initial success", got)
	}
	reporter.refresh(t.Context())
	success, _ := metricValue(t, m, "nsc_operational_collection_success_seconds", "", "")
	if success <= 0 {
		t.Fatal("successful observation has no time")
	}
	store.failure = errors.New("fixture read unavailable")
	reporter.refresh(t.Context())
	if got, _ := metricValue(t, m, "nsc_operational_collection_success_seconds", "", ""); got != success {
		t.Fatal("failure extended healthy evidence", got, success)
	}
	if got, _ := metricValue(t, m, "nsc_operational_collection_failed", "", ""); got != 1 {
		t.Fatal("failure hidden", got)
	}
	store.failure = nil
	reporter.refresh(t.Context())
	if got, _ := metricValue(t, m, "nsc_operational_collection_failed", "", ""); got != 0 {
		t.Fatal("new successful observation stayed failed", got)
	}
}
