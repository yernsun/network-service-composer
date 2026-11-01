package maintenance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

// A plan binds cluster resources, topology and target identities. The Manager
// serving a read may differ from the fixed maintenance executor; its local
// NodeID/EngineID are diagnostic evidence, not changes to the approved targets.
type Snapshot struct {
	ClusterID        string                        `json:"cluster_id"`
	NodeID           string                        `json:"-"`
	EngineID         string                        `json:"-"`
	Components       map[string]bootstrap.Resource `json:"components"`
	Topology         bootstrap.ManagerTopology     `json:"topology"`
	SchemaCompatible bool                          `json:"schema_compatible"`
	Healthy          bool                          `json:"healthy"`
	Data             bootstrap.DatabasePin         `json:"data"`
	Targets          []bootstrap.PlanTarget        `json:"targets"`
	Services         map[string]swarm.Service      `json:"-"`
}

type Runtime interface {
	Observe(context.Context) (Snapshot, error)
	PrepareImages(context.Context, bootstrap.Release) (bootstrap.Resource, error)
	Update(context.Context, bootstrap.Resource, swarm.ServiceSpec) (bootstrap.Resource, error)
	Verify(context.Context, string) error
}

type Driver struct {
	Record     *bootstrap.Record
	Runtime    Runtime
	Journal    interface{ Save(*bootstrap.Record) error }
	Target     *bootstrap.Release
	Components []string
}

func Latest(record *bootstrap.Record, id string) bootstrap.Resource {
	result := bootstrap.Resource{}
	for key, receipt := range record.Resources {
		if strings.HasPrefix(key, "intent:") {
			continue
		}
		if receipt.ID == id && receipt.Kind == "service" && receipt.Version >= result.Version {
			result = receipt
		}
	}
	return result
}
func component(record *bootstrap.Record, key string) bootstrap.Resource {
	receipt := record.Resources["reserve:"+key]
	if key == "postgresql" {
		receipt = record.Resources["service:"+key]
	}
	return Latest(record, receipt.ID)
}
func nonImageDigest(spec swarm.ServiceSpec) string {
	spec.TaskTemplate.ForceUpdate = 0
	if spec.TaskTemplate.ContainerSpec != nil {
		copy := *spec.TaskTemplate.ContainerSpec
		copy.Image = ""
		spec.TaskTemplate.ContainerSpec = &copy
	}
	return bootstrap.Digest(spec)
}

func (d *Driver) Observe(ctx context.Context) (any, error) {
	actual, err := d.Runtime.Observe(ctx)
	if err != nil {
		return nil, err
	}
	if actual.ClusterID != d.Record.ClusterID || !actual.SchemaCompatible || actual.Data != d.Record.Database || actual.Data.NodeID == "" || actual.Data.Volume == "" {
		return nil, &bootstrap.MaintenanceError{Code: "ownership_mismatch"}
	}
	for _, key := range []string{"controller", "postgresql", "nginx", "nginx-sandbox"} {
		expected := component(d.Record, key)
		value := actual.Components[key]
		if expected.ID == "" || value.ID != expected.ID || (!MatchesRecordedSpec(d.Record, expected, value)) || value.Version < expected.Version {
			return nil, &bootstrap.MaintenanceError{Code: "external_drift"}
		}
	}
	return actual, nil
}

