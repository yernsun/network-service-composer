package storage

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

const certificateColumns = `id,pool_id,provider,acme_challenge,dns_provider_ref,status,primary_domain,certificate_pem,encrypted_private_key,
encryption_key_id,fingerprint,serial_number,not_before,not_after,attempt_count,next_attempt_at,last_error,
distribution_revision,revision,created_at,updated_at`

func scanCertificateValue(row scanner) (model.Certificate, error) {
	var value model.Certificate
	var encrypted []byte
	var notBefore, notAfter, next sql.NullTime
	err := row.Scan(&value.ID, &value.PoolID, &value.Provider, &value.ACMEChallenge, &value.DNSProviderRef, &value.Status, &value.PrimaryDomain,
		&value.CertificatePEM, &encrypted, &value.EncryptionKeyID, &value.Fingerprint, &value.SerialNumber,
		&notBefore, &notAfter, &value.AttemptCount, &next, &value.LastError, &value.DistributionRevision, &value.Revision,
		&value.CreatedAt, &value.UpdatedAt)
	if err != nil {
		return value, mapError(err)
	}
	value.EncryptedPrivateKey = encrypted
	if notBefore.Valid {
		value.NotBefore = &notBefore.Time
	}
	if notAfter.Valid {
		value.NotAfter = &notAfter.Time
	}
	if next.Valid {
		value.NextAttemptAt = &next.Time
	}
	if value.Provider == "custom" || value.Provider == "self-signed" {
		if block, _ := pem.Decode([]byte(value.CertificatePEM)); block != nil {
			if leaf, err := x509.ParseCertificate(block.Bytes); err == nil {
				value.Domains = append([]string(nil), leaf.DNSNames...)
			}
		}
	}
	return value, nil
}

func certificateArgs(value *model.Certificate) []any {
	return []any{
		value.ID, value.PoolID, value.Provider, value.ACMEChallenge, value.DNSProviderRef, value.Status, value.PrimaryDomain,
		value.CertificatePEM, value.EncryptedPrivateKey, value.EncryptionKeyID, value.Fingerprint, value.SerialNumber,
		ptrTime(value.NotBefore), ptrTime(value.NotAfter), value.AttemptCount, ptrTime(value.NextAttemptAt),
		value.LastError, value.DistributionRevision, value.Revision, value.CreatedAt, value.UpdatedAt,
	}
}

