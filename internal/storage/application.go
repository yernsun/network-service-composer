package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

const catalogSourceColumns = `id,name,source_type,url,pinned_ref,trusted,enabled,last_synced_at,last_error,revision,created_at,updated_at,approved_endpoint,market_json`

func scanCatalogSource(row scanner) (model.CatalogSource, error) {
	var value model.CatalogSource
	var synced sql.NullTime
	err := row.Scan(&value.ID, &value.Name, &value.Type, &value.URL, &value.PinnedRef, &value.Trusted, &value.Enabled,
		&synced, &value.LastError, &value.Revision, &value.CreatedAt, &value.UpdatedAt, &value.ApprovedEndpoint, &value.MarketJSON)
	if synced.Valid {
		value.LastSyncedAt = &synced.Time
	}
	return value, mapError(err)
}

func (s *SQLStore) CreateCatalogSource(ctx context.Context, value *model.CatalogSource) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO catalog_sources (`+catalogSourceColumns+`) VALUES (`+columnPlaceholders(catalogSourceColumns)+`)`),
		value.ID, value.Name, value.Type, value.URL, value.PinnedRef, value.Trusted, value.Enabled, ptrTime(value.LastSyncedAt),
		value.LastError, value.Revision, value.CreatedAt, value.UpdatedAt, value.ApprovedEndpoint, jsonObject(value.MarketJSON))
	return mapError(err)
}

func (s *SQLStore) ListCatalogSources(ctx context.Context, page Page) ([]model.CatalogSource, error) {
	page = page.normalized()
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+catalogSourceColumns+` FROM catalog_sources WHERE id>:id ORDER BY id LIMIT :limit`), page.After, page.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]model.CatalogSource, 0, page.Limit)
	for rows.Next() {
		value, scanErr := scanCatalogSource(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) GetCatalogSource(ctx context.Context, sourceID string) (model.CatalogSource, error) {
	return scanCatalogSource(s.db.QueryRowContext(ctx, s.bind(`SELECT `+catalogSourceColumns+` FROM catalog_sources WHERE id=:id`), sourceID))
}

func (s *SQLStore) UpdateCatalogSource(ctx context.Context, value *model.CatalogSource, expected int64) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE catalog_sources SET name=:name,url=:url,pinned_ref=:pinned_ref,trusted=:trusted,enabled=:enabled,last_synced_at=:last_synced_at,last_error=:last_error,approved_endpoint=:approved_endpoint,market_json=:market_json,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`),
		value.Name, value.URL, value.PinnedRef, value.Trusted, value.Enabled, ptrTime(value.LastSyncedAt), value.LastError, value.ApprovedEndpoint, jsonObject(value.MarketJSON),
		value.UpdatedAt, value.ID, expected)
	if err != nil {
		return mapError(err)
	}
	return requireAffected(result, true)
}

const catalogVersionColumns = `id,source_id,app_key,name,description,version,commit_ref,artifact_digest,compose_sha256,
manifest_json,compose_yaml,env_example,readme,icon_url,services_json,compose_compatible,swarm_compatible,
compatibility_issues,revision,created_at,updated_at,market_json,withdrawn,swarm_yaml,swarm_sha256`

func scanCatalogVersion(row scanner) (model.CatalogAppVersion, error) {
	var value model.CatalogAppVersion
	var servicesJSON, issuesJSON string
	err := row.Scan(&value.ID, &value.SourceID, &value.AppKey, &value.Name, &value.Description, &value.Version,
		&value.Commit, &value.ArtifactDigest, &value.ComposeSHA256, &value.ManifestJSON, &value.ComposeYAML,
		&value.EnvExample, &value.README, &value.IconURL, &servicesJSON, &value.ComposeCompatible,
		&value.SwarmCompatible, &issuesJSON, &value.Revision, &value.CreatedAt, &value.UpdatedAt, &value.MarketJSON, &value.Withdrawn, &value.SwarmYAML, &value.SwarmSHA256)
	if err == nil {
		err = json.Unmarshal([]byte(servicesJSON), &value.Services)
	}
	if err == nil {
		err = json.Unmarshal([]byte(issuesJSON), &value.CompatibilityIssues)
	}
	return value, mapError(err)
}

func catalogVersionArgs(value *model.CatalogAppVersion) []any {
	servicesJSON, _ := json.Marshal(value.Services)
	issuesJSON, _ := json.Marshal(value.CompatibilityIssues)
	return []any{value.ID, value.SourceID, value.AppKey, value.Name, value.Description, value.Version, value.Commit,
		value.ArtifactDigest, value.ComposeSHA256, value.ManifestJSON, value.ComposeYAML, value.EnvExample, value.README,
		value.IconURL, string(servicesJSON), value.ComposeCompatible, value.SwarmCompatible, string(issuesJSON), value.Revision,
		value.CreatedAt, value.UpdatedAt, jsonObject(value.MarketJSON), value.Withdrawn, value.SwarmYAML, value.SwarmSHA256}
}

