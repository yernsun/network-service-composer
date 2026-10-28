package storage

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/yernsun/network-service-composer/internal/model"
)

// Resolve associations in bounded batches rather than querying once per row.
func (s *SQLStore) ServiceEnvironments(ctx context.Context, ids []string) (map[string]model.EnvironmentReference, error) {
	result := map[string]model.EnvironmentReference{}
	if len(ids) == 0 {
		return result, nil
	}
	args := []any{}
	for _, key := range ids {
		args = append(args, key)
	}
	rows, e := s.db.QueryContext(ctx, s.bind(`SELECT s.id,COALESCE(sd.payload,d.payload) FROM services s LEFT JOIN control_documents sd ON sd.id=s.runtime_object_id AND sd.parent_id=s.runtime_id AND sd.kind='system-service' LEFT JOIN control_documents d ON d.parent_id=s.application_id AND d.kind='application-environment' WHERE s.id IN (`+namedPlaceholders("service_id", len(ids))+`)`), args...)
	if e != nil {
		return nil, e
	}
	serviceEnvs := map[string]string{}
	envs := map[string]bool{}
	for rows.Next() {
		var key string
		var payload sql.NullString
		if e = rows.Scan(&key, &payload); e != nil {
			rows.Close()
			return nil, e
		}
		if payload.Valid {
			var claim struct {
				EnvironmentID string `json:"environment_id"`
			}
			if e = json.Unmarshal([]byte(payload.String), &claim); e != nil {
				rows.Close()
				return nil, e
			}
			serviceEnvs[key] = claim.EnvironmentID
			envs[claim.EnvironmentID] = true
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if len(envs) == 0 {
		return result, nil
	}
	args = nil
	for key := range envs {
		args = append(args, key)
	}
	rows, e = s.db.QueryContext(ctx, s.bind(`SELECT id,payload FROM control_documents WHERE kind='environment' AND id IN (`+namedPlaceholders("document_id", len(args))+`)`), args...)
	if e != nil {
		return nil, e
	}
	refs := map[string]model.EnvironmentReference{}
	for rows.Next() {
		var key, payload string
		if e = rows.Scan(&key, &payload); e != nil {
			rows.Close()
			return nil, e
		}
		var v struct {
			System      bool   `json:"system"`
			Name        string `json:"name"`
			ProjectID   string `json:"project_id"`
			ProjectName string `json:"project_name"`
		}
		if e = json.Unmarshal([]byte(payload), &v); e != nil {
			rows.Close()
			return nil, e
		}
		refs[key] = model.EnvironmentReference{System: v.System, ID: key, Name: v.Name, ProjectID: v.ProjectID, ProjectName: v.ProjectName}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for key, env := range serviceEnvs {
		if ref, ok := refs[env]; ok {
			result[key] = ref
		}
	}
	return result, nil
}
