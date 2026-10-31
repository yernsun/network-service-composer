package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"gopkg.in/yaml.v3"
)

func editAdoptConfig(t *testing.T, i *Installer, change func(*config.Config)) {
	t.Helper()
	file := i.Config.Adoption.ControllerConfigFile
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	change(&cfg)
	data, err = yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func adoptStore(t *testing.T, i *Installer, apply func(storage.Store) error) {
	t.Helper()
	if err := i.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error { return apply(store) }); err != nil {
		t.Fatal(err)
	}
}

func TestAdoptionExistingMarkerFencesInstallationRuntimeAndStorage(t *testing.T) {
	for _, scenario := range []string{"corrupt", "kind", "requested-installation", "anchor", "cluster", "runtime", "data-node", "engine", "volume", "component"} {
		t.Run(scenario, func(t *testing.T) {
			i, cluster := adoptionFixture(t, 1)
			observation, err := i.Observe(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			owner := observation.(Observation).Adoption.Installation
			kind := bootstrap.InstallationDocumentKind
			switch scenario {
			case "kind":
				kind = "foreign-document"
			case "requested-installation":
				i.Config.Adoption.InstallationID = "another-installation"
			case "anchor":
				owner.MaintenanceAnchor = "another-private-directory"
			case "cluster":
				owner.ClusterID = "other-cluster"
			case "runtime":
				owner.RuntimeID = "other-runtime"
			case "data-node":
				owner.Database.NodeID = "other-node"
			case "engine":
				owner.Database.EngineID = "other-engine"
			case "volume":
				owner.Database.Volume = "other-volume"
			case "component":
				r := owner.Components["nginx"]
				r.ID = "other-service"
				owner.Components["nginx"] = r
			}
			payload, err := json.Marshal(owner)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "corrupt" {
				payload = []byte(`{"id":true}`)
			}
			adoptStore(t, i, func(store storage.Store) error {
				return store.WriteControlDocuments(storage.WithSystemBootstrap(t.Context()), []storage.ControlWrite{{Document: storage.ControlDocument{ID: bootstrap.InstallationDocumentID, Kind: kind, ParentID: owner.ID, Payload: payload}}})
			})
			i.Record.ConfigDigest = bootstrap.Digest(i.Config)
			before := bootstrap.Digest(cluster.services)
			if _, err = i.Plan(t.Context(), bootstrap.Adopt); err == nil {
				t.Fatal("foreign installation authority was accepted")
			}
			if bootstrap.Digest(cluster.services) != before || len(i.Record.Resources) != 0 {
				t.Fatal("rejected plan changed ownership or service objects")
			}
		})
	}
}

func TestAdoptionRequiresExistingPlacementStorageAndAuthenticatedRegistration(t *testing.T) {
	cases := []string{"storage-driver", "registration-cluster", "registration-runtime", "entry-cluster", "controller-binding", "controller-engine", "controller-cluster", "membership-missing", "membership-role", "data-node-down", "data-node-drained", "wrong-data-node", "database-global", "database-unpinned", "database-constraint", "missing-volume", "volume-driver", "runtime-disabled", "runtime-type", "runtime-cluster", "pool-disabled", "database-unavailable"}
	for _, scenario := range cases {
		t.Run(scenario, func(t *testing.T) {
			i, cluster := adoptionFixture(t, 1)
			switch scenario {
			case "storage-driver":
				editAdoptConfig(t, i, func(c *config.Config) { c.Storage.Driver = "sqlite"; c.Storage.HA = false })
			case "registration-cluster":
				editAdoptConfig(t, i, func(c *config.Config) { c.Controller.SystemProject.ClusterID = "foreign" })
			case "registration-runtime":
				editAdoptConfig(t, i, func(c *config.Config) { c.Controller.SystemProject.RuntimeID = "" })
			case "entry-cluster":
				i.Hosts["one"].(*fakeHost).clusterID = "foreign"
			case "controller-binding":
				editAdoptConfig(t, i, func(c *config.Config) { c.Controller.Nodes = nil })
			case "controller-engine":
				editAdoptConfig(t, i, func(c *config.Config) { c.Controller.Nodes[0].EngineID = "foreign" })
			case "controller-cluster":
				editAdoptConfig(t, i, func(c *config.Config) { c.Controller.Nodes[0].ClusterID = "foreign" })
			case "membership-missing":
				delete(cluster.nodes, i.Record.Nodes["one"].NodeID)
			case "membership-role":
				n := cluster.nodes[i.Record.Nodes["one"].NodeID]
				n.Spec.Role = swarm.NodeRoleWorker
				cluster.nodes[n.ID] = n
			case "data-node-down":
				n := cluster.nodes[i.Record.Nodes["one"].NodeID]
				n.Status.State = swarm.NodeStateDown
				cluster.nodes[n.ID] = n
			case "data-node-drained":
				n := cluster.nodes[i.Record.Nodes["one"].NodeID]
				n.Spec.Availability = swarm.NodeAvailabilityDrain
				cluster.nodes[n.ID] = n
			case "wrong-data-node":
				i.Config.Adoption.DataNodeID = "foreign"
			case "database-global":
				v := cluster.services[i.Config.Adoption.ServiceIDs["postgresql"]]
				v.Spec.Mode = swarm.ServiceMode{Global: &swarm.GlobalService{}}
				cluster.services[v.ID] = v
			case "database-unpinned":
				v := cluster.services[i.Config.Adoption.ServiceIDs["postgresql"]]
				v.Spec.TaskTemplate.Placement = nil
				cluster.services[v.ID] = v
			case "database-constraint":
				v := cluster.services[i.Config.Adoption.ServiceIDs["postgresql"]]
				v.Spec.TaskTemplate.Placement.Constraints = []string{"node.role == manager"}
				cluster.services[v.ID] = v
			case "missing-volume":
				delete(i.Hosts["one"].(*fakeHost).volumes, i.Config.Adoption.Volume)
			case "volume-driver":
				i.Hosts["one"].(*fakeHost).volumes[i.Config.Adoption.Volume]["Driver"] = "external-driver"
			case "runtime-disabled", "runtime-type", "runtime-cluster":
				adoptStore(t, i, func(store storage.Store) error {
					doc, err := store.GetControlDocument(t.Context(), "fixture-runtime:"+i.Record.RuntimeID)
					if err != nil {
						return err
					}
					var rt model.Runtime
					if err = json.Unmarshal(doc.Payload, &rt); err != nil {
						return err
					}
					switch scenario {
					case "runtime-disabled":
						rt.Enabled = false
					case "runtime-type":
						rt.Type = model.RuntimeDocker
					case "runtime-cluster":
						rt.ClusterID = "foreign"
					}
					doc.Payload, err = json.Marshal(rt)
					if err != nil {
						return err
					}
					return store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: doc, Expected: doc.Revision}})
				})
			case "pool-disabled":
				adoptStore(t, i, func(store storage.Store) error {
					pool, err := store.GetPool(t.Context(), i.Record.PoolID)
					if err != nil {
						return err
					}
					pool.Enabled = false
					return store.UpdatePool(t.Context(), &pool, pool.Revision)
				})
			case "database-unavailable":
				i.Database = sqliteConnector{fail: fmt.Errorf("database unavailable")}
			}
			i.Record.ConfigDigest = bootstrap.Digest(i.Config)
			if _, err := i.Plan(t.Context(), bootstrap.Adopt); err == nil {
				t.Fatal("missing or conflicting ownership evidence did not block adoption")
			}
		})
	}
}

