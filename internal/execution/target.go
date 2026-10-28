// Package execution binds durable operation attempts to provisioned executors.
// Observation, scheduling authority and Docker identity are independent proofs.
package execution

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

var (
	ErrUnavailable = errors.New("no verified execution node is available")
	ErrFenced      = errors.New("execution authority is unavailable or stale")
	ErrUnknown     = errors.New("execution acceptance is uncertain")
)

type Target struct {
	InstanceID          string    `json:"instance_id"`
	NodeID              string    `json:"node_id"`
	EngineID            string    `json:"engine_id"`
	ClusterID           string    `json:"cluster_id"`
	CollectorGeneration int64     `json:"collector_generation"`
	CollectorOwner      int64     `json:"collector_owner"`
	ObservedAt          time.Time `json:"observed_at"`
}

type TargetIdentity struct {
	InstanceID string `json:"instance_id"`
	NodeID     string `json:"node_id"`
	EngineID   string `json:"engine_id"`
	ClusterID  string `json:"cluster_id"`
}

func (t Target) Identity() TargetIdentity {
	return TargetIdentity{t.InstanceID, t.NodeID, t.EngineID, t.ClusterID}
}

type Verifier struct {
	Store      storage.Store
	Config     config.Config
	Reports    *nodecontrol.Reports
	Leader     controlplane.StateStore
	CheckOwner func(context.Context, string, int64) error
	Now        func(context.Context) (time.Time, error)
	Keys       *security.Keyring
}

func NodeID(endpoint string) string {
	if !strings.HasPrefix(endpoint, "node://") {
		return ""
	}
	value := strings.TrimPrefix(endpoint, "node://")
	if value == "" || strings.ContainsAny(value, "/:?#@") {
		return ""
	}
	return value
}

func (v *Verifier) binding(node string) (config.ControllerNodeConfig, error) {
	if node == "" {
		return config.ControllerNodeConfig{}, ErrUnavailable
	}
	for _, binding := range v.Config.Controller.Nodes {
		if binding.NodeID == node {
			return binding, nil
		}
	}
	return config.ControllerNodeConfig{}, ErrUnavailable
}

// Select never changes a Compose Engine. A Swarm alternate must belong to the
// immutable cluster, have fresh complete inventory and own its collector lease.
func (v *Verifier) Select(ctx context.Context, rt model.Runtime) (Target, error) {
	if v.Config.Controller.RecoveryMode || !v.Config.Management.Enabled || !rt.Enabled || !rt.ManagementEnabled || rt.Type != model.RuntimeDocker && rt.Type != model.RuntimeSwarm {
		return Target{}, ErrUnavailable
	}
	original, err := v.binding(NodeID(rt.Endpoint))
	if err != nil || v.Reports == nil || v.CheckOwner == nil || rt.CredentialRef != "" {
		return Target{}, ErrUnavailable
	}
	candidates := []config.ControllerNodeConfig{original}
	if rt.Type == model.RuntimeSwarm {
		if rt.ClusterID == "" || rt.ClusterID != original.ClusterID {
			return Target{}, ErrUnavailable
		}
		alternates := slices.Clone(v.Config.Controller.Nodes)
		slices.SortFunc(alternates, func(a, b config.ControllerNodeConfig) int { return strings.Compare(a.InstanceID, b.InstanceID) })
		for _, binding := range alternates {
			if binding.NodeID != original.NodeID && binding.ClusterID == rt.ClusterID {
				candidates = append(candidates, binding)
			}
		}
	}
	for _, binding := range candidates {
		report, session, err := v.observation(ctx, binding)
		if err != nil {
			continue
		}
		if rt.Type == model.RuntimeSwarm && (!report.Observation.Info.Swarm.ControlAvailable || report.Observation.Info.Swarm.Cluster == nil || !Quorum(report.Observation.Info, report.Observation.Nodes)) {
			continue
		}
		return Target{InstanceID: binding.InstanceID, NodeID: binding.NodeID, EngineID: binding.EngineID, ClusterID: binding.ClusterID, CollectorGeneration: session.Generation, CollectorOwner: session.Owner, ObservedAt: report.Observation.At}, nil
	}
	return Target{}, ErrUnavailable
}

