package deployment

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestNodeSwarmCommandsRequireCurrentManagerIdentityAndQuorum(t *testing.T) {
	for _, scenario := range []string{"available", "drained-voter", "worker", "missing-cluster", "foreign-cluster", "missing-enrolled-cluster", "list-unavailable", "list-empty", "inspect-unavailable", "invalid-inventory", "lost-majority"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Controller.InstanceID = "controller"
			cfg.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: "other-controller", NodeID: "other-node", EngineID: "other-engine", ClusterID: "cluster"}, {InstanceID: "controller", NodeID: "node", EngineID: "engine", ClusterID: "cluster"}}
			rt := model.Runtime{Type: model.RuntimeSwarm, Endpoint: "node://node", ClusterID: "cluster"}
			info := system.Info{ID: "engine", Swarm: swarm.Info{NodeID: "node", ControlAvailable: true, Managers: 3, Cluster: &swarm.ClusterInfo{ID: "cluster"}}}
			nodes := []swarm.Node{}
			for i, id := range []string{"node", "second", "third"} {
				nodes = append(nodes, swarm.Node{ID: id, Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager, Availability: swarm.NodeAvailabilityActive}, ManagerStatus: &swarm.ManagerStatus{Leader: i == 0, Reachability: swarm.ReachabilityReachable}})
			}
			switch scenario {
			case "drained-voter":
				nodes[1].Spec.Availability = swarm.NodeAvailabilityDrain
			case "worker":
				info.Swarm.ControlAvailable = false
			case "missing-cluster":
				info.Swarm.Cluster = nil
			case "foreign-cluster":
				info.Swarm.Cluster.ID = "foreign"
			case "missing-enrolled-cluster":
				cfg.Controller.Nodes[1].ClusterID = ""
			case "lost-majority":
				nodes[1].ManagerStatus.Reachability = swarm.ReachabilityUnreachable
				nodes[2].ManagerStatus.Reachability = swarm.ReachabilityUnreachable
			}
			runner := &recordingRunner{run: func(call runnerCall) (string, error) {
				command := strings.Join(call.arguments, " ")
				var value any
				switch {
				case strings.Contains(command, " info --format "):
					value = info
				case strings.Contains(command, " node ls "):
					if scenario == "list-unavailable" {
						return "private diagnostic", errors.New("unavailable")
					}
					if scenario == "list-empty" {
						return "", nil
					}
					return "node\nsecond\nthird", nil
				case strings.Contains(command, " node inspect "):
					if scenario == "inspect-unavailable" {
						return "private diagnostic", errors.New("unavailable")
					}
					if scenario == "invalid-inventory" {
						return "{", nil
					}
					value = nodes
				default:
					t.Error("identity check attempted a mutating or unexpected command", command)
					return "", errors.New("unexpected command")
				}
				raw, err := json.Marshal(value)
				return string(raw), err
			}}
			w := NewWorkerWithRunner(nil, nil, nil, cfg, nil, runner)
			err := w.verifyExecutionNode(t.Context(), rt)
			if (err == nil) != (scenario == "available" || scenario == "drained-voter") {
				t.Fatal("wrong management evidence decision", scenario, err)
			}
			if err != nil && strings.Contains(err.Error(), "private diagnostic") {
				t.Fatal("raw Engine diagnostic escaped evidence boundary")
			}
		})
	}
}