type registrationFailureStore struct{ storage.Store }

func (s registrationFailureStore) ReconcileSystemApplications(context.Context, string, []storage.SystemApplicationRegistration, []storage.ControlWrite) error {
	return fmt.Errorf("registration acknowledgement lost")
}

type registrationFailureConnector struct{ DatabaseConnector }

func (c registrationFailureConnector) Open(ctx context.Context, cfg config.Config, host Engine, container string, readOnly bool) (storage.Store, error) {
	store, err := c.DatabaseConnector.Open(ctx, cfg, host, container, readOnly)
	if err != nil {
		return nil, err
	}
	if readOnly {
		return store, nil
	}
	return registrationFailureStore{store}, nil
}

func TestAdoptionInterruptedRegistrationResumesSameObjectsWithoutResettingIdentity(t *testing.T) {
	i, cluster := adoptionFixture(t, 1)
	original := i.Database
	plan, err := i.Plan(t.Context(), bootstrap.Adopt)
	if err != nil {
		t.Fatal(err)
	}
	i.Database = registrationFailureConnector{original}
	executor := bootstrap.Executor{Journal: i.Journal, Driver: i}
	before := bootstrap.Digest(cluster.services)
	operation, err := executor.Execute(t.Context(), i.Record, plan, plan.Hash, "adopt-interrupted")
	if err == nil || operation.State != bootstrap.Unknown {
		t.Fatal(operation.State, err)
	}
	adoptStore(t, i, func(store storage.Store) error {
		doc, err := store.GetControlDocument(t.Context(), bootstrap.InstallationDocumentID)
		if err != nil {
			return err
		}
		var marker bootstrap.Installation
		if err = json.Unmarshal(doc.Payload, &marker); err != nil {
			return err
		}
		if marker.State != bootstrap.Pending {
			return fmt.Errorf("partial registration was reported as complete")
		}
		return nil
	})
	i.Database = original
	operation, err = executor.Resume(t.Context(), i.Record, operation.ID)
	if err != nil || operation.State != bootstrap.Succeeded {
		t.Fatal(operation.State, err)
	}
	if bootstrap.Digest(cluster.services) != before {
		t.Fatal("recovery changed existing service objects")
	}
}

