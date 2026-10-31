package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
	"gopkg.in/yaml.v3"
)

func adoptionFixture(t *testing.T, count int) (*Installer, *fakeCluster) {
	t.Helper()
	original, cluster := installerFixture(t, count)
	infrastructure(t, original)
	if err := original.initializeDatabase(t.Context()); err != nil {
		t.Fatal(err)
	}
	cfg, err := original.localConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Server.ConsoleHTTPSCertFile = "/run/nsc/console.crt"
	cfg.Server.ConsoleHTTPSKeyFile = "/run/nsc/console.key"
	controllerConfig := filepath.Join(original.Directory, "existing.yaml")
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(controllerConfig, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = original.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error {
		// SQLite tests only metadata ownership transactions. Swarm execution and
		// PostgreSQL are covered separately by the real nested-node acceptance.
		runtime, err := store.GetRuntime(t.Context(), original.Record.RuntimeID)
		if err != nil {
			return err
		}
		runtime.Type = model.RuntimeDocker
		if err = store.(*unitSeedStore).SQLStore.CreateRuntime(t.Context(), &runtime); err != nil {
			return err
		}
		for key, receipt := range original.seedRecord().Components {
			value := model.Service{ID: "svc-" + key, Name: key, RuntimeID: runtime.ID, RuntimeObjectID: receipt.ID, ExternalID: receipt.ID, ServiceKey: key, Metadata: model.Metadata{Revision: 1, CreatedAt: timeNow(), UpdatedAt: timeNow()}}
			if err = store.UpsertService(t.Context(), &value); err != nil {
				return err
			}
		}
		doc, err := store.GetControlDocument(t.Context(), bootstrap.InstallationDocumentID)
		if err != nil {
			return err
		}
		return store.WriteControlDocuments(storage.WithSystemBootstrap(t.Context()), []storage.ControlWrite{{Document: doc, Expected: doc.Revision, Remove: true}})
	}); err != nil {
		t.Fatal(err)
	}
	bootstrapConfig := original.Config
	bootstrapConfig.Adoption = &bootstrap.Adoption{ControllerConfigFile: controllerConfig, ClusterID: original.Record.ClusterID, DataNodeID: original.Record.Nodes[original.Config.DataNode].NodeID, Volume: original.Record.Resources["volume:postgresql"].ID, ServiceIDs: map[string]string{}}
	for key, receipt := range original.seedRecord().Components {
		bootstrapConfig.Adoption.ServiceIDs[key] = receipt.ID
	}
	directory := filepath.Join(t.TempDir(), "adopt")
	journal, err := bootstrap.OpenJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { journal.Close() })
	record, err := journal.Initialize(bootstrapConfig, "fixture-machine")
	if err != nil {
		t.Fatal(err)
	}
	record.Nodes = original.Record.Nodes
	record.PoolID = original.Record.PoolID
	record.RuntimeID = original.Record.RuntimeID
	record.CertificateID = original.Record.CertificateID
	return &Installer{Config: bootstrapConfig, Record: &record, Journal: journal, Directory: directory, Hosts: original.Hosts, Database: original.Database}, cluster
}

