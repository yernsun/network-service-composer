package maintenance

import (
	"context"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// SchemaVerifier is read-only. A prepared recovery database cannot be used to
// restart old writers or upgrade the control plane before its separate release.
func SchemaVerifier(store storage.Store) func(context.Context) error {
	return func(ctx context.Context) error {
		schema, ok := store.(interface {
			ValidateRecoverySchema(context.Context) error
			RecoveryPrepared(context.Context) (bool, error)
		})
		if !ok {
			return &bootstrap.MaintenanceError{Code: "schema_verification_unavailable"}
		}
		prepared, err := schema.RecoveryPrepared(ctx)
		if err != nil {
			return err
		}
		if prepared {
			return &bootstrap.MaintenanceError{Code: "recovery_barrier_active"}
		}
		return schema.ValidateRecoverySchema(ctx)
	}
}
