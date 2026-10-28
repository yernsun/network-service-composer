package nodecontrol

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/hoststats"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
)

var ErrExecutionRequired = errors.New("node mutation requires an authorized durable execution task")

// Client is one immutable observation. Callers create it for each refresh, so
// service/task/container reads cannot mix generations midway through a snapshot.
type Client struct {
	Value       Observation
	unavailable error
}

var _ engine.MonitoringClient = (*Client)(nil)

func (s Reports) Client(ctx context.Context, rt model.Runtime) (*Client, error) {
	node := strings.TrimPrefix(rt.Endpoint, "node://")
	if node == rt.Endpoint || node == "" || strings.ContainsAny(node, "/?#@:") {
		return nil, ErrIdentity
	}
	var target Binding
	for _, b := range s.Bindings {
		if b.NodeID == node {
			target = b
			break
		}
	}
	if target.InstanceID == "" {
		return nil, ErrIdentity
	}
	if rt.Type == model.RuntimeSwarm {
		if target.ClusterID == "" {
			return nil, ErrIdentity
		}
		ids := []string{}
		for _, b := range s.Bindings {
			if b.ClusterID == target.ClusterID {
				ids = append(ids, b.NodeID)
			}
		}
		sort.Strings(ids)
		var best *Client
		var retained *Client
		for _, id := range ids {
			r, err := s.Read(ctx, id)
			if err != nil && !errors.Is(err, ErrStale) && !errors.Is(err, ErrIncomplete) {
				continue
			}
			info := r.Observation.Info.Swarm
			if info.ControlAvailable && info.Cluster != nil && info.Cluster.ID == target.ClusterID {
				if err == nil && (best == nil || r.Observation.At.After(best.Value.At)) {
					best = &Client{Value: r.Observation}
				}
				if retained == nil || r.Observation.At.After(retained.Value.At) {
					retained = &Client{Value: r.Observation, unavailable: err}
				}
			}
		}
		if best == nil {
			return retained, ErrIncomplete
		}
		return best, nil
	}
	r, err := s.Read(ctx, node)
	if err != nil {
		if errors.Is(err, ErrStale) || errors.Is(err, ErrIncomplete) {
			return &Client{Value: r.Observation, unavailable: err}, err
		}
		return nil, err
	}
	return &Client{Value: r.Observation}, nil
}

// MonitoringClient may display retained inventory, explicitly marked unavailable.
// Discovery and execution must use Client and reject its error instead.
func (s Reports) MonitoringClient(ctx context.Context, rt model.Runtime) (*Client, error) {
	client, err := s.Client(ctx, rt)
	if client != nil && (errors.Is(err, ErrStale) || errors.Is(err, ErrIncomplete)) {
		return client, nil
	}
	return client, err
}

func (c *Client) ObservationError() error { return c.unavailable }

func (c *Client) HostObservation() (*hoststats.Metrics, *hoststats.Health) {
	var host *hoststats.Metrics
	var health *hoststats.Health
	if c.Value.Host != nil {
		value := *c.Value.Host
		value.Stale = value.Stale || errors.Is(c.unavailable, ErrStale)
		host = &value
	}
	if c.Value.Health != nil {
		value := *c.Value.Health
		value.Stale = value.Stale || errors.Is(c.unavailable, ErrStale)
		health = &value
	}
	return host, health
}

func (c *Client) ObservedAt() time.Time                     { return c.Value.At }
func (c *Client) Info(context.Context) (system.Info, error) { return c.Value.Info, nil }
func (c *Client) ContainerList(context.Context) ([]container.Summary, error) {
	return c.Value.Containers, nil
}
func (c *Client) NodeList(context.Context) ([]swarm.Node, error)       { return c.Value.Nodes, nil }
func (c *Client) ServiceList(context.Context) ([]swarm.Service, error) { return c.Value.Services, nil }
func (c *Client) TaskList(context.Context) ([]swarm.Task, error)       { return c.Value.Tasks, nil }
func (c *Client) NetworkList(context.Context) ([]network.Summary, error) {
	return c.Value.Networks, nil
}
func (c *Client) ContainerInspect(_ context.Context, id string) (container.InspectResponse, error) {
	v, ok := c.Value.Inspections[id]
	if !ok || c.Value.Errors["inspect:"+id] != "" {
		return container.InspectResponse{}, ErrIncomplete
	}
	return v, nil
}
func (c *Client) ContainerStats(_ context.Context, id string) (container.StatsResponse, error) {
	if c.unavailable != nil {
		return container.StatsResponse{}, c.unavailable
	}
	v, ok := c.Value.Stats[id]
	if !ok || c.Value.Errors["stats:"+id] != "" {
		return container.StatsResponse{}, ErrIncomplete
	}
	return v, nil
}
func (c *Client) NodeInspect(_ context.Context, id string) (swarm.Node, error) {
	for _, node := range c.Value.Nodes {
		if node.ID == id {
			return node, nil
		}
	}
	return swarm.Node{}, ErrIncomplete
}
func (c *Client) NodeUpdate(context.Context, string, swarm.Version, swarm.NodeSpec) error {
	return ErrExecutionRequired
}
func (c *Client) Events(ctx context.Context, _ []string) (<-chan events.Message, <-chan error) {
	messages, failures := make(chan events.Message), make(chan error)
	go func() { <-ctx.Done(); close(messages); close(failures) }()
	return messages, failures
}
func (c *Client) Close() error { return nil }