func (d *Driver) Plan(ctx context.Context, input bootstrap.PlanRequest) (bootstrap.Plan, error) {
	if input.Revision != d.Record.Revision {
		return bootstrap.Plan{}, &bootstrap.MaintenanceError{Code: "stale_plan"}
	}
	switch input.Action {
	case bootstrap.Verify, bootstrap.Repair, bootstrap.Upgrade, bootstrap.Rollback:
	default:
		return bootstrap.Plan{}, &bootstrap.MaintenanceError{Code: "unsupported_action"}
	}
	if input.Action != bootstrap.Upgrade && input.Release != nil || input.Action != bootstrap.Repair && len(input.Components) > 0 {
		return bootstrap.Plan{}, &bootstrap.MaintenanceError{Code: "unsupported_action_input"}
	}
	observed, err := d.Observe(ctx)
	if err != nil {
		return bootstrap.Plan{}, err
	}
	snapshot := observed.(Snapshot)
	if !snapshot.Topology.QuorumAvailable {
		return bootstrap.Plan{}, &bootstrap.MaintenanceError{Code: "manager_unavailable"}
	}
	target := input.Release
	if input.Action == bootstrap.Upgrade {
		if target == nil {
			return bootstrap.Plan{}, &bootstrap.MaintenanceError{Code: "release_required"}
		}
		if err = target.Validate(); err != nil {
			return bootstrap.Plan{}, err
		}
		if d.Record.Release == nil {
			return bootstrap.Plan{}, &bootstrap.MaintenanceError{Code: "baseline_missing"}
		}
		if registry(target.Images.Controller) != registry(d.Record.Release.Images.Controller) || registry(target.Images.Gateway) != registry(d.Record.Release.Images.Gateway) {
			return bootstrap.Plan{}, &bootstrap.MaintenanceError{Code: "registry_preparation_requires_cli"}
		}
		if target.Images.PostgreSQL != d.Record.Release.Images.PostgreSQL {
			return bootstrap.Plan{}, &bootstrap.MaintenanceError{Code: "database_upgrade_requires_cli"}
		}
		if !snapshot.Healthy {
			return bootstrap.Plan{}, &bootstrap.MaintenanceError{Code: "health_unconfirmed"}
		}
	}
	if input.Action == bootstrap.Rollback {
		if len(d.Record.History) == 0 {
			return bootstrap.Plan{}, &bootstrap.MaintenanceError{Code: "rollback_unavailable"}
		}
		previous := d.Record.History[len(d.Record.History)-1]
		if previous.ConfigurationDigest != d.Record.ConfigDigest || previous.Release.Images.PostgreSQL != d.Record.Database.Image {
			return bootstrap.Plan{}, &bootstrap.MaintenanceError{Code: "incompatible_rollback"}
		}
		if err = previous.Release.Validate(); err != nil {
			return bootstrap.Plan{}, err
		}
		for key, spec := range snapshot.Services {
			if previous.Components[key].ID != snapshot.Components[key].ID || previous.NonImageDigests[key] == "" || previous.NonImageDigests[key] != nonImageDigest(spec.Spec) {
				return bootstrap.Plan{}, &bootstrap.MaintenanceError{Code: "incompatible_rollback"}
			}
		}
		value := previous.Release
		target = &value
	}
	keys := input.Components
	if len(keys) == 0 {
		keys = []string{"controller", "nginx", "nginx-sandbox"}
	}
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] || (key != "controller" && key != "nginx" && key != "nginx-sandbox" && key != "postgresql") {
			return bootstrap.Plan{}, &bootstrap.MaintenanceError{Code: "unsupported_component"}
		}
		seen[key] = true
	}
	if input.Action != bootstrap.Repair {
		keys = []string{"controller", "nginx", "nginx-sandbox"}
	}
	steps := []bootstrap.Step{}
	if input.Action != bootstrap.Verify {
		if target != nil {
			steps = append(steps, bootstrap.Step{Key: "prepare-images", Description: "Verify immutable target images and their supported release contracts"})
		}
		if input.Action == bootstrap.Upgrade {
			steps = append(steps, bootstrap.Step{Key: "retain-release", Description: "Retain the compatible rollback baseline without changing database or business volumes"})
		}
		for _, key := range keys {
			before := snapshot.Components[key]
			steps = append(steps, bootstrap.Step{Key: "update:" + key, Description: "Update only the owned component using an exact current object version", Before: &before}, bootstrap.Step{Key: "verify:" + key, Description: "Wait for actual target tasks, fresh loaded evidence and access"})
		}
	}
	steps = append(steps, bootstrap.Step{Key: "verify:installation", Description: "Verify current installation identities, loaded configuration and actual access"}, bootstrap.Step{Key: "finalize-maintenance", Description: "Record the verified release and preserve original database ownership"})
	plan, err := bootstrap.NewPlan(*d.Record, input.Action, snapshot, steps, timeNow())
	if err != nil {
		return plan, err
	}
	scope := bootstrap.PlanScope{Targets: append([]bootstrap.PlanTarget{}, snapshot.Targets...), Mode: "multi", ClusterID: d.Record.ClusterID, Images: bootstrap.Images{}, DataNode: snapshot.Data.NodeID, DataVolume: snapshot.Data.Volume, DatabaseSinglePointOfFailure: true, Topology: snapshot.Topology, Release: target, Components: keys}
	if snapshot.Topology.Managers == 1 {
		scope.Mode = "single"
		plan.Impact = append(plan.Impact, "Single-node maintenance interrupts the console and Gateway; use the independent CLI if access is lost.")
	}
	if target != nil {
		scope.Images = target.Images
	}
	return plan.WithScope(scope), nil
}

