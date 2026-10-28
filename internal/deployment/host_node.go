package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/model"
)

const localDockerEndpoint = "unix:///var/run/docker.sock"

var errExecutionNode = errors.New("deployment host identity was not verified")

// A node:// runtime is an observation binding, not a remote Docker socket.
// Mutations may only use the local socket after verifying both configured and
// live identity. The selected executor must be on the verified host.
func (w *Worker) verifyExecutionNode(ctx context.Context, runtime model.Runtime) error {
	nodeID := applicationx.RuntimeNodeID(runtime.Endpoint)
	if nodeID == "" {
		return nil
	}
	var bound config.ControllerNodeConfig
	for _, node := range w.config.Controller.Nodes {
		if node.InstanceID == w.config.Controller.InstanceID && node.NodeID == nodeID {
			bound = node
			break
		}
	}
	if bound.EngineID == "" || bound.ClusterID == "" || runtime.CredentialRef != "" {
		return fmt.Errorf("%w: elected Controller must be on the bound host", errExecutionNode)
	}
	check, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := w.runner.Run(check, "", nil, []string{w.config.Management.DockerCLI, "--host", localDockerEndpoint, "info", "--format", "{{json .}}"})
	if err != nil {
		return fmt.Errorf("%w: Docker is unavailable", errExecutionNode)
	}
	var info system.Info
	if json.Unmarshal([]byte(output), &info) != nil || info.ID != bound.EngineID || info.Swarm.NodeID != nodeID || info.Swarm.Cluster != nil && info.Swarm.Cluster.ID != bound.ClusterID {
		return fmt.Errorf("%w: local Docker does not match the bound node", errExecutionNode)
	}
	if runtime.Type == model.RuntimeSwarm {
		if !info.Swarm.ControlAvailable || info.Swarm.Cluster == nil || runtime.ClusterID != info.Swarm.Cluster.ID {
			return fmt.Errorf("%w: Swarm management identity is unavailable", errExecutionNode)
		}
		ids, err := w.runner.Run(check, "", nil, []string{w.config.Management.DockerCLI, "--host", localDockerEndpoint, "node", "ls", "--quiet"})
		if err != nil || len(strings.Fields(ids)) == 0 {
			return fmt.Errorf("%w: Swarm inventory is unavailable", errExecutionNode)
		}
		args := append([]string{w.config.Management.DockerCLI, "--host", localDockerEndpoint, "node", "inspect"}, strings.Fields(ids)...)
		inventory, err := w.runner.Run(check, "", nil, args)
		var nodes []swarm.Node
		if err != nil || json.Unmarshal([]byte(inventory), &nodes) != nil || !execution.Quorum(info, nodes) {
			return fmt.Errorf("%w: Swarm quorum is unavailable", errExecutionNode)
		}
	}
	return nil
}