func (s *SQLStore) scanCertificate(ctx context.Context, row scanner) (model.Certificate, error) {
	value, err := scanCertificateValue(row)
	if err != nil {
		return value, err
	}
	if value.Provider == "custom" || value.Provider == "self-signed" {
		return value, nil
	}
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT d.host FROM certificate_domains cd JOIN domains d ON d.id=cd.domain_id WHERE cd.certificate_id=:certificate_id ORDER BY d.host`), value.ID)
	if err != nil {
		return value, err
	}
	defer rows.Close()
	for rows.Next() {
		var host string
		if err := rows.Scan(&host); err != nil {
			return value, err
		}
		value.Domains = append(value.Domains, host)
	}
	return value, rows.Err()
}

func (s *SQLStore) CreateCertificate(ctx context.Context, value *model.Certificate) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO certificates (`+certificateColumns+`) VALUES (`+columnPlaceholders(certificateColumns)+`)`), certificateArgs(value)...)
	if err != nil {
		return mapError(err)
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) ProvisionManagedCertificate(ctx context.Context, value *model.Certificate, domainID string, now time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if err = s.guardProjectResourceTx(ctx, tx, domainID); err != nil {
		return 0, err
	}
	query := `SELECT ` + domainColumns + ` FROM domains WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	domain, err := scanDomain(tx.QueryRowContext(ctx, s.bind(query), domainID))
	if err != nil {
		return 0, err
	}
	if domain.PoolID != value.PoolID || domain.TLSMode != model.TLSManaged {
		return 0, fmt.Errorf("%w: managed certificate domain is invalid", ErrConflict)
	}
	if domain.CertificateID != "" {
		var revision int64
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT desired_revision FROM gateway_pools WHERE id=:id`), domain.PoolID).Scan(&revision); err != nil {
			return 0, err
		}
		return revision, nil
	}
	if domain.ACMEChallenge == "" {
		domain.ACMEChallenge = model.ACMEHTTP01
	}
	value.ACMEChallenge = domain.ACMEChallenge
	value.DNSProviderRef = domain.DNSProviderRef
	_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO certificates (`+certificateColumns+`) VALUES (`+columnPlaceholders(certificateColumns)+`)`), certificateArgs(value)...)
	if err != nil {
		return 0, mapError(err)
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM certificate_domains WHERE domain_id=:domain_id`), domainID); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO certificate_domains(certificate_id,domain_id) VALUES(:certificate_id,:domain_id)`), value.ID, domainID); err != nil {
		return 0, mapError(err)
	}
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE domains SET certificate_id=:certificate_id,status=:status,last_error='',revision=revision+1,updated_at=:updated_at WHERE id=:id`),
		value.ID, model.DomainCertPending, now, domainID); err != nil {
		return 0, err
	}
	revision, err := bumpPoolRevisionTx(ctx, s, tx, value.PoolID, now)
	if err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE certificates SET distribution_revision=:distribution_revision,revision=revision+1,updated_at=:updated_at WHERE id=:id`), revision, now, value.ID); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	committed = true
	return revision, nil
}

// InstallCustomCertificate commits the certificate, its SAN relationships,
// and every affected domain transition as one gateway-state transaction.
func (s *SQLStore) InstallCustomCertificate(ctx context.Context, value *model.Certificate, domainIDs []string, now time.Time) error {
	ids := append([]string(nil), domainIDs...)
	sort.Strings(ids)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	domains := make([]model.Domain, 0, len(ids))
	for _, domainID := range ids {
		if err = s.guardProjectResourceTx(ctx, tx, domainID); err != nil {
			return err
		}
		query := `SELECT ` + domainColumns + ` FROM domains WHERE id=:id`
		if s.dialect != SQLite {
			query += ` FOR UPDATE`
		}
		domain, domainErr := scanDomain(tx.QueryRowContext(ctx, s.bind(query), domainID))
		if domainErr != nil {
			return domainErr
		}
		if domain.PoolID != value.PoolID {
			return fmt.Errorf("%w: certificate and domain pools differ", ErrConflict)
		}
		domains = append(domains, domain)
	}
	_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO certificates (`+certificateColumns+`) VALUES (`+columnPlaceholders(certificateColumns)+`)`), certificateArgs(value)...)
	if err != nil {
		return mapError(err)
	}
	for _, domain := range domains {
		if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM certificate_domains WHERE domain_id=:domain_id`), domain.ID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO certificate_domains(certificate_id,domain_id) VALUES(:certificate_id,:domain_id)`), value.ID, domain.ID); err != nil {
			return mapError(err)
		}
		redirect := domain.HTTPSRedirect
		if domain.TLSMode == model.TLSDisabled {
			redirect = true
		}
		if _, err = tx.ExecContext(ctx, s.bind(`UPDATE domains SET tls_mode=:tls_mode,acme_challenge='',dns_provider_ref='',https_redirect=:https_redirect,http_enabled=:http_enabled,https_enabled=:https_enabled,status=:status,certificate_id=:certificate_id,last_error='',revision=revision+1,updated_at=:updated_at WHERE id=:id`),
			model.TLSCustom, redirect, true, true, model.DomainReady, value.ID, now, domain.ID); err != nil {
			return err
		}
	}
	if _, err = bumpPoolRevisionTx(ctx, s, tx, value.PoolID, now); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) ListCertificates(ctx context.Context, page Page) ([]model.Certificate, error) {
	page = page.normalized()
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+certificateColumns+` FROM certificates WHERE id>:id ORDER BY id LIMIT :limit`), page.After, page.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []model.Certificate
	for rows.Next() {
		value, err := scanCertificateValue(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range values {
		if values[i].Provider == "custom" || values[i].Provider == "self-signed" {
			continue
		}
		domains, err := s.certificateDomainNames(ctx, values[i].ID)
		if err != nil {
			return nil, err
		}
		values[i].Domains = domains
	}
	return values, nil
}

func (s *SQLStore) certificateDomainNames(ctx context.Context, certificateID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT d.host FROM certificate_domains cd JOIN domains d ON d.id=cd.domain_id WHERE cd.certificate_id=:certificate_id ORDER BY d.host`), certificateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) GetCertificate(ctx context.Context, certificateID string) (model.Certificate, error) {
	return s.scanCertificate(ctx, s.db.QueryRowContext(ctx, s.bind(`SELECT `+certificateColumns+` FROM certificates WHERE id=:id`), certificateID))
}

func (s *SQLStore) UpdateCertificate(ctx context.Context, value *model.Certificate, expected int64) error {
	current, err := s.GetCertificate(ctx, value.ID)
	if err != nil {
		return err
	}
	effectiveChanged := certificateIsDistributed(current.Status) != certificateIsDistributed(value.Status)
	if certificateIsDistributed(current.Status) && certificateIsDistributed(value.Status) {
		effectiveChanged = current.CertificatePEM != value.CertificatePEM || string(current.EncryptedPrivateKey) != string(value.EncryptedPrivateKey)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE certificates SET provider=:provider,status=:status,primary_domain=:primary_domain,certificate_pem=:certificate_pem,encrypted_private_key=:encrypted_private_key,
        encryption_key_id=:encryption_key_id,fingerprint=:fingerprint,serial_number=:serial_number,not_before=:not_before,not_after=:not_after,attempt_count=:attempt_count,next_attempt_at=:next_attempt_at,last_error=:last_error,
		distribution_revision=:distribution_revision,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`), value.Provider, value.Status, value.PrimaryDomain,
		value.CertificatePEM, value.EncryptedPrivateKey, value.EncryptionKeyID, value.Fingerprint,
		value.SerialNumber, ptrTime(value.NotBefore), ptrTime(value.NotAfter), value.AttemptCount,
		ptrTime(value.NextAttemptAt), value.LastError, value.DistributionRevision, value.UpdatedAt, value.ID, expected)
	if err != nil {
		return mapError(err)
	}
	if err := requireAffected(result, true); err != nil {
		return err
	}
	if effectiveChanged {
		if _, err = bumpPoolRevisionTx(ctx, s, tx, value.PoolID, value.UpdatedAt); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// ActivateCertificate atomically publishes newly issued key material and marks
// every linked domain ready. The pool revision is advanced in the same
// transaction, so a gateway can never observe an active certificate while the
// domain still reports an earlier provisioning state.
func (s *SQLStore) ActivateCertificate(ctx context.Context, value *model.Certificate, expected int64, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	query := `SELECT pool_id,status,certificate_pem,encrypted_private_key,revision FROM certificates WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	var poolID string
	var previousStatus model.CertificateStatus
	var previousPEM string
	var previousEncrypted []byte
	var currentRevision int64
	if err = tx.QueryRowContext(ctx, s.bind(query), value.ID).Scan(&poolID, &previousStatus, &previousPEM, &previousEncrypted, &currentRevision); err != nil {
		return mapError(err)
	}
	if currentRevision != expected {
		return ErrPrecondition
	}
	if poolID != value.PoolID || value.Status != model.CertificateActive {
		return fmt.Errorf("%w: invalid certificate activation", ErrConflict)
	}
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE certificates SET provider=:provider,status=:status,primary_domain=:primary_domain,certificate_pem=:certificate_pem,encrypted_private_key=:encrypted_private_key,
        encryption_key_id=:encryption_key_id,fingerprint=:fingerprint,serial_number=:serial_number,not_before=:not_before,not_after=:not_after,attempt_count=:attempt_count,next_attempt_at=:next_attempt_at,last_error=:last_error,
		distribution_revision=:distribution_revision,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`), value.Provider, value.Status,
		value.PrimaryDomain, value.CertificatePEM, value.EncryptedPrivateKey, value.EncryptionKeyID, value.Fingerprint,
		value.SerialNumber, ptrTime(value.NotBefore), ptrTime(value.NotAfter), value.AttemptCount,
		ptrTime(value.NextAttemptAt), value.LastError, value.DistributionRevision, now, value.ID, expected)
	if err != nil {
		return mapError(err)
	}
	if err = requireAffected(result, true); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE domains SET status=:status,last_error='',revision=revision+1,updated_at=:updated_at
        WHERE id IN (SELECT domain_id FROM certificate_domains WHERE certificate_id=:certificate_id)`), model.DomainReady, now, value.ID); err != nil {
		return err
	}
	effectiveChanged := !certificateIsDistributed(previousStatus) || previousPEM != value.CertificatePEM || string(previousEncrypted) != string(value.EncryptedPrivateKey)
	if effectiveChanged {
		if _, err = bumpPoolRevisionTx(ctx, s, tx, value.PoolID, now); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func certificateIsDistributed(status model.CertificateStatus) bool {
	return status == model.CertificateActive || status == model.CertificateRenewing
}

func (s *SQLStore) SetCertificateDistributionRevision(ctx context.Context, certificateID string, revision int64) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE certificates SET distribution_revision=:distribution_revision,revision=revision+1,updated_at=:updated_at WHERE id=:id`), revision, time.Now().UTC(), certificateID)
	if err != nil {
		return err
	}
	return requireAffected(result, false)
}

