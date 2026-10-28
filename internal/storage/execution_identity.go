package storage

import (
	"context"
	"database/sql"
	"strings"

	"github.com/yernsun/network-service-composer/internal/model"
)

// ConfigureOperationNodes installs immutable operator enrollments for future
// queue admissions. Existing operation snapshots are never rewritten.
func (s *SQLStore) ConfigureOperationNodes(nodes map[string]model.RuntimeExecutionIdentity) {
	s.executionMu.Lock()
	defer s.executionMu.Unlock()
	s.executionNodes = make(map[string]model.RuntimeExecutionIdentity, len(nodes))
	for endpoint, node := range nodes {
		s.executionNodes[endpoint] = node
	}
}

func (s *SQLStore) captureOperationIdentity(ctx context.Context, tx *sql.Tx, op *model.DeploymentOperation) error {
	query := `SELECT pool_id,runtime_type,endpoint,credential_ref,cluster_id FROM runtimes WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	var target model.RuntimeExecutionIdentity
	if err := tx.QueryRowContext(ctx, s.bind(query), op.RuntimeID).Scan(&target.PoolID, &target.Type, &target.Endpoint, &target.CredentialRef, &target.ClusterID); err != nil {
		return mapError(err)
	}
	s.executionMu.RLock()
	node, ok := s.executionNodes[target.Endpoint]
	s.executionMu.RUnlock()
	if strings.HasPrefix(target.Endpoint, "node://") && (!ok || node.NodeID != strings.TrimPrefix(target.Endpoint, "node://") || node.EngineID == "" || target.Type == model.RuntimeSwarm && target.ClusterID == "") {
		return ErrConflict
	}
	if ok {
		target.NodeID, target.EngineID = node.NodeID, node.EngineID
	}
	op.ExecutionIdentity = target
	return nil
}
