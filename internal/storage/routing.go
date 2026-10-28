package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

const domainColumns = `id,pool_id,host,tls_mode,acme_challenge,dns_provider_ref,https_redirect,http_enabled,https_enabled,http3_enabled,status,certificate_id,source,enabled,last_error,revision,created_at,updated_at`

func scanDomain(row scanner) (model.Domain, error) {
	var value model.Domain
	var certificateID sql.NullString
	err := row.Scan(&value.ID, &value.PoolID, &value.Host, &value.TLSMode, &value.ACMEChallenge, &value.DNSProviderRef, &value.HTTPSRedirect,
		&value.HTTPEnabled, &value.HTTPSEnabled, &value.HTTP3Enabled,
		&value.Status, &certificateID, &value.Source, &value.Enabled, &value.LastError,
		&value.Revision, &value.CreatedAt, &value.UpdatedAt)
	if certificateID.Valid {
		value.CertificateID = certificateID.String
	}
	return value, mapError(err)
}

func domainArgs(value *model.Domain) []any {
	return []any{
		value.ID, value.PoolID, value.Host, value.TLSMode, value.ACMEChallenge, value.DNSProviderRef, value.HTTPSRedirect,
		value.HTTPEnabled, value.HTTPSEnabled, value.HTTP3Enabled,
		value.Status, nullString(value.CertificateID), value.Source, value.Enabled, value.LastError, value.Revision,
		value.CreatedAt, value.UpdatedAt,
	}
}