func TestAdoptionApprovalBindsCurrentRuntimeAuthority(t *testing.T) {
	i, _ := adoptionFixture(t, 1)
	plan, err := i.Plan(t.Context(), bootstrap.Adopt)
	if err != nil {
		t.Fatal(err)
	}
	i.Hosts["one"].(*fakeHost).files["controller-secrets/session-key"] = []byte("isolated-external-session-key-rotation-requires-replan")
	executor := bootstrap.Executor{Journal: i.Journal, Driver: i}
	if _, err = executor.Execute(t.Context(), i.Record, plan, plan.Hash, "stale-authority"); err == nil {
		t.Fatal("changed runtime authority used an old approval")
	}
	if len(i.Record.Operations) != 0 || len(i.Record.Resources) != 0 {
		t.Fatal("stale approval changed the installation")
	}
	if err = i.checkAdoption(t.Context(), bootstrap.Step{Key: "adopt", Before: plan.Steps[0].Before}); err == nil {
		t.Fatal("resumed step ignored changed authority")
	}
	i.Config.Adoption = nil
	if err = i.Check(t.Context(), bootstrap.Step{Key: "adopt"}); err == nil {
		t.Fatal("adoption without explicit authority was accepted")
	}
	if _, err = i.Apply(t.Context(), bootstrap.Step{Key: "adopt"}); err == nil {
		t.Fatal("unapproved adoption was dispatched")
	}
}

func TestAdoptionBlocksForeignGatewayRegistrationBeforeOwnershipWrite(t *testing.T) {
	for _, scenario := range []string{"foreign-marker", "corrupt-marker", "orphan-system-project"} {
		t.Run(scenario, func(t *testing.T) {
			i, _ := adoptionFixture(t, 1)
			adoptStore(t, i, func(store storage.Store) error {
				document := storage.ControlDocument{ID: "system-project:gateway", Kind: "system-project", Payload: []byte(`{"project_id":"foreign","cluster_id":"foreign","runtime_id":"foreign"}`)}
				if scenario == "corrupt-marker" {
					document.Payload = []byte(`{"project_id":true}`)
				}
				if scenario == "orphan-system-project" {
					document = storage.ControlDocument{ID: "orphan-system-project", Kind: "project", Payload: []byte(`{"id":"orphan-system-project","name":"Gateway","slug":"gateway","system":true}`)}
				}
				return store.WriteControlDocuments(storage.WithSystemBootstrap(t.Context()), []storage.ControlWrite{{Document: document}})
			})
			if _, err := i.Plan(t.Context(), bootstrap.Adopt); err == nil {
				t.Fatal("foreign or orphaned Gateway registration was accepted")
			}
			adoptStore(t, i, func(store storage.Store) error {
				if _, err := store.GetControlDocument(t.Context(), bootstrap.InstallationDocumentID); !errors.Is(err, storage.ErrNotFound) {
					return fmt.Errorf("rejected plan wrote installation ownership")
				}
				return nil
			})
		})
	}
}

