package maintenance

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

type engineFunc func(context.Context, string, string, any, any) error

func (f engineFunc) Engine(ctx context.Context, method, path string, input, output any) error {
	return f(ctx, method, path, input, output)
}
func TestMaintenanceRuntimeRefusesPhysicalAndStorageDrift(t *testing.T) {
	for _, scenario := range []string{"info", "nodes", "worker-entry", "missing-cluster", "missing-service", "wrong-service-id", "no-container", "data-down", "data-drained", "no-placement", "wrong-placement", "mount-type", "mount-name", "mount-target", "no-mode", "no-replicas", "two-replicas", "postgres-image", "no-physical", "physical", "no-schema", "schema"} {
		t.Run(scenario, func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			runtime := driver.Runtime.(*DockerRuntime)
			pg := engine.services["postgresql"]
			switch scenario {
			case "info":
				engine.fail = "GET /info"
			case "nodes":
				engine.fail = "GET /nodes"
			case "worker-entry", "missing-cluster":
				runtime.Engine = engineFunc(func(ctx context.Context, method, path string, input, output any) error {
					if path == "/info" {
						return encodeFixture(map[string]any{"ID": "engine-one", "Swarm": map[string]any{"NodeID": "node-one", "ControlAvailable": scenario != "worker-entry"}}, output)
					}
					return engine.Engine(ctx, method, path, input, output)
				})
			case "missing-service":
				delete(engine.services, "controller")
			case "wrong-service-id":
				value := engine.services["controller"]
				value.ID = "foreign"
				engine.services["controller"] = value
			case "no-container":
				value := engine.services["controller"]
				value.Spec.TaskTemplate.ContainerSpec = nil
				engine.services["controller"] = value
			case "data-down":
				engine.nodes[0].Status.State = swarm.NodeStateDown
			case "data-drained":
				engine.nodes[0].Spec.Availability = swarm.NodeAvailabilityDrain
			case "no-placement":
				pg.Spec.TaskTemplate.Placement = nil
			case "wrong-placement":
				pg.Spec.TaskTemplate.Placement.Constraints = []string{"node.id == foreign"}
			case "mount-type":
				pg.Spec.TaskTemplate.ContainerSpec.Mounts[0].Type = mount.TypeBind
			case "mount-name":
				pg.Spec.TaskTemplate.ContainerSpec.Mounts[0].Source = "empty-replacement"
			case "mount-target":
				pg.Spec.TaskTemplate.ContainerSpec.Mounts[0].Target = "/other"
			case "no-mode":
				pg.Spec.Mode.Replicated = nil
			case "no-replicas":
				pg.Spec.Mode.Replicated.Replicas = nil
			case "two-replicas":
				two := uint64(2)
				pg.Spec.Mode.Replicated.Replicas = &two
			case "postgres-image":
				pg.Spec.TaskTemplate.ContainerSpec.Image = release("b").Images.Controller
			case "no-physical":
				runtime.Physical = nil
			case "physical":
				runtime.Physical = func(context.Context) error { return errors.New("fresh node identity is unavailable") }
			case "no-schema":
				runtime.Schema = nil
			case "schema":
				engine.schema = false
			}
			engine.services["postgresql"] = pg
			if _, err := runtime.Observe(t.Context()); err == nil {
				t.Fatal("unverified storage/physical evidence passed", scenario)
			}
			for _, call := range engine.calls {
				if strings.HasPrefix(call, "POST ") {
					t.Fatal("read-only preflight changed resources")
				}
			}
		})
	}
}
func TestMaintenanceRuntimeReportsActualVotingMembersAndControllerCopies(t *testing.T) {
	driver, engine, _ := maintenanceFixture(t)
	engine.nodes = append(engine.nodes, swarm.Node{ID: "worker", Spec: swarm.NodeSpec{Role: swarm.NodeRoleWorker, Availability: swarm.NodeAvailabilityActive}, Status: swarm.NodeStatus{State: swarm.NodeStateReady}}, swarm.Node{ID: "other-manager", Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager, Availability: swarm.NodeAvailabilityActive}, Status: swarm.NodeStatus{State: swarm.NodeStateReady}, ManagerStatus: &swarm.ManagerStatus{Reachability: swarm.ReachabilityUnreachable}})
	snapshot, err := driver.Runtime.Observe(t.Context())
	if err != nil || snapshot.Topology.Managers != 2 || snapshot.Topology.Majority != 2 || snapshot.Topology.QuorumAvailable {
		t.Fatal(snapshot.Topology, err)
	}
	service := engine.services["controller"]
	service.Spec.Mode = swarm.ServiceMode{Global: &swarm.GlobalService{}}
	engine.services["controller"] = service
	driver.Record.Resources["reserve:controller"] = Receipt(service)
	driver.Record.Topology.NodeIDs = append(driver.Record.Topology.NodeIDs, "other-manager")
	driver.Record.Nodes["worker"] = bootstrap.Identity{NodeID: "worker", InstanceID: "unused-worker-controller"}
	// Managers outside the configured Controller inventory are voting members,
	// but must not inflate the expected Controller replica count.
	if err = driver.Runtime.Verify(t.Context(), "controller"); err != nil {
		t.Fatal(err)
	}
}
func TestImagePreparationAndVersionedUpdatesKeepDefiniteAndUnknownFailuresDistinct(t *testing.T) {
	for _, scenario := range []string{"pull", "pull-fails", "inspect-fails", "platform", "protocol", "maintenance", "schema", "cas-read", "cas-version", "cas-spec", "cas-write", "cas-readback", "cas-drift"} {
		t.Run(scenario, func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			runtime := driver.Runtime.(*DockerRuntime)
			calls := 0
			runtime.Engine = engineFunc(func(ctx context.Context, method, path string, input, output any) error {
				if strings.HasPrefix(path, "/images/") {
					calls++
					if scenario == "pull" || scenario == "pull-fails" || scenario == "inspect-fails" {
						if method == "POST" {
							if scenario == "pull-fails" {
								return errors.New("registry unavailable")
							}
							return nil
						}
						if calls == 1 || scenario == "inspect-fails" {
							return errors.New("image unavailable")
						}
					}
					if scenario == "platform" {
						return encodeFixture(map[string]any{"Os": "other", "Architecture": "amd64"}, output)
					}
				}
				if method == "GET" && path == "/services/controller" {
					calls++
					if scenario == "cas-read" || scenario == "cas-readback" && calls > 1 {
						return errors.New("transport unavailable")
					}
					value := engine.services["controller"]
					if scenario == "cas-version" {
						value.Version.Index++
					}
					if scenario == "cas-spec" || scenario == "cas-drift" && calls > 1 {
						value.Spec.Name = "external"
					}
					return encodeFixture(value, output)
				}
				return engine.Engine(ctx, method, path, input, output)
			})
			if scenario == "protocol" || scenario == "maintenance" || scenario == "schema" {
				engine.imageLabels = map[string]string{"io.nsc.bootstrap.contract": "1", "io.nsc.bootstrap.protocol": "2", "io.nsc.bootstrap.schema": "18", "io.nsc.bootstrap.maintenance": "1"}
				engine.imageLabels["io.nsc.bootstrap."+scenario] = "unsupported"
			}
			if strings.HasPrefix(scenario, "cas-") {
				before := component(driver.Record, "controller")
				spec := engine.services["controller"].Spec
				copy := *spec.TaskTemplate.ContainerSpec
				copy.Image = release("b").Images.Controller
				spec.TaskTemplate.ContainerSpec = &copy
				if scenario == "cas-write" {
					engine.fail = "POST /services/controller/update"
				}
				receipt, err := runtime.Update(t.Context(), before, spec)
				if err == nil {
					t.Fatal("unconfirmed update returned success")
				}
				if (scenario == "cas-write" || scenario == "cas-readback" || scenario == "cas-drift") && receipt.ID != before.ID {
					t.Fatal("lost known-ID receipt")
				}
				return
			}
			_, err := runtime.PrepareImages(t.Context(), release("b"))
			if (err == nil) != (scenario == "pull") {
				t.Fatal(scenario, err)
			}
		})
	}
}
func TestRolloutReadinessRejectsWrongTaskImageMissingTasksAndExternalChanges(t *testing.T) {
	for _, scenario := range []string{"service-transport", "changed-spec", "wrong-service", "tasks-transport", "count", "state", "container", "image", "nil-health", "recovered"} {
		t.Run(scenario, func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			runtime := driver.Runtime.(*DockerRuntime)
			service := engine.services["controller"]
			task := swarm.Task{ServiceID: service.ID, Spec: service.Spec.TaskTemplate, Status: swarm.TaskStatus{State: swarm.TaskStateRunning}}
			switch scenario {
			case "service-transport":
				engine.fail = "GET /services/controller"
			case "changed-spec":
				service.Spec.Name = "external"
				engine.services[service.ID] = service
			case "wrong-service":
				service.ID = "foreign"
				engine.services["controller"] = service
			case "tasks-transport":
				engine.fail = "GET /tasks"
			case "count":
				engine.tasks = []swarm.Task{task, task}
			case "state":
				task.Status.State = swarm.TaskStateFailed
				engine.tasks = []swarm.Task{task}
			case "container":
				task.Spec.ContainerSpec = nil
				engine.tasks = []swarm.Task{task}
			case "image":
				copy := *task.Spec.ContainerSpec
				copy.Image = release("b").Images.Controller
				task.Spec.ContainerSpec = &copy
				engine.tasks = []swarm.Task{task}
			case "nil-health":
				runtime.Health = nil
			case "recovered":
				attempts := 0
				runtime.Health = func(context.Context) error {
					attempts++
					if attempts == 1 {
						return errors.New("pending heartbeat")
					}
					return nil
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 1100*time.Millisecond)
			if scenario != "recovered" {
				cancel()
			}
			err := runtime.Verify(ctx, "controller")
			cancel()
			if (err == nil) != (scenario == "recovered") {
				t.Fatal(scenario, err)
			}
		})
	}
}

