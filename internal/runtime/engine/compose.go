package engine

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	containertypes "github.com/moby/moby/api/types/container"

	"github.com/yernsun/network-service-composer/internal/model"
	runtimex "github.com/yernsun/network-service-composer/internal/runtime"
)

type composeProvider struct{ baseProvider }

func (p *composeProvider) Snapshot(ctx context.Context) (model.RuntimeSnapshot, error) {
	containers, err := p.client.ContainerList(ctx)
	if err != nil {
		return model.RuntimeSnapshot{}, fmt.Errorf("list containers: %w", err)
	}
	sort.Slice(containers, func(i, j int) bool { return containers[i].ID < containers[j].ID })
	type aggregate struct {
		config          runtimex.LabelConfig
		configSet       bool
		resolvedPort    int
		resolvedPortSet bool
		runningMembers  int
		networkMembers  int
		aliasMembers    int
		service         model.DiscoveredService
	}
	groups := map[string]*aggregate{}
	for _, container := range containers {
		labels := container.Labels
		if labels["com.docker.compose.oneoff"] == "True" {
			continue
		}
		project, serviceName := labels["com.docker.compose.project"], labels["com.docker.compose.service"]
		if project == "" || serviceName == "" {
			continue
		}
		// ContainerList and ContainerInspect are not one atomic Docker API
		// operation. During Compose recreate the list can legitimately contain
		// an ID that has disappeared by the time it is inspected. Treat that
		// precise NotFound as an unobserved old replica; turning it into a DNS
		// configuration error would transiently remove a stable Service DNS
		// upstream and force an unnecessary Gateway reload.
		inspect, inspectErr := p.client.ContainerInspect(ctx, container.ID)
		if cerrdefs.IsNotFound(inspectErr) {
			continue
		}
		externalID := project + "/" + serviceName
		group := groups[externalID]
		if group == nil {
			qualifiedName := project + "_" + serviceName
			group = &aggregate{service: model.DiscoveredService{ServiceKey: externalID, ExternalID: externalID, Name: serviceName, Namespace: project,
				QualifiedName: qualifiedName, DNSName: qualifiedName, DNSStatus: model.DNSMissing}}
			groups[externalID] = group
		}
		group.service.DesiredInstances++
		labelConfig, parseErr := runtimex.ParseLabels(labels, p.defaults)
		if parseErr != nil {
			addRoutingIssue(&group.service, parseErr.Error())
			labelConfig = runtimex.LabelConfig{Protocol: "http", Readiness: p.defaults}
		}
		if !group.configSet {
			group.config = labelConfig
			group.configSet = true
			group.service.Protocol = labelConfig.Protocol
			group.service.TargetPort = labelConfig.Port
			group.service.Enabled = true
			group.service.Readiness = labelConfig.Readiness
			if labelConfig.Enabled && parseErr == nil {
				group.service.Routes = slices.Clone(labelConfig.Routes)
			}
		} else if !equalLabelConfig(group.config, labelConfig) {
			addConfigurationError(&group.service, "replicas have inconsistent NSC labels")
			group.service.Enabled = false
		}
		if inspectErr != nil {
			addConfigurationError(&group.service, "inspect container: "+inspectErr.Error())
			group.service.Enabled = false
			continue
		}
		port := labelConfig.Port
		if port == 0 {
			port, err = inferContainerPort(inspect)
			if err != nil {
				addRoutingIssue(&group.service, err.Error())
			}
		}
		if port != 0 {
			if !group.resolvedPortSet {
				group.resolvedPort = port
				group.resolvedPortSet = true
				group.service.TargetPort = port
			} else if group.resolvedPort != port {
				addConfigurationError(&group.service, "replicas expose inconsistent target ports")
				group.service.Enabled = false
			}
		}
		state := mapContainerState(string(container.State))
		health := model.HealthUnknown
		var startedAt *time.Time
		if inspect.State != nil {
			state = mapContainerState(string(inspect.State.Status))
			health = mapContainerHealth(inspect.State)
			if parsed, parseErr := time.Parse(time.RFC3339Nano, inspect.State.StartedAt); parseErr == nil && !parsed.IsZero() {
				parsed = parsed.UTC()
				startedAt = &parsed
			}
		}
		address := containerAddress(inspect, p.network)
		if state == model.EndpointRunning {
			group.service.RunningInstances++
			// Native convergence is independent of Gateway DNS/probe readiness.
			// A current inspect must confirm running and either healthy or the
			// absence of a Docker healthcheck; a list entry alone is insufficient.
			if inspect.State != nil && (inspect.State.Health == nil || health == model.HealthHealthy) {
				group.service.ReadyInstances++
			}
			group.runningMembers++
			if address == "" {
				addRoutingIssue(&group.service, fmt.Sprintf("running container is not attached to network %q", p.network))
			} else {
				group.networkMembers++
				if containerHasAlias(inspect, p.network, group.service.DNSName) {
					group.aliasMembers++
				}
			}
		}
		group.service.Endpoints = append(group.service.Endpoints, model.DiscoveredEndpoint{ExternalID: container.ID, Address: address, Port: port, RuntimeState: state, HealthState: health, Weight: 1, StartedAt: startedAt})
		group.service.RuntimeObjectID = container.ID
	}
	result := model.RuntimeSnapshot{RuntimeID: p.runtime.ID, ObservedAt: time.Now().UTC()}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := groups[key]
		switch {
		case group.runningMembers == 0:
			group.service.DNSStatus = model.DNSMissing
			addRoutingIssue(&group.service, "service has no running replica providing Docker DNS")
		case group.networkMembers != group.runningMembers:
			group.service.DNSStatus = model.DNSMissing
			addRoutingIssue(&group.service, fmt.Sprintf("all running replicas must be attached to network %q before Docker can provide service DNS", p.network))
		case group.aliasMembers != group.runningMembers:
			group.service.DNSStatus = model.DNSMissing
			addRoutingIssue(&group.service, fmt.Sprintf("network %q does not provide required service alias %q", p.network, group.service.DNSName))
		default:
			group.service.DNSStatus = model.DNSReady
		}
		group.service.RoutingEligible = group.service.DNSStatus == model.DNSReady && group.service.TargetPort > 0 && group.service.ConfigurationError == ""
		group.service.Enabled = group.service.RoutingEligible
		result.Services = append(result.Services, group.service)
	}
	return result, nil
}

