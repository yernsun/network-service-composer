package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func (s *SQLStore) ListServiceDomainBindings(ctx context.Context, serviceID string) ([]model.DomainBinding, error) {
	service, err := s.GetService(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	runtime, err := s.GetRuntime(ctx, service.RuntimeID)
	if err != nil {
		return nil, err
	}
	dnsTargets, err := s.ListGatewayDNSTargets(ctx, runtime.PoolID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+routeColumns+` FROM routes WHERE service_id=:service_id AND route_status<>:route_status ORDER BY id`), serviceID, model.RouteDisabled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var routes []model.Route
	for rows.Next() {
		route, scanErr := scanRoute(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		routes = append(routes, route)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	values := make([]model.DomainBinding, 0, len(routes))
	for _, route := range routes {
		domain, getErr := s.GetDomain(ctx, route.DomainID)
		if getErr != nil {
			return nil, getErr
		}
		values = append(values, model.DomainBinding{ID: route.ID, Service: service, Domain: domain, Route: route,
			Source: route.Source, Status: route.Status, Editable: route.Source == model.SourceAPI, DNSTargets: dnsTargets})
	}
	return values, nil
}

func (s *SQLStore) ListGatewayDNSTargets(ctx context.Context, poolID string) ([]model.DNSTarget, error) {
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+gatewayColumns+` FROM gateways WHERE pool_id=:pool_id AND enabled=:enabled ORDER BY id`), poolID, true)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := make([]model.DNSTarget, 0)
	seen := make(map[string]struct{})
	for rows.Next() {
		gateway, scanErr := scanGateway(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		recordType, value, ok := gatewayDNSRecord(gateway.Address)
		if !ok {
			continue
		}
		key := recordType + "\x00" + value
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, model.DNSTarget{GatewayID: gateway.ID, GatewayName: gateway.Name, RecordType: recordType, Value: value})
	}
	return targets, rows.Err()
}

func gatewayDNSRecord(address string) (string, string, bool) {
	value := strings.TrimSpace(address)
	if value == "" || value != address {
		return "", "", false
	}
	if parsed := net.ParseIP(value); parsed != nil {
		if parsed.To4() != nil {
			return "A", parsed.String(), true
		}
		return "AAAA", parsed.String(), true
	}
	value = strings.TrimSuffix(strings.ToLower(value), ".")
	if value == "" || len(value) > 253 || strings.ContainsAny(value, "/\\@:#?* \t\r\n") {
		return "", "", false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", "", false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return "", "", false
			}
		}
	}
	return "CNAME", value, true
}

func (s *SQLStore) CreateServiceDomainBinding(ctx context.Context, domain *model.Domain, certificate *model.Certificate, route *model.Route, operation *model.DeploymentOperation, servicePatch *model.Service, serviceExpected int64) error {
	return s.createServiceDomainBinding(ctx, domain, certificate, route, operation, servicePatch, serviceExpected, nil, 0)
}

func (s *SQLStore) CreateServiceDomainBindingWithDraft(ctx context.Context, domain *model.Domain, certificate *model.Certificate, route *model.Route, operation *model.DeploymentOperation, servicePatch *model.Service, serviceExpected int64, draft ControlWrite) error {
	if draft.Document.Kind != "domain-draft" || draft.Expected < 1 || draft.Document.ParentID == "" {
		return ErrConflict
	}
	return s.createServiceDomainBinding(ctx, domain, certificate, route, operation, servicePatch, serviceExpected, &draft, 0)
}

func (s *SQLStore) CreateServiceDomainBindingWithIntent(ctx context.Context, domain *model.Domain, certificate *model.Certificate, route *model.Route, operation *model.DeploymentOperation, intent ControlWrite, expected int64) error {
	if operation == nil || intent.Document.Kind != "service-entrypoint-intent" || intent.Document.ParentID != operation.ApplicationID || expected < 1 {
		return ErrConflict
	}
	return s.createServiceDomainBinding(ctx, domain, certificate, route, operation, nil, 0, &intent, expected)
}

