package maintenance

import (
	"context"
	"errors"
	"testing"

	"github.com/yernsun/network-service-composer/internal/storage"
)

type schemaStateStore struct {
	storage.Store
	prepared            bool
	readErr, versionErr error
}

func (s schemaStateStore) RecoveryPrepared(context.Context) (bool, error) {
	return s.prepared, s.readErr
}
func (s schemaStateStore) ValidateRecoverySchema(context.Context) error { return s.versionErr }
func TestSchemaCompatibilityCannotReleaseRecoveryOrMigrateImplicitly(t *testing.T) {
	for _, scenario := range []string{"ready", "prepared", "unreadable", "incompatible", "unsupported"} {
		t.Run(scenario, func(t *testing.T) {
			var store storage.Store = schemaStateStore{}
			switch scenario {
			case "prepared":
				store = schemaStateStore{prepared: true}
			case "unreadable":
				store = schemaStateStore{readErr: errors.New("unavailable")}
			case "incompatible":
				store = schemaStateStore{versionErr: errors.New("unknown schema")}
			case "unsupported":
				store = struct{ storage.Store }{}
			}
			err := SchemaVerifier(store)(t.Context())
			if (err == nil) != (scenario == "ready") {
				t.Fatal(scenario, err)
			}
		})
	}
}
