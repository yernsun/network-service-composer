// Package projectlife verifies and removes explicitly owned runtime workloads.
// It never deletes volumes, images, bind mounts, networks, secrets or configs.
package projectlife

import (
	"context"
	"errors"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
	"github.com/opencontainers/go-digest"
	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
)

var ErrOwnership = errors.New("project deletion ownership cannot be established")
var ErrRunning = errors.New("project services are still running")
var ErrChanged = errors.New("project deletion runtime scope changed")
var ErrReadiness = errors.New("Swarm application container readiness failed")
var ErrDataRetention = errors.New("Swarm volume retention cannot be established")

type Object struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Service string `json:"service"`
	Active  bool   `json:"active"`
	Version uint64 `json:"version,omitempty"`
}
type Observation struct {
	Objects    []Object         `json:"objects"`
	Preserved  []string         `json:"preserved"`
	ObservedAt time.Time        `json:"observed_at"`
	Retained   []RetainedVolume `json:"retained,omitempty"`
	Ready      bool             `json:"ready"`
	Unhealthy  bool             `json:"unhealthy,omitempty"`
	Nodes      []string         `json:"nodes,omitempty"`
}
type Runtime interface {
	Observe(context.Context, model.Runtime, model.Application, []model.Service) (Observation, error)
	Remove(context.Context, model.Runtime, model.Application, []model.Service, Observation, bool) error
}

type ProgressRuntime interface {
	RemoveWithProgress(context.Context, model.Runtime, model.Application, []model.Service, Observation, bool, func(Object) error) error
}
type Docker struct {
	Config   config.Config
	ReadNode NodeReader
}

func (d Docker) open(rt model.Runtime, app model.Application) (*client.Client, error) {
	if !d.Config.Management.Enabled || !rt.Enabled || !rt.ManagementEnabled || !app.Managed || app.RuntimeID != rt.ID || app.ProjectName == "" {
		return nil, ErrOwnership
	}
	files := engine.TLSFiles{}
	if rt.CredentialRef != "" {
		credential, ok := d.Config.Credentials[rt.CredentialRef]
		if !ok {
			return nil, ErrOwnership
		}
		files = engine.TLSFiles{CA: credential.CAFile, Certificate: credential.CertFile, Key: credential.KeyFile}
	}
	node := applicationx.RuntimeNodeID(rt.Endpoint)
	if node == "" {
		return engine.NewManagementClient(rt, files)
	}
	for _, bound := range d.Config.Controller.Nodes {
		if bound.InstanceID != d.Config.Controller.InstanceID || bound.NodeID != node || bound.ClusterID != rt.ClusterID || rt.CredentialRef != "" {
			continue
		}
		local := rt
		local.Endpoint = "unix:///var/run/docker.sock"
		cli, err := engine.NewManagementClient(local, files)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		info, err := cli.Info(ctx, client.InfoOptions{})
		cancel()
		if err == nil && info.Info.ID == bound.EngineID && info.Info.Swarm.NodeID == node && info.Info.Swarm.Cluster != nil && info.Info.Swarm.Cluster.ID == bound.ClusterID {
			return cli, nil
		}
		cli.Close()
		return nil, ErrOwnership
	}
	return nil, ErrOwnership
}

func ownedServices(rt model.Runtime, app model.Application, services []model.Service) map[string]bool {
	allowed := map[string]bool{}
	for _, s := range services {
		if s.ApplicationID != app.ID || s.RuntimeID != rt.ID || !s.Managed {
			continue
		}
		prefix := app.ProjectName + "/"
		if rt.Type == model.RuntimeSwarm {
			prefix = app.ProjectName + "_"
		}
		if strings.HasPrefix(s.ServiceKey, prefix) {
			allowed[strings.TrimPrefix(s.ServiceKey, prefix)] = true
		}
	}
	return allowed
}

func (d Docker) Observe(ctx context.Context, rt model.Runtime, app model.Application, services []model.Service) (Observation, error) {
	cli, err := d.open(rt, app)
	if err != nil {
		return Observation{}, err
	}
	defer cli.Close()
	read := d.ReadNode
	if read == nil {
		read = localNodeReader(cli)
	}
	return observe(ctx, cli, rt, app, ownedServices(rt, app, services), serviceObjects(services), read)
}

