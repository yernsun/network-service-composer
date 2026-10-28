package metrics

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type Metrics struct {
	Registry             *prometheus.Registry
	SchedulerActive      prometheus.Gauge
	CollectionAttempt    prometheus.Gauge
	CollectionSuccess    prometheus.Gauge
	CollectionFailed     prometheus.Gauge
	RuntimeUp            *prometheus.GaugeVec
	Services             prometheus.Gauge
	InvalidServices      prometheus.Gauge
	Endpoints            prometheus.Gauge
	ReadyEndpoints       prometheus.Gauge
	DiscoveryReconcile   *prometheus.CounterVec
	DiscoveryDuration    *prometheus.HistogramVec
	DiscoveryErrors      *prometheus.CounterVec
	GatewayDesired       *prometheus.GaugeVec
	GatewayApplied       *prometheus.GaugeVec
	GatewayConfigLag     *prometheus.GaugeVec
	GatewayProbeRevision *prometheus.GaugeVec
	GatewayProbeLag      *prometheus.GaugeVec
	ServiceDNSStatus     *prometheus.GaugeVec
	SnapshotGeneration   prometheus.Histogram
	SnapshotBytes        prometheus.Histogram
	NGINXValidations     *prometheus.CounterVec
	NGINXValidateTime    prometheus.Histogram
	NGINXReloads         *prometheus.CounterVec
	NGINXReloadTime      prometheus.Histogram
	Certificates         *prometheus.GaugeVec
	CertificateExpiry    *prometheus.GaugeVec
	ACMEOrders           *prometheus.CounterVec
	ACMEErrors           *prometheus.CounterVec
	ProbeResults         *prometheus.CounterVec
	DeploymentOperations *prometheus.GaugeVec
	DeploymentQueue      prometheus.Gauge
	DeploymentDuration   *prometheus.HistogramVec
	CatalogSources       *prometheus.GaugeVec
	NodeEventReports     *prometheus.CounterVec
}

