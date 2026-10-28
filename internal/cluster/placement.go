package cluster

import (
	"context"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/composecheck"
	"github.com/yernsun/network-service-composer/internal/model"
)

// PlacementNodes always reads the target manager. Inventory cache age or nodes
// from another runtime must never authorize a deployment.
func (m *Monitor) PlacementNodes(ctx context.Context, runtimeID string) ([]composecheck.Node, error) {
	rt, err := m.store.GetRuntime(ctx, runtimeID)
	if err != nil {
		return nil, err
	}
	if !rt.Enabled || rt.Type != model.RuntimeSwarm {
		return nil, ErrUnavailable
	}
	cli, err := m.client(rt)
	if err != nil {
		return nil, err
	}
	defer cli.Close()
	call, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	info, err := cli.Info(call)
	if err != nil {
		return nil, err
	}
	if !info.Swarm.ControlAvailable {
		return nil, ErrUnavailable
	}
	nodes, err := cli.NodeList(call)
	if err != nil {
		return nil, err
	}
	return PlacementNodeValues(nodes), nil
}

func PlacementNodeValues(nodes []swarm.Node) []composecheck.Node {
	result := make([]composecheck.Node, 0, len(nodes))
	for _, n := range nodes {
		labels := map[string]string{}
		for k, v := range n.Description.Engine.Labels {
			labels[k] = v
		}
		result = append(result, composecheck.Node{ID: n.ID, Name: n.Description.Hostname, Role: string(n.Spec.Role), State: string(n.Status.State), Availability: string(n.Spec.Availability), OS: n.Description.Platform.OS, Arch: n.Description.Platform.Architecture, IP: n.Status.Addr, Labels: n.Spec.Labels, EngineLabels: labels, Version: n.Version.Index})
	}
	return result
}
