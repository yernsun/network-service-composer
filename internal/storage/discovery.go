package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
)

func (s *SQLStore) ApplyRuntimeSnapshot(ctx context.Context, snapshot model.RuntimeSnapshot) (bool, int64, error) {
	for attempt := 0; ; attempt++ {
		changed, revision, err := s.applyRuntimeSnapshotOnce(ctx, snapshot)
		if err == nil || attempt >= 3 || !retryableTransactionError(err) {
			return changed, revision, err
		}
		if err = waitTransactionRetry(ctx, attempt); err != nil {
			return false, 0, err
		}
	}
}

func (s *SQLStore) applyRuntimeSnapshotOnce(ctx context.Context, snapshot model.RuntimeSnapshot) (bool, int64, error) {
	snapshot.ObservedAt = runtimeObservationTime(s.dialect, snapshot.ObservedAt)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	query := `SELECT pool_id,observation_epoch,applied_observation,enabled FROM runtimes WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	var poolID string
	var issued, applied int64
	var enabled bool
	if err := tx.QueryRowContext(ctx, s.bind(query), snapshot.RuntimeID).Scan(&poolID, &issued, &applied, &enabled); err != nil {
		return false, 0, mapError(err)
	}
	if !enabled || snapshot.Observation <= applied || snapshot.Observation < issued {
		pool, err := scanPool(tx.QueryRowContext(ctx, s.bind(`SELECT `+poolColumns+` FROM gateway_pools WHERE id=:id`), poolID))
		return false, pool.DesiredRevision, err
	}

	existing, err := loadDiscoveryStateTx(ctx, s, tx, snapshot.RuntimeID, poolID)
	if err != nil {
		return false, 0, err
	}
	// Docker inventory order is not stable. Lock existing Service rows in the
	// same ID order as system project registration and endpoint reconciliation.
	snapshot.Services = append([]model.DiscoveredService(nil), snapshot.Services...)
	sort.Slice(snapshot.Services, func(i, j int) bool {
		key := func(value model.DiscoveredService) string {
			if current, found := existing.servicesByExternalID[discoveredServiceKey(value)]; found {
				return current.ID
			}
			return "~" + discoveredServiceKey(value)
		}
		return key(snapshot.Services[i]) < key(snapshot.Services[j])
	})
	// Normalize managed ports before deciding whether this snapshot can change
	// the data plane. Swarm ServiceInspect drops Compose `expose` metadata; if
	// normalization happened only during the write loop, the preflight could
	// incorrectly classify disabled -> routable as a metadata-only change and
	// fail to publish the new probe plan.
	for index := range snapshot.Services {
		discovered := &snapshot.Services[index]
		if service, found := existing.servicesByExternalID[discoveredServiceKey(*discovered)]; found {
			applySavedTargetPort(service, discovered)
		}
	}
	stateBefore, err := gatewayDataPlaneStateTx(ctx, s, tx, poolID)
	if err != nil {
		return false, 0, err
	}
	probeStateBefore, err := gatewayProbeStateTx(ctx, s, tx, poolID)
	if err != nil {
		return false, 0, err
	}
	seenServices := make(map[string]struct{}, len(snapshot.Services))
	guarded := map[string]bool{}
	guards, err := tx.QueryContext(ctx, `SELECT parent_id FROM control_documents WHERE kind IN ('project-guard','deleted-project-guard')`)
	if err != nil {
		return false, 0, err
	}
	for guards.Next() {
		var key string
		if err = guards.Scan(&key); err != nil {
			guards.Close()
			return false, 0, err
		}
		guarded[key] = true
	}
	err = guards.Err()
	guards.Close()
	if err != nil {
		return false, 0, err
	}
	for _, discovered := range snapshot.Services {
		if previous, ok := existing.servicesByExternalID[discoveredServiceKey(discovered)]; ok && guarded[previous.ID] {
			seenServices[previous.ID] = struct{}{}
			continue
		}
		service, _, err := upsertDiscoveredServiceTx(ctx, s, tx, snapshot, discovered, existing.servicesByExternalID)
		if err != nil {
			return false, 0, err
		}
		existing.servicesByExternalID[discoveredServiceKey(discovered)] = service
		seenServices[service.ID] = struct{}{}
		endpoints := existing.endpointsByServiceID[service.ID]
		if endpoints == nil {
			endpoints = make(map[string]model.Endpoint, len(discovered.Endpoints))
			existing.endpointsByServiceID[service.ID] = endpoints
		}
		service, _, _, err = reconcileEndpointsTx(ctx, s, tx, service, discovered, endpoints, snapshot.ObservedAt)
		if err != nil {
			return false, 0, err
		}
		existing.servicesByExternalID[discoveredServiceKey(discovered)] = service
		routes := existing.routesByServiceID[service.ID]
		if routes == nil {
			routes = make(map[string]model.Route, len(discovered.Routes))
			existing.routesByServiceID[service.ID] = routes
		}
		_, err = reconcileRoutesTx(ctx, s, tx, poolID, service, discovered.Routes, existing.domainsByHost, routes, snapshot.ObservedAt)
		if err != nil {
			return false, 0, err
		}
	}

	serviceKeys := make([]string, 0, len(existing.servicesByExternalID))
	for key := range existing.servicesByExternalID {
		serviceKeys = append(serviceKeys, key)
	}
	sort.Strings(serviceKeys)
	for _, key := range serviceKeys {
		service := existing.servicesByExternalID[key]
		if guarded[service.ID] {
			continue
		}
		if service.Status == model.ServiceRemoved {
			continue
		}
		if _, ok := seenServices[service.ID]; ok {
			continue
		}
		for _, endpoint := range existing.endpointsByServiceID[service.ID] {
			if endpoint.Eligible() {
				break
			}
		}
		missingStatus := model.ServiceRemoved
		if service.Managed {
			missingStatus = model.ServiceUnavailable
		}
		query := `UPDATE services SET status=:status,desired_instances=0,running_instances=0,runtime_ready_instances=0,ready_instances=0,last_seen_at=:last_seen_at`
		args := []any{missingStatus, snapshot.ObservedAt}
		if missingStatus == model.ServiceRemoved {
			query += `,removed_at=COALESCE(removed_at,:removed_at),revision=revision+1`
			args = append(args, snapshot.ObservedAt)
		}
		// A managed Service with zero observed containers remains a stable
		// configuration target. Repeated missing observations must not expire
		// its If-Match while an operator stops or removes its runtime.
		query += `,updated_at=:updated_at WHERE id=:id`
		args = append(args, snapshot.ObservedAt, service.ID)
		if _, err := tx.ExecContext(ctx, s.bind(query), args...); err != nil {
			return false, 0, err
		}
		if _, err := tx.ExecContext(ctx, s.bind(`UPDATE service_instances SET ready_state=:ready_state,drain_since=COALESCE(drain_since,:drain_since),last_seen_at=:last_seen_at,revision=revision+1,updated_at=:updated_at WHERE service_id=:service_id AND ready_state NOT IN (:draining_state,:removed_state)`), model.Draining, snapshot.ObservedAt, snapshot.ObservedAt, snapshot.ObservedAt, service.ID, model.Draining, model.Removed); err != nil {
			return false, 0, err
		}
		if _, err := tx.ExecContext(ctx, s.bind(`UPDATE routes SET enabled=:enabled,last_error='',revision=revision+1,updated_at=:updated_at WHERE service_id=:service_id AND source=:source AND (enabled=:enabled_2 OR last_error LIKE :last_error OR last_error LIKE :last_error_2)`), false, snapshot.ObservedAt, service.ID, model.SourceDiscovery, true, discoveryRouteConflictPrefix+"%", discoveryDomainTLSConflictPrefix+"%"); err != nil {
			return false, 0, err
		}
	}
	if err = resolveServiceDNSConflictsTx(ctx, s, tx, poolID, snapshot.ObservedAt); err != nil {
		return false, 0, err
	}
	if err = resolveDiscoveryRouteConflictsTx(ctx, s, tx, poolID, snapshot.ObservedAt); err != nil {
		return false, 0, err
	}
	stateAfter, err := gatewayDataPlaneStateTx(ctx, s, tx, poolID)
	if err != nil {
		return false, 0, err
	}
	probeStateAfter, err := gatewayProbeStateTx(ctx, s, tx, poolID)
	if err != nil {
		return false, 0, err
	}
	configChanged := !slices.Equal(stateBefore, stateAfter)
	probeChanged := !slices.Equal(probeStateBefore, probeStateAfter)

	if err = s.writeRuntimePresenceTx(ctx, tx, snapshot, seenServices); err != nil {
		return false, 0, err
	}
	if _, err := tx.ExecContext(ctx, s.bind(`UPDATE runtimes SET status=:status,observation_epoch=:observation_epoch,applied_observation=:applied_observation,last_seen_at=:last_seen_at,last_error='',revision=revision+1,updated_at=:updated_at WHERE id=:id`), model.RuntimeOnline, snapshot.Observation, snapshot.Observation, snapshot.ObservedAt, snapshot.ObservedAt, snapshot.RuntimeID); err != nil {
		return false, 0, err
	}
	var revision int64
	if configChanged {
		revision, err = bumpPoolRevisionTx(ctx, s, tx, poolID, snapshot.ObservedAt)
		if err != nil {
			return false, 0, err
		}
	} else if err := tx.QueryRowContext(ctx, s.bind(`SELECT desired_revision FROM gateway_pools WHERE id=:id`), poolID).Scan(&revision); err != nil {
		return false, 0, err
	}
	if probeChanged {
		if _, err = bumpPoolProbeRevisionTx(ctx, s, tx, poolID, snapshot.ObservedAt); err != nil {
			return false, 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, 0, err
	}
	committed = true
	// Probe-plan changes are versioned independently and observed by connected
	// Gateways during their control-stream poll. Only configuration changes wake
	// the snapshot publisher; instance churn must not cause an NGINX reload.
	return configChanged, revision, nil
}

const serviceDNSConflictPrefix = "gateway pool DNS conflict:"

// resolveServiceDNSConflictsTx enforces a single owner for every Docker DNS
// name visible to a Gateway Pool. The upstream suffix prevents NGINX symbol
// collisions; this check prevents two Docker services from answering the same
// DNS query and silently sharing traffic.
func resolveServiceDNSConflictsTx(ctx context.Context, store *SQLStore, tx *sql.Tx, poolID string, now time.Time) error {
	rows, err := tx.QueryContext(ctx, store.bind(`SELECT `+serviceColumns+` FROM services
		WHERE runtime_id IN (SELECT id FROM runtimes WHERE pool_id=:pool_id AND enabled=:enabled) AND status<>:status ORDER BY id`),
		poolID, true, model.ServiceRemoved)
	if err != nil {
		return err
	}
	var services []model.Service
	for rows.Next() {
		service, scanErr := scanService(rows)
		if scanErr != nil {
			rows.Close()
			return scanErr
		}
		services = append(services, service)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}

	owners := make(map[string][]string)
	for _, service := range services {
		if dnsName := strings.ToLower(strings.TrimSpace(service.DNSName)); dnsName != "" {
			owners[dnsName] = append(owners[dnsName], service.ID)
		}
	}
	for _, service := range services {
		dnsName := strings.ToLower(strings.TrimSpace(service.DNSName))
		ownerIDs := owners[dnsName]
		conflicted := dnsName != "" && len(ownerIDs) > 1
		generatedIssue := ""
		if conflicted {
			generatedIssue = fmt.Sprintf("%s %q is claimed by %d services", serviceDNSConflictPrefix, service.DNSName, len(ownerIDs))
		}
		issues := make([]string, 0, len(service.RoutingIssues)+1)
		for _, issue := range service.RoutingIssues {
			if !strings.HasPrefix(issue, serviceDNSConflictPrefix) {
				issues = append(issues, issue)
			}
		}
		if generatedIssue != "" {
			issues = append(issues, generatedIssue)
		}

		status, lastError, eligible := service.DNSStatus, service.DNSLastError, service.RoutingEligible
		wasPoolConflict := service.DNSStatus == model.DNSConflict && strings.HasPrefix(service.DNSLastError, serviceDNSConflictPrefix)
		switch {
		case conflicted:
			status, lastError, eligible = model.DNSConflict, generatedIssue, false
		case wasPoolConflict:
			// Discovery refreshes the authoritative state on its next observation.
			// Until then, recover only when the remaining metadata is unambiguous.
			lastError = ""
			if service.DNSName == "" {
				status, eligible = model.DNSMissing, false
			} else if len(issues) == 0 && service.ConfigurationError == "" && service.EffectivePort() > 0 {
				status, eligible = model.DNSReady, service.EffectiveEnabled()
			} else {
				status, eligible = model.DNSMissing, false
			}
		}
		if status == service.DNSStatus && lastError == service.DNSLastError && eligible == service.RoutingEligible && slices.Equal(issues, service.RoutingIssues) {
			continue
		}
		encodedIssues, _ := json.Marshal(issues)
		if _, err = tx.ExecContext(ctx, store.bind(`UPDATE services SET dns_status=:dns_status,dns_last_error=:dns_last_error,routing_eligible=:routing_eligible,routing_issues=:routing_issues,revision=revision+1,updated_at=:updated_at WHERE id=:id AND NOT EXISTS (SELECT 1 FROM control_documents guard WHERE guard.parent_id=services.id AND guard.kind IN ('project-guard','deleted-project-guard'))`),
			status, lastError, eligible, string(encodedIssues), now, service.ID); err != nil {
			return err
		}
	}
	return nil
}

// MarkRuntimeOffline records source unavailability without changing the last
// known readiness decision. The lifecycle sweep applies the configured stale
// policy separately, so retain-ready never creates an accidental outage.
func (s *SQLStore) MarkRuntimeOffline(ctx context.Context, runtimeID string, observation int64, cause string, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	query := `SELECT observation_epoch,applied_observation,enabled FROM runtimes WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	var issued, applied int64
	var enabled bool
	if err = tx.QueryRowContext(ctx, s.bind(query), runtimeID).Scan(&issued, &applied, &enabled); err != nil {
		return false, mapError(err)
	}
	if !enabled || observation != issued || observation <= applied {
		return false, nil
	}
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE runtimes SET status=:status,applied_observation=:applied_observation,last_error=:last_error,revision=revision+1,updated_at=:updated_at WHERE id=:id AND enabled=:enabled`),
		model.RuntimeOffline, observation, cause, now, runtimeID, true)
	if err != nil {
		return false, err
	}
	if err = requireAffected(result, false); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE service_instances SET stale_since=COALESCE(stale_since,:stale_since),revision=revision+1,updated_at=:updated_at
        WHERE service_id IN (SELECT id FROM services WHERE runtime_id=:runtime_id AND NOT EXISTS (SELECT 1 FROM control_documents guard WHERE guard.parent_id=services.id AND guard.kind IN ('project-guard','deleted-project-guard'))) AND ready_state<>:ready_state`), now, now, runtimeID, model.Removed); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	committed = true
	return true, nil
}

