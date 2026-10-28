package nodecontrol

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestSwarmReadsFreshManagerWhileComposeStaysOnItsNode(t *testing.T) {
	s, now, owner := reportsFixture(t)
	ctx := context.Background()
	s.CheckOwner = func(context.Context, string, int64) error { return nil }
	for instance, b := range s.Bindings {
		b.ClusterID = "cluster-one"
		s.Bindings[instance] = b
		session, err := s.Begin(ctx, instance, b.NodeID, b.EngineID, "boot", *owner)
		if err != nil {
			t.Fatal(err)
		}
		observation := observationFixture(*now)
		observation.Info.ID = b.EngineID
		observation.Info.Swarm.NodeID = b.NodeID
		observation.Info.Swarm.ControlAvailable = true
		observation.Info.Swarm.Cluster = &swarm.ClusterInfo{ID: b.ClusterID}
		if b.NodeID == "node-a" {
			observation.At = now.Add(-25 * time.Second)
		}
		if err := s.Accept(ctx, instance, Report{NodeID: b.NodeID, EngineID: b.EngineID, Generation: session.Generation, Sequence: 1, Observation: observation}); err != nil {
			t.Fatal(err)
		}
	}
	*now = now.Add(10 * time.Second)
	for _, rt := range []model.Runtime{{Type: model.RuntimeSwarm, Endpoint: "node://node-a"}, {Type: model.RuntimeSwarm, Endpoint: "node://node-b"}} {
		reader, err := s.Client(ctx, rt)
		if err != nil || reader.Value.Info.ID != "engine-b" {
			t.Fatal("Manager failover lost cluster view", err)
		}
		if reader.ObservedAt().Equal(*now) {
			t.Fatal("query refreshed the observation timestamp")
		}
	}
	if _, err := s.Client(ctx, model.Runtime{Type: model.RuntimeDocker, Endpoint: "node://node-a"}); !errors.Is(err, ErrStale) {
		t.Fatal("Compose moved to another node", err)
	}
	for _, endpoint := range []string{"unix:///var/run/docker.sock", "node://node-a/other", "node://unknown", "node://node-a?anything"} {
		if _, err := s.Client(ctx, model.Runtime{Endpoint: endpoint}); !errors.Is(err, ErrIdentity) {
			t.Fatal("unbound endpoint accepted", endpoint, err)
		}
	}
	*now = now.Add(31 * time.Second)
	if _, err := s.Client(ctx, model.Runtime{Type: model.RuntimeSwarm, Endpoint: "node://node-a"}); !errors.Is(err, ErrIncomplete) {
		t.Fatal("stale Manager used as live cluster evidence", err)
	}
}