func (s *SQLStore) ListPendingCertificates(ctx context.Context, now time.Time, limit int) ([]model.Certificate, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+certificateColumns+` FROM certificates
        WHERE ((status IN (:pending_status,:error_status,:renewing_status,:authorizing_status,:validating_status) AND (next_attempt_at IS NULL OR next_attempt_at<=:next_attempt_at))
           OR (status IN (:active_status,:revoked_status) AND next_attempt_at IS NOT NULL AND next_attempt_at<=:next_attempt_at_2))
        AND NOT EXISTS (SELECT 1 FROM control_documents h WHERE h.id='recovery:barrier' OR h.id=`+s.concat("'recovery-hold:certificate:'", "certificates.id")+`)
        ORDER BY updated_at LIMIT :limit`),
		model.CertificatePending, model.CertificateError, model.CertificateRenewing, model.CertificateAuthorizing, model.CertificateValidating, now,
		model.CertificateActive, model.CertificateRevoked, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []model.Certificate
	for rows.Next() {
		value, err := scanCertificateValue(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) LinkCertificateDomains(ctx context.Context, certificateID string, domainIDs []string) error {
	certificate, err := s.GetCertificate(ctx, certificateID)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	for _, domainID := range domainIDs {
		if err = s.guardProjectResourceTx(ctx, tx, domainID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM certificate_domains WHERE certificate_id=:certificate_id`), certificateID); err != nil {
		return err
	}
	for _, domainID := range domainIDs {
		var poolID string
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT pool_id FROM domains WHERE id=:id`), domainID).Scan(&poolID); err != nil {
			return mapError(err)
		}
		if poolID != certificate.PoolID {
			return fmt.Errorf("%w: certificate and domain pools differ", ErrConflict)
		}
		if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO certificate_domains(certificate_id,domain_id) VALUES(:certificate_id,:domain_id)`), certificateID, domainID); err != nil {
			return mapError(err)
		}
		if _, err = tx.ExecContext(ctx, s.bind(`UPDATE domains SET certificate_id=:certificate_id,revision=revision+1,updated_at=:updated_at WHERE id=:id`), certificateID, time.Now().UTC(), domainID); err != nil {
			return err
		}
	}
	if _, err = bumpPoolRevisionTx(ctx, s, tx, certificate.PoolID, time.Now().UTC()); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) UpdateCertificateDomains(ctx context.Context, certificateID string, status model.DomainStatus, lastError string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, s.bind(`UPDATE domains SET status=:status,last_error=:last_error,revision=revision+1,updated_at=:updated_at
        WHERE id IN (SELECT domain_id FROM certificate_domains WHERE certificate_id=:certificate_id)`), status, lastError, now, certificateID)
	return err
}

const gatewayColumns = `id,pool_id,name,address,certificate_hash,enabled,status,desired_revision,applied_revision,applied_probe_revision,
agent_version,protocol_version,renderer_version,http3_supported,http3_advertised_port,model_digest,rendered_digest,last_error,last_seen_at,revision,created_at,updated_at`

func scanGateway(row scanner) (model.Gateway, error) {
	var value model.Gateway
	var seen sql.NullTime
	err := row.Scan(&value.ID, &value.PoolID, &value.Name, &value.Address, &value.CertificateHash,
		&value.Enabled, &value.Status, &value.DesiredRevision, &value.AppliedRevision,
		&value.AppliedProbeRevision,
		&value.AgentVersion, &value.ProtocolVersion, &value.RendererVersion, &value.HTTP3Supported, &value.HTTP3AdvertisedPort, &value.ModelDigest, &value.RenderedDigest,
		&value.LastError, &seen, &value.Revision, &value.CreatedAt, &value.UpdatedAt)
	if seen.Valid {
		value.LastSeenAt = &seen.Time
	}
	return value, mapError(err)
}

func (s *SQLStore) UpsertGateway(ctx context.Context, value *model.Gateway) error {
	existing, err := s.GetGateway(ctx, value.ID)
	if errors.Is(err, ErrNotFound) {
		tx, e := s.db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		query := `SELECT id FROM gateway_pools WHERE id=:id`
		if s.dialect != SQLite {
			query += ` FOR UPDATE`
		}
		var pool string
		if e = tx.QueryRowContext(ctx, s.bind(query), value.PoolID).Scan(&pool); e != nil {
			return mapError(e)
		}
		var guard string
		e = tx.QueryRowContext(ctx, s.bind(`SELECT id FROM control_documents WHERE id=:id AND kind='project-ingress-guard'`), "project-ingress-guard:"+pool).Scan(&guard)
		if e == nil {
			return ErrConflict
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO gateways (`+gatewayColumns+`) VALUES (`+columnPlaceholders(gatewayColumns)+`)`),
			value.ID, value.PoolID, value.Name, value.Address, value.CertificateHash, value.Enabled,
			value.Status, value.DesiredRevision, value.AppliedRevision, value.AppliedProbeRevision, value.AgentVersion, value.ProtocolVersion,
			value.RendererVersion, value.HTTP3Supported, value.HTTP3AdvertisedPort, value.ModelDigest, value.RenderedDigest, value.LastError,
			ptrTime(value.LastSeenAt), value.Revision, value.CreatedAt, value.UpdatedAt)
		if err != nil {
			return mapError(err)
		}
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if !existing.Enabled {
		return fmt.Errorf("%w: gateway is disabled or revoked", ErrConflict)
	}
	if existing.PoolID != value.PoolID || (existing.CertificateHash != "" && existing.CertificateHash != value.CertificateHash) {
		return fmt.Errorf("%w: gateway identity does not match enrollment", ErrConflict)
	}
	_, err = s.db.ExecContext(ctx, s.bind(`UPDATE gateways SET name=:name,address=:address,certificate_hash=:certificate_hash,status=:status,desired_revision=:desired_revision,
		applied_revision=CASE WHEN applied_revision<:applied_revision THEN :new_applied_revision ELSE applied_revision END,agent_version=:agent_version,protocol_version=:protocol_version,renderer_version=:renderer_version,http3_supported=:http3_supported,http3_advertised_port=:http3_advertised_port,model_digest=:model_digest,rendered_digest=:rendered_digest,last_error=:last_error,last_seen_at=:last_seen_at,
		applied_probe_revision=CASE WHEN applied_probe_revision<:applied_probe_revision THEN :new_applied_probe_revision ELSE applied_probe_revision END,
		revision=revision+1,updated_at=:updated_at WHERE id=:id AND enabled=:enabled`), value.Name, value.Address,
		value.CertificateHash, value.Status, value.DesiredRevision, value.AppliedRevision, value.AppliedRevision,
		value.AgentVersion, value.ProtocolVersion, value.RendererVersion, value.HTTP3Supported, value.HTTP3AdvertisedPort, value.ModelDigest, value.RenderedDigest,
		value.LastError, ptrTime(value.LastSeenAt), value.AppliedProbeRevision, value.AppliedProbeRevision,
		value.UpdatedAt, value.ID, true)
	return err
}

