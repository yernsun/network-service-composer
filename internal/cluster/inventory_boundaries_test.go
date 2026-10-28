package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestInventoryAvailabilityAndManagementEvidence(t *testing.T) {
	for _, stage := range []string{"missing runtime", "disabled runtime", "no observation", "empty engine", "worker", "standalone", "missing cluster", "management disabled", "runtime management disabled", "stale observation", "failed observation"} {
		t.Run(stage, func(t *testing.T) {
			m, s, c := fixtureMonitor()
			ctx := context.Background()
			if e := m.Refresh(ctx); e != nil {
				t.Fatal(e)
			}
			rt := s.Runtimes[0]
			key := connection(rt)
			src := m.sources[key]
			runtimeID := rt.ID
			switch stage {
			case "missing runtime":
				runtimeID = "missing"
			case "disabled runtime":
				s.Runtimes[0].Enabled = false
			case "no observation":
				delete(m.sources, key)
			case "empty engine":
				src.Info.ID = ""
				m.sources[key] = src
			case "worker":
				src.Info.Swarm.ControlAvailable = false
				m.sources[key] = src
			case "standalone":
				s.Runtimes[0].Type = model.RuntimeDocker
			case "missing cluster":
				src.Info.Swarm.Cluster = nil
				m.sources[key] = src
			case "management disabled":
				m.config.Management.Enabled = false
			case "runtime management disabled":
				s.Runtimes[0].ManagementEnabled = false
			case "stale observation":
				src.At = m.now().Add(-time.Minute)
				m.sources[key] = src
			case "failed observation":
				src.Error = "disconnected"
				m.sources[key] = src
			}
			inv, e := m.Inventory(ctx, runtimeID, false)
			if stage == "missing runtime" {
				if !errors.Is(e, storage.ErrNotFound) {
					t.Fatal(e)
				}
				return
			}
			if stage == "disabled runtime" || stage == "no observation" || stage == "empty engine" {
				if !errors.Is(e, ErrUnavailable) || len(inv.Instances) != 0 {
					t.Fatal("missing evidence presented as inventory", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			n, e := inv.Node("node-a")
			if e != nil {
				t.Fatal(e)
			}
			switch stage {
			case "worker":
				if inv.Overview.Error != "manager_required" || n.CanEditLabels || n.ReadOnlyReason != "manager_required" {
					t.Fatal("worker claimed cluster management")
				}
			case "standalone":
				if n.CanEditLabels || n.ReadOnlyReason != "not_swarm" || inv.Overview.ID != c.System.ID {
					t.Fatal("standalone claimed Swarm authority")
				}
			case "management disabled", "runtime management disabled":
				if n.CanEditLabels || n.ReadOnlyReason != "management_disabled" {
					t.Fatal("disabled management allowed labels")
				}
			case "stale observation", "failed observation":
				if !inv.Overview.Stale || !n.Stale || n.InventoryComplete {
					t.Fatal("expired or failed evidence remained current")
				}
			}
		})
	}
}

func TestInventoryUsesLatestManagerWithoutExpandingWorkerAuthority(t *testing.T) {
	for _, worker := range []bool{false, true} {
		t.Run(map[bool]string{false: "old manager", true: "selected worker"}[worker], func(t *testing.T) {
			m, s, _ := fixtureMonitor()
			ctx := context.Background()
			if e := m.Refresh(ctx); e != nil {
				t.Fatal(e)
			}
			key := connection(s.Runtimes[0])
			base := m.sources[key]
			base.Info.Swarm.ControlAvailable = !worker
			base.At = m.now().Add(-10 * time.Second)
			m.sources[key] = base
			latest := base
			latest.Info.ID = "engine-b"
			latest.Info.Swarm.NodeID = "node-b"
			latest.Info.Swarm.ControlAvailable = true
			cluster := *latest.Info.Swarm.Cluster
			cluster.Spec.Name = "latest-cluster"
			latest.Info.Swarm.Cluster = &cluster
			latest.At = m.now()
			latest.Containers = nil
			m.sources["other-manager"] = latest
			unknown := source{}
			m.sources["unobserved"] = unknown
			inv, e := m.Inventory(ctx, s.Runtimes[0].ID, false)
			if e != nil {
				t.Fatal(e)
			}
			if inv.Overview.Name != "latest-cluster" || !inv.Overview.ObservedAt.Equal(latest.At) {
				t.Fatal("new manager evidence not selected")
			}
			n, e := inv.Node("node-a")
			if e != nil || !n.ObservedAt.Equal(base.At) {
				t.Fatal("node evidence time falsely advanced", e)
			}
			if n.CanEditLabels == worker {
				t.Fatal("manager observation changed selected runtime authorization")
			}
		})
	}
}

func TestInventoryTaskJoinsHistoryAndRuntimeReferenceIsolation(t *testing.T) {
	for _, history := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "history"}[history], func(t *testing.T) {
			m, s, c := fixtureMonitor()
			ctx := context.Background()
			c.Tasks[0].Status.ContainerStatus = nil // Task labels remain a valid join when a task omits container status.
			c.Tasks[0].Spec.ContainerSpec = &swarm.ContainerSpec{Image: "example.test/web:v1"}
			c.Tasks[1].NodeID = ""
			c.Tasks[1].ServiceID = "unknown-service"
			c.Tasks[1].Spec.ContainerSpec = &swarm.ContainerSpec{Image: "example.test/unknown:v1"}
			c.Tasks[1].NetworksAttachments = []swarm.NetworkAttachment{{Addresses: []netip.Prefix{netip.MustParsePrefix("10.1.0.2/24")}}}
			c.Tasks[2].DesiredState = swarm.TaskStateRemove
			c.Services[0].Endpoint.Ports = []swarm.PortConfig{{PublishedPort: 443, TargetPort: 8443, Protocol: "tcp"}}
			c.Containers = append(c.Containers,
				container.Summary{ID: "retained", Labels: map[string]string{"com.docker.swarm.task.id": "task-old", "com.docker.swarm.service.id": "service-web"}},
				container.Summary{ID: "unjoined", Labels: map[string]string{"com.docker.swarm.service.id": "unknown", "com.docker.swarm.service.name": "fallback-name"}},
				container.Summary{ID: "wrong-node", Labels: map[string]string{"com.docker.swarm.task.id": "task-b", "com.docker.swarm.service.id": "service-web"}},
				container.Summary{ID: "partial-compose", Labels: map[string]string{"com.docker.compose.project": "shop"}})
			alias := s.Runtimes[0]
			alias.ID = "rt-alias"
			s.Runtimes = append(s.Runtimes, alias)
			docker := alias
			docker.ID = "rt-docker"
			docker.Type = model.RuntimeDocker
			s.Runtimes = append(s.Runtimes, docker)
			s.Services = []model.Service{{ID: "linked-swarm", RuntimeID: alias.ID, ServiceKey: "service-web"}, {ID: "linked-compose", RuntimeID: docker.ID, ServiceKey: "shop/db"}}
			if e := m.Refresh(ctx); e != nil {
				t.Fatal(e)
			}
			m.details["engine-local/container-a"] = containerDetail{Health: "unhealthy", Error: "check failed", At: m.now()}
			inv, e := m.Inventory(ctx, "rt-cluster", history)
			if e != nil {
				t.Fatal(e)
			}
			joined, retained, fallback, unassigned := 0, 0, false, false
			for _, i := range inv.Instances {
				if i.ContainerID == "container-a" {
					joined++
					if i.TaskID != "task-a" || i.ServiceID != "linked-swarm" || i.Health != "unhealthy" || i.Error != "check failed" {
						t.Fatalf("task join lost evidence: %+v", i)
					}
				}
				if i.ContainerID == "retained" {
					retained++
				}
				if i.TaskID == "task-b" {
					unassigned = true
					if i.NodeID != "unassigned" || i.Image != "example.test/unknown:v1" || len(i.Addresses) != 1 {
						t.Fatal("unassigned task evidence lost")
					}
				}
				if i.ContainerID == "compose-a" && i.ServiceID != "linked-compose" {
					t.Fatal("same-engine compose service not linked")
				}
			}
			for _, w := range inv.Workloads {
				if w.ID == "swarm:unknown" {
					fallback = w.Name == "fallback-name"
				}
			}
			if joined != 1 || !unassigned || !fallback {
				t.Fatal("inventory duplicated or lost workload identity")
			}
			if !history && retained != 0 {
				t.Fatal("removed task reintroduced as standalone container")
			}
		})
	}
}