func (s *SQLStore) UpsertCatalogAppVersion(ctx context.Context, value *model.CatalogAppVersion) error {
	var existingID string
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT id FROM catalog_app_versions WHERE source_id=:source_id AND app_key=:app_key AND version=:version`),
		value.SourceID, value.AppKey, value.Version).Scan(&existingID)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.ExecContext(ctx, s.bind(`INSERT INTO catalog_app_versions (`+catalogVersionColumns+`) VALUES (`+columnPlaceholders(catalogVersionColumns)+`)`), catalogVersionArgs(value)...)
		insertErr := mapError(err)
		if !errors.Is(insertErr, ErrConflict) {
			return insertErr
		}
		// Another initializer may have committed this immutable version after
		// our read. Compare its complete snapshot before accepting the race.
		err = s.db.QueryRowContext(ctx, s.bind(`SELECT id FROM catalog_app_versions WHERE source_id=:source_id AND app_key=:app_key AND version=:version`), value.SourceID, value.AppKey, value.Version).Scan(&existingID)
		if err != nil {
			return insertErr
		}
	}
	if err != nil {
		return err
	}
	existing, err := s.GetCatalogAppVersion(ctx, existingID)
	if err != nil {
		return err
	}
	// One-time documentation enrichment for pre-manifest built-ins leaves install artifacts untouched.
	enrich := jsonObject(existing.MarketJSON) == "{}" && jsonObject(value.MarketJSON) != "{}"
	if enrich {
		source, e := s.GetCatalogSource(ctx, value.SourceID)
		if e != nil {
			return e
		}
		enrich = source.Type == model.CatalogBuiltin
	}
	if enrich {
		existing.MarketJSON = value.MarketJSON
	}
	if !sameCatalogVersion(existing, *value) {
		return fmt.Errorf("%w: catalog version %s/%s/%s is immutable", ErrConflict, value.SourceID, value.AppKey, value.Version)
	}
	if enrich {
		if _, err = s.db.ExecContext(ctx, s.bind(`UPDATE catalog_app_versions SET market_json=:market_json WHERE id=:id AND (market_json='{}' OR market_json='')`), value.MarketJSON, existing.ID); err != nil {
			return err
		}
	}
	value.ID = existing.ID
	value.Metadata = existing.Metadata
	return nil
}

// SyncCatalogAppVersions publishes one verified index atomically. An invalid or
// immutable-conflicting item must never leave a source partially synchronized.
func (s *SQLStore) SyncCatalogAppVersions(ctx context.Context, source *model.CatalogSource, versions []model.CatalogAppVersion, expected int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	query := `SELECT revision FROM catalog_sources WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	var revision int64
	if err = tx.QueryRowContext(ctx, s.bind(query), source.ID).Scan(&revision); err != nil {
		return mapError(err)
	}
	if revision != expected {
		return ErrPrecondition
	}
	if source.Type == model.CatalogManifest {
		if _, err = tx.ExecContext(ctx, s.bind(`UPDATE catalog_app_versions SET withdrawn=TRUE WHERE source_id=:source_id`), source.ID); err != nil {
			return err
		}
	}
	for index := range versions {
		value := &versions[index]
		if value.SourceID != source.ID {
			return fmt.Errorf("%w: catalog version belongs to another source", ErrConflict)
		}
		var existingID string
		err = tx.QueryRowContext(ctx, s.bind(`SELECT id FROM catalog_app_versions WHERE source_id=:source_id AND app_key=:app_key AND version=:version`),
			value.SourceID, value.AppKey, value.Version).Scan(&existingID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO catalog_app_versions (`+catalogVersionColumns+`) VALUES (`+columnPlaceholders(catalogVersionColumns)+`)`), catalogVersionArgs(value)...); err != nil {
				return mapError(err)
			}
		case err != nil:
			return err
		default:
			existing, scanErr := scanCatalogVersion(tx.QueryRowContext(ctx, s.bind(`SELECT `+catalogVersionColumns+` FROM catalog_app_versions WHERE id=:id`), existingID))
			if scanErr != nil {
				return scanErr
			}
			if !sameCatalogVersion(existing, *value) {
				return fmt.Errorf("%w: catalog version %s/%s/%s is immutable", ErrConflict, value.SourceID, value.AppKey, value.Version)
			}
		}
		if source.Type == model.CatalogManifest {
			if _, err = tx.ExecContext(ctx, s.bind(`UPDATE catalog_app_versions SET withdrawn=FALSE WHERE source_id=:source_id AND app_key=:app_key AND version=:version`), source.ID, value.AppKey, value.Version); err != nil {
				return err
			}
		}
	}
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE catalog_sources SET last_synced_at=:last_synced_at,last_error='',market_json=:market_json,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`),
		ptrTime(source.LastSyncedAt), jsonObject(source.MarketJSON), source.UpdatedAt, source.ID, expected)
	if err != nil {
		return mapError(err)
	}
	if err = requireAffected(result, true); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func sameCatalogVersion(left, right model.CatalogAppVersion) bool {
	return jsonObject(left.MarketJSON) == jsonObject(right.MarketJSON) && left.SourceID == right.SourceID && left.AppKey == right.AppKey && left.Name == right.Name &&
		left.Description == right.Description && left.Version == right.Version && left.Commit == right.Commit &&
		left.ArtifactDigest == right.ArtifactDigest && left.ComposeSHA256 == right.ComposeSHA256 &&
		left.SwarmYAML == right.SwarmYAML && left.SwarmSHA256 == right.SwarmSHA256 &&
		left.ManifestJSON == right.ManifestJSON && left.ComposeYAML == right.ComposeYAML && left.EnvExample == right.EnvExample &&
		left.README == right.README && left.IconURL == right.IconURL && slices.Equal(left.Services, right.Services) &&
		left.ComposeCompatible == right.ComposeCompatible && left.SwarmCompatible == right.SwarmCompatible &&
		slices.Equal(left.CompatibilityIssues, right.CompatibilityIssues)
}

func (s *SQLStore) ListCatalogAppVersions(ctx context.Context, sourceID, appKey string, page Page) ([]model.CatalogAppVersion, error) {
	page = page.normalized()
	query := `SELECT ` + catalogVersionColumns + ` FROM catalog_app_versions WHERE id>:id`
	args := []any{page.After}
	if sourceID != "" {
		query += ` AND source_id=:source_id`
		args = append(args, sourceID)
	}
	if appKey != "" {
		query += ` AND app_key=:app_key`
		args = append(args, appKey)
	}
	query += ` ORDER BY app_key,version,id LIMIT :limit`
	args = append(args, page.Limit)
	rows, err := s.db.QueryContext(ctx, s.bind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]model.CatalogAppVersion, 0, page.Limit)
	for rows.Next() {
		value, scanErr := scanCatalogVersion(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) GetCatalogAppVersion(ctx context.Context, versionID string) (model.CatalogAppVersion, error) {
	return scanCatalogVersion(s.db.QueryRowContext(ctx, s.bind(`SELECT `+catalogVersionColumns+` FROM catalog_app_versions WHERE id=:id`), versionID))
}

const applicationColumns = `id,runtime_id,name,project_name,registry_credential_ref,catalog_app_version_id,active_revision_id,desired_revision_id,replica_overrides,status,managed,last_error,revision,created_at,updated_at`

const applicationReadColumns = applicationColumns + `,COALESCE((SELECT payload FROM control_documents WHERE id=applications.id AND kind='system-application'),'{}')`

func scanApplication(row scanner) (model.Application, error) {
	var value model.Application
	var catalogID sql.NullString
	var replicaOverrides, systemJSON string
	err := row.Scan(&value.ID, &value.RuntimeID, &value.Name, &value.ProjectName, &value.RegistryCredentialRef, &catalogID, &value.ActiveRevisionID,
		&value.DesiredRevisionID, &replicaOverrides, &value.Status, &value.Managed, &value.LastError, &value.Revision, &value.CreatedAt, &value.UpdatedAt, &systemJSON)
	if catalogID.Valid {
		value.CatalogAppVersionID = catalogID.String
	}
	if err == nil {
		err = json.Unmarshal([]byte(replicaOverrides), &value.ReplicaOverrides)
	}
	if value.ReplicaOverrides == nil {
		value.ReplicaOverrides = make(map[string]int)
	}
	if err == nil {
		var metadata struct {
			System      bool                        `json:"system"`
			Key         string                      `json:"key"`
			Environment *model.EnvironmentReference `json:"environment"`
			ObservedAt  time.Time                   `json:"observed_at"`
		}
		err = json.Unmarshal([]byte(systemJSON), &metadata)
		value.System, value.SystemKey, value.Environment = metadata.System, metadata.Key, metadata.Environment
		value.LifecycleOperations = []string{}
		if value.System && (metadata.ObservedAt.IsZero() || time.Since(metadata.ObservedAt) > time.Minute) {
			value.Status = model.ApplicationUnknown
		}
	}
	return value, mapError(err)
}

func applicationArgs(value *model.Application) []any {
	replicaOverrides, _ := json.Marshal(value.ReplicaOverrides)
	if string(replicaOverrides) == "null" {
		replicaOverrides = []byte("{}")
	}
	return []any{value.ID, value.RuntimeID, value.Name, value.ProjectName, value.RegistryCredentialRef, nullString(value.CatalogAppVersionID),
		value.ActiveRevisionID, value.DesiredRevisionID, string(replicaOverrides), value.Status, value.Managed, value.LastError, value.Revision,
		value.CreatedAt, value.UpdatedAt}
}

const revisionColumns = `id,application_id,sequence,parent_revision_id,catalog_app_version_id,compose_sha256,environment_sha256,
resolved_sha256,environment_keys,secret_keys,encrypted_compose,encrypted_environment,encrypted_resolved,encryption_key_id,created_by,created_at,configuration_source`

func scanApplicationRevision(row scanner) (model.ApplicationRevision, error) {
	var value model.ApplicationRevision
	var keysJSON, secretsJSON, sourceJSON string
	err := row.Scan(&value.ID, &value.ApplicationID, &value.Sequence, &value.ParentRevisionID, &value.CatalogAppVersionID,
		&value.ComposeSHA256, &value.EnvironmentSHA256, &value.ResolvedSHA256, &keysJSON, &secretsJSON,
		&value.EncryptedCompose, &value.EncryptedEnvironment, &value.EncryptedResolved, &value.EncryptionKeyID,
		&value.CreatedBy, &value.CreatedAt, &sourceJSON)
	if err == nil {
		err = json.Unmarshal([]byte(sourceJSON), &value.ConfigurationSource)
	}
	if err == nil {
		err = json.Unmarshal([]byte(keysJSON), &value.EnvironmentKeys)
	}
	if err == nil {
		err = json.Unmarshal([]byte(secretsJSON), &value.SecretKeys)
	}
	return value, mapError(err)
}

func revisionArgs(value *model.ApplicationRevision) []any {
	sourceJSON, _ := json.Marshal(value.ConfigurationSource)
	keysJSON, _ := json.Marshal(value.EnvironmentKeys)
	secretsJSON, _ := json.Marshal(value.SecretKeys)
	return []any{value.ID, value.ApplicationID, value.Sequence, value.ParentRevisionID, value.CatalogAppVersionID,
		value.ComposeSHA256, value.EnvironmentSHA256, value.ResolvedSHA256, string(keysJSON), string(secretsJSON),
		value.EncryptedCompose, value.EncryptedEnvironment, value.EncryptedResolved, value.EncryptionKeyID, value.CreatedBy, value.CreatedAt, string(sourceJSON)}
}

func (s *SQLStore) CreateApplication(ctx context.Context, application *model.Application, revision *model.ApplicationRevision, operation *model.DeploymentOperation) error {
	return s.CreateApplicationInstall(ctx, application, revision, nil, nil, nil, nil, operation)
}

// CreateApplicationInstall persists the complete initial desired state in one
// transaction. In particular, a Route can never become visible without its
// owning Application, immutable revision, placeholder Service and deployment
// operation all having been committed successfully.
func (s *SQLStore) CreateApplicationInstall(ctx context.Context, application *model.Application, revision *model.ApplicationRevision, services []model.Service, domain *model.Domain, certificate *model.Certificate, route *model.Route, operation *model.DeploymentOperation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	for _, resource := range []string{revision.ConfigurationSource.EnvironmentID} {
		if err = s.guardProjectResourceTx(ctx, tx, resource); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO applications (`+applicationColumns+`) VALUES (`+columnPlaceholders(applicationColumns)+`)`), applicationArgs(application)...); err != nil {
		return mapError(err)
	}
	if err = s.bindEnvironmentRevision(ctx, tx, revision); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO application_revisions (`+revisionColumns+`) VALUES (`+columnPlaceholders(revisionColumns)+`)`), revisionArgs(revision)...); err != nil {
		return mapError(err)
	}
	for index := range services {
		if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO services (`+serviceColumns+`) VALUES (`+columnPlaceholders(serviceColumns)+`)`), serviceArgs(&services[index])...); err != nil {
			return mapError(err)
		}
	}
	poolID := ""
	if route != nil {
		if domain == nil {
			return fmt.Errorf("%w: install route requires a domain", ErrConflict)
		}
		poolID, err = insertServiceDomainBindingTx(ctx, s, tx, domain, certificate, route)
		if err != nil {
			return err
		}
	} else if domain != nil || certificate != nil {
		return fmt.Errorf("%w: install domain and certificate require a route", ErrConflict)
	}
	if err = s.insertOperationWithScopeTx(ctx, tx, operation); err != nil {
		return err
	}
	if poolID != "" {
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

func (s *SQLStore) ListApplications(ctx context.Context, runtimeID string, page Page) ([]model.Application, error) {
	page = page.normalized()
	query := `SELECT ` + applicationReadColumns + ` FROM applications WHERE NOT EXISTS (SELECT 1 FROM control_documents tomb WHERE tomb.kind='deleted-project-guard' AND tomb.parent_id=applications.id) AND id>:id`
	args := []any{page.After}
	if runtimeID != "" {
		query += ` AND runtime_id=:runtime_id`
		args = append(args, runtimeID)
	}
	query += ` ORDER BY id LIMIT :limit`
	args = append(args, page.Limit)
	rows, err := s.db.QueryContext(ctx, s.bind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]model.Application, 0, page.Limit)
	for rows.Next() {
		value, scanErr := scanApplication(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) GetApplication(ctx context.Context, applicationID string) (model.Application, error) {
	return scanApplication(s.db.QueryRowContext(ctx, s.bind(`SELECT `+applicationReadColumns+` FROM applications WHERE id=:id AND NOT EXISTS (SELECT 1 FROM control_documents tomb WHERE tomb.kind='deleted-project-guard' AND tomb.parent_id=applications.id)`), applicationID))
}

func (s *SQLStore) UpdateApplication(ctx context.Context, value *model.Application, expected int64) error {
	replicaOverrides, _ := json.Marshal(value.ReplicaOverrides)
	if string(replicaOverrides) == "null" {
		replicaOverrides = []byte("{}")
	}
	result, err := s.execProjectMutation(ctx, value.ID, s.bind(`UPDATE applications SET name=:name,registry_credential_ref=:registry_credential_ref,catalog_app_version_id=:catalog_app_version_id,active_revision_id=:active_revision_id,desired_revision_id=:desired_revision_id,replica_overrides=:replica_overrides,status=:status,managed=:managed,last_error=:last_error,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`),
		value.Name, value.RegistryCredentialRef, nullString(value.CatalogAppVersionID), value.ActiveRevisionID, value.DesiredRevisionID,
		string(replicaOverrides), value.Status, value.Managed, value.LastError, value.UpdatedAt, value.ID, expected)
	if err != nil {
		return mapError(err)
	}
	return requireAffected(result, true)
}

// SetApplicationStatus updates only operation-owned lifecycle state. Desired
// revisions and catalog metadata may be changed concurrently by a newly queued
// upgrade and must never be overwritten by a long-running Docker command.
func (s *SQLStore) SetApplicationStatus(ctx context.Context, applicationID string, status model.ApplicationStatus, lastError string, now time.Time) error {
	result, err := s.execProjectMutation(ctx, applicationID, s.bind(`UPDATE applications SET status=:status,last_error=:last_error,revision=revision+1,updated_at=:updated_at WHERE id=:id`),
		status, lastError, now, applicationID)
	if err != nil {
		return mapError(err)
	}
	return requireAffected(result, false)
}

// FailServiceRevision restores the lifecycle state of the last active
// revision, while retaining the failed desired revision for diagnosis.
func (s *SQLStore) FailServiceRevision(ctx context.Context, applicationID, revisionID string, status model.ApplicationStatus, lastError string, now time.Time) error {
	if status != model.ApplicationRunning && status != model.ApplicationStopped && status != model.ApplicationDown {
		return ErrPrecondition
	}
	result, err := s.execProjectMutation(ctx, applicationID, s.bind(`UPDATE applications SET status=:status,last_error=:last_error,revision=revision+1,updated_at=:updated_at WHERE id=:id AND desired_revision_id=:desired_revision_id AND active_revision_id<>:desired_revision_id_2`),
		status, lastError, now, applicationID, revisionID, revisionID)
	if err != nil {
		return mapError(err)
	}
	return requireAffected(result, true)
}

// DeferServiceRevision keeps an application stopped while making a selected
// Compose source update the desired revision for its next explicit START.
func (s *SQLStore) DeferServiceRevision(ctx context.Context, applicationID, revisionID, serviceName string, status model.ApplicationStatus, now time.Time) error {
	if status != model.ApplicationStopped && status != model.ApplicationDown {
		return ErrPrecondition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if err = s.guardProjectResourceTx(ctx, tx, applicationID); err != nil {
		return err
	}
	query := `SELECT desired_revision_id,replica_overrides FROM applications WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	var desired, encoded string
	if err = tx.QueryRowContext(ctx, s.bind(query), applicationID).Scan(&desired, &encoded); err != nil {
		return mapError(err)
	}
	if desired != revisionID {
		return ErrPrecondition
	}
	overrides := map[string]int{}
	if err = json.Unmarshal([]byte(encoded), &overrides); err != nil {
		return fmt.Errorf("decode replica overrides: %w", err)
	}
	delete(overrides, serviceName)
	data, err := json.Marshal(overrides)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE applications SET replica_overrides=:replica_overrides,status=:status,last_error='',revision=revision+1,updated_at=:updated_at WHERE id=:id AND desired_revision_id=:desired_revision_id`),
		string(data), status, now, applicationID, revisionID)
	if err != nil {
		return err
	}
	if err = requireAffected(result, true); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) SetApplicationReplicaOverride(ctx context.Context, applicationID, serviceName string, replicas int, now time.Time) error {
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
	query := `SELECT replica_overrides FROM applications WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	var encoded string
	if err = tx.QueryRowContext(ctx, s.bind(query), applicationID).Scan(&encoded); err != nil {
		return mapError(err)
	}
	overrides := make(map[string]int)
	if err = json.Unmarshal([]byte(encoded), &overrides); err != nil {
		return fmt.Errorf("decode replica overrides: %w", err)
	}
	overrides[serviceName] = replicas
	data, err := json.Marshal(overrides)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE applications SET replica_overrides=:replica_overrides,revision=revision+1,updated_at=:updated_at WHERE id=:id`), string(data), now, applicationID)
	if err != nil {
		return mapError(err)
	}
	if err = requireAffected(result, false); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) CreateApplicationRevision(ctx context.Context, revision *model.ApplicationRevision, operation *model.DeploymentOperation, expected int64, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	for _, resource := range []string{revision.ApplicationID} {
		if err = s.guardProjectResourceTx(ctx, tx, resource); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE applications SET desired_revision_id=:desired_revision_id,catalog_app_version_id=:catalog_app_version_id,status=:status,last_error='',revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`),
		revision.ID, nullString(revision.CatalogAppVersionID), model.ApplicationPending, now, revision.ApplicationID, expected)
	if err != nil {
		return mapError(err)
	}
	if err = requireAffected(result, true); err != nil {
		return err
	}
	if err = s.bindEnvironmentRevision(ctx, tx, revision); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO application_revisions (`+revisionColumns+`) VALUES (`+columnPlaceholders(revisionColumns)+`)`), revisionArgs(revision)...); err != nil {
		return mapError(err)
	}
	if err = s.insertOperationWithScopeTx(ctx, tx, operation); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) ListApplicationRevisions(ctx context.Context, applicationID string, page Page) ([]model.ApplicationRevision, error) {
	page = page.normalized()
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+revisionColumns+` FROM application_revisions WHERE application_id=:application_id AND id>:id ORDER BY sequence,id LIMIT :limit`), applicationID, page.After, page.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]model.ApplicationRevision, 0, page.Limit)
	for rows.Next() {
		value, scanErr := scanApplicationRevision(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) GetApplicationRevision(ctx context.Context, revisionID string) (model.ApplicationRevision, error) {
	return scanApplicationRevision(s.db.QueryRowContext(ctx, s.bind(`SELECT `+revisionColumns+` FROM application_revisions WHERE id=:id`), revisionID))
}

const operationColumns = `id,application_id,runtime_id,service_id,revision_id,operation_type,status,idempotency_key,
request_sha256,encrypted_input,encryption_key_id,redacted_log,error_code,error_message,attempt,worker_id,heartbeat_at,started_at,finished_at,
duration_ms,revision,created_at,updated_at,runtime_identity`

func scanOperation(row scanner) (model.DeploymentOperation, error) {
	var value model.DeploymentOperation
	var idempotency sql.NullString
	var identity string
	var heartbeat, started, finished sql.NullTime
	err := row.Scan(&value.ID, &value.ApplicationID, &value.RuntimeID, &value.ServiceID, &value.RevisionID, &value.Type,
		&value.Status, &idempotency, &value.RequestSHA256, &value.EncryptedInput, &value.EncryptionKeyID, &value.RedactedLog, &value.ErrorCode,
		&value.ErrorMessage, &value.Attempt, &value.WorkerID, &heartbeat, &started, &finished, &value.DurationMS,
		&value.Revision, &value.CreatedAt, &value.UpdatedAt, &identity)
	if err == nil && identity != "" {
		if err := json.Unmarshal([]byte(identity), &value.ExecutionIdentity); err != nil {
			return value, err
		}
	}
	if idempotency.Valid {
		value.IdempotencyKey = idempotency.String
	}
	if heartbeat.Valid {
		value.HeartbeatAt = &heartbeat.Time
	}
	if started.Valid {
		value.StartedAt = &started.Time
	}
	if finished.Valid {
		value.FinishedAt = &finished.Time
	}
	return value, mapError(err)
}

func operationArgs(value *model.DeploymentOperation) []any {
	identity, _ := json.Marshal(value.ExecutionIdentity)
	return []any{value.ID, value.ApplicationID, value.RuntimeID, value.ServiceID, value.RevisionID, value.Type, value.Status,
		nullString(value.IdempotencyKey), value.RequestSHA256, value.EncryptedInput, value.EncryptionKeyID, value.RedactedLog, value.ErrorCode,
		value.ErrorMessage, value.Attempt, value.WorkerID, ptrTime(value.HeartbeatAt), ptrTime(value.StartedAt), ptrTime(value.FinishedAt),
		value.DurationMS, value.Revision, value.CreatedAt, value.UpdatedAt, string(identity)}
}

func (s *SQLStore) CreateDeploymentOperation(ctx context.Context, value *model.DeploymentOperation) (model.DeploymentOperation, error) {
	if value.IdempotencyKey != "" {
		existing, err := scanOperation(s.db.QueryRowContext(ctx, s.bind(`SELECT `+operationColumns+` FROM deployment_operations WHERE runtime_id=:runtime_id AND idempotency_key=:idempotency_key`), value.RuntimeID, value.IdempotencyKey))
		if err == nil {
			if !sameIdempotentOperation(existing, *value) {
				return model.DeploymentOperation{}, ErrConflict
			}
			return existing, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return model.DeploymentOperation{}, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if err = s.guardProjectResourceTx(ctx, tx, value.ApplicationID); err != nil {
		return model.DeploymentOperation{}, err
	}
	err = s.insertOperationWithScopeTx(ctx, tx, value)
	if err == nil {
		err = tx.Commit()
		committed = err == nil
	} else {
		_ = tx.Rollback()
		committed = true
	}
	if err != nil && value.IdempotencyKey != "" && errors.Is(mapError(err), ErrConflict) {
		existing, getErr := scanOperation(s.db.QueryRowContext(ctx, s.bind(`SELECT `+operationColumns+` FROM deployment_operations WHERE runtime_id=:runtime_id AND idempotency_key=:idempotency_key`), value.RuntimeID, value.IdempotencyKey))
		if getErr != nil {
			if errors.Is(getErr, ErrNotFound) {
				return model.DeploymentOperation{}, mapError(err)
			}
			return model.DeploymentOperation{}, getErr
		}
		if !sameIdempotentOperation(existing, *value) {
			return model.DeploymentOperation{}, ErrConflict
		}
		return existing, nil
	}
	return *value, mapError(err)
}

func sameIdempotentOperation(existing, candidate model.DeploymentOperation) bool {
	if existing.RuntimeID != candidate.RuntimeID {
		return false
	}
	if existing.RequestSHA256 != "" || candidate.RequestSHA256 != "" {
		return existing.RequestSHA256 != "" && existing.RequestSHA256 == candidate.RequestSHA256
	}
	return existing.Type == candidate.Type && existing.ApplicationID == candidate.ApplicationID &&
		existing.ServiceID == candidate.ServiceID && existing.RevisionID == candidate.RevisionID
}

func (s *SQLStore) ListDeploymentOperations(ctx context.Context, applicationID string, page Page) ([]model.DeploymentOperation, error) {
	page = page.normalized()
	query := `SELECT ` + operationColumns + ` FROM deployment_operations WHERE id>:id`
	args := []any{page.After}
	if applicationID != "" {
		query += ` AND application_id=:application_id`
		args = append(args, applicationID)
	}
	query += ` ORDER BY id LIMIT :limit`
	args = append(args, page.Limit)
	rows, err := s.db.QueryContext(ctx, s.bind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]model.DeploymentOperation, 0, page.Limit)
	for rows.Next() {
		value, scanErr := scanOperation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) GetDeploymentOperation(ctx context.Context, operationID string) (model.DeploymentOperation, error) {
	return scanOperation(s.db.QueryRowContext(ctx, s.bind(`SELECT `+operationColumns+` FROM deployment_operations WHERE id=:id`), operationID))
}

// GetDeploymentOperationScope returns the claim captured when the operation
// was queued. Current service or application bindings are not evidence for
// historical authorization.
func (s *SQLStore) GetDeploymentOperationScope(ctx context.Context, operationID string) (string, string, bool, error) {
	var projectID, environmentID string
	var snapshot bool
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT project_id,environment_id,scope_snapshot FROM deployment_operations WHERE id=:id`), operationID).Scan(&projectID, &environmentID, &snapshot)
	return projectID, environmentID, snapshot, mapError(err)
}