func (s *SQLStore) CreateDomain(ctx context.Context, value *model.Domain) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO domains (`+domainColumns+`) VALUES (`+columnPlaceholders(domainColumns)+`)`), domainArgs(value)...)
	if err != nil {
		return mapError(err)
	}
	if value.CertificateID != "" {
		var certificatePool string
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT pool_id FROM certificates WHERE id=:id`), value.CertificateID).Scan(&certificatePool); err != nil {
			return mapError(err)
		}
		if certificatePool != value.PoolID {
			return fmt.Errorf("%w: certificate and domain pools differ", ErrConflict)
		}
		if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO certificate_domains(certificate_id,domain_id) VALUES(:certificate_id,:domain_id)`), value.CertificateID, value.ID); err != nil {
			return mapError(err)
		}
	}
	if _, err = bumpPoolRevisionTx(ctx, s, tx, value.PoolID, value.UpdatedAt); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) CreateManagedDomain(ctx context.Context, domain *model.Domain, certificate *model.Certificate) error {
	if domain.TLSMode != model.TLSManaged || certificate.Provider != "acme" || domain.PoolID != certificate.PoolID {
		return fmt.Errorf("%w: invalid managed domain certificate", ErrConflict)
	}
	if domain.ACMEChallenge == "" {
		domain.ACMEChallenge = model.ACMEHTTP01
	}
	certificate.ACMEChallenge = domain.ACMEChallenge
	certificate.DNSProviderRef = domain.DNSProviderRef
	domain.CertificateID = certificate.ID
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO certificates (`+certificateColumns+`) VALUES (`+columnPlaceholders(certificateColumns)+`)`), certificateArgs(certificate)...)
	if err != nil {
		return mapError(err)
	}
	_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO domains (`+domainColumns+`) VALUES (`+columnPlaceholders(domainColumns)+`)`), domainArgs(domain)...)
	if err != nil {
		return mapError(err)
	}
	if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO certificate_domains(certificate_id,domain_id) VALUES(:certificate_id,:domain_id)`), certificate.ID, domain.ID); err != nil {
		return mapError(err)
	}
	revision, err := bumpPoolRevisionTx(ctx, s, tx, domain.PoolID, domain.UpdatedAt)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE certificates SET distribution_revision=:distribution_revision,revision=revision+1,updated_at=:updated_at WHERE id=:id`), revision, certificate.UpdatedAt, certificate.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	domain.CertificateID = certificate.ID
	certificate.DistributionRevision = revision
	certificate.Revision++
	return nil
}

func (s *SQLStore) ListDomains(ctx context.Context, page Page) ([]model.Domain, error) {
	page = page.normalized()
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+domainColumns+` FROM domains WHERE NOT EXISTS (SELECT 1 FROM control_documents tomb WHERE tomb.kind='deleted-project-guard' AND tomb.parent_id=domains.id) AND id>:id ORDER BY id LIMIT :limit`), page.After, page.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]model.Domain, 0, page.Limit)
	for rows.Next() {
		value, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) GetDomain(ctx context.Context, domainID string) (model.Domain, error) {
	return scanDomain(s.db.QueryRowContext(ctx, s.bind(`SELECT `+domainColumns+` FROM domains WHERE id=:id AND NOT EXISTS (SELECT 1 FROM control_documents tomb WHERE tomb.kind='deleted-project-guard' AND tomb.parent_id=domains.id)`), domainID))
}

func (s *SQLStore) UpdateDomain(ctx context.Context, value *model.Domain, expected int64) error {
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
	query := `SELECT certificate_id FROM domains WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	var previousCertificateID sql.NullString
	if err = tx.QueryRowContext(ctx, s.bind(query), value.ID).Scan(&previousCertificateID); err != nil {
		return mapError(err)
	}
	if value.CertificateID != "" {
		var certificatePool string
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT pool_id FROM certificates WHERE id=:id`), value.CertificateID).Scan(&certificatePool); err != nil {
			return mapError(err)
		}
		if certificatePool != value.PoolID {
			return fmt.Errorf("%w: certificate and domain pools differ", ErrConflict)
		}
	}
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE domains SET host=:host,tls_mode=:tls_mode,acme_challenge=:acme_challenge,dns_provider_ref=:dns_provider_ref,https_redirect=:https_redirect,http_enabled=:http_enabled,https_enabled=:https_enabled,http3_enabled=:http3_enabled,status=:status,certificate_id=:certificate_id,enabled=:enabled,
		last_error=:last_error,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`), value.Host, value.TLSMode, value.ACMEChallenge, value.DNSProviderRef,
		value.HTTPSRedirect, value.HTTPEnabled, value.HTTPSEnabled, value.HTTP3Enabled, value.Status, nullString(value.CertificateID), value.Enabled, value.LastError,
		value.UpdatedAt, value.ID, expected)
	if err != nil {
		return mapError(err)
	}
	if err := requireAffected(result, true); err != nil {
		return err
	}
	if previousCertificateID.String != value.CertificateID {
		if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM certificate_domains WHERE domain_id=:domain_id`), value.ID); err != nil {
			return err
		}
		if previousCertificateID.Valid {
			// A managed certificate is owned by its linked domains. Once its last
			// domain moves away, enqueue revocation instead of renewing an orphan.
			if _, err = tx.ExecContext(ctx, s.bind(`UPDATE certificates SET status=:status,next_attempt_at=:next_attempt_at,revision=revision+1,updated_at=:updated_at
				WHERE id=:id AND provider='acme'
				AND NOT EXISTS (SELECT 1 FROM certificate_domains WHERE certificate_id=:certificate_id)`),
				model.CertificateRevoked, value.UpdatedAt, value.UpdatedAt, previousCertificateID.String, previousCertificateID.String); err != nil {
				return err
			}
		}
		if value.CertificateID != "" {
			if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO certificate_domains(certificate_id,domain_id) VALUES(:certificate_id,:domain_id)`), value.CertificateID, value.ID); err != nil {
				return mapError(err)
			}
		}
	}
	if _, err = bumpPoolRevisionTx(ctx, s, tx, value.PoolID, value.UpdatedAt); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) DeleteDomain(ctx context.Context, domainID string, expected int64) error {
	domain, err := s.GetDomain(ctx, domainID)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	for _, resource := range []string{domainID} {
		if err = s.guardProjectResourceTx(ctx, tx, resource); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE domains SET enabled=:enabled,status=:status,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`), false, model.DomainDisabled, now, domainID, expected)
	if err != nil {
		return err
	}
	if err := requireAffected(result, true); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE routes SET enabled=:enabled,revision=revision+1,updated_at=:updated_at WHERE domain_id=:domain_id`), false, now, domainID); err != nil {
		return err
	}
	if _, err = bumpPoolRevisionTx(ctx, s, tx, domain.PoolID, now); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