func TestMaintenancePlanListsObservedTargetsAndOriginalEngineBindings(t *testing.T) {
	driver, engine, _ := maintenanceFixture(t)
	engine.nodes[0].Status.Addr = "192.0.2.1"
	driver.Record.Nodes["second"] = bootstrap.Identity{NodeID: "node-two", EngineID: "engine-two"}
	engine.nodes = append(engine.nodes, swarm.Node{ID: "node-two", Spec: swarm.NodeSpec{Role: swarm.NodeRoleWorker}, Status: swarm.NodeStatus{Addr: "192.0.2.2"}})
	plan, err := driver.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Verify, Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Scope.Targets) != 2 || plan.Scope.Targets[0].Name != "one" || plan.Scope.Targets[0].EngineID != "engine-one" || !plan.Scope.Targets[0].Manager || plan.Scope.Targets[0].Address != "192.0.2.1" || plan.Scope.Targets[1].Manager {
		t.Fatal("plan omitted or invented target identities", plan.Scope.Targets)
	}
}

func TestDrainedManagersStillCountTowardActualRaftQuorum(t *testing.T) {
	driver, engine, _ := maintenanceFixture(t)
	for _, id := range []string{"drained-two", "drained-three"} {
		engine.nodes = append(engine.nodes, swarm.Node{ID: id, Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager, Availability: swarm.NodeAvailabilityDrain}, Status: swarm.NodeStatus{State: swarm.NodeStateReady}, ManagerStatus: &swarm.ManagerStatus{Reachability: swarm.ReachabilityReachable}})
	}
	snapshot, err := driver.Runtime.Observe(t.Context())
	if err != nil || snapshot.Topology.Managers != 3 || snapshot.Topology.Reachable != 3 || snapshot.Topology.Majority != 2 || !snapshot.Topology.QuorumAvailable {
		t.Fatal("workload drain was confused with loss of a Raft voter", snapshot.Topology, err)
	}
}

