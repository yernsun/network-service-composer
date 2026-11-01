package install

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/bootstrap/maintenance"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/storage"
	"gopkg.in/yaml.v3"
)

type maintenanceConnection struct {
	DatabaseConnector
	modes []bool
}

func (c *maintenanceConnection) Open(ctx context.Context, cfg config.Config, engine Engine, container string, readOnly bool) (storage.Store, error) {
	c.modes = append(c.modes, readOnly)
	return c.DatabaseConnector.Open(ctx, cfg, engine, container, readOnly)
}

type cliMaintenanceRuntime struct {
	installer *Installer
	cluster   *fakeCluster
	lost      bool
}

func (r *cliMaintenanceRuntime) Observe(context.Context) (maintenance.Snapshot, error) {
	value := maintenance.Snapshot{ClusterID: r.installer.Record.ClusterID, NodeID: r.installer.Record.Nodes["one"].NodeID, EngineID: r.installer.Record.Nodes["one"].EngineID, Components: map[string]bootstrap.Resource{}, Services: map[string]swarm.Service{}, Topology: r.installer.Record.Topology, Data: r.installer.Record.Database, SchemaCompatible: true, Healthy: true}
	for key, receipt := range r.installer.seedRecord().Components {
		actual := r.cluster.services[receipt.ID]
		value.Components[key] = serviceReceipt(actual)
		value.Services[key] = actual
	}
	return value, nil
}
func (r *cliMaintenanceRuntime) PrepareImages(_ context.Context, target bootstrap.Release) (bootstrap.Resource, error) {
	return bootstrap.Resource{Kind: "images", ID: r.installer.Record.ID, SpecDigest: bootstrap.Digest(target)}, nil
}
func (r *cliMaintenanceRuntime) Update(ctx context.Context, before bootstrap.Resource, spec swarm.ServiceSpec) (bootstrap.Resource, error) {
	result, err := r.installer.updateService(ctx, before, spec)
	if r.lost && err == nil {
		r.lost = false
		return result, errors.New("lost response")
	}
	return result, err
}
func (r *cliMaintenanceRuntime) Verify(context.Context, string) error { return nil }
func day2Fixture(t *testing.T) (*Installer, *cliMaintenanceRuntime, *maintenanceConnection) {
	t.Helper()
	installer, cluster := installerFixture(t, 1)
	infrastructure(t, installer)
	for _, key := range []string{"controller", "nginx", "nginx-sandbox"} {
		receipt, err := installer.Apply(t.Context(), bootstrap.Step{Key: "start:" + key})
		if err != nil {
			t.Fatal(err)
		}
		installer.Record.Resources["start:"+key] = receipt
	}
	installer.Record.Revision = 2
	if err := installer.initializeDatabase(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := installer.Journal.Save(installer.Record); err != nil {
		t.Fatal(err)
	}
	runtime := &cliMaintenanceRuntime{installer: installer, cluster: cluster}
	installer.MaintenanceRuntime = func(*bootstrap.Record, storage.Store) maintenance.Runtime { return runtime }
	connection := &maintenanceConnection{DatabaseConnector: installer.Database}
	installer.Database = connection
	return installer, runtime, connection
}
func TestCLIUpgradeAndRollbackUseWritableMetadataAndRetainPinnedData(t *testing.T) {
	installer, _, connection := day2Fixture(t)
	before := installer.Record.Database
	initial := *installer.Record.Release
	target := initial
	target.Version = "v0.1.0-maintenance-fixture"
	target.Images.Controller = "registry.example.test/controller@sha256:" + strings.Repeat("b", 64)
	target.Images.Gateway = target.Images.Controller
	installer.Target = &target
	plan, err := installer.PlanMaintenance(t.Context(), bootstrap.Upgrade)
	if err != nil {
		t.Fatal(err)
	}
	if len(connection.modes) == 0 || !connection.modes[len(connection.modes)-1] {
		t.Fatal("planning opened a writable connection")
	}
	operation, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "cli-upgrade-plan")
	if err != nil || operation.State != bootstrap.Succeeded {
		t.Fatal(operation, err)
	}
	if connection.modes[len(connection.modes)-1] || installer.Record.Database != before || installer.Record.Release.Version != target.Version {
		t.Fatal("maintenance used read-only SQL or changed database authority")
	}
	duplicate, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "cli-upgrade-plan")
	if err != nil || duplicate.ID != operation.ID {
		t.Fatal("duplicate command changed operation", err)
	}
	if _, err = installer.ExecuteMaintenance(t.Context(), bootstrap.Plan{Hash: "different"}, "different", "cli-upgrade-plan"); err == nil {
		t.Fatal("a CLI idempotency key approved another plan")
	}
	rollback, err := installer.PlanMaintenance(t.Context(), bootstrap.Rollback)
	if err != nil {
		t.Fatal(err)
	}
	result, err := installer.ExecuteMaintenance(t.Context(), rollback, rollback.Hash, "cli-rollback-plan")
	if err != nil || result.State != bootstrap.Succeeded || installer.Record.Release.Version != initial.Version || len(installer.Record.History) != 0 {
		t.Fatal(result, err)
	}
}
func TestCLIInterruptedMaintenanceResumesOriginalClaim(t *testing.T) {
	installer, runtime, connection := day2Fixture(t)
	plan, err := installer.PlanMaintenance(t.Context(), bootstrap.Repair)
	if err != nil {
		t.Fatal(err)
	}
	runtime.lost = true
	operation, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "cli-interrupted-repair")
	if err == nil || operation.State != bootstrap.Unknown {
		t.Fatal(operation, err)
	}
	resumed, err := installer.ResumeMaintenance(t.Context(), operation.ID)
	if err != nil || resumed.State != bootstrap.Succeeded || connection.modes[len(connection.modes)-1] {
		t.Fatal(resumed, err)
	}
	if _, err = installer.ResumeMaintenance(t.Context(), "foreign-operation"); err == nil {
		t.Fatal("missing claim or foreign operation resumed")
	}
}
func TestCLIMaintenanceRejectsStalePlansLostIdentitiesAndForeignInstallation(t *testing.T) {
	for _, scenario := range []string{"stale", "foreign", "lost-key", "bad-trust", "missing-baseline", "invalid-release"} {
		t.Run(scenario, func(t *testing.T) {
			installer, _, _ := day2Fixture(t)
			switch scenario {
			case "foreign":
				installer.Record.ID = "other"
			case "lost-key":
				os.Remove(installer.material("maintenance.key"))
			case "bad-trust":
				installer.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error {
					repository := maintenance.Repository{Store: store}
					value, document, err := repository.Installation(t.Context())
					if err != nil {
						return err
					}
					value.TrustSHA256 = "changed"
					return writeInstallation(t.Context(), store, value, document.Revision)
				})
			case "missing-baseline":
				installer.Record.Release = nil
				installer.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error {
					value, document, err := (maintenance.Repository{Store: store}).Installation(t.Context())
					if err != nil {
						return err
					}
					value.Release = nil
					return writeInstallation(t.Context(), store, value, document.Revision)
				})
			case "invalid-release":
				file := filepath.Join(t.TempDir(), "release.json")
				os.WriteFile(file, []byte(`{"unknown":true}`), 0600)
				if _, err := DecodeMaintenanceRelease(file); err == nil {
					t.Fatal("unknown manifest fields accepted")
				}
				if _, err := DecodeMaintenanceRelease(file + "-missing"); err == nil {
					t.Fatal("missing manifest accepted")
				}
				return
			}
			plan, err := installer.PlanMaintenance(t.Context(), bootstrap.Repair)
			if scenario == "stale" {
				if err != nil {
					t.Fatal(err)
				}
				installer.Record.Revision++
				if _, err = installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "stale-maintenance"); err == nil {
					t.Fatal("stale plan executed")
				}
			} else if err == nil {
				t.Fatal("missing or foreign maintenance authority accepted")
			}
		})
	}
}

