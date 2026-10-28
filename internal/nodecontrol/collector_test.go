package nodecontrol

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/runtime/engine/enginetest"
)

type collectingEngine struct {
	*enginetest.Client
	statsFailure bool
	closed       bool
}

func (c *collectingEngine) ContainerStats(ctx context.Context, id string) (container.StatsResponse, error) {
	if c.statsFailure {
		return container.StatsResponse{}, errors.New("stats unavailable")
	}
	return c.Client.ContainerStats(ctx, id)
}
func (c *collectingEngine) Close() error { c.closed = true; return nil }

func TestLocalCollectorUsesEngineIdentityAndKeepsMissingMetricsExplicit(t *testing.T) {
	for _, mode := range []string{"manager", "worker", "missing metrics", "wrong engine", "wrong node", "wrong cluster", "missing engine", "missing node", "quorum metadata unavailable", "offline"} {
		t.Run(mode, func(t *testing.T) {
			_, fixture := enginetest.Fixture()
			client := &collectingEngine{Client: fixture}
			collector := Collector{Binding: Binding{NodeID: "node-a", EngineID: "engine-local", ClusterID: "cluster-a"}, Open: func() (engine.MonitoringClient, error) { return client, nil }}
			var want error
			switch mode {
			case "worker":
				fixture.System.Swarm.ControlAvailable = false
				fixture.System.Swarm.Cluster = nil
			case "missing metrics":
				client.statsFailure = true
			case "missing engine":
				fixture.System.ID = ""
				want = ErrIncomplete
			case "missing node":
				fixture.System.Swarm.NodeID = ""
				want = ErrIncomplete
			case "quorum metadata unavailable":
				fixture.System.Swarm.Cluster.ID = ""
				want = ErrIncomplete
			case "wrong engine":
				collector.Binding.EngineID = "another"
				want = ErrIdentity
			case "wrong node":
				collector.Binding.NodeID = "another"
				want = ErrIdentity
			case "wrong cluster":
				collector.Binding.ClusterID = "another"
				want = ErrIdentity
			case "offline":
				want = errors.New("engine offline")
				fixture.Failure = want
			}
			observation, err := collector.Collect(context.Background())
			if !errors.Is(err, want) || !client.closed {
				t.Fatalf("collection result: %v, closed=%v", err, client.closed)
			}
			if want != nil {
				if observation.Complete {
					t.Fatal("failed collection marked complete")
				}
				return
			}
			if !observation.Complete || observation.At.IsZero() || len(observation.Containers) != 3 || observation.Info.Swarm.NodeID != "node-a" {
				t.Fatal("local inventory missing")
			}
			if mode == "worker" {
				if len(observation.Nodes)+len(observation.Services)+len(observation.Tasks) != 0 {
					t.Fatal("worker fabricated Manager inventory")
				}
			} else if len(observation.Services) != 2 || len(observation.Nodes) != 2 {
				t.Fatal("Manager inventory missing")
			}
			reader := &Client{Value: observation}
			_, err = reader.ContainerStats(context.Background(), "container-a")
			if mode == "missing metrics" {
				if !errors.Is(err, ErrIncomplete) || len(observation.Stats) != 0 {
					t.Fatal("missing metrics became zero samples", err)
				}
			} else if err != nil || len(observation.Stats) != 2 {
				t.Fatal("running-container metrics missing", err)
			}
			if _, err := reader.ContainerStats(context.Background(), "independent"); !errors.Is(err, ErrIncomplete) {
				t.Fatal("stopped container received fabricated metrics")
			}
		})
	}
}

func TestNodeEventQueueOnlyAdmitsTimelineEvidence(t *testing.T) {
	for _, tc := range []struct {
		actor, action string
		wanted        bool
	}{
		{"container", "die", true}, {"container", "oom", true},
		{"container", "health_status: unhealthy", true},
		{"task", "update", true}, {"service", "remove", true},
		{"container", "attach", false}, {"container", "exec_start", false},
		{"network", "create", false}, {"task", "health_status: unhealthy", false},
	} {
		if got := wantedDockerEvent(tc.actor, tc.action); got != tc.wanted {
			t.Errorf("%s/%s admitted=%t, want %t", tc.actor, tc.action, got, tc.wanted)
		}
	}
}

func TestNodeEventReportFailureMetricsAreBoundedAndContentFree(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{reportStatusError(http.StatusForbidden), "identity_rejected"},
		{reportStatusError(http.StatusConflict), "ownership_lost"},
		{reportStatusError(http.StatusServiceUnavailable), "storage_unavailable"},
		{errors.New("transport failure containing arbitrary content"), "transport_failed"},
	} {
		if got := reportFailureClass(tc.err); got != tc.want {
			t.Errorf("failure class %q, want %q", got, tc.want)
		}
	}
}
