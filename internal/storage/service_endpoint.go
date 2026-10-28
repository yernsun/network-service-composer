package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

const serviceColumns = `id,runtime_id,service_key,runtime_object_id,external_id,application_id,managed,routing_eligible,routing_issues,name,namespace,qualified_name,dns_name,dns_status,dns_last_error,discovered_protocol,protocol_override,
discovered_port,target_port_override,discovered_enabled,enabled_override,
readiness_mode,readiness_path,readiness_scheme,readiness_interval_ns,readiness_timeout_ns,
readiness_success_threshold,readiness_failure_threshold,readiness_startup_grace_ns,
readiness_startup_timeout_ns,readiness_tcp_fallback,readiness_override,desired_instances,running_instances,
runtime_ready_instances,ready_instances,status,configuration_error,last_seen_at,runtime_created_at,runtime_updated_at,removed_at,revision,created_at,updated_at`

func scanService(row scanner) (model.Service, error) {
	var value model.Service
	var protocol sql.NullString
	var targetPort sql.NullInt64
	var enabled sql.NullBool
	var readinessOverride sql.NullString
	var applicationID sql.NullString
	var routingIssues string
	var seen, runtimeCreated, runtimeUpdated, removed sql.NullTime
	var interval, timeout, grace, startup int64
	err := row.Scan(&value.ID, &value.RuntimeID, &value.ServiceKey, &value.RuntimeObjectID, &value.ExternalID, &applicationID,
		&value.Managed, &value.RoutingEligible, &routingIssues, &value.Name, &value.Namespace,
		&value.QualifiedName, &value.DNSName, &value.DNSStatus, &value.DNSLastError,
		&value.DiscoveredProtocol, &protocol, &value.DiscoveredPort, &targetPort, &value.DiscoveredEnabled,
		&enabled, &value.Readiness.Mode, &value.Readiness.Path, &value.Readiness.Scheme,
		&interval, &timeout, &value.Readiness.SuccessThreshold, &value.Readiness.FailureThreshold,
		&grace, &startup, &value.Readiness.TCPFallback, &readinessOverride, &value.DesiredInstances, &value.RunningInstances,
		&value.RuntimeReadyInstances, &value.ReadyInstances, &value.Status, &value.ConfigurationError, &seen,
		&runtimeCreated, &runtimeUpdated, &removed, &value.Revision,
		&value.CreatedAt, &value.UpdatedAt)
	if protocol.Valid {
		v := protocol.String
		value.ProtocolOverride = &v
	}
	if applicationID.Valid {
		value.ApplicationID = applicationID.String
	}
	if routingIssues != "" && err == nil {
		err = json.Unmarshal([]byte(routingIssues), &value.RoutingIssues)
	}
	if value.Managed {
		value.LifecycleOperations = []string{"restart", "scale", "remove_runtime"}
	}
	if targetPort.Valid {
		v := int(targetPort.Int64)
		value.TargetPortOverride = &v
	}
	if enabled.Valid {
		v := enabled.Bool
		value.EnabledOverride = &v
	}
	if readinessOverride.Valid {
		var override model.ReadinessPolicy
		if err == nil {
			err = json.Unmarshal([]byte(readinessOverride.String), &override)
		}
		if err == nil {
			value.ReadinessOverride = &override
		}
	}
	if seen.Valid {
		value.LastSeenAt = &seen.Time
	}
	if runtimeCreated.Valid {
		value.RuntimeCreatedAt = &runtimeCreated.Time
	}
	if runtimeUpdated.Valid {
		value.RuntimeUpdatedAt = &runtimeUpdated.Time
	}
	if removed.Valid {
		value.RemovedAt = &removed.Time
	}
	value.Readiness.Interval = time.Duration(interval)
	value.Readiness.Timeout = time.Duration(timeout)
	value.Readiness.StartupGracePeriod = time.Duration(grace)
	value.Readiness.StartupTimeout = time.Duration(startup)
	return value, mapError(err)
}