func (d *Driver) Check(ctx context.Context, step bootstrap.Step) error {
	// A component rollout can temporarily remove every Controller listener.
	// Its bounded read-only verification must be allowed to wait independently.
	if strings.HasPrefix(step.Key, "verify:") {
		return nil
	}
	observed, err := d.Observe(ctx)
	// A completed Controller rollout may still be restarting its only listener
	// when an interrupted executor resumes. Recheck ownership read-only until
	// authenticated binding evidence returns; new writes still fail immediately.
	for err != nil && step.State == bootstrap.Succeeded {
		var unavailable *bootstrap.MaintenanceError
		if !errors.As(err, &unavailable) || unavailable.Code != "database_identity_unconfirmed" {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
		observed, err = d.Observe(ctx)
	}
	if err != nil {
		return err
	}
	snapshot := observed.(Snapshot)
	if step.Before != nil && step.State != bootstrap.Succeeded {
		key := strings.TrimPrefix(step.Key, "update:")
		current := snapshot.Components[key]
		if current.ID != step.Before.ID || current.SpecDigest != step.Before.SpecDigest {
			return &bootstrap.MaintenanceError{Code: "external_drift"}
		}
	}
	return nil
}
func (d *Driver) Apply(ctx context.Context, step bootstrap.Step) (bootstrap.Resource, error) {
	switch {
	case step.Key == "prepare-images":
		if d.Target == nil {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "release target is missing"}
		}
		return d.Runtime.PrepareImages(ctx, *d.Target)
	case step.Key == "retain-release":
		if d.Record.Release == nil {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "rollback baseline is missing"}
		}
		actual, err := d.Runtime.Observe(ctx)
		if err != nil {
			return bootstrap.Resource{}, err
		}
		previous := bootstrap.ReleaseReceipt{Release: *d.Record.Release, Components: actual.Components, ConfigurationDigest: d.Record.ConfigDigest, NonImageDigests: map[string]string{}, Revision: d.Record.Revision, RecordedAt: timeNow()}
		for key, value := range actual.Services {
			previous.NonImageDigests[key] = nonImageDigest(value.Spec)
		}
		d.Record.History = append(d.Record.History, previous)
		if err = d.Journal.Save(d.Record); err != nil {
			return bootstrap.Resource{}, err
		}
		return bootstrap.Resource{Kind: "release", ID: bootstrap.Digest(previous), SpecDigest: bootstrap.Digest(previous)}, nil
	case strings.HasPrefix(step.Key, "update:"):
		key := strings.TrimPrefix(step.Key, "update:")
		if key != "controller" && key != "nginx" && key != "nginx-sandbox" && key != "postgresql" {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "component is outside the maintenance allowlist"}
		}
		actual, err := d.Runtime.Observe(ctx)
		if err != nil {
			return bootstrap.Resource{}, err
		}
		spec, ok := actual.Services[key]
		if !ok || step.Before == nil {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "owned component receipt is missing"}
		}
		before := actual.Components[key]
		if before.ID != step.Before.ID || before.SpecDigest != step.Before.SpecDigest {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "component changed after approval"}
		}
		if d.Target == nil {
			spec.Spec.TaskTemplate.ForceUpdate++
		} else {
			image := d.Target.Images.Controller
			if key == "nginx" || key == "nginx-sandbox" {
				image = d.Target.Images.Gateway
			}
			if key == "postgresql" {
				return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "database image upgrades are not supported"}
			}
			copy := *spec.Spec.TaskTemplate.ContainerSpec
			copy.Image = image
			spec.Spec.TaskTemplate.ContainerSpec = &copy
		}
		// Status-only object version changes can be rebased after exact Spec proof.
		// Persist the actual version and intended Spec before the external CAS write.
		intended := bootstrap.Resource{Kind: "service", ID: before.ID, Version: before.Version, SpecDigest: bootstrap.Digest(spec.Spec), Image: spec.Spec.TaskTemplate.ContainerSpec.Image}
		d.Record.Resources["intent:"+step.Key] = intended
		if err = d.Journal.Save(d.Record); err != nil {
			return bootstrap.Resource{}, err
		}
		receipt, err := d.Runtime.Update(ctx, before, spec.Spec)
		if err != nil {
			return intended, err
		}
		return receipt, nil
	case strings.HasPrefix(step.Key, "verify:"):
		key := strings.TrimPrefix(step.Key, "verify:")
		if err := d.Runtime.Verify(ctx, key); err != nil {
			return bootstrap.Resource{}, err
		}
		return bootstrap.Resource{Kind: "maintenance-verification", ID: d.Record.ID + ":" + key, SpecDigest: bootstrap.Digest(component(d.Record, key))}, nil
	case step.Key == "finalize-maintenance":
		if d.Target != nil {
			copy := *d.Target
			if len(d.Record.Operations) > 0 && d.Record.Operations[len(d.Record.Operations)-1].Plan.Action == bootstrap.Rollback && len(d.Record.History) > 0 && bootstrap.Digest(d.Record.History[len(d.Record.History)-1].Release) == bootstrap.Digest(copy) {
				d.Record.History = d.Record.History[:len(d.Record.History)-1]
			}
			d.Record.Release = &copy
		}
		if err := d.Journal.Save(d.Record); err != nil {
			return bootstrap.Resource{}, err
		}
		return bootstrap.Resource{Kind: "maintenance", ID: d.Record.ID, SpecDigest: bootstrap.Digest(d.Record.Release)}, nil
	default:
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "step is outside the maintenance allowlist"}
	}
}
func (d *Driver) Reconcile(ctx context.Context, step bootstrap.Step) (*bootstrap.Resource, error) {
	if strings.HasPrefix(step.Key, "update:") {
		actual, err := d.Runtime.Observe(ctx)
		if err != nil {
			return nil, err
		}
		key := strings.TrimPrefix(step.Key, "update:")
		current := actual.Components[key]
		intended := d.Record.Resources["intent:"+step.Key]
		if intended.ID != "" && current.ID == intended.ID && current.SpecDigest == intended.SpecDigest {
			return &current, nil
		}
		if step.Before != nil && current.ID == step.Before.ID && current.SpecDigest == step.Before.SpecDigest {
			return nil, &bootstrap.RetrySafe{Reason: "same owned Spec is unchanged; retry the current-version update"}
		}
		return nil, fmt.Errorf("maintenance update outcome does not match a recorded receipt")
	}
	if step.Key == "prepare-images" || strings.HasPrefix(step.Key, "verify:") || step.Key == "finalize-maintenance" {
		return nil, &bootstrap.RetrySafe{Reason: "repeat the same bounded image preparation or read-only verification"}
	}
	if step.Key == "retain-release" {
		if len(d.Record.History) == 0 || d.Record.History[len(d.Record.History)-1].Revision != d.Record.Revision {
			return nil, &bootstrap.RetrySafe{Reason: "retain the current owned release baseline"}
		}
		previous := d.Record.History[len(d.Record.History)-1]
		value := bootstrap.Resource{Kind: "release", ID: bootstrap.Digest(previous), SpecDigest: bootstrap.Digest(previous)}
		return &value, nil
	}
	return nil, errors.New("maintenance outcome is unconfirmed")
}

func timeNow() time.Time { return time.Now().UTC() }

func registry(image string) string { return bootstrap.ImageRegistry(image) }

// MatchesRecordedSpec lets earlier steps observe an approved update whose acknowledgement
// was lost. Accept only that durable intent on its already known Service ID;
// the Executor must still reconcile the UNKNOWN step before advancing.
func MatchesRecordedSpec(record *bootstrap.Record, expected, actual bootstrap.Resource) bool {
	if expected.SpecDigest == actual.SpecDigest {
		return true
	}
	for _, operation := range record.Operations {
		for _, step := range operation.Plan.Steps {
			if step.State != bootstrap.Running && step.State != bootstrap.Unknown {
				continue
			}
			intended := record.Resources["intent:"+step.Key]
			if (strings.HasPrefix(step.Key, "update:") || step.Key == "prepare:controller") && step.Before != nil && step.Before.ID == expected.ID && intended.ID == actual.ID && intended.SpecDigest == actual.SpecDigest && actual.Version >= intended.Version {
				return true
			}
		}
	}
	return false
}
