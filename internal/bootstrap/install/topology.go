package install

import (
	"fmt"
	"sort"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

func managerTopology(nodes []swarm.Node) bootstrap.ManagerTopology {
	result := bootstrap.ManagerTopology{NodeIDs: []string{}}
	for _, node := range nodes {
		if node.Spec.Role != swarm.NodeRoleManager {
			continue
		}
		result.NodeIDs = append(result.NodeIDs, node.ID)
		if node.ManagerStatus != nil && (node.ManagerStatus.Leader || node.ManagerStatus.Reachability == swarm.ReachabilityReachable) {
			result.Reachable++
		}
	}
	sort.Strings(result.NodeIDs)
	result.Managers = len(result.NodeIDs)
	if result.Managers > 0 {
		result.Majority = result.Managers/2 + 1
		result.TolerableFailures = (result.Managers - 1) / 2
	}
	result.QuorumAvailable = result.Managers > 0 && result.Reachable >= result.Majority
	return result
}

func requirePlacement(o Observation, cfg bootstrap.Config) error {
	for _, node := range cfg.Nodes {
		actual := o.Nodes[node.Name]
		if actual.Swarm.LocalNodeState != "active" {
			continue
		}
		member, ok := o.Membership[actual.Swarm.NodeID]
		if !ok || member.Status.State != swarm.NodeStateReady || member.Spec.Availability != swarm.NodeAvailabilityActive {
			return fmt.Errorf("node %s is not Ready/Active; placement is blocked", node.Name)
		}
		if (member.Spec.Role == swarm.NodeRoleManager) != node.Manager {
			return fmt.Errorf("registered and actual node roles differ")
		}
	}
	return nil
}
