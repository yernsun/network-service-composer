package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	containertypes "github.com/moby/moby/api/types/container"
	eventtypes "github.com/moby/moby/api/types/events"
	networktypes "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"

	"github.com/yernsun/network-service-composer/internal/model"
)

type fakeDockerClient struct {
	containers  []containertypes.Summary
	inspections map[string]containertypes.InspectResponse
	inspectErrs map[string]error
	services    []swarm.Service
	tasks       []swarm.Task
	nodes       []swarm.Node
	networks    []networktypes.Summary
	events      chan eventtypes.Message
	eventErrors chan error
	eventTypes  []string
}

func newFakeDockerClient() *fakeDockerClient {
	return &fakeDockerClient{
		inspections: map[string]containertypes.InspectResponse{},
		inspectErrs: map[string]error{},
		events:      make(chan eventtypes.Message, 4),
		eventErrors: make(chan error, 1),
	}
}

func (f *fakeDockerClient) ContainerList(context.Context) ([]containertypes.Summary, error) {
	return f.containers, nil
}
func (f *fakeDockerClient) ContainerInspect(_ context.Context, containerID string) (containertypes.InspectResponse, error) {
	if err := f.inspectErrs[containerID]; err != nil {
		return containertypes.InspectResponse{}, err
	}
	value, ok := f.inspections[containerID]
	if !ok {
		return containertypes.InspectResponse{}, errors.New("container not found")
	}
	return value, nil
}

func TestComposeSnapshotIgnoresReplicaRemovedBetweenListAndInspect(t *testing.T) {
	client := newFakeDockerClient()
	labels := map[string]string{
		"com.docker.compose.project": "shop",
		"com.docker.compose.service": "api",
		"nsc.port":                   "8080",
	}
	client.containers = []containertypes.Summary{
		{ID: "removed-replica", State: containertypes.StateRunning, Labels: labels},
		{ID: "replacement-replica", State: containertypes.StateRunning, Labels: labels},
	}
	client.inspectErrs["removed-replica"] = cerrdefs.ErrNotFound
	client.inspections["replacement-replica"] = composeInspectWithPort("10.42.0.9", 8080)

	provider := NewWithClient(client, model.Runtime{ID: "rt_01", Type: model.RuntimeDocker}, "nsc-ingress", providerReadiness())
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Services) != 1 {
		t.Fatalf("services = %#v", snapshot.Services)
	}
	service := snapshot.Services[0]
	if service.DesiredInstances != 1 || len(service.Endpoints) != 1 || service.Endpoints[0].ExternalID != "replacement-replica" ||
		service.ConfigurationError != "" || service.DNSStatus != model.DNSReady || !service.RoutingEligible {
		t.Fatalf("removed replica destabilized DNS discovery: %+v", service)
	}
}

func TestComposeSnapshotIgnoresStartingReplicaForStableDNSIdentity(t *testing.T) {
	client := newFakeDockerClient()
	labels := map[string]string{
		"com.docker.compose.project": "shop",
		"com.docker.compose.service": "api",
		"nsc.port":                   "8080",
	}
	client.containers = []containertypes.Summary{
		{ID: "running-replica", State: containertypes.StateRunning, Labels: labels},
		{ID: "starting-replica", State: containertypes.StateCreated, Labels: labels},
	}
	client.inspections["running-replica"] = composeInspectWithPort("10.42.0.9", 8080)
	client.inspections["starting-replica"] = containertypes.InspectResponse{
		State:           &containertypes.State{Status: containertypes.StateCreated},
		Config:          &containertypes.Config{ExposedPorts: networktypes.PortSet{networktypes.MustParsePort("8080/tcp"): {}}},
		NetworkSettings: &containertypes.NetworkSettings{Networks: map[string]*networktypes.EndpointSettings{}},
	}

	provider := NewWithClient(client, model.Runtime{ID: "rt_01", Type: model.RuntimeDocker}, "nsc-ingress", providerReadiness())
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Services) != 1 {
		t.Fatalf("services = %#v", snapshot.Services)
	}
	service := snapshot.Services[0]
	if service.DesiredInstances != 2 || service.RunningInstances != 1 || len(service.Endpoints) != 2 ||
		service.ConfigurationError != "" || service.DNSStatus != model.DNSReady || !service.RoutingEligible {
		t.Fatalf("starting replica destabilized stable Docker DNS identity: %+v", service)
	}
}
func (f *fakeDockerClient) ServiceList(context.Context) ([]swarm.Service, error) {
	return f.services, nil
}
func (f *fakeDockerClient) TaskList(context.Context) ([]swarm.Task, error) {
	return f.tasks, nil
}
func (f *fakeDockerClient) NodeList(context.Context) ([]swarm.Node, error) {
	return f.nodes, nil
}
func (f *fakeDockerClient) NetworkList(context.Context) ([]networktypes.Summary, error) {
	return f.networks, nil
}
func (f *fakeDockerClient) Events(_ context.Context, types []string) (<-chan eventtypes.Message, <-chan error) {
	f.eventTypes = append([]string(nil), types...)
	return f.events, f.eventErrors
}
func (f *fakeDockerClient) Close() error { return nil }

