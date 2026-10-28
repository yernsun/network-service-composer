package cluster

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func (m *Monitor) Inventory(ctx context.Context, runtimeID string, history bool) (Inventory, error) {
	rt, err := m.store.GetRuntime(ctx, runtimeID)
	if err != nil {
		return Inventory{}, err
	}
	if !rt.Enabled {
		return Inventory{}, ErrUnavailable
	}
	m.mu.RLock()
	sources := make(map[string]source, len(m.sources))
	for k, v := range m.sources {
		sources[k] = v
	}
	runtimes := m.runtimes
	refs := m.references
	details := make(map[string]containerDetail, len(m.details))
	for k, v := range m.details {
		details[k] = v
	}
	m.mu.RUnlock()
	base, ok := sources[connection(rt)]
	if !ok || base.Info.ID == "" {
		return Inventory{}, ErrUnavailable
	}
	now := m.now()
	fullSwarm := rt.Type == model.RuntimeSwarm
	manager := base
	clusterID := ""
	if base.Info.Swarm.Cluster != nil {
		clusterID = base.Info.Swarm.Cluster.ID
	}
	// Prefer the latest successful manager observation for the same cluster.
	if fullSwarm && clusterID != "" {
		for _, s := range sources {
			if s.Info.Swarm.Cluster != nil && s.Info.Swarm.Cluster.ID == clusterID && s.Info.Swarm.ControlAvailable && (s.At.After(manager.At) || !manager.Info.Swarm.ControlAvailable) {
				manager = s
			}
		}
	}
	inv := Inventory{Overview: Overview{RuntimeID: rt.ID, ID: base.Info.ID, Name: rt.Name, Kind: string(rt.Type), ObservedAt: base.At, Error: base.Error}, Nodes: []Node{}, Workloads: []Workload{}, Instances: []Instance{}}
	nodes := map[string]Node{}
	if fullSwarm && manager.Info.Swarm.ControlAvailable {
		inv.Overview.ID = clusterID
		inv.Overview.ObservedAt = manager.At
		inv.Overview.Error = manager.Error
		if manager.Info.Swarm.Cluster != nil && manager.Info.Swarm.Cluster.Spec.Name != "" {
			inv.Overview.Name = manager.Info.Swarm.Cluster.Spec.Name
		}
		for _, n := range manager.Nodes {
			nodes[n.ID] = swarmNode(n, manager.At)
		}
	} else {
		id := nodeID(base.Info)
		n := Node{ID: id, Name: base.Info.Name, EngineID: base.Info.ID, Role: "standalone", State: "ready", EngineVersion: base.Info.ServerVersion, Address: base.Info.Swarm.NodeAddr, CPUs: float64(base.Info.NCPU), MemoryBytes: base.Info.MemTotal, Labels: map[string]string{}, EngineLabels: labels(base.Info.Labels), ObservedAt: base.At}
		for _, sn := range base.Nodes {
			if sn.ID == id {
				n = swarmNode(sn, base.At)
			}
		}
		nodes[id] = n
		if fullSwarm {
			inv.Overview.Error = "manager_required"
		}
	}
	nodeSources := map[string]source{}
	for _, s := range sources {
		id := nodeID(s.Info)
		if _, ok := nodes[id]; !ok || s.Info.ID == "" {
			continue
		}
		if old, ok := nodeSources[id]; !ok || s.At.After(old.At) || (s.Error == "" && old.Error != "") {
			nodeSources[id] = s
		}
	}
	for id, n := range nodes {
		if s, ok := nodeSources[id]; ok {
			if s.Host != nil {
				host := *s.Host
				host.Stale = host.Stale || now.Sub(host.At) > 30*time.Second || host.At.After(now.Add(5*time.Second))
				n.Host = &host
				if !host.Stale && (host.CPUPercent != nil || host.MemoryUsed != nil || len(host.Disks) > 0) {
					inv.Overview.HostMetricsAvailable = true
				}
			}
			if s.Health != nil {
				health := *s.Health
				health.Stale = health.Stale || now.Sub(health.At) > 30*time.Second || health.At.After(now.Add(5*time.Second))
				if health.Stale {
					health.Controller, health.Collector, health.Gateway, health.Role = "unavailable", "unavailable", "unavailable", "unknown"
				}
				n.Controller = &health
			}
			n.EngineID = s.Info.ID
			n.Connected = true
			n.InventoryComplete = s.Error == "" && now.Sub(s.At) <= 30*time.Second
			if s.Error != "" {
				n.Error = s.Error
			}
			if s.At.Before(n.ObservedAt) {
				n.ObservedAt = s.At
			}
		}
		n.Stale = now.Sub(n.ObservedAt) > 30*time.Second || !n.InventoryComplete && n.Connected
		n.ReadOnlyReason = "not_swarm"
		if fullSwarm {
			n.ReadOnlyReason = "management_disabled"
			if m.config.Management.Enabled && rt.ManagementEnabled {
				n.ReadOnlyReason = "manager_required"
				if base.Info.Swarm.ControlAvailable {
					n.ReadOnlyReason = ""
					n.CanEditLabels = true
				}
			}
		}
		nodes[id] = n
	}
	lookup := func(key, engineID string, sw bool) string {
		// Prefer the selected Runtime. Other references must share the real cluster/engine.
		if id := refs[rt.ID][key]; id != "" && ((sw && fullSwarm) || (!sw && base.Info.ID == engineID)) {
			return id
		}
		ids := make([]string, 0, len(runtimes))
		for id := range runtimes {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			r := runtimes[id]
			s := sources[connection(r)]
			matches := !sw && r.Type == model.RuntimeDocker && s.Info.ID == engineID
			if sw && r.Type == model.RuntimeSwarm && clusterID != "" && s.Info.Swarm.Cluster != nil && s.Info.Swarm.Cluster.ID == clusterID {
				matches = true
			}
			if matches {
				if ref := refs[id][key]; ref != "" {
					return ref
				}
			}
		}
		return ""
	}
	services := map[string]swarm.Service{}
	if fullSwarm {
		for _, s := range manager.Services {
			services[s.ID] = s
		}
	}
	workloads := map[string]Workload{}
	putWorkload := func(node string, w Workload) {
		w.NodeID = node
		key := node + "/" + w.ID
		if _, ok := workloads[key]; !ok {
			workloads[key] = w
		}
	}
	taskContainers := map[string]int{}
	taskIDs := map[string]int{}
	if fullSwarm {
		for _, task := range manager.Tasks {
			historical := task.DesiredState == swarm.TaskStateShutdown || task.DesiredState == swarm.TaskStateRemove
			if !history && historical {
				continue
			}
			node := task.NodeID
			if node == "" {
				node = "unassigned"
			}
			svc := services[task.ServiceID]
			name := svc.Spec.Name
			if name == "" {
				name = task.ServiceID
			}
			sid := lookup(task.ServiceID, "", true)
			w := Workload{ID: "swarm:" + task.ServiceID, Name: name, Kind: "swarm", Project: svc.Spec.Labels["com.docker.stack.namespace"], ServiceID: sid}
			putWorkload(node, w)
			i := Instance{ID: "task:" + task.ID, TaskID: task.ID, NodeID: node, WorkloadID: w.ID, Name: fmt.Sprintf("%s.%d", name, task.Slot), Kind: "task", ServiceID: sid, State: string(task.Status.State), DesiredState: string(task.DesiredState), Health: "unknown", Error: task.Status.Err, Historical: historical, ObservedAt: manager.At, Addresses: []string{}, Ports: []string{}}
			i.CreatedAt = nonZeroTime(task.Meta.CreatedAt)
			i.UpdatedAt = nonZeroTime(task.Meta.UpdatedAt)
			i.StateChangedAt = nonZeroTime(task.Status.Timestamp)
			if task.Spec.ContainerSpec != nil {
				i.Image = task.Spec.ContainerSpec.Image
			}
			if task.Status.ContainerStatus != nil {
				i.ContainerID = task.Status.ContainerStatus.ContainerID
			}
			for _, a := range task.NetworksAttachments {
				for _, address := range a.Addresses {
					i.Addresses = append(i.Addresses, address.String())
				}
			}
			for _, p := range svc.Endpoint.Ports {
				i.Ports = append(i.Ports, fmt.Sprintf("%d→%d/%s", p.PublishedPort, p.TargetPort, p.Protocol))
			}
			if i.ContainerID != "" {
				taskContainers[node+"/"+i.ContainerID] = len(inv.Instances)
			}
			taskIDs[task.ID] = len(inv.Instances)
			inv.Instances = append(inv.Instances, i)
		}
	}
	for node, s := range nodeSources {
		for _, c := range s.Containers {
			if fullSwarm {
				if index, ok := taskContainers[node+"/"+c.ID]; ok {
					enrichContainer(&inv.Instances[index], c, s.Info.ID, s.At)
					continue
				}
				if index, ok := taskIDs[c.Labels["com.docker.swarm.task.id"]]; ok && inv.Instances[index].NodeID == node {
					enrichContainer(&inv.Instances[index], c, s.Info.ID, s.At)
					continue
				}
				// A retained shutdown task is not reintroduced as a standalone container.
				if !history {
					skip := false
					for _, t := range manager.Tasks {
						if t.ID == c.Labels["com.docker.swarm.task.id"] && (t.DesiredState == swarm.TaskStateShutdown || t.DesiredState == swarm.TaskStateRemove) {
							skip = true
							break
						}
					}
					if skip {
						continue
					}
				}
			}
			project, service := c.Labels["com.docker.compose.project"], c.Labels["com.docker.compose.service"]
			w := Workload{ID: "standalone", Name: "", Kind: "standalone"}
			if swarmID := c.Labels["com.docker.swarm.service.id"]; swarmID != "" {
				svc := services[swarmID]
				name := svc.Spec.Name
				if name == "" {
					name = c.Labels["com.docker.swarm.service.name"]
				}
				w = Workload{ID: "swarm:" + swarmID, Name: name, Kind: "swarm", Project: svc.Spec.Labels["com.docker.stack.namespace"], ServiceID: lookup(swarmID, "", true)}
			} else if project != "" && service != "" {
				b, _ := json.Marshal([]string{project, service})
				w = Workload{ID: "compose:" + base64.RawURLEncoding.EncodeToString(b), Name: service, Project: project, Kind: "compose", ServiceID: lookup(project+"/"+service, s.Info.ID, false)}
			}
			putWorkload(node, w)
			i := Instance{ID: "container:" + s.Info.ID + ":" + c.ID, NodeID: node, WorkloadID: w.ID, ServiceID: w.ServiceID, Kind: "container", Health: "unknown", Addresses: []string{}, Ports: []string{}}
			enrichContainer(&i, c, s.Info.ID, s.At)
			inv.Instances = append(inv.Instances, i)
		}
	}
	if fullSwarm {
		for _, s := range services {
			id := "swarm:" + s.ID
			found := false
			for _, w := range workloads {
				if w.ID == id {
					found = true
					break
				}
			}
			if !found {
				putWorkload("unassigned", Workload{ID: id, Name: s.Spec.Name, Kind: "swarm", Project: s.Spec.Labels["com.docker.stack.namespace"], ServiceID: lookup(s.ID, "", true)})
			}
		}
	}
	uniqueServices := map[string]bool{}
	for index, i := range inv.Instances {
		if i.EngineID != "" && i.ContainerID != "" {
			v := m.Metrics(i.EngineID, i.ContainerID)
			if d, ok := details[i.EngineID+"/"+i.ContainerID]; ok && !d.At.Before(i.ObservedAt) {
				if d.Health != "" {
					inv.Instances[index].Health = d.Health
				}
				if d.Error != "" {
					inv.Instances[index].Error = d.Error
				}
				if d.CreatedAt != nil {
					inv.Instances[index].ContainerCreatedAt = d.CreatedAt
					if inv.Instances[index].Kind == "container" {
						inv.Instances[index].CreatedAt = d.CreatedAt
					}
				}
				inv.Instances[index].StartedAt = d.StartedAt
				inv.Instances[index].FinishedAt = d.FinishedAt
				inv.Instances[index].RestartCount = d.RestartCount
				inv.Instances[index].ExitCode = d.ExitCode
				inv.Instances[index].OOMKilled = d.OOMKilled
			}
			if v.Current != nil && (i.State != "running" || nodes[i.NodeID].Stale) {
				v.Current.Stale = true
			}
			inv.Instances[index].Metric = v.Current
		}
		key := i.NodeID + "/" + i.WorkloadID
		w := workloads[key]
		w.Instances++
		if i.State == "running" {
			w.Running++
			inv.Overview.Running++
		}
		workloads[key] = w
	}
	for _, w := range workloads {
		n, ok := nodes[w.NodeID]
		if !ok {
			n = Node{ID: w.NodeID, Name: w.NodeID, Virtual: true, State: "unknown", Labels: map[string]string{}, EngineLabels: map[string]string{}, ObservedAt: inv.Overview.ObservedAt, ReadOnlyReason: "not_swarm"}
		}
		n.Workloads++
		n.Instances += w.Instances
		n.Running += w.Running
		nodes[w.NodeID] = n
		inv.Workloads = append(inv.Workloads, w)
		if w.Kind == "swarm" {
			uniqueServices[w.ID] = true
		} else if w.Kind == "compose" {
			uniqueServices[w.NodeID+"/"+w.ID] = true
		}
	}
	for _, n := range nodes {
		inv.Nodes = append(inv.Nodes, n)
		if !n.Virtual {
			inv.Overview.Nodes++
			if n.State == "ready" {
				inv.Overview.ReadyNodes++
			}
			if n.Role == "manager" {
				inv.Overview.Managers++
			}
			if n.InventoryComplete {
				inv.Overview.ConnectedNodes++
			}
		}
	}
	inv.Overview.Services = len(uniqueServices)
	inv.Overview.Instances = len(inv.Instances)
	inv.Overview.InventoryComplete = inv.Overview.ConnectedNodes == inv.Overview.Nodes && inv.Overview.Error == ""
	inv.Overview.Stale = now.Sub(inv.Overview.ObservedAt) > 30*time.Second || inv.Overview.Error != ""
	inv.Overview.ManagerAvailable = rt.Type == model.RuntimeSwarm && m.config.Management.Enabled && rt.ManagementEnabled &&
		base.Info.Swarm.ControlAvailable && base.Error == "" && base.Info.Swarm.Cluster != nil && base.Info.Swarm.Cluster.ID != "" &&
		!base.At.IsZero() && !base.At.After(now) && now.Sub(base.At) <= 30*time.Second
	sort.Slice(inv.Nodes, func(i, j int) bool { return inv.Nodes[i].ID < inv.Nodes[j].ID })
	sort.Slice(inv.Workloads, func(i, j int) bool {
		a, b := inv.Workloads[i], inv.Workloads[j]
		return a.NodeID+"/"+a.ID < b.NodeID+"/"+b.ID
	})
	sort.Slice(inv.Instances, func(i, j int) bool { return inv.Instances[i].ID < inv.Instances[j].ID })
	return inv, nil
}
func enrichContainer(i *Instance, c container.Summary, engineID string, at time.Time) {
	i.ContainerID = c.ID
	i.EngineID = engineID
	i.Image = c.Image
	i.State = string(c.State)
	i.ObservedAt = at
	if c.Created > 0 {
		created := time.Unix(c.Created, 0).UTC()
		i.ContainerCreatedAt = &created
		if i.Kind == "container" {
			i.CreatedAt = &created
		}
	}
	if len(c.Names) > 0 {
		i.Name = strings.TrimPrefix(c.Names[0], "/")
	}
	if c.Health != nil {
		i.Health = string(c.Health.Status)
	} else {
		// Older Engine API versions expose health only in the human status summary.
		for _, health := range []string{"unhealthy", "healthy", "starting"} {
			if strings.Contains(c.Status, "("+health+")") || strings.Contains(c.Status, "(health: "+health+")") {
				i.Health = health
				break
			}
		}
	}
	if c.NetworkSettings != nil {
		for _, n := range c.NetworkSettings.Networks {
			if n.IPAddress.IsValid() {
				i.Addresses = appendUnique(i.Addresses, n.IPAddress.String())
			}
			if n.GlobalIPv6Address.IsValid() {
				i.Addresses = appendUnique(i.Addresses, n.GlobalIPv6Address.String())
			}
		}
	}
	for _, p := range c.Ports {
		value := fmt.Sprintf("%d/%s", p.PrivatePort, p.Type)
		if p.PublicPort > 0 {
			value = fmt.Sprintf("%s:%d→%s", p.IP, p.PublicPort, value)
		}
		i.Ports = appendUnique(i.Ports, value)
	}
	sort.Strings(i.Addresses)
	sort.Strings(i.Ports)
}

func nonZeroTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}

func parseDockerTime(value string) *time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	return nonZeroTime(parsed)
}

func pointer[T any](value T) *T { return &value }
func appendUnique(values []string, value string) []string {
	for _, v := range values {
		if v == value {
			return values
		}
	}
	return append(values, value)
}
func (v Inventory) Node(id string) (Node, error) {
	for _, n := range v.Nodes {
		if n.ID == id {
			return n, nil
		}
	}
	return Node{}, storage.ErrNotFound
}