func TestAdoptionPlanIsReadOnlyAndRegistrationPreservesExistingResources(t *testing.T) {
	i, cluster := adoptionFixture(t, 1)
	before := bootstrap.Digest(cluster.services)
	var identities []byte
	if err := i.withStore(t.Context(), true, func(store storage.Store, _ config.Config) error {
		doc, err := store.GetControlDocument(t.Context(), "community:identities")
		identities = bytes.Clone(doc.Payload)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	cluster.calls = nil
	plan, err := i.Plan(t.Context(), bootstrap.Adopt)
	if err != nil {
		t.Fatal(err)
	}
	if len(i.Record.Resources) != 0 || i.Record.RuntimeID == i.Config.Adoption.ServiceIDs["controller"] {
		t.Fatal("planning changed local ownership")
	}
	if plan.Scope.ClusterID != i.Config.Adoption.ClusterID || plan.Scope.DataVolume != i.Config.Adoption.Volume {
		t.Fatal("plan omitted actual storage")
	}
	for _, call := range cluster.calls {
		if strings.HasPrefix(call, "POST ") {
			t.Fatal("planning wrote to Docker")
		}
	}
	executor := bootstrap.Executor{Journal: i.Journal, Driver: i}
	result, err := executor.Execute(t.Context(), i.Record, plan, plan.Hash, "verified-adopt")
	if err != nil || result.State != bootstrap.Succeeded {
		t.Fatal(result.State, err)
	}
	if bootstrap.Digest(cluster.services) != before || len(cluster.services) != 4 {
		t.Fatal("adoption changed or recreated service objects")
	}
	if err = i.withStore(t.Context(), true, func(store storage.Store, _ config.Config) error {
		doc, err := store.GetControlDocument(t.Context(), "community:identities")
		if err != nil {
			return err
		}
		if !bytes.Equal(identities, doc.Payload) {
			return errors.New("administrator identity was rewritten")
		}
		doc, err = store.GetControlDocument(t.Context(), bootstrap.InstallationDocumentID)
		if err != nil {
			return err
		}
		var value bootstrap.Installation
		json.Unmarshal(doc.Payload, &value)
		if value.ID != i.Record.ID || value.State != bootstrap.Succeeded || value.Health != "UNVERIFIED" || len(value.Drift) == 0 {
			return errors.New("registration was misreported as verified health")
		}
		apps, err := store.ListApplications(t.Context(), i.Record.RuntimeID, storage.Page{Limit: 20})
		if err != nil {
			return err
		}
		if len(apps) != 4 {
			return errors.New("four system applications were not registered")
		}
		for _, app := range apps {
			if !app.System || app.Managed {
				return errors.New("legacy system protection changed")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	again, err := executor.Execute(t.Context(), i.Record, plan, plan.Hash, "verified-adopt")
	if err != nil || again.ID != result.ID {
		t.Fatal("duplicate adoption was not idempotent", err)
	}
	var output bytes.Buffer
	if err = i.Handoff(&output); err != nil || !strings.Contains(output.String(), "UNVERIFIED") {
		t.Fatal(output.String(), err)
	}
	if err = i.Verify(t.Context(), &output); err != nil {
		t.Fatal(err)
	}
}

func TestAdoptionRefusesOwnershipStorageAndTrustConflicts(t *testing.T) {
	for _, scenario := range []string{"missing-service", "foreign-installation", "database-replicas", "database-volume", "ordinary-application", "gateway-name", "gateway-role", "controller-identity", "changed-receipt"} {
		t.Run(scenario, func(t *testing.T) {
			i, cluster := adoptionFixture(t, 1)
			switch scenario {
			case "missing-service":
				i.Config.Adoption.ServiceIDs["controller"] = "unregistered"
			case "foreign-installation":
				if err := i.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error {
					foreign := i.seedRecord()
					foreign.ID = "foreign-installation"
					return writeInstallation(t.Context(), store, foreign, 0)
				}); err != nil {
					t.Fatal(err)
				}
			case "database-replicas":
				s := cluster.services[i.Config.Adoption.ServiceIDs["postgresql"]]
				*s.Spec.Mode.Replicated.Replicas = 2
				cluster.services[s.ID] = s
			case "database-volume":
				s := cluster.services[i.Config.Adoption.ServiceIDs["postgresql"]]
				s.Spec.TaskTemplate.ContainerSpec.Mounts = nil
				cluster.services[s.ID] = s
			case "ordinary-application":
				i.Database = claimedConnector{i.Database}

			case "gateway-name":
				if err := i.withStore(t.Context(), false, func(store storage.Store, cfg config.Config) error {
					ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
					if err != nil {
						return err
					}
					_, err = core.New(store, cfg, ring).CreateProject(t.Context(), "Legacy", "gateway")
					return err
				}); err != nil {
					t.Fatal(err)
				}
			case "gateway-role":
				h := i.Hosts["one"].(*fakeHost)
				h.files["gateway.yaml"] = h.files["sandbox.yaml"]
			case "controller-identity":
				h := i.Hosts["one"].(*fakeHost)
				h.files["controller-secrets/controller.crt"] = h.files["gateway-secrets/agent.crt"]
			case "changed-receipt":
				s := cluster.services[i.Config.Adoption.ServiceIDs["controller"]]
				i.Record.Resources["reserve:controller"] = serviceReceipt(s)
				s.Version.Index++
				s.Spec.TaskTemplate.ForceUpdate++
				cluster.services[s.ID] = s
			}
			i.Record.ConfigDigest = bootstrap.Digest(i.Config)
			if _, err := i.Plan(t.Context(), bootstrap.Adopt); err == nil {
				t.Fatal("conflicting ownership or identity was accepted")
			}
			for _, call := range cluster.calls {
				if strings.HasPrefix(call, "POST /services/") && strings.Contains(call, "/update") {
					t.Fatal("adoption changed a service")
				}
			}
		})
	}
}

type embeddedEngine interface{ Engine }
type unavailableHost struct{ embeddedEngine }

func (h unavailableHost) Engine(context.Context, string, string, any, any) error {
	return bootstrap.ErrNodeUnavailable
}

type failingIdentityHost struct{ embeddedEngine }

func (h failingIdentityHost) Engine(context.Context, string, string, any, any) error {
	return errors.New("SSH host identity differs")
}

func TestAdoptionOfflineNodeIsUnverifiedAndHostIdentityMismatchBlocks(t *testing.T) {
	i, _ := adoptionFixture(t, 3)
	original := i.Hosts["three"]
	i.Hosts["three"] = unavailableHost{original}
	observation, err := i.Observe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	evidence := observation.(Observation).Adoption
	if !contains(evidence.Installation.Drift, "node_identity_unverified:"+i.Record.Nodes["three"].NodeID) {
		// A new local journal has no existing Node ID yet. Compare registered evidence.
		if !contains(evidence.Installation.Drift, "node_identity_unverified:"+evidence.Installation.Nodes["three"].NodeID) {
			t.Fatal("offline node reported as healthy")
		}
	}
	i.Hosts["three"] = failingIdentityHost{original}
	if _, err = i.Plan(t.Context(), bootstrap.Adopt); err == nil {
		t.Fatal("SSH host identity mismatch was treated as offline")
	}
	i.Hosts["three"] = original
	i.Hosts["one"] = unavailableHost{i.Hosts["one"]}
	if _, err = i.Plan(t.Context(), bootstrap.Adopt); err == nil {
		t.Fatal("unavailable maintenance/data node was accepted")
	}
}

func TestManagerTopologyRetainsActualVotingMembershipAndPlacement(t *testing.T) {
	i, cluster := installerFixture(t, 3)
	infrastructure(t, i)
	extra := swarm.Node{ID: "outside-inventory", Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager}, ManagerStatus: &swarm.ManagerStatus{Reachability: swarm.ReachabilityUnreachable}}
	cluster.nodes[extra.ID] = extra
	observed, err := i.Observe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	topology := observed.(Observation).Topology
	if topology.Managers != 4 || topology.Majority != 3 || topology.TolerableFailures != 1 || !topology.QuorumAvailable {
		t.Fatal(topology)
	}
	if result := managerTopology(nil); result.QuorumAvailable || result.Majority != 0 {
		t.Fatal(result)
	}
	node := cluster.nodes[i.Record.Nodes["one"].NodeID]
	node.Spec.Availability = swarm.NodeAvailabilityDrain
	cluster.nodes[node.ID] = node
	observed, err = i.Observe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = requirePlacement(observed.(Observation), i.Config); err == nil {
		t.Fatal("drained data node allowed new placement")
	}
}

type claimedConnector struct{ DatabaseConnector }

func (c claimedConnector) Open(ctx context.Context, cfg config.Config, engine Engine, id string, readOnly bool) (storage.Store, error) {
	store, err := c.DatabaseConnector.Open(ctx, cfg, engine, id, readOnly)
	if err != nil {
		return nil, err
	}
	return claimedStore{store}, nil
}

type claimedStore struct{ storage.Store }

func (s claimedStore) ListAllServicesFiltered(ctx context.Context, filter storage.ServiceFilter) ([]model.Service, error) {
	values, err := s.Store.ListAllServicesFiltered(ctx, filter)
	for n := range values {
		if values[n].ID == "svc-controller" {
			values[n].Managed = true
			values[n].ApplicationID = "ordinary-application"
		}
	}
	return values, err
}
func (s claimedStore) GetApplication(ctx context.Context, id string) (model.Application, error) {
	if id == "ordinary-application" {
		return model.Application{ID: id, Managed: true}, nil
	}
	return s.Store.GetApplication(ctx, id)
}

func TestExistingTwoManagerAdoptionPreservesTopology(t *testing.T) {
	i, _ := adoptionFixture(t, 3)
	i.Config.Nodes = i.Config.Nodes[:2]
	if err := i.Config.Validate(); err != nil {
		t.Fatal("existing two-Manager topology was forced to three", err)
	}
	i.Config.Adoption = nil
	if err := i.Config.Validate(); err == nil {
		t.Fatal("new multi-node bootstrap omitted its third Manager")
	}
}
