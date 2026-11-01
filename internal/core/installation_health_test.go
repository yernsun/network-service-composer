package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/hoststats"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func healthyInstallation(t *testing.T) (*Core, bootstrap.Installation, model.Gateway) {
	t.Helper()
	c, release := releaseFixture(t, 1)
	now := c.now()
	c.now = func() time.Time { return now }
	gateway, err := c.store.GetGateway(t.Context(), release.Targets[0].GatewayID)
	if err != nil {
		t.Fatal(err)
	}
	owner := installationOwnerFixture(t, c, "system-runtime", gateway.PoolID)
	owner.Nodes = map[string]bootstrap.Identity{"one": {NodeID: "node", EngineID: "engine", InstanceID: "controller", GatewayID: gateway.ID, SandboxID: "system-sandbox"}}
	document, _ := c.store.GetControlDocument(t.Context(), bootstrap.InstallationDocumentID)
	if err = c.saveControl(t.Context(), document.ID, document.Kind, document.ParentID, owner, document.Revision); err != nil {
		t.Fatal(err)
	}
	c.config.Controller.InstanceID = "controller"
	c.config.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: "controller", NodeID: "node", EngineID: "engine", ClusterID: "cluster"}}
	runtime := model.Runtime{ID: owner.RuntimeID, Type: model.RuntimeSwarm, PoolID: owner.PoolID, ClusterID: "cluster", Endpoint: "node://node", Enabled: true, ManagementEnabled: true}
	c.store = haRuntimeStore{Store: c.store, runtime: runtime}
	reports := &nodecontrol.Reports{Store: c.store, Keys: c.keyring, Bindings: map[string]nodecontrol.Binding{"controller": {InstanceID: "controller", NodeID: "node", EngineID: "engine", ClusterID: "cluster"}}, Now: func(context.Context) (time.Time, error) { return c.now(), nil }, CheckOwner: func(context.Context, string, int64) error { return nil }}
	c.HAReports = reports
	c.HAController = func(context.Context) HAEvidence {
		return HAEvidence{ID: "controller", InstanceID: "controller", NodeID: "node", State: "READY", Term: 11}
	}
	session, err := reports.Begin(t.Context(), "controller", "node", "engine", "boot", 11)
	if err != nil {
		t.Fatal(err)
	}
	if err = reports.Accept(t.Context(), "controller", nodecontrol.Report{NodeID: "node", EngineID: "engine", Generation: session.Generation, Sequence: 1, Observation: nodecontrol.Observation{At: now, Complete: true, Info: system.Info{ID: "engine", Swarm: swarm.Info{NodeID: "node", ControlAvailable: true, Managers: 1, Cluster: &swarm.ClusterInfo{ID: "cluster"}}}, Nodes: []swarm.Node{{ID: "node", Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager, Availability: swarm.NodeAvailabilityActive}, Status: swarm.NodeStatus{State: swarm.NodeStateReady}, ManagerStatus: &swarm.ManagerStatus{Leader: true, Reachability: swarm.ReachabilityReachable}}}, Health: &hoststats.Health{InstanceID: "controller", Controller: "healthy", Role: "leader", Execution: "enabled", Term: 11}}}); err != nil {
		t.Fatal(err)
	}
	gateway.Status = model.GatewayReady
	gateway.DesiredRevision = release.ConfigRevision
	gateway.AppliedRevision = gateway.DesiredRevision
	gateway.ModelDigest = "verified-model"
	gateway.RenderedDigest = "verified-render"
	gateway.LastSeenAt = &now
	if err = c.store.UpdateGateway(t.Context(), &gateway, gateway.Revision); err != nil {
		t.Fatal(err)
	}
	if err = c.store.HeartbeatGateway(t.Context(), gateway.ID, gateway.DesiredRevision, 0, now); err != nil {
		t.Fatal(err)
	}
	if err = c.store.AckGateway(t.Context(), gateway.ID, gateway.AppliedRevision, gateway.ModelDigest, gateway.RendererVersion, gateway.RenderedDigest, ""); err != nil {
		t.Fatal(err)
	}
	if err = c.RecordGatewayEvidence(t.Context(), gateway.ID, GatewayEvidence{LoadingConfirmed: true, ConfirmedRevision: gateway.AppliedRevision, StartupState: "CONFIRMED", ModelDigest: gateway.ModelDigest, RenderedDigest: gateway.RenderedDigest, DiskDigest: gateway.RenderedDigest, ObservedAt: now, RefreshIntervalMillis: 1000}); err != nil {
		t.Fatal(err)
	}
	if err = c.saveControl(t.Context(), "system-sandbox:sandbox-status", "sandbox-status", owner.PoolID, map[string]any{}, 0); err != nil {
		t.Fatal(err)
	}
	return c, owner, gateway
}
func TestInstallationHealthNeedsFreshPhysicalLoadedAndSandboxEvidence(t *testing.T) {
	for _, scenario := range []string{"healthy", "controller", "quorum", "heartbeat", "zero-revision", "digest", "sandbox", "engine", "no-reports"} {
		t.Run(scenario, func(t *testing.T) {
			c, owner, gateway := healthyInstallation(t)
			switch scenario {
			case "controller":
				c.HAController = nil
			case "quorum":
				c.config.Controller.Nodes[0].ClusterID = "foreign"
			case "heartbeat":
				old := c.now().Add(-time.Minute)
				gateway.LastSeenAt = &old
				c.store.HeartbeatGateway(t.Context(), gateway.ID, gateway.DesiredRevision, 0, old)
			case "zero-revision":
				gateway.DesiredRevision = 0
				c.store.HeartbeatGateway(t.Context(), gateway.ID, 0, 0, c.now())
			case "digest":
				gateway.ModelDigest = "external"
				c.store.AckGateway(t.Context(), gateway.ID, gateway.AppliedRevision, "external", gateway.RendererVersion, gateway.RenderedDigest, "")
			case "sandbox":
				document, _ := c.store.GetControlDocument(t.Context(), "system-sandbox:sandbox-status")
				document.UpdatedAt = c.now().Add(-time.Minute)
				c.store = healthDocumentStore{Store: c.store, document: document}
			case "engine":
				owner.Database.EngineID = "foreign"
				document, _ := c.store.GetControlDocument(t.Context(), bootstrap.InstallationDocumentID)
				c.saveControl(t.Context(), document.ID, document.Kind, document.ParentID, owner, document.Revision)
			case "no-reports":
				c.HAReports = nil
			}
			binding := c.InstallationBinding(t.Context())
			health := c.InstallationHealth(t.Context())
			if scenario == "healthy" {
				if binding != nil || health != nil {
					t.Fatal(binding, health)
				}
			} else if scenario == "engine" {
				if binding == nil {
					t.Fatal("foreign database Engine identity was accepted")
				}
			} else if health == nil {
				t.Fatal("incomplete installation health was declared verified")
			}
		})
	}
}

