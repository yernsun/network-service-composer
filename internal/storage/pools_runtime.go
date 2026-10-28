package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

const poolColumns = `id, name, backend_network, challenge_upstream, enabled,
desired_revision, probe_revision, probe_gateway_id, probe_epoch, last_snapshot_sha256,
last_snapshot_built_at, revision, created_at, updated_at`

type scanner interface{ Scan(...any) error }

func scanPool(row scanner) (model.GatewayPool, error) {
	var value model.GatewayPool
	var snapshotAt sql.NullTime
	err := row.Scan(&value.ID, &value.Name, &value.BackendNetwork, &value.ChallengeUpstream, &value.Enabled,
		&value.DesiredRevision, &value.ProbeRevision, &value.ProbeGatewayID, &value.ProbeEpoch, &value.LastSnapshotSHA256,
		&snapshotAt, &value.Revision, &value.CreatedAt, &value.UpdatedAt)
	if snapshotAt.Valid {
		value.LastSnapshotBuiltAt = snapshotAt.Time
	}
	return value, mapError(err)
}

func (s *SQLStore) CreatePool(ctx context.Context, value *model.GatewayPool) error {
	query := s.bind(`INSERT INTO gateway_pools
		(id,name,backend_network,challenge_upstream,enabled,desired_revision,probe_revision,probe_gateway_id,probe_epoch,last_snapshot_sha256,last_snapshot_built_at,revision,created_at,updated_at)
		VALUES (:id,:name,:backend_network,:challenge_upstream,:enabled,:desired_revision,:probe_revision,:probe_gateway_id,:probe_epoch,:last_snapshot_sha256,:last_snapshot_built_at,:revision,:created_at,:updated_at)`)
	_, err := s.db.ExecContext(ctx, query, value.ID, value.Name, value.BackendNetwork, value.ChallengeUpstream,
		value.Enabled, value.DesiredRevision, value.ProbeRevision, value.ProbeGatewayID, value.ProbeEpoch, value.LastSnapshotSHA256,
		nullTime(value.LastSnapshotBuiltAt), value.Revision, value.CreatedAt, value.UpdatedAt)
	return mapError(err)
}