func TestInventoryPreservesDockerLifecycleSemantics(t *testing.T) {
	m, s, c := fixtureMonitor()
	observed := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	taskCreated := observed.Add(-24 * time.Hour)
	taskUpdated := observed.Add(-2 * time.Hour)
	stateChanged := observed.Add(-90 * time.Minute)
	containerCreated := observed.Add(-100 * time.Minute)
	containerStarted := observed.Add(-80 * time.Minute)
	standaloneCreated := observed.Add(-3 * time.Hour)
	standaloneStarted := observed.Add(-170 * time.Minute)
	standaloneFinished := observed.Add(-10 * time.Minute)
	m.now = func() time.Time { return observed }
	c.Tasks[0].Meta.CreatedAt = taskCreated
	c.Tasks[0].Meta.UpdatedAt = taskUpdated
	c.Tasks[0].Status.Timestamp = stateChanged
	c.Containers[0].Created = containerCreated.Add(-time.Minute).Unix()
	c.Containers[1].Created = standaloneCreated.Add(-time.Minute).Unix()
	c.Inspections["container-a"] = container.InspectResponse{
		Created:      containerCreated.Format(time.RFC3339Nano),
		RestartCount: 2,
		State: &container.State{
			StartedAt:  containerStarted.Format(time.RFC3339Nano),
			FinishedAt: time.Time{}.Format(time.RFC3339Nano),
			ExitCode:   0,
			OOMKilled:  false,
		},
	}
	c.Inspections["compose-a"] = container.InspectResponse{
		Created:      standaloneCreated.Format(time.RFC3339Nano),
		RestartCount: 4,
		State: &container.State{
			StartedAt:  standaloneStarted.Format(time.RFC3339Nano),
			FinishedAt: standaloneFinished.Format(time.RFC3339Nano),
			ExitCode:   137,
			OOMKilled:  true,
		},
	}
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	inv, err := m.Inventory(context.Background(), s.Runtimes[0].ID, false)
	if err != nil {
		t.Fatal(err)
	}
	byContainer := map[string]Instance{}
	for _, instance := range inv.Instances {
		byContainer[instance.ContainerID] = instance
	}
	task := byContainer["container-a"]
	if task.Kind != "task" || task.CreatedAt == nil || !task.CreatedAt.Equal(taskCreated) || task.UpdatedAt == nil || !task.UpdatedAt.Equal(taskUpdated) || task.StateChangedAt == nil || !task.StateChangedAt.Equal(stateChanged) {
		t.Fatalf("Swarm Task metadata collapsed into generic time: %+v", task)
	}
	if task.ContainerCreatedAt == nil || !task.ContainerCreatedAt.Equal(containerCreated) || task.StartedAt == nil || !task.StartedAt.Equal(containerStarted) || task.FinishedAt != nil || task.RestartCount == nil || *task.RestartCount != 2 || task.ExitCode == nil || *task.ExitCode != 0 || task.OOMKilled == nil || *task.OOMKilled {
		t.Fatalf("Task container lifecycle evidence lost: %+v", task)
	}
	standalone := byContainer["compose-a"]
	if standalone.Kind != "container" || standalone.CreatedAt == nil || !standalone.CreatedAt.Equal(standaloneCreated) || standalone.ContainerCreatedAt == nil || !standalone.ContainerCreatedAt.Equal(standaloneCreated) || standalone.UpdatedAt != nil || standalone.StateChangedAt != nil {
		t.Fatalf("standalone Container gained a fabricated update time: %+v", standalone)
	}
	if standalone.StartedAt == nil || !standalone.StartedAt.Equal(standaloneStarted) || standalone.FinishedAt == nil || !standalone.FinishedAt.Equal(standaloneFinished) || standalone.RestartCount == nil || *standalone.RestartCount != 4 || standalone.ExitCode == nil || *standalone.ExitCode != 137 || standalone.OOMKilled == nil || !*standalone.OOMKilled {
		t.Fatalf("standalone Container lifecycle evidence lost: %+v", standalone)
	}

	// An inspect response older than the list observation cannot describe the
	// current container generation and must not overwrite its list timestamp.
	wrong := observed.Add(-48 * time.Hour)
	m.details["engine-local/compose-a"] = containerDetail{At: observed.Add(-time.Second), CreatedAt: &wrong, StartedAt: &wrong}
	inv, err = m.Inventory(context.Background(), s.Runtimes[0].ID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range inv.Instances {
		if instance.ContainerID == "compose-a" && (instance.CreatedAt == nil || instance.CreatedAt.Equal(wrong) || instance.StartedAt != nil) {
			t.Fatalf("stale inspect evidence replaced current list evidence: %+v", instance)
		}
	}
}

func TestContainerSummaryHealthFallbacksAndDeduplication(t *testing.T) {
	for _, raw := range []string{"Up (healthy)", "Up (health: unhealthy)", "Up (health: starting)", "Up"} {
		i := Instance{Health: "unknown"}
		enrichContainer(&i, container.Summary{ID: "container", Status: raw}, "engine", time.Now())
		want := "unknown"
		switch raw {
		case "Up (healthy)":
			want = "healthy"
		case "Up (health: unhealthy)":
			want = "unhealthy"
		case "Up (health: starting)":
			want = "starting"
		}
		if i.Health != want || i.ContainerID != "container" || i.EngineID != "engine" {
			t.Fatalf("legacy health=%s want=%s", i.Health, want)
		}
	}
	i := Instance{Health: "unknown"}
	enrichContainer(&i, container.Summary{Health: &container.HealthSummary{Status: container.Healthy}}, "engine", time.Now())
	if i.Health != "healthy" {
		t.Fatal("structured health discarded")
	}
	values := appendUnique([]string{"one", "two"}, "one")
	values = appendUnique(values, "three")
	if len(values) != 3 || values[2] != "three" {
		t.Fatal("duplicate addresses or ports retained")
	}
}

func TestContainerInventoryPreservesDistinctAddressesAndPublishedPorts(t *testing.T) {
	var c container.Summary
	if e := json.Unmarshal([]byte(`{"Id":"container","Names":["/web"],"NetworkSettings":{"Networks":{"first":{"IPAddress":"10.0.0.2","GlobalIPv6Address":"2001:db8::2"},"duplicate":{"IPAddress":"10.0.0.2"},"empty":{}}},"Ports":[{"PrivatePort":8080,"Type":"tcp"},{"IP":"127.0.0.1","PrivatePort":8080,"PublicPort":80,"Type":"tcp"},{"PrivatePort":8080,"Type":"tcp"}]}`), &c); e != nil {
		t.Fatal(e)
	}
	i := Instance{}
	enrichContainer(&i, c, "engine", time.Now())
	if i.Name != "web" || !reflect.DeepEqual(i.Addresses, []string{"10.0.0.2", "2001:db8::2"}) || !reflect.DeepEqual(i.Ports, []string{"127.0.0.1:80→8080/tcp", "8080/tcp"}) {
		t.Fatalf("inventory network evidence: %+v", i)
	}
}