func (s *SQLStore) ListGateways(ctx context.Context, page Page) ([]model.Gateway, error) {
	page = page.normalized()
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+gatewayColumns+` FROM gateways WHERE id>:id ORDER BY id LIMIT :limit`), page.After, page.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []model.Gateway
	for rows.Next() {
		value, err := scanGateway(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) GetGateway(ctx context.Context, gatewayID string) (model.Gateway, error) {
	return scanGateway(s.db.QueryRowContext(ctx, s.bind(`SELECT `+gatewayColumns+` FROM gateways WHERE id=:id`), gatewayID))
}

func (s *SQLStore) UpdateGateway(ctx context.Context, value *model.Gateway, expected int64) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE gateways SET name=:name,enabled=:enabled,status=:status,last_error=:last_error,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`),
		value.Name, value.Enabled, value.Status, value.LastError, value.UpdatedAt, value.ID, expected)
	if err != nil {
		return mapError(err)
	}
	return requireAffected(result, true)
}

func (s *SQLStore) HeartbeatGateway(ctx context.Context, gatewayID string, desiredRevision, appliedProbeRevision int64, at time.Time) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE gateways SET status=CASE WHEN status=:failed_status THEN status WHEN applied_revision>=:applied_revision THEN :ready_status ELSE :stale_status END,
		desired_revision=:desired_revision,applied_probe_revision=CASE WHEN applied_probe_revision<:applied_probe_revision THEN :new_applied_probe_revision ELSE applied_probe_revision END,
		last_seen_at=:last_seen_at,revision=revision+1,updated_at=:updated_at WHERE id=:id AND enabled=:enabled`),
		model.GatewayApplyFailed, desiredRevision, model.GatewayReady, model.GatewayStale, desiredRevision,
		appliedProbeRevision, appliedProbeRevision, at, at, gatewayID, true)
	if err != nil {
		return err
	}
	return requireAffected(result, false)
}