func (s *SQLStore) GetDeploymentOperationByIdempotency(ctx context.Context, runtimeID, key string) (model.DeploymentOperation, error) {
	if strings.TrimSpace(key) == "" {
		return model.DeploymentOperation{}, ErrNotFound
	}
	return scanOperation(s.db.QueryRowContext(ctx, s.bind(`SELECT `+operationColumns+` FROM deployment_operations WHERE runtime_id=:runtime_id AND idempotency_key=:idempotency_key`), runtimeID, strings.TrimSpace(key)))
}

func (s *SQLStore) ReencryptApplicationRevisions(ctx context.Context, transform func(model.ApplicationRevision) ([]byte, []byte, []byte, string, error)) (int, error) {
	count := 0
	after := ""
	for {
		rows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+revisionColumns+` FROM application_revisions WHERE id>:id ORDER BY id LIMIT :limit`), after, 200)
		if err != nil {
			return count, err
		}
		values := make([]model.ApplicationRevision, 0, 200)
		for rows.Next() {
			value, scanErr := scanApplicationRevision(rows)
			if scanErr != nil {
				rows.Close()
				return count, scanErr
			}
			values = append(values, value)
		}
		if err = rows.Close(); err != nil {
			return count, err
		}
		if err = rows.Err(); err != nil {
			return count, err
		}
		for _, value := range values {
			compose, environment, resolved, keyID, transformErr := transform(value)
			if transformErr != nil {
				return count, transformErr
			}
			if keyID == value.EncryptionKeyID {
				continue
			}
			result, updateErr := s.db.ExecContext(ctx, s.bind(`UPDATE application_revisions SET encrypted_compose=:encrypted_compose,encrypted_environment=:encrypted_environment,encrypted_resolved=:encrypted_resolved,encryption_key_id=:encryption_key_id WHERE id=:id AND encryption_key_id=:encryption_key_id_2`),
				compose, environment, resolved, keyID, value.ID, value.EncryptionKeyID)
			if updateErr != nil {
				return count, updateErr
			}
			if updateErr = requireAffected(result, true); updateErr != nil {
				return count, updateErr
			}
			count++
		}
		if len(values) < 200 {
			return count, nil
		}
		after = values[len(values)-1].ID
	}
}

