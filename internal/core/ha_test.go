package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/hoststats"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func haFind(t *testing.T, c *Core, layer string) HALayer {
	t.Helper()
	overview, err := c.SystemHA(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if overview.EndToEndVerified {
		t.Fatal("unverified dependencies became end-to-end HA")
	}
	for _, v := range overview.Layers {
		if v.Name == layer {
			return v
		}
	}
	t.Fatal("missing layer", layer)
	return HALayer{}
}

func TestHALayersPreserveUnknownDisabledAndStorageLimitations(t *testing.T) {
	c, rt, _ := controlFixture(t)
	if got := haFind(t, c, "controller"); got.State != "UNKNOWN" {
		t.Fatal(got)
	}
	c.HAController = func(context.Context) HAEvidence {
		return HAEvidence{ID: "controller", State: "READY", Reason: "leader_scheduling", Source: "live_term_and_readiness"}
	}
	if got := haFind(t, c, "controller"); got.State != "READY" {
		t.Fatal(got)
	}
	for _, layer := range []string{"ingress", "execution", "swarm_quorum"} {
		if got := haFind(t, c, layer); got.State != "UNKNOWN" {
			t.Fatal(got)
		}
	}
	c.config.Storage.Driver = "sqlite"
	if got := haFind(t, c, "storage"); got.State != "LIMITED" || got.Evidence[0].Reason != "single_process_storage" {
		t.Fatal(got)
	}
	c.config.Storage.Driver = "postgres"
	if got := haFind(t, c, "storage"); got.Evidence[0].Reason != "storage_failover_unverified" {
		t.Fatal(got)
	}
	rt.ManagementEnabled = false
	if err := c.store.UpdateRuntime(t.Context(), &rt, rt.Revision); err != nil {
		t.Fatal(err)
	}
	if got := haFind(t, c, "execution"); got.Evidence[0].State != "DISABLED" {
		t.Fatal(got)
	}
	c.store = haFailedStore{Store: c.store}
	if got := haFind(t, c, "storage"); got.State != "DEGRADED" {
		t.Fatal(got)
	}
}

type haFailedStore struct{ storage.Store }

func (haFailedStore) Ping(context.Context) error { return errors.New("private-dsn-do-not-disclose") }

func TestHAGatewayNeedsFreshHeartbeatLoadedAndStartupEvidence(t *testing.T) {
	c, release := releaseFixture(t, 1)
	now := c.now()
	c.now = func() time.Time { return now }
	g, err := c.store.GetGateway(t.Context(), release.Targets[0].GatewayID)
	if err != nil {
		t.Fatal(err)
	}
	g.Status = model.GatewayReady
	g.AppliedRevision = g.DesiredRevision
	g.LastSeenAt = &now
	if err = c.store.UpdateGateway(t.Context(), &g, g.Revision); err != nil {
		t.Fatal(err)
	}
	e := GatewayEvidence{ConfirmedRevision: g.DesiredRevision, LoadingConfirmed: true, StartupState: "CONFIRMED", RenderedDigest: "same", DiskDigest: "same", ObservedAt: now, RefreshIntervalMillis: 1000}
	if err = c.RecordGatewayEvidence(t.Context(), g.ID, e); err != nil {
		t.Fatal(err)
	}
	if got := haFind(t, c, "gateway"); got.State != "READY" {
		t.Fatal(got)
	}
	c.now = func() time.Time { return now.Add(4 * time.Second) }
	if got := haFind(t, c, "gateway"); got.State == "READY" {
		t.Fatal("stale configuration stayed healthy", got)
	}
	now = now.Add(5 * time.Second)
	c.now = func() time.Time { return now }
	e.ObservedAt = now
	e.StartupState = "RISK"
	if err = c.RecordGatewayEvidence(t.Context(), g.ID, e); err != nil {
		t.Fatal(err)
	}
	if got := haFind(t, c, "gateway"); got.State != "DEGRADED" {
		t.Fatal(got)
	}
	g.Revision++
	g.Enabled = false
	if err = c.store.UpdateGateway(t.Context(), &g, g.Revision); err != nil {
		t.Fatal(err)
	}
	if got := haFind(t, c, "gateway"); got.Evidence[0].State != "DISABLED" {
		t.Fatal(got)
	}
}