func TestPlanRemainsValidAcrossManagersButRejectsChangedTargets(t *testing.T) {
	driver, engine, _ := maintenanceFixture(t)
	plan, err := driver.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Verify, Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	runtime := driver.Runtime.(*DockerRuntime)
	runtime.Engine = engineFunc(func(ctx context.Context, method, path string, input, output any) error {
		if path == "/info" {
			return encodeFixture(map[string]any{"ID": "engine-two", "Swarm": map[string]any{"NodeID": "node-two", "ControlAvailable": true, "Cluster": map[string]string{"ID": driver.Record.ClusterID}}}, output)
		}
		return engine.Engine(ctx, method, path, input, output)
	})
	observed, err := driver.Observe(t.Context())
	if err != nil || plan.Validate(*driver.Record, plan.Hash, observed, time.Now()) != nil {
		t.Fatal("the Manager serving a read invalidated an unchanged cluster plan", err)
	}
	snapshot := observed.(Snapshot)
	snapshot.Targets[0].EngineID = "replacement-engine"
	if plan.Validate(*driver.Record, plan.Hash, snapshot, time.Now()) == nil {
		t.Fatal("a changed target Engine identity was accepted")
	}
}

func TestImagePreparationAuthenticatesPullsAndServiceUpdatesWithoutJournalingCredentials(t *testing.T) {
	driver, engine, _ := maintenanceFixture(t)
	runtime := driver.Runtime.(*DockerRuntime)
	authorization := rand.Text()
	runtime.RegistryAuth = map[string]string{"registry.example.test": authorization}
	pulled := false
	updated := false
	runtime.Engine = engineFunc(func(ctx context.Context, method, path string, input, output any) error {
		if strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json") && !pulled {
			return os.ErrNotExist
		}
		if method == "POST" {
			private, ok := input.(bootstrap.RegistryRequest)
			if !ok || private.Authorization != authorization {
				t.Fatal("approved private registry authentication is missing")
			}
			if strings.HasPrefix(path, "/images/create") {
				pulled = true
				return nil
			}
			if strings.Contains(path, "registryAuthFrom=") {
				t.Fatal("an old credential superseded the explicit approved auth")
			}
			body, _ := json.Marshal(private.Body)
			if strings.Contains(string(body), authorization) {
				t.Fatal("a credential entered the resource specification")
			}
			updated = true
		}
		return engine.Engine(ctx, method, path, input, output)
	})
	target := release("b")
	if _, err := runtime.PrepareImages(t.Context(), target); err != nil || !pulled {
		t.Fatal("private image preparation failed", err)
	}
	service := engine.services["controller"]
	spec := service.Spec
	copy := *spec.TaskTemplate.ContainerSpec
	copy.Image = target.Images.Controller
	spec.TaskTemplate.ContainerSpec = &copy
	if _, err := runtime.Update(t.Context(), Receipt(service), spec); err != nil || !updated {
		t.Fatal("private rollout authentication was not retained", err)
	}
	record, _ := json.Marshal(driver.Record)
	if strings.Contains(string(record), authorization) {
		t.Fatal("registry credential escaped into the ordinary journal")
	}
}
func TestImagePreparationBlocksWrongOrUnconfirmedPlatformsBeforeServiceChanges(t *testing.T) {
	for _, scenario := range []string{"foreign-image", "engine-os", "engine-arch", "mixed-targets", "missing-target-platform", "platforms-unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			runtime := driver.Runtime.(*DockerRuntime)
			runtime.Engine = engineFunc(func(ctx context.Context, method, path string, input, output any) error {
				if path == "/info" && (scenario == "engine-os" || scenario == "engine-arch") {
					architecture, osType := "x86_64", "linux"
					if scenario == "engine-os" {
						osType = "windows"
					} else {
						architecture = "unknown"
					}
					return encodeFixture(map[string]string{"Architecture": architecture, "OSType": osType}, output)
				}
				if path == "/nodes" && scenario == "platforms-unavailable" {
					return os.ErrNotExist
				}
				if strings.HasPrefix(path, "/images/") && scenario == "foreign-image" {
					return encodeFixture(map[string]any{"Os": "linux", "Architecture": "arm64"}, output)
				}
				return engine.Engine(ctx, method, path, input, output)
			})
			if scenario == "mixed-targets" {
				engine.nodes[0].Description.Platform.Architecture = "aarch64"
			}
			if scenario == "missing-target-platform" {
				engine.nodes = nil
			}
			if _, err := runtime.PrepareImages(t.Context(), release("b")); err == nil {
				t.Fatal("an unverified platform was accepted")
			}
			for _, call := range engine.calls {
				if strings.HasPrefix(call, "POST /services/") {
					t.Fatal("preflight changed a service")
				}
			}
		})
	}
}

