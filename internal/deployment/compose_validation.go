package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/api/types/swarm"
	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/cluster"
	"github.com/yernsun/network-service-composer/internal/composecheck"
	"github.com/yernsun/network-service-composer/internal/model"
)

type composeAdmissionError struct{ cause error }

func (e *composeAdmissionError) Error() string { return e.cause.Error() }
func (e *composeAdmissionError) Unwrap() error { return e.cause }

func (w *Worker) validateComposePlacement(ctx context.Context, rt model.Runtime, value material, overrides ...map[string]int) error {
	options := composecheck.Options{Swarm: rt.Type == model.RuntimeSwarm, Plan: value.revision.ConfigurationSource.DeploymentPlan}
	options.ReplicasOverride = map[string]int{}
	for name, replicas := range value.application.ReplicaOverrides {
		options.ReplicasOverride[name] = replicas
	}
	if len(overrides) > 0 {
		for name, replicas := range overrides[0] {
			options.ReplicasOverride[name] = replicas
		}
	}
	if err := applicationx.ValidateDataNodePolicy(value.resolved, options.Plan.DataNodeID, options.ReplicasOverride); err != nil {
		return err
	}
	if options.Swarm && composecheck.NeedsNodes(value.resolved) {
		call, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		args := w.dockerArgs(rt)
		ids, err := w.runner.Run(call, ".", nil, append(args, "node", "ls", "-q"))
		if err != nil {
			return fmt.Errorf("SWARM_NODE_CHECK_UNAVAILABLE")
		}
		if len(strings.Fields(ids)) > 0 {
			raw, err := w.runner.Run(call, ".", nil, append(append(args, "node", "inspect"), strings.Fields(ids)...))
			if err != nil {
				return fmt.Errorf("SWARM_NODE_CHECK_UNAVAILABLE")
			}
			var nodes []swarm.Node
			if json.Unmarshal([]byte(raw), &nodes) != nil {
				return fmt.Errorf("SWARM_NODE_CHECK_UNAVAILABLE")
			}
			options.Nodes = cluster.PlacementNodeValues(nodes)
		}
		options.ClusterChecked = true
	}
	if options.Swarm && options.Plan.DataNodeID != "" {
		names, err := applicationx.ExternalNetworkNames(value.resolved)
		if err != nil {
			return err
		}
		for _, name := range names {
			raw, err := w.runner.Run(ctx, ".", nil, append(w.dockerArgs(rt), "network", "inspect", name, "--format", "{{.Driver}} {{.Scope}}"))
			if err != nil || strings.TrimSpace(raw) != "overlay swarm" {
				return fmt.Errorf("external database network must be an existing Swarm overlay network")
			}
		}
	}

	return composecheck.Analyze(value.resolved, options).Error()
}
