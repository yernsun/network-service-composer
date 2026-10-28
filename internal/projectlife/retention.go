package projectlife

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/swarm"
	"github.com/opencontainers/go-digest"
	"github.com/yernsun/network-service-composer/internal/model"
)

// NodeStorage contains only evidence obtained from a provisioned node collector.
type NodeStorage struct {
	NodeID       string
	Generation   int64
	Sequence     uint64
	At           time.Time
	Complete     bool
	VolumeOwners map[string]string
	Volumes      map[string]time.Time
	Images       map[string][]string
	Containers   map[string]container.InspectResponse
}
type NodeReader func(context.Context, string) (NodeStorage, error)
type RetainedVolume struct {
	NodeID    string    `json:"node_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Target    string    `json:"target"`
	Owner     string    `json:"owner"`
}

func readStorage(ctx context.Context, read NodeReader, node string) (NodeStorage, error) {
	if read == nil {
		return NodeStorage{}, ErrDataRetention
	}
	value, err := read(ctx, node)
	if err != nil || value.NodeID != node || !value.Complete || value.At.IsZero() || time.Since(value.At) > 30*time.Second || value.At.After(time.Now().Add(5*time.Second)) {
		return NodeStorage{}, ErrDataRetention
	}
	return value, nil
}
func storageNode(service swarm.Service) (string, error) {
	if service.Spec.Mode.Replicated == nil || service.Spec.Mode.Replicated.Replicas == nil || *service.Spec.Mode.Replicated.Replicas > 1 || service.Spec.TaskTemplate.Placement == nil {
		return "", ErrDataRetention
	}
	node := ""
	for _, constraint := range service.Spec.TaskTemplate.Placement.Constraints {
		parts := strings.Split(constraint, "==")
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == "node.id" {
			if node != "" && node != strings.TrimSpace(parts[1]) {
				return "", ErrDataRetention
			}
			node = strings.TrimSpace(parts[1])
		}
	}
	if node == "" {
		return "", ErrDataRetention
	}
	return node, nil
}
func retainedNodeSpec(spec *swarm.ContainerSpec, evidence NodeStorage) ([]RetainedVolume, error) {
	if spec == nil {
		return nil, ErrDataRetention
	}
	ref := spec.Image
	if _, suffix, ok := strings.Cut(ref, "@"); ok {
		ref = suffix
	}
	if _, err := digest.Parse(ref); err != nil {
		return nil, ErrDataRetention
	}
	targets, ok := evidence.Images[spec.Image]
	if !ok {
		if repository, suffix, pinned := strings.Cut(spec.Image, "@"); pinned {
			if colon := strings.LastIndex(repository, ":"); colon > strings.LastIndex(repository, "/") {
				repository = repository[:colon]
			}
			targets, ok = evidence.Images[repository+"@"+suffix]
		}
	}
	if !ok {
		return nil, ErrDataRetention
	}
	mounted := map[string]bool{}
	retained := []RetainedVolume{}
	for _, m := range spec.Mounts {
		if string(m.Type) == "tmpfs" {
			continue
		}
		if string(m.Type) != "volume" || m.Source == "" {
			return nil, ErrDataRetention
		}
		created, exists := evidence.Volumes[m.Source]
		if !exists || created.IsZero() {
			return nil, ErrDataRetention
		}
		mounted[path.Clean(m.Target)] = true
		retained = append(retained, RetainedVolume{NodeID: evidence.NodeID, Name: m.Source, CreatedAt: created, Owner: evidence.VolumeOwners[m.Source], Target: path.Clean(m.Target)})
	}
	for _, target := range targets {
		if !mounted[path.Clean(target)] {
			return nil, ErrDataRetention
		}
	}
	return retained, nil
}
func VerifyRetainedVolumes(ctx context.Context, read NodeReader, volumes []RetainedVolume) error {
	for _, v := range volumes {
		evidence, err := readStorage(ctx, read, v.NodeID)
		if err != nil {
			return err
		}
		if current, ok := evidence.Volumes[v.Name]; !ok || !current.Equal(v.CreatedAt) || (v.Owner != "" && evidence.VolumeOwners[v.Name] != v.Owner) {
			return ErrDataRetention
		}
	}
	return nil
}

func appendRetainedContainers(ctx context.Context, o *Observation, read NodeReader, volumes []RetainedVolume, reviewedNodes []string, known map[string]bool) error {
	nodes := map[string]bool{}
	for _, node := range reviewedNodes {
		nodes[node] = true
	}
	objects := map[string]bool{}
	for _, obj := range o.Objects {
		objects[obj.ID] = true
	}
	for _, v := range volumes {
		nodes[v.NodeID] = true
	}
	for node := range nodes {
		evidence, err := readStorage(ctx, read, node)
		if err != nil {
			return err
		}
		for _, c := range evidence.Containers {
			if c.Config == nil || c.State == nil {
				return ErrDataRetention
			}
			service := c.Config.Labels["com.docker.swarm.service.id"]
			if !known[service] || objects[c.ID] {
				continue
			}
			if c.State.Running || c.State.Restarting || c.State.Paused {
				o.Objects = append(o.Objects, Object{ID: c.ID, Kind: "swarm-container", Service: service, Active: true})
			}
		}
	}
	return VerifyRetainedVolumes(ctx, read, volumes)
}

func (d Docker) VerifyRetention(ctx context.Context, rt model.Runtime, app model.Application, volumes []RetainedVolume) error {
	read := d.ReadNode
	if read == nil {
		cli, err := d.open(rt, app)
		if err != nil {
			return err
		}
		defer cli.Close()
		read = localNodeReader(cli)
	}
	fresh := executionNodeReader(read)
	if err := VerifyRetainedVolumes(ctx, fresh, volumes); err != nil {
		return err
	}
	nodes := map[string]bool{}
	for _, v := range volumes {
		nodes[v.NodeID] = true
	}
	for node := range nodes {
		evidence, err := readStorage(ctx, fresh, node)
		if err != nil {
			return err
		}
		if err := verifyApplicationMounts(evidence, app.ProjectName, volumes); err != nil {
			return err
		}
	}
	return nil
}

// Two subsequent reports ensure the collector started a fresh storage scan after
// admission. Direct local SDK observations already perform a live scan.
func executionNodeReader(read NodeReader) NodeReader {
	cache := map[string]NodeStorage{}
	return func(ctx context.Context, node string) (NodeStorage, error) {
		if value, ok := cache[node]; ok {
			return value, nil
		}
		bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		var initial NodeStorage
		for {
			value, err := readStorage(bounded, read, node)
			if err == nil {
				initial = value
				break
			}
			select {
			case <-bounded.Done():
				return NodeStorage{}, ErrDataRetention
			case <-ticker.C:
			}
		}
		if initial.Generation == 0 {
			cache[node] = initial
			return initial, nil
		}
		for {
			select {
			case <-bounded.Done():
				return NodeStorage{}, ErrDataRetention
			case <-ticker.C:
				value, err := readStorage(bounded, read, node)
				if err != nil {
					continue
				}
				if (value.Generation == initial.Generation && value.Sequence > initial.Sequence && value.Sequence-initial.Sequence >= 2) || (value.Generation > initial.Generation && value.Sequence >= 2) {
					cache[node] = value
					return value, nil
				}
			}
		}
	}
}
func hasDataNode(service swarm.Service) bool {
	if service.Spec.TaskTemplate.Placement == nil {
		return false
	}
	for _, c := range service.Spec.TaskTemplate.Placement.Constraints {
		if strings.HasPrefix(strings.ReplaceAll(c, " ", ""), "node.id==") {
			return true
		}
	}
	return false
}

// Task metadata alone cannot prove which mounts a running remote container uses.
func retainedActiveTask(task swarm.Task, evidence NodeStorage) error {
	if !taskActive(task.Status.State) {
		return nil
	}
	if task.Status.ContainerStatus == nil || task.Status.ContainerStatus.ContainerID == "" {
		return ErrDataRetention
	}
	id := task.Status.ContainerStatus.ContainerID
	actual, exists := evidence.Containers[id]
	spec := task.Spec.ContainerSpec
	if !exists || actual.ID != id || actual.Config == nil || actual.State == nil || spec == nil || actual.Config.Image != spec.Image || actual.Config.Labels["com.docker.swarm.service.id"] != task.ServiceID || actual.Config.Labels["com.docker.swarm.task.id"] != task.ID {
		return ErrDataRetention
	}
	expected := map[string]string{}
	for _, m := range spec.Mounts {
		if string(m.Type) == "volume" {
			expected[path.Clean(m.Target)] = m.Source
		}
	}
	actualVolumes := 0
	for _, m := range actual.Mounts {
		if string(m.Type) == "tmpfs" {
			continue
		}
		if string(m.Type) != "volume" || m.Name == "" || expected[path.Clean(m.Destination)] != m.Name {
			return ErrDataRetention
		}
		actualVolumes++
	}
	if actualVolumes != len(expected) {
		return ErrDataRetention
	}
	return nil
}

// A first installation cannot adopt a previously unrecorded data volume.
func (d Docker) VerifyNewStorage(ctx context.Context, rt model.Runtime, app model.Application, node string, names []string) error {
	read := d.ReadNode
	if read == nil {
		cli, err := d.open(rt, app)
		if err != nil {
			return err
		}
		defer cli.Close()
		read = localNodeReader(cli)
	}
	evidence, err := executionNodeReader(read)(ctx, node)
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, exists := evidence.Volumes[name]; exists {
			return ErrDataRetention
		}
	}
	return nil
}

// Recheck actual running mounts before writes, not only the existence of the
// original volumes: an externally changed Service cannot be silently repaired.
func verifyApplicationMounts(evidence NodeStorage, project string, volumes []RetainedVolume) error {
	known := map[string]RetainedVolume{}
	writers := map[string]string{}
	for _, v := range volumes {
		if v.NodeID == evidence.NodeID {
			known[v.Name] = v
		}
	}
	for _, c := range evidence.Containers {
		if c.Config == nil || c.State == nil {
			return ErrDataRetention
		}
		if !c.State.Running && !c.State.Restarting && !c.State.Paused {
			continue
		}
		if c.Config.Labels["com.docker.stack.namespace"] != project {
			for _, m := range c.Mounts {
				if _, shared := known[m.Name]; shared && m.RW {
					return ErrDataRetention
				}
			}
			continue
		}
		if c.Config.Labels["com.docker.swarm.service.id"] == "" || c.Config.Labels["com.docker.swarm.task.id"] == "" {
			return ErrDataRetention
		}
		targets := map[string]bool{}
		for _, m := range c.Mounts {
			if string(m.Type) == "tmpfs" {
				continue
			}
			v, exists := known[m.Name]
			if string(m.Type) != "volume" || !exists || (v.Target != "" && path.Clean(m.Destination) != v.Target) {
				return ErrDataRetention
			}
			if previous, exists := writers[m.Name]; exists && previous != c.ID {
				return ErrDataRetention
			}
			writers[m.Name] = c.ID
			targets[path.Clean(m.Destination)] = true
		}
		declared, exists := evidence.Images[c.Config.Image]
		if !exists {
			return ErrDataRetention
		}
		for _, target := range declared {
			if !targets[path.Clean(target)] {
				return ErrDataRetention
			}
		}
	}
	return nil
}