func (s *SQLStore) AckGateway(ctx context.Context, gatewayID string, revision int64, modelSHA256, rendererVersion, renderedSHA256, applyError string) error {
	status := model.GatewayReady
	if applyError != "" {
		status = model.GatewayApplyFailed
		// A rejected candidate is not evidence of a newly loaded version.
		// Preserve the digests and renderer associated with the confirmed revision.
		modelSHA256, rendererVersion, renderedSHA256 = "", "", ""
	}
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE gateways SET applied_revision=CASE WHEN applied_revision<:applied_revision AND :apply_error_guard='' THEN :new_applied_revision ELSE applied_revision END,
		model_digest=CASE WHEN :model_digest_guard='' THEN model_digest ELSE :new_model_digest END,renderer_version=CASE WHEN :renderer_version_guard='' THEN renderer_version ELSE :new_renderer_version END,
		rendered_digest=CASE WHEN :rendered_digest_guard='' THEN rendered_digest ELSE :new_rendered_digest END,status=:status,last_error=:last_error,revision=revision+1,updated_at=:updated_at WHERE id=:id AND enabled=:enabled`),
		revision, applyError, revision, modelSHA256, modelSHA256, rendererVersion, rendererVersion, renderedSHA256, renderedSHA256,
		status, applyError, time.Now().UTC(), gatewayID, true)
	if err != nil {
		return err
	}
	return requireAffected(result, false)
}

func (s *SQLStore) SelectProbeLeader(ctx context.Context, poolID string, offlineAfter time.Duration) (model.GatewayPool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.GatewayPool{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	query := `SELECT ` + poolColumns + ` FROM gateway_pools WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	pool, err := scanPool(tx.QueryRowContext(ctx, s.bind(query), poolID))
	if err != nil {
		return pool, err
	}
	cutoff := time.Now().UTC().Add(-offlineAfter)
	var selected string
	if pool.Enabled {
		err = tx.QueryRowContext(ctx, s.bind(`SELECT id FROM gateways WHERE pool_id=:pool_id AND enabled=:enabled AND last_seen_at>=:last_seen_at ORDER BY id LIMIT 1`), poolID, true, cutoff).Scan(&selected)
		if errors.Is(err, sql.ErrNoRows) {
			selected = ""
		} else if err != nil {
			return pool, err
		}
	}
	if pool.ProbeGatewayID == selected {
		if err = tx.Commit(); err != nil {
			return pool, err
		}
		committed = true
		return pool, nil
	}
	pool.ProbeGatewayID = selected
	pool.ProbeEpoch++
	pool.UpdatedAt = time.Now().UTC()
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE gateway_pools SET probe_gateway_id=:probe_gateway_id,probe_epoch=:probe_epoch,revision=revision+1,updated_at=:updated_at WHERE id=:id`),
		pool.ProbeGatewayID, pool.ProbeEpoch, pool.UpdatedAt, pool.ID); err != nil {
		return pool, err
	}
	// Probe leadership is fenced by epoch. Reset only the Service-DNS probe
	// counters: task/container instance health is discovery state and must never
	// be withdrawn merely because the probing Gateway changed.
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE service_dns_probes SET success_count=0,failure_count=0,last_sequence=0
		WHERE service_id IN (SELECT sv.id FROM services sv JOIN runtimes r ON r.id=sv.runtime_id WHERE r.pool_id=:pool_id)`), poolID); err != nil {
		return pool, err
	}
	if err = tx.Commit(); err != nil {
		return pool, err
	}
	committed = true
	return pool, nil
}