func providerReadiness() model.ReadinessPolicy {
	return model.ReadinessPolicy{
		Mode:               model.ReadinessAuto,
		Interval:           2 * time.Second,
		Timeout:            time.Second,
		SuccessThreshold:   2,
		FailureThreshold:   3,
		StartupGracePeriod: 2 * time.Second,
		StartupTimeout:     time.Minute,
		TCPFallback:        true,
	}
}

func TestComposeSnapshotIsDeterministicAndNetworkScoped(t *testing.T) {
	client := newFakeDockerClient()
	labels := map[string]string{
		"com.docker.compose.project":      "shop",
		"com.docker.compose.service":      "api",
		"nsc.enable":                      "true",
		"nsc.port":                        "8080",
		"nsc.routes.public.host":          "API.Example.COM.",
		"nsc.routes.public.path":          "/v1",
		"nsc.routes.public.strip-path":    "true",
		"nsc.routes.public.websocket":     "true",
		"nsc.readiness.mode":              "docker",
		"nsc.readiness.success-threshold": "1",
		"nsc.readiness.failure-threshold": "1",
	}
	client.containers = []containertypes.Summary{
		{ID: "container-b", State: containertypes.StateRunning, Labels: labels},
		{ID: "container-a", State: containertypes.StateRunning, Labels: labels},
	}
	client.inspections["container-a"] = composeInspect("10.42.0.2", containertypes.Healthy)
	client.inspections["container-b"] = composeInspect("10.42.0.3", containertypes.Unhealthy)

	runtimeValue := model.Runtime{ID: "rt_01", Type: model.RuntimeDocker}
	provider := NewWithClient(client, runtimeValue, "nsc-ingress", providerReadiness())
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Services) != 1 {
		t.Fatalf("service count = %d, want 1", len(snapshot.Services))
	}
	service := snapshot.Services[0]
	if service.ExternalID != "shop/api" || service.DesiredInstances != 2 || !service.Enabled {
		t.Fatalf("unexpected service identity/count: %+v", service)
	}
	if service.ConfigurationError != "" {
		t.Fatalf("configuration error = %q", service.ConfigurationError)
	}
	if len(service.Routes) != 1 || service.Routes[0].Host != "api.example.com" || service.Routes[0].Path != "/v1/" || !service.Routes[0].StripPath || !service.Routes[0].WebsocketEnabled {
		t.Fatalf("unexpected normalized route: %+v", service.Routes)
	}
	wantEndpointIDs := []string{"container-a", "container-b"}
	gotEndpointIDs := []string{service.Endpoints[0].ExternalID, service.Endpoints[1].ExternalID}
	if !reflect.DeepEqual(gotEndpointIDs, wantEndpointIDs) {
		t.Fatalf("endpoint IDs = %v, want %v", gotEndpointIDs, wantEndpointIDs)
	}
	if service.Endpoints[0].Address != "10.42.0.2" || service.Endpoints[0].HealthState != model.HealthHealthy {
		t.Fatalf("unexpected first endpoint: %+v", service.Endpoints[0])
	}
	if service.Endpoints[1].Address != "10.42.0.3" || service.Endpoints[1].HealthState != model.HealthUnhealthy {
		t.Fatalf("unexpected second endpoint: %+v", service.Endpoints[1])
	}
}

