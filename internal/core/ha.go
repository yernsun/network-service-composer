package core

import (
	"context"
	"time"

	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// HAEvidence deliberately contains no connection addresses, lock owners, raw
// diagnostics or credentials. Unknown dependencies cannot establish end-to-end HA.
type HAEvidence struct {
	ID         string     `json:"id"`
	State      string     `json:"state"`
	Reason     string     `json:"reason"`
	Source     string     `json:"source"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
	InstanceID string     `json:"instance_id,omitempty"`
	NodeID     string     `json:"node_id,omitempty"`
	Term       int64      `json:"term,omitempty"`
}
type HALayer struct {
	Name     string       `json:"name"`
	State    string       `json:"state"`
	Evidence []HAEvidence `json:"evidence"`
}
type HAOverview struct {
	GeneratedAt      time.Time `json:"generated_at"`
	EndToEndVerified bool      `json:"end_to_end_verified"`
	Layers           []HALayer `json:"layers"`
}

func haFresh(now time.Time, at *time.Time, budget time.Duration) bool {
	return at != nil && !at.IsZero() && now.Sub(*at) >= -5*time.Second && now.Sub(*at) < budget
}

func haLayer(name string, evidence []HAEvidence) HALayer {
	state := "READY"
	if len(evidence) == 0 {
		state = "UNKNOWN"
	}
	for _, item := range evidence {
		if item.State == "DEGRADED" {
			state = "DEGRADED"
			break
		}
		if item.State != "READY" {
			state = "UNKNOWN"
		}
	}
	return HALayer{Name: name, State: state, Evidence: evidence}
}

// SystemHA uses existing authenticated inventory only. It never accepts or
// probes caller-supplied destinations, and does not write health evidence.
func (c *Core) SystemHA(ctx context.Context) (HAOverview, error) {
	now := c.now()
	value := HAOverview{GeneratedAt: now, Layers: []HALayer{}}
	controller := HAEvidence{ID: c.config.Controller.InstanceID, State: "UNKNOWN", Reason: "controller_evidence_unavailable", Source: "local_readiness"}
	if c.config.Controller.RecoveryMode {
		controller.State, controller.Reason = "DISABLED", "recovery_mode"
	}
	if c.HAController != nil && !c.config.Controller.RecoveryMode {
		controller = c.HAController(ctx)
	}
	controllers := []HAEvidence{controller}
	if c.HAReports != nil {
		for _, node := range c.config.Controller.Nodes {
			if node.InstanceID == controller.InstanceID || node.InstanceID == c.config.Controller.InstanceID {
				continue
			}
			item := HAEvidence{ID: node.InstanceID, InstanceID: node.InstanceID, NodeID: node.NodeID, State: "UNKNOWN", Reason: "controller_evidence_unavailable", Source: "authenticated_node_inventory"}
			if report, err := c.HAReports.ReadControllerHealth(ctx, node.NodeID); err == nil {
				item.ObservedAt = &report.Observation.At
				health := report.Observation.Health
				if health != nil && health.InstanceID == node.InstanceID && health.Controller == "healthy" && (health.Role == "leader" || health.Role == "follower") && (health.Execution == "enabled" || health.Execution == "disabled") && health.Term > 0 && health.Term == controller.Term {
					item.State, item.Term = "READY", health.Term
					item.Reason = "follower_forwarding"
					if health.Role == "leader" {
						item.Reason = "leader_scheduling"
					}
				}
			}
			controllers = append(controllers, item)
		}
	}
	value.Layers = append(value.Layers, haLayer("controller", controllers))
	executors, quorum := []HAEvidence{}, []HAEvidence{}
	for after := ""; ; {
		runtimes, err := c.store.ListRuntimes(ctx, storage.Page{After: after, Limit: 200})
		if err != nil {
			return HAOverview{}, err
		}
		for _, rt := range runtimes {
			item := HAEvidence{ID: rt.ID, State: "UNKNOWN", Reason: "execution_not_verified", Source: "authenticated_node_inventory"}
			if !rt.Enabled || !rt.ManagementEnabled || !c.config.Management.Enabled || c.config.Controller.RecoveryMode {
				item.State, item.Reason = "DISABLED", "management_disabled"
			} else if c.HAReports != nil && execution.NodeID(rt.Endpoint) != "" {
				verifier := execution.Verifier{Store: c.store, Config: c.config, Reports: c.HAReports, CheckOwner: c.HAReports.CheckOwner}
				if target, err := verifier.Select(ctx, rt); err == nil {
					item.State, item.Reason, item.InstanceID, item.NodeID, item.ObservedAt = "READY", "verified_executor", target.InstanceID, target.NodeID, &target.ObservedAt
				}
			}
			executors = append(executors, item)
			if rt.Type == model.RuntimeSwarm {
				quorum = append(quorum, c.haQuorum(ctx, rt))
			}
		}
		if len(runtimes) < 200 {
			break
		}
		after = runtimes[len(runtimes)-1].ID
	}
	value.Layers = append(value.Layers, haLayer("execution", executors), haLayer("swarm_quorum", quorum))
	gateways := []HAEvidence{}
	for after := ""; ; {
		rows, err := c.store.ListGateways(ctx, storage.Page{After: after, Limit: 200})
		if err != nil {
			return HAOverview{}, err
		}
		for _, gateway := range rows {
			item := HAEvidence{ID: gateway.ID, State: "UNKNOWN", Reason: "gateway_evidence_unavailable", Source: "gateway_heartbeat_and_loaded_evidence", ObservedAt: gateway.LastSeenAt}
			if !gateway.Enabled {
				item.State, item.Reason = "DISABLED", "gateway_disabled"
			} else if haFresh(now, gateway.LastSeenAt, c.config.Controller.Gateway.OfflineTimeout.Std()) {
				evidence, err := c.GatewayEvidence(ctx, gateway.ID)
				if err == nil && haFresh(now, &evidence.ObservedAt, 3*time.Duration(evidence.RefreshIntervalMillis)*time.Millisecond) && haFresh(now, &evidence.ReceivedAt, 3*time.Duration(evidence.RefreshIntervalMillis)*time.Millisecond) {
					item.ObservedAt = &evidence.ObservedAt
					item.State, item.Reason = "DEGRADED", "gateway_configuration_unconfirmed"
					if evidence.ConfigurationState == "UNKNOWN" {
						item.State = "UNKNOWN"
					}
					if evidence.ConfigurationState == "CONFIRMED" && evidence.StartupState == "CONFIRMED" && gateway.Status == model.GatewayReady && evidence.LoadingConfirmed && gateway.AppliedRevision >= gateway.DesiredRevision {
						item.State, item.Reason = "READY", "gateway_configuration_confirmed"
					}
				}
			}
			gateways = append(gateways, item)
		}
		if len(rows) < 200 {
			break
		}
		after = rows[len(rows)-1].ID
	}
	value.Layers = append(value.Layers, haLayer("gateway", gateways))
	value.Layers = append(value.Layers, haLayer("ingress", []HAEvidence{{ID: "external", State: "UNKNOWN", Reason: "external_ingress_not_observed", Source: "external_monitor_required"}}))
	storageEvidence := HAEvidence{ID: c.config.Storage.Driver, State: "LIMITED", Reason: "storage_failover_unverified", Source: "database_ping", ObservedAt: &now}
	if c.config.Storage.Driver == "sqlite" {
		storageEvidence.Reason = "single_process_storage"
	}
	if c.store.Ping(ctx) != nil {
		storageEvidence.State, storageEvidence.Reason = "DEGRADED", "storage_unavailable"
	}
	value.Layers = append(value.Layers, HALayer{Name: "storage", State: storageEvidence.State, Evidence: []HAEvidence{storageEvidence}})
	return value, nil
}

func (c *Core) haQuorum(ctx context.Context, rt model.Runtime) HAEvidence {
	item := HAEvidence{ID: rt.ID, State: "UNKNOWN", Reason: "quorum_evidence_unavailable", Source: "authenticated_manager_inventory"}
	if !rt.Enabled {
		item.State, item.Reason = "DISABLED", "runtime_disabled"
		return item
	}
	if c.HAReports == nil || rt.ClusterID == "" {
		return item
	}
	for _, node := range c.config.Controller.Nodes {
		if node.ClusterID != rt.ClusterID {
			continue
		}
		report, err := c.HAReports.ReadLive(ctx, node.NodeID)
		if err != nil {
			continue
		}
		info := report.Observation.Info
		if info.ID != node.EngineID || info.Swarm.NodeID != node.NodeID || !info.Swarm.ControlAvailable || info.Swarm.Cluster == nil || info.Swarm.Cluster.ID != rt.ClusterID {
			continue
		}
		item.State, item.Reason, item.ObservedAt, item.NodeID = "DEGRADED", "quorum_unavailable", &report.Observation.At, node.NodeID
		if execution.Quorum(info, report.Observation.Nodes) {
			item.State, item.Reason = "READY", "quorum_confirmed"
			break
		}
	}
	return item
}