func New() *Metrics {
	m := &Metrics{Registry: prometheus.NewRegistry()}
	m.SchedulerActive = prometheus.NewGauge(prometheus.GaugeOpts{Name: "nsc_controller_scheduler_active", Help: "Whether this process currently runs Leader workers; not proof of ingress or storage failover."})
	m.CollectionAttempt = prometheus.NewGauge(prometheus.GaugeOpts{Name: "nsc_operational_collection_attempt_seconds", Help: "Unix time of the last operational metrics collection attempt."})
	m.CollectionSuccess = prometheus.NewGauge(prometheus.GaugeOpts{Name: "nsc_operational_collection_success_seconds", Help: "Unix time of the last successful operational metrics collection; zero means never observed."})
	m.CollectionFailed = prometheus.NewGauge(prometheus.GaugeOpts{Name: "nsc_operational_collection_failed", Help: "Whether the latest operational collection failed; previous values retain their original evidence time."})
	m.CollectionFailed.Set(1)
	m.Registry.MustRegister(m.SchedulerActive, m.CollectionAttempt, m.CollectionSuccess, m.CollectionFailed)
	m.RuntimeUp = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "nsc_runtime_up", Help: "Whether at least one enabled runtime of a type is online."}, []string{"runtime_type"})
	m.Services = prometheus.NewGauge(prometheus.GaugeOpts{Name: "nsc_services_total", Help: "Number of known services."})
	m.InvalidServices = prometheus.NewGauge(prometheus.GaugeOpts{Name: "nsc_services_invalid", Help: "Number of discovered services excluded or degraded by invalid configuration."})
	m.Endpoints = prometheus.NewGauge(prometheus.GaugeOpts{Name: "nsc_endpoints_total", Help: "Number of known endpoints."})
	m.ReadyEndpoints = prometheus.NewGauge(prometheus.GaugeOpts{Name: "nsc_endpoints_ready", Help: "Number of endpoints eligible for traffic."})
	m.DiscoveryReconcile = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "nsc_discovery_reconcile_total", Help: "Discovery reconcile attempts."}, []string{"runtime_type", "result"})
	m.DiscoveryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "nsc_discovery_reconcile_duration_seconds", Help: "Discovery reconcile duration.", Buckets: prometheus.DefBuckets}, []string{"runtime_type"})
	m.DiscoveryErrors = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "nsc_discovery_errors_total", Help: "Discovery failures."}, []string{"runtime_type", "operation"})
	m.GatewayDesired = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "nsc_gateway_config_revision", Help: "Current DNS-only Gateway configuration revision."}, []string{"pool"})
	m.GatewayApplied = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "nsc_gateway_applied_config_revision", Help: "Minimum applied configuration revision across enabled Gateways."}, []string{"pool"})
	m.GatewayConfigLag = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "nsc_gateway_config_revision_lag", Help: "Configuration revisions not yet applied by the slowest enabled Gateway."}, []string{"pool"})
	m.GatewayProbeRevision = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "nsc_gateway_probe_revision", Help: "Current Service-DNS probe-plan revision."}, []string{"pool"})
	m.GatewayProbeLag = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "nsc_gateway_probe_revision_lag", Help: "Probe-plan revisions not yet applied by the elected probing Gateway."}, []string{"pool"})
	m.ServiceDNSStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "nsc_service_dns_status", Help: "Known Services by Docker DNS status."}, []string{"status"})
	m.SnapshotGeneration = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "nsc_snapshot_generation_duration_seconds", Help: "Database build and deterministic encoding time for gateway snapshots.", Buckets: prometheus.ExponentialBuckets(0.005, 2, 12)})
	m.SnapshotBytes = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "nsc_snapshot_compressed_bytes", Help: "Compressed deterministic gateway snapshot size.", Buckets: prometheus.ExponentialBuckets(1024, 4, 9)})
	m.NGINXValidations = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "nsc_nginx_validation_total", Help: "NGINX candidate configuration validation attempts reported by gateway agents."}, []string{"result"})
	m.NGINXValidateTime = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "nsc_nginx_validation_duration_seconds", Help: "NGINX candidate configuration validation duration reported by gateway agents.", Buckets: prometheus.ExponentialBuckets(0.005, 2, 12)})
	m.NGINXReloads = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "nsc_nginx_reload_total", Help: "NGINX reload attempts."}, []string{"result"})
	m.NGINXReloadTime = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "nsc_nginx_reload_duration_seconds", Help: "NGINX graceful reload duration reported by gateway agents.", Buckets: prometheus.ExponentialBuckets(0.005, 2, 12)})
	m.Certificates = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "nsc_certificates_total", Help: "Certificates by status."}, []string{"status"})
	m.CertificateExpiry = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "nsc_certificate_expiry_seconds", Help: "Seconds until the nearest certificate expiration."}, []string{"pool"})
	m.ACMEOrders = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "nsc_acme_orders_total", Help: "ACME order attempts."}, []string{"result"})
	m.ACMEErrors = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "nsc_acme_errors_total", Help: "ACME failures."}, []string{"operation"})
	m.ProbeResults = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "nsc_service_dns_probe_results_total", Help: "Service-DNS readiness probe results."}, []string{"mode", "result"})
	m.DeploymentOperations = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "nsc_deployment_operations", Help: "Persisted deployment operations by status."}, []string{"status"})
	m.DeploymentQueue = prometheus.NewGauge(prometheus.GaugeOpts{Name: "nsc_deployment_queue_depth", Help: "Queued deployment operations."})
	m.DeploymentDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "nsc_deployment_duration_seconds", Help: "Application and service operation duration by outcome.", Buckets: prometheus.ExponentialBuckets(0.1, 2, 16)}, []string{"operation", "status"})
	m.CatalogSources = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "nsc_catalog_sources", Help: "Catalog sources by last synchronization health."}, []string{"status"})
	m.NodeEventReports = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "nsc_node_event_reports_total", Help: "Node event report acknowledgements, rejections and queue overflows without event content."}, []string{"node", "result"})
	m.Registry.MustRegister(m.RuntimeUp, m.Services, m.InvalidServices, m.Endpoints, m.ReadyEndpoints, m.DiscoveryReconcile, m.DiscoveryDuration, m.DiscoveryErrors, m.GatewayDesired, m.GatewayApplied, m.GatewayConfigLag, m.GatewayProbeRevision, m.GatewayProbeLag, m.ServiceDNSStatus, m.SnapshotGeneration, m.SnapshotBytes, m.NGINXValidations, m.NGINXValidateTime, m.NGINXReloads, m.NGINXReloadTime, m.Certificates, m.CertificateExpiry, m.ACMEOrders, m.ACMEErrors, m.ProbeResults, m.DeploymentOperations, m.DeploymentQueue, m.DeploymentDuration, m.CatalogSources, m.NodeEventReports)
	m.Registry.MustRegister(prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	return m
}