const routeBaseColumns = `id,domain_id,service_id,external_key,path,priority,strip_path,target_port,upstream_protocol,upstream_server_name,websocket_enabled,enabled,route_status,source,last_error,revision,created_at,updated_at`
const routeColumns = routeBaseColumns + `,kind,match_kind,external_url,response_code,response_value,read_timeout_seconds,connect_timeout_seconds`

func scanRoute(row scanner) (model.Route, error) {
	var value model.Route
	var serviceID sql.NullString
	var kind, match, externalURL, responseValue sql.NullString
	var responseCode, readTimeout, connectTimeout sql.NullInt64
	err := row.Scan(&value.ID, &value.DomainID, &serviceID, &value.ExternalKey, &value.Path,
		&value.Priority, &value.StripPath, &value.TargetPort, &value.UpstreamProtocol,
		&value.UpstreamServerName, &value.WebsocketEnabled, &value.Enabled, &value.Status, &value.Source,
		&value.LastError, &value.Revision, &value.CreatedAt, &value.UpdatedAt,
		&kind, &match, &externalURL, &responseCode, &responseValue,
		&readTimeout, &connectTimeout)
	value.ServiceID = serviceID.String
	value.Kind, value.Match, value.ExternalURL, value.ResponseValue = kind.String, match.String, externalURL.String, responseValue.String
	value.ResponseCode, value.ReadTimeoutSeconds, value.ConnectTimeoutSeconds = int(responseCode.Int64), int(readTimeout.Int64), int(connectTimeout.Int64)
	if value.Kind == "" {
		value.Kind = "proxy"
	}
	if value.Match == "" {
		value.Match = "prefer"
	}
	return value, mapError(err)
}

