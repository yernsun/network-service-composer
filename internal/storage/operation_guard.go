package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/yernsun/network-service-composer/internal/model"
)

var ErrOperationOwnership = errors.New("deployment operation ownership is no longer current")

type operationGuardKey struct{}
type operationAuthorityKey struct{}

// WithOperationAuthority adds the live term and session checks supplied by the
// authenticated executor. Ordinary API and single-owner SQLite calls do not
// receive this context capability.
func WithOperationAuthority(ctx context.Context, check func(context.Context, *sql.Tx) error) context.Context {
	return context.WithValue(ctx, operationAuthorityKey{}, check)
}
func checkOperationAuthority(ctx context.Context, tx *sql.Tx) error {
	if check, ok := ctx.Value(operationAuthorityKey{}).(func(context.Context, *sql.Tx) error); ok {
		if err := check(ctx, tx); err != nil {
			return errors.Join(ErrOperationOwnership, err)
		}
	}
	return nil
}

type operationGuard struct {
	ID, Worker, Application string
	Attempt                 int
}

// WithDeploymentOperation binds executor writes to the immutable claim attempt.
// A delayed executor must not activate state after the operation is reclaimed,
// even when a restarted instance uses the same configured worker identity.
func WithDeploymentOperation(ctx context.Context, operation model.DeploymentOperation) context.Context {
	return context.WithValue(ctx, operationGuardKey{}, operationGuard{operation.ID, operation.WorkerID, operation.ApplicationID, operation.Attempt})
}

func (s *SQLStore) guardDeploymentOperationTx(ctx context.Context, tx *sql.Tx, applicationID string) error {
	if err := checkOperationAuthority(ctx, tx); err != nil {
		return err
	}
	guard, ok := ctx.Value(operationGuardKey{}).(operationGuard)
	if !ok {
		return nil
	}
	if applicationID == "" {
		applicationID = guard.Application
	}
	query := `SELECT id FROM deployment_operations WHERE id=:id AND worker_id=:worker_id AND attempt=:attempt AND status=:status AND application_id=:application_id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	var found string
	if err := tx.QueryRowContext(ctx, s.bind(query), guard.ID, guard.Worker, guard.Attempt, model.OperationRunning, applicationID).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrOperationOwnership
		}
		return errors.Join(ErrOperationOwnership, err)
	}
	return nil
}