func TestOfflineAdoptionRequiresAnUnambiguousPriorNodeIdentity(t *testing.T) {
	for _, scenario := range []string{"missing", "bad-url", "different-address", "duplicate", "missing-engine", "missing-node", "different-cluster", "configuration-unreadable"} {
		t.Run(scenario, func(t *testing.T) {
			i, _ := adoptionFixture(t, 3)
			editAdoptConfig(t, i, func(cfg *config.Config) {
				index := 2
				switch scenario {
				case "missing":
					cfg.Controller.Nodes = cfg.Controller.Nodes[:2]
				case "bad-url":
					cfg.Controller.Nodes[index].URL = "%invalid"
				case "different-address":
					cfg.Controller.Nodes[index].URL = "https://192.0.2.99:9444"
				case "duplicate":
					cfg.Controller.Nodes = append(cfg.Controller.Nodes, cfg.Controller.Nodes[index])
				case "missing-engine":
					cfg.Controller.Nodes[index].EngineID = ""
				case "missing-node":
					cfg.Controller.Nodes[index].NodeID = ""
				case "different-cluster":
					cfg.Controller.Nodes[index].ClusterID = "foreign"
				}
			})
			if scenario == "configuration-unreadable" {
				if err := os.Remove(i.Config.Adoption.ControllerConfigFile); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := i.offlineIdentity(t.Context(), i.Config.Nodes[2]); err == nil {
				t.Fatal("missing or ambiguous offline authority was accepted")
			}
		})
	}
}

func TestVotingTopologyDoesNotInferReadinessFromRoleAlone(t *testing.T) {
	nodes := []swarm.Node{
		{ID: "worker", Spec: swarm.NodeSpec{Role: swarm.NodeRoleWorker}},
		{ID: "unknown-manager", Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager}},
		{ID: "leader", Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager}, ManagerStatus: &swarm.ManagerStatus{Leader: true}},
		{ID: "unreachable-manager", Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager}, ManagerStatus: &swarm.ManagerStatus{Reachability: swarm.ReachabilityUnreachable}},
	}
	result := managerTopology(nodes)
	if result.Managers != 3 || result.Reachable != 1 || result.Majority != 2 || result.TolerableFailures != 1 || result.QuorumAvailable {
		t.Fatal("manager role was used as live quorum evidence", result)
	}
	i, cluster := installerFixture(t, 1)
	infrastructure(t, i)
	observed, err := i.Observe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	observation := observed.(Observation)
	member := observation.Membership[i.Record.Nodes["one"].NodeID]
	for _, scenario := range []string{"missing", "down", "role"} {
		copy := observation
		copy.Membership = map[string]swarm.Node{member.ID: member}
		switch scenario {
		case "missing":
			delete(copy.Membership, member.ID)
		case "down":
			v := member
			v.Status.State = swarm.NodeStateDown
			copy.Membership[v.ID] = v
		case "role":
			v := member
			v.Spec.Role = swarm.NodeRoleWorker
			copy.Membership[v.ID] = v
		}
		if err = requirePlacement(copy, i.Config); err == nil {
			t.Fatal("unsafe placement accepted", scenario)
		}
	}
	node := cluster.nodes[member.ID]
	node.Status.State = swarm.NodeStateDown
	cluster.nodes[node.ID] = node
	step := bootstrap.Step{Key: "swarm:one"}
	receipt, err := i.Apply(t.Context(), step)
	if err == nil || receipt.ID != member.ID {
		t.Fatal("unready acknowledged node lost its receipt", err)
	}
}

