package engine

import (
	"context"
	"errors"
	networktypes "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/model"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type swarmReadFailureClient struct {
	*fakeDockerClient
	stage   string
	failure error
}

func (c *swarmReadFailureClient) ServiceList(ctx context.Context) ([]swarm.Service, error) {
	if c.stage == "services" {
		return nil, c.failure
	}
	return c.fakeDockerClient.ServiceList(ctx)
}
func (c *swarmReadFailureClient) TaskList(ctx context.Context) ([]swarm.Task, error) {
	if c.stage == "tasks" {
		return nil, c.failure
	}
	return c.fakeDockerClient.TaskList(ctx)
}
func (c *swarmReadFailureClient) NodeList(ctx context.Context) ([]swarm.Node, error) {
	if c.stage == "nodes" {
		return nil, c.failure
	}
	return c.fakeDockerClient.NodeList(ctx)
}
func (c *swarmReadFailureClient) NetworkList(ctx context.Context) ([]networktypes.Summary, error) {
	if c.stage == "networks" {
		return nil, c.failure
	}
	return c.fakeDockerClient.NetworkList(ctx)
}
func TestSwarmSnapshotDoesNotPublishPartialInventoryOnReadFailure(t *testing.T) {
	for _, stage := range []string{"services", "tasks", "nodes", "networks"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New("isolated read failure")
			client := &swarmReadFailureClient{newFakeDockerClient(), stage, failure}
			client.services = []swarm.Service{{ID: "existing"}}
			provider := NewWithClient(client, model.Runtime{ID: "runtime", Type: model.RuntimeSwarm}, "overlay", providerReadiness())
			snapshot, e := provider.Snapshot(context.Background())
			if !errors.Is(e, failure) || !strings.Contains(e.Error(), stage) || len(snapshot.Services) != 0 || !snapshot.ObservedAt.IsZero() {
				t.Fatalf("partial discovery accepted: %+v %v", snapshot, e)
			}
		})
	}
}
func TestSwarmSnapshotPortNetworkAndTaskBoundaries(t *testing.T) {
	for _, stage := range []string{"inferred port", "no target port", "multiple TCP ports", "UDP only", "zero TCP port", "missing network", "wrong scope", "detached", "invalid labels", "missing node", "global desired tasks"} {
		t.Run(stage, func(t *testing.T) {
			client := newFakeDockerClient()
			client.networks = []networktypes.Summary{{ID: "other", Name: "irrelevant"}, {ID: "overlay", Name: "ingress", Driver: "overlay", Scope: "swarm"}}
			createdAt := time.Date(2026, 9, 10, 1, 2, 3, 0, time.FixedZone("test", 8*60*60))
			updatedAt := createdAt.Add(time.Hour)
			client.services = []swarm.Service{{Meta: swarm.Meta{CreatedAt: createdAt, UpdatedAt: updatedAt}, ID: "service", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "stack_web"}, TaskTemplate: swarm.TaskSpec{Networks: []swarm.NetworkAttachmentConfig{{Target: "other"}, {Target: "overlay"}}}}, Endpoint: swarm.Endpoint{Spec: swarm.EndpointSpec{Ports: []swarm.PortConfig{{Protocol: networktypes.TCP, TargetPort: 8080}}}}}}
			client.nodes = []swarm.Node{{ID: "node", Status: swarm.NodeStatus{State: swarm.NodeStateReady}, Spec: swarm.NodeSpec{Availability: swarm.NodeAvailabilityActive}}}
			client.tasks = []swarm.Task{{ID: "b", ServiceID: "service", NodeID: "node", DesiredState: swarm.TaskStateRunning, Status: swarm.TaskStatus{State: swarm.TaskStateRunning}, NetworksAttachments: swarmAttachments("overlay", "10.0.0.2/24")}, {ID: "a", ServiceID: "service", NodeID: "node", DesiredState: swarm.TaskStateShutdown, Status: swarm.TaskStatus{State: swarm.TaskStateComplete}}, {ID: "foreign", ServiceID: "other", DesiredState: swarm.TaskStateRunning}}
			service := &client.services[0]
			switch stage {
			case "no target port":
				service.Endpoint.Spec.Ports = nil
			case "multiple TCP ports":
				service.Endpoint.Spec.Ports = append(service.Endpoint.Spec.Ports, swarm.PortConfig{Protocol: networktypes.TCP, TargetPort: 9090})
			case "UDP only":
				service.Endpoint.Spec.Ports[0].Protocol = networktypes.UDP
			case "zero TCP port":
				service.Endpoint.Spec.Ports[0].TargetPort = 0
			case "missing network":
				client.networks = client.networks[:1]
			case "wrong scope":
				client.networks[1].Scope = "local"
			case "detached":
				service.Spec.TaskTemplate.Networks = service.Spec.TaskTemplate.Networks[:1]
			case "invalid labels":
				service.Spec.Labels = map[string]string{"nsc.enable": "true", "nsc.port": "invalid"}
			case "missing node":
				client.nodes = nil
			case "global desired tasks":
				service.Spec.Mode.Global = &swarm.GlobalService{}
			}
			snapshot, e := NewWithClient(client, model.Runtime{ID: "runtime", Type: model.RuntimeSwarm}, "ingress", providerReadiness()).Snapshot(context.Background())
			if e != nil || len(snapshot.Services) != 1 {
				t.Fatalf("discovery failed: %v", e)
			}
			got := snapshot.Services[0]
			if got.RuntimeCreatedAt == nil || !got.RuntimeCreatedAt.Equal(createdAt.UTC()) || got.RuntimeUpdatedAt == nil || !got.RuntimeUpdatedAt.Equal(updatedAt.UTC()) {
				t.Fatalf("Docker Service metadata timestamps lost: %+v", got)
			}
			if got.DesiredInstances != 1 || got.RunningInstances != 1 || len(got.Endpoints) != 2 || got.Endpoints[0].ExternalID != "a" {
				t.Fatalf("task grouping/order/counts incorrect: %+v", got)
			}
			eligible := stage == "inferred port" || stage == "missing node" || stage == "global desired tasks" || stage == "invalid labels"
			if stage == "invalid labels" && (len(got.Routes) != 0 || len(got.RoutingIssues) == 0) {
				t.Fatal("invalid routing labels silently accepted")
			}
			if got.RoutingEligible != eligible || got.Enabled != eligible {
				t.Fatalf("unsupported routing enabled: %+v", got)
			}
			if stage == "missing node" {
				if got.ReadyInstances != 0 || got.Endpoints[1].RuntimeState != model.EndpointStopping {
					t.Fatal("unverified node accepted for traffic")
				}
			} else if got.ReadyInstances != 1 {
				t.Fatal("ready task count lost")
			}
		})
	}
}
func TestSwarmTaskStateAndAddressInterpretation(t *testing.T) {
	for _, state := range []swarm.TaskState{swarm.TaskStateRunning, swarm.TaskStateAccepted, swarm.TaskStatePreparing, swarm.TaskStateReady, swarm.TaskStateStarting, swarm.TaskStateShutdown, swarm.TaskStateComplete, swarm.TaskStateFailed} {
		for _, desired := range []swarm.TaskState{swarm.TaskStateRunning, swarm.TaskStateShutdown} {
			want := model.EndpointDead
			if desired == swarm.TaskStateRunning {
				switch state {
				case swarm.TaskStateRunning:
					want = model.EndpointRunning
				case swarm.TaskStateAccepted, swarm.TaskStatePreparing, swarm.TaskStateReady, swarm.TaskStateStarting:
					want = model.EndpointStarting
				case swarm.TaskStateShutdown, swarm.TaskStateComplete:
					want = model.EndpointStopping
				}
			} else if state == swarm.TaskStateRunning || state == swarm.TaskStateShutdown || state == swarm.TaskStateComplete {
				want = model.EndpointStopping
			}
			if got := mapTaskState(swarm.Task{DesiredState: desired, Status: swarm.TaskStatus{State: state}}); got != want {
				t.Fatalf("state %s desired %s: %s", state, desired, got)
			}
		}
	}
	task := swarm.Task{NetworksAttachments: swarmAttachments("other", "10.0.0.2/24")}
	task.NetworksAttachments = append(task.NetworksAttachments, swarm.NetworkAttachment{Network: swarm.Network{ID: "target"}, Addresses: []netip.Prefix{{}, netip.MustParsePrefix("::ffff:10.0.0.3/120")}})
	if got := taskAddress(task, "target"); got != "10.0.0.3" {
		t.Fatal("invalid or foreign address selected", got)
	}
	if taskAddress(task, "missing") != "" {
		t.Fatal("missing network invented address")
	}
}
