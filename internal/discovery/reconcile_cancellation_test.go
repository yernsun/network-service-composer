package discovery

import (
	"context"
	"errors"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type interruptedSnapshotStore struct {
	storage.Store
	cancel  context.CancelFunc
	failure error
}

func (s interruptedSnapshotStore) ApplyRuntimeSnapshot(context.Context, model.RuntimeSnapshot) (bool, int64, error) {
	if s.cancel != nil {
		s.cancel()
	}
	return false, 0, s.failure
}

func TestReconcilePreservesLiveStorageFailuresAndNormalizesShutdown(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "live failure", true: "cancelled transaction"}[cancelled], func(t *testing.T) {
			manager, runtime, provider, _ := discoveryBoundaryFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("database transaction interrupted")
			store := interruptedSnapshotStore{Store: manager.store, failure: failure}
			if cancelled {
				store.cancel = cancel
			}
			manager.store = store
			err := manager.reconcile(ctx, runtime, provider, "fixture")
			if cancelled && !errors.Is(err, context.Canceled) || !cancelled && !errors.Is(err, failure) {
				t.Fatal("wrong reconciliation outcome", err)
			}
		})
	}
}
