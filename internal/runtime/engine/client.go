package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	containertypes "github.com/moby/moby/api/types/container"
	eventtypes "github.com/moby/moby/api/types/events"
	networktypes "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"

	"github.com/yernsun/network-service-composer/internal/model"
	runtimex "github.com/yernsun/network-service-composer/internal/runtime"
)

type TLSFiles struct{ CA, Certificate, Key string }

type APIClient interface {
	ContainerList(context.Context) ([]containertypes.Summary, error)
	ContainerInspect(context.Context, string) (containertypes.InspectResponse, error)
	ServiceList(context.Context) ([]swarm.Service, error)
	TaskList(context.Context) ([]swarm.Task, error)
	NodeList(context.Context) ([]swarm.Node, error)
	NetworkList(context.Context) ([]networktypes.Summary, error)
	Events(context.Context, []string) (<-chan eventtypes.Message, <-chan error)
	Close() error
}

// sdkClient keeps the provider contract deliberately small and shields discovery
// from Moby client option/result churn. It also avoids importing the deprecated
// github.com/docker/docker root module, which contains daemon-only packages.
type sdkClient struct{ value *client.Client }

func (c *sdkClient) ContainerList(ctx context.Context) ([]containertypes.Summary, error) {
	result, err := c.value.ContainerList(ctx, client.ContainerListOptions{All: true})
	return result.Items, err
}

func (c *sdkClient) ContainerInspect(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
	result, err := c.value.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	return result.Container, err
}

func (c *sdkClient) ServiceList(ctx context.Context) ([]swarm.Service, error) {
	result, err := c.value.ServiceList(ctx, client.ServiceListOptions{})
	return result.Items, err
}

func (c *sdkClient) TaskList(ctx context.Context) ([]swarm.Task, error) {
	result, err := c.value.TaskList(ctx, client.TaskListOptions{})
	return result.Items, err
}

func (c *sdkClient) NodeList(ctx context.Context) ([]swarm.Node, error) {
	result, err := c.value.NodeList(ctx, client.NodeListOptions{})
	return result.Items, err
}

func (c *sdkClient) NetworkList(ctx context.Context) ([]networktypes.Summary, error) {
	result, err := c.value.NetworkList(ctx, client.NetworkListOptions{})
	return result.Items, err
}

func (c *sdkClient) Events(ctx context.Context, types []string) (<-chan eventtypes.Message, <-chan error) {
	filters := make(client.Filters).Add("type", types...)
	result := c.value.Events(ctx, client.EventsListOptions{Filters: filters})
	return result.Messages, result.Err
}

func (c *sdkClient) Close() error { return c.value.Close() }

func New(rt model.Runtime, network string, defaults model.ReadinessPolicy, tlsFiles TLSFiles) (runtimex.Provider, error) {
	cli, err := NewMonitoringClient(rt, tlsFiles)
	if err != nil {
		return nil, err
	}
	return NewWithClient(cli, rt, network, defaults), nil
}

func NewMonitoringClient(rt model.Runtime, tlsFiles TLSFiles) (MonitoringClient, error) {
	cli, err := NewManagementClient(rt, tlsFiles)
	if err != nil {
		return nil, err
	}
	return &sdkClient{value: cli}, nil
}

// Management and observation use the same endpoint and TLS validation.
func NewManagementClient(rt model.Runtime, tlsFiles TLSFiles) (*client.Client, error) {
	opts := []client.Opt{client.WithHost(rt.Endpoint), client.WithAPIVersionNegotiation()}
	if tlsFiles.CA != "" || tlsFiles.Certificate != "" || tlsFiles.Key != "" {
		if tlsFiles.CA == "" || tlsFiles.Certificate == "" || tlsFiles.Key == "" {
			return nil, fmt.Errorf("Docker TLS credential requires ca, certificate, and key")
		}
		opts = append(opts, client.WithTLSClientConfig(tlsFiles.CA, tlsFiles.Certificate, tlsFiles.Key))
	}
	cli, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("create Docker client: %w", err)
	}
	return cli, nil
}

func NewWithClient(cli APIClient, rt model.Runtime, network string, defaults model.ReadinessPolicy) runtimex.Provider {
	base := baseProvider{client: cli, runtime: rt, network: network, defaults: defaults}
	if rt.Type == model.RuntimeSwarm {
		return &swarmProvider{baseProvider: base}
	}
	return &composeProvider{baseProvider: base}
}

type baseProvider struct {
	client   APIClient
	runtime  model.Runtime
	network  string
	defaults model.ReadinessPolicy
}

func (p *baseProvider) Close() error { return p.client.Close() }

func (p *baseProvider) watch(ctx context.Context, types ...string) (<-chan model.DiscoverySignal, <-chan error) {
	eventsCh, sourceErrors := p.client.Events(ctx, types)
	signals := make(chan model.DiscoverySignal, 32)
	errorsCh := make(chan error, 1)
	go func() {
		defer close(signals)
		defer close(errorsCh)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-eventsCh:
				if !ok {
					return
				}
				select {
				case signals <- NormalizeDockerEvent(event, p.runtime.ID):
				case <-ctx.Done():
					return
				}
			case err, ok := <-sourceErrors:
				if ok && err != nil {
					errorsCh <- err
				}
				return
			}
		}
	}()
	return signals, errorsCh
}

// NormalizeDockerEvent copies only identity and state evidence from Docker.
// Labels and raw event attributes must never enter the timeline or node reports.
func NormalizeDockerEvent(value eventtypes.Message, runtimeID string) model.DiscoverySignal {
	attrs := value.Actor.Attributes
	result := model.DiscoverySignal{RuntimeID: runtimeID, Kind: string(value.Action), ExternalID: value.Actor.ID,
		At: timeFromEvent(value), ActorType: string(value.Type),
		ServiceObjectID: attrs["com.docker.swarm.service.id"], TaskID: attrs["com.docker.swarm.task.id"],
		NodeID: attrs["com.docker.swarm.node.id"]}
	if project, service := attrs["com.docker.compose.project"], attrs["com.docker.compose.service"]; project != "" && service != "" {
		result.ServiceKey = project + "/" + service
	}
	if result.ActorType == "service" {
		result.ServiceObjectID = value.Actor.ID
	}
	if result.ActorType == "task" && result.ServiceObjectID == "" {
		result.ServiceObjectID = attrs["service"]
	}
	if code, err := strconv.Atoi(strings.TrimSpace(attrs["exitCode"])); err == nil {
		result.ExitCode = &code
	}
	for _, candidate := range []string{"state", "status"} {
		switch strings.ToLower(attrs[candidate]) {
		case "running", "shutdown", "failed", "complete", "rejected", "pending", "preparing", "starting", "assigned", "accepted", "ready", "healthy", "unhealthy":
			result.Status = strings.ToLower(attrs[candidate])
		}
		if result.Status != "" {
			break
		}
	}
	return result
}

func timeFromEvent(value eventtypes.Message) time.Time {
	if value.TimeNano > 0 {
		return time.Unix(0, value.TimeNano).UTC()
	}
	return time.Unix(value.Time, 0).UTC()
}
