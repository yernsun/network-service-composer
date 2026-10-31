package install

import (
	"context"
	"fmt"
	"net/url"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

// Offline evidence is a registered identity, never a fresh node observation.
// Authentication/host-key errors must not enter this fallback.
func (i *Installer) offlineIdentity(ctx context.Context, node bootstrap.Node) (NodeInfo, error) {
	var result NodeInfo
	cfg, err := i.localConfig(ctx)
	if err != nil {
		return result, err
	}
	for _, binding := range cfg.Controller.Nodes {
		endpoint, err := url.Parse(binding.URL)
		if err != nil || endpoint.Hostname() != node.Address {
			continue
		}
		if result.ID != "" || binding.EngineID == "" || binding.NodeID == "" || binding.ClusterID != i.Config.Adoption.ClusterID {
			return NodeInfo{}, fmt.Errorf("offline node registration is ambiguous or incomplete")
		}
		result.ID = binding.EngineID
		result.Swarm.NodeID = binding.NodeID
		result.Swarm.LocalNodeState = "active"
		result.Swarm.ControlAvailable = node.Manager
	}
	if result.ID == "" {
		return result, fmt.Errorf("offline node has no pre-existing registered identity")
	}
	return result, nil
}