func TestComposeSnapshotRejectsInconsistentReplicaLabels(t *testing.T) {
	client := newFakeDockerClient()
	labelsA := map[string]string{
		"com.docker.compose.project": "shop",
		"com.docker.compose.service": "api",
		"nsc.enable":                 "true",
		"nsc.port":                   "8080",
		"nsc.routes.public.host":     "api.example.com",
	}
	labelsB := map[string]string{
		"com.docker.compose.project": "shop",
		"com.docker.compose.service": "api",
		"nsc.enable":                 "true",
		"nsc.port":                   "8080",
		"nsc.routes.public.host":     "other.example.com",
	}
	client.containers = []containertypes.Summary{
		{ID: "container-a", State: containertypes.StateRunning, Labels: labelsA},
		{ID: "container-b", State: containertypes.StateRunning, Labels: labelsB},
	}
	client.inspections["container-a"] = composeInspect("10.42.0.2", containertypes.Healthy)
	client.inspections["container-b"] = composeInspect("10.42.0.3", containertypes.Healthy)

	provider := NewWithClient(client, model.Runtime{ID: "rt_01", Type: model.RuntimeDocker}, "nsc-ingress", providerReadiness())
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Services) != 1 {
		t.Fatalf("service count = %d, want 1", len(snapshot.Services))
	}
	service := snapshot.Services[0]
	if service.Enabled || service.ConfigurationError != "replicas have inconsistent NSC labels" {
		t.Fatalf("inconsistent replicas were accepted: %+v", service)
	}
}

func TestComposeSnapshotRejectsInconsistentInferredPorts(t *testing.T) {
	client := newFakeDockerClient()
	labels := map[string]string{
		"com.docker.compose.project": "shop",
		"com.docker.compose.service": "api",
		"nsc.enable":                 "true",
	}
	client.containers = []containertypes.Summary{
		{ID: "container-a", State: containertypes.StateRunning, Labels: labels},
		{ID: "container-b", State: containertypes.StateRunning, Labels: labels},
	}
	client.inspections["container-a"] = composeInspectWithPort("10.42.0.2", 8080)
	client.inspections["container-b"] = composeInspectWithPort("10.42.0.3", 9090)

	provider := NewWithClient(client, model.Runtime{ID: "rt_01", Type: model.RuntimeDocker}, "nsc-ingress", providerReadiness())
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	service := snapshot.Services[0]
	if service.Enabled || service.ConfigurationError != "replicas expose inconsistent target ports" {
		t.Fatalf("inconsistent inferred ports were accepted: %+v", service)
	}
}

func TestComposeSnapshotRequiresQualifiedAliasOnEveryReplica(t *testing.T) {
	client := newFakeDockerClient()
	labels := map[string]string{
		"com.docker.compose.project": "shop",
		"com.docker.compose.service": "api",
		"nsc.port":                   "8080",
	}
	client.containers = []containertypes.Summary{
		{ID: "container-a", State: containertypes.StateRunning, Labels: labels},
		{ID: "container-b", State: containertypes.StateRunning, Labels: labels},
	}
	client.inspections["container-a"] = composeInspect("10.42.0.2", containertypes.Healthy)
	withoutAlias := composeInspect("10.42.0.3", containertypes.Healthy)
	withoutAlias.NetworkSettings.Networks["nsc-ingress"].Aliases = []string{"api"}
	client.inspections["container-b"] = withoutAlias

	provider := NewWithClient(client, model.Runtime{ID: "rt_01", Type: model.RuntimeDocker}, "nsc-ingress", providerReadiness())
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	service := snapshot.Services[0]
	if service.DNSStatus != model.DNSMissing || service.RoutingEligible || service.Enabled ||
		!strings.Contains(strings.Join(service.RoutingIssues, "\n"), `required service alias "shop_api"`) {
		t.Fatalf("replica without the project-qualified alias was routable: %+v", service)
	}
}

