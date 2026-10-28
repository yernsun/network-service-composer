package storage

import (
	"context"
	"github.com/yernsun/network-service-composer/internal/model"
)

// The new child task and receipt advance together. Concurrent or interrupted
// retries cannot leave an unreferenced queued task behind a stale receipt.
func (s *SQLStore) CreateDeploymentOperationWithReceipt(ctx context.Context, operation *model.DeploymentOperation, receipt ControlWrite, expected int64) error {
	if receipt.Document.Kind != "service-entrypoint-intent" || receipt.Document.ParentID != operation.ApplicationID || expected < 1 {
		return ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if err = s.guardProjectResourceTx(ctx, tx, operation.ApplicationID); err != nil {
		return err
	}
	query := `SELECT revision FROM applications WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	var revision int64
	if err = tx.QueryRowContext(ctx, s.bind(query), operation.ApplicationID).Scan(&revision); err != nil {
		return mapError(err)
	}
	if revision != expected {
		return ErrPrecondition
	}
	if err = s.insertOperationWithScopeTx(ctx, tx, operation); err != nil {
		return err
	}
	if err = s.writeControlTx(ctx, tx, []ControlWrite{receipt}); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
