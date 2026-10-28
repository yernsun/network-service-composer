package controller

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/cluster"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/hoststats"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func (c *Controller) configureNodes(store *storage.SQLStore, keys *security.Keyring, locker lease.Locker) error {
	if len(c.config.Controller.Nodes) == 0 {
		return nil
	}
	if c.internalTLS == nil || store.Dialect() == storage.SQLite {
		return errors.New("node cluster requires internal mutual TLS and PostgreSQL/MySQL")
	}
	if c.config.Controller.HostProc != "" || c.config.Controller.HostRoot != "" {
		if !filepath.IsAbs(c.config.Controller.HostProc) || !filepath.IsAbs(c.config.Controller.HostRoot) {
			return errors.New("host_proc and host_root must both be explicit absolute host mount paths")
		}
	}
	bindings := map[string]nodecontrol.Binding{}
	nodes, engines := map[string]bool{}, map[string]bool{}
	for _, item := range c.config.Controller.Nodes {
		if _, err := controlplane.IdentityURI(item.InstanceID); err != nil {
			return err
		}
		if err := controlplane.ValidateInternalURL(item.URL); err != nil {
			return err
		}
		if item.NodeID == "" || item.EngineID == "" || item.ClusterID == "" || strings.ContainsAny(item.NodeID, "/:?#@") || nodes[item.NodeID] || engines[item.EngineID] || bindings[item.InstanceID].InstanceID != "" {
			return errors.New("controller node bindings must have unique instance, node and engine identities")
		}
		nodes[item.NodeID], engines[item.EngineID] = true, true
		bindings[item.InstanceID] = nodecontrol.Binding{InstanceID: item.InstanceID, NodeID: item.NodeID, EngineID: item.EngineID, ClusterID: item.ClusterID, URL: item.URL}
	}
	self, ok := bindings[c.config.Controller.InstanceID]
	if !ok || self.URL != c.config.Controller.Internal.URL {
		return errors.New("Controller is missing its exact provisioned node binding")
	}
	state := controlplane.SQLState{Store: store}
	reports := &nodecontrol.Reports{Store: store, EventsEnabled: c.config.Controller.RuntimeEvents.Enabled, Keys: keys, Bindings: bindings, Now: state.DatabaseTime, CheckOwner: func(ctx context.Context, key string, owner int64) error {
		return lease.CheckOwner(ctx, store.DB(), store.Dialect(), key, owner)
	}}
	c.nodeReports = reports
	c.core.HAReports = reports
	c.localCollector = &nodecontrol.Collector{Binding: self, Locker: locker, TLS: c.internalTLS, Open: func() (engine.MonitoringClient, error) {
		return engine.NewMonitoringClient(model.Runtime{Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock"}, engine.TLSFiles{})
	}}
	c.localCollector.EventsEnabled = c.config.Controller.RuntimeEvents.Enabled
	c.localCollector.EventReport = func(result string) {
		if c.metrics != nil {
			c.metrics.NodeEventReports.WithLabelValues(self.NodeID, result).Inc()
		}
	}
	c.localCollector.Host = &hoststats.Collector{Proc: c.config.Controller.HostProc, Root: c.config.Controller.HostRoot}
	c.localCollector.Health = func(ctx context.Context) hoststats.Health {
		health := hoststats.Health{Controller: "degraded", Role: "unknown", Gateway: "disabled"}
		health.Execution = "disabled"
		if c.config.Management.Enabled && c.ready.Load() {
			health.Execution = "enabled"
		}
		leader, err := c.election.Store.Current(ctx)
		if err == nil && c.ready.Load() && c.haController(ctx).State == "READY" {
			health.Controller, health.Role, health.Term = "healthy", "follower", leader.Term
			if leader.ID == self.InstanceID {
				health.Role = "leader"
			}
		}
		if c.config.Controller.LocalGateway {
			health.Gateway = "unavailable"
			g, err := store.GetGateway(ctx, c.config.Agent.GatewayID)
			if err == nil && g.Enabled && g.PoolID == c.config.Agent.PoolID && g.LastSeenAt != nil && time.Since(*g.LastSeenAt) >= -5*time.Second && time.Since(*g.LastSeenAt) <= c.config.Controller.Gateway.OfflineTimeout.Std() {
				health.Gateway = strings.ToLower(string(g.Status))
			}
		}
		return health
	}
	c.core.Cluster = cluster.NewWithFactory(store, c.config, func(rt model.Runtime, _ engine.TLSFiles) (engine.MonitoringClient, error) {
		ctx, cancel := context.WithTimeout(context.Background(), c.config.Server.ReadTimeout.Std())
		defer cancel()
		return reports.MonitoringClient(ctx, rt)
	})
	return nil
}

// bindMissingRuntimeClusters repairs pre-identity Swarm runtimes only after a
// fresh, authenticated Manager observation confirms the provisioned cluster.
// Existing nonempty identities are never silently rewritten.
func (c *Controller) bindMissingRuntimeClusters(ctx context.Context) error {
	if c.nodeReports == nil {
		return nil
	}
	for after := ""; ; {
		runtimes, err := c.store.ListRuntimes(ctx, storage.Page{After: after, Limit: 200})
		if err != nil {
			return err
		}
		for _, rt := range runtimes {
			if rt.Type != model.RuntimeSwarm || rt.ClusterID != "" || !strings.HasPrefix(rt.Endpoint, "node://") {
				continue
			}
			for _, binding := range c.config.Controller.Nodes {
				if rt.Endpoint != "node://"+binding.NodeID || binding.ClusterID == "" {
					continue
				}
				candidate := rt
				candidate.ClusterID = binding.ClusterID
				client, readErr := c.nodeReports.Client(ctx, candidate)
				if readErr != nil || client == nil {
					continue
				}
				info, infoErr := client.Info(ctx)
				_ = client.Close()
				if infoErr != nil || !info.Swarm.ControlAvailable || info.Swarm.Cluster == nil || info.Swarm.Cluster.ID != binding.ClusterID {
					continue
				}
				candidate.UpdatedAt = time.Now().UTC()
				if err := c.store.UpdateRuntime(ctx, &candidate, rt.Revision); err != nil && !errors.Is(err, storage.ErrPrecondition) {
					return err
				}
				break
			}
		}
		if len(runtimes) < 200 {
			return nil
		}
		after = runtimes[len(runtimes)-1].ID
	}
}
