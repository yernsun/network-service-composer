package cluster

import (
	"context"
	"errors"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/runtime/engine/enginetest"
	"github.com/yernsun/network-service-composer/internal/storage"
	"reflect"
	"testing"
	"time"
)

func fixtureMonitor() (*Monitor, *enginetest.Store, *enginetest.Client) {
	s, c := enginetest.Fixture()
	cfg := config.Defaults()
	cfg.Management.Enabled = true
	m := NewWithFactory(s, cfg, func(model.Runtime, engine.TLSFiles) (engine.MonitoringClient, error) { return c, nil })
	return m, s, c
}
func TestInventoryIdentityCoverageAndDeduplication(t *testing.T) {
	m, s, c := fixtureMonitor()
	duplicate := s.Runtimes[0]
	duplicate.ID = "rt-other"
	duplicate.Endpoint = "tcp://same-daemon:2376"
	s.Runtimes = append(s.Runtimes, duplicate)
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.StatsCalls != 2 {
		t.Fatalf("sampled duplicate engine: %d", c.StatsCalls)
	}
	v, err := m.Inventory(context.Background(), "rt-cluster", false)
	if err != nil {
		t.Fatal(err)
	}
	if v.Overview.Nodes != 2 || v.Overview.ConnectedNodes != 1 || v.Overview.Instances != 4 || v.Overview.InventoryComplete {
		t.Fatalf("overview: %+v", v.Overview)
	}
	a, _ := v.Node("node-a")
	b, _ := v.Node("node-b")
	if !a.Connected || b.Connected || a.Name != b.Name {
		t.Fatalf("hostname used as identity: %+v %+v", a, b)
	}
	seen := map[string]bool{}
	for _, i := range v.Instances {
		if seen[i.ID] {
			t.Fatal("duplicate instance")
		}
		seen[i.ID] = true
		if i.TaskID == "task-a" && i.EngineID != "engine-local" {
			t.Fatal("task/container not joined")
		}
		if i.TaskID == "task-b" && i.EngineID != "" {
			t.Fatal("remote task attributed to local engine")
		}
	}
	if _, err = v.Node("unassigned"); err != nil {
		t.Fatal("zero replica service missing")
	}
	history, _ := m.Inventory(context.Background(), "rt-cluster", true)
	if len(history.Instances) != 5 {
		t.Fatal("historical tasks missing")
	}
	c.Failure = errors.New("offline")
	if err = m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	v, _ = m.Inventory(context.Background(), "rt-cluster", false)
	if !v.Overview.Stale || len(v.Instances) != 4 {
		t.Fatal("failed poll dropped inventory")
	}
	if metric := m.Metrics("engine-local", "container-a"); metric.Current == nil || !metric.Current.Stale {
		t.Fatal("daemon failure kept metrics current", metric)
	}
}
func TestNodeLabelOptimisticConcurrencyAndBoundaries(t *testing.T) {
	m, s, c := fixtureMonitor()
	ctx := context.Background()
	_ = m.Refresh(ctx)
	original := c.Nodes[0].Spec
	result, err := m.UpdateLabels(ctx, "rt-cluster", "node-a", 7, map[string]string{"zone": "b", "empty": ""})
	if err != nil || result.Version != 8 || result.Labels["zone"] != "b" {
		t.Fatalf("update %+v %v", result, err)
	}
	if c.Nodes[0].Spec.Role != original.Role || c.Nodes[0].Spec.Availability != original.Availability {
		t.Fatal("unrelated node spec changed")
	}
	if _, err = m.UpdateLabels(ctx, "rt-cluster", "node-a", 7, nil); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("stale precondition accepted", err)
	}
	c.Race = true
	if _, err = m.UpdateLabels(ctx, "rt-cluster", "node-a", 8, nil); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("Docker update race not mapped", err)
	}
	if c.Updates != 1 || c.Nodes[0].Spec.Availability != swarm.NodeAvailabilityPause || !reflect.DeepEqual(c.Nodes[0].Spec.Labels, map[string]string{"zone": "b", "empty": ""}) {
		t.Fatal("racing update overwritten")
	}
	s.Runtimes[0].ManagementEnabled = false
	if _, err = m.UpdateLabels(ctx, "rt-cluster", "node-a", 9, nil); !errors.Is(err, ErrReadOnly) {
		t.Fatal("runtime management ignored")
	}
	s.Runtimes[0].ManagementEnabled = true
	m.config.Management.Enabled = false
	if _, err = m.UpdateLabels(ctx, "rt-cluster", "node-a", 9, nil); !errors.Is(err, ErrReadOnly) {
		t.Fatal("global management ignored")
	}
	m.config.Management.Enabled = true
	c.System.Swarm.ControlAvailable = false
	if _, err = m.UpdateLabels(ctx, "rt-cluster", "node-a", 9, nil); !errors.Is(err, ErrReadOnly) {
		t.Fatal("worker mutation allowed")
	}
}
func TestMetricCalculationGapsAndRetention(t *testing.T) {
	now := time.Now().UTC()
	current := container.StatsResponse{CPUStats: container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 150}, SystemUsage: 1000, OnlineCPUs: 4}, MemoryStats: container.MemoryStats{Usage: 1000, Limit: 2000, Stats: map[string]uint64{"inactive_file": 300}}}
	p := sample(current, nil, now)
	if p.CPUPercent != nil || *p.MemoryUsage != 700 || *p.MemoryRaw != 1000 {
		t.Fatal(p)
	}
	prev := current
	prev.CPUStats.CPUUsage.TotalUsage = 100
	prev.CPUStats.SystemUsage = 800
	p = sample(current, &prev, now)
	if p.CPUPercent == nil || *p.CPUPercent != 100 {
		t.Fatal(p)
	}
	current.CPUStats.CPUUsage.TotalUsage = 1
	if sample(current, &prev, now).CPUPercent != nil {
		t.Fatal("counter reset produced a spike")
	}
	current.MemoryStats.Stats = map[string]uint64{"total_inactive_file": 100, "inactive_file": 300}
	if *sample(current, nil, now).MemoryUsage != 900 {
		t.Fatal("cgroup v1 accounting")
	}
	m, _, _ := fixtureMonitor()
	m.now = func() time.Time { return now }
	s := &series{}
	s.append(p, now)
	s.append(Metric{At: now.Add(time.Second), Error: "collection_failed"}, now.Add(time.Second))
	m.metrics["e/c"] = s
	metrics := m.Metrics("e", "c")
	if metrics.Current == nil || !metrics.Current.Stale || len(metrics.Samples) != 2 || metrics.Samples[1].CPUPercent != nil {
		t.Fatal("gap or last good sample lost", metrics)
	}
	now = now.Add(2 * time.Hour)
	m.prune(now)
	if len(m.metrics) != 0 {
		t.Fatal("series not expired")
	}
	for i := 0; i < 400; i++ {
		s.append(Metric{At: now.Add(time.Duration(i) * time.Second)}, now.Add(time.Duration(i)*time.Second))
	}
	if len(s.Points) != 360 {
		t.Fatal("unbounded series")
	}
}
func TestSeparateDockerEnginesAndServiceReferences(t *testing.T) {
	m, s, c := fixtureMonitor()
	r := model.Runtime{ID: "rt-docker", Enabled: true, Type: model.RuntimeDocker, Endpoint: "unix:///another.sock"}
	s.Runtimes = append(s.Runtimes, r)
	_, other := enginetest.Fixture()
	other.System.ID = "another-engine"
	other.System.Swarm = swarm.Info{}
	other.Containers = other.Containers[1:2]
	s.Services = []model.Service{{ID: "svc-compose", RuntimeID: r.ID, ServiceKey: "shop/db"}, {ID: "svc-swarm", RuntimeID: "rt-cluster", RuntimeObjectID: "service-web"}}
	m.factory = func(rt model.Runtime, _ engine.TLSFiles) (engine.MonitoringClient, error) {
		if rt.ID == r.ID {
			return other, nil
		}
		return c, nil
	}
	_ = m.Refresh(context.Background())
	v, _ := m.Inventory(context.Background(), r.ID, false)
	if v.Overview.Nodes != 1 || v.Overview.ID != "another-engine" || len(v.Instances) != 1 || v.Instances[0].ServiceID != "svc-compose" {
		t.Fatalf("docker inventory %+v", v)
	}
	v, _ = m.Inventory(context.Background(), "rt-cluster", false)
	for _, i := range v.Instances {
		if i.TaskID == "task-a" && i.ServiceID != "svc-swarm" {
			t.Fatal("swarm reference missing")
		}
		if i.ContainerID == "compose-a" && i.ServiceID != "" {
			t.Fatal("cross-runtime compose identity merged")
		}
	}
}

func TestNodeAddressUsesManagerWhenStatusIsUnspecified(t *testing.T) {
	for _, tc := range []struct{ status, manager, want string }{{"0.0.0.0", "192.0.2.43:2377", "192.0.2.43"}, {"::", "[2001:db8::1]:2377", "2001:db8::1"}, {"192.0.2.2", "192.0.2.43:2377", "192.0.2.2"}} {
		node := swarm.Node{Status: swarm.NodeStatus{Addr: tc.status}, ManagerStatus: &swarm.ManagerStatus{Addr: tc.manager}}
		if got := swarmNode(node, time.Now()).Address; got != tc.want {
			t.Fatalf("address=%s want %s", got, tc.want)
		}
	}
}
