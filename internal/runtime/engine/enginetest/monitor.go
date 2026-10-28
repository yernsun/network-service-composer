// Package enginetest supplies an in-memory Engine for monitoring and HTTP tests.
package enginetest

import (
	"context"
	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"sort"
	"sync"
)

type Client struct {
	sync.Mutex
	System      system.Info
	Nodes       []swarm.Node
	Services    []swarm.Service
	Tasks       []swarm.Task
	Containers  []container.Summary
	Inspections map[string]container.InspectResponse
	Stats       container.StatsResponse
	StatsCalls  int
	Failure     error
	Race        bool
	Updates     int
}

func (c *Client) Info(context.Context) (system.Info, error) { return c.System, c.Failure }
func (c *Client) NodeList(context.Context) ([]swarm.Node, error) {
	c.Lock()
	defer c.Unlock()
	return append([]swarm.Node(nil), c.Nodes...), c.Failure
}
func (c *Client) ServiceList(context.Context) ([]swarm.Service, error) { return c.Services, c.Failure }
func (c *Client) TaskList(context.Context) ([]swarm.Task, error)       { return c.Tasks, c.Failure }
func (c *Client) ContainerList(context.Context) ([]container.Summary, error) {
	return c.Containers, c.Failure
}
func (c *Client) ContainerInspect(_ context.Context, id string) (container.InspectResponse, error) {
	c.Lock()
	defer c.Unlock()
	return c.Inspections[id], nil
}
func (c *Client) NetworkList(context.Context) ([]network.Summary, error) { return nil, nil }
func (c *Client) Events(context.Context, []string) (<-chan events.Message, <-chan error) {
	return nil, nil
}
func (c *Client) Close() error { return nil }
func (c *Client) ContainerStats(context.Context, string) (container.StatsResponse, error) {
	c.Lock()
	defer c.Unlock()
	c.StatsCalls++
	return c.Stats, c.Failure
}
func (c *Client) NodeInspect(_ context.Context, id string) (swarm.Node, error) {
	c.Lock()
	defer c.Unlock()
	for _, n := range c.Nodes {
		if n.ID == id {
			return n, nil
		}
	}
	return swarm.Node{}, errdefs.ErrNotFound
}
func (c *Client) NodeUpdate(_ context.Context, id string, version swarm.Version, spec swarm.NodeSpec) error {
	c.Lock()
	defer c.Unlock()
	for i, n := range c.Nodes {
		if n.ID == id {
			if c.Race {
				c.Nodes[i].Version.Index++
				c.Nodes[i].Spec.Availability = swarm.NodeAvailabilityPause
				c.Race = false
				return errdefs.ErrInvalidArgument
			}
			if n.Version != version {
				return errdefs.ErrConflict
			}
			c.Nodes[i].Spec = spec
			c.Nodes[i].Version.Index++
			c.Updates++
			return nil
		}
	}
	return errdefs.ErrNotFound
}

type Store struct {
	Runtimes []model.Runtime
	Services []model.Service
}

func (s *Store) GetRuntime(_ context.Context, id string) (model.Runtime, error) {
	for _, r := range s.Runtimes {
		if r.ID == id {
			return r, nil
		}
	}
	return model.Runtime{}, storage.ErrNotFound
}
func (s *Store) ListRuntimes(_ context.Context, p storage.Page) ([]model.Runtime, error) {
	rows := []model.Runtime{}
	for _, r := range s.Runtimes {
		if r.ID > p.After {
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if p.Limit > 0 && len(rows) > p.Limit {
		rows = rows[:p.Limit]
	}
	return rows, nil
}
func (s *Store) ListServicesFiltered(_ context.Context, f storage.ServiceFilter, p storage.Page) ([]model.Service, error) {
	rows := []model.Service{}
	for _, v := range s.Services {
		if v.RuntimeID == f.RuntimeID && v.ID > p.After {
			rows = append(rows, v)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if p.Limit > 0 && len(rows) > p.Limit {
		rows = rows[:p.Limit]
	}
	return rows, nil
}
func Fixture() (*Store, *Client) {
	rt := model.Runtime{ID: "rt-cluster", Name: "Test cluster", Type: model.RuntimeSwarm, Enabled: true, ManagementEnabled: true, Endpoint: "unix:///test.sock"}
	c := &Client{System: system.Info{ID: "engine-local", Name: "same-hostname", NCPU: 4, MemTotal: 8 << 30, Swarm: swarm.Info{NodeID: "node-a", ControlAvailable: true, Cluster: &swarm.ClusterInfo{ID: "cluster-a"}}}, Inspections: map[string]container.InspectResponse{}}
	c.Nodes = []swarm.Node{{ID: "node-a", Meta: swarm.Meta{Version: swarm.Version{Index: 7}}, Spec: swarm.NodeSpec{Annotations: swarm.Annotations{Labels: map[string]string{"zone": "a"}}, Role: swarm.NodeRoleManager, Availability: swarm.NodeAvailabilityActive}, Description: swarm.NodeDescription{Hostname: "same-hostname", Resources: swarm.Resources{NanoCPUs: 4e9, MemoryBytes: 8 << 30}}, Status: swarm.NodeStatus{State: swarm.NodeStateReady}}, {ID: "node-b", Description: swarm.NodeDescription{Hostname: "same-hostname"}, Status: swarm.NodeStatus{State: swarm.NodeStateReady}}}
	c.Services = []swarm.Service{{ID: "service-web", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "shop_web", Labels: map[string]string{"com.docker.stack.namespace": "shop"}}}}, {ID: "service-zero", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "zero"}}}}
	c.Tasks = []swarm.Task{{ID: "task-a", NodeID: "node-a", ServiceID: "service-web", DesiredState: swarm.TaskStateRunning, Status: swarm.TaskStatus{State: swarm.TaskStateRunning, ContainerStatus: &swarm.ContainerStatus{ContainerID: "container-a"}}}, {ID: "task-b", NodeID: "node-b", ServiceID: "service-web", DesiredState: swarm.TaskStateRunning, Status: swarm.TaskStatus{State: swarm.TaskStateRunning, ContainerStatus: &swarm.ContainerStatus{ContainerID: "container-b"}}}, {ID: "task-old", NodeID: "node-a", ServiceID: "service-web", DesiredState: swarm.TaskStateShutdown, Status: swarm.TaskStatus{State: swarm.TaskStateFailed}}}
	c.Containers = []container.Summary{{ID: "container-a", Names: []string{"/shop_web.1"}, State: container.StateRunning, Labels: map[string]string{"com.docker.swarm.task.id": "task-a", "com.docker.swarm.service.id": "service-web"}}, {ID: "compose-a", Names: []string{"/shop-db-1"}, State: container.StateRunning, Labels: map[string]string{"com.docker.compose.project": "shop", "com.docker.compose.service": "db"}}, {ID: "independent", Names: []string{"/independent"}, State: container.StateExited}}
	return &Store{Runtimes: []model.Runtime{rt}}, c
}