func (s *SQLStore) ReencryptDeploymentOperations(ctx context.Context, transform func(model.DeploymentOperation) ([]byte, string, error)) (int, error) {
	count := 0
	after := ""
	for {
		values, err := s.ListDeploymentOperations(ctx, "", Page{After: after, Limit: 200})
		if err != nil {
			return count, err
		}
		for _, value := range values {
			if len(value.EncryptedInput) == 0 {
				continue
			}
			encrypted, keyID, transformErr := transform(value)
			if transformErr != nil {
				return count, transformErr
			}
			if keyID == value.EncryptionKeyID {
				continue
			}
			now := time.Now().UTC()
			result, updateErr := s.db.ExecContext(ctx, s.bind(`UPDATE deployment_operations SET encrypted_input=:encrypted_input,encryption_key_id=:encryption_key_id,revision=revision+1,updated_at=:updated_at WHERE id=:id AND encryption_key_id=:encryption_key_id_2`),
				encrypted, keyID, now, value.ID, value.EncryptionKeyID)
			if updateErr != nil {
				return count, updateErr
			}
			if updateErr = requireAffected(result, true); updateErr != nil {
				return count, updateErr
			}
			count++
		}
		if len(values) < 200 {
			return count, nil
		}
		after = values[len(values)-1].ID
	}
}