type day2Delegate interface{ Engine }
type day2Engine struct {
	day2Delegate
	image func(string, any) error
}

func (e day2Engine) Engine(ctx context.Context, method, path string, input, output any) error {
	if method == "GET" && strings.HasPrefix(path, "/images/") && e.image != nil {
		return e.image(path, output)
	}
	return e.day2Delegate.Engine(ctx, method, path, input, output)
}

func TestLegacyBaselineRequiresImmutableImagesAndKnownContracts(t *testing.T) {
	for _, scenario := range []string{"verified", "no-adoption", "mutable", "inspect", "contract", "protocol", "schema", "gateway-contract", "version"} {
		t.Run(scenario, func(t *testing.T) {
			installer, cluster := adoptionFixture(t, 1)
			value := bootstrap.Installation{Components: map[string]bootstrap.Resource{}}
			for key, id := range installer.Config.Adoption.ServiceIDs {
				value.Components[key] = serviceReceipt(cluster.services[id])
			}
			value.Database.Image = value.Components["postgresql"].Image
			gateway := cluster.services[installer.Config.Adoption.ServiceIDs["nginx"]]
			gateway.Spec.TaskTemplate.ContainerSpec.Image = "registry.example.test/gateway@sha256:" + strings.Repeat("b", 64)
			cluster.services[gateway.ID] = gateway
			value.Components["nginx"] = serviceReceipt(gateway)
			original := installer.Hosts["one"]
			controllerImage := url.PathEscape(value.Components["controller"].Image)
			installer.Hosts["one"] = day2Engine{day2Delegate: original, image: func(path string, output any) error {
				if scenario == "inspect" {
					return errors.New("image receipt unavailable")
				}
				labels := map[string]string{"io.nsc.bootstrap.contract": "1", "io.nsc.bootstrap.protocol": "2", "io.nsc.bootstrap.schema": "18"}
				controller := strings.Contains(path, controllerImage)
				if controller {
					switch scenario {
					case "contract":
						labels["io.nsc.bootstrap.contract"] = "0"
					case "protocol":
						labels["io.nsc.bootstrap.protocol"] = "3"
					case "schema":
						labels["io.nsc.bootstrap.schema"] = "19"
					case "version":
						labels["org.opencontainers.image.version"] = "v0.1.0-existing"
					}
				} else if scenario == "gateway-contract" {
					labels["io.nsc.bootstrap.contract"] = "0"
				}
				return encode(map[string]any{"Config": map[string]any{"Labels": labels}}, output)
			}}
			if scenario == "no-adoption" {
				installer.Config.Adoption = nil
			}
			if scenario == "mutable" {
				receipt := value.Components["controller"]
				receipt.Image = "registry.example.test/controller:latest"
				value.Components["controller"] = receipt
			}
			baseline, err := installer.adoptedBaseline(t.Context(), value)
			valid := scenario == "verified" || scenario == "version"
			if valid {
				if err != nil || baseline.Images.PostgreSQL != value.Database.Image || baseline.Images.Controller != value.Components["controller"].Image {
					t.Fatal("verified legacy baseline was changed", err)
				}
				if scenario == "version" && baseline.Version != "v0.1.0-existing" {
					t.Fatal("recorded version was lost")
				}
			} else if err == nil {
				t.Fatal("unknown legacy baseline gained upgrade rights", scenario)
			}
		})
	}
}
func TestCLIMaintenanceCanEstablishALegacyBaselineWithoutNewIdentityOrData(t *testing.T) {
	installer, cluster := adoptionFixture(t, 1)
	plan, err := installer.Plan(t.Context(), bootstrap.Adopt)
	if err != nil {
		t.Fatal(err)
	}
	result, err := (bootstrap.Executor{Journal: installer.Journal, Driver: installer}).Execute(t.Context(), installer.Record, plan, plan.Hash, "legacy-adopt-before-maintenance")
	if err != nil || result.State != bootstrap.Succeeded {
		t.Fatal(result.State, err)
	}
	// The already adopted original services remain the only ownership source.
	runtime := &cliMaintenanceRuntime{installer: installer, cluster: cluster}
	installer.MaintenanceRuntime = func(*bootstrap.Record, storage.Store) maintenance.Runtime { return runtime }
	installer.Components = []string{"controller"}
	repair, err := installer.PlanMaintenance(t.Context(), bootstrap.Repair)
	if err != nil {
		t.Fatal(err)
	}
	before := installer.Record.Database
	maintenanceIdentity := installer.Record.MaintenanceID
	result, err = installer.ExecuteMaintenance(t.Context(), repair, repair.Hash, "legacy-controlled-repair")
	if err != nil || result.State != bootstrap.Succeeded || installer.Record.Database != before || installer.Record.Release == nil {
		t.Fatal("legacy maintenance lost original data authority", result.State, err)
	}
	if installer.Record.MaintenanceID != maintenanceIdentity || installer.Record.Resources["secret:maintenance"].ID != "" {
		t.Fatal("legacy maintenance fabricated a missing identity")
	}
}
func TestLegacyConsoleVerificationUsesOnlyItsMountedCertificate(t *testing.T) {
	for _, scenario := range []string{"verified", "config-unavailable", "http-only", "service-unavailable", "certificate-unavailable", "invalid-certificate"} {
		t.Run(scenario, func(t *testing.T) {
			installer, cluster := adoptionFixture(t, 1)
			host := installer.Hosts["one"].(*fakeHost)
			switch scenario {
			case "config-unavailable":
				os.Remove(installer.Config.Adoption.ControllerConfigFile)
			case "http-only":
				cfg, err := installer.localConfig(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				cfg.Server.ConsoleHTTPSListen = ""
				raw, _ := yaml.Marshal(cfg)
				os.WriteFile(installer.Config.Adoption.ControllerConfigFile, raw, 0600)
			case "service-unavailable":
				delete(cluster.services, installer.Config.Adoption.ServiceIDs["controller"])
			case "certificate-unavailable":
				delete(host.files, "controller-secrets/console.crt")
			case "invalid-certificate":
				host.files["controller-secrets/console.crt"] = []byte("not-a-certificate")
			}
			client, err := installer.apiClientContext(t.Context())
			if scenario == "verified" {
				if err != nil {
					t.Fatal(err)
				}
				client.CloseIdleConnections()
				credential, err := installer.apiCredential()
				if err != nil || len(credential) == 0 {
					t.Fatal("registered current credential unavailable", err)
				}
				clear(credential)
			} else if err == nil {
				client.CloseIdleConnections()
				t.Fatal("legacy verification bypassed its original console trust", scenario)
			}
		})
	}
}
func TestCLIResumeRequiresOriginalOperationAndCurrentClaim(t *testing.T) {
	for _, scenario := range []string{"foreign-id", "unavailable-job", "changed-key", "history"} {
		t.Run(scenario, func(t *testing.T) {
			installer, runtime, _ := day2Fixture(t)
			plan, err := installer.PlanMaintenance(t.Context(), bootstrap.Repair)
			if err != nil {
				t.Fatal(err)
			}
			runtime.lost = true
			operation, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "interrupted-cli-resume")
			if err == nil || operation.State != bootstrap.Unknown {
				t.Fatal(operation.State, err)
			}
			id := operation.ID
			switch scenario {
			case "foreign-id":
				id = "foreign"
			case "changed-key":
				installer.Record.Operations[len(installer.Record.Operations)-1].Key = "other"
			case "history":
				installer.Record.History = nil
			case "unavailable-job":
				installer.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error {
					repo := maintenance.Repository{Store: store}
					value, doc, err := repo.Installation(t.Context())
					if err != nil {
						return err
					}
					value.OperationID = "missing"
					return writeInstallation(t.Context(), store, value, doc.Revision)
				})
			}
			resumed, err := installer.ResumeMaintenance(t.Context(), id)
			if scenario == "history" {
				if err != nil || resumed.State != bootstrap.Succeeded {
					t.Fatal("retained installation history was not reusable", err)
				}
			} else if err == nil {
				t.Fatal("foreign or unavailable claim resumed", scenario)
			}
		})
	}
}

func TestLocalDataPinAndConsumedHistoryCannotBeReplacedByAnOlderDatabaseCheckpoint(t *testing.T) {
	for _, scenario := range []string{"database-pin", "consumed-history"} {
		t.Run(scenario, func(t *testing.T) {
			installer, _, _ := day2Fixture(t)
			pin := installer.Record.Database
			err := installer.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error {
				repo := maintenance.Repository{Store: store}
				value, document, err := repo.Installation(t.Context())
				if err != nil {
					return err
				}
				if scenario == "database-pin" {
					value.Database.Volume = "external-replacement"
				} else {
					value.History = []bootstrap.ReleaseReceipt{{Release: *value.Release, Revision: 1}}
				}
				return writeInstallation(t.Context(), store, value, document.Revision)
			})
			if err != nil {
				t.Fatal(err)
			}
			installer.Record.CheckpointRevision++
			_, err = installer.PlanMaintenance(t.Context(), bootstrap.Repair)
			if scenario == "database-pin" {
				if err == nil || installer.Record.Database != pin {
					t.Fatal("external metadata replaced the original volume pin")
				}
			} else if err != nil || len(installer.Record.History) != 0 {
				t.Fatal("an older mirror resurrected consumed rollback history", err)
			}
		})
	}
}