func serviceObjects(services []model.Service) map[string]bool {
	ids := map[string]bool{}
	for _, s := range services {
		if s.RuntimeObjectID != "" {
			ids[s.RuntimeObjectID] = true
		}
	}
	return ids
}
func taskActive(state swarm.TaskState) bool {
	switch state {
	case swarm.TaskStateComplete, swarm.TaskStateShutdown, swarm.TaskStateFailed, swarm.TaskStateRejected, swarm.TaskStateRemove:
		return false
	default:
		return true
	}
}

// Swarm's task executor removes containers with RemoveVolume=true. Checking the
// removal request alone therefore does not protect anonymous or image VOLUME
// data. Inspect immutable image metadata and every reviewed task specification
// before authorizing service removal, including tasks from an older rollout.
func retainedSwarmSpec(ctx context.Context, cli *client.Client, spec *swarm.ContainerSpec, images map[string][]string) error {
	if spec == nil {
		return ErrDataRetention
	}
	ref := spec.Image
	if _, suffix, ok := strings.Cut(ref, "@"); ok {
		ref = suffix
	}
	if _, err := digest.Parse(ref); err != nil {
		// A mutable tag can now refer to a different image from the task's image.
		return ErrDataRetention
	}
	targets := map[string]bool{}
	for _, m := range spec.Mounts {
		switch string(m.Type) {
		case "volume", "bind":
			if m.Source == "" {
				return ErrDataRetention
			}
			targets[path.Clean(m.Target)] = true
		}
	}
	volumes, ok := images[spec.Image]
	if !ok {
		result, err := cli.ImageInspect(ctx, spec.Image)
		if err != nil || result.Config == nil {
			return ErrDataRetention
		}
		volumes = []string{}
		for target := range result.Config.Volumes {
			volumes = append(volumes, path.Clean(target))
		}
		images[spec.Image] = volumes
	}
	for _, target := range volumes {
		if !targets[target] {
			return ErrDataRetention
		}
	}
	return nil
}

