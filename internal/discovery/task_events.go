package discovery

import (
	"context"
	"sort"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

// observedTask is a minimal Swarm inventory baseline. It carries no task spec,
// Docker labels, log text, or failure message into persisted event evidence.
type observedTask struct {
	state, serviceKey, serviceObjectID, nodeID string
	statusAt                                   time.Time
}

func allowedTaskState(value string) bool {
	switch value {
	case "new", "allocated", "pending", "assigned", "accepted", "preparing", "ready", "starting",
		"running", "complete", "shutdown", "failed", "rejected", "remove", "orphaned":
		return true
	default:
		return false
	}
}

// Docker's manager event stream does not consistently publish task replacement
// events. A complete Swarm inventory can still prove that a task appeared,
// changed state, or disappeared. The first snapshot establishes a baseline;
// it never invents historical transitions.
func (m *Manager) recordTaskChanges(ctx context.Context, rt model.Runtime, snapshot model.RuntimeSnapshot) error {
	current := make(map[string]observedTask)
	for _, service := range snapshot.Services {
		for _, endpoint := range service.Endpoints {
			if endpoint.ExternalID == "" || !allowedTaskState(endpoint.TaskState) {
				continue
			}
			at := time.Time{}
			if endpoint.TaskStatusAt != nil && !endpoint.TaskStatusAt.IsZero() {
				at = endpoint.TaskStatusAt.UTC()
			}
			current[endpoint.ExternalID] = observedTask{state: endpoint.TaskState, statusAt: at,
				serviceKey: service.ServiceKey, serviceObjectID: service.RuntimeObjectID, nodeID: endpoint.NodeID}
		}
	}
	m.mu.Lock()
	previous, initialized := m.tasks[rt.ID]
	m.mu.Unlock()
	signals := []model.DiscoverySignal{}
	if initialized {
		ids := make([]string, 0, len(current))
		for id := range current {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			value := current[id]
			before, known := previous[id]
			if known && before == value {
				continue
			}
			action := "create"
			if known {
				action = "update"
			}
			occurredAt := value.statusAt
			if occurredAt.IsZero() {
				occurredAt = snapshot.ObservedAt
			}
			signals = append(signals, model.DiscoverySignal{ActorType: "task", ExternalID: id, TaskID: id,
				ServiceKey: value.serviceKey, ServiceObjectID: value.serviceObjectID, NodeID: value.nodeID,
				Kind: action, Status: value.state, At: occurredAt})
		}
		for id, before := range previous {
			if _, exists := current[id]; exists {
				continue
			}
			signals = append(signals, model.DiscoverySignal{ActorType: "task", ExternalID: id, TaskID: id,
				ServiceKey: before.serviceKey, ServiceObjectID: before.serviceObjectID, NodeID: before.nodeID,
				Kind: "remove", At: snapshot.ObservedAt})
		}
	}
	source := model.RuntimeEventSource{ID: "runtime:" + rt.ID, RuntimeID: rt.ID}
	if err := m.store.RecordRuntimeEvents(ctx, source, signals, time.Now().UTC()); err != nil {
		return err
	}
	m.mu.Lock()
	if m.tasks == nil {
		m.tasks = map[string]map[string]observedTask{}
	}
	m.tasks[rt.ID] = current
	m.mu.Unlock()
	return nil
}
