package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

const RecoveryBarrierID = "recovery:barrier"

// EncryptedControlKinds is shared by key rotation and recovery validation.
func EncryptedControlKinds() []string {
	return []string{"variables", "variable-version", "environment-compose", "environment-review", "release-snapshot", "application-restore", "application-source-draft", "application-draft-receipt", "node-report", "hook", "dns-provider-profile", "notification-settings", "notification-receiver"}
}

func RecoveryHoldID(kind, id string) string { return "recovery-hold:" + kind + ":" + id }

func (s *SQLStore) concat(left, right string) string {
	if s.dialect == MySQL {
		return "CONCAT(" + left + "," + right + ")"
	}
	return left + " || " + right
}

type recoveryQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// RecoveryFingerprint binds review to the restored resource and encrypted
// input state. Recovery bookkeeping is excluded; ordinary records are not.
func RecoveryFingerprint(ctx context.Context, queryer recoveryQueryer) (string, error) {
	digest := sha256.New()
	for _, table := range []string{"gateway_pools", "gateways", "runtimes", "services", "service_instances", "domains", "routes", "certificates", "certificate_domains", "acme_challenges", "applications", "application_revisions", "deployment_operations", "acme_accounts", "catalog_sources", "catalog_app_versions", "pool_snapshots", "audit_events", "service_dns_probes", "runtime_event_sources", "runtime_events", "docker_resources", "control_documents"} {
		query := "SELECT * FROM " + table
		if table == "control_documents" {
			query += " WHERE id NOT LIKE 'recovery:%' AND id NOT LIKE 'recovery-hold:%'"
		}
		order := "id"
		switch table {
		case "certificate_domains":
			order = "certificate_id,domain_id"
		case "service_dns_probes":
			order = "service_id"
		case "docker_resources":
			order = "cluster_id,kind,docker_id"
		}
		rows, err := queryer.QueryContext(ctx, query+" ORDER BY "+order)
		if err != nil {
			return "", fmt.Errorf("inspect recovery table %s: %w", table, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return "", err
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err = rows.Scan(pointers...); err != nil {
				rows.Close()
				return "", err
			}
			for i, value := range values {
				switch value := value.(type) {
				case []byte:
					values[i] = []any{"bytes", hex.EncodeToString(value)}
				case time.Time:
					values[i] = value.UTC().Format(time.RFC3339Nano)
				}
			}
			// SQL scalar values have JSON representations. The table and column
			// identities keep an equal value in another resource unambiguous.
			encoded, err := json.Marshal([]any{table, columns, values})
			if err != nil {
				rows.Close()
				return "", err
			}
			digest.Write(encoded)
			digest.Write([]byte{'\n'})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// ApplyRecoveryReview atomically guards the baseline, removes only explicitly
// approved holds and archives the review before allowing normal startup.
func (s *SQLStore) ApplyRecoveryReview(ctx context.Context, expected int64, fingerprint string, holdIDs []string, review json.RawMessage, writes ...ControlWrite) error {
	return s.ApplyRecoveryReviewWithIdentities(ctx, expected, fingerprint, holdIDs, review, nil, writes...)
}

type ReviewedExecutionIdentity struct {
	OperationID      string
	ExpectedRevision int64
	Identity         model.RuntimeExecutionIdentity
}

func (s *SQLStore) ApplyRecoveryReviewWithIdentities(ctx context.Context, expected int64, fingerprint string, holdIDs []string, review json.RawMessage, identities []ReviewedExecutionIdentity, writes ...ControlWrite) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	current, err := RecoveryFingerprint(ctx, tx)
	if err != nil {
		return err
	}
	if current != fingerprint {
		return ErrPrecondition
	}
	var revision int64
	if err = tx.QueryRowContext(ctx, s.bind("SELECT revision FROM control_documents WHERE id=:id AND kind='recovery-barrier'"), RecoveryBarrierID).Scan(&revision); err != nil {
		return mapError(err)
	}
	if revision != expected {
		return ErrPrecondition
	}
	for _, reviewed := range identities {
		var runtimeID, previous string
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT runtime_id,runtime_identity FROM deployment_operations WHERE id=:id AND revision=:revision`), reviewed.OperationID, reviewed.ExpectedRevision).Scan(&runtimeID, &previous); err != nil {
			return mapError(err)
		}
		if previous != "" {
			return ErrPrecondition
		}
		// Offline recovery has no running Controller enrollment map. Only the
		// validated receipt supplies missing node/Engine identity; the physical
		// Runtime tuple is still checked inside this same review transaction.
		current := model.RuntimeExecutionIdentity{}
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT pool_id,runtime_type,endpoint,credential_ref,cluster_id FROM runtimes WHERE id=:id`), runtimeID).Scan(&current.PoolID, &current.Type, &current.Endpoint, &current.CredentialRef, &current.ClusterID); err != nil {
			return err
		}
		current.NodeID, current.EngineID = reviewed.Identity.NodeID, reviewed.Identity.EngineID
		if current.Endpoint == "" || current != reviewed.Identity {
			return ErrPrecondition
		}
		payload, err := json.Marshal(reviewed.Identity)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, s.bind(`UPDATE deployment_operations SET runtime_identity=:identity,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision AND runtime_identity=''`), string(payload), time.Now().UTC(), reviewed.OperationID, reviewed.ExpectedRevision)
		if err != nil {
			return err
		}
		if err = requireAffected(result, true); err != nil {
			return err
		}
	}
	for _, key := range holdIDs {
		if !strings.HasPrefix(key, "recovery-hold:") {
			return ErrConflict
		}
		result, err := tx.ExecContext(ctx, s.bind("DELETE FROM control_documents WHERE id=:id AND kind='recovery-hold'"), key)
		if err != nil {
			return err
		}
		if err = requireAffected(result, false); err != nil {
			return err
		}
	}
	if err = s.writeControlTx(ctx, tx, writes); err != nil {
		return err
	}
	now := time.Now().UTC()
	if err = s.writeControlTx(ctx, tx, []ControlWrite{{Document: ControlDocument{ID: "recovery:review:" + now.Format("20060102T150405.000000000Z"), Kind: "recovery-review", Payload: review, CreatedAt: now, UpdatedAt: now}}}); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, s.bind("DELETE FROM control_documents WHERE id=:id AND revision=:revision AND kind='recovery-barrier'"), RecoveryBarrierID, expected)
	if err != nil {
		return err
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

// RecoveryHeld is checked at executor boundaries as well as SQL queue claims.
// An unavailable store cannot establish that an operation is safe to execute.
func RecoveryHeld(ctx context.Context, store Store, kind, id string) (bool, error) {
	_, err := store.GetControlDocument(ctx, RecoveryBarrierID)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	_, err = store.GetControlDocument(ctx, RecoveryHoldID(kind, id))
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// RecoveryPrepared also supports a fresh database before its first migration.
// Checking an existing restore happens before any startup migration or seed.
func (s *SQLStore) RecoveryPrepared(ctx context.Context) (bool, error) {
	query := "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='control_documents'"
	switch s.dialect {
	case Postgres:
		query = "SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='control_documents'"
	case MySQL:
		query = "SELECT count(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='control_documents'"
	}
	var count int
	if err := s.db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect recovery barrier: %w", err)
	}
	if count == 0 {
		return false, nil
	}
	_, err := s.GetControlDocument(ctx, RecoveryBarrierID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}