func (s *SQLStore) GatewayACKed(ctx context.Context, poolID string, revision int64, cutoff time.Time) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM gateways WHERE pool_id=:pool_id AND enabled=:enabled AND status=:status AND applied_revision>=:applied_revision AND last_seen_at>=:last_seen_at`),
		poolID, true, model.GatewayReady, revision, cutoff).Scan(&count)
	return count > 0, err
}

func (s *SQLStore) MarkGatewaysOffline(ctx context.Context, cutoff, now time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE gateways SET status=:status,revision=revision+1,updated_at=:updated_at
		WHERE enabled=:enabled AND last_seen_at IS NOT NULL AND last_seen_at<:last_seen_at AND status<>:status_2 AND status<>:status_3`),
		model.GatewayOffline, now, true, cutoff, model.GatewayOffline, model.GatewayIncompatible)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *SQLStore) PutChallenge(ctx context.Context, challenge Challenge) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO acme_challenges(id,certificate_id,domain,challenge_type,token,key_authorization,status,expires_at,created_at,updated_at)
        VALUES(:id,:certificate_id,:domain,:challenge_type,:token,:key_authorization,:status,:expires_at,:created_at,:updated_at)`), challenge.ID, challenge.CertificateID, challenge.Domain, challenge.Type,
		challenge.Token, challenge.KeyAuthorization, challenge.Status, challenge.ExpiresAt, challenge.CreatedAt, challenge.UpdatedAt)
	return mapError(err)
}

func (s *SQLStore) GetChallenge(ctx context.Context, token string, now time.Time) (Challenge, error) {
	var value Challenge
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT id,certificate_id,domain,challenge_type,token,key_authorization,status,expires_at,created_at,updated_at
        FROM acme_challenges WHERE token=:token AND expires_at>:expires_at`), token, now).Scan(&value.ID, &value.CertificateID,
		&value.Domain, &value.Type, &value.Token, &value.KeyAuthorization, &value.Status, &value.ExpiresAt,
		&value.CreatedAt, &value.UpdatedAt)
	return value, mapError(err)
}