func serviceArgs(value *model.Service) []any {
	routingIssues, _ := json.Marshal(value.RoutingIssues)
	return []any{value.ID, value.RuntimeID, value.ServiceKey, value.RuntimeObjectID, value.ExternalID, nullString(value.ApplicationID),
		value.Managed, value.RoutingEligible, string(routingIssues), value.Name, value.Namespace,
		value.QualifiedName, value.DNSName, value.DNSStatus, value.DNSLastError,
		value.DiscoveredProtocol, stringPtr(value.ProtocolOverride), value.DiscoveredPort, intPtr(value.TargetPortOverride),
		value.DiscoveredEnabled, boolPtr(value.EnabledOverride),
		value.Readiness.Mode, value.Readiness.Path, value.Readiness.Scheme, int64(value.Readiness.Interval),
		int64(value.Readiness.Timeout), value.Readiness.SuccessThreshold, value.Readiness.FailureThreshold,
		int64(value.Readiness.StartupGracePeriod), int64(value.Readiness.StartupTimeout), value.Readiness.TCPFallback,
		readinessOverrideValue(value.ReadinessOverride), value.DesiredInstances, value.RunningInstances, value.RuntimeReadyInstances,
		value.ReadyInstances, value.Status,
		value.ConfigurationError, ptrTime(value.LastSeenAt), ptrTime(value.RuntimeCreatedAt), ptrTime(value.RuntimeUpdatedAt),
		ptrTime(value.RemovedAt), value.Revision, value.CreatedAt, value.UpdatedAt}
}

func (s *SQLStore) UpsertService(ctx context.Context, value *model.Service) error {
	for attempt := 0; ; attempt++ {
		err := s.upsertServiceOnce(ctx, value)
		if err == nil || attempt >= 3 || (!errors.Is(err, ErrConflict) && !retryableTransactionError(err)) {
			return err
		}
		if err = waitTransactionRetry(ctx, attempt); err != nil {
			return err
		}
	}
}

