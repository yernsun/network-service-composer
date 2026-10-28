package nodecontrol

import (
	"context"
	"sort"
	"strings"

	"github.com/yernsun/network-service-composer/internal/model"
)

// ManagerBinding selects a read-only inventory location. It does not grant
// execution authority or require the management switch to be enabled.
func (s Reports) ManagerBinding(ctx context.Context, rt model.Runtime) (Binding, error) {
	original := strings.TrimPrefix(rt.Endpoint, "node://")
	if original == rt.Endpoint || original == "" || rt.Type != model.RuntimeSwarm || rt.ClusterID == "" {
		return Binding{}, ErrIdentity
	}
	bindings := []Binding{}
	enrolled := false
	for _, b := range s.Bindings {
		if b.NodeID == original && b.ClusterID == rt.ClusterID {
			enrolled = true
		}
		if b.ClusterID == rt.ClusterID {
			bindings = append(bindings, b)
		}
	}
	if !enrolled {
		return Binding{}, ErrIdentity
	}
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].NodeID == original {
			return true
		}
		if bindings[j].NodeID == original {
			return false
		}
		return bindings[i].NodeID < bindings[j].NodeID
	})
	for _, b := range bindings {
		r, err := s.ReadLive(ctx, b.NodeID)
		if err != nil {
			continue
		}
		info := r.Observation.Info
		if info.ID == b.EngineID && info.Swarm.NodeID == b.NodeID && info.Swarm.ControlAvailable && info.Swarm.Cluster != nil && info.Swarm.Cluster.ID == rt.ClusterID {
			return b, nil
		}
	}
	return Binding{}, ErrIncomplete
}