func TestComposeListsUnlabelledAndStoppedServicesButExcludesOneOff(t *testing.T) {
	client := newFakeDockerClient()
	base := map[string]string{
		"com.docker.compose.project": "shop",
		"com.docker.compose.service": "worker",
	}
	oneOff := map[string]string{
		"com.docker.compose.project": "shop",
		"com.docker.compose.service": "job",
		"com.docker.compose.oneoff":  "True",
	}
	client.containers = []containertypes.Summary{
		{ID: "stopped-worker", State: containertypes.StateExited, Labels: base},
		{ID: "one-off", State: containertypes.StateExited, Labels: oneOff},
	}
	client.inspections["stopped-worker"] = containertypes.InspectResponse{
		State:           &containertypes.State{Status: containertypes.StateExited},
		Config:          &containertypes.Config{ExposedPorts: networktypes.PortSet{networktypes.MustParsePort("8080/tcp"): {}}},
		NetworkSettings: &containertypes.NetworkSettings{Networks: map[string]*networktypes.EndpointSettings{}},
	}
	provider := NewWithClient(client, model.Runtime{ID: "rt_01", Type: model.RuntimeDocker}, "nsc-ingress", providerReadiness())
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Services) != 1 {
		t.Fatalf("services = %#v, want only the normal Compose service", snapshot.Services)
	}
	service := snapshot.Services[0]
	if service.ServiceKey != "shop/worker" || service.RuntimeObjectID != "stopped-worker" || service.Enabled || len(service.Routes) != 0 || service.RunningInstances != 0 {
		t.Fatalf("unexpected stopped unlabelled service: %#v", service)
	}
}

func composeInspect(address string, health containertypes.HealthStatus) containertypes.InspectResponse {
	return containertypes.InspectResponse{
		State: &containertypes.State{
			Status:    containertypes.StateRunning,
			StartedAt: "2026-09-09T01:02:03.000000004Z",
			Health:    &containertypes.Health{Status: health},
		},
		NetworkSettings: &containertypes.NetworkSettings{Networks: map[string]*networktypes.EndpointSettings{
			"nsc-ingress": {IPAddress: netip.MustParseAddr(address), Aliases: []string{"shop_api"}},
		}},
	}
}

func composeInspectWithPort(address string, port uint16) containertypes.InspectResponse {
	value := composeInspect(address, containertypes.Healthy)
	value.Config = &containertypes.Config{ExposedPorts: networktypes.PortSet{networktypes.MustParsePort(fmt.Sprintf("%d/tcp", port)): {}}}
	return value
}

func TestProviderWatchNormalizesDockerEvents(t *testing.T) {
	client := newFakeDockerClient()
	provider := NewWithClient(client, model.Runtime{ID: "rt_watch", Type: model.RuntimeDocker}, "nsc-ingress", providerReadiness())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals, errs := provider.Watch(ctx)

	wantTime := time.Date(2026, 9, 9, 1, 2, 3, 400, time.UTC)
	client.events <- eventtypes.Message{Action: eventtypes.Action("health_status: healthy"), Actor: eventtypes.Actor{ID: "container-a"}, TimeNano: wantTime.UnixNano()}
	select {
	case signal := <-signals:
		if signal.RuntimeID != "rt_watch" || signal.ExternalID != "container-a" || signal.Kind != "health_status: healthy" || !signal.At.Equal(wantTime) {
			t.Fatalf("unexpected signal: %+v", signal)
		}
	case err := <-errs:
		t.Fatalf("unexpected watch error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for discovery signal")
	}
	gotTypes := client.eventTypes
	sort.Strings(gotTypes)
	if !reflect.DeepEqual(gotTypes, []string{"container", "network"}) {
		t.Fatalf("event types = %v", gotTypes)
	}
}

func TestDockerEventNormalizationKeepsOnlyAllowlistedEvidence(t *testing.T) {
	value := eventtypes.Message{Type: eventtypes.ContainerEventType, Action: eventtypes.Action("die"),
		Actor: eventtypes.Actor{ID: "container-1", Attributes: map[string]string{
			"com.docker.compose.project": "demoapp", "com.docker.compose.service": "api", "exitCode": "137",
			"secret": "must-not-persist", "state": "failed",
		}}, Time: 1}
	signal := NormalizeDockerEvent(value, "runtime-1")
	if signal.ServiceKey != "demoapp/api" || signal.ExitCode == nil || *signal.ExitCode != 137 || signal.Status != "failed" ||
		signal.ActorType != "container" {
		t.Fatalf("lost allowed Docker event evidence: %#v", signal)
	}
	raw, err := json.Marshal(signal)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("must-not-persist")) || bytes.Contains(raw, []byte("secret")) {
		t.Fatalf("raw Docker attributes escaped the allowlist: %s", raw)
	}
}