func (s *SQLStore) ClaimDeploymentOperation(ctx context.Context, workerID string, now, staleBefore time.Time) (model.DeploymentOperation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE deployment_operations SET status=:status,worker_id='',heartbeat_at=NULL,revision=revision+1,updated_at=:updated_at WHERE status=:status_2 AND heartbeat_at IS NOT NULL AND heartbeat_at<:heartbeat_at
        AND NOT EXISTS (SELECT 1 FROM control_documents h WHERE h.id='recovery:barrier' OR h.id=`+s.concat("'recovery-hold:deployment:'", "deployment_operations.id")+`)`),
		model.OperationQueued, now, model.OperationRunning, staleBefore); err != nil {
		return model.DeploymentOperation{}, err
	}
	query := `SELECT ` + operationColumns + ` FROM deployment_operations o WHERE o.status=:status
        AND NOT EXISTS (SELECT 1 FROM control_documents h WHERE h.id='recovery:barrier' OR h.id=` + s.concat("'recovery-hold:deployment:'", "o.id") + `) AND (o.application_id='' OR NOT EXISTS
		(SELECT 1 FROM deployment_operations active WHERE active.application_id=o.application_id AND active.status=:status_2)) ORDER BY o.created_at,o.id LIMIT 1`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	value, err := scanOperation(tx.QueryRowContext(ctx, s.bind(query), model.OperationQueued, model.OperationRunning))
	if err != nil {
		return value, err
	}
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE deployment_operations SET status=:status,worker_id=:worker_id,heartbeat_at=:heartbeat_at,started_at=COALESCE(started_at,:started_at),attempt=attempt+1,revision=revision+1,updated_at=:updated_at WHERE id=:id AND status=:status_2`),
		model.OperationRunning, workerID, now, now, now, value.ID, model.OperationQueued)
	if err != nil {
		return value, err
	}
	if err = requireAffected(result, false); err != nil {
		return value, err
	}
	if err = tx.Commit(); err != nil {
		return value, err
	}
	committed = true
	return s.GetDeploymentOperation(ctx, value.ID)
}