func TestComponentVerificationWaitsForTheCompletedSwarmUpdate(t *testing.T) {
	driver, engine, _ := maintenanceFixture(t)
	runtime := driver.Runtime.(*DockerRuntime)
	service := engine.services["controller"]
	service.UpdateStatus = &swarm.UpdateStatus{State: swarm.UpdateStateUpdating}
	engine.services[service.ID] = service
	if err := runtime.verifyOnce(t.Context(), "controller"); err == nil {
		t.Fatal("RUNNING tasks made an unfinished rollout healthy")
	}
	service.UpdateStatus.State = swarm.UpdateStateCompleted
	engine.services[service.ID] = service
	if err := runtime.verifyOnce(t.Context(), "controller"); err != nil {
		t.Fatal("completed rollout did not pass its actual evidence", err)
	}
	service.UpdateStatus.State = swarm.UpdateStateRollbackCompleted
	engine.services[service.ID] = service
	if err := runtime.verifyOnce(t.Context(), "controller"); err != nil {
		t.Fatal("completed compatible rollback was rejected", err)
	}
}

func TestGatewayPhaseCannotCompleteTheInstallationBeforeItsObservationWindow(t *testing.T) {
	driver, engine, _ := maintenanceFixture(t)
	runtime := driver.Runtime.(*DockerRuntime)
	service := engine.services["nginx"]
	service.UpdateStatus = &swarm.UpdateStatus{State: swarm.UpdateStateUpdating}
	engine.services[service.ID] = service
	if err := runtime.verifyOnce(t.Context(), "nginx"); err != nil {
		t.Fatal("fresh target task and loading evidence was rejected", err)
	}
	if err := runtime.verifyOnce(t.Context(), "installation"); err == nil {
		t.Fatal("the installation completed before Gateway rollout settled")
	}
	service.UpdateStatus.State = swarm.UpdateStateCompleted
	engine.services[service.ID] = service
	if err := runtime.verifyOnce(t.Context(), "installation"); err != nil {
		t.Fatal("completed installation did not pass", err)
	}
}
