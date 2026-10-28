package engine

import (
	"context"
	"encoding/json"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
)

// MonitoringClient extends discovery without granting the monitor container mutations.
type MonitoringClient interface {
	APIClient
	Info(context.Context) (system.Info, error)
	ContainerStats(context.Context, string) (container.StatsResponse, error)
	NodeInspect(context.Context, string) (swarm.Node, error)
	NodeUpdate(context.Context, string, swarm.Version, swarm.NodeSpec) error
}

func (c *sdkClient) Info(ctx context.Context) (system.Info, error) {
	v, err := c.value.Info(ctx, client.InfoOptions{})
	return v.Info, err
}
func (c *sdkClient) ContainerStats(ctx context.Context, id string) (container.StatsResponse, error) {
	v, err := c.value.ContainerStats(ctx, id, client.ContainerStatsOptions{})
	if err != nil {
		return container.StatsResponse{}, err
	}
	defer v.Body.Close()
	var stats container.StatsResponse
	err = json.NewDecoder(v.Body).Decode(&stats)
	return stats, err
}
func (c *sdkClient) NodeInspect(ctx context.Context, id string) (swarm.Node, error) {
	v, err := c.value.NodeInspect(ctx, id, client.NodeInspectOptions{})
	return v.Node, err
}
func (c *sdkClient) NodeUpdate(ctx context.Context, id string, version swarm.Version, spec swarm.NodeSpec) error {
	_, err := c.value.NodeUpdate(ctx, id, client.NodeUpdateOptions{Version: version, Spec: spec})
	return err
}