func (s *SQLStore) DeleteChallenges(ctx context.Context, certificateID string) error {
	_, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM acme_challenges WHERE certificate_id=:certificate_id`), certificateID)
	return err
}

func (s *SQLStore) DeleteChallengeToken(ctx context.Context, certificateID, token string) error {
	_, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM acme_challenges WHERE certificate_id=:certificate_id AND token=:token`), certificateID, token)
	return err
}

func (s *SQLStore) ReencryptCertificates(ctx context.Context, transform func(model.Certificate) ([]byte, string, error)) (int, error) {
	count := 0
	after := ""
	for {
		values, err := s.ListCertificates(ctx, Page{After: after, Limit: 200})
		if err != nil {
			return count, err
		}
		for _, value := range values {
			if len(value.EncryptedPrivateKey) == 0 {
				continue
			}
			encrypted, keyID, err := transform(value)
			if err != nil {
				return count, err
			}
			if keyID == value.EncryptionKeyID {
				continue
			}
			value.EncryptedPrivateKey = encrypted
			value.EncryptionKeyID = keyID
			value.UpdatedAt = time.Now().UTC()
			result, err := s.db.ExecContext(ctx, s.bind(`UPDATE certificates SET encrypted_private_key=:encrypted_private_key,encryption_key_id=:encryption_key_id,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`),
				value.EncryptedPrivateKey, value.EncryptionKeyID, value.UpdatedAt, value.ID, value.Revision)
			if err != nil {
				return count, err
			}
			if err := requireAffected(result, true); err != nil {
				return count, err
			}
			count++
		}
		if len(values) < 200 {
			break
		}
		after = values[len(values)-1].ID
	}
	return count, nil
}