func (s *SQLStore) createServiceDomainBinding(ctx context.Context, domain *model.Domain, certificate *model.Certificate, route *model.Route, operation *model.DeploymentOperation, servicePatch *model.Service, serviceExpected int64, draft *ControlWrite, expectedApplication int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if expectedApplication > 0 {
		query := `SELECT revision FROM applications WHERE id=:id`
		if s.dialect != SQLite {
			query += ` FOR UPDATE`
		}
		var current int64
		if err = tx.QueryRowContext(ctx, s.bind(query), operation.ApplicationID).Scan(&current); err != nil {
			return mapError(err)
		}
		if current != expectedApplication {
			return ErrPrecondition
		}
	}
	for _, resource := range []string{route.ServiceID} {
		if err = s.guardProjectResourceTx(ctx, tx, resource); err != nil {
			return err
		}
	}
	var servicePool string
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT rt.pool_id FROM services sv JOIN runtimes rt ON rt.id=sv.runtime_id WHERE sv.id=:id`), route.ServiceID).Scan(&servicePool); err != nil {
		return mapError(err)
	}
	probeBefore, err := gatewayProbeStateTx(ctx, s, tx, servicePool)
	if err != nil {
		return err
	}
	if servicePatch != nil {
		if servicePatch.ID != route.ServiceID || serviceExpected < 1 {
			return fmt.Errorf("%w: invalid binding service override", ErrConflict)
		}
		result, updateErr := tx.ExecContext(ctx, s.bind(`UPDATE services SET target_port_override=:target_port_override,enabled_override=:enabled_override,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`),
			intPtr(servicePatch.TargetPortOverride), boolPtr(servicePatch.EnabledOverride), servicePatch.UpdatedAt, servicePatch.ID, serviceExpected)
		if updateErr != nil {
			return mapError(updateErr)
		}
		if err = requireAffected(result, true); err != nil {
			return err
		}
	}
	insertedPool, err := insertServiceDomainBindingTx(ctx, s, tx, domain, certificate, route)
	if err != nil {
		return err
	}
	if insertedPool != servicePool {
		return fmt.Errorf("%w: service pool changed while creating binding", ErrConflict)
	}
	if operation != nil {
		if operation.ApplicationID != "" || operation.ServiceID != "" {
			if err = s.insertOperationWithScopeTx(ctx, tx, operation); err != nil {
				return err
			}
		} else if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO deployment_operations (`+operationColumns+`) VALUES (`+columnPlaceholders(operationColumns)+`)`), operationArgs(operation)...); err != nil {
			return mapError(err)
		}
	}
	if draft != nil {
		if err = s.writeControlTx(ctx, tx, []ControlWrite{*draft}); err != nil {
			return err
		}
	}
	if _, err = bumpPoolRevisionTx(ctx, s, tx, servicePool, route.UpdatedAt); err != nil {
		return err
	}
	probeAfter, err := gatewayProbeStateTx(ctx, s, tx, servicePool)
	if err != nil {
		return err
	}
	if !slices.Equal(probeBefore, probeAfter) {
		if _, err = bumpPoolProbeRevisionTx(ctx, s, tx, servicePool, route.UpdatedAt); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func insertServiceDomainBindingTx(ctx context.Context, s *SQLStore, tx *sql.Tx, domain *model.Domain, certificate *model.Certificate, route *model.Route) (string, error) {
	var err error
	var servicePool string
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT rt.pool_id FROM services sv JOIN runtimes rt ON rt.id=sv.runtime_id WHERE sv.id=:id`), route.ServiceID).Scan(&servicePool); err != nil {
		return "", mapError(err)
	}
	var existingPool string
	err = tx.QueryRowContext(ctx, s.bind(`SELECT pool_id FROM domains WHERE id=:id`), domain.ID).Scan(&existingPool)
	switch {
	case err == nil:
		if existingPool != servicePool || domain.PoolID != servicePool {
			return "", fmt.Errorf("%w: domain and service pools differ", ErrConflict)
		}
	case err == sql.ErrNoRows:
		if domain.PoolID != servicePool {
			return "", fmt.Errorf("%w: domain and service pools differ", ErrConflict)
		}
		if certificate != nil {
			if certificate.PoolID != domain.PoolID {
				return "", fmt.Errorf("%w: certificate and domain pools differ", ErrConflict)
			}
			domain.CertificateID = certificate.ID
			if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO certificates (`+certificateColumns+`) VALUES (`+columnPlaceholders(certificateColumns)+`)`), certificateArgs(certificate)...); err != nil {
				return "", mapError(err)
			}
		}
		if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO domains (`+domainColumns+`) VALUES (`+columnPlaceholders(domainColumns)+`)`), domainArgs(domain)...); err != nil {
			return "", mapError(err)
		}
		if domain.CertificateID != "" {
			if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO certificate_domains(certificate_id,domain_id) VALUES(:certificate_id,:domain_id)`), domain.CertificateID, domain.ID); err != nil {
				return "", mapError(err)
			}
		}
	default:
		return "", err
	}
	if err = lockRouteDomainTx(ctx, s, tx, domain.ID); err != nil {
		return "", err
	}
	if route.Status == "" {
		route.Status = model.RouteActive
	}
	if route.Enabled || route.Status == model.RoutePaused {
		if err = ensureRoutePrecedenceAvailableTx(ctx, s, tx, *route, ""); err != nil {
			return "", err
		}
	}
	if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO routes (`+routeBaseColumns+`) VALUES (`+columnPlaceholders(routeBaseColumns)+`)`),
		route.ID, route.DomainID, route.ServiceID, route.ExternalKey, route.Path, route.Priority, route.StripPath,
		route.TargetPort, route.UpstreamProtocol, route.UpstreamServerName, route.WebsocketEnabled, route.Enabled,
		route.Status, route.Source, route.LastError, route.Revision, route.CreatedAt, route.UpdatedAt); err != nil {
		return "", mapError(err)
	}
	return servicePool, nil
}

// UpdateServiceDomainBinding updates domain-level TLS settings and the Route
// under one transaction. Both revisions are checked so a shared Domain cannot
// be overwritten by a stale Service page.
func (s *SQLStore) UpdateServiceDomainBinding(ctx context.Context, serviceID string, domain *model.Domain, certificate *model.Certificate, route *model.Route, domainExpected, routeExpected int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	for _, resource := range []string{serviceID} {
		if err = s.guardProjectResourceTx(ctx, tx, resource); err != nil {
			return err
		}
	}
	var poolID string
	var source model.Source
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT rt.pool_id,r.source FROM routes r JOIN services sv ON sv.id=r.service_id JOIN runtimes rt ON rt.id=sv.runtime_id WHERE r.id=:id AND r.service_id=:service_id`), route.ID, serviceID).Scan(&poolID, &source); err != nil {
		return mapError(err)
	}
	if source != model.SourceAPI || domain.PoolID != poolID || route.ServiceID != serviceID || route.DomainID != domain.ID {
		return fmt.Errorf("%w: binding identity, source, or pool mismatch", ErrConflict)
	}
	if err = lockRouteDomainTx(ctx, s, tx, domain.ID); err != nil {
		return err
	}
	stateBefore, err := gatewayDataPlaneStateTx(ctx, s, tx, poolID)
	if err != nil {
		return err
	}
	var previousCertificateID sql.NullString
	var currentDomainRevision, currentRouteRevision int64
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT certificate_id,revision FROM domains WHERE id=:id`), domain.ID).Scan(&previousCertificateID, &currentDomainRevision); err != nil {
		return mapError(err)
	}
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT revision FROM routes WHERE id=:id AND service_id=:service_id`), route.ID, serviceID).Scan(&currentRouteRevision); err != nil {
		return mapError(err)
	}
	if currentDomainRevision != domainExpected || currentRouteRevision != routeExpected {
		return ErrPrecondition
	}
	if certificate != nil {
		if certificate.PoolID != poolID || certificate.ID != domain.CertificateID {
			return fmt.Errorf("%w: certificate and domain differ", ErrConflict)
		}
		if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO certificates (`+certificateColumns+`) VALUES (`+columnPlaceholders(certificateColumns)+`)`), certificateArgs(certificate)...); err != nil {
			return mapError(err)
		}
	}
	if domain.CertificateID != "" {
		var certificatePool string
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT pool_id FROM certificates WHERE id=:id`), domain.CertificateID).Scan(&certificatePool); err != nil {
			return mapError(err)
		}
		if certificatePool != poolID {
			return fmt.Errorf("%w: certificate and domain pools differ", ErrConflict)
		}
	}
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE domains SET host=:host,tls_mode=:tls_mode,acme_challenge=:acme_challenge,dns_provider_ref=:dns_provider_ref,https_redirect=:https_redirect,http_enabled=:http_enabled,https_enabled=:https_enabled,http3_enabled=:http3_enabled,status=:status,certificate_id=:certificate_id,enabled=:enabled,last_error=:last_error,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`),
		domain.Host, domain.TLSMode, domain.ACMEChallenge, domain.DNSProviderRef, domain.HTTPSRedirect, domain.HTTPEnabled, domain.HTTPSEnabled, domain.HTTP3Enabled, domain.Status,
		nullString(domain.CertificateID), domain.Enabled, domain.LastError, domain.UpdatedAt, domain.ID, domainExpected)
	if err != nil {
		return mapError(err)
	}
	if err = requireAffected(result, true); err != nil {
		return err
	}
	if previousCertificateID.String != domain.CertificateID {
		if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM certificate_domains WHERE domain_id=:domain_id`), domain.ID); err != nil {
			return err
		}
		if previousCertificateID.Valid {
			if _, err = tx.ExecContext(ctx, s.bind(`UPDATE certificates SET status=:status,next_attempt_at=:next_attempt_at,revision=revision+1,updated_at=:updated_at
				WHERE id=:id AND provider='acme' AND NOT EXISTS (SELECT 1 FROM certificate_domains WHERE certificate_id=:certificate_id)`),
				model.CertificateRevoked, domain.UpdatedAt, domain.UpdatedAt, previousCertificateID.String, previousCertificateID.String); err != nil {
				return err
			}
		}
		if domain.CertificateID != "" {
			if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO certificate_domains(certificate_id,domain_id) VALUES(:certificate_id,:domain_id)`), domain.CertificateID, domain.ID); err != nil {
				return mapError(err)
			}
		}
	}
	if route.Enabled {
		if err = ensureRoutePrecedenceAvailableTx(ctx, s, tx, *route, route.ID); err != nil {
			return err
		}
	}
	result, err = tx.ExecContext(ctx, s.bind(`UPDATE routes SET path=:path,priority=:priority,strip_path=:strip_path,target_port=:target_port,upstream_protocol=:upstream_protocol,upstream_server_name=:upstream_server_name,websocket_enabled=:websocket_enabled,enabled=:enabled,route_status=:route_status,last_error=:last_error,revision=revision+1,updated_at=:updated_at WHERE id=:id AND service_id=:service_id AND revision=:revision`),
		route.Path, route.Priority, route.StripPath, route.TargetPort, route.UpstreamProtocol, route.UpstreamServerName,
		route.WebsocketEnabled, route.Enabled, route.Status, route.LastError, route.UpdatedAt, route.ID, serviceID, routeExpected)
	if err != nil {
		return mapError(err)
	}
	if err = requireAffected(result, true); err != nil {
		return err
	}
	stateAfter, err := gatewayDataPlaneStateTx(ctx, s, tx, poolID)
	if err != nil {
		return err
	}
	if !slices.Equal(stateBefore, stateAfter) {
		if _, err = bumpPoolRevisionTx(ctx, s, tx, poolID, route.UpdatedAt); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) DisableServiceDomainBinding(ctx context.Context, serviceID, routeID string, expected int64, now time.Time, operation *model.DeploymentOperation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	for _, resource := range []string{serviceID} {
		if err = s.guardProjectResourceTx(ctx, tx, resource); err != nil {
			return err
		}
	}
	var source model.Source
	var poolID string
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT r.source,rt.pool_id FROM routes r JOIN services s ON s.id=r.service_id JOIN runtimes rt ON rt.id=s.runtime_id WHERE r.id=:id AND r.service_id=:service_id`), routeID, serviceID).Scan(&source, &poolID); err != nil {
		return mapError(err)
	}
	if source != model.SourceAPI {
		return fmt.Errorf("%w: label-sourced bindings are read-only", ErrConflict)
	}
	stateBefore, err := gatewayDataPlaneStateTx(ctx, s, tx, poolID)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE routes SET enabled=:enabled,route_status=:route_status,revision=revision+1,updated_at=:updated_at WHERE id=:id AND service_id=:service_id AND revision=:revision`),
		false, model.RouteDisabled, now, routeID, serviceID, expected)
	if err != nil {
		return mapError(err)
	}
	if err = requireAffected(result, true); err != nil {
		return err
	}
	if operation != nil {
		if operation.ApplicationID != "" || operation.ServiceID != "" {
			if err = s.insertOperationWithScopeTx(ctx, tx, operation); err != nil {
				return err
			}
		} else if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO deployment_operations (`+operationColumns+`) VALUES (`+columnPlaceholders(operationColumns)+`)`), operationArgs(operation)...); err != nil {
			return mapError(err)
		}
	}
	stateAfter, err := gatewayDataPlaneStateTx(ctx, s, tx, poolID)
	if err != nil {
		return err
	}
	if !slices.Equal(stateBefore, stateAfter) {
		if _, err = bumpPoolRevisionTx(ctx, s, tx, poolID, now); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) SetApplicationRouteStatus(ctx context.Context, applicationID string, status model.RouteStatus, message string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	for _, resource := range []string{applicationID} {
		if err = s.guardProjectResourceTx(ctx, tx, resource); err != nil {
			return err
		}
	}
	var poolID string
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT rt.pool_id FROM applications a JOIN runtimes rt ON rt.id=a.runtime_id WHERE a.id=:id`), applicationID).Scan(&poolID); err != nil {
		return mapError(err)
	}
	enabled := status == model.RouteActive || status == model.RoutePending
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE routes SET route_status=:route_status,enabled=:enabled,last_error=:last_error,revision=revision+1,updated_at=:updated_at
		WHERE source=:source AND service_id IN (SELECT id FROM services WHERE application_id=:application_id AND managed=:managed) AND route_status IN (:pending_status,:error_status)`),
		status, enabled, message, now, model.SourceAPI, applicationID, true, model.RoutePending, model.RouteError)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	// Pending and error routes are both excluded from snapshots. Only activation
	// changes the effective data plane and therefore needs a new Pool revision.
	if affected > 0 && status == model.RouteActive {
		if _, err = bumpPoolRevisionTx(ctx, s, tx, poolID, now); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// SetApplicationRouteStatusByID transitions only the binding owned by the
// operation. Domain attach/detach jobs must never revive unrelated ERROR routes
// that happen to belong to the same application.
func (s *SQLStore) SetApplicationRouteStatusByID(ctx context.Context, applicationID, routeID string, status model.RouteStatus, message string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	for _, resource := range []string{applicationID} {
		if err = s.guardProjectResourceTx(ctx, tx, resource); err != nil {
			return err
		}
	}
	var poolID, serviceID, qualifiedName string
	var current model.RouteStatus
	var managed bool
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT rt.pool_id,r.route_status,sv.id,sv.qualified_name,sv.managed FROM routes r JOIN services sv ON sv.id=r.service_id JOIN runtimes rt ON rt.id=sv.runtime_id WHERE r.id=:id AND sv.application_id=:application_id AND r.source=:source`), routeID, applicationID, model.SourceAPI).Scan(&poolID, &current, &serviceID, &qualifiedName, &managed); err != nil {
		return mapError(err)
	}
	if current == status {
		if err = tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	}
	if current != model.RoutePending && current != model.RouteError {
		return fmt.Errorf("%w: route %s cannot transition from %s to %s", ErrConflict, routeID, current, status)
	}
	stateBefore, err := gatewayDataPlaneStateTx(ctx, s, tx, poolID)
	if err != nil {
		return err
	}
	probeBefore, err := gatewayProbeStateTx(ctx, s, tx, poolID)
	if err != nil {
		return err
	}
	activationError := ""
	if status == model.RouteActive {
		if !managed || strings.TrimSpace(qualifiedName) == "" {
			return fmt.Errorf("%w: managed Service DNS identity is unavailable", ErrConflict)
		}
		if _, err = tx.ExecContext(ctx, s.bind(`UPDATE services SET dns_name=:dns_name,dns_status=:dns_status,dns_last_error='',routing_eligible=:routing_eligible,routing_issues='[]',revision=revision+1,updated_at=:updated_at WHERE id=:id`),
			qualifiedName, model.DNSReady, true, now, serviceID); err != nil {
			return err
		}
		if err = resolveServiceDNSConflictsTx(ctx, s, tx, poolID, now); err != nil {
			return err
		}
		var dnsStatus model.DNSStatus
		var dnsError string
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT dns_status,dns_last_error FROM services WHERE id=:id`), serviceID).Scan(&dnsStatus, &dnsError); err != nil {
			return mapError(err)
		}
		if dnsStatus != model.DNSReady {
			activationError = dnsError
			if activationError == "" {
				activationError = fmt.Sprintf("service DNS is %s after network attachment", dnsStatus)
			}
			status = model.RouteError
			message = activationError
		}
	}
	enabled := status == model.RouteActive || status == model.RoutePending
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE routes SET route_status=:route_status,enabled=:enabled,last_error=:last_error,revision=revision+1,updated_at=:updated_at WHERE id=:id AND route_status=:route_status_2`),
		status, enabled, message, now, routeID, current)
	if err != nil {
		return err
	}
	if err = requireAffected(result, false); err != nil {
		return err
	}
	stateAfter, err := gatewayDataPlaneStateTx(ctx, s, tx, poolID)
	if err != nil {
		return err
	}
	probeAfter, err := gatewayProbeStateTx(ctx, s, tx, poolID)
	if err != nil {
		return err
	}
	if !slices.Equal(stateBefore, stateAfter) {
		if _, err = bumpPoolRevisionTx(ctx, s, tx, poolID, now); err != nil {
			return err
		}
	}
	if !slices.Equal(probeBefore, probeAfter) {
		if _, err = bumpPoolProbeRevisionTx(ctx, s, tx, poolID, now); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	if activationError != "" {
		return fmt.Errorf("%w: %s", ErrConflict, activationError)
	}
	return nil
}

// CompleteApplicationDomainDetach records the network state after a successful
// managed redeploy. If another desired binding was queued while this operation
// ran, the network remains desired and this method intentionally becomes a
// no-op; the following attach operation will own its activation.
func (s *SQLStore) CompleteApplicationDomainDetach(ctx context.Context, applicationID, serviceID string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	for _, resource := range []string{applicationID} {
		if err = s.guardProjectResourceTx(ctx, tx, resource); err != nil {
			return err
		}
	}
	var poolID string
	var managed bool
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT rt.pool_id,sv.managed FROM services sv JOIN runtimes rt ON rt.id=sv.runtime_id WHERE sv.id=:id AND sv.application_id=:application_id`), serviceID, applicationID).Scan(&poolID, &managed); err != nil {
		return mapError(err)
	}
	if !managed {
		return fmt.Errorf("%w: domain detach completion requires a managed service", ErrConflict)
	}
	var desiredBindings int
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM routes WHERE service_id=:service_id AND route_status<>:route_status`), serviceID, model.RouteDisabled).Scan(&desiredBindings); err != nil {
		return err
	}
	if desiredBindings > 0 {
		if err = tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	}
	stateBefore, err := gatewayDataPlaneStateTx(ctx, s, tx, poolID)
	if err != nil {
		return err
	}
	probeBefore, err := gatewayProbeStateTx(ctx, s, tx, poolID)
	if err != nil {
		return err
	}
	issue := "service is not attached to the Gateway Pool network because it has no domain bindings"
	issues, _ := json.Marshal([]string{issue})
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE services SET dns_status=:dns_status,dns_last_error=:dns_last_error,routing_eligible=:routing_eligible,routing_issues=:routing_issues,revision=revision+1,updated_at=:updated_at WHERE id=:id`),
		model.DNSMissing, issue, false, string(issues), now, serviceID); err != nil {
		return err
	}
	if err = resolveServiceDNSConflictsTx(ctx, s, tx, poolID, now); err != nil {
		return err
	}
	stateAfter, err := gatewayDataPlaneStateTx(ctx, s, tx, poolID)
	if err != nil {
		return err
	}
	probeAfter, err := gatewayProbeStateTx(ctx, s, tx, poolID)
	if err != nil {
		return err
	}
	if !slices.Equal(stateBefore, stateAfter) {
		if _, err = bumpPoolRevisionTx(ctx, s, tx, poolID, now); err != nil {
			return err
		}
	}
	if !slices.Equal(probeBefore, probeAfter) {
		if _, err = bumpPoolProbeRevisionTx(ctx, s, tx, poolID, now); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
