package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

// The application, operation, environment linkage and variable source versions
// commit together. A failed install cannot leave an environment half linked.
func (s *SQLStore) bindEnvironmentRevision(ctx context.Context, tx *sql.Tx, r *model.ApplicationRevision) error {
	source := r.ConfigurationSource
	if source.UpdatePolicyRevision > 0 {
		query := `SELECT revision FROM control_documents WHERE id=:id AND kind='image-policy'`
		if s.dialect != SQLite {
			query += ` FOR UPDATE`
		}
		var version int64
		if e := tx.QueryRowContext(ctx, s.bind(query), source.EnvironmentID+":image-policy").Scan(&version); e != nil {
			return mapError(e)
		}
		if version != source.UpdatePolicyRevision {
			return ErrPrecondition
		}
	}
	if source.ImageUpdate {
		var count int
		if e := tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM deployment_operations WHERE application_id=:application_id AND status IN (:queued_status,:running_status)`), r.ApplicationID, model.OperationQueued, model.OperationRunning).Scan(&count); e != nil {
			return e
		}
		if count > 0 {
			return ErrConflict
		}
	}
	if source.EnvironmentID == "" || source.EnvironmentRevision == 0 {
		return nil
	}
	var payload, parent string
	var revision int64
	if e := tx.QueryRowContext(ctx, s.bind(`SELECT payload,parent_id,revision FROM control_documents WHERE id=:id AND kind=:kind`), source.EnvironmentID, "environment").Scan(&payload, &parent, &revision); e != nil {
		return mapError(e)
	}
	if revision != source.EnvironmentRevision {
		return ErrPrecondition
	}
	if e := s.lockControlOwner(ctx, tx, parent); e != nil {
		return e
	}
	if e := s.lockControlOwner(ctx, tx, source.EnvironmentID); e != nil {
		return e
	}
	// Read and lock the reviewed variable layers in the same transaction.
	for _, layer := range []struct {
		key     string
		version int64
	}{{parent + ":variables", source.PublicVariablesVersion}, {source.EnvironmentID + ":variables", source.EnvironmentVariablesVersion}} {
		var current int64
		err := tx.QueryRowContext(ctx, s.bind(`SELECT revision FROM control_documents WHERE id=:id`), layer.key).Scan(&current)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if current != layer.version {
			return ErrPrecondition
		}

	}
	var v map[string]any
	if e := json.Unmarshal([]byte(payload), &v); e != nil {
		return e
	}
	if v == nil {
		return ErrConflict
	}
	previous, _ := v["application_id"].(string)
	if previous != "" && previous != r.ApplicationID {
		return ErrConflict
	}
	v["application_id"] = r.ApplicationID
	v["public_variables_version"] = source.PublicVariablesVersion
	v["environment_variables_version"] = source.EnvironmentVariablesVersion
	encoded, e := json.Marshal(v)
	if e != nil {
		return e
	}
	now := time.Now().UTC()
	result, e := tx.ExecContext(ctx, s.bind(`UPDATE control_documents SET payload=:payload,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`), string(encoded), now, source.EnvironmentID, revision)
	if e != nil {
		return e
	}
	if e = requireAffected(result, true); e != nil {
		return e
	}
	if previous == "" {
		claim, _ := json.Marshal(map[string]string{"environment_id": source.EnvironmentID})
		_, e = tx.ExecContext(ctx, s.bind(`INSERT INTO control_documents(id,kind,parent_id,payload,revision,created_at,updated_at) VALUES(:id,:kind,:parent_id,:payload,1,:revision,:created_at)`), "application-environment:"+r.ApplicationID, "application-environment", r.ApplicationID, string(claim), now, now)
	}
	return mapError(e)
}
