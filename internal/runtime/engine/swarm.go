package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	networktypes "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"

	"github.com/yernsun/network-service-composer/internal/model"
	runtimex "github.com/yernsun/network-service-composer/internal/runtime"
)

type swarmProvider struct{ baseProvider }

func (p *swarmProvider) Snapshot(ctx context.Context) (model.RuntimeSnapshot, error) {
	services, err := p.client.ServiceList(ctx)
	if err != nil {
		return model.RuntimeSnapshot{}, fmt.Errorf("list swarm services: %w", err)
	}
	tasks, err := p.client.TaskList(ctx)
	if err != nil {
		return model.RuntimeSnapshot{}, fmt.Errorf("list swarm tasks: %w", err)
	}
	nodes, err := p.client.NodeList(ctx)
	if err != nil {
		return model.RuntimeSnapshot{}, fmt.Errorf("list swarm nodes: %w", err)
	}
	networks, err := p.client.NetworkList(ctx)
	if err != nil {
		return model.RuntimeSnapshot{}, fmt.Errorf("list swarm networks: %w", err)
	}
	networkID := ""
	networkProblem := fmt.Sprintf("overlay network %q does not exist", p.network)
	for _, network := range networks {
		if network.Name == p.network {
			if network.Driver == "overlay" && network.Scope == "swarm" {
				networkID = network.ID
			} else {
				networkProblem = fmt.Sprintf("network %q is not a swarm overlay", p.network)
			}
			break
		}
	}
	nodesByID := make(map[string]swarm.Node, len(nodes))
	for _, node := range nodes {
		nodesByID[node.ID] = node
	}
	result := model.RuntimeSnapshot{RuntimeID: p.runtime.ID, ObservedAt: time.Now().UTC()}
	for _, service := range services {
		cfg, parseErr := runtimex.ParseLabels(service.Spec.Labels, p.defaults)
		discovered := model.DiscoveredService{ServiceKey: service.Spec.Name, RuntimeObjectID: service.ID, ExternalID: service.Spec.Name,
			Name: service.Spec.Name, Namespace: service.Spec.Labels["com.docker.stack.namespace"], QualifiedName: service.Spec.Name, DNSName: service.Spec.Name,
			DNSStatus: model.DNSMissing, Protocol: cfg.Protocol, TargetPort: cfg.Port, Enabled: true, Readiness: cfg.Readiness}
		if !service.Meta.CreatedAt.IsZero() {
			createdAt := service.Meta.CreatedAt.UTC()
			discovered.RuntimeCreatedAt = &createdAt
		}
		if !service.Meta.UpdatedAt.IsZero() {
			updatedAt := service.Meta.UpdatedAt.UTC()
			discovered.RuntimeUpdatedAt = &updatedAt
		}
		if cfg.Enabled && parseErr == nil {
			discovered.Routes = cfg.Routes
		}
		if parseErr != nil {
			addRoutingIssue(&discovered, parseErr.Error())
		}
		if cfg.Enabled && !strings.Contains(service.Spec.Name, "_") {
			discovered.DNSStatus = model.DNSMissing
			addConfigurationError(&discovered, "Swarm DNS name must be stack-qualified (stack_service)")
			addRoutingIssue(&discovered, "use a full stack-qualified service name instead of a shared-network short name")
		}
		if discovered.TargetPort == 0 {
			discovered.TargetPort = inferSwarmPort(service)
			if discovered.TargetPort == 0 {
				addRoutingIssue(&discovered, "nsc.port is required when the service does not expose exactly one TCP target port")
			}
		}
		serviceTasks := tasksForService(tasks, service.ID)
		if service.Spec.Mode.Replicated != nil && service.Spec.Mode.Replicated.Replicas != nil {
			discovered.DesiredInstances = int(*service.Spec.Mode.Replicated.Replicas)
		} else {
			for _, task := range serviceTasks {
				if task.DesiredState == swarm.TaskStateRunning {
					discovered.DesiredInstances++
				}
			}
		}
		for _, task := range serviceTasks {
			if task.Status.State == swarm.TaskStateRunning {
				discovered.RunningInstances++
				if task.DesiredState == swarm.TaskStateRunning && swarmNodeAllowsTraffic(nodesByID, task.NodeID) {
					discovered.ReadyInstances++
				}
			}
		}
		if networkID == "" {
			addConfigurationError(&discovered, networkProblem)
			addRoutingIssue(&discovered, networkProblem)
		}
		for _, task := range serviceTasks {
			address := taskAddress(task, networkID)
			runtimeState := mapTaskState(task)
			if runtimeState == model.EndpointRunning && (task.DesiredState != swarm.TaskStateRunning || !swarmNodeAllowsTraffic(nodesByID, task.NodeID)) {
				runtimeState = model.EndpointStopping
			}
			// Task addresses are retained only for instance status. They never enter
			// a Gateway snapshot or an NGINX upstream.
			discovered.Endpoints = append(discovered.Endpoints, model.DiscoveredEndpoint{ExternalID: task.ID, Address: address, Port: discovered.TargetPort, NodeID: task.NodeID,
				RuntimeState: runtimeState, HealthState: model.HealthUnknown, Weight: 1, StartedAt: &task.Status.Timestamp,
				TaskState: string(task.Status.State), TaskStatusAt: &task.Status.Timestamp})
		}
		if discovered.ConfigurationError == "" && networkID != "" && serviceAttachedToNetwork(service, networkID) {
			discovered.DNSStatus = model.DNSReady
		} else {
			discovered.DNSStatus = model.DNSMissing
			addRoutingIssue(&discovered, fmt.Sprintf("service is not attached to overlay network %q", p.network))
		}
		discovered.RoutingEligible = discovered.DNSStatus == model.DNSReady && discovered.TargetPort > 0 && discovered.ConfigurationError == ""
		discovered.Enabled = discovered.RoutingEligible
		result.Services = append(result.Services, discovered)
	}
	sort.Slice(result.Services, func(i, j int) bool { return result.Services[i].ExternalID < result.Services[j].ExternalID })
	return result, nil
}