func TestSwarmSnapshotUsesStableServiceDNSAndKeepsTasksAsStatusOnly(t *testing.T) {
	client := newFakeDockerClient()
	replicas := uint64(2)
	client.networks = []networktypes.Summary{{ID: "network-1", Name: "nsc-ingress", Driver: "overlay", Scope: "swarm"}}
	client.services = []swarm.Service{{
		ID: "service-1",
		Spec: swarm.ServiceSpec{
			Annotations: swarm.Annotations{Name: "demo_web", Labels: map[string]string{
				"nsc.enable":              "true",
				"nsc.port":                "9000",
				"nsc.routes.web.host":     "web.example.com",
				"nsc.routes.web.protocol": "http",
			}},
			Mode:         swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &replicas}},
			TaskTemplate: swarm.TaskSpec{Networks: []swarm.NetworkAttachmentConfig{{Target: "network-1"}}},
		},
		Endpoint: swarm.Endpoint{VirtualIPs: []swarm.EndpointVirtualIP{{NetworkID: "network-1", Addr: netip.MustParsePrefix("10.50.0.100/24")}}},
	}}
	started := time.Date(2026, 9, 9, 2, 0, 0, 0, time.UTC)
	client.tasks = []swarm.Task{
		{ID: "task-b", ServiceID: "service-1", NodeID: "node-b", DesiredState: swarm.TaskStateRunning, Status: swarm.TaskStatus{State: swarm.TaskStateRunning, Timestamp: started}, NetworksAttachments: swarmAttachments("network-1", "10.50.0.3/24")},
		{ID: "task-a", ServiceID: "service-1", NodeID: "node-a", DesiredState: swarm.TaskStateRunning, Status: swarm.TaskStatus{State: swarm.TaskStateStarting, Timestamp: started}, NetworksAttachments: swarmAttachments("network-1", "10.50.0.2/24")},
	}
	client.nodes = []swarm.Node{
		{ID: "node-a", Spec: swarm.NodeSpec{Availability: swarm.NodeAvailabilityActive}, Status: swarm.NodeStatus{State: swarm.NodeStateReady}},
		{ID: "node-b", Spec: swarm.NodeSpec{Availability: swarm.NodeAvailabilityActive}, Status: swarm.NodeStatus{State: swarm.NodeStateReady}},
	}

	provider := NewWithClient(client, model.Runtime{ID: "rt_swarm", Type: model.RuntimeSwarm}, "nsc-ingress", providerReadiness())
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	service := snapshot.Services[0]
	if service.DesiredInstances != 2 || service.RunningInstances != 1 || service.ReadyInstances != 1 {
		t.Fatalf("unexpected swarm counts: desired=%d running=%d ready=%d", service.DesiredInstances, service.RunningInstances, service.ReadyInstances)
	}
	if service.QualifiedName != "demo_web" || service.DNSName != "demo_web" || service.DNSStatus != model.DNSReady || !service.RoutingEligible {
		t.Fatalf("service DNS identity is not routable: %+v", service)
	}
	if len(service.Endpoints) != 2 || service.Endpoints[0].ExternalID != "task-a" || service.Endpoints[0].Address != "10.50.0.2" || service.Endpoints[1].ExternalID != "task-b" {
		t.Fatalf("unexpected task status instances: %+v", service.Endpoints)
	}
	client.nodes[1].Spec.Availability = swarm.NodeAvailabilityDrain
	snapshot, err = provider.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("draining Snapshot() error = %v", err)
	}
	if got := snapshot.Services[0]; got.ReadyInstances != 0 || len(got.Endpoints) != 2 || got.DNSStatus != model.DNSReady {
		t.Fatalf("task availability changed the stable DNS backend: %+v", got)
	}
}