func (s *SQLStore) upsertServiceOnce(ctx context.Context, value *model.Service) error {
	if value.ServiceKey == "" {
		value.ServiceKey = value.ExternalID
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	existing, err := scanService(tx.QueryRowContext(ctx, s.bind(`SELECT `+serviceColumns+` FROM services WHERE runtime_id=:runtime_id AND service_key=:service_key`), value.RuntimeID, value.ServiceKey))
	if errors.Is(err, ErrNotFound) {
		if err = s.guardProjectResourceTx(ctx, tx, value.ApplicationID); err != nil {
			return err
		}
		query := s.bind(`INSERT INTO services (` + serviceColumns + `) VALUES (` + columnPlaceholders(serviceColumns) + `)`)
		_, err = tx.ExecContext(ctx, query, serviceArgs(value)...)
		if err != nil {
			return mapError(err)
		}
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if err = s.guardProjectResourceTx(ctx, tx, existing.ID); err != nil {
		if errors.Is(err, ErrConflict) {
			*value = existing
			return nil
		}
		return err
	}
	value.ID = existing.ID
	value.ProtocolOverride = existing.ProtocolOverride
	value.TargetPortOverride = existing.TargetPortOverride
	value.EnabledOverride = existing.EnabledOverride
	value.ReadinessOverride = existing.ReadinessOverride
	value.ApplicationID = existing.ApplicationID
	value.Managed = existing.Managed
	if value.RuntimeCreatedAt == nil {
		value.RuntimeCreatedAt = existing.RuntimeCreatedAt
	}
	if value.RuntimeUpdatedAt == nil {
		value.RuntimeUpdatedAt = existing.RuntimeUpdatedAt
	}
	// A new Docker object proves that explicit redeployment recovered a
	// previously removed runtime instance. A heartbeat for the old object
	// must not clear the removal marker.
	if value.RuntimeObjectID != "" && value.RuntimeObjectID != existing.RuntimeObjectID {
		value.RemovedAt = nil
	} else if value.RemovedAt == nil {
		value.RemovedAt = existing.RemovedAt
	}
	value.CreatedAt = existing.CreatedAt
	// Discovery refreshes last_seen_at and health counters frequently. Those
	// observations must not invalidate an operator's Service If-Match before
	// the subsequent scale/remove request reaches the Controller.
	revisionIncrement := int64(0)
	if value.RuntimeObjectID != existing.RuntimeObjectID || !equalOptionalTime(value.RuntimeUpdatedAt, existing.RuntimeUpdatedAt) ||
		value.Name != existing.Name || value.Namespace != existing.Namespace || value.DNSName != existing.DNSName ||
		value.DiscoveredPort != existing.DiscoveredPort || value.DiscoveredProtocol != existing.DiscoveredProtocol ||
		value.DiscoveredEnabled != existing.DiscoveredEnabled || value.DesiredInstances != existing.DesiredInstances {
		revisionIncrement = 1
	}
	value.Revision = existing.Revision + revisionIncrement
	routingIssues, _ := json.Marshal(value.RoutingIssues)
	query := s.bind(`UPDATE services SET runtime_object_id=:runtime_object_id,external_id=:external_id,routing_eligible=:routing_eligible,routing_issues=:routing_issues,name=:name,namespace=:namespace,qualified_name=:qualified_name,dns_name=:dns_name,dns_status=:dns_status,dns_last_error=:dns_last_error,discovered_protocol=:discovered_protocol,discovered_port=:discovered_port,discovered_enabled=:discovered_enabled,
        readiness_mode=:readiness_mode,readiness_path=:readiness_path,readiness_scheme=:readiness_scheme,readiness_interval_ns=:readiness_interval_ns,readiness_timeout_ns=:readiness_timeout_ns,readiness_success_threshold=:readiness_success_threshold,
        readiness_failure_threshold=:readiness_failure_threshold,readiness_startup_grace_ns=:readiness_startup_grace_ns,readiness_startup_timeout_ns=:readiness_startup_timeout_ns,readiness_tcp_fallback=:readiness_tcp_fallback,
		desired_instances=:desired_instances,running_instances=:running_instances,runtime_ready_instances=:runtime_ready_instances,ready_instances=:ready_instances,status=:status,configuration_error=:configuration_error,last_seen_at=:last_seen_at,
		runtime_created_at=:runtime_created_at,runtime_updated_at=:runtime_updated_at,removed_at=:removed_at,revision=revision+:revision_increment,updated_at=:updated_at WHERE id=:id`)
	_, err = tx.ExecContext(ctx, query, value.RuntimeObjectID, value.ExternalID, value.RoutingEligible, string(routingIssues), value.Name, value.Namespace,
		value.QualifiedName, value.DNSName, value.DNSStatus, value.DNSLastError, value.DiscoveredProtocol, value.DiscoveredPort,
		value.DiscoveredEnabled, value.Readiness.Mode, value.Readiness.Path, value.Readiness.Scheme,
		int64(value.Readiness.Interval), int64(value.Readiness.Timeout), value.Readiness.SuccessThreshold,
		value.Readiness.FailureThreshold, int64(value.Readiness.StartupGracePeriod), int64(value.Readiness.StartupTimeout),
		value.Readiness.TCPFallback, value.DesiredInstances, value.RunningInstances, value.RuntimeReadyInstances,
		value.ReadyInstances, value.Status,
		value.ConfigurationError, ptrTime(value.LastSeenAt), ptrTime(value.RuntimeCreatedAt), ptrTime(value.RuntimeUpdatedAt),
		ptrTime(value.RemovedAt), revisionIncrement, value.UpdatedAt, value.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLStore) ListServices(ctx context.Context, page Page) ([]model.Service, error) {
	page = page.normalized()
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+serviceColumns+` FROM services WHERE NOT EXISTS (SELECT 1 FROM control_documents tomb WHERE tomb.kind='deleted-project-guard' AND tomb.parent_id=services.id) AND id>:id ORDER BY id LIMIT :limit`), page.After, page.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]model.Service, 0, page.Limit)
	for rows.Next() {
		value, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) GetService(ctx context.Context, serviceID string) (model.Service, error) {
	return scanService(s.db.QueryRowContext(ctx, s.bind(`SELECT `+serviceColumns+` FROM services WHERE id=:id AND NOT EXISTS (SELECT 1 FROM control_documents tomb WHERE tomb.kind='deleted-project-guard' AND tomb.parent_id=services.id)`), serviceID))
}

// MarkServiceRuntimeRemoved records the absence of a managed runtime object
// without deleting its configuration, ownership, or route bindings.
func (s *SQLStore) MarkServiceRuntimeRemoved(ctx context.Context, serviceID string, now time.Time) error {
	result, err := s.execProjectMutation(ctx, serviceID, s.bind(`UPDATE services SET status=:status,removed_at=COALESCE(removed_at,:removed_at),desired_instances=0,running_instances=0,runtime_ready_instances=0,ready_instances=0,revision=revision+1,updated_at=:updated_at WHERE id=:id AND managed=:managed AND status<>:removed_status`),
		model.ServiceUnavailable, now, now, serviceID, true, model.ServiceRemoved)
	if err != nil {
		return mapError(err)
	}
	return requireAffected(result, true)
}

func (s *SQLStore) UpdateService(ctx context.Context, value *model.Service, expected int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	for _, resource := range []string{value.ID} {
		if err = s.guardProjectResourceTx(ctx, tx, resource); err != nil {
			return err
		}
	}
	query := `SELECT ` + serviceColumns + ` FROM services WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	current, err := scanService(tx.QueryRowContext(ctx, s.bind(query), value.ID))
	if err != nil {
		return err
	}
	var poolID string
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT r.pool_id FROM runtimes r WHERE r.id=:id`), current.RuntimeID).Scan(&poolID); err != nil {
		return mapError(err)
	}
	configChanged := current.EffectiveProtocol() != value.EffectiveProtocol() || current.EffectivePort() != value.EffectivePort() ||
		current.EffectiveEnabled() != value.EffectiveEnabled()
	probeChanged := configChanged || current.EffectiveReadiness() != value.EffectiveReadiness()
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE services SET protocol_override=:protocol_override,target_port_override=:target_port_override,enabled_override=:enabled_override,
		readiness_override=:readiness_override,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`),
		stringPtr(value.ProtocolOverride), intPtr(value.TargetPortOverride), boolPtr(value.EnabledOverride),
		readinessOverrideValue(value.ReadinessOverride),
		value.UpdatedAt, value.ID, expected)
	if err != nil {
		return mapError(err)
	}
	if err := requireAffected(result, true); err != nil {
		return err
	}
	if configChanged {
		if _, err = bumpPoolRevisionTx(ctx, s, tx, poolID, value.UpdatedAt); err != nil {
			return err
		}
	}
	if probeChanged {
		if _, err = bumpPoolProbeRevisionTx(ctx, s, tx, poolID, value.UpdatedAt); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func readinessOverrideValue(value *model.ReadinessPolicy) any {
	if value == nil {
		return nil
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func effectiveReadinessFromJSON(base model.ReadinessPolicy, override sql.NullString) (model.ReadinessPolicy, error) {
	if !override.Valid {
		return base, nil
	}
	if err := json.Unmarshal([]byte(override.String), &base); err != nil {
		return model.ReadinessPolicy{}, err
	}
	return base, nil
}

const endpointColumns = `id,service_id,external_id,address,port,node_id,runtime_state,health_state,
ready_state,readiness_source,weight,success_count,failure_count,last_probe_sequence,last_probe_error,
first_seen_at,ready_since,drain_since,stale_since,last_seen_at,revision,created_at,updated_at`

const qualifiedEndpointColumns = `e.id,e.service_id,e.external_id,e.address,e.port,e.node_id,e.runtime_state,e.health_state,
e.ready_state,e.readiness_source,e.weight,e.success_count,e.failure_count,e.last_probe_sequence,e.last_probe_error,
e.first_seen_at,e.ready_since,e.drain_since,e.stale_since,e.last_seen_at,e.revision,e.created_at,e.updated_at`

func scanEndpoint(row scanner) (model.Endpoint, error) {
	var value model.Endpoint
	var readySince, drainSince, staleSince sql.NullTime
	err := row.Scan(&value.ID, &value.ServiceID, &value.ExternalID, &value.Address, &value.Port, &value.NodeID,
		&value.RuntimeState, &value.HealthState, &value.ReadyState, &value.ReadinessSource, &value.Weight,
		&value.SuccessCount, &value.FailureCount, &value.LastProbeSequence, &value.LastProbeError,
		&value.FirstSeenAt, &readySince, &drainSince, &staleSince, &value.LastSeenAt, &value.Revision,
		&value.CreatedAt, &value.UpdatedAt)
	if readySince.Valid {
		value.ReadySince = &readySince.Time
	}
	if drainSince.Valid {
		value.DrainSince = &drainSince.Time
	}
	if staleSince.Valid {
		value.StaleSince = &staleSince.Time
	}
	return value, mapError(err)
}

func endpointArgs(value *model.Endpoint) []any {
	return []any{value.ID, value.ServiceID, value.ExternalID, value.Address, value.Port, value.NodeID,
		value.RuntimeState, value.HealthState, value.ReadyState, value.ReadinessSource, value.Weight,
		value.SuccessCount, value.FailureCount, value.LastProbeSequence, value.LastProbeError,
		value.FirstSeenAt, ptrTime(value.ReadySince), ptrTime(value.DrainSince), ptrTime(value.StaleSince),
		value.LastSeenAt, value.Revision, value.CreatedAt, value.UpdatedAt}
}

func (s *SQLStore) ListEndpoints(ctx context.Context, serviceID string) ([]model.Endpoint, error) {
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+endpointColumns+` FROM service_instances WHERE service_id=:service_id ORDER BY id`), serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []model.Endpoint
	for rows.Next() {
		value, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) UpsertEndpoint(ctx context.Context, value *model.Endpoint) error {
	existing, err := scanEndpoint(s.db.QueryRowContext(ctx, s.bind(`SELECT `+endpointColumns+` FROM service_instances WHERE service_id=:service_id AND external_id=:external_id`), value.ServiceID, value.ExternalID))
	if errors.Is(err, ErrNotFound) {
		_, err = s.execProjectMutation(ctx, value.ServiceID, s.bind(`INSERT INTO service_instances (`+endpointColumns+`) VALUES (`+columnPlaceholders(endpointColumns)+`)`), endpointArgs(value)...)
		if errors.Is(mapError(err), ErrConflict) {
			if blocked := s.CheckProjectResource(ctx, value.ServiceID); blocked != nil {
				return blocked
			}
			return s.UpsertEndpoint(ctx, value)
		}
		return mapError(err)
	}
	if err != nil {
		return err
	}
	value.ID = existing.ID
	value.FirstSeenAt = existing.FirstSeenAt
	value.SuccessCount = existing.SuccessCount
	value.FailureCount = existing.FailureCount
	value.LastProbeSequence = existing.LastProbeSequence
	value.ReadySince = existing.ReadySince
	if value.RuntimeState != model.EndpointRunning && existing.ReadyState == model.Ready {
		now := value.UpdatedAt
		value.ReadyState = model.Draining
		value.DrainSince = &now
	}
	_, err = s.execProjectMutation(ctx, value.ServiceID, s.bind(`UPDATE service_instances SET address=:address,port=:port,node_id=:node_id,runtime_state=:runtime_state,health_state=:health_state,
        ready_state=:ready_state,readiness_source=:readiness_source,weight=:weight,last_seen_at=:last_seen_at,stale_since=:stale_since,drain_since=:drain_since,revision=revision+1,updated_at=:updated_at WHERE id=:id`),
		value.Address, value.Port, value.NodeID, value.RuntimeState, value.HealthState, value.ReadyState,
		value.ReadinessSource, value.Weight, value.LastSeenAt, ptrTime(value.StaleSince), ptrTime(value.DrainSince),
		value.UpdatedAt, value.ID)
	return err
}

func (s *SQLStore) UpdateServiceProbe(ctx context.Context, result model.ProbeResult) (bool, error) {
	for attempt := 0; ; attempt++ {
		changed, err := s.updateServiceProbeOnce(ctx, result)
		if err == nil || attempt >= 3 || !retryableTransactionError(err) {
			return changed, err
		}
		if err = waitTransactionRetry(ctx, attempt); err != nil {
			return false, err
		}
	}
}

func (s *SQLStore) updateServiceProbeOnce(ctx context.Context, result model.ProbeResult) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	serviceID := result.ServiceID
	if serviceID == "" {
		return false, nil
	}
	// Project deletion and system registration take the project owner lock
	// before a Service lock. Probe updates must use the same order.
	if err = s.guardProjectResourceAccessTx(ctx, tx, serviceID, true); err != nil {
		if errors.Is(err, ErrConflict) {
			return false, nil
		}
		return false, err
	}
	lockQuery := `SELECT id FROM services WHERE id=:id`
	if s.dialect != SQLite {
		lockQuery += ` FOR UPDATE`
	}
	var lockedID string
	if err = tx.QueryRowContext(ctx, s.bind(lockQuery), serviceID).Scan(&lockedID); err != nil {
		return false, mapError(err)
	}
	query := `SELECT s.dns_name,s.dns_status,s.dns_last_error,s.routing_eligible,s.discovered_port,s.target_port_override,
		s.readiness_mode,s.readiness_path,s.readiness_scheme,s.readiness_interval_ns,s.readiness_timeout_ns,
		s.readiness_success_threshold,s.readiness_failure_threshold,s.readiness_startup_grace_ns,
		s.readiness_startup_timeout_ns,s.readiness_tcp_fallback,s.readiness_override,p.probe_epoch,p.probe_revision,p.id,
		s.configuration_error,s.discovered_enabled,s.enabled_override,r.enabled,p.enabled
		FROM services s JOIN runtimes r ON r.id=s.runtime_id JOIN gateway_pools p ON p.id=r.pool_id WHERE s.id=:id`
	row := tx.QueryRowContext(ctx, s.bind(query), serviceID)
	var dnsName, dnsLastError, configurationError, poolID string
	var dnsStatus model.DNSStatus
	var discoveredPort int
	var portOverride sql.NullInt64
	var policy model.ReadinessPolicy
	var interval, timeout, grace, startup int64
	var readinessOverride sql.NullString
	var epoch, probeRevision int64
	var routingEligible, discoveredEnabled, runtimeEnabled, poolEnabled bool
	var enabledOverride sql.NullBool
	err = row.Scan(&dnsName, &dnsStatus, &dnsLastError, &routingEligible, &discoveredPort, &portOverride,
		&policy.Mode, &policy.Path, &policy.Scheme, &interval, &timeout, &policy.SuccessThreshold,
		&policy.FailureThreshold, &grace, &startup, &policy.TCPFallback, &readinessOverride, &epoch,
		&probeRevision, &poolID, &configurationError, &discoveredEnabled,
		&enabledOverride, &runtimeEnabled, &poolEnabled)
	if err != nil {
		return false, mapError(err)
	}
	// The owner lock and service row lock above serialize this observation with
	// project deletion while the probe epoch rejects stale evidence.
	policy.Interval = time.Duration(interval)
	policy.Timeout = time.Duration(timeout)
	policy.StartupGracePeriod = time.Duration(grace)
	policy.StartupTimeout = time.Duration(startup)
	policy, err = effectiveReadinessFromJSON(policy, readinessOverride)
	if err != nil {
		return false, err
	}
	effectiveEnabled := true
	if enabledOverride.Valid {
		effectiveEnabled = enabledOverride.Bool
	}
	policy = normalizedDNSProbePolicy(policy)
	port := discoveredPort
	if portOverride.Valid {
		port = int(portOverride.Int64)
	}
	targetToken := serviceDNSProbeToken(policy, dnsName, port)
	if epoch != result.Epoch || result.Revision != probeRevision || result.PoolID != poolID ||
		result.TargetToken == "" || result.TargetToken != targetToken ||
		configurationError != "" || dnsStatus == model.DNSConflict ||
		!effectiveEnabled || !runtimeEnabled || !poolEnabled {
		return false, nil
	}
	var successCount, failureCount int
	var lastSequence int64
	var previousToken string
	stateErr := tx.QueryRowContext(ctx, s.bind(`SELECT success_count,failure_count,last_sequence,target_token
		FROM service_dns_probes WHERE service_id=:service_id`), serviceID).Scan(&successCount, &failureCount, &lastSequence, &previousToken)
	if stateErr != nil && !errors.Is(stateErr, sql.ErrNoRows) {
		return false, stateErr
	}
	if previousToken != targetToken {
		successCount, failureCount, lastSequence = 0, 0, 0
	}
	if result.Sequence <= lastSequence {
		return false, nil
	}
	previousStatus, previousError := dnsStatus, dnsLastError
	if result.Success {
		successCount++
		failureCount = 0
		if successCount >= max(policy.SuccessThreshold, 1) {
			dnsStatus = model.DNSReady
			dnsLastError = ""
		}
	} else {
		failureCount++
		successCount = 0
		if failureCount >= max(policy.FailureThreshold, 1) {
			dnsStatus = model.DNSUnreachable
			dnsLastError = result.Error
		}
	}
	if errors.Is(stateErr, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO service_dns_probes(service_id,success_count,failure_count,last_sequence,target_token,success,last_error,latency_ns,observed_at)
			VALUES(:service_id,:success_count,:failure_count,:last_sequence,:target_token,:success,:last_error,:latency_ns,:observed_at)`), serviceID, successCount, failureCount, result.Sequence, targetToken, result.Success, result.Error, int64(result.Latency), result.ObservedAt)
	} else {
		_, err = tx.ExecContext(ctx, s.bind(`UPDATE service_dns_probes SET success_count=:success_count,failure_count=:failure_count,last_sequence=:last_sequence,target_token=:target_token,success=:success,last_error=:last_error,latency_ns=:latency_ns,observed_at=:observed_at WHERE service_id=:service_id`),
			successCount, failureCount, result.Sequence, targetToken, result.Success, result.Error, int64(result.Latency), result.ObservedAt, serviceID)
	}
	if err != nil {
		return false, mapError(err)
	}
	changed := previousStatus != dnsStatus || previousError != dnsLastError
	if changed {
		if _, err = tx.ExecContext(ctx, s.bind(`UPDATE services SET dns_status=:dns_status,dns_last_error=:dns_last_error,updated_at=:updated_at WHERE id=:id`),
			dnsStatus, dnsLastError, result.ObservedAt, serviceID); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	committed = true
	return changed, nil
}