func TestCompletedAdoptionNeverMasksMissingOrPartialDatabaseAuthority(t *testing.T) {
	i, _ := adoptionFixture(t, 1)
	if err := i.Verify(t.Context(), io.Discard); err == nil {
		t.Fatal("missing registration was reported as verified")
	}
	observation, err := i.Observe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	marker := observation.(Observation).Adoption.Installation
	marker.State = bootstrap.Pending
	adoptStore(t, i, func(store storage.Store) error { return writeInstallation(t.Context(), store, marker, 0) })
	if err = i.checkAdoption(t.Context(), bootstrap.Step{Key: "adopt", State: bootstrap.Succeeded}); err == nil {
		t.Fatal("partial registration was accepted as completed")
	}
	adoptStore(t, i, func(store storage.Store) error {
		doc, err := store.GetControlDocument(t.Context(), bootstrap.InstallationDocumentID)
		if err != nil {
			return err
		}
		marker.State = bootstrap.Succeeded
		marker.ConfigDigest = "different-configuration"
		return writeInstallation(t.Context(), store, marker, doc.Revision)
	})
	if err = i.checkAdoption(t.Context(), bootstrap.Step{Key: "adopt", State: bootstrap.Succeeded}); err == nil {
		t.Fatal("changed installation revision was accepted")
	}
	i.Config.Adoption = nil
	if _, _, err := i.adoptionView(t.Context(), Observation{}); err == nil {
		t.Fatal("missing adoption input was accepted")
	}
}

func TestAdoptionCanBindAnExistingWorkerDataNodeWithoutInventingAController(t *testing.T) {
	i, cluster := adoptionFixture(t, 3)
	i.Config.DataNode = "three"
	i.Config.Nodes[2].Manager = false
	worker := i.Hosts["three"].(*fakeHost)
	worker.worker = true
	node := cluster.nodes[i.Record.Nodes["three"].NodeID]
	node.Spec.Role = swarm.NodeRoleWorker
	node.ManagerStatus = nil
	cluster.nodes[node.ID] = node
	editAdoptConfig(t, i, func(cfg *config.Config) { cfg.Controller.Nodes = cfg.Controller.Nodes[:2] })
	i.Config.Adoption.DataNodeID = node.ID
	worker.volumes[i.Config.Adoption.Volume] = i.Hosts["one"].(*fakeHost).volumes[i.Config.Adoption.Volume]
	service := cluster.services[i.Config.Adoption.ServiceIDs["postgresql"]]
	service.Spec.TaskTemplate.Placement.Constraints = []string{"node.id == " + node.ID}
	cluster.services[service.ID] = service
	cluster.taskNode = node.ID
	i.Record.ConfigDigest = bootstrap.Digest(i.Config)
	observation, err := i.Observe(t.Context())
	if err != nil {
		t.Fatal("original worker data placement could not be verified", err)
	}
	installation := observation.(Observation).Adoption.Installation
	identity := installation.Nodes["three"]
	if identity.NodeID != node.ID || identity.EngineID != worker.engineID || identity.InstanceID != "" || identity.SandboxID == "" || installation.Topology.Managers != 2 {
		t.Fatal("adoption invented a Controller or changed voting topology", identity)
	}
}

func TestAdoptionCannotReopenUnavailableAuthorityOrLostService(t *testing.T) {
	for _, scenario := range []string{"configuration-missing", "manager-query", "service-missing", "data-node-query"} {
		t.Run(scenario, func(t *testing.T) {
			i, cluster := adoptionFixture(t, 1)
			switch scenario {
			case "configuration-missing":
				if err := os.Remove(i.Config.Adoption.ControllerConfigFile); err != nil {
					t.Fatal(err)
				}
			case "manager-query":
				cluster.fail = "GET /nodes"
			case "service-missing":
				delete(cluster.services, i.Config.Adoption.ServiceIDs["nginx"])
			case "data-node-query":
				cluster.fail = "GET /info"
			}
			if _, err := i.Apply(t.Context(), bootstrap.Step{Key: "adopt"}); err == nil {
				t.Fatal("unavailable original authority allowed adoption")
			}
			if len(i.Record.Resources) != 0 {
				t.Fatal("failed authority check registered resources")
			}
		})
	}
}
