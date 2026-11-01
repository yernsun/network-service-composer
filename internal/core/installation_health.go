package core

import (
	"context"
	"fmt"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

// InstallationHealth requires fresh authenticated evidence for the registered
// installation only. Idle executor jobs and unrelated application health do not
// turn task RUNNING or an old Gateway heartbeat into a successful maintenance.
func (c *Core) InstallationHealth(ctx context.Context) error {
	installation, err := c.installationOwner(ctx)
	if err != nil {
		return err
	}
	if installation.ID == "" {
		return fmt.Errorf("installation ownership is unavailable")
	}
	overview, err := c.SystemHA(ctx)
	if err != nil {
		return err
	}
	controller, quorum := false, false
	for _, layer := range overview.Layers {
		if layer.Name == "controller" {
			controller = layer.State == "READY" && len(layer.Evidence) == len(c.config.Controller.Nodes)
		}
		if layer.Name == "swarm_quorum" {
			for _, item := range layer.Evidence {
				if item.ID == installation.RuntimeID && item.State == "READY" {
					quorum = true
				}
			}
		}
	}
	if !controller || !quorum {
		return fmt.Errorf("Controller or Manager evidence is not fresh")
	}
	managers := map[string]bool{}
	for _, node := range c.config.Controller.Nodes {
		managers[node.NodeID] = true
	}
	for _, node := range installation.Nodes {
		if !managers[node.NodeID] {
			continue
		}
		gateway, err := c.store.GetGateway(ctx, node.GatewayID)
		if err != nil {
			return err
		}
		if gateway.PoolID != installation.PoolID || gateway.Status != model.GatewayReady || gateway.DesiredRevision <= 0 || gateway.AppliedRevision < gateway.DesiredRevision || !haFresh(c.now(), gateway.LastSeenAt, 15*time.Second) {
			return fmt.Errorf("Gateway has no fresh positive loaded revision")
		}
		evidence, err := c.GatewayEvidence(ctx, gateway.ID)
		if err != nil {
			return err
		}
		if !evidence.LoadingConfirmed || evidence.ConfigurationState != "CONFIRMED" || evidence.StartupState != "CONFIRMED" || !haFresh(c.now(), &evidence.ObservedAt, 15*time.Second) || !haFresh(c.now(), &evidence.ReceivedAt, 15*time.Second) || evidence.ConfirmedRevision != gateway.AppliedRevision || evidence.ModelDigest == "" || evidence.ModelDigest != gateway.ModelDigest || evidence.RenderedDigest == "" || evidence.RenderedDigest != gateway.RenderedDigest {
			return fmt.Errorf("Gateway loading is unconfirmed")
		}
	}
	sandbox := ""
	for _, node := range installation.Nodes {
		if node.NodeID == installation.Database.NodeID {
			sandbox = node.SandboxID
		}
	}
	document, err := c.store.GetControlDocument(ctx, sandbox+":sandbox-status")
	if err != nil {
		return err
	}
	if sandbox == "" || document.Kind != "sandbox-status" || document.ParentID != installation.PoolID || !haFresh(c.now(), &document.UpdatedAt, 15*time.Second) {
		return fmt.Errorf("Sandbox heartbeat is unconfirmed")
	}
	return nil
}

// InstallationBinding remains available while Controller/Gateway health is
// degraded, allowing a controlled repair without relaxing physical ownership.
func (c *Core) InstallationBinding(ctx context.Context) error {
	value, err := c.installationOwner(ctx)
	if err != nil {
		return err
	}
	if value.ID == "" || c.HAReports == nil {
		return fmt.Errorf("authenticated database-node binding is unavailable; use the CLI")
	}
	report, err := c.HAReports.ReadLive(ctx, value.Database.NodeID)
	if err != nil {
		return err
	}
	if report.NodeID != value.Database.NodeID || report.EngineID != value.Database.EngineID || report.Observation.Info.ID != value.Database.EngineID || report.Observation.Info.Swarm.NodeID != value.Database.NodeID || report.Observation.Info.Swarm.Cluster == nil || report.Observation.Info.Swarm.Cluster.ID != value.ClusterID {
		return fmt.Errorf("database node or Engine identity differs")
	}
	return nil
}