// Volume requests carry the original data Engine in their encrypted input.
// Manager failover must never turn a local-volume request into data migration.
func (v *Verifier) SelectPinned(ctx context.Context, rt model.Runtime, node, engine string) (Target, error) {
	if v.Reports == nil || v.CheckOwner == nil || !rt.Enabled || !rt.ManagementEnabled || !v.Config.Management.Enabled || v.Config.Controller.RecoveryMode || rt.CredentialRef != "" || rt.Type != model.RuntimeDocker && rt.Type != model.RuntimeSwarm {
		return Target{}, ErrUnavailable
	}
	binding, err := v.binding(node)
	if err != nil || engine == "" || binding.EngineID != engine {
		return Target{}, ErrUnavailable
	}
	if rt.Type == model.RuntimeDocker && NodeID(rt.Endpoint) != node {
		return Target{}, ErrUnavailable
	}
	if rt.Type == model.RuntimeSwarm && (rt.ClusterID == "" || rt.ClusterID != binding.ClusterID) {
		return Target{}, ErrUnavailable
	}
	report, session, err := v.observation(ctx, binding)
	if err != nil {
		return Target{}, ErrUnavailable
	}
	if rt.Type == model.RuntimeSwarm {
		if _, err := v.Select(ctx, rt); err != nil {
			return Target{}, ErrUnavailable
		}
	}
	return Target{InstanceID: binding.InstanceID, NodeID: binding.NodeID, EngineID: binding.EngineID, ClusterID: binding.ClusterID, CollectorGeneration: session.Generation, CollectorOwner: session.Owner, ObservedAt: report.Observation.At}, nil
}

func (v *Verifier) observation(ctx context.Context, binding config.ControllerNodeConfig) (nodecontrol.Report, nodecontrol.Session, error) {
	report, err := v.Reports.Read(ctx, binding.NodeID)
	if err != nil {
		return report, nodecontrol.Session{}, err
	}
	if report.Observation.Health == nil || report.Observation.Health.Execution != "enabled" {
		return report, nodecontrol.Session{}, ErrUnavailable
	}
	info := report.Observation.Info
	if binding.InstanceID == "" || binding.EngineID == "" || binding.ClusterID == "" || report.EngineID != binding.EngineID || info.ID != binding.EngineID || info.Swarm.NodeID != binding.NodeID || info.Swarm.Cluster != nil && info.Swarm.Cluster.ID != binding.ClusterID {
		return report, nodecontrol.Session{}, ErrFenced
	}
	doc, err := v.Store.GetControlDocument(ctx, "node-session:"+binding.NodeID)
	if err != nil {
		return report, nodecontrol.Session{}, err
	}
	var session nodecontrol.Session
	if doc.Kind != "node-session" || doc.ParentID != binding.NodeID || json.Unmarshal(doc.Payload, &session) != nil || session.Owner <= 0 || session.Generation != report.Generation {
		return report, session, ErrFenced
	}
	if err = v.CheckOwner(ctx, nodecontrol.LockKey(binding.NodeID), session.Owner); err != nil {
		return report, session, ErrFenced
	}
	return report, session, nil
}

// Quorum describes only current management evidence. It cannot imply workload
// availability or prove an independent physical failure domain.
func Quorum(info system.Info, nodes []swarm.Node) bool {
	managers, reachable, leaders := 0, 0, 0
	for _, node := range nodes {
		if node.Spec.Role != swarm.NodeRoleManager {
			continue
		}
		managers++
		if node.ManagerStatus != nil && node.ManagerStatus.Reachability == swarm.ReachabilityReachable {
			reachable++
			if node.ManagerStatus.Leader {
				leaders++
			}
		}
	}
	return managers > 0 && managers == info.Swarm.Managers && reachable >= managers/2+1 && leaders == 1
}