// SweepEndpointLifecycle advances stale instances to draining/removal. Docker
// instance identity is status-only in the DNS data plane, so this never bumps
// or publishes an NGINX configuration revision.
func (s *SQLStore) SweepEndpointLifecycle(ctx context.Context, runtimeID string, observation int64, offlinePolicy string, now time.Time, staleTimeout, drainTimeout, retention time.Duration) (bool, int64, error) {
	for attempt := 0; ; attempt++ {
		changed, revision, err := s.sweepEndpointLifecycleOnce(ctx, runtimeID, observation, offlinePolicy, now, staleTimeout, drainTimeout, retention)
		if err == nil || attempt >= 3 || !retryableTransactionError(err) {
			return changed, revision, err
		}
		if err := waitTransactionRetry(ctx, attempt); err != nil {
			return false, 0, err
		}
	}
}

func (s *SQLStore) sweepEndpointLifecycleOnce(ctx context.Context, runtimeID string, observation int64, offlinePolicy string, now time.Time, staleTimeout, drainTimeout, retention time.Duration) (bool, int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	query := `SELECT pool_id,observation_epoch,applied_observation FROM runtimes WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	var poolID string
	var issued, applied int64
	if err = tx.QueryRowContext(ctx, s.bind(query), runtimeID).Scan(&poolID, &issued, &applied); err != nil {
		return false, 0, mapError(err)
	}
	if observation != issued || observation != applied {
		var revision int64
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT desired_revision FROM gateway_pools WHERE id=:id`), poolID).Scan(&revision); err != nil {
			return false, 0, err
		}
		return false, revision, nil
	}
	if offlinePolicy == "drain-after-timeout" {
		rows, queryErr := tx.QueryContext(ctx, s.bind(`SELECT `+endpointColumns+` FROM service_instances
            WHERE service_id IN (SELECT id FROM services WHERE runtime_id=:runtime_id AND NOT EXISTS (SELECT 1 FROM control_documents guard WHERE guard.parent_id=services.id AND guard.kind IN ('project-guard','deleted-project-guard'))) AND stale_since IS NOT NULL AND stale_since<=:stale_since AND ready_state=:ready_state`),
			runtimeID, now.Add(-staleTimeout), model.Ready)
		if queryErr != nil {
			return false, 0, queryErr
		}
		var stale []model.Endpoint
		for rows.Next() {
			value, scanErr := scanEndpoint(rows)
			if scanErr != nil {
				rows.Close()
				return false, 0, scanErr
			}
			stale = append(stale, value)
		}
		if queryErr = rows.Err(); queryErr != nil {
			rows.Close()
			return false, 0, queryErr
		}
		if queryErr = rows.Close(); queryErr != nil {
			return false, 0, queryErr
		}
		for _, endpoint := range stale {
			if _, err = tx.ExecContext(ctx, s.bind(`UPDATE service_instances SET ready_state=:ready_state,drain_since=COALESCE(drain_since,:drain_since),revision=revision+1,updated_at=:updated_at WHERE id=:id`),
				model.Draining, now, now, endpoint.ID); err != nil {
				return false, 0, err
			}
		}
	}
	if drainTimeout >= 0 {
		if _, err = tx.ExecContext(ctx, s.bind(`UPDATE service_instances SET ready_state=:ready_state,revision=revision+1,updated_at=:updated_at
            WHERE service_id IN (SELECT id FROM services WHERE runtime_id=:runtime_id AND NOT EXISTS (SELECT 1 FROM control_documents guard WHERE guard.parent_id=services.id AND guard.kind IN ('project-guard','deleted-project-guard'))) AND ready_state=:ready_state_2 AND drain_since IS NOT NULL AND drain_since<=:drain_since`),
			model.Removed, now, runtimeID, model.Draining, now.Add(-drainTimeout)); err != nil {
			return false, 0, err
		}
	}
	if retention > 0 {
		if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM service_instances WHERE service_id IN (SELECT id FROM services WHERE runtime_id=:runtime_id AND NOT EXISTS (SELECT 1 FROM control_documents guard WHERE guard.parent_id=services.id AND guard.kind IN ('project-guard','deleted-project-guard')))
            AND ready_state=:ready_state AND last_seen_at<=:last_seen_at`), runtimeID, model.Removed, now.Add(-retention)); err != nil {
			return false, 0, err
		}
	}
	serviceRows, queryErr := tx.QueryContext(ctx, s.bind(`SELECT id FROM services WHERE runtime_id=:runtime_id AND NOT EXISTS (SELECT 1 FROM control_documents guard WHERE guard.parent_id=services.id AND guard.kind IN ('project-guard','deleted-project-guard'))`), runtimeID)
	if queryErr != nil {
		return false, 0, queryErr
	}
	var serviceIDs []string
	for serviceRows.Next() {
		var serviceID string
		if queryErr = serviceRows.Scan(&serviceID); queryErr != nil {
			serviceRows.Close()
			return false, 0, queryErr
		}
		serviceIDs = append(serviceIDs, serviceID)
	}
	if queryErr = serviceRows.Err(); queryErr != nil {
		serviceRows.Close()
		return false, 0, queryErr
	}
	if queryErr = serviceRows.Close(); queryErr != nil {
		return false, 0, queryErr
	}
	sort.Strings(serviceIDs)
	for _, serviceID := range serviceIDs {
		if err = recomputeServiceTx(ctx, s, tx, serviceID, now); err != nil {
			return false, 0, err
		}
	}
	var revision int64
	err = tx.QueryRowContext(ctx, s.bind(`SELECT desired_revision FROM gateway_pools WHERE id=:id`), poolID).Scan(&revision)
	if err != nil {
		return false, 0, err
	}
	if err = tx.Commit(); err != nil {
		return false, 0, err
	}
	committed = true
	return false, revision, nil
}

type discoveryState struct {
	servicesByExternalID map[string]model.Service
	endpointsByServiceID map[string]map[string]model.Endpoint
	routesByServiceID    map[string]map[string]model.Route
	domainsByHost        map[string]model.Domain
}

func loadDiscoveryStateTx(ctx context.Context, store *SQLStore, tx *sql.Tx, runtimeID, poolID string) (discoveryState, error) {
	state := discoveryState{
		servicesByExternalID: make(map[string]model.Service),
		endpointsByServiceID: make(map[string]map[string]model.Endpoint),
		routesByServiceID:    make(map[string]map[string]model.Route),
		domainsByHost:        make(map[string]model.Domain),
	}
	serviceRows, err := tx.QueryContext(ctx, store.bind(`SELECT `+serviceColumns+` FROM services WHERE runtime_id=:runtime_id`), runtimeID)
	if err != nil {
		return state, err
	}
	for serviceRows.Next() {
		service, scanErr := scanService(serviceRows)
		if scanErr != nil {
			serviceRows.Close()
			return state, scanErr
		}
		state.servicesByExternalID[service.ServiceKey] = service
	}
	if err = serviceRows.Err(); err != nil {
		serviceRows.Close()
		return state, err
	}
	if err = serviceRows.Close(); err != nil {
		return state, err
	}

	endpointRows, err := tx.QueryContext(ctx, store.bind(`SELECT `+qualifiedEndpointColumns+` FROM service_instances e JOIN services sv ON sv.id=e.service_id WHERE sv.runtime_id=:runtime_id`), runtimeID)
	if err != nil {
		return state, err
	}
	for endpointRows.Next() {
		endpoint, scanErr := scanEndpoint(endpointRows)
		if scanErr != nil {
			endpointRows.Close()
			return state, scanErr
		}
		byExternalID := state.endpointsByServiceID[endpoint.ServiceID]
		if byExternalID == nil {
			byExternalID = make(map[string]model.Endpoint)
			state.endpointsByServiceID[endpoint.ServiceID] = byExternalID
		}
		byExternalID[endpoint.ExternalID] = endpoint
	}
	if err = endpointRows.Err(); err != nil {
		endpointRows.Close()
		return state, err
	}
	if err = endpointRows.Close(); err != nil {
		return state, err
	}

	routeFields := strings.Split(routeColumns, ",")
	for i := range routeFields {
		routeFields[i] = "r." + strings.TrimSpace(routeFields[i])
	}
	routeRows, err := tx.QueryContext(ctx, store.bind(`SELECT `+strings.Join(routeFields, ",")+` FROM routes r JOIN services sv ON sv.id=r.service_id WHERE sv.runtime_id=:runtime_id AND r.source=:source`), runtimeID, model.SourceDiscovery)
	if err != nil {
		return state, err
	}
	for routeRows.Next() {
		route, scanErr := scanRoute(routeRows)
		if scanErr != nil {
			routeRows.Close()
			return state, scanErr
		}
		byExternalKey := state.routesByServiceID[route.ServiceID]
		if byExternalKey == nil {
			byExternalKey = make(map[string]model.Route)
			state.routesByServiceID[route.ServiceID] = byExternalKey
		}
		byExternalKey[route.ExternalKey] = route
	}
	if err = routeRows.Err(); err != nil {
		routeRows.Close()
		return state, err
	}
	if err = routeRows.Close(); err != nil {
		return state, err
	}

	domainRows, err := tx.QueryContext(ctx, store.bind(`SELECT `+domainColumns+` FROM domains WHERE pool_id=:pool_id`), poolID)
	if err != nil {
		return state, err
	}
	for domainRows.Next() {
		domain, scanErr := scanDomain(domainRows)
		if scanErr != nil {
			domainRows.Close()
			return state, scanErr
		}
		state.domainsByHost[domain.Host] = domain
	}
	if err = domainRows.Err(); err != nil {
		domainRows.Close()
		return state, err
	}
	if err = domainRows.Close(); err != nil {
		return state, err
	}
	return state, nil
}

func discoveryTopologyMayChange(snapshot model.RuntimeSnapshot, existing discoveryState) bool {
	seenServices := make(map[string]struct{}, len(snapshot.Services))
	for _, discovered := range snapshot.Services {
		service, found := existing.servicesByExternalID[discoveredServiceKey(discovered)]
		if !found {
			if len(discovered.Routes) > 0 {
				return true
			}
			continue
		}
		seenServices[service.ID] = struct{}{}
		if discoveredServiceChangesDataPlane(service, discovered) {
			return true
		}
		seenRoutes := make(map[string]struct{}, len(discovered.Routes))
		for _, observed := range discovered.Routes {
			seenRoutes[observed.ExternalKey] = struct{}{}
			domain, domainFound := existing.domainsByHost[observed.Host]
			if !domainFound {
				return true
			}
			httpEnabled, httpsEnabled := discoveredRouteEntrypoints(observed)
			if domain.Source == model.SourceDiscovery && (domain.TLSMode != observed.TLSMode ||
				domain.HTTPSRedirect != observed.HTTPSRedirect || domain.HTTPEnabled != httpEnabled || domain.HTTPSEnabled != httpsEnabled || !domain.Enabled ||
				(domain.TLSMode == model.TLSManaged && domain.CertificateID == "")) {
				return true
			}
			route, routeFound := existing.routesByServiceID[service.ID][observed.ExternalKey]
			if !routeFound {
				return true
			}
			conflicted := strings.HasPrefix(route.LastError, discoveryRouteConflictPrefix)
			expectedEnabled := !conflicted
			if route.DomainID != domain.ID || route.Path != observed.Path || route.Priority != observed.Priority ||
				route.StripPath != observed.StripPath || route.TargetPort != observed.TargetPort ||
				route.UpstreamProtocol != observed.UpstreamProtocol || route.UpstreamServerName != observed.UpstreamServerName ||
				route.WebsocketEnabled != observed.WebsocketEnabled || route.Enabled != expectedEnabled {
				return true
			}
		}
		for externalKey, route := range existing.routesByServiceID[service.ID] {
			if _, seen := seenRoutes[externalKey]; !seen && (route.Enabled || discoveryRouteHasConflict(route.LastError)) {
				return true
			}
		}
	}
	for _, service := range existing.servicesByExternalID {
		if service.Status == model.ServiceRemoved {
			continue
		}
		if _, seen := seenServices[service.ID]; seen {
			continue
		}
		for _, route := range existing.routesByServiceID[service.ID] {
			if route.Enabled || discoveryRouteHasConflict(route.LastError) {
				return true
			}
		}
	}
	return false
}

func discoveredServiceChangesDataPlane(service model.Service, discovered model.DiscoveredService) bool {
	protocol := effectiveProtocol(discovered.Protocol)
	if service.ProtocolOverride != nil {
		protocol = *service.ProtocolOverride
	}
	port := discovered.TargetPort
	if service.TargetPortOverride != nil {
		port = *service.TargetPortOverride
	}
	enabled := discovered.Enabled
	if service.EnabledOverride != nil {
		enabled = *service.EnabledOverride
	}
	wasEligible := service.EffectiveEnabled() && service.ConfigurationError == ""
	willBeEligible := enabled && discovered.ConfigurationError == ""
	return wasEligible != willBeEligible || (willBeEligible &&
		(service.EffectiveProtocol() != protocol || service.EffectivePort() != port))
}

type effectiveRouteReference struct {
	host, path, routeID, serviceID string
	priority                       int
	source                         model.Source
}

func effectiveRoutedServicesTx(ctx context.Context, store *SQLStore, tx *sql.Tx, poolID string) (map[string]struct{}, error) {
	rows, err := tx.QueryContext(ctx, store.bind(`SELECT d.host,r.path,r.priority,r.source,r.id,r.service_id,s.discovered_enabled,s.enabled_override
		FROM routes r JOIN domains d ON d.id=r.domain_id JOIN services s ON s.id=r.service_id JOIN runtimes rt ON rt.id=s.runtime_id
		WHERE d.pool_id=:pool_id AND rt.pool_id=:pool_id_2 AND d.enabled=:enabled AND r.enabled=:enabled_2 AND r.route_status=:route_status AND rt.enabled=:enabled_3 AND s.configuration_error=''`), poolID, poolID, true, true, model.RouteActive, true)
	if err != nil {
		return nil, err
	}
	var candidates []effectiveRouteReference
	for rows.Next() {
		var candidate effectiveRouteReference
		var discoveredEnabled bool
		var enabledOverride sql.NullBool
		if err = rows.Scan(&candidate.host, &candidate.path, &candidate.priority, &candidate.source, &candidate.routeID,
			&candidate.serviceID, &discoveredEnabled, &enabledOverride); err != nil {
			rows.Close()
			return nil, err
		}
		enabled := true
		if enabledOverride.Valid {
			enabled = enabledOverride.Bool
		}
		if enabled {
			candidates = append(candidates, candidate)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].host != candidates[j].host {
			return candidates[i].host < candidates[j].host
		}
		if len(candidates[i].path) != len(candidates[j].path) {
			return len(candidates[i].path) > len(candidates[j].path)
		}
		if candidates[i].source.Priority() != candidates[j].source.Priority() {
			return candidates[i].source.Priority() > candidates[j].source.Priority()
		}
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority > candidates[j].priority
		}
		return candidates[i].routeID < candidates[j].routeID
	})
	services := make(map[string]struct{})
	seenLocations := make(map[string]struct{})
	for _, candidate := range candidates {
		location := candidate.host + "\x00" + candidate.path
		if _, seen := seenLocations[location]; seen {
			continue
		}
		seenLocations[location] = struct{}{}
		services[candidate.serviceID] = struct{}{}
	}
	return services, nil
}

func upsertDiscoveredServiceTx(ctx context.Context, store *SQLStore, tx *sql.Tx, snapshot model.RuntimeSnapshot, discovered model.DiscoveredService, existing map[string]model.Service) (model.Service, bool, error) {
	stableKey := discoveredServiceKey(discovered)
	service, found := existing[stableKey]
	if !found {
		now := snapshot.ObservedAt
		service = model.Service{ID: id.New(id.Service), RuntimeID: snapshot.RuntimeID, ServiceKey: stableKey,
			RuntimeObjectID: discovered.RuntimeObjectID, ExternalID: discovered.ExternalID, Name: discovered.Name,
			Namespace: discovered.Namespace, QualifiedName: discovered.QualifiedName, DNSName: discovered.DNSName,
			DNSStatus: discovered.DNSStatus, DiscoveredProtocol: discovered.Protocol, DiscoveredPort: discovered.TargetPort,
			DiscoveredEnabled: discovered.Enabled, Readiness: discovered.Readiness,
			RoutingEligible: discovered.RoutingEligible, RoutingIssues: discovered.RoutingIssues,
			DesiredInstances: discovered.DesiredInstances, RuntimeReadyInstances: discovered.ReadyInstances,
			Status: model.ServiceDiscovered, ConfigurationError: discovered.ConfigurationError,
			RuntimeCreatedAt: discovered.RuntimeCreatedAt, RuntimeUpdatedAt: discovered.RuntimeUpdatedAt,
			LastSeenAt: &now, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if _, err := tx.ExecContext(ctx, store.bind(`INSERT INTO services (`+serviceColumns+`) VALUES (`+columnPlaceholders(serviceColumns)+`)`), serviceArgs(&service)...); err != nil {
			return service, false, mapError(err)
		}
		return service, true, nil
	}
	applySavedTargetPort(service, &discovered)
	dnsStatus, dnsLastError := discovered.DNSStatus, ""
	if service.DNSStatus == model.DNSUnreachable && discovered.DNSStatus == model.DNSReady &&
		service.DNSName == discovered.DNSName {
		// Docker metadata proves that the stable alias still exists, but only the
		// Gateway can prove reachability from the data-plane network. Preserve a
		// failed DNS probe until that same Service target probes successfully.
		dnsStatus, dnsLastError = service.DNSStatus, service.DNSLastError
	}
	gatewayChanged := discoveredServiceChangesDataPlane(service, discovered)
	// A fresh snapshot revives a previously missing external discovery even
	// when its source supplies no Docker object ID. Explicitly removed managed
	// services need proof of a new object before the marker can be cleared.
	recoveredRuntime := service.RemovedAt != nil && (!service.Managed && service.Status == model.ServiceRemoved ||
		discovered.RuntimeObjectID != "" && discovered.RuntimeObjectID != service.RuntimeObjectID)
	revisionChanged := service.RuntimeObjectID != discovered.RuntimeObjectID || service.ExternalID != discovered.ExternalID ||
		service.Name != discovered.Name || service.Namespace != discovered.Namespace ||
		service.QualifiedName != discovered.QualifiedName || service.DNSName != discovered.DNSName ||
		service.DiscoveredProtocol != discovered.Protocol || service.DiscoveredPort != discovered.TargetPort ||
		service.DiscoveredEnabled != discovered.Enabled || service.Readiness != discovered.Readiness ||
		service.DesiredInstances != discovered.DesiredInstances ||
		!equalOptionalTime(service.RuntimeCreatedAt, discovered.RuntimeCreatedAt) ||
		!equalOptionalTime(service.RuntimeUpdatedAt, discovered.RuntimeUpdatedAt) || service.Status == model.ServiceRemoved
	metadataChanged := service.RuntimeObjectID != discovered.RuntimeObjectID || service.ExternalID != discovered.ExternalID ||
		service.Name != discovered.Name || service.Namespace != discovered.Namespace ||
		service.QualifiedName != discovered.QualifiedName || service.DNSName != discovered.DNSName || service.DNSStatus != dnsStatus ||
		service.DNSLastError != dnsLastError ||
		service.DiscoveredProtocol != discovered.Protocol || service.DiscoveredPort != discovered.TargetPort ||
		service.DiscoveredEnabled != discovered.Enabled ||
		service.Readiness != discovered.Readiness || service.DesiredInstances != discovered.DesiredInstances ||
		service.RuntimeReadyInstances != discovered.ReadyInstances ||
		service.ConfigurationError != discovered.ConfigurationError || service.RoutingEligible != discovered.RoutingEligible ||
		!slices.Equal(service.RoutingIssues, discovered.RoutingIssues) ||
		!equalOptionalTime(service.RuntimeCreatedAt, discovered.RuntimeCreatedAt) ||
		!equalOptionalTime(service.RuntimeUpdatedAt, discovered.RuntimeUpdatedAt) ||
		service.Status == model.ServiceRemoved || recoveredRuntime
	service.ServiceKey = stableKey
	service.RuntimeObjectID = discovered.RuntimeObjectID
	service.ExternalID = discovered.ExternalID
	service.Name = discovered.Name
	service.Namespace = discovered.Namespace
	service.QualifiedName = discovered.QualifiedName
	service.DNSName = discovered.DNSName
	service.DNSStatus = dnsStatus
	service.DNSLastError = dnsLastError
	service.DiscoveredProtocol = discovered.Protocol
	service.DiscoveredPort = discovered.TargetPort
	service.DiscoveredEnabled = discovered.Enabled
	service.Readiness = discovered.Readiness
	service.DesiredInstances = discovered.DesiredInstances
	service.RuntimeReadyInstances = discovered.ReadyInstances
	service.ConfigurationError = discovered.ConfigurationError
	service.RoutingEligible = discovered.RoutingEligible
	service.RoutingIssues = discovered.RoutingIssues
	service.RuntimeCreatedAt = discovered.RuntimeCreatedAt
	service.RuntimeUpdatedAt = discovered.RuntimeUpdatedAt
	if recoveredRuntime {
		service.RemovedAt = nil
	}
	service.LastSeenAt = &snapshot.ObservedAt
	service.UpdatedAt = snapshot.ObservedAt
	if !metadataChanged {
		// An unchanged, accepted snapshot is still fresh observation evidence.
		// Do not increment the configuration revision for this heartbeat.
		_, err := tx.ExecContext(ctx, store.bind(`UPDATE services SET last_seen_at=:last_seen_at WHERE id=:id`), snapshot.ObservedAt, service.ID)
		return service, gatewayChanged, err
	}
	revisionIncrement := int64(0)
	if revisionChanged {
		revisionIncrement = 1
	}
	routingIssues, _ := json.Marshal(service.RoutingIssues)
	_, err := tx.ExecContext(ctx, store.bind(`UPDATE services SET service_key=:service_key,runtime_object_id=:runtime_object_id,external_id=:external_id,routing_eligible=:routing_eligible,routing_issues=:routing_issues,name=:name,namespace=:namespace,qualified_name=:qualified_name,dns_name=:dns_name,dns_status=:dns_status,dns_last_error=:dns_last_error,discovered_protocol=:discovered_protocol,discovered_port=:discovered_port,discovered_enabled=:discovered_enabled,
        readiness_mode=:readiness_mode,readiness_path=:readiness_path,readiness_scheme=:readiness_scheme,readiness_interval_ns=:readiness_interval_ns,readiness_timeout_ns=:readiness_timeout_ns,readiness_success_threshold=:readiness_success_threshold,readiness_failure_threshold=:readiness_failure_threshold,
		readiness_startup_grace_ns=:readiness_startup_grace_ns,readiness_startup_timeout_ns=:readiness_startup_timeout_ns,readiness_tcp_fallback=:readiness_tcp_fallback,desired_instances=:desired_instances,runtime_ready_instances=:runtime_ready_instances,configuration_error=:configuration_error,last_seen_at=:last_seen_at,
		runtime_created_at=:runtime_created_at,runtime_updated_at=:runtime_updated_at,removed_at=:removed_at,revision=revision+:revision_increment,updated_at=:updated_at WHERE id=:id`),
		service.ServiceKey, service.RuntimeObjectID, service.ExternalID, service.RoutingEligible, string(routingIssues), service.Name, service.Namespace,
		service.QualifiedName, service.DNSName, service.DNSStatus, service.DNSLastError, service.DiscoveredProtocol, service.DiscoveredPort, service.DiscoveredEnabled,
		service.Readiness.Mode, service.Readiness.Path, service.Readiness.Scheme, int64(service.Readiness.Interval), int64(service.Readiness.Timeout),
		service.Readiness.SuccessThreshold, service.Readiness.FailureThreshold, int64(service.Readiness.StartupGracePeriod), int64(service.Readiness.StartupTimeout),
		service.Readiness.TCPFallback, service.DesiredInstances, service.RuntimeReadyInstances, service.ConfigurationError,
		snapshot.ObservedAt, ptrTime(service.RuntimeCreatedAt), ptrTime(service.RuntimeUpdatedAt), ptrTime(service.RemovedAt), revisionIncrement, snapshot.ObservedAt, service.ID)
	if err == nil {
		if revisionChanged {
			service.Revision++
		}
	}
	return service, gatewayChanged, err
}

func equalOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func applySavedTargetPort(service model.Service, discovered *model.DiscoveredService) {
	if discovered.TargetPort > 0 {
		return
	}
	targetPort := 0
	if service.TargetPortOverride != nil {
		targetPort = *service.TargetPortOverride
	} else if service.Managed {
		// Swarm does not expose a service's Compose `expose` metadata through
		// ServiceInspect. Keep the unambiguous port from the saved revision.
		targetPort = service.DiscoveredPort
	}
	if targetPort < 1 || targetPort > 65535 {
		return
	}
	discovered.TargetPort = targetPort
	for index := range discovered.Endpoints {
		endpoint := &discovered.Endpoints[index]
		if endpoint.Port == 0 {
			endpoint.Port = targetPort
		}
	}
	issues := discovered.RoutingIssues[:0]
	for _, issue := range discovered.RoutingIssues {
		if !missingTargetPortIssue(issue) {
			issues = append(issues, issue)
		}
	}
	discovered.RoutingIssues = issues
	if discovered.ConfigurationError == "" && len(issues) == 0 {
		discovered.RoutingEligible = true
		discovered.Enabled = true
	}
}

func missingTargetPortIssue(issue string) bool {
	value := strings.ToLower(issue)
	return strings.Contains(value, "port") && strings.Contains(value, "required") &&
		(strings.Contains(value, "exactly one") || strings.Contains(value, "does not expose") || strings.Contains(value, "target_port"))
}

func discoveredServiceKey(value model.DiscoveredService) string {
	if value.ServiceKey != "" {
		return value.ServiceKey
	}
	return value.ExternalID
}

func reconcileEndpointsTx(ctx context.Context, store *SQLStore, tx *sql.Tx, service model.Service, discovered model.DiscoveredService, existing map[string]model.Endpoint, now time.Time) (model.Service, bool, bool, error) {
	changed := false
	probeChanged := false
	readiness := service.EffectiveReadiness()
	seen := make(map[string]struct{}, len(discovered.Endpoints))
	for _, observed := range discovered.Endpoints {
		if observed.Port == 0 && service.EffectivePort() > 0 {
			observed.Port = service.EffectivePort()
		}
		seen[observed.ExternalID] = struct{}{}
		current, found := existing[observed.ExternalID]
		if !found {
			ready, source := initialReadiness(readiness, observed, now)
			value := model.Endpoint{ID: id.New(id.Endpoint), ServiceID: service.ID, ExternalID: observed.ExternalID, Address: observed.Address, Port: observed.Port, NodeID: observed.NodeID,
				RuntimeState: observed.RuntimeState, HealthState: observed.HealthState, ReadyState: ready, ReadinessSource: source, Weight: max(observed.Weight, 1),
				FirstSeenAt: now, LastSeenAt: now, Revision: 1, CreatedAt: now, UpdatedAt: now}
			if ready == model.Ready {
				value.ReadySince = &now
				changed = true
			} else if ready == model.Draining {
				value.DrainSince = &now
			}
			probeChanged = probeChanged || (service.ConfigurationError == "" && agentProbeSignature(readiness, value.Address, value.Port, value.RuntimeState, value.HealthState) != "")
			if _, err := tx.ExecContext(ctx, store.bind(`INSERT INTO service_instances (`+endpointColumns+`) VALUES (`+columnPlaceholders(endpointColumns)+`)`), endpointArgs(&value)...); err != nil {
				return service, false, false, mapError(err)
			}
			existing[observed.ExternalID] = value
			continue
		}
		wasEligible := current.Eligible()
		ready, source := observedReadiness(readiness, observed, current, now)
		weight := max(observed.Weight, 1)
		needsWrite := current.Address != observed.Address || current.Port != observed.Port || current.NodeID != observed.NodeID ||
			current.RuntimeState != observed.RuntimeState || current.HealthState != observed.HealthState || current.ReadyState != ready ||
			current.ReadinessSource != source || current.Weight != weight || current.StaleSince != nil
		updated := current
		updated.Address = observed.Address
		updated.Port = observed.Port
		updated.NodeID = observed.NodeID
		updated.RuntimeState = observed.RuntimeState
		updated.HealthState = observed.HealthState
		updated.ReadinessSource = source
		updated.Weight = weight
		updated.StaleSince = nil
		oldProbe, newProbe := "", ""
		if service.ConfigurationError == "" {
			oldProbe = agentProbeSignature(readiness, current.Address, current.Port, current.RuntimeState, current.HealthState)
			newProbe = agentProbeSignature(readiness, updated.Address, updated.Port, updated.RuntimeState, updated.HealthState)
		}
		probeChanged = probeChanged || oldProbe != newProbe
		if ready != updated.ReadyState {
			updated.ReadyState = ready
			if ready == model.Ready {
				updated.ReadySince = &now
				updated.DrainSince = nil
			} else if ready == model.Draining || wasEligible {
				updated.DrainSince = &now
			}
		}
		if wasEligible != updated.Eligible() || current.Address != updated.Address || current.Port != updated.Port || current.Weight != updated.Weight {
			changed = true
		}
		if !needsWrite {
			continue
		}
		updated.LastSeenAt = now
		updated.UpdatedAt = now
		updated.Revision++
		_, err := tx.ExecContext(ctx, store.bind(`UPDATE service_instances SET address=:address,port=:port,node_id=:node_id,runtime_state=:runtime_state,health_state=:health_state,ready_state=:ready_state,readiness_source=:readiness_source,weight=:weight,
			ready_since=:ready_since,drain_since=:drain_since,stale_since=NULL,last_seen_at=:last_seen_at,revision=revision+1,updated_at=:updated_at WHERE id=:id`), updated.Address, updated.Port, updated.NodeID,
			updated.RuntimeState, updated.HealthState, updated.ReadyState, updated.ReadinessSource, updated.Weight, ptrTime(updated.ReadySince), ptrTime(updated.DrainSince), now, now, updated.ID)
		if err != nil {
			return service, false, false, err
		}
		existing[observed.ExternalID] = updated
	}
	for externalID, value := range existing {
		if _, ok := seen[externalID]; ok || value.ReadyState == model.Removed {
			continue
		}
		if value.Eligible() {
			changed = true
		}
		needsWrite := value.RuntimeState != model.EndpointStopping || value.ReadyState != model.Draining
		oldProbe := ""
		if service.ConfigurationError == "" {
			oldProbe = agentProbeSignature(readiness, value.Address, value.Port, value.RuntimeState, value.HealthState)
		}
		value.RuntimeState = model.EndpointStopping
		probeChanged = probeChanged || oldProbe != ""
		if value.ReadyState != model.Draining {
			value.ReadyState = model.Draining
			value.DrainSince = &now
		}
		if !needsWrite {
			continue
		}
		value.LastSeenAt = now
		value.UpdatedAt = now
		value.Revision++
		if _, err := tx.ExecContext(ctx, store.bind(`UPDATE service_instances SET runtime_state=:runtime_state,ready_state=:ready_state,drain_since=:drain_since,last_seen_at=:last_seen_at,revision=revision+1,updated_at=:updated_at WHERE id=:id`), value.RuntimeState, value.ReadyState, ptrTime(value.DrainSince), now, now, value.ID); err != nil {
			return service, false, false, err
		}
		existing[externalID] = value
	}
	var running, ready int
	for _, endpoint := range existing {
		if endpoint.ReadyState == model.Removed {
			continue
		}
		if endpoint.RuntimeState == model.EndpointRunning {
			running++
			if endpoint.ReadyState == model.Ready && endpoint.HealthState != model.HealthUnhealthy {
				ready++
			}
		}
	}
	running = max(running, discovered.RunningInstances)
	status := model.ServiceUnavailable
	if ready > 0 && ready == discovered.DesiredInstances {
		status = model.ServiceAvailable
	} else if ready > 0 {
		status = model.ServiceDegraded
	}
	if service.DesiredInstances != discovered.DesiredInstances || service.RunningInstances != running ||
		service.RuntimeReadyInstances != discovered.ReadyInstances || service.ReadyInstances != ready || service.Status != status {
		// Endpoint health and task counts are observations. Changing them must
		// not expire a Service If-Match used for a configuration operation.
		// A changed desired replica count already advances the revision in
		// upsertDiscoveredServiceTx above.
		if _, err := tx.ExecContext(ctx, store.bind(`UPDATE services SET desired_instances=:desired_instances,running_instances=:running_instances,runtime_ready_instances=:runtime_ready_instances,ready_instances=:ready_instances,status=:status,last_seen_at=:last_seen_at,updated_at=:updated_at WHERE id=:id`), discovered.DesiredInstances, running, discovered.ReadyInstances, ready, status, now, now, service.ID); err != nil {
			return service, false, false, err
		}
	}
	service.DesiredInstances = discovered.DesiredInstances
	service.RunningInstances = running
	service.RuntimeReadyInstances = discovered.ReadyInstances
	service.ReadyInstances = ready
	service.Status = status
	service.LastSeenAt = &now
	service.UpdatedAt = now
	return service, changed, probeChanged, nil
}

func reconcileRoutesTx(ctx context.Context, store *SQLStore, tx *sql.Tx, poolID string, service model.Service, routes []model.DiscoveredRoute, domains map[string]model.Domain, existing map[string]model.Route, now time.Time) (bool, error) {
	changed := false
	seen := map[string]struct{}{}
	for _, observed := range routes {
		seen[observed.ExternalKey] = struct{}{}
		httpEnabled, httpsEnabled := discoveredRouteEntrypoints(observed)
		domain, found := domains[observed.Host]
		domainConflict := ""
		if !found {
			domain = model.Domain{ID: id.New(id.Domain), PoolID: poolID, Host: observed.Host, TLSMode: observed.TLSMode, HTTPSRedirect: observed.HTTPSRedirect, HTTPEnabled: httpEnabled, HTTPSEnabled: httpsEnabled, Status: domainStatusForTLS(observed.TLSMode), Source: model.SourceDiscovery, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
			if domain.TLSMode == model.TLSManaged {
				domain.ACMEChallenge = model.ACMEHTTP01
			}
			_, err := tx.ExecContext(ctx, store.bind(`INSERT INTO domains (`+domainColumns+`) VALUES (`+columnPlaceholders(domainColumns)+`)`), domainArgs(&domain)...)
			if err != nil {
				return false, mapError(err)
			}
			if domain.TLSMode == model.TLSManaged {
				if err := ensureDiscoveredManagedCertificateTx(ctx, store, tx, &domain, now); err != nil {
					return false, err
				}
			}
			domains[observed.Host] = domain
			changed = true
		} else if domain.Source == model.SourceDiscovery {
			tlsModeChanged := domain.TLSMode != observed.TLSMode
			policyChanged := tlsModeChanged || domain.HTTPSRedirect != observed.HTTPSRedirect || domain.HTTPEnabled != httpEnabled || domain.HTTPSEnabled != httpsEnabled
			if policyChanged {
				var activeClaims int
				err := tx.QueryRowContext(ctx, store.bind(`SELECT COUNT(*) FROM routes r
					JOIN services s ON s.id=r.service_id JOIN runtimes rt ON rt.id=s.runtime_id
					WHERE r.domain_id=:domain_id AND r.service_id<>:service_id AND r.source=:source AND r.enabled=:enabled
					AND s.configuration_error='' AND COALESCE(s.enabled_override,s.discovered_enabled)=:service_enabled AND rt.enabled=:enabled_2`),
					domain.ID, service.ID, model.SourceDiscovery, true, true, true).Scan(&activeClaims)
				if err != nil {
					return false, err
				}
				if activeClaims > 0 {
					domainConflict = fmt.Sprintf("%s host %s already has incompatible entrypoints or TLS policy", discoveryDomainTLSConflictPrefix, domain.Host)
					policyChanged = false
				}
			}
			status := domainStatusForTLS(observed.TLSMode)
			if domain.CertificateID != "" && !tlsModeChanged && domainConflict == "" {
				status = domain.Status
			}
			if domainConflict == "" && (policyChanged || !domain.Enabled) {
				domain.TLSMode = observed.TLSMode
				if tlsModeChanged {
					domain.ACMEChallenge = ""
					domain.DNSProviderRef = ""
					if observed.TLSMode == model.TLSManaged {
						domain.ACMEChallenge = model.ACMEHTTP01
					}
				}
				domain.HTTPSRedirect = observed.HTTPSRedirect
				domain.HTTPEnabled = httpEnabled
				domain.HTTPSEnabled = httpsEnabled
				domain.Status = status
				domain.Enabled = true
				if tlsModeChanged || observed.TLSMode != model.TLSManaged {
					domain.CertificateID = ""
				}
				if _, err := tx.ExecContext(ctx, store.bind(`UPDATE domains SET tls_mode=:tls_mode,acme_challenge=:acme_challenge,dns_provider_ref=:dns_provider_ref,https_redirect=:https_redirect,http_enabled=:http_enabled,https_enabled=:https_enabled,http3_enabled=CASE WHEN :https_enabled_for_http3 THEN http3_enabled ELSE FALSE END,status=:status,certificate_id=:certificate_id,enabled=:enabled,last_error='',revision=revision+1,updated_at=:updated_at WHERE id=:id`),
					domain.TLSMode, domain.ACMEChallenge, domain.DNSProviderRef, domain.HTTPSRedirect, domain.HTTPEnabled, domain.HTTPSEnabled, domain.HTTPSEnabled, domain.Status, nullString(domain.CertificateID), true, now, domain.ID); err != nil {
					return false, err
				}
				domain.Revision++
				domain.UpdatedAt = now
			}
			if domainConflict == "" && domain.TLSMode == model.TLSManaged && domain.CertificateID == "" {
				if err := ensureDiscoveredManagedCertificateTx(ctx, store, tx, &domain, now); err != nil {
					return false, err
				}
			}
			domains[observed.Host] = domain
		}
		route, found := existing[observed.ExternalKey]
		if !found {
			routeError := domainConflict
			if routeError == "" && !service.RoutingEligible {
				routeError = discoveryDNSAvailabilityError(service)
			}
			enabled := routeError == ""
			status := model.RouteActive
			if !enabled {
				status = model.RouteError
			}
			route = model.Route{ID: id.New(id.Route), DomainID: domain.ID, ServiceID: service.ID, ExternalKey: observed.ExternalKey, Path: observed.Path, Priority: observed.Priority, StripPath: observed.StripPath, TargetPort: observed.TargetPort, UpstreamProtocol: observed.UpstreamProtocol, UpstreamServerName: observed.UpstreamServerName, WebsocketEnabled: observed.WebsocketEnabled, Enabled: enabled, Status: status, Source: model.SourceDiscovery, LastError: routeError, Revision: 1, CreatedAt: now, UpdatedAt: now}
			_, err := tx.ExecContext(ctx, store.bind(`INSERT INTO routes (`+routeBaseColumns+`) VALUES (`+columnPlaceholders(routeBaseColumns)+`)`), route.ID, route.DomainID, route.ServiceID, route.ExternalKey, route.Path, route.Priority, route.StripPath, route.TargetPort, route.UpstreamProtocol, route.UpstreamServerName, route.WebsocketEnabled, enabled, route.Status, route.Source, routeError, route.Revision, now, now)
			if err != nil {
				return false, mapError(err)
			}
			existing[observed.ExternalKey] = route
			changed = true
		} else {
			precedenceConflicted := strings.HasPrefix(route.LastError, discoveryRouteConflictPrefix)
			wasActive := route.Enabled && route.Status == model.RouteActive
			dnsNotReady := !service.RoutingEligible && !wasActive
			enabled := !precedenceConflicted && domainConflict == "" && !dnsNotReady
			lastError := domainConflict
			if precedenceConflicted {
				lastError = route.LastError
			} else if dnsNotReady && domainConflict == "" {
				lastError = discoveryDNSAvailabilityError(service)
			}
			status := model.RouteActive
			if !enabled {
				status = model.RouteError
			}
			needsWrite := route.DomainID != domain.ID || route.Path != observed.Path || route.Priority != observed.Priority || route.StripPath != observed.StripPath || route.TargetPort != observed.TargetPort || route.UpstreamProtocol != observed.UpstreamProtocol || route.UpstreamServerName != observed.UpstreamServerName || route.WebsocketEnabled != observed.WebsocketEnabled || route.Enabled != enabled || route.Status != status || route.LastError != lastError
			if needsWrite {
				changed = true
			}
			if needsWrite {
				_, err := tx.ExecContext(ctx, store.bind(`UPDATE routes SET domain_id=:domain_id,path=:path,priority=:priority,strip_path=:strip_path,target_port=:target_port,upstream_protocol=:upstream_protocol,upstream_server_name=:upstream_server_name,websocket_enabled=:websocket_enabled,enabled=:enabled,route_status=:route_status,last_error=:last_error,revision=revision+1,updated_at=:updated_at WHERE id=:id`), domain.ID, observed.Path, observed.Priority, observed.StripPath, observed.TargetPort, observed.UpstreamProtocol, observed.UpstreamServerName, observed.WebsocketEnabled, enabled, status, lastError, now, route.ID)
				if err != nil {
					return false, err
				}
				route.Revision++
				route.UpdatedAt = now
			}
			route.DomainID = domain.ID
			route.Path = observed.Path
			route.Priority = observed.Priority
			route.StripPath = observed.StripPath
			route.TargetPort = observed.TargetPort
			route.UpstreamProtocol = observed.UpstreamProtocol
			route.UpstreamServerName = observed.UpstreamServerName
			route.WebsocketEnabled = observed.WebsocketEnabled
			route.Enabled = enabled
			route.Status = status
			route.LastError = lastError
			existing[observed.ExternalKey] = route
		}
	}
	for externalKey, route := range existing {
		if _, ok := seen[externalKey]; ok || (!route.Enabled && !discoveryRouteHasConflict(route.LastError)) {
			continue
		}
		changed = true
		if _, err := tx.ExecContext(ctx, store.bind(`UPDATE routes SET enabled=:enabled,last_error='',revision=revision+1,updated_at=:updated_at WHERE id=:id`), false, now, route.ID); err != nil {
			return false, err
		}
		route.Enabled = false
		route.LastError = ""
		route.Revision++
		route.UpdatedAt = now
		existing[externalKey] = route
	}
	return changed, nil
}

func ensureDiscoveredManagedCertificateTx(ctx context.Context, store *SQLStore, tx *sql.Tx, domain *model.Domain, now time.Time) error {
	if domain.CertificateID != "" {
		return nil
	}
	var desiredRevision int64
	if err := tx.QueryRowContext(ctx, store.bind(`SELECT desired_revision FROM gateway_pools WHERE id=:id`), domain.PoolID).Scan(&desiredRevision); err != nil {
		return mapError(err)
	}
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: domain.PoolID, Provider: "acme", ACMEChallenge: domain.ACMEChallenge, DNSProviderRef: domain.DNSProviderRef, Status: model.CertificatePending,
		PrimaryDomain: domain.Host, Domains: []string{domain.Host}, DistributionRevision: desiredRevision + 1,
		Revision: 1, CreatedAt: now, UpdatedAt: now}
	if certificate.ACMEChallenge == "" {
		certificate.ACMEChallenge = model.ACMEHTTP01
	}
	if _, err := tx.ExecContext(ctx, store.bind(`INSERT INTO certificates (`+certificateColumns+`) VALUES (`+columnPlaceholders(certificateColumns)+`)`), certificateArgs(&certificate)...); err != nil {
		return mapError(err)
	}
	if _, err := tx.ExecContext(ctx, store.bind(`INSERT INTO certificate_domains(certificate_id,domain_id) VALUES(:certificate_id,:domain_id)`), certificate.ID, domain.ID); err != nil {
		return mapError(err)
	}
	domain.CertificateID = certificate.ID
	domain.Status = model.DomainCertPending
	if _, err := tx.ExecContext(ctx, store.bind(`UPDATE domains SET certificate_id=:certificate_id,status=:status,revision=revision+1,updated_at=:updated_at WHERE id=:id`),
		certificate.ID, domain.Status, now, domain.ID); err != nil {
		return err
	}
	return nil
}

const (
	discoveryRouteConflictPrefix     = "route precedence conflict:"
	discoveryDomainTLSConflictPrefix = "domain TLS conflict:"
	discoveryDNSNotReadyPrefix       = "service DNS not ready:"
)

func discoveryDNSAvailabilityError(service model.Service) string {
	reason := strings.Join(service.RoutingIssues, "; ")
	if reason == "" {
		reason = fmt.Sprintf("Docker DNS status is %s", service.DNSStatus)
	}
	return discoveryDNSNotReadyPrefix + " " + reason
}

func discoveryRouteHasConflict(lastError string) bool {
	return strings.HasPrefix(lastError, discoveryRouteConflictPrefix) || strings.HasPrefix(lastError, discoveryDomainTLSConflictPrefix)
}

type discoveryRouteClaim struct {
	id        string
	domainID  string
	path      string
	priority  int
	enabled   bool
	lastError string
}

func resolveDiscoveryRouteConflictsTx(ctx context.Context, store *SQLStore, tx *sql.Tx, poolID string, now time.Time) error {
	rows, err := tx.QueryContext(ctx, store.bind(`SELECT r.id,r.domain_id,r.path,r.priority,r.enabled,r.last_error FROM routes r
		JOIN domains d ON d.id=r.domain_id WHERE d.pool_id=:pool_id AND r.source=:source AND (r.enabled=:enabled OR r.last_error LIKE :last_error)
		AND NOT EXISTS (SELECT 1 FROM control_documents guard WHERE guard.parent_id=r.id AND guard.kind IN ('project-guard','deleted-project-guard'))
		ORDER BY r.domain_id,r.path,r.priority,r.id`), poolID, model.SourceDiscovery, true, discoveryRouteConflictPrefix+"%")
	if err != nil {
		return err
	}
	groups := make(map[string][]discoveryRouteClaim)
	for rows.Next() {
		var claim discoveryRouteClaim
		if err = rows.Scan(&claim.id, &claim.domainID, &claim.path, &claim.priority, &claim.enabled, &claim.lastError); err != nil {
			rows.Close()
			return err
		}
		key := fmt.Sprintf("%s\x00%s\x00%d", claim.domainID, claim.path, claim.priority)
		groups[key] = append(groups[key], claim)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, claims := range groups {
		if len(claims) == 1 {
			claim := claims[0]
			if strings.HasPrefix(claim.lastError, discoveryRouteConflictPrefix) {
				if _, err = tx.ExecContext(ctx, store.bind(`UPDATE routes SET enabled=:enabled,route_status=:route_status,last_error='',revision=revision+1,updated_at=:updated_at WHERE id=:id`), true, model.RouteActive, now, claim.id); err != nil {
					return err
				}
			}
			continue
		}
		message := fmt.Sprintf("%s %s at priority %d has %d discovery claims", discoveryRouteConflictPrefix, claims[0].path, claims[0].priority, len(claims))
		for _, claim := range claims {
			if !claim.enabled && claim.lastError == message {
				continue
			}
			if _, err = tx.ExecContext(ctx, store.bind(`UPDATE routes SET enabled=:enabled,route_status=:route_status,last_error=:last_error,revision=revision+1,updated_at=:updated_at WHERE id=:id`), false, model.RouteError, message, now, claim.id); err != nil {
				return err
			}
		}
	}
	return nil
}

type gatewayDataPlaneState struct {
	kind                                           string
	domainID, host, tlsMode, certificateID         string
	redirect, httpEnabled, httpsEnabled            bool
	routeID, serviceID, path, protocol, serverName string
	runtimeID, serviceKey, qualifiedName, dnsName  string
	priority, targetPort                           int
	stripPath, websocket, paused                   bool
}

type gatewayRouteCandidate struct {
	state     gatewayDataPlaneState
	source    model.Source
	status    model.RouteStatus
	dnsStatus model.DNSStatus
	enabled   bool
}

func gatewayDataPlaneStateTx(ctx context.Context, store *SQLStore, tx *sql.Tx, poolID string) ([]gatewayDataPlaneState, error) {
	var values []gatewayDataPlaneState
	domainRows, err := tx.QueryContext(ctx, store.bind(`SELECT id,host,tls_mode,https_redirect,http_enabled,https_enabled,COALESCE(certificate_id,'')
		FROM domains WHERE pool_id=:pool_id AND enabled=:enabled ORDER BY host,id`), poolID, true)
	if err != nil {
		return nil, err
	}
	for domainRows.Next() {
		value := gatewayDataPlaneState{kind: "domain"}
		if err = domainRows.Scan(&value.domainID, &value.host, &value.tlsMode, &value.redirect, &value.httpEnabled, &value.httpsEnabled, &value.certificateID); err != nil {
			domainRows.Close()
			return nil, err
		}
		values = append(values, value)
	}
	if err = domainRows.Err(); err != nil {
		domainRows.Close()
		return nil, err
	}
	if err = domainRows.Close(); err != nil {
		return nil, err
	}

	routeRows, err := tx.QueryContext(ctx, store.bind(`SELECT r.id,r.domain_id,r.service_id,r.path,r.priority,r.strip_path,r.target_port,
		r.upstream_protocol,r.upstream_server_name,r.websocket_enabled,r.source,r.route_status,d.host,d.tls_mode,d.https_redirect,COALESCE(d.certificate_id,''),
		s.discovered_protocol,s.protocol_override,s.discovered_port,s.target_port_override,s.discovered_enabled,s.enabled_override,
		s.runtime_id,s.service_key,s.qualified_name,s.dns_name,s.dns_status
		FROM routes r JOIN domains d ON d.id=r.domain_id JOIN services s ON s.id=r.service_id JOIN runtimes rt ON rt.id=s.runtime_id
		WHERE d.pool_id=:pool_id AND rt.pool_id=:pool_id_2 AND d.enabled=:enabled AND ((r.enabled=:enabled_2 AND r.route_status=:route_status) OR r.route_status=:route_status_2) AND rt.enabled=:enabled_3
		AND (r.route_status=:route_status_3 OR s.configuration_error='')`),
		poolID, poolID, true, true, model.RouteActive, model.RoutePaused, true, model.RoutePaused)
	if err != nil {
		return nil, err
	}
	var candidates []gatewayRouteCandidate
	for routeRows.Next() {
		candidate := gatewayRouteCandidate{state: gatewayDataPlaneState{kind: "route"}}
		var routeProtocol, discoveredProtocol string
		var protocolOverride sql.NullString
		var discoveredPort, routePort int
		var portOverride sql.NullInt64
		var discoveredEnabled bool
		var enabledOverride sql.NullBool
		if err = routeRows.Scan(&candidate.state.routeID, &candidate.state.domainID, &candidate.state.serviceID,
			&candidate.state.path, &candidate.state.priority, &candidate.state.stripPath, &routePort, &routeProtocol,
			&candidate.state.serverName, &candidate.state.websocket, &candidate.source, &candidate.status, &candidate.state.host,
			&candidate.state.tlsMode, &candidate.state.redirect, &candidate.state.certificateID, &discoveredProtocol,
			&protocolOverride, &discoveredPort, &portOverride, &discoveredEnabled, &enabledOverride,
			&candidate.state.runtimeID, &candidate.state.serviceKey, &candidate.state.qualifiedName, &candidate.state.dnsName,
			&candidate.dnsStatus); err != nil {
			routeRows.Close()
			return nil, err
		}
		candidate.enabled = true
		if enabledOverride.Valid {
			candidate.enabled = enabledOverride.Bool
		}
		candidate.state.protocol = routeProtocol
		if candidate.state.protocol == "" {
			candidate.state.protocol = discoveredProtocol
			if protocolOverride.Valid {
				candidate.state.protocol = protocolOverride.String
			}
		}
		candidate.state.targetPort = routePort
		if candidate.state.targetPort == 0 {
			candidate.state.targetPort = discoveredPort
			if portOverride.Valid {
				candidate.state.targetPort = int(portOverride.Int64)
			}
		}
		// A PAUSED Route deliberately stays in the snapshot and renders a local
		// 503. It does not need a reachable backend, but still requires the
		// discovery-verified DNS identity used for trace metadata.
		candidate.state.paused = candidate.status == model.RoutePaused
		// Route activation is the configuration trust boundary. Once an ACTIVE
		// route has been validated, transient Docker DNS absence is backend
		// health, not a reason to withdraw or reload its stable DNS upstream.
		// A pool-wide DNS ownership conflict remains fail-closed because it can
		// route traffic to the wrong service.
		if candidate.state.dnsName != "" && candidate.dnsStatus != model.DNSConflict && (candidate.state.paused || candidate.enabled) {
			candidates = append(candidates, candidate)
		}
	}
	if err = routeRows.Err(); err != nil {
		routeRows.Close()
		return nil, err
	}
	if err = routeRows.Close(); err != nil {
		return nil, err
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].state.host != candidates[j].state.host {
			return candidates[i].state.host < candidates[j].state.host
		}
		if len(candidates[i].state.path) != len(candidates[j].state.path) {
			return len(candidates[i].state.path) > len(candidates[j].state.path)
		}
		if candidates[i].source.Priority() != candidates[j].source.Priority() {
			return candidates[i].source.Priority() > candidates[j].source.Priority()
		}
		if candidates[i].state.priority != candidates[j].state.priority {
			return candidates[i].state.priority > candidates[j].state.priority
		}
		return candidates[i].state.routeID < candidates[j].state.routeID
	})

	seenLocations := make(map[string]struct{})
	for _, candidate := range candidates {
		key := candidate.state.host + "\x00" + candidate.state.path
		if _, exists := seenLocations[key]; exists {
			continue
		}
		seenLocations[key] = struct{}{}
		values = append(values, candidate.state)
	}
	return values, nil
}

type gatewayProbeState struct {
	serviceID string
	dnsName   string
	port      int
	policy    model.ReadinessPolicy
}

// gatewayProbeStateTx deliberately excludes container and task identity. A
// recreate, task IP change, or replica count change does not alter a
// service-DNS probe target and therefore must not publish a new probe plan.
func gatewayProbeStateTx(ctx context.Context, store *SQLStore, tx *sql.Tx, poolID string) ([]gatewayProbeState, error) {
	rows, err := tx.QueryContext(ctx, store.bind(`SELECT `+serviceColumns+` FROM services
		WHERE runtime_id IN (SELECT id FROM runtimes WHERE pool_id=:pool_id AND enabled=:enabled)
		AND (dns_status<>:dns_status OR EXISTS (SELECT 1 FROM routes probe_route WHERE probe_route.service_id=services.id
			AND probe_route.enabled=:enabled_2 AND probe_route.route_status=:route_status)) ORDER BY id`),
		poolID, true, model.DNSMissing, true, model.RouteActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []gatewayProbeState
	for rows.Next() {
		service, scanErr := scanService(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if !serviceRouteEnabled(service) || service.ConfigurationError != "" || service.EffectiveDNSName() == "" ||
			service.DNSStatus == model.DNSConflict || service.EffectivePort() < 1 {
			continue
		}
		values = append(values, gatewayProbeState{serviceID: service.ID, dnsName: service.EffectiveDNSName(),
			port: service.EffectivePort(), policy: service.EffectiveReadiness()})
	}
	return values, rows.Err()
}

func initialReadiness(policy model.ReadinessPolicy, observed model.DiscoveredEndpoint, now time.Time) (model.ReadyState, model.ReadinessMode) {
	return observedReadiness(policy, observed, model.Endpoint{ReadyState: model.ReadyStarting, FirstSeenAt: now}, now)
}
func observedReadiness(policy model.ReadinessPolicy, observed model.DiscoveredEndpoint, existing model.Endpoint, now time.Time) (model.ReadyState, model.ReadinessMode) {
	if observed.RuntimeState == model.EndpointStopping || observed.RuntimeState == model.EndpointDead {
		return model.Draining, policy.Mode
	}
	if observed.RuntimeState != model.EndpointRunning {
		return model.ReadyStarting, policy.Mode
	}
	if net.ParseIP(observed.Address) == nil || observed.Port < 1 || observed.Port > 65535 {
		return model.NotReady, policy.Mode
	}
	mode := policy.Mode
	if mode == model.ReadinessAuto {
		if policy.Path != "" {
			mode = model.ReadinessHTTP
		} else if observed.HealthState != model.HealthUnknown {
			mode = model.ReadinessDocker
		} else if policy.TCPFallback {
			mode = model.ReadinessTCP
		} else {
			mode = model.ReadinessGrace
		}
	}
	switch mode {
	case model.ReadinessDocker:
		if observed.HealthState == model.HealthHealthy {
			return model.Ready, mode
		}
		return model.NotReady, mode
	case model.ReadinessGrace:
		started := existing.FirstSeenAt
		if observed.StartedAt != nil {
			started = *observed.StartedAt
		}
		if now.Sub(started) >= policy.StartupGracePeriod {
			return model.Ready, mode
		}
		return model.ReadyStarting, mode
	case model.ReadinessHTTP, model.ReadinessTCP:
		// HTTP/TCP readiness is evaluated once per stable Service DNS target by
		// the Gateway. It cannot truthfully classify individual Docker replicas.
		// At the instance layer a running replica is therefore runtime-ready;
		// an explicit Docker healthcheck can still mark it unhealthy, while the
		// Service DNS status carries end-to-end reachability.
		return model.Ready, mode
	default:
		return model.NotReady, mode
	}
}

func agentProbeSignature(policy model.ReadinessPolicy, address string, port int, runtimeState model.RuntimeState, healthState model.HealthState) string {
	if runtimeState != model.EndpointRunning || net.ParseIP(address) == nil || port < 1 || port > 65535 {
		return ""
	}
	mode := policy.Mode
	if mode == model.ReadinessAuto {
		switch {
		case policy.Path != "":
			mode = model.ReadinessHTTP
		case healthState != model.HealthUnknown:
			mode = model.ReadinessDocker
		case policy.TCPFallback:
			mode = model.ReadinessTCP
		default:
			mode = model.ReadinessGrace
		}
	}
	if mode != model.ReadinessHTTP && mode != model.ReadinessTCP {
		return ""
	}
	return fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00%d\x00%d\x00%t",
		mode, address, port, policy.Path, policy.Scheme, int64(policy.Interval), int64(policy.Timeout),
		policy.SuccessThreshold, policy.FailureThreshold, int64(policy.StartupGracePeriod), int64(policy.StartupTimeout), policy.TCPFallback)
}

func agentProbeToken(policy model.ReadinessPolicy, address string, port int, runtimeState model.RuntimeState, healthState model.HealthState) string {
	signature := agentProbeSignature(policy, address, port, runtimeState, healthState)
	if signature == "" {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(signature)))
}

func serviceDNSProbeToken(policy model.ReadinessPolicy, dnsName string, port int) string {
	if dnsName == "" || len(dnsName) > 253 || net.ParseIP(dnsName) != nil || port < 1 || port > 65535 {
		return ""
	}
	for _, character := range dnsName {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' || character == '.' {
			continue
		}
		return ""
	}
	policy = normalizedDNSProbePolicy(policy)
	if policy.Mode != model.ReadinessHTTP && policy.Mode != model.ReadinessTCP {
		return ""
	}
	signature := fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s\x00%d\x00%d\x00%d\x00%d",
		policy.Mode, dnsName, port, policy.Path, policy.Scheme, int64(policy.Interval), int64(policy.Timeout),
		policy.SuccessThreshold, policy.FailureThreshold)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(signature)))
}

func effectiveProtocol(value string) string {
	if value == "" {
		return "http"
	}
	return value
}
func domainStatusForTLS(value model.TLSMode) model.DomainStatus {
	if value == model.TLSDisabled {
		return model.DomainReady
	}
	return model.DomainCertPending
}

func discoveredRouteEntrypoints(value model.DiscoveredRoute) (bool, bool) {
	// Provider parsers always set an explicit policy. The fallback keeps custom
	// RuntimeSnapshot producers on the documented label default: HTTP only.
	if !value.HTTPEnabled && !value.HTTPSEnabled {
		return true, false
	}
	return value.HTTPEnabled, value.HTTPSEnabled
}