func TestHAExecutionAndQuorumFollowAuthenticatedPhysicalIdentity(t *testing.T) {
	c, rt, _ := controlFixture(t)
	ctx := t.Context()
	now := c.now()
	c.now = func() time.Time { return now }
	c.config.Management.Enabled = true
	c.config.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: "controller", NodeID: "node", EngineID: "engine", ClusterID: "cluster", URL: "https://controller.example.test"}}
	rt.ID += "-swarm"
	rt.Type = model.RuntimeSwarm
	rt.Endpoint = "node://node"
	rt.ClusterID = "cluster"
	rt.ManagementEnabled = true
	c.store = haRuntimeStore{Store: c.store, runtime: rt}
	ownerActive := true
	reports := &nodecontrol.Reports{Store: c.store, Keys: c.keyring, Bindings: map[string]nodecontrol.Binding{"controller": {InstanceID: "controller", NodeID: "node", EngineID: "engine", ClusterID: "cluster"}}, Now: func(context.Context) (time.Time, error) { return now, nil }, CheckOwner: func(context.Context, string, int64) error {
		if !ownerActive {
			return errors.New("lost")
		}
		return nil
	}}
	c.HAReports = reports
	session, err := reports.Begin(ctx, "controller", "node", "engine", "boot", 11)
	if err != nil {
		t.Fatal(err)
	}
	info := system.Info{ID: "engine", Swarm: swarm.Info{NodeID: "node", ControlAvailable: true, Managers: 3, Cluster: &swarm.ClusterInfo{ID: "cluster"}}}
	nodes := []swarm.Node{{ID: "node", Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager}, ManagerStatus: &swarm.ManagerStatus{Leader: true, Reachability: swarm.ReachabilityReachable}}, {ID: "second", Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager}, ManagerStatus: &swarm.ManagerStatus{Reachability: swarm.ReachabilityReachable}}, {ID: "third", Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager}, ManagerStatus: &swarm.ManagerStatus{Reachability: swarm.ReachabilityUnreachable}}}
	sequence := uint64(0)
	publish := func() {
		sequence++
		if err := reports.Accept(ctx, "controller", nodecontrol.Report{NodeID: "node", EngineID: "engine", Generation: session.Generation, Sequence: sequence, Observation: nodecontrol.Observation{Info: info, Nodes: nodes, At: now, Complete: true, Health: &hoststats.Health{InstanceID: "controller", Execution: "enabled"}}}); err != nil {
			t.Fatal(err)
		}
	}
	publish()
	for _, layer := range []string{"execution", "swarm_quorum"} {
		found := false
		for _, item := range haFind(t, c, layer).Evidence {
			if item.ID == rt.ID {
				found = true
				if item.State != "READY" {
					t.Fatal(item)
				}
			}
		}
		if !found {
			t.Fatal("missing Runtime evidence", layer)
		}
	}
	now = now.Add(time.Second)
	nodes[1].ManagerStatus.Reachability = swarm.ReachabilityUnreachable
	publish()
	if got := haFind(t, c, "swarm_quorum"); got.State != "DEGRADED" {
		t.Fatal(got)
	}
	if got := haFind(t, c, "execution"); got.State == "READY" {
		t.Fatal("lost quorum permitted writes", got)
	}
	// Enrollment conflicts and absent cluster evidence cannot establish quorum.
	originalStore := c.store
	originalNodes := append([]config.ControllerNodeConfig(nil), c.config.Controller.Nodes...)
	nodes[1].ManagerStatus.Reachability = swarm.ReachabilityReachable
	for _, mode := range []string{"unrelated-enrollment", "engine-replaced", "no-control", "no-cluster", "different-cluster", "no-runtime-cluster", "no-reports"} {
		c.config.Controller.Nodes = append([]config.ControllerNodeConfig(nil), originalNodes...)
		info.Swarm.ControlAvailable = true
		info.Swarm.Cluster = &swarm.ClusterInfo{ID: "cluster"}
		c.HAReports = reports
		c.store = originalStore
		switch mode {
		case "unrelated-enrollment":
			c.config.Controller.Nodes[0].ClusterID = "foreign"
		case "engine-replaced":
			c.config.Controller.Nodes[0].EngineID = "replacement"
		case "no-control":
			info.Swarm.ControlAvailable = false
		case "no-cluster":
			info.Swarm.Cluster = nil
		case "different-cluster":
			changed := rt
			changed.ClusterID = "foreign"
			c.store = haRuntimeStore{Store: originalStore, runtime: changed}
			c.config.Controller.Nodes[0].ClusterID = "foreign"
		case "no-runtime-cluster":
			changed := rt
			changed.ClusterID = ""
			c.store = haRuntimeStore{Store: originalStore, runtime: changed}
		case "no-reports":
			c.HAReports = nil
		}
		publish()
		if got := haFind(t, c, "swarm_quorum"); got.State != "UNKNOWN" {
			t.Fatal("unverified quorum reported", mode, got)
		}
	}
	c.store = originalStore
	c.HAReports = reports
	c.config.Controller.Nodes = originalNodes
	info.Swarm.Cluster = &swarm.ClusterInfo{ID: "cluster"}
	publish()
	ownerActive = false
	if got := haFind(t, c, "swarm_quorum"); got.State != "UNKNOWN" {
		t.Fatal("retained lost-owner inventory stayed healthy", got)
	}
	value, err := c.SystemHA(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value)
	for _, private := range []string{"https://", "collector_owner", "session_key", "private-dsn"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("HA leaked private authority", string(raw))
		}
	}
}