type healthDocumentStore struct {
	storage.Store
	document storage.ControlDocument
	err      error
}

func (s healthDocumentStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if key == s.document.ID {
		return s.document, s.err
	}
	return s.Store.GetControlDocument(ctx, key)
}

type installationEvidenceStore struct {
	storage.Store
	gateway                   *model.Gateway
	gatewayErr, errorRuntimes error
}

func (s installationEvidenceStore) GetGateway(ctx context.Context, id string) (model.Gateway, error) {
	if s.gatewayErr != nil {
		return model.Gateway{}, s.gatewayErr
	}
	if s.gateway != nil && s.gateway.ID == id {
		return *s.gateway, nil
	}
	return s.Store.GetGateway(ctx, id)
}
func (s installationEvidenceStore) ListRuntimes(ctx context.Context, page storage.Page) ([]model.Runtime, error) {
	if s.errorRuntimes != nil {
		return nil, s.errorRuntimes
	}
	return s.Store.ListRuntimes(ctx, page)
}
func TestInstallationHealthCannotPromotePartialOrForeignEvidence(t *testing.T) {
	for _, scenario := range []string{"unmanaged", "ownership-unavailable", "runtime-unavailable", "gateway-unavailable", "gateway-pool", "gateway-status", "gateway-pending", "loading", "startup", "model-empty", "model-other", "render-empty", "render-other", "revision", "evidence-corrupt", "sandbox-kind", "sandbox-parent", "sandbox-unavailable", "sandbox-identity", "report-stale", "unrelated-worker"} {
		t.Run(scenario, func(t *testing.T) {
			c, owner, gateway := healthyInstallation(t)
			switch scenario {
			case "unmanaged":
				c.store = installationReadStore{Store: c.store, err: storage.ErrNotFound}
			case "ownership-unavailable":
				c.store = installationReadStore{Store: c.store, err: errors.New("database unavailable")}
			case "runtime-unavailable":
				c.store = installationEvidenceStore{Store: c.store, errorRuntimes: errors.New("runtime evidence unavailable")}
			case "gateway-unavailable":
				c.store = installationEvidenceStore{Store: c.store, gatewayErr: storage.ErrNotFound}
			case "gateway-pool", "gateway-status", "gateway-pending":
				if scenario == "gateway-pool" {
					gateway.PoolID = "foreign"
				}
				if scenario == "gateway-status" {
					gateway.Status = model.GatewayOffline
				}
				if scenario == "gateway-pending" {
					gateway.AppliedRevision = 0
				}
				c.store = installationEvidenceStore{Store: c.store, gateway: &gateway}
			case "loading", "startup", "model-empty", "model-other", "render-empty", "render-other", "revision", "evidence-corrupt":
				document, err := c.store.GetControlDocument(t.Context(), gateway.ID+":evidence")
				if err != nil {
					t.Fatal(err)
				}
				var evidence GatewayEvidence
				if err = json.Unmarshal(document.Payload, &evidence); err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "loading":
					evidence.LoadingConfirmed = false
				case "startup":
					evidence.StartupState = "RISK"
				case "model-empty":
					evidence.ModelDigest = ""
				case "model-other":
					evidence.ModelDigest = "foreign"
				case "render-empty":
					evidence.RenderedDigest = ""
					evidence.DiskDigest = ""
				case "render-other":
					evidence.RenderedDigest = "foreign"
					evidence.DiskDigest = "foreign"
				case "revision":
					evidence.ConfirmedRevision++
				}
				document.Payload, _ = json.Marshal(evidence)
				if scenario == "evidence-corrupt" {
					document.Payload = []byte("{")
				}
				c.store = healthDocumentStore{Store: c.store, document: document}
			case "sandbox-kind", "sandbox-parent", "sandbox-unavailable":
				document, err := c.store.GetControlDocument(t.Context(), "system-sandbox:sandbox-status")
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "sandbox-kind" {
					document.Kind = "foreign"
				}
				if scenario == "sandbox-parent" {
					document.ParentID = "foreign"
				}
				var unavailable error
				if scenario == "sandbox-unavailable" {
					unavailable = storage.ErrNotFound
				}
				c.store = healthDocumentStore{Store: c.store, document: document, err: unavailable}
			case "sandbox-identity", "unrelated-worker":
				if scenario == "sandbox-identity" {
					node := owner.Nodes["one"]
					node.SandboxID = ""
					owner.Nodes["one"] = node
				} else {
					owner.Nodes["unrelated-worker"] = bootstrap.Identity{NodeID: "worker", GatewayID: "not-a-controller-gateway"}
				}
				document, _ := c.store.GetControlDocument(t.Context(), bootstrap.InstallationDocumentID)
				if err := c.saveControl(t.Context(), document.ID, document.Kind, document.ParentID, owner, document.Revision); err != nil {
					t.Fatal(err)
				}
			case "report-stale":
				now := c.now().Add(2 * time.Minute)
				c.now = func() time.Time { return now }
			}
			err := c.InstallationHealth(t.Context())
			if scenario == "unrelated-worker" {
				if err != nil {
					t.Fatal("worker-only identity inflated the Gateway requirement", err)
				}
			} else if err == nil {
				t.Fatal("partial installation evidence reported healthy", scenario)
			}
			if scenario == "unmanaged" || scenario == "ownership-unavailable" || scenario == "report-stale" {
				if err := c.InstallationBinding(t.Context()); err == nil {
					t.Fatal("missing physical identity reported verified")
				}
			}
		})
	}
}
