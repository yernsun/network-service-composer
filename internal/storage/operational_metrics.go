package storage

import (
	"context"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func (s *SQLStore) ReadOperationalMetrics(ctx context.Context, now time.Time) (OperationalMetrics, error) {
	value := OperationalMetrics{
		RuntimeOnline:        make(map[model.RuntimeType]int),
		PoolDesired:          make(map[string]int64),
		PoolApplied:          make(map[string]int64),
		PoolProbeDesired:     make(map[string]int64),
		PoolProbeApplied:     make(map[string]int64),
		DNSStatuses:          make(map[model.DNSStatus]int),
		Certificates:         make(map[model.CertificateStatus]int),
		CertificateExpiry:    make(map[string]float64),
		DeploymentOperations: make(map[model.DeploymentOperationStatus]int),
		CatalogSources:       make(map[string]int),
	}
	if err := s.db.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM services WHERE status<>:status`), model.ServiceRemoved).Scan(&value.Services); err != nil {
		return value, err
	}
	if err := s.db.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM services WHERE status<>:status AND configuration_error<>''`), model.ServiceRemoved).Scan(&value.InvalidServices); err != nil {
		return value, err
	}
	if err := s.db.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM service_instances WHERE ready_state<>:ready_state`), model.Removed).Scan(&value.Endpoints); err != nil {
		return value, err
	}
	if err := s.db.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM service_instances WHERE runtime_state=:runtime_state AND ready_state=:ready_state AND health_state<>:health_state`),
		model.EndpointRunning, model.Ready, model.HealthUnhealthy).Scan(&value.ReadyEndpoints); err != nil {
		return value, err
	}
	dnsRows, err := s.db.QueryContext(ctx, s.bind(`SELECT dns_status,COUNT(*) FROM services WHERE status<>:status GROUP BY dns_status`), model.ServiceRemoved)
	if err != nil {
		return value, err
	}
	for dnsRows.Next() {
		var status model.DNSStatus
		var count int
		if err = dnsRows.Scan(&status, &count); err != nil {
			dnsRows.Close()
			return value, err
		}
		value.DNSStatuses[status] = count
	}
	if err = closeRows(dnsRows); err != nil {
		return value, err
	}

	runtimeRows, err := s.db.QueryContext(ctx, s.bind(`SELECT runtime_type,COUNT(*) FROM runtimes WHERE enabled=:enabled AND status=:status GROUP BY runtime_type`), true, model.RuntimeOnline)
	if err != nil {
		return value, err
	}
	for runtimeRows.Next() {
		var runtimeType model.RuntimeType
		var count int
		if err = runtimeRows.Scan(&runtimeType, &count); err != nil {
			runtimeRows.Close()
			return value, err
		}
		value.RuntimeOnline[runtimeType] = count
	}
	if err = closeRows(runtimeRows); err != nil {
		return value, err
	}

	poolRows, err := s.db.QueryContext(ctx, `SELECT id,desired_revision,probe_revision FROM gateway_pools ORDER BY id`)
	if err != nil {
		return value, err
	}
	for poolRows.Next() {
		var poolID string
		var revision, probeRevision int64
		if err = poolRows.Scan(&poolID, &revision, &probeRevision); err != nil {
			poolRows.Close()
			return value, err
		}
		value.PoolDesired[poolID] = revision
		value.PoolApplied[poolID] = 0
		value.PoolProbeDesired[poolID] = probeRevision
		value.PoolProbeApplied[poolID] = 0
	}
	if err = closeRows(poolRows); err != nil {
		return value, err
	}

	// The minimum applied revision is the useful pool-level signal: it exposes
	// the slowest enabled gateway without adding a gateway-ID label.
	gatewayRows, err := s.db.QueryContext(ctx, s.bind(`SELECT pool_id,MIN(applied_revision) FROM gateways WHERE enabled=:enabled GROUP BY pool_id`), true)
	if err != nil {
		return value, err
	}
	for gatewayRows.Next() {
		var poolID string
		var revision int64
		if err = gatewayRows.Scan(&poolID, &revision); err != nil {
			gatewayRows.Close()
			return value, err
		}
		value.PoolApplied[poolID] = revision
	}
	if err = closeRows(gatewayRows); err != nil {
		return value, err
	}
	probeRows, err := s.db.QueryContext(ctx, `SELECT p.id,COALESCE(g.applied_probe_revision,0)
		FROM gateway_pools p LEFT JOIN gateways g ON g.id=p.probe_gateway_id AND g.enabled=TRUE ORDER BY p.id`)
	if err != nil {
		return value, err
	}
	for probeRows.Next() {
		var poolID string
		var revision int64
		if err = probeRows.Scan(&poolID, &revision); err != nil {
			probeRows.Close()
			return value, err
		}
		value.PoolProbeApplied[poolID] = revision
	}
	if err = closeRows(probeRows); err != nil {
		return value, err
	}

	certificateRows, err := s.db.QueryContext(ctx, `SELECT status,COUNT(*) FROM certificates GROUP BY status`)
	if err != nil {
		return value, err
	}
	for certificateRows.Next() {
		var status model.CertificateStatus
		var count int
		if err = certificateRows.Scan(&status, &count); err != nil {
			certificateRows.Close()
			return value, err
		}
		value.Certificates[status] = count
	}
	if err = closeRows(certificateRows); err != nil {
		return value, err
	}

	expiryRows, err := s.db.QueryContext(ctx, s.bind(`SELECT pool_id,not_after FROM certificates WHERE status IN (:active_status,:renewing_status) AND not_after IS NOT NULL ORDER BY pool_id,not_after`), model.CertificateActive, model.CertificateRenewing)
	if err != nil {
		return value, err
	}
	for expiryRows.Next() {
		var poolID string
		var notAfter time.Time
		if err = expiryRows.Scan(&poolID, &notAfter); err != nil {
			expiryRows.Close()
			return value, err
		}
		if _, exists := value.CertificateExpiry[poolID]; !exists {
			value.CertificateExpiry[poolID] = notAfter.Sub(now).Seconds()
		}
	}
	if err = closeRows(expiryRows); err != nil {
		return value, err
	}
	operationRows, err := s.db.QueryContext(ctx, `SELECT status,COUNT(*) FROM deployment_operations GROUP BY status`)
	if err != nil {
		return value, err
	}
	for operationRows.Next() {
		var status model.DeploymentOperationStatus
		var count int
		if err = operationRows.Scan(&status, &count); err != nil {
			operationRows.Close()
			return value, err
		}
		value.DeploymentOperations[status] = count
	}
	if err = closeRows(operationRows); err != nil {
		return value, err
	}
	catalogRows, err := s.db.QueryContext(ctx, `SELECT CASE WHEN last_error='' THEN 'healthy' ELSE 'error' END,COUNT(*) FROM catalog_sources GROUP BY CASE WHEN last_error='' THEN 'healthy' ELSE 'error' END`)
	if err != nil {
		return value, err
	}
	for catalogRows.Next() {
		var status string
		var count int
		if err = catalogRows.Scan(&status, &count); err != nil {
			catalogRows.Close()
			return value, err
		}
		value.CatalogSources[status] = count
	}
	if err = closeRows(catalogRows); err != nil {
		return value, err
	}
	return value, nil
}

type rowSet interface {
	Close() error
	Err() error
}

func closeRows(rows rowSet) error {
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	return rows.Close()
}