type haRuntimeStore struct {
	storage.Store
	runtime model.Runtime
}

func (s haRuntimeStore) ListRuntimes(_ context.Context, p storage.Page) ([]model.Runtime, error) {
	if p.After != "" {
		return nil, nil
	}
	return []model.Runtime{s.runtime}, nil
}

func TestHAControllerPeersRequireLiveLeaseCurrentTermAndForwardingHealth(t *testing.T) {
	c, _, _ := controlFixture(t)
	now := c.now()
	c.now = func() time.Time { return now }
	c.config.Controller.InstanceID = "local"
	c.config.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: "peer", NodeID: "peer-node", EngineID: "peer-engine"}}
	c.HAController = func(context.Context) HAEvidence {
		return HAEvidence{ID: "local", InstanceID: "local", State: "READY", Term: 12}
	}
	owner := true
	reports := &nodecontrol.Reports{Store: c.store, Keys: c.keyring, Bindings: map[string]nodecontrol.Binding{"peer": {InstanceID: "peer", NodeID: "peer-node", EngineID: "peer-engine"}}, Now: func(context.Context) (time.Time, error) { return now, nil }, CheckOwner: func(context.Context, string, int64) error {
		if !owner {
			return errors.New("collector ended")
		}
		return nil
	}}
	c.HAReports = reports
	session, err := reports.Begin(t.Context(), "peer", "peer-node", "peer-engine", "boot", 21)
	if err != nil {
		t.Fatal(err)
	}
	sequence := uint64(0)
	publish := func(health *hoststats.Health) HAEvidence {
		health.At = now
		sequence++
		if err := reports.Accept(t.Context(), "peer", nodecontrol.Report{NodeID: "peer-node", EngineID: "peer-engine", Generation: session.Generation, Sequence: sequence, Observation: nodecontrol.Observation{Info: system.Info{ID: "peer-engine", Swarm: swarm.Info{NodeID: "peer-node"}}, Complete: true, At: now, Health: health}}); err != nil {
			t.Fatal(err)
		}
		return haFind(t, c, "controller").Evidence[1]
	}
	health := &hoststats.Health{InstanceID: "peer", Controller: "healthy", Execution: "disabled", Role: "follower", Term: 12}
	if got := publish(health); got.State != "READY" || got.Reason != "follower_forwarding" {
		t.Fatal(got)
	}
	for _, change := range []func(*hoststats.Health){
		func(h *hoststats.Health) { h.Term = 11 },
		func(h *hoststats.Health) { h.Controller = "unavailable" },
		func(h *hoststats.Health) { h.Role = "unknown" },
		func(h *hoststats.Health) { h.Execution = "" },
	} {
		bad := *health
		change(&bad)
		if got := publish(&bad); got.State != "UNKNOWN" {
			t.Fatal("unverified peer became ready", got)
		}
	}
	health.Execution = "enabled"
	health.Role = "leader"
	if got := publish(health); got.State != "READY" || got.Reason != "leader_scheduling" {
		t.Fatal(got)
	}
	owner = false
	if got := haFind(t, c, "controller"); got.Evidence[1].State != "UNKNOWN" {
		t.Fatal("retained report outlived collector authority", got)
	}
}