func TestSwarmListsUnlabelledServiceWithStableName(t *testing.T) {
	client := newFakeDockerClient()
	replicas := uint64(0)
	client.networks = []networktypes.Summary{{ID: "network-1", Name: "nsc-ingress", Driver: "overlay", Scope: "swarm"}}
	client.services = []swarm.Service{{
		ID:   "replaceable-object-id",
		Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "demo_api"}, Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &replicas}}},
	}}
	provider := NewWithClient(client, model.Runtime{ID: "rt_swarm", Type: model.RuntimeSwarm}, "nsc-ingress", providerReadiness())
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Services) != 1 {
		t.Fatalf("unlabelled Swarm service was hidden: %#v", snapshot.Services)
	}
	service := snapshot.Services[0]
	if service.ServiceKey != "demo_api" || service.RuntimeObjectID != "replaceable-object-id" || service.Enabled || len(service.Routes) != 0 {
		t.Fatalf("unexpected stable Swarm identity: %#v", service)
	}
}

func TestSwarmUnavailableNodeIsNotEligible(t *testing.T) {
	client := newFakeDockerClient()
	replicas := uint64(1)
	client.networks = []networktypes.Summary{{ID: "network-1", Name: "nsc-ingress", Driver: "overlay", Scope: "swarm"}}
	client.services = []swarm.Service{{
		ID: "service-1",
		Spec: swarm.ServiceSpec{
			Annotations: swarm.Annotations{Name: "demo_web", Labels: map[string]string{"nsc.enable": "true", "nsc.port": "8080"}},
			Mode:        swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &replicas}},
		},
	}}
	client.tasks = []swarm.Task{{ID: "task-1", ServiceID: "service-1", NodeID: "node-1", DesiredState: swarm.TaskStateRunning, Status: swarm.TaskStatus{State: swarm.TaskStateRunning, Timestamp: time.Now().UTC()}, NetworksAttachments: swarmAttachments("network-1", "10.60.0.2/24")}}
	client.nodes = []swarm.Node{{ID: "node-1", Spec: swarm.NodeSpec{Availability: swarm.NodeAvailabilityDrain}, Status: swarm.NodeStatus{State: swarm.NodeStateReady}}}
	provider := NewWithClient(client, model.Runtime{ID: "rt_swarm", Type: model.RuntimeSwarm}, "nsc-ingress", providerReadiness())
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	service := snapshot.Services[0]
	if service.RunningInstances != 1 || service.ReadyInstances != 0 || service.Endpoints[0].RuntimeState != model.EndpointStopping || service.ConfigurationError != "" {
		t.Fatalf("unavailable node remained eligible: %+v", service)
	}
}