func containerHasAlias(value containertypes.InspectResponse, networkName, alias string) bool {
	if value.NetworkSettings == nil || value.NetworkSettings.Networks[networkName] == nil {
		return false
	}
	return slices.Contains(value.NetworkSettings.Networks[networkName].Aliases, alias)
}

func addRoutingIssue(service *model.DiscoveredService, message string) {
	if message == "" || slices.Contains(service.RoutingIssues, message) {
		return
	}
	service.RoutingIssues = append(service.RoutingIssues, message)
}

func equalLabelConfig(left, right runtimex.LabelConfig) bool {
	return left.Enabled == right.Enabled &&
		left.Port == right.Port &&
		left.Protocol == right.Protocol &&
		left.Readiness == right.Readiness &&
		slices.Equal(left.Routes, right.Routes)
}

func addConfigurationError(service *model.DiscoveredService, message string) {
	if message == "" {
		return
	}
	for _, existing := range strings.Split(service.ConfigurationError, "; ") {
		if existing == message {
			return
		}
	}
	if service.ConfigurationError != "" {
		service.ConfigurationError += "; "
	}
	service.ConfigurationError += message
}

func (p *composeProvider) Watch(ctx context.Context) (<-chan model.DiscoverySignal, <-chan error) {
	return p.watch(ctx, "container", "network")
}

func inferContainerPort(value containertypes.InspectResponse) (int, error) {
	if value.Config == nil {
		return 0, fmt.Errorf("container has no configuration")
	}
	ports := make([]int, 0, len(value.Config.ExposedPorts))
	for port := range value.Config.ExposedPorts {
		if port.Proto() != "tcp" {
			continue
		}
		ports = append(ports, int(port.Num()))
	}
	sort.Ints(ports)
	if len(ports) != 1 {
		return 0, fmt.Errorf("nsc.port is required when the container does not expose exactly one TCP port")
	}
	return ports[0], nil
}
func containerAddress(value containertypes.InspectResponse, networkName string) string {
	if value.NetworkSettings == nil {
		return ""
	}
	settings := value.NetworkSettings.Networks[networkName]
	if settings == nil {
		return ""
	}
	if settings.IPAddress.IsValid() {
		return settings.IPAddress.Unmap().String()
	}
	if settings.GlobalIPv6Address.IsValid() {
		return settings.GlobalIPv6Address.String()
	}
	return ""
}
func mapContainerState(value string) model.RuntimeState {
	switch strings.ToLower(value) {
	case "created":
		return model.EndpointCreated
	case "running":
		return model.EndpointRunning
	case "restarting":
		return model.EndpointStarting
	case "removing", "exited", "dead":
		return model.EndpointDead
	default:
		return model.EndpointStarting
	}
}
func mapContainerHealth(state *containertypes.State) model.HealthState {
	if state == nil || state.Health == nil {
		return model.HealthUnknown
	}
	switch state.Health.Status {
	case containertypes.Healthy:
		return model.HealthHealthy
	case containertypes.Unhealthy:
		return model.HealthUnhealthy
	default:
		return model.HealthUnknown
	}
}
