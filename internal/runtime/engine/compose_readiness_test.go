package engine

import (
	"context"
	"errors"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	containertypes "github.com/moby/moby/api/types/container"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestComposeSnapshotNativeReadinessRequiresCurrentRunningHealthEvidence(t *testing.T) {
	for _, scenario := range []string{"healthy", "no healthcheck", "health starting", "unhealthy", "unknown health", "missing inspect state", "created", "restarting", "exited", "inspection unavailable", "disappeared", "missing Gateway network"} {
		t.Run(scenario, func(t *testing.T) {
			client := newFakeDockerClient()
			labels := map[string]string{"com.docker.compose.project": "owned", "com.docker.compose.service": "web", "nsc.port": "8080"}
			client.containers = []containertypes.Summary{{ID: "owned-container", State: containertypes.StateRunning, Labels: labels}}
			inspect := composeInspectWithPort("10.42.0.2", 8080)
			inspect.NetworkSettings.Networks["nsc-ingress"].Aliases = []string{"owned_web"}
			wantReady, wantRunning := 0, 1
			switch scenario {
			case "healthy":
				wantReady = 1
			case "no healthcheck":
				inspect.State.Health = nil
				wantReady = 1
			case "health starting":
				inspect.State.Health.Status = containertypes.Starting
			case "unhealthy":
				inspect.State.Health.Status = containertypes.Unhealthy
			case "unknown health":
				inspect.State.Health.Status = "unrecognized"
			case "missing inspect state":
				inspect.State = nil
			case "created":
				inspect.State.Status, wantRunning = containertypes.StateCreated, 0
			case "restarting":
				inspect.State.Status, wantRunning = containertypes.StateRestarting, 0
			case "exited":
				inspect.State.Status, wantRunning = containertypes.StateExited, 0
			case "inspection unavailable":
				client.inspectErrs["owned-container"] = errors.New("bounded inspection unavailable")
				wantRunning = 0
			case "disappeared":
				client.inspectErrs["owned-container"] = cerrdefs.ErrNotFound
			case "missing Gateway network":
				inspect.NetworkSettings = nil
				wantReady = 1
			}
			client.inspections["owned-container"] = inspect
			provider := NewWithClient(client, model.Runtime{ID: "owned-runtime", Type: model.RuntimeDocker}, "nsc-ingress", providerReadiness())
			snapshot, err := provider.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "disappeared" {
				if len(snapshot.Services) != 0 {
					t.Fatal("disappeared replica retained a convergence claim", snapshot.Services)
				}
				return
			}
			if len(snapshot.Services) != 1 {
				t.Fatal("current Service identity was lost", snapshot.Services)
			}
			service := snapshot.Services[0]
			if service.DesiredInstances != 1 || service.RunningInstances != wantRunning || service.ReadyInstances != wantReady {
				t.Fatal("native readiness was guessed or healthy convergence remained pending", service)
			}
			if scenario == "missing Gateway network" && service.RoutingEligible {
				t.Fatal("native convergence incorrectly established Gateway reachability")
			}
			if scenario == "healthy" && !service.RoutingEligible {
				t.Fatal("healthy fixture lost its separately observed Gateway network")
			}
		})
	}
}

func TestComposeSnapshotNativeReadinessPreservesPartialReplicaAndZeroWorkloadEvidence(t *testing.T) {
	client := newFakeDockerClient()
	labels := map[string]string{"com.docker.compose.project": "owned", "com.docker.compose.service": "web", "nsc.port": "8080"}
	for _, name := range []string{"healthy", "starting", "unhealthy", "unknown"} {
		client.containers = append(client.containers, containertypes.Summary{ID: name, State: containertypes.StateRunning, Labels: labels})
		inspect := composeInspectWithPort("10.42.0.2", 8080)
		inspect.NetworkSettings.Networks["nsc-ingress"].Aliases = []string{"owned_web"}
		switch name {
		case "starting":
			inspect.State.Health.Status = containertypes.Starting
		case "unhealthy":
			inspect.State.Health.Status = containertypes.Unhealthy
		case "unknown":
			inspect.State = nil
		}
		client.inspections[name] = inspect
	}
	provider := NewWithClient(client, model.Runtime{ID: "owned-runtime", Type: model.RuntimeDocker}, "nsc-ingress", providerReadiness())
	snapshot, err := provider.Snapshot(context.Background())
	if err != nil || len(snapshot.Services) != 1 || snapshot.Services[0].DesiredInstances != 4 || snapshot.Services[0].RunningInstances != 4 || snapshot.Services[0].ReadyInstances != 1 {
		t.Fatal("one healthy replica concealed unconverged siblings", snapshot.Services, err)
	}
	client.containers = nil
	snapshot, err = provider.Snapshot(context.Background())
	if err != nil || len(snapshot.Services) != 0 {
		t.Fatal("an empty Compose inventory invented a ready zero-replica Service", snapshot.Services, err)
	}
}