func serviceAttachedToNetwork(service swarm.Service, networkID string) bool {
	for _, network := range service.Spec.TaskTemplate.Networks {
		if network.Target == networkID {
			return true
		}
	}
	return false
}

func invalidateDiscoveredService(service *model.DiscoveredService, message string) {
	addConfigurationError(service, message)
	service.Enabled = false
}

func (p *swarmProvider) Watch(ctx context.Context) (<-chan model.DiscoverySignal, <-chan error) {
	return p.watch(ctx, "service", "task", "network", "node")
}

func swarmNodeAllowsTraffic(nodes map[string]swarm.Node, nodeID string) bool {
	node, ok := nodes[nodeID]
	if !ok || node.Status.State != swarm.NodeStateReady {
		return false
	}
	return node.Spec.Availability != swarm.NodeAvailabilityDrain
}

func tasksForService(tasks []swarm.Task, serviceID string) []swarm.Task {
	values := make([]swarm.Task, 0)
	for _, task := range tasks {
		if task.ServiceID == serviceID {
			values = append(values, task)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return values
}
func inferSwarmPort(service swarm.Service) int {
	var values []int
	for _, port := range service.Endpoint.Spec.Ports {
		if port.Protocol == networktypes.TCP && port.TargetPort > 0 {
			values = append(values, int(port.TargetPort))
		}
	}
	if len(values) == 1 {
		return values[0]
	}
	return 0
}
func taskAddress(task swarm.Task, networkID string) string {
	for _, attachment := range task.NetworksAttachments {
		if attachment.Network.ID != networkID {
			continue
		}
		for _, address := range attachment.Addresses {
			if address.IsValid() {
				return address.Addr().Unmap().String()
			}
		}
	}
	return ""
}
func mapTaskState(task swarm.Task) model.RuntimeState {
	if task.DesiredState != swarm.TaskStateRunning {
		if task.Status.State == swarm.TaskStateRunning || task.Status.State == swarm.TaskStateShutdown || task.Status.State == swarm.TaskStateComplete {
			return model.EndpointStopping
		}
		return model.EndpointDead
	}
	switch task.Status.State {
	case swarm.TaskStateRunning:
		return model.EndpointRunning
	case swarm.TaskStateAccepted, swarm.TaskStatePreparing, swarm.TaskStateReady, swarm.TaskStateStarting:
		return model.EndpointStarting
	case swarm.TaskStateShutdown, swarm.TaskStateComplete:
		return model.EndpointStopping
	default:
		return model.EndpointDead
	}
}
