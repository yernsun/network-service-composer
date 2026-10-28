package nodecontrol

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestReadOnlyManagerSelectionPreservesClusterIdentityAndLease(t *testing.T) {
	s, now, _ := reportsFixture(t)
	active := map[string]bool{"node-a": true, "node-b": true}
	// Use exact lease keys without depending on their prefix spelling.
	s.CheckOwner = func(_ context.Context, key string, owner int64) error {
		for id, live := range active {
			if key == LockKey(id) && live && owner == 11 {
				return nil
			}
		}
		return ErrStale
	}
	for key, b := range s.Bindings {
		b.ClusterID = "cluster"
		s.Bindings[key] = b
	}
	sessions := map[string]Session{}
	for _, instance := range []string{"controller-a", "controller-b"} {
		b := s.Bindings[instance]
		session, err := s.Begin(t.Context(), instance, b.NodeID, b.EngineID, "boot", 11)
		if err != nil {
			t.Fatal(err)
		}
		sessions[instance] = session
	}
	publish := func(instance string, complete bool, sequence uint64) {
		b := s.Bindings[instance]
		if err := s.Accept(t.Context(), instance, Report{NodeID: b.NodeID, EngineID: b.EngineID, Generation: sessions[instance].Generation, Sequence: sequence, Observation: Observation{At: *now, Complete: complete, Info: system.Info{ID: b.EngineID, Swarm: swarm.Info{NodeID: b.NodeID, ControlAvailable: true, Cluster: &swarm.ClusterInfo{ID: "cluster"}}}}}); err != nil {
			t.Fatal(err)
		}
	}
	publish("controller-a", true, 1)
	*now = now.Add(time.Second)
	publish("controller-b", true, 1)
	rt := model.Runtime{Type: model.RuntimeSwarm, Endpoint: "node://node-a", ClusterID: "cluster", ManagementEnabled: false}

	s.Bindings["controller-c"] = Binding{InstanceID: "controller-c", NodeID: "node-c", EngineID: "engine-c", ClusterID: "cluster"}
	s.Bindings["controller-d"] = Binding{InstanceID: "controller-d", NodeID: "node-d", EngineID: "engine-d", ClusterID: "cluster"}
	selected, err := s.ManagerBinding(t.Context(), rt)
	if err != nil || selected.NodeID != "node-a" {
		t.Fatal("newer report displaced available original Manager", selected, err)
	}
	active["node-a"] = false
	selected, err = s.ManagerBinding(t.Context(), rt)
	if err != nil || selected.NodeID != "node-b" {
		t.Fatal("same-cluster inventory did not survive original Manager loss", selected, err)
	}
	for _, changed := range []model.Runtime{{Type: model.RuntimeDocker, Endpoint: rt.Endpoint, ClusterID: rt.ClusterID}, {Type: rt.Type, Endpoint: "unix:///var/run/docker.sock", ClusterID: rt.ClusterID}, {Type: rt.Type, Endpoint: rt.Endpoint}, {Type: rt.Type, Endpoint: rt.Endpoint, ClusterID: "foreign"}, {Type: rt.Type, Endpoint: "node://missing", ClusterID: rt.ClusterID}, {Type: rt.Type, Endpoint: "node://", ClusterID: rt.ClusterID}} {
		if _, err := s.ManagerBinding(t.Context(), changed); !errors.Is(err, ErrIdentity) {
			t.Fatal("foreign identity selected", changed, err)
		}
	}
	b := s.Bindings["controller-b"]
	b.EngineID = "replacement-engine"
	s.Bindings["controller-b"] = b
	if _, err = s.ManagerBinding(t.Context(), rt); !errors.Is(err, ErrIncomplete) {
		t.Fatal("replacement Engine supplied inventory", err)
	}
	b.EngineID = "engine-b"
	s.Bindings["controller-b"] = b
	publish("controller-b", false, 2)
	if _, err = s.ManagerBinding(t.Context(), rt); !errors.Is(err, ErrIncomplete) {
		t.Fatal("incomplete report selected", err)
	}

	for index, mode := range []string{"no-control", "no-cluster"} {
		b := s.Bindings["controller-b"]
		info := system.Info{ID: b.EngineID, Swarm: swarm.Info{NodeID: b.NodeID, ControlAvailable: mode != "no-control"}}
		if mode != "no-cluster" {
			info.Swarm.Cluster = &swarm.ClusterInfo{ID: "cluster"}
		}
		if err = s.Accept(t.Context(), "controller-b", Report{NodeID: b.NodeID, EngineID: b.EngineID, Generation: sessions["controller-b"].Generation, Sequence: uint64(3 + index), Observation: Observation{At: *now, Complete: true, Info: info}}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.ManagerBinding(t.Context(), rt); !errors.Is(err, ErrIncomplete) {
			t.Fatal("worker or missing cluster supplied Manager evidence", mode, err)
		}
	}
	publish("controller-b", true, 5)
	*now = now.Add(31 * time.Second)
	if _, err = s.ManagerBinding(t.Context(), rt); !errors.Is(err, ErrIncomplete) {
		t.Fatal("retained old inventory selected", err)
	}
}
