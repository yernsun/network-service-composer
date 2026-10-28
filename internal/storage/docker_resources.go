package storage

import (
	"context"
	"database/sql"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

// DockerResource records NSC ownership evidence only. Docker remains the
// source of truth for existence, content and current Service references.
type DockerResource struct {
	ClusterID         string    `json:"cluster_id"`
	Kind              string    `json:"kind"`
	DockerID          string    `json:"id"`
	Name              string    `json:"name"`
	CreateOperationID string    `json:"create_operation_id"`
	CreatedAt         time.Time `json:"created_at"`
}

const dockerResourceColumns = `cluster_id,kind,docker_id,name,create_operation_id,created_at`

func scanDockerResource(row scanner) (DockerResource, error) {
	var value DockerResource
	err := row.Scan(&value.ClusterID, &value.Kind, &value.DockerID, &value.Name, &value.CreateOperationID, &value.CreatedAt)
	return value, mapError(err)
}

func (s *SQLStore) GetDockerResource(ctx context.Context, clusterID, kind, dockerID string) (DockerResource, error) {
	return scanDockerResource(s.db.QueryRowContext(ctx, s.bind(`SELECT `+dockerResourceColumns+` FROM docker_resources WHERE cluster_id=:cluster_id AND kind=:kind AND docker_id=:docker_id`), clusterID, kind, dockerID))
}

func (s *SQLStore) GetDockerResourceByOperation(ctx context.Context, operationID string) (DockerResource, error) {
	return scanDockerResource(s.db.QueryRowContext(ctx, s.bind(`SELECT `+dockerResourceColumns+` FROM docker_resources WHERE create_operation_id=:create_operation_id`), operationID))
}

func (s *SQLStore) ListDockerResources(ctx context.Context, clusterID, kind string, page Page) ([]DockerResource, error) {
	page = page.normalized()
	query := `SELECT ` + dockerResourceColumns + ` FROM docker_resources WHERE cluster_id=:cluster_id AND docker_id>:docker_id`
	args := []any{clusterID, page.After}
	if kind != "" {
		query += ` AND kind=:kind`
		args = append(args, kind)
	}
	query += ` ORDER BY docker_id LIMIT :limit`
	args = append(args, page.Limit)
	rows, err := s.db.QueryContext(ctx, s.bind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]DockerResource, 0, page.Limit)
	for rows.Next() {
		value, err := scanDockerResource(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) RecordDockerResource(ctx context.Context, value DockerResource) error {
	if value.ClusterID == "" || (value.Kind != "config" && value.Kind != "secret") || value.DockerID == "" || value.Name == "" || value.CreateOperationID == "" {
		return ErrConflict
	}
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO docker_resources (`+dockerResourceColumns+`) VALUES (`+columnPlaceholders(dockerResourceColumns)+`)`),
		value.ClusterID, value.Kind, value.DockerID, value.Name, value.CreateOperationID, value.CreatedAt)
	if err == nil {
		return nil
	}
	if existing, readErr := s.GetDockerResourceByOperation(ctx, value.CreateOperationID); readErr == nil && existing.ClusterID == value.ClusterID && existing.Kind == value.Kind && existing.DockerID == value.DockerID && existing.Name == value.Name {
		return nil
	}
	return mapError(err)
}

func (s *SQLStore) DeleteDockerResource(ctx context.Context, clusterID, kind, dockerID string) error {
	result, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM docker_resources WHERE cluster_id=:cluster_id AND kind=:kind AND docker_id=:docker_id`), clusterID, kind, dockerID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// A queued deployment may still freeze or submit a Compose reference. Until
// it finishes, resource deletion has no complete consumer evidence.
func (s *SQLStore) HasPendingDockerResourceConsumer(ctx context.Context, clusterID, excludeOperationID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM deployment_operations o JOIN runtimes r ON r.id=o.runtime_id
		WHERE r.cluster_id=:cluster_id AND o.id<>:id AND o.operation_type NOT IN ('DOCKER_RESOURCE_CREATE','DOCKER_RESOURCE_DELETE','VOLUME_CREATE','VOLUME_DELETE') AND o.status IN (:queued_status,:running_status)`),
		clusterID, excludeOperationID, model.OperationQueued, model.OperationRunning).Scan(&count)
	return count > 0, err
}
