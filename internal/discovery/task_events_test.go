package discovery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type taskEventStore struct {
	storage.Store
	failure error
	batches [][]model.DiscoverySignal
}

func (s *taskEventStore) RecordRuntimeEvents(_ context.Context, _ model.RuntimeEventSource, signals []model.DiscoverySignal, _ time.Time) error {
	if s.failure != nil {
		return s.failure
	}
	s.batches = append(s.batches, append([]model.DiscoverySignal(nil), signals...))
	return nil
}

func TestSwarmTaskChangesUseObservedStatusAndRetryFailedPersistence(t *testing.T) {
	store := &taskEventStore{}
	manager := &Manager{store: store, tasks: map[string]map[string]observedTask{}}
	runtime := model.Runtime{ID: "runtime-a", Type: model.RuntimeSwarm}
	at := time.Now().UTC()
	snapshot := func(observed time.Time, endpoints ...model.DiscoveredEndpoint) model.RuntimeSnapshot {
		return model.RuntimeSnapshot{RuntimeID: runtime.ID, ObservedAt: observed,
			Services: []model.DiscoveredService{{ServiceKey: "demoapp_api", RuntimeObjectID: "docker-service-a", Endpoints: endpoints}}}
	}
	endpoint := func(id, node, state string, stamp time.Time) model.DiscoveredEndpoint {
		return model.DiscoveredEndpoint{ExternalID: id, NodeID: node, TaskState: state, TaskStatusAt: &stamp}
	}
	initial := snapshot(at, endpoint("task-a", "worker-a", "running", at))
	if err := manager.recordTaskChanges(t.Context(), runtime, initial); err != nil || len(store.batches) != 1 || len(store.batches[0]) != 0 {
		t.Fatalf("initial inventory fabricated task history: %#v %v", store.batches, err)
	}
	replacement := snapshot(at.Add(2*time.Second), endpoint("task-a", "worker-a", "failed", at.Add(time.Second)),
		endpoint("task-b", "worker-b", "running", at.Add(2*time.Second)))
	store.failure = errors.New("database unavailable")
	if err := manager.recordTaskChanges(t.Context(), runtime, replacement); !errors.Is(err, store.failure) {
		t.Fatalf("failed task event write was acknowledged: %v", err)
	}
	store.failure = nil
	if err := manager.recordTaskChanges(t.Context(), runtime, replacement); err != nil {
		t.Fatal(err)
	}
	if len(store.batches) != 2 || len(store.batches[1]) != 2 || store.batches[1][0].Kind != "update" ||
		store.batches[1][0].Status != "failed" || store.batches[1][0].ServiceObjectID != "docker-service-a" ||
		store.batches[1][1].Kind != "create" || store.batches[1][1].NodeID != "worker-b" ||
		!store.batches[1][1].At.Equal(at.Add(2*time.Second)) {
		t.Fatalf("task state/replacement evidence was lost: %#v", store.batches)
	}
	if err := manager.recordTaskChanges(t.Context(), runtime, replacement); err != nil || len(store.batches[2]) != 0 {
		t.Fatalf("unchanged inventory repeated events: %#v %v", store.batches, err)
	}
	removed := snapshot(at.Add(3*time.Second), endpoint("task-b", "worker-b", "running", at.Add(2*time.Second)))
	if err := manager.recordTaskChanges(t.Context(), runtime, removed); err != nil || len(store.batches[3]) != 1 ||
		store.batches[3][0].Kind != "remove" || store.batches[3][0].ExternalID != "task-a" ||
		!store.batches[3][0].At.Equal(removed.ObservedAt) {
		t.Fatalf("complete inventory did not record task disappearance: %#v %v", store.batches, err)
	}
}

func TestSwarmTaskWithoutStatusTimestampDoesNotRepeatUnchangedState(t *testing.T) {
	store := &taskEventStore{}
	manager := &Manager{store: store, tasks: map[string]map[string]observedTask{}}
	runtime := model.Runtime{ID: "runtime-a", Type: model.RuntimeSwarm}
	now := time.Now().UTC()
	for _, observedAt := range []time.Time{now, now.Add(time.Second)} {
		snapshot := model.RuntimeSnapshot{RuntimeID: runtime.ID, ObservedAt: observedAt,
			Services: []model.DiscoveredService{{ServiceKey: "demoapp_api", RuntimeObjectID: "docker-service-a",
				Endpoints: []model.DiscoveredEndpoint{{ExternalID: "task-pending", TaskState: "pending"}}}}}
		if err := manager.recordTaskChanges(t.Context(), runtime, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.batches) != 2 || len(store.batches[0]) != 0 || len(store.batches[1]) != 0 {
		t.Fatalf("unchanged task without status timestamp repeated: %#v", store.batches)
	}
}