func TestSwarmRunningTaskNoLongerDesiredIsDraining(t *testing.T) {
	client := newFakeDockerClient()
	replicas := uint64(1)
	client.networks = []networktypes.Summary{{ID: "network-1", Name: "nsc-ingress", Driver: "overlay", Scope: "swarm"}}
	client.services = []swarm.Service{{ID: "service-1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "demo_web", Labels: map[string]string{"nsc.enable": "true", "nsc.port": "8080"}}, Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &replicas}}}}}
	client.tasks = []swarm.Task{{ID: "task-old", ServiceID: "service-1", NodeID: "node-1", DesiredState: swarm.TaskStateShutdown, Status: swarm.TaskStatus{State: swarm.TaskStateRunning, Timestamp: time.Now().UTC()}, NetworksAttachments: swarmAttachments("network-1", "10.61.0.2/24")}}
	client.nodes = []swarm.Node{{ID: "node-1", Spec: swarm.NodeSpec{Availability: swarm.NodeAvailabilityActive}, Status: swarm.NodeStatus{State: swarm.NodeStateReady}}}
	provider := NewWithClient(client, model.Runtime{ID: "rt_swarm", Type: model.RuntimeSwarm}, "nsc-ingress", providerReadiness())
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	service := snapshot.Services[0]
	if service.RunningInstances != 1 || service.ReadyInstances != 0 || len(service.Endpoints) != 1 || service.Endpoints[0].RuntimeState != model.EndpointStopping {
		t.Fatalf("rolling-update task did not enter drain: %+v", service)
	}
}

func swarmAttachments(networkID, address string) []swarm.NetworkAttachment {
	return []swarm.NetworkAttachment{{Network: swarm.Network{ID: networkID}, Addresses: []netip.Prefix{netip.MustParsePrefix(address)}}}
}

func TestSwarmRejectsSameNamedNonOverlayNetwork(t *testing.T) {
	client := newFakeDockerClient()
	client.networks = []networktypes.Summary{{ID: "network-1", Name: "nsc-ingress", Driver: "bridge", Scope: "local"}}
	client.services = []swarm.Service{{ID: "service-1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "demo_web", Labels: map[string]string{"nsc.enable": "true", "nsc.port": "8080"}}}}}
	provider := NewWithClient(client, model.Runtime{ID: "rt_swarm", Type: model.RuntimeSwarm}, "nsc-ingress", providerReadiness())
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if got := snapshot.Services[0].ConfigurationError; got != "network \"nsc-ingress\" is not a swarm overlay" {
		t.Fatalf("configuration error = %q", got)
	}
}

func TestSwarmRejectsSharedNetworkShortServiceName(t *testing.T) {
	client := newFakeDockerClient()
	client.networks = []networktypes.Summary{{ID: "network-1", Name: "nsc-ingress", Driver: "overlay", Scope: "swarm"}}
	client.services = []swarm.Service{{
		ID: "service-1",
		Spec: swarm.ServiceSpec{
			Annotations:  swarm.Annotations{Name: "api", Labels: map[string]string{"nsc.enable": "true", "nsc.port": "8080"}},
			TaskTemplate: swarm.TaskSpec{Networks: []swarm.NetworkAttachmentConfig{{Target: "network-1"}}},
		},
	}}
	provider := NewWithClient(client, model.Runtime{ID: "rt_swarm", Type: model.RuntimeSwarm}, "nsc-ingress", providerReadiness())
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	service := snapshot.Services[0]
	if service.DNSStatus != model.DNSMissing || service.RoutingEligible || service.Enabled ||
		!strings.Contains(service.ConfigurationError, "stack-qualified") {
		t.Fatalf("short Swarm service name was accepted on a shared network: %+v", service)
	}
}

func TestSwarmUnroutedShortNameDoesNotReportRouteError(t *testing.T) {
	client := newFakeDockerClient()
	client.networks = []networktypes.Summary{{ID: "network-1", Name: "nsc-ingress", Driver: "overlay", Scope: "swarm"}}
	client.services = []swarm.Service{{ID: "postgres", Spec: swarm.ServiceSpec{
		Annotations:  swarm.Annotations{Name: "nsc-controller-postgres"},
		TaskTemplate: swarm.TaskSpec{Networks: []swarm.NetworkAttachmentConfig{{Target: "network-1"}}},
	}}}
	provider := NewWithClient(client, model.Runtime{ID: "rt_swarm", Type: model.RuntimeSwarm}, "nsc-ingress", providerReadiness())
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Services[0].ConfigurationError; got != "" {
		t.Fatalf("unrouted service has route configuration error: %q", got)
	}
}

func TestSwarmGroupUsesStackLabelWithoutNameGuessing(t *testing.T) {
	client := newFakeDockerClient()
	client.networks = []networktypes.Summary{{ID: "network-1", Name: "nsc-ingress", Driver: "overlay", Scope: "swarm"}}
	client.services = []swarm.Service{{ID: "service-1", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "stack_with_underscores_web", Labels: map[string]string{"com.docker.stack.namespace": "stack_with_underscores", "nsc.port": "8080"}}}}}
	provider := NewWithClient(client, model.Runtime{ID: "rt_swarm", Type: model.RuntimeSwarm}, "nsc-ingress", providerReadiness())
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Services[0].Namespace != "stack_with_underscores" {
		t.Fatal(snapshot.Services[0])
	}
	delete(client.services[0].Spec.Labels, "com.docker.stack.namespace")
	unlabeled, err := provider.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if unlabeled.Services[0].Namespace != "" || unlabeled.Services[0].ServiceKey != snapshot.Services[0].ServiceKey || unlabeled.Services[0].DNSName != snapshot.Services[0].DNSName {
		t.Fatal(unlabeled.Services[0])
	}
}