func observe(ctx context.Context, cli *client.Client, rt model.Runtime, app model.Application, allowed, knownIDs map[string]bool, readers ...NodeReader) (Observation, error) {
	o := Observation{Ready: true, Objects: []Object{}, Preserved: []string{}, ObservedAt: time.Now().UTC()}
	keep := map[string]bool{}
	var read NodeReader
	if len(readers) > 0 {
		read = readers[0]
	}
	nodeEvidence := map[string]NodeStorage{}
	if rt.Type == model.RuntimeDocker {
		result, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: make(client.Filters).Add("label", "com.docker.compose.project="+app.ProjectName)})
		if err != nil {
			return o, err
		}
		for _, listed := range result.Items {
			result, err := cli.ContainerInspect(ctx, listed.ID, client.ContainerInspectOptions{})
			if err != nil {
				return o, err
			}
			c := result.Container
			if c.Config == nil || c.State == nil || c.Config.Labels["com.docker.compose.project"] != app.ProjectName || !allowed[c.Config.Labels["com.docker.compose.service"]] {
				return o, ErrOwnership
			}
			o.Objects = append(o.Objects, Object{ID: c.ID, Kind: "container", Service: c.Config.Labels["com.docker.compose.service"], Active: c.State.Running || c.State.Restarting || c.State.Paused})
			for _, mount := range c.Mounts {
				value := string(mount.Type) + ":" + mount.Name
				if mount.Name == "" {
					value = string(mount.Type) + ":" + mount.Source
				}
				keep[value] = true
			}
		}
	} else if rt.Type == model.RuntimeSwarm {
		images := map[string][]string{}
		result, err := cli.ServiceList(ctx, client.ServiceListOptions{Filters: make(client.Filters).Add("label", "com.docker.stack.namespace="+app.ProjectName)})
		if err != nil {
			return o, err
		}
		localNode := ""
		if len(result.Items) > 0 {
			info, err := cli.Info(ctx, client.InfoOptions{})
			if err != nil || info.Info.Swarm.NodeID == "" {
				return o, ErrDataRetention
			}
			localNode = info.Info.Swarm.NodeID
		}
		for _, service := range result.Items {
			name := strings.TrimPrefix(service.Spec.Name, app.ProjectName+"_")
			if service.Spec.Labels["com.docker.stack.namespace"] != app.ProjectName || !allowed[name] {
				return o, ErrOwnership
			}
			tasks, err := cli.TaskList(ctx, client.TaskListOptions{Filters: make(client.Filters).Add("service", service.ID)})
			if err != nil {
				return o, err
			}
			node := ""
			statelessEvidence := false
			if service.Spec.TaskTemplate.ContainerSpec == nil {
				return o, ErrDataRetention
			}
			if read != nil && len(service.Spec.TaskTemplate.ContainerSpec.Mounts) > 0 && hasDataNode(service) {
				node, err = storageNode(service)
				if err != nil {
					return o, err
				}
				evidence, e := readStorage(ctx, read, node)
				if e != nil {
					return o, e
				}
				nodeEvidence[node] = evidence
				retained, e := retainedNodeSpec(service.Spec.TaskTemplate.ContainerSpec, evidence)
				if e != nil {
					return o, e
				}
				for _, volume := range retained {
					if volume.Owner != app.ProjectName {
						return o, ErrOwnership
					}
				}
				o.Retained = append(o.Retained, retained...)
			} else if read != nil && len(service.Spec.TaskTemplate.ContainerSpec.Mounts) == 0 && hasRemoteTask(tasks.Items, localNode) {
				statelessEvidence = true
				for _, task := range tasks.Items {
					if task.NodeID == "" {
						return o, ErrDataRetention
					}
					evidence, e := readStorage(ctx, read, task.NodeID)
					if e != nil {
						return o, e
					}
					nodeEvidence[task.NodeID] = evidence
					if _, e := retainedNodeSpec(service.Spec.TaskTemplate.ContainerSpec, evidence); e != nil {
						return o, e
					}
				}
			} else if err := retainedSwarmSpec(ctx, cli, service.Spec.TaskTemplate.ContainerSpec, images); err != nil {
				return o, err
			}
			readyTasks := 0
			active := service.Spec.Mode.Replicated == nil || service.Spec.Mode.Replicated.Replicas == nil || *service.Spec.Mode.Replicated.Replicas > 0
			for _, task := range tasks.Items {
				// A manifest-list digest can select different image configs on
				// different worker platforms. Manager-local metadata cannot prove
				// remote task volume retention; per-node inspection is required.
				if !statelessEvidence && node == "" && task.NodeID != "" && task.NodeID != localNode {
					return o, ErrDataRetention
				}
				if node != "" {
					if task.NodeID != "" && task.NodeID != node {
						return o, ErrDataRetention
					}
					if err := retainedActiveTask(task, nodeEvidence[node]); err != nil {
						return o, err
					}
					if task.Status.State == swarm.TaskStateRunning && task.DesiredState == swarm.TaskStateRunning {
						readyTasks++
						actual := nodeEvidence[node].Containers[task.Status.ContainerStatus.ContainerID]
						if !actual.State.Running {
							o.Ready = false
						}
						if task.Spec.ContainerSpec.Healthcheck != nil {
							if actual.State.Health == nil || actual.State.Health.Status != "healthy" {
								o.Ready = false
							}
							if actual.State.Health != nil && actual.State.Health.Status == "unhealthy" {
								o.Unhealthy = true
							}
						}
					}
					if _, err := retainedNodeSpec(task.Spec.ContainerSpec, nodeEvidence[node]); err != nil {
						return o, err
					}
				} else if statelessEvidence {
					if err := retainedActiveTask(task, nodeEvidence[task.NodeID]); err != nil {
						return o, err
					}
					if _, err := retainedNodeSpec(task.Spec.ContainerSpec, nodeEvidence[task.NodeID]); err != nil {
						return o, err
					}
				} else if err := retainedSwarmSpec(ctx, cli, task.Spec.ContainerSpec, images); err != nil {
					return o, err
				}
				for _, mount := range task.Spec.ContainerSpec.Mounts {
					keep[string(mount.Type)+":"+mount.Source] = true
				}
				if taskActive(task.Status.State) {
					active = true
				}
			}
			if node != "" && readyTasks != 1 {
				o.Ready = false
			}
			o.Objects = append(o.Objects, Object{ID: service.ID, Kind: "swarm-service", Service: name, Active: active, Version: service.Version.Index})
			if service.Spec.TaskTemplate.ContainerSpec != nil {
				for _, m := range service.Spec.TaskTemplate.ContainerSpec.Mounts {
					keep[string(m.Type)+":"+m.Source] = true
				}
			}
		}
		// Tasks can outlive removal of the service object. Read the complete task
		// inventory as well; a missing service alone does not prove termination.
		tasks, err := cli.TaskList(ctx, client.TaskListOptions{})
		if err != nil {
			return o, err
		}
		for _, obj := range o.Objects {
			knownIDs[obj.ID] = true
		}
		for _, task := range tasks.Items {
			belongs := knownIDs[task.ServiceID] || (task.Spec.ContainerSpec != nil && task.Spec.ContainerSpec.Labels["com.docker.stack.namespace"] == app.ProjectName)
			if belongs && taskActive(task.Status.State) {
				o.Objects = append(o.Objects, Object{ID: task.ID, Kind: "swarm-task", Service: task.ServiceID, Active: true})
			}
		}
		if read != nil {
			for node, evidence := range nodeEvidence {
				o.Nodes = append(o.Nodes, node)
				for _, c := range evidence.Containers {
					if c.Config == nil || c.State == nil {
						return o, ErrDataRetention
					}
					serviceID := c.Config.Labels["com.docker.swarm.service.id"]
					if !knownIDs[serviceID] {
						continue
					}
					if c.State.Running || c.State.Restarting || c.State.Paused {
						o.Objects = append(o.Objects, Object{ID: c.ID, Kind: "swarm-container", Service: serviceID, Active: true})
					}
					for _, mount := range c.Mounts {
						if string(mount.Type) != "volume" || mount.Name == "" {
							return o, ErrDataRetention
						}
						if _, ok := evidence.Volumes[mount.Name]; !ok {
							return o, ErrDataRetention
						}
						keep["volume:"+mount.Name] = true
					}
					_ = node
				}
			}
		}

		// Terminal task records do not prove container exit or removal. Inspect
		// actual containers even when their service/task records have disappeared.
		containers, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: make(client.Filters).Add("label", "com.docker.swarm.service.id")})
		if err != nil {
			return o, err
		}
		for _, listed := range containers.Items {
			serviceID := listed.Labels["com.docker.swarm.service.id"]
			if !knownIDs[serviceID] {
				continue
			}
			inspected, err := cli.ContainerInspect(ctx, listed.ID, client.ContainerInspectOptions{})
			if errdefs.IsNotFound(err) {
				continue
			}
			if err != nil {
				return o, err
			}
			container := inspected.Container
			if container.Config == nil || container.State == nil || container.Config.Labels["com.docker.swarm.service.id"] != serviceID {
				return o, ErrOwnership
			}
			o.Objects = append(o.Objects, Object{ID: container.ID, Kind: "swarm-container", Service: serviceID, Active: container.State.Running || container.State.Restarting || container.State.Paused})
			for _, mount := range container.Mounts {
				source := mount.Name
				if source == "" {
					source = mount.Source
				}
				keep[string(mount.Type)+":"+source] = true
			}
		}
	} else {
		return o, ErrOwnership
	}
	for value := range keep {
		o.Preserved = append(o.Preserved, value)
	}
	sort.Strings(o.Preserved)
	sort.Slice(o.Objects, func(i, j int) bool { return o.Objects[i].ID < o.Objects[j].ID })
	return o, nil
}