func (s *SQLStore) CreateRoute(ctx context.Context, value *model.Route) error {
	var domainPool, servicePool string
	if err := s.db.QueryRowContext(ctx, s.bind(`SELECT d.pool_id,r.pool_id FROM domains d CROSS JOIN services sv JOIN runtimes r ON r.id=sv.runtime_id WHERE d.id=:id AND sv.id=:id_2`), value.DomainID, value.ServiceID).Scan(&domainPool, &servicePool); err != nil {
		return mapError(err)
	}
	if domainPool != servicePool {
		return ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	for _, resource := range []string{value.ServiceID, value.DomainID} {
		if err = s.guardProjectResourceTx(ctx, tx, resource); err != nil {
			return err
		}
	}
	if err = lockRouteDomainTx(ctx, s, tx, value.DomainID); err != nil {
		return err
	}
	stateBefore, err := gatewayDataPlaneStateTx(ctx, s, tx, domainPool)
	if err != nil {
		return err
	}
	if value.Enabled || value.Status == model.RoutePaused {
		if err = ensureRoutePrecedenceAvailableTx(ctx, s, tx, *value, ""); err != nil {
			return err
		}
	}
	if value.Status == "" {
		value.Status = model.RouteActive
	}
	_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO routes (`+routeColumns+`) VALUES (`+columnPlaceholders(routeColumns)+`)`),
		value.ID, value.DomainID, nullString(value.ServiceID), value.ExternalKey, value.Path, value.Priority,
		value.StripPath, value.TargetPort, value.UpstreamProtocol, value.UpstreamServerName,
		value.WebsocketEnabled, value.Enabled, value.Status, value.Source, value.LastError, value.Revision,
		value.CreatedAt, value.UpdatedAt, value.Kind, value.Match, value.ExternalURL,
		value.ResponseCode, value.ResponseValue, value.ReadTimeoutSeconds, value.ConnectTimeoutSeconds)
	if err != nil {
		return mapError(err)
	}
	stateAfter, err := gatewayDataPlaneStateTx(ctx, s, tx, domainPool)
	if err != nil {
		return err
	}
	if !slices.Equal(stateBefore, stateAfter) {
		if _, err = bumpPoolRevisionTx(ctx, s, tx, domainPool, value.UpdatedAt); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) ListRoutes(ctx context.Context, page Page) ([]model.Route, error) {
	page = page.normalized()
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+routeColumns+` FROM routes WHERE NOT EXISTS (SELECT 1 FROM control_documents tomb WHERE tomb.kind='deleted-project-guard' AND tomb.parent_id=routes.id) AND id>:id ORDER BY id LIMIT :limit`), page.After, page.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]model.Route, 0, page.Limit)
	for rows.Next() {
		value, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) GetRoute(ctx context.Context, routeID string) (model.Route, error) {
	return scanRoute(s.db.QueryRowContext(ctx, s.bind(`SELECT `+routeColumns+` FROM routes WHERE id=:id AND NOT EXISTS (SELECT 1 FROM control_documents tomb WHERE tomb.kind='deleted-project-guard' AND tomb.parent_id=routes.id)`), routeID))
}

func (s *SQLStore) UpdateRoute(ctx context.Context, value *model.Route, expected int64) error {
	current, err := s.GetRoute(ctx, value.ID)
	if err != nil {
		return err
	}
	if current.Revision != expected {
		return ErrPrecondition
	}
	oldDomain, err := s.GetDomain(ctx, current.DomainID)
	if err != nil {
		return err
	}
	newDomain, err := s.GetDomain(ctx, value.DomainID)
	if err != nil {
		return err
	}
	service, err := s.GetService(ctx, value.ServiceID)
	if err != nil {
		return err
	}
	runtime, err := s.GetRuntime(ctx, service.RuntimeID)
	if err != nil {
		return err
	}
	if newDomain.PoolID != runtime.PoolID {
		return ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	for _, resource := range []string{value.ID, value.ServiceID, value.DomainID} {
		if err = s.guardProjectResourceTx(ctx, tx, resource); err != nil {
			return err
		}
	}
	domainIDs := []string{current.DomainID, value.DomainID}
	slices.Sort(domainIDs)
	domainIDs = slices.Compact(domainIDs)
	for _, domainID := range domainIDs {
		if err = lockRouteDomainTx(ctx, s, tx, domainID); err != nil {
			return err
		}
	}
	poolIDs := []string{oldDomain.PoolID, newDomain.PoolID}
	slices.Sort(poolIDs)
	poolIDs = slices.Compact(poolIDs)
	statesBefore := make(map[string][]gatewayDataPlaneState, len(poolIDs))
	for _, poolID := range poolIDs {
		statesBefore[poolID], err = gatewayDataPlaneStateTx(ctx, s, tx, poolID)
		if err != nil {
			return err
		}
	}
	if value.Enabled || value.Status == model.RoutePaused {
		if err = ensureRoutePrecedenceAvailableTx(ctx, s, tx, *value, value.ID); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE routes SET domain_id=:domain_id,service_id=:service_id,path=:path,priority=:priority,strip_path=:strip_path,target_port=:target_port,
		upstream_protocol=:upstream_protocol,upstream_server_name=:upstream_server_name,websocket_enabled=:websocket_enabled,enabled=:enabled,route_status=:route_status,last_error=:last_error,revision=revision+1,updated_at=:updated_at,
		kind=:kind,match_kind=:match_kind,external_url=:external_url,response_code=:response_code,response_value=:response_value,read_timeout_seconds=:read_timeout_seconds,connect_timeout_seconds=:connect_timeout_seconds
		WHERE id=:id AND revision=:revision`), value.DomainID, nullString(value.ServiceID), value.Path, value.Priority, value.StripPath,
		value.TargetPort, value.UpstreamProtocol, value.UpstreamServerName, value.WebsocketEnabled,
		value.Enabled, value.Status, value.LastError, value.UpdatedAt, value.Kind, value.Match, value.ExternalURL,
		value.ResponseCode, value.ResponseValue, value.ReadTimeoutSeconds, value.ConnectTimeoutSeconds, value.ID, expected)
	if err != nil {
		return mapError(err)
	}
	if err := requireAffected(result, true); err != nil {
		return err
	}
	for _, poolID := range poolIDs {
		stateAfter, stateErr := gatewayDataPlaneStateTx(ctx, s, tx, poolID)
		if stateErr != nil {
			return stateErr
		}
		if slices.Equal(statesBefore[poolID], stateAfter) {
			continue
		}
		if _, err = bumpPoolRevisionTx(ctx, s, tx, poolID, value.UpdatedAt); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func lockRouteDomainTx(ctx context.Context, store *SQLStore, tx *sql.Tx, domainID string) error {
	query := `SELECT id FROM domains WHERE id=:id`
	if store.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	var id string
	if err := tx.QueryRowContext(ctx, store.bind(query), domainID).Scan(&id); err != nil {
		return mapError(err)
	}
	return nil
}

func ensureRoutePrecedenceAvailableTx(ctx context.Context, store *SQLStore, tx *sql.Tx, value model.Route, excludeID string) error {
	query := `SELECT id FROM routes WHERE domain_id=:domain_id AND path=:path AND priority=:priority AND source=:source AND ((enabled=:enabled) OR route_status=:route_status) AND id<>:id LIMIT 1`
	var conflictingID string
	err := tx.QueryRowContext(ctx, store.bind(query), value.DomainID, value.Path, value.Priority, value.Source, true, model.RoutePaused, excludeID).Scan(&conflictingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: route precedence conflicts with %s", ErrConflict, conflictingID)
}

func (s *SQLStore) DeleteRoute(ctx context.Context, routeID string, expected int64) error {
	route, err := s.GetRoute(ctx, routeID)
	if err != nil {
		return err
	}
	domain, err := s.GetDomain(ctx, route.DomainID)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	for _, resource := range []string{routeID} {
		if err = s.guardProjectResourceTx(ctx, tx, resource); err != nil {
			return err
		}
	}
	if err = lockRouteDomainTx(ctx, s, tx, route.DomainID); err != nil {
		return err
	}
	stateBefore, err := gatewayDataPlaneStateTx(ctx, s, tx, domain.PoolID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE routes SET enabled=:enabled,route_status=:route_status,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`), false, model.RouteDisabled, now, routeID, expected)
	if err != nil {
		return err
	}
	if err := requireAffected(result, true); err != nil {
		return err
	}
	stateAfter, err := gatewayDataPlaneStateTx(ctx, s, tx, domain.PoolID)
	if err != nil {
		return err
	}
	if !slices.Equal(stateBefore, stateAfter) {
		if _, err = bumpPoolRevisionTx(ctx, s, tx, domain.PoolID, now); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func bumpPoolRevisionTx(ctx context.Context, store *SQLStore, tx *sql.Tx, poolID string, now time.Time) (int64, error) {
	var revision int64
	query := `SELECT desired_revision FROM gateway_pools WHERE id=:id`
	if store.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	if err := tx.QueryRowContext(ctx, store.bind(query), poolID).Scan(&revision); err != nil {
		return 0, mapError(err)
	}
	revision++
	if _, err := tx.ExecContext(ctx, store.bind(`UPDATE gateway_pools SET desired_revision=:desired_revision,revision=revision+1,updated_at=:updated_at WHERE id=:id`), revision, now, poolID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, store.bind(`UPDATE gateways SET desired_revision=:desired_revision,revision=revision+1,updated_at=:updated_at WHERE pool_id=:pool_id`), revision, now, poolID); err != nil {
		return 0, err
	}
	if err := notifyPoolChangeTx(ctx, store, tx, poolID, revision); err != nil {
		return 0, err
	}
	return revision, nil
}

func bumpPoolProbeRevisionTx(ctx context.Context, store *SQLStore, tx *sql.Tx, poolID string, now time.Time) (int64, error) {
	var revision int64
	query := `SELECT probe_revision FROM gateway_pools WHERE id=:id`
	if store.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	if err := tx.QueryRowContext(ctx, store.bind(query), poolID).Scan(&revision); err != nil {
		return 0, mapError(err)
	}
	revision++
	if _, err := tx.ExecContext(ctx, store.bind(`UPDATE gateway_pools SET probe_revision=:probe_revision,revision=revision+1,updated_at=:updated_at WHERE id=:id`), revision, now, poolID); err != nil {
		return 0, err
	}
	if err := notifyPoolChangeTx(ctx, store, tx, poolID, revision); err != nil {
		return 0, err
	}
	return revision, nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