// GarbageCollect removes transient ACME state and unreferenced terminal
// certificates after their configured retention window. Referenced
// certificates are deliberately retained regardless of status for diagnosis.
func (s *SQLStore) GarbageCollect(ctx context.Context, now time.Time, failedRetention time.Duration) (GCResult, error) {
	if failedRetention <= 0 {
		failedRetention = 30 * 24 * time.Hour
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GCResult{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	challenges, err := tx.ExecContext(ctx, s.bind(`DELETE FROM acme_challenges WHERE expires_at<=:expires_at
		AND NOT EXISTS (SELECT 1 FROM control_documents h WHERE h.id='recovery:barrier' OR h.id=`+s.concat("'recovery-hold:certificate:'", "acme_challenges.certificate_id")+`)`), now)
	if err != nil {
		return GCResult{}, err
	}
	certificates, err := tx.ExecContext(ctx, s.bind(`DELETE FROM certificates
		WHERE status IN (:error_status,:expired_status,:revoked_status) AND updated_at<:updated_at
		AND NOT EXISTS (SELECT 1 FROM control_documents h WHERE h.id='recovery:barrier' OR h.id=`+s.concat("'recovery-hold:certificate:'", "certificates.id")+`)
		AND NOT EXISTS (SELECT 1 FROM certificate_domains cd WHERE cd.certificate_id=certificates.id)
		AND NOT EXISTS (SELECT 1 FROM domains d WHERE d.certificate_id=certificates.id)`),
		model.CertificateError, model.CertificateExpired, model.CertificateRevoked, now.Add(-failedRetention))
	if err != nil {
		return GCResult{}, err
	}
	result := GCResult{}
	result.ExpiredChallenges, err = challenges.RowsAffected()
	if err != nil {
		return GCResult{}, err
	}
	result.Certificates, err = certificates.RowsAffected()
	if err != nil {
		return GCResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return GCResult{}, err
	}
	committed = true
	return result, nil
}