func (s *SQLStore) ListPools(ctx context.Context, page Page) ([]model.GatewayPool, error) {
	page = page.normalized()
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+poolColumns+` FROM gateway_pools WHERE id > :id ORDER BY id LIMIT :limit`), page.After, page.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]model.GatewayPool, 0, page.Limit)
	for rows.Next() {
		value, err := scanPool(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) GetPool(ctx context.Context, id string) (model.GatewayPool, error) {
	return scanPool(s.db.QueryRowContext(ctx, s.bind(`SELECT `+poolColumns+` FROM gateway_pools WHERE id = :id`), id))
}

func (s *SQLStore) UpdatePool(ctx context.Context, value *model.GatewayPool, expected int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	query := `SELECT ` + poolColumns + ` FROM gateway_pools WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	current, err := scanPool(tx.QueryRowContext(ctx, s.bind(query), value.ID))
	if err != nil {
		return err
	}
	effectiveChanged := current.ChallengeUpstream != value.ChallengeUpstream || current.Enabled != value.Enabled
	revisionExpression := "revision+1"
	if effectiveChanged {
		// bumpPoolRevisionTx increments both the desired and resource revisions.
		revisionExpression = "revision"
	}
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE gateway_pools SET name=:name, backend_network=:backend_network, challenge_upstream=:challenge_upstream, enabled=:enabled,
		revision=`+revisionExpression+`,updated_at=:updated_at WHERE id=:id AND revision=:revision`),
		value.Name, value.BackendNetwork, value.ChallengeUpstream, value.Enabled, value.UpdatedAt, value.ID, expected)
	if err != nil {
		return mapError(err)
	}
	if err = requireAffected(result, true); err != nil {
		return err
	}
	if current.BackendNetwork != value.BackendNetwork {
		if _, err = tx.ExecContext(ctx, s.bind(`UPDATE runtimes SET revision=revision+1,updated_at=:updated_at WHERE pool_id=:pool_id`), value.UpdatedAt, value.ID); err != nil {
			return err
		}
	}
	if effectiveChanged {
		if _, err = bumpPoolRevisionTx(ctx, s, tx, value.ID, value.UpdatedAt); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) DeletePool(ctx context.Context, id string, expected int64) error {
	var refs int
	query := s.bind(`SELECT (SELECT COUNT(*) FROM runtimes WHERE pool_id=:pool_id) + (SELECT COUNT(*) FROM domains WHERE pool_id=:pool_id_2) + (SELECT COUNT(*) FROM gateways WHERE pool_id=:pool_id_3)`)
	if err := s.db.QueryRowContext(ctx, query, id, id, id).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		return fmt.Errorf("%w: gateway pool still has bound resources", ErrConflict)
	}
	result, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM gateway_pools WHERE id=:id AND revision=:revision`), id, expected)
	if err != nil {
		return mapError(err)
	}
	return requireAffected(result, true)
}

func (s *SQLStore) BumpPoolRevision(ctx context.Context, id string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	revision, err := bumpPoolRevisionTx(ctx, s, tx, id, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	committed = true
	return revision, nil
}

const runtimeColumns = `id, pool_id, name, runtime_type, endpoint, credential_ref, cluster_id,
enabled, management_enabled, status, observation_epoch, applied_observation, last_seen_at,
last_error, revision, created_at, updated_at`

func scanRuntime(row scanner) (model.Runtime, error) {
	var value model.Runtime
	var seen sql.NullTime
	err := row.Scan(&value.ID, &value.PoolID, &value.Name, &value.Type, &value.Endpoint, &value.CredentialRef,
		&value.ClusterID, &value.Enabled, &value.ManagementEnabled, &value.Status, &value.ObservationEpoch,
		&value.AppliedObservation, &seen, &value.LastError, &value.Revision, &value.CreatedAt, &value.UpdatedAt)
	if seen.Valid {
		value.LastSeenAt = &seen.Time
	}
	return value, mapError(err)
}

func (s *SQLStore) CreateRuntime(ctx context.Context, value *model.Runtime) error {
	if s.dialect == SQLite && value.Type == model.RuntimeSwarm {
		return fmt.Errorf("%w: SQLite supports Compose runtimes only", ErrConflict)
	}
	query := s.bind(`INSERT INTO runtimes
		(id,pool_id,name,runtime_type,endpoint,credential_ref,cluster_id,enabled,management_enabled,status,observation_epoch,applied_observation,last_seen_at,last_error,revision,created_at,updated_at)
		VALUES (:id,:pool_id,:name,:runtime_type,:endpoint,:credential_ref,:cluster_id,:enabled,:management_enabled,:status,:observation_epoch,:applied_observation,:last_seen_at,:last_error,:revision,:created_at,:updated_at)`)
	_, err := s.db.ExecContext(ctx, query, value.ID, value.PoolID, value.Name, value.Type, value.Endpoint,
		value.CredentialRef, value.ClusterID, value.Enabled, value.ManagementEnabled, value.Status,
		value.ObservationEpoch, value.AppliedObservation, ptrTime(value.LastSeenAt), value.LastError,
		value.Revision, value.CreatedAt, value.UpdatedAt)
	return mapError(err)
}

func (s *SQLStore) ListRuntimes(ctx context.Context, page Page) ([]model.Runtime, error) {
	page = page.normalized()
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+runtimeColumns+` FROM runtimes WHERE id > :id ORDER BY id LIMIT :limit`), page.After, page.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]model.Runtime, 0, page.Limit)
	for rows.Next() {
		value, err := scanRuntime(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) GetRuntime(ctx context.Context, id string) (model.Runtime, error) {
	return scanRuntime(s.db.QueryRowContext(ctx, s.bind(`SELECT `+runtimeColumns+` FROM runtimes WHERE id=:id`), id))
}

func (s *SQLStore) UpdateRuntime(ctx context.Context, value *model.Runtime, expected int64) error {
	if s.dialect == SQLite && value.Type == model.RuntimeSwarm {
		return fmt.Errorf("%w: SQLite supports Compose runtimes only", ErrConflict)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	query := `SELECT ` + runtimeColumns + ` FROM runtimes WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	current, err := scanRuntime(tx.QueryRowContext(ctx, s.bind(query), value.ID))
	if err != nil {
		return err
	}
	if current.Type != value.Type || current.ClusterID != "" && current.ClusterID != value.ClusterID {
		return fmt.Errorf("%w: runtime type and established cluster identity are immutable", ErrConflict)
	}
	if current.Endpoint != value.Endpoint || current.CredentialRef != value.CredentialRef {
		var owned, pending int
		if err := tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM applications WHERE runtime_id=:id AND managed=:managed`), value.ID, true).Scan(&owned); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM deployment_operations WHERE runtime_id=:id AND status IN (:queued,:running)`), value.ID, model.OperationQueued, model.OperationRunning).Scan(&pending); err != nil {
			return err
		}
		if owned > 0 || pending > 0 {
			return fmt.Errorf("%w: remove owned applications and pending tasks before rebinding a runtime", ErrConflict)
		}
	}
	if current.PoolID != value.PoolID {
		var count int
		if err := tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM services WHERE runtime_id=:runtime_id AND status <> :status`), value.ID, model.ServiceRemoved).Scan(&count); err != nil {
			return err
		}
		if count > 0 || current.Enabled || value.Enabled {
			return fmt.Errorf("%w: disable and drain a runtime before moving pools", ErrConflict)
		}
	}
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE runtimes SET pool_id=:pool_id,name=:name,runtime_type=:runtime_type,endpoint=:endpoint,credential_ref=:credential_ref,cluster_id=:cluster_id,enabled=:enabled,management_enabled=:management_enabled,status=:status,last_error=:last_error,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`),
		value.PoolID, value.Name, value.Type, value.Endpoint, value.CredentialRef, value.ClusterID,
		value.Enabled, value.ManagementEnabled, value.Status, value.LastError, value.UpdatedAt, value.ID, expected)
	if err != nil {
		return mapError(err)
	}
	if err := requireAffected(result, true); err != nil {
		return err
	}
	if current.Enabled != value.Enabled {
		if _, err = bumpPoolRevisionTx(ctx, s, tx, current.PoolID, value.UpdatedAt); err != nil {
			return err
		}
		if current.PoolID != value.PoolID {
			if _, err = bumpPoolRevisionTx(ctx, s, tx, value.PoolID, value.UpdatedAt); err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) DeleteRuntime(ctx context.Context, id string, expected int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	query := `SELECT ` + runtimeColumns + ` FROM runtimes WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	value, err := scanRuntime(tx.QueryRowContext(ctx, s.bind(query), id))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE runtimes SET enabled=:enabled,status=:status,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`), false, model.RuntimeDisabled, now, id, expected)
	if err != nil {
		return mapError(err)
	}
	if err := requireAffected(result, true); err != nil {
		return err
	}
	if value.Enabled {
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

func (s *SQLStore) NextObservation(ctx context.Context, id string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	query := `SELECT observation_epoch FROM runtimes WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	var epoch int64
	if err := tx.QueryRowContext(ctx, s.bind(query), id).Scan(&epoch); err != nil {
		return 0, mapError(err)
	}
	epoch++
	if _, err := tx.ExecContext(ctx, s.bind(`UPDATE runtimes SET observation_epoch=:observation_epoch,updated_at=:updated_at WHERE id=:id`), epoch, time.Now().UTC(), id); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return epoch, nil
}

func ptrTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func rollbackUnlessCommitted(tx *sql.Tx, committed *bool) {
	if !*committed {
		_ = tx.Rollback()
	}
}