// ReviewedSubset allows already removed objects, but never unreviewed replacements.
func ReviewedSubset(current, reviewed Observation) bool {
	preserved := map[string]bool{}
	for _, value := range reviewed.Preserved {
		preserved[value] = true
	}
	for _, value := range current.Preserved {
		if !preserved[value] {
			return false
		}
	}
	previous := map[string]Object{}
	for _, o := range reviewed.Objects {
		previous[o.ID] = o
	}
	for _, o := range current.Objects {
		p, ok := previous[o.ID]
		if !ok || p.Kind != o.Kind || p.Service != o.Service || p.Version != o.Version {
			return false
		}
	}
	return true
}
func Stopped(o Observation) bool {
	for _, v := range o.Objects {
		if v.Active {
			return false
		}
	}
	return true
}

func (d Docker) Remove(ctx context.Context, rt model.Runtime, app model.Application, services []model.Service, reviewed Observation, force bool) error {
	return d.RemoveWithProgress(ctx, rt, app, services, reviewed, force, nil)
}

func (d Docker) RemoveWithProgress(ctx context.Context, rt model.Runtime, app model.Application, services []model.Service, reviewed Observation, force bool, progress func(Object) error) error {
	cli, err := d.open(rt, app)
	if err != nil {
		return err
	}
	defer cli.Close()
	known := serviceObjects(services)
	for _, obj := range reviewed.Objects {
		if obj.Kind == "swarm-service" {
			known[obj.ID] = true
		}
	}
	read := d.ReadNode
	if read == nil {
		read = localNodeReader(cli)
	}
	readCurrent := func(callCtx context.Context) (Observation, error) {
		value, err := observe(callCtx, cli, rt, app, ownedServices(rt, app, services), known, read)
		if err == nil {
			err = appendRetainedContainers(callCtx, &value, read, reviewed.Retained, reviewed.Nodes, known)
		}
		return value, err
	}
	// Containers may disappear during the collector's scan after ServiceRemove.
	// Wait for complete exit evidence; never treat a partial scan as success.
	readAfterMutation := func() (Observation, error) {
		bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			value, err := readCurrent(bounded)
			if !errors.Is(err, ErrDataRetention) {
				return value, err
			}
			select {
			case <-bounded.Done():
				if ctx.Err() != nil {
					return value, ctx.Err()
				}
				return value, ErrDataRetention
			case <-ticker.C:
			}
		}
	}
	current, err := readCurrent(ctx)
	if err != nil {
		return err
	}
	if !ReviewedSubset(current, reviewed) {
		return ErrChanged
	}
	if !force && !Stopped(current) {
		return ErrRunning
	}
	completed := map[string]bool{}
	record := func(observed Observation) error {
		remaining := map[string]bool{}
		for _, obj := range observed.Objects {
			remaining[obj.ID] = true
			if obj.Kind == "swarm-task" || obj.Kind == "swarm-container" {
				remaining[obj.Service] = true
			}
		}
		for _, obj := range reviewed.Objects {
			if remaining[obj.ID] || completed[obj.ID] {
				continue
			}
			if progress != nil {
				if err := progress(obj); err != nil {
					return err
				}
			}
			completed[obj.ID] = true
		}
		return nil
	}
	if err = record(current); err != nil {
		return err
	}
	for _, obj := range current.Objects {
		switch obj.Kind {
		case "container":
			if force && obj.Active {
				timeout := 30
				if _, err = cli.ContainerStop(ctx, obj.ID, client.ContainerStopOptions{Timeout: &timeout}); err != nil && !errdefs.IsNotFound(err) {
					return err
				}
			}
			// Force and RemoveVolumes deliberately remain false. A concurrent
			// restart makes removal fail rather than silently stopping a workload.
			_, err = cli.ContainerRemove(ctx, obj.ID, client.ContainerRemoveOptions{})
		case "swarm-service":
			_, err = cli.ServiceRemove(ctx, obj.ID, client.ServiceRemoveOptions{})
		case "swarm-task", "swarm-container":
			continue // The engine owns task exit; only observe its convergence.
		default:
			return ErrOwnership
		}
		if err != nil && !errdefs.IsNotFound(err) {
			return err
		}
		observed, e := readAfterMutation()
		if e != nil {
			return e
		}
		if !ReviewedSubset(observed, reviewed) {
			return ErrChanged
		}
		if err = record(observed); err != nil {
			return err
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		remaining, err := readAfterMutation()
		if err != nil {
			return err
		}
		if len(remaining.Objects) == 0 {
			if err := VerifyRetainedVolumes(ctx, read, reviewed.Retained); err != nil {
				return err
			}
			return record(remaining)
		}
		if !ReviewedSubset(remaining, reviewed) {
			return ErrChanged
		}
		if err = record(remaining); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func hasRemoteTask(tasks []swarm.Task, local string) bool {
	for _, t := range tasks {
		if t.NodeID != "" && t.NodeID != local {
			return true
		}
	}
	return false
}