type StateReporter struct {
	store    storage.Store
	metrics  *Metrics
	interval time.Duration
	logger   *slog.Logger
}

func NewStateReporter(store storage.Store, metrics *Metrics, interval time.Duration, logger *slog.Logger) *StateReporter {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &StateReporter{store: store, metrics: metrics, interval: interval, logger: logger}
}

func (r *StateReporter) Run(ctx context.Context) error {
	r.refresh(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.refresh(ctx)
		}
	}
}

func (r *StateReporter) refresh(ctx context.Context) {
	if r.metrics == nil {
		return
	}
	now := time.Now().UTC()
	r.metrics.CollectionAttempt.Set(float64(now.Unix()))
	read, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := r.store.ReadOperationalMetrics(read, now)
	if err != nil {
		r.metrics.CollectionFailed.Set(1)
		if !errors.Is(err, context.Canceled) {
			r.logger.ErrorContext(ctx, "refresh operational metrics", "error", err)
		}
		return
	}
	r.metrics.CollectionSuccess.Set(float64(now.Unix()))
	r.metrics.CollectionFailed.Set(0)
	r.metrics.Services.Set(float64(state.Services))
	r.metrics.InvalidServices.Set(float64(state.InvalidServices))
	r.metrics.Endpoints.Set(float64(state.Endpoints))
	r.metrics.ReadyEndpoints.Set(float64(state.ReadyEndpoints))
	r.metrics.RuntimeUp.Reset()
	for _, runtimeType := range []model.RuntimeType{model.RuntimeDocker, model.RuntimeSwarm} {
		up := 0.0
		if state.RuntimeOnline[runtimeType] > 0 {
			up = 1
		}
		r.metrics.RuntimeUp.WithLabelValues(string(runtimeType)).Set(up)
	}
	r.metrics.GatewayDesired.Reset()
	r.metrics.GatewayApplied.Reset()
	r.metrics.GatewayConfigLag.Reset()
	r.metrics.GatewayProbeRevision.Reset()
	r.metrics.GatewayProbeLag.Reset()
	for poolID, desired := range state.PoolDesired {
		applied := state.PoolApplied[poolID]
		probeDesired := state.PoolProbeDesired[poolID]
		probeApplied := state.PoolProbeApplied[poolID]
		r.metrics.GatewayDesired.WithLabelValues(poolID).Set(float64(desired))
		r.metrics.GatewayApplied.WithLabelValues(poolID).Set(float64(applied))
		r.metrics.GatewayConfigLag.WithLabelValues(poolID).Set(float64(max(desired-applied, 0)))
		r.metrics.GatewayProbeRevision.WithLabelValues(poolID).Set(float64(probeDesired))
		r.metrics.GatewayProbeLag.WithLabelValues(poolID).Set(float64(max(probeDesired-probeApplied, 0)))
	}
	r.metrics.ServiceDNSStatus.Reset()
	for _, status := range []model.DNSStatus{model.DNSReady, model.DNSMissing, model.DNSConflict, model.DNSUnreachable} {
		r.metrics.ServiceDNSStatus.WithLabelValues(string(status)).Set(float64(state.DNSStatuses[status]))
	}
	r.metrics.Certificates.Reset()
	for _, status := range []model.CertificateStatus{
		model.CertificatePending, model.CertificateAuthorizing, model.CertificateValidating,
		model.CertificateIssued, model.CertificateActive, model.CertificateRenewing,
		model.CertificateExpired, model.CertificateRevoked, model.CertificateError,
	} {
		r.metrics.Certificates.WithLabelValues(string(status)).Set(float64(state.Certificates[status]))
	}
	r.metrics.CertificateExpiry.Reset()
	for poolID, seconds := range state.CertificateExpiry {
		r.metrics.CertificateExpiry.WithLabelValues(poolID).Set(seconds)
	}
	r.metrics.DeploymentOperations.Reset()
	for status, count := range state.DeploymentOperations {
		r.metrics.DeploymentOperations.WithLabelValues(string(status)).Set(float64(count))
	}
	r.metrics.DeploymentQueue.Set(float64(state.DeploymentOperations[model.OperationQueued]))
	r.metrics.CatalogSources.Reset()
	for status, count := range state.CatalogSources {
		r.metrics.CatalogSources.WithLabelValues(status).Set(float64(count))
	}
}