func (s *SQLStore) HeartbeatDeploymentOperation(ctx context.Context, operationID, workerID string, now time.Time) error {
	query := `UPDATE deployment_operations SET heartbeat_at=:heartbeat_at,updated_at=:updated_at WHERE id=:id AND worker_id=:worker_id AND status=:status`
	args := []any{now, now, operationID, workerID, model.OperationRunning}
	if guard, ok := ctx.Value(operationGuardKey{}).(operationGuard); ok {
		if guard.ID != operationID || guard.Worker != workerID {
			return ErrOperationOwnership
		}
		query += ` AND attempt=:attempt`
		args = append(args, guard.Attempt)
	}
	result, err := s.db.ExecContext(ctx, s.bind(query), args...)
	if err != nil {
		return err
	}
	return requireAffected(result, false)
}

func (s *SQLStore) FinishDeploymentOperation(ctx context.Context, value *model.DeploymentOperation, now time.Time) error {
	var executor interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	} = s.db
	var tx *sql.Tx
	if ctx.Value(operationAuthorityKey{}) != nil {
		var err error
		tx, err = s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = s.guardDeploymentOperationTx(ctx, tx, value.ApplicationID); err != nil {
			return err
		}
		executor = tx
	}
	value.FinishedAt = &now
	if value.StartedAt != nil {
		value.DurationMS = now.Sub(*value.StartedAt).Milliseconds()
	}
	clearInput := value.Type == "DOCKER_RESOURCE_CREATE" || value.Type == "DOCKER_RESOURCE_DELETE" || value.Type == "VOLUME_CREATE" || value.Type == "VOLUME_DELETE"
	result, err := executor.ExecContext(ctx, s.bind(`UPDATE deployment_operations SET status=:status,redacted_log=:redacted_log,error_code=:error_code,error_message=:error_message,heartbeat_at=NULL,finished_at=:finished_at,duration_ms=:duration_ms,encrypted_input=CASE WHEN :clear_input THEN :cleared_input ELSE encrypted_input END,encryption_key_id=CASE WHEN :clear_key_id THEN '' ELSE encryption_key_id END,revision=revision+1,updated_at=:updated_at WHERE id=:id AND worker_id=:worker_id AND status=:running_status AND attempt=:attempt`),
		value.Status, value.RedactedLog, value.ErrorCode, value.ErrorMessage, now, value.DurationMS, clearInput, []byte{}, clearInput, now, value.ID, value.WorkerID, model.OperationRunning, value.Attempt)
	if err != nil {
		return err
	}
	if err = requireAffected(result, false); err != nil {
		return err
	}
	if tx != nil {
		return tx.Commit()
	}
	return nil
}

// ActivateApplicationRevision atomically commits the complete Controller-side
// result of a successful Docker deployment. The active Service set,
// Application revision/status and recoverable API Routes can therefore never
// describe different deployments. completedRevisionID is the operation's
// candidate; it differs from activeRevisionID only while committing a rollback.
// A newer desired revision queued while Docker was converging is preserved.
func (s *SQLStore) ActivateApplicationRevision(ctx context.Context, runtimeID, applicationID, activeRevisionID, completedRevisionID string, serviceKeys []string, replicaOverrides map[string]int, lastError string, now time.Time) error {
	return s.activateApplicationRevision(ctx, runtimeID, applicationID, activeRevisionID, completedRevisionID, serviceKeys, replicaOverrides, lastError, now, false)
}

func (s *SQLStore) ActivateServiceRevision(ctx context.Context, runtimeID, applicationID, activeRevisionID, completedRevisionID string, serviceKeys []string, replicaOverrides map[string]int, lastError string, now time.Time) error {
	return s.activateApplicationRevision(ctx, runtimeID, applicationID, activeRevisionID, completedRevisionID, serviceKeys, replicaOverrides, lastError, now, true)
}

