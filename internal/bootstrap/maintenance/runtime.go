package maintenance

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

type Engine interface {
	Engine(context.Context, string, string, any, any) error
}

// DockerRuntime uses an authenticated Manager and typed health evidence. It has
// no SSH/private CA dependency and cannot execute arbitrary commands.
type DockerRuntime struct {
	RegistryAuth map[string]string
	// CLI preparation can verify/pull on every authenticated inventory node.
	Prepare  func(context.Context, bootstrap.Release) (bootstrap.Resource, error)
	Engine   Engine
	Record   *bootstrap.Record
	Schema   func(context.Context) error
	Physical func(context.Context) error
	Health   func(context.Context) error
}

func Receipt(value swarm.Service) bootstrap.Resource {
	image := ""
	if value.Spec.TaskTemplate.ContainerSpec != nil {
		image = value.Spec.TaskTemplate.ContainerSpec.Image
	}
	return bootstrap.Resource{Kind: "service", ID: value.ID, Version: value.Version.Index, SpecDigest: bootstrap.Digest(value.Spec), Image: image}
}
func (d *DockerRuntime) Observe(ctx context.Context) (Snapshot, error) {
	result := Snapshot{Components: map[string]bootstrap.Resource{}, Services: map[string]swarm.Service{}}
	var info struct {
		ID    string
		Swarm struct {
			NodeID           string
			ControlAvailable bool
			Cluster          *struct{ ID string }
		}
	}
	if err := d.Engine.Engine(ctx, "GET", "/info", nil, &info); err != nil {
		return result, err
	}
	if !info.Swarm.ControlAvailable || info.Swarm.Cluster == nil {
		return result, &bootstrap.MaintenanceError{Code: "manager_unavailable"}
	}
	result.ClusterID, result.NodeID, result.EngineID = info.Swarm.Cluster.ID, info.Swarm.NodeID, info.ID
	var nodes []swarm.Node
	if err := d.Engine.Engine(ctx, "GET", "/nodes", nil, &nodes); err != nil {
		return result, err
	}
	result.Topology.NodeIDs = []string{}
	dataReady := false
	for _, node := range nodes {
		ready := node.Status.State == swarm.NodeStateReady && node.Spec.Availability == swarm.NodeAvailabilityActive
		if node.ID == d.Record.Database.NodeID && ready {
			dataReady = true
		}
		if node.Spec.Role != swarm.NodeRoleManager {
			continue
		}
		result.Topology.Managers++
		result.Topology.NodeIDs = append(result.Topology.NodeIDs, node.ID)
		if node.Status.State == swarm.NodeStateReady && node.ManagerStatus != nil && (node.ManagerStatus.Leader || node.ManagerStatus.Reachability == swarm.ReachabilityReachable) {
			result.Topology.Reachable++
		}
	}
	result.Targets = []bootstrap.PlanTarget{}
	for name, identity := range d.Record.Nodes {
		target := bootstrap.PlanTarget{Name: name, NodeID: identity.NodeID, EngineID: identity.EngineID}
		for _, node := range nodes {
			if node.ID == identity.NodeID {
				target.Address, target.Manager = node.Status.Addr, node.Spec.Role == swarm.NodeRoleManager
				break
			}
		}
		result.Targets = append(result.Targets, target)
	}
	sort.Slice(result.Targets, func(i, j int) bool { return result.Targets[i].Name < result.Targets[j].Name })
	sort.Strings(result.Topology.NodeIDs)
	result.Topology.Majority = result.Topology.Managers/2 + 1
	result.Topology.TolerableFailures = (result.Topology.Managers - 1) / 2
	result.Topology.QuorumAvailable = result.Topology.Managers > 0 && result.Topology.Reachable >= result.Topology.Majority
	for _, key := range []string{"controller", "postgresql", "nginx", "nginx-sandbox"} {
		expected := component(d.Record, key)
		if expected.ID == "" {
			return result, &bootstrap.MaintenanceError{Code: "ownership_mismatch"}
		}
		var actual swarm.Service
		if err := d.Engine.Engine(ctx, "GET", "/services/"+url.PathEscape(expected.ID), nil, &actual); err != nil {
			return result, err
		}
		if actual.ID != expected.ID || actual.Spec.TaskTemplate.ContainerSpec == nil {
			return result, &bootstrap.MaintenanceError{Code: "ownership_mismatch"}
		}
		result.Components[key], result.Services[key] = Receipt(actual), actual
	}
	pg := result.Services["postgresql"].Spec
	pinned := false
	if pg.TaskTemplate.Placement != nil {
		for _, constraint := range pg.TaskTemplate.Placement.Constraints {
			if strings.ReplaceAll(constraint, " ", "") == "node.id=="+d.Record.Database.NodeID {
				pinned = true
			}
		}
	}
	volume := false
	for _, value := range pg.TaskTemplate.ContainerSpec.Mounts {
		if value.Type == mount.TypeVolume && value.Source == d.Record.Database.Volume && value.Target == "/var/lib/postgresql" {
			volume = true
		}
	}
	if !dataReady || !pinned || !volume || pg.Mode.Replicated == nil || pg.Mode.Replicated.Replicas == nil || *pg.Mode.Replicated.Replicas != 1 || pg.TaskTemplate.ContainerSpec.Image != d.Record.Database.Image {
		return result, &bootstrap.MaintenanceError{Code: "database_pin_unconfirmed"}
	}
	if d.Physical == nil || d.Physical(ctx) != nil {
		return result, &bootstrap.MaintenanceError{Code: "database_identity_unconfirmed"}
	}
	result.Data = d.Record.Database
	if d.Schema == nil {
		return result, &bootstrap.MaintenanceError{Code: "schema_verification_unavailable"}
	}
	if err := d.Schema(ctx); err != nil {
		return result, err
	}
	result.SchemaCompatible = true
	result.Healthy = d.Health != nil && d.Health(ctx) == nil
	return result, nil
}
func (d *DockerRuntime) PrepareImages(ctx context.Context, target bootstrap.Release) (bootstrap.Resource, error) {
	if err := target.Validate(); err != nil {
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "release compatibility is unsupported"}
	}
	if d.Prepare != nil {
		return d.Prepare(ctx, target)
	}
	var info struct{ OSType, Architecture string }
	if err := d.Engine.Engine(ctx, "GET", "/info", nil, &info); err != nil {
		return bootstrap.Resource{}, err
	}
	architecture := bootstrap.ImageArchitecture(info.Architecture)
	if info.OSType != "linux" || architecture == "" {
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "maintenance Engine platform is unconfirmed"}
	}
	var nodes []swarm.Node
	if err := d.Engine.Engine(ctx, "GET", "/nodes", nil, &nodes); err != nil {
		return bootstrap.Resource{}, err
	}
	confirmed := map[string]bool{}
	for _, node := range nodes {
		for _, identity := range d.Record.Nodes {
			if node.ID != identity.NodeID {
				continue
			}
			if node.Description.Platform.OS != "linux" || bootstrap.ImageArchitecture(node.Description.Platform.Architecture) != architecture {
				return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "target platforms require per-node CLI image preparation"}
			}
			confirmed[node.ID] = true
		}
	}
	if len(confirmed) != len(d.Record.Nodes) {
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "target node platforms are unconfirmed"}
	}
	for _, item := range []struct{ key, image string }{{"controller", target.Images.Controller}, {"gateway", target.Images.Gateway}} {
		var image struct {
			Os, Architecture string
			Config           struct{ Labels map[string]string }
		}
		path := "/images/" + url.PathEscape(item.image) + "/json"
		if err := d.Engine.Engine(ctx, "GET", path, nil, &image); err != nil {
			input := bootstrap.RegistryRequest{Authorization: d.RegistryAuth[bootstrap.ImageRegistry(item.image)]}
			if err = d.Engine.Engine(ctx, "POST", "/images/create?fromImage="+url.QueryEscape(item.image), input, nil); err != nil {
				return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "target image could not be prepared; no system service was changed"}
			}
			if err = d.Engine.Engine(ctx, "GET", path, nil, &image); err != nil {
				return bootstrap.Resource{}, err
			}
		}
		labels := image.Config.Labels
		if image.Os != "linux" || image.Architecture != architecture || labels["io.nsc.bootstrap.contract"] != "1" || labels["io.nsc.bootstrap.protocol"] != "2" || item.key == "controller" && (labels["io.nsc.bootstrap.schema"] != "18" || labels["io.nsc.bootstrap.maintenance"] != "1") {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "target image compatibility labels are unverified"}
		}
	}
	return bootstrap.Resource{Kind: "images", ID: d.Record.ID, SpecDigest: bootstrap.Digest(target)}, nil
}
func (d *DockerRuntime) Update(ctx context.Context, before bootstrap.Resource, spec swarm.ServiceSpec) (bootstrap.Resource, error) {
	var actual swarm.Service
	if err := d.Engine.Engine(ctx, "GET", "/services/"+url.PathEscape(before.ID), nil, &actual); err != nil {
		return bootstrap.Resource{}, err
	}
	if actual.ID != before.ID || actual.Version.Index != before.Version || bootstrap.Digest(actual.Spec) != before.SpecDigest {
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "service changed before its versioned update"}
	}
	intended := bootstrap.Resource{Kind: "service", ID: before.ID, SpecDigest: bootstrap.Digest(spec), Image: spec.TaskTemplate.ContainerSpec.Image}
	path := fmt.Sprintf("/services/%s/update?version=%d", url.PathEscape(before.ID), before.Version)
	var input any = spec
	if auth := d.RegistryAuth[bootstrap.ImageRegistry(spec.TaskTemplate.ContainerSpec.Image)]; auth != "" {
		input = bootstrap.RegistryRequest{Body: spec, Authorization: auth}
	} else {
		path += "&registryAuthFrom=spec"
	}
	if err := d.Engine.Engine(ctx, "POST", path, input, nil); err != nil {
		return intended, err
	}
	if err := d.Engine.Engine(ctx, "GET", "/services/"+url.PathEscape(before.ID), nil, &actual); err != nil {
		return intended, err
	}
	if bootstrap.Digest(actual.Spec) != intended.SpecDigest {
		return intended, fmt.Errorf("updated component receipt is unconfirmed")
	}
	return Receipt(actual), nil
}
func (d *DockerRuntime) Verify(ctx context.Context, key string) error {
	if key != "installation" && key != "controller" && key != "nginx" && key != "nginx-sandbox" && key != "postgresql" {
		return &bootstrap.Rejected{Reason: "unsupported verification component"}
	}
	var last error
	for {
		last = d.verifyOnce(ctx, key)
		var rejected *bootstrap.Rejected
		if errors.As(last, &rejected) {
			return last
		}
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return &bootstrap.Rejected{Reason: "component health or actual access was not confirmed before the maintenance deadline"}
		case <-time.After(time.Second):
		}
	}
}
func (d *DockerRuntime) verifyOnce(ctx context.Context, key string) error {
	keys := []string{key}
	if key == "installation" {
		keys = []string{"controller", "nginx", "nginx-sandbox", "postgresql"}
	}
	for _, componentKey := range keys {
		expected := component(d.Record, componentKey)
		var service swarm.Service
		if err := d.Engine.Engine(ctx, "GET", "/services/"+url.PathEscape(expected.ID), nil, &service); err != nil {
			return err
		}
		if service.ID != expected.ID || bootstrap.Digest(service.Spec) != expected.SpecDigest {
			return fmt.Errorf("component spec differs")
		}
		if service.UpdateStatus != nil && (service.UpdateStatus.State == swarm.UpdateStatePaused || service.UpdateStatus.State == swarm.UpdateStateRollbackPaused) {
			return &bootstrap.Rejected{Reason: "Swarm rollout is paused"}
		}
		// Controller replacement must settle before Gateway changes. The final
		// installation proof also waits for every component's observation window;
		// intermediate Gateway checks still require target tasks and loaded data.
		if (key == "controller" || key == "installation") && service.UpdateStatus != nil && service.UpdateStatus.State != swarm.UpdateStateCompleted && service.UpdateStatus.State != swarm.UpdateStateRollbackCompleted {
			return fmt.Errorf("component rollout has not completed")
		}
		filters := url.QueryEscape(fmt.Sprintf(`{"service":[%q],"desired-state":["running"]}`, service.ID))
		var tasks []swarm.Task
		if err := d.Engine.Engine(ctx, "GET", "/tasks?filters="+filters, nil, &tasks); err != nil {
			return err
		}
		minimum := 1
		if service.Spec.Mode.Global != nil {
			minimum = 0
			managers := map[string]bool{}
			for _, id := range d.Record.Topology.NodeIDs {
				managers[id] = true
			}
			for _, node := range d.Record.Nodes {
				if managers[node.NodeID] && node.InstanceID != "" {
					minimum++
				}
			}
		}
		if len(tasks) != minimum {
			return fmt.Errorf("component task count is not confirmed")
		}
		for _, task := range tasks {
			if task.ServiceID != service.ID || task.Status.State != swarm.TaskStateRunning || task.Spec.ContainerSpec == nil || task.Spec.ContainerSpec.Image != expected.Image {
				return fmt.Errorf("target component tasks are not running")
			}
		}
	}
	if d.Health == nil {
		return fmt.Errorf("fresh typed maintenance health is unavailable")
	}
	return d.Health(ctx)
}
