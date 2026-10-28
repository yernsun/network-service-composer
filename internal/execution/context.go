package execution

import (
	"context"
	"database/sql"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type contextKey struct{}
type boundExecution struct {
	verifier *Verifier
	record   Record
}

// WithRecord is installed by the operation endpoint after authenticating the
// current Leader. Resource managers use the same claim and collector fences.
func WithRecord(ctx context.Context, v *Verifier, record Record) context.Context {
	ctx = context.WithValue(ctx, contextKey{}, boundExecution{v, record})
	if store, ok := v.Store.(*storage.SQLStore); ok && store.Dialect() != storage.SQLite {
		return storage.WithOperationAuthority(ctx, func(check context.Context, tx *sql.Tx) error { return v.CheckStorageAuthority(check, tx, record) })
	}
	return ctx
}
func CheckContext(ctx context.Context) error {
	bound, ok := ctx.Value(contextKey{}).(boundExecution)
	if !ok || bound.verifier == nil {
		return ErrFenced
	}
	return bound.verifier.CheckExecutor(ctx, bound.record)
}
func BoundTarget(ctx context.Context, operation, runtime string) (Target, bool, error) {
	bound, ok := ctx.Value(contextKey{}).(boundExecution)
	if !ok {
		return Target{}, false, nil
	}
	if bound.record.Reference.OperationID != operation || bound.record.Reference.RuntimeID != runtime {
		return Target{}, true, ErrFenced
	}
	if err := bound.verifier.CheckExecutor(ctx, bound.record); err != nil {
		return Target{}, true, err
	}
	return bound.record.Reference.Target, true, nil
}

// BeforeMutation is called at the actual SDK write, after read-only ownership
// and consumer checks. Legacy direct-engine paths have no distributed context.
func BeforeMutation(ctx context.Context, operation, runtime string) error {
	_, bound, err := BoundTarget(ctx, operation, runtime)
	if err != nil {
		return err
	}
	if bound {
		return MarkCommand(ctx)
	}
	return nil
}

func MutationStarted(ctx context.Context) (bool, bool, error) {
	bound, ok := ctx.Value(contextKey{}).(boundExecution)
	if !ok {
		return false, false, nil
	}
	record, err := Load(ctx, bound.verifier.Store, bound.record.Reference.OperationID, bound.record.Reference.Attempt)
	if err != nil {
		return true, true, err
	}
	return record.CommandStartedAt != nil, true, nil
}
