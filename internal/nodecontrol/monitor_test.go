package nodecontrol

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/cluster"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/hoststats"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/runtime/engine/enginetest"
)

func TestFourNodeInventorySurvivesReaderRestartAndOfflineCollectors(t *testing.T) {
	s, now, owner := reportsFixture(t)
	s.CheckOwner = func(context.Context, string, int64) error { return nil }
	s.Bindings = map[string]Binding{}
	store := &enginetest.Store{Runtimes: []model.Runtime{{ID: "swarm", Type: model.RuntimeSwarm, Endpoint: "node://node-0", Enabled: true}}}
	ctx := context.Background()
	nodes := []swarm.Node{}
	for i := 0; i < 4; i++ {
		node := fmt.Sprintf("node-%d", i)
		nodes = append(nodes, swarm.Node{ID: node, Description: swarm.NodeDescription{Hostname: "same-hostname"}, Status: swarm.NodeStatus{State: swarm.NodeStateReady}})
	}
	for i := 0; i < 4; i++ {
		b := Binding{InstanceID: fmt.Sprintf("controller-%d", i), NodeID: nodes[i].ID, EngineID: fmt.Sprintf("engine-%d", i), ClusterID: "cluster-one"}
		s.Bindings[b.InstanceID] = b
		store.Runtimes = append(store.Runtimes, model.Runtime{ID: b.NodeID, Type: model.RuntimeDocker, Endpoint: "node://" + b.NodeID, Enabled: true})
		session, err := s.Begin(ctx, b.InstanceID, b.NodeID, b.EngineID, "boot", *owner)
		if err != nil {
			t.Fatal(err)
		}
		o := observationFixture(*now)
		o.Info.ID, o.Info.Swarm.NodeID = b.EngineID, b.NodeID
		o.Info.Name = "same-hostname"
		cpu, memory := float64(i), uint64(1024)
		o.Host = &hoststats.Metrics{At: *now, CPUPercent: &cpu, MemoryUsed: &memory}
		o.Health = &hoststats.Health{At: *now, InstanceID: b.InstanceID, Controller: "healthy", Collector: "healthy", Role: "follower", Gateway: "ready"}
		o.Containers = []container.Summary{{ID: "container-" + b.NodeID, Names: []string{"/same-container-name"}, State: container.StateRunning}}
		if i == 0 {
			o.Info.Swarm.ControlAvailable = true
			o.Info.Swarm.Cluster = &swarm.ClusterInfo{ID: b.ClusterID}
			o.Nodes = nodes
		}
		if err := s.Accept(ctx, b.InstanceID, Report{NodeID: b.NodeID, EngineID: b.EngineID, Generation: session.Generation, Sequence: 1, Observation: o}); err != nil {
			t.Fatal(err)
		}
	}
	read := func() cluster.Inventory {
		t.Helper()
		// A fresh reader has no previous in-memory inventory and no elected-leader role.
		monitor := cluster.NewWithFactory(store, config.Defaults(), func(rt model.Runtime, _ engine.TLSFiles) (engine.MonitoringClient, error) {
			return s.MonitoringClient(ctx, rt)
		})
		if err := monitor.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
		inventory, err := monitor.Inventory(ctx, "swarm", false)
		if err != nil {
			t.Fatal(err)
		}
		return inventory
	}
	for i := 0; i < 2; i++ {
		inventory := read()
		if inventory.Overview.ConnectedNodes != 4 || !inventory.Overview.InventoryComplete || len(inventory.Instances) != 4 {
			t.Fatalf("incomplete shared view: %+v", inventory.Overview)
		}
		for _, instance := range inventory.Instances {
			if instance.ContainerID != "container-"+instance.NodeID || instance.EngineID == "" {
				t.Fatal("name collision changed container ownership", instance)
			}
		}
		for _, node := range inventory.Nodes {
			if node.Host == nil || node.Host.CPUPercent == nil || node.Host.Stale || node.Controller == nil || node.Controller.InstanceID != "controller-"+node.ID[len("node-"):] || node.Controller.Stale {
				t.Fatal("node telemetry lost or assigned by hostname", node.ID)
			}
		}
	}
	*now = now.Add(31 * time.Second)
	stale := read()
	if len(stale.Instances) != 4 || !stale.Overview.Stale || stale.Overview.InventoryComplete || stale.Overview.ConnectedNodes != 0 {
		t.Fatalf("offline observations became empty/healthy after reader restart: %+v", stale.Overview)
	}
	for _, node := range stale.Nodes {
		if !node.Stale {
			t.Fatal("offline node shown current", node.ID)
		}
		if node.Host == nil || !node.Host.Stale || node.Controller == nil || !node.Controller.Stale || node.Controller.Controller != "unavailable" {
			t.Fatal("expired node telemetry remained healthy", node.ID)
		}
	}
}