func (s *SQLStore) activateApplicationRevision(ctx context.Context, runtimeID, applicationID, activeRevisionID, completedRevisionID string, serviceKeys []string, replicaOverrides map[string]int, lastError string, now time.Time, incremental bool) error {
	if len(serviceKeys) == 0 {
		return fmt.Errorf("%w: an application revision must contain at least one service", ErrConflict)
	}
	if activeRevisionID == "" || completedRevisionID == "" {
		return fmt.Errorf("%w: active and completed revision identities are required", ErrConflict)
	}
	serviceKeys = uniqueStrings(serviceKeys)
	overridesJSON, err := json.Marshal(replicaOverrides)
	if err != nil {
		return err
	}
	if string(overridesJSON) == "null" {
		overridesJSON = []byte("{}")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if err = s.guardDeploymentOperationTx(ctx, tx, applicationID); err != nil {
		return err
	}
	for _, resource := range []string{applicationID} {
		if err = s.guardProjectResourceTx(ctx, tx, resource); err != nil {
			return err
		}
	}

	applicationQuery := `SELECT a.runtime_id,rt.pool_id,a.desired_revision_id,a.catalog_app_version_id FROM applications a JOIN runtimes rt ON rt.id=a.runtime_id WHERE a.id=:id`
	if s.dialect != SQLite {
		applicationQuery += ` FOR UPDATE`
	}
	var applicationRuntimeID, poolID, desiredRevisionID string
	var currentCatalogVersion sql.NullString
	if err = tx.QueryRowContext(ctx, s.bind(applicationQuery), applicationID).Scan(&applicationRuntimeID, &poolID, &desiredRevisionID, &currentCatalogVersion); err != nil {
		return mapError(err)
	}
	if applicationRuntimeID != runtimeID {
		return fmt.Errorf("%w: application and runtime differ", ErrConflict)
	}
	var revisionOwner string
	var activeCatalogVersion sql.NullString
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT application_id,catalog_app_version_id FROM application_revisions WHERE id=:id`), activeRevisionID).Scan(&revisionOwner, &activeCatalogVersion); err != nil {
		return mapError(err)
	}
	if revisionOwner != applicationID {
		return fmt.Errorf("%w: active revision belongs to another application", ErrConflict)
	}
	var completedOwner string
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT application_id FROM application_revisions WHERE id=:id`), completedRevisionID).Scan(&completedOwner); err != nil {
		return mapError(err)
	}
	if completedOwner != applicationID {
		return fmt.Errorf("%w: completed revision belongs to another application", ErrConflict)
	}
	stateBefore, err := gatewayDataPlaneStateTx(ctx, s, tx, poolID)
	if err != nil {
		return err
	}
	probeBefore, err := gatewayProbeStateTx(ctx, s, tx, poolID)
	if err != nil {
		return err
	}

	marks := namedPlaceholders("service_key", len(serviceKeys))
	keyArgs := make([]any, 0, len(serviceKeys)+1)
	keyArgs = append(keyArgs, runtimeID)
	for _, key := range serviceKeys {
		keyArgs = append(keyArgs, key)
	}
	rows, err := tx.QueryContext(ctx, s.bind(fmt.Sprintf(`SELECT service_key,application_id,managed FROM services WHERE runtime_id=:runtime_id AND service_key IN (%s)`, marks)), keyArgs...)
	if err != nil {
		return err
	}
	found := make(map[string]struct{}, len(serviceKeys))
	for rows.Next() {
		var key string
		var owner sql.NullString
		var managed bool
		if err = rows.Scan(&key, &owner, &managed); err != nil {
			rows.Close()
			return err
		}
		if owner.Valid && owner.String != applicationID {
			rows.Close()
			return fmt.Errorf("%w: service %q is managed by another application", ErrConflict, key)
		}
		found[key] = struct{}{}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, key := range serviceKeys {
		if _, ok := found[key]; !ok {
			return fmt.Errorf("%w: application service %q is missing", ErrNotFound, key)
		}
	}

	activeArgs := []any{applicationID, true, model.ServiceRemoved, model.ServiceUnavailable, model.ServiceRemoved, now, runtimeID}
	for _, key := range serviceKeys {
		activeArgs = append(activeArgs, key)
	}
	if _, err = tx.ExecContext(ctx, s.bind(fmt.Sprintf(`UPDATE services SET application_id=:application_id,managed=:managed,status=CASE WHEN status=:removed_status THEN :unavailable_status ELSE status END,removed_at=CASE WHEN status=:removed_status_for_null THEN NULL ELSE removed_at END,revision=revision+1,updated_at=:updated_at WHERE runtime_id=:runtime_id AND service_key IN (%s)`, marks)), activeArgs...); err != nil {
		return err
	}
	readyArgs := []any{true, "[]", model.DNSReady, now, runtimeID}
	readyArgs = append(readyArgs, stringsToAny(serviceKeys)...)
	readyArgs = append(readyArgs, model.RouteDisabled)
	if _, err = tx.ExecContext(ctx, s.bind(fmt.Sprintf(`UPDATE services SET routing_eligible=:routing_eligible,routing_issues=:routing_issues,dns_status=:dns_status,dns_last_error='',revision=revision+1,updated_at=:updated_at
		WHERE runtime_id=:runtime_id AND service_key IN (%s) AND EXISTS (SELECT 1 FROM routes WHERE routes.service_id=services.id AND routes.route_status<>:route_status)`, marks)), readyArgs...); err != nil {
		return err
	}

	removedPredicate := ""
	if incremental {
		removedPredicate = " AND 1=0"
	}
	removedRows, err := tx.QueryContext(ctx, s.bind(fmt.Sprintf(`SELECT id FROM services WHERE runtime_id=:runtime_id AND application_id=:application_id AND service_key NOT IN (%s)`+removedPredicate+` ORDER BY id`, marks)), append([]any{runtimeID, applicationID}, stringsToAny(serviceKeys)...)...)
	if err != nil {
		return err
	}
	var removedIDs []string
	for removedRows.Next() {
		var serviceID string
		if err = removedRows.Scan(&serviceID); err != nil {
			removedRows.Close()
			return err
		}
		removedIDs = append(removedIDs, serviceID)
	}
	if err = removedRows.Close(); err != nil {
		return err
	}
	if err = removedRows.Err(); err != nil {
		return err
	}
	if len(removedIDs) > 0 {
		removedMarks := namedPlaceholders("service_id", len(removedIDs))
		message := "service removed from active application revision"
		routeArgs := []any{false, model.RouteError, message, now}
		routeArgs = append(routeArgs, stringsToAny(removedIDs)...)
		routeArgs = append(routeArgs, model.RouteDisabled)
		if _, err = tx.ExecContext(ctx, s.bind(fmt.Sprintf(`UPDATE routes SET enabled=:enabled,route_status=:route_status,last_error=:last_error,revision=revision+1,updated_at=:updated_at WHERE service_id IN (%s) AND route_status<>:route_status_2`, removedMarks)), routeArgs...); err != nil {
			return err
		}
		issues, _ := json.Marshal([]string{message})
		serviceArgs := []any{applicationID, false, false, string(issues), model.DNSMissing, message, model.ServiceRemoved, now, now}
		serviceArgs = append(serviceArgs, stringsToAny(removedIDs)...)
		if _, err = tx.ExecContext(ctx, s.bind(fmt.Sprintf(`UPDATE services SET application_id=:application_id,managed=:managed,routing_eligible=:routing_eligible,routing_issues=:routing_issues,dns_status=:dns_status,dns_last_error=:dns_last_error,desired_instances=0,running_instances=0,runtime_ready_instances=0,ready_instances=0,status=:status,removed_at=COALESCE(removed_at,:removed_at),revision=revision+1,updated_at=:updated_at WHERE id IN (%s)`, removedMarks)), serviceArgs...); err != nil {
			return err
		}
	}
	if err = resolveServiceDNSConflictsTx(ctx, s, tx, poolID, now); err != nil {
		return err
	}

	// Recover only one deterministic winner when multiple previously failed
	// bindings now compete for the same precedence. A routing conflict must not
	// roll back an otherwise successful application deployment.
	routeRows, err := tx.QueryContext(ctx, s.bind(`SELECT `+routeColumns+` FROM routes WHERE source=:source
		AND service_id IN (SELECT id FROM services WHERE application_id=:application_id AND managed=:managed)
		AND route_status IN (:pending_status,:error_status) ORDER BY id`), model.SourceAPI, applicationID, true, model.RoutePending, model.RouteError)
	if err != nil {
		return err
	}
	var recoverableRoutes []model.Route
	for routeRows.Next() {
		route, scanErr := scanRoute(routeRows)
		if scanErr != nil {
			routeRows.Close()
			return scanErr
		}
		recoverableRoutes = append(recoverableRoutes, route)
	}
	if err = routeRows.Close(); err != nil {
		return err
	}
	if err = routeRows.Err(); err != nil {
		return err
	}
	for _, route := range recoverableRoutes {
		if incremental {
			var key string
			if err = tx.QueryRowContext(ctx, s.bind(`SELECT service_key FROM services WHERE id=:id`), route.ServiceID).Scan(&key); err != nil {
				return err
			}
			if !slices.Contains(serviceKeys, key) {
				continue
			}
		}
		if precedenceErr := ensureRoutePrecedenceAvailableTx(ctx, s, tx, route, route.ID); precedenceErr != nil {
			if errors.Is(precedenceErr, ErrConflict) {
				continue
			}
			return precedenceErr
		}
		if _, err = tx.ExecContext(ctx, s.bind(`UPDATE routes SET route_status=:route_status,enabled=:enabled,last_error='',revision=revision+1,updated_at=:updated_at WHERE id=:id`),
			model.RouteActive, true, now, route.ID); err != nil {
			return err
		}
	}

	nextDesiredRevisionID := desiredRevisionID
	nextCatalogVersion := currentCatalogVersion.String
	if desiredRevisionID == "" || desiredRevisionID == completedRevisionID {
		nextDesiredRevisionID = activeRevisionID
		nextCatalogVersion = activeCatalogVersion.String
	}
	applicationStatus := model.ApplicationPending
	if nextDesiredRevisionID == activeRevisionID {
		applicationStatus = model.ApplicationRunning
	}
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE applications SET active_revision_id=:active_revision_id,desired_revision_id=:desired_revision_id,catalog_app_version_id=:catalog_app_version_id,replica_overrides=:replica_overrides,status=:status,last_error=:last_error,revision=revision+1,updated_at=:updated_at WHERE id=:id AND runtime_id=:runtime_id`),
		activeRevisionID, nextDesiredRevisionID, nullString(nextCatalogVersion), string(overridesJSON), applicationStatus, lastError, now, applicationID, runtimeID)
	if err != nil {
		return mapError(err)
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
	return nil
}

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for index := range values {
		result[index] = values[index]
	}
	return result
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (s *SQLStore) ListServicesFiltered(ctx context.Context, filter ServiceFilter, page Page) ([]model.Service, error) {
	page = page.normalized()
	query := `SELECT ` + serviceColumns + ` FROM services WHERE NOT EXISTS (SELECT 1 FROM control_documents tomb WHERE tomb.kind='deleted-project-guard' AND tomb.parent_id=services.id) AND id>:id`
	args := []any{page.After}
	if filter.ExcludeRemoved {
		query += " AND status<>:status"
		args = append(args, model.ServiceRemoved)
	}
	if filter.GroupKey != "" {
		parts, err := ParseGroupKey(filter.GroupKey)
		if err != nil {
			return nil, err
		}
		query += " AND runtime_id=:runtime_id AND namespace=:namespace AND runtime_id IN (SELECT id FROM runtimes WHERE runtime_type=:runtime_type)"
		args = append(args, parts[0], parts[2], parts[1])
	}
	if filter.RuntimeID != "" {
		query += ` AND runtime_id=:runtime_id`
		args = append(args, filter.RuntimeID)
	}
	if filter.ApplicationID != "" {
		query += ` AND application_id=:application_id`
		args = append(args, filter.ApplicationID)
	}
	if filter.Status != "" {
		query += ` AND status=:status`
		args = append(args, filter.Status)
	}
	if filter.Managed != nil {
		query += ` AND managed=:managed`
		args = append(args, *filter.Managed)
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		query += ` AND (LOWER(name) LIKE :search_name OR LOWER(namespace) LIKE :search_namespace OR LOWER(service_key) LIKE :search_service_key)`
		pattern := "%" + strings.ToLower(search) + "%"
		args = append(args, pattern, pattern, pattern)
	}
	query += ` ORDER BY id LIMIT :limit`
	args = append(args, page.Limit)
	rows, err := s.db.QueryContext(ctx, s.bind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]model.Service, 0, page.Limit)
	for rows.Next() {
		value, scanErr := scanService(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// ListAllServicesFiltered is for correctness-sensitive controller workflows
// such as project ownership and revision activation. Public list endpoints stay
// paginated, while these internal decisions must never silently ignore service
// 201 and accidentally claim or omit an existing Docker object.
func (s *SQLStore) ListAllServicesFiltered(ctx context.Context, filter ServiceFilter) ([]model.Service, error) {
	var values []model.Service
	after := ""
	for {
		page, err := s.ListServicesFiltered(ctx, filter, Page{After: after, Limit: 200})
		if err != nil {
			return nil, err
		}
		values = append(values, page...)
		if len(page) < 200 {
			return values, nil
		}
		after = page[len(page)-1].ID
	}
}

func jsonObject(value string) string {
	if value == "" {
		return "{}"
	}
	return value
}