func recomputeServiceTx(ctx context.Context, store *SQLStore, tx *sql.Tx, serviceID string, now time.Time) error {
	var desired, observedRunning, running, ready int
	if err := tx.QueryRowContext(ctx, store.bind(`SELECT desired_instances,running_instances FROM services WHERE id=:id`), serviceID).Scan(&desired, &observedRunning); err != nil {
		return mapError(err)
	}
	if err := tx.QueryRowContext(ctx, store.bind(`SELECT COALESCE(SUM(CASE WHEN runtime_state=:runtime_state THEN 1 ELSE 0 END),0),
        COALESCE(SUM(CASE WHEN runtime_state=:runtime_state_2 AND ready_state=:ready_state AND health_state<>:health_state THEN 1 ELSE 0 END),0)
        FROM service_instances WHERE service_id=:service_id AND ready_state<>:ready_state_2`), model.EndpointRunning, model.EndpointRunning,
		model.Ready, model.HealthUnhealthy, serviceID, model.Removed).Scan(&running, &ready); err != nil {
		return err
	}
	running = max(running, observedRunning)
	status := model.ServiceUnavailable
	if ready > 0 && ready == desired {
		status = model.ServiceAvailable
	} else if ready > 0 {
		status = model.ServiceDegraded
	}
	// Endpoint readiness and probe results are observations. They must not
	// invalidate an operator's Service ETag during a scale or remove request.
	_, err := tx.ExecContext(ctx, store.bind(`UPDATE services SET desired_instances=:desired_instances,running_instances=:running_instances,ready_instances=:ready_instances,status=:status,updated_at=:updated_at WHERE id=:id AND status<>:status_2`),
		desired, running, ready, status, now, serviceID, model.ServiceRemoved)
	return err
}
