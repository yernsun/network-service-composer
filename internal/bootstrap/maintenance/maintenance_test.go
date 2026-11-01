package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type memoryJournal struct {
	saved int
	fail  bool
}

func (j *memoryJournal) Save(*bootstrap.Record) error {
	j.saved++
	if j.fail {
		return errors.New("checkpoint unavailable")
	}
	return nil
}

type fixtureEngine struct {
	record      *bootstrap.Record
	services    map[string]swarm.Service
	nodes       []swarm.Node
	tasks       []swarm.Task
	calls       []string
	fail        string
	lost        bool
	paused      bool
	health      bool
	schema      bool
	control     map[string]any
	secret      map[string]any
	imageLabels map[string]string
}

func encodeFixture(value, target any) error {
	if target == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
func (e *fixtureEngine) Engine(_ context.Context, method, path string, input, output any) error {
	if private, ok := input.(bootstrap.RegistryRequest); ok {
		input = private.Body
	}
	e.calls = append(e.calls, method+" "+path)
	if e.fail != "" && strings.Contains(method+" "+path, e.fail) {
		return errors.New("injected transport failure")
	}
	parsed, err := url.Parse(path)
	if err != nil {
		return err
	}
	switch {
	case parsed.Path == "/info":
		return encodeFixture(map[string]any{"OSType": "linux", "Architecture": "x86_64", "ID": "engine-one", "Swarm": map[string]any{"NodeID": "node-one", "ControlAvailable": true, "Cluster": map[string]string{"ID": e.record.ClusterID}}}, output)
	case parsed.Path == "/nodes":
		return encodeFixture(e.nodes, output)
	case strings.HasPrefix(parsed.Path, "/images/"):
		labels := e.imageLabels
		if labels == nil {
			labels = map[string]string{"io.nsc.bootstrap.contract": "1", "io.nsc.bootstrap.protocol": "2", "io.nsc.bootstrap.schema": "18", "io.nsc.bootstrap.maintenance": "1"}
		}
		return encodeFixture(map[string]any{"Os": "linux", "Architecture": "amd64", "Config": map[string]any{"Labels": labels}}, output)
	case strings.HasPrefix(parsed.Path, "/configs/"):
		if method == "POST" {
			var spec map[string]any
			encodeFixture(input, &spec)
			e.control["Spec"] = spec
			return nil
		}
		return encodeFixture(e.control, output)
	case strings.HasPrefix(parsed.Path, "/secrets/"):
		return encodeFixture(e.secret, output)
	case strings.HasPrefix(parsed.Path, "/services/"):
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		value, exists := e.services[parts[1]]
		if !exists {
			return os.ErrNotExist
		}
		if method == "POST" {
			if parsed.Query().Get("version") != fmt.Sprint(value.Version.Index) {
				return errors.New("version conflict")
			}
			if e.lost {
				e.lost = false
				var intended swarm.ServiceSpec
				encodeFixture(input, &intended)
				value.Spec = intended
				value.Version.Index++
				if value.Spec.Mode.ReplicatedJob != nil {
					value.JobStatus = &swarm.JobStatus{JobIteration: swarm.Version{Index: value.Version.Index}}
				}
				e.services[value.ID] = value
				return errors.New("lost update acknowledgement")
			}
			encodeFixture(input, &value.Spec)
			value.Version.Index++
			if value.Spec.Mode.ReplicatedJob != nil {
				value.JobStatus = &swarm.JobStatus{JobIteration: swarm.Version{Index: value.Version.Index}}
			}
			e.services[value.ID] = value
			return nil
		}
		if e.paused {
			value.UpdateStatus = &swarm.UpdateStatus{State: swarm.UpdateStatePaused}
		}
		return encodeFixture(value, output)
	case parsed.Path == "/tasks":

		var filter map[string][]string
		json.Unmarshal([]byte(parsed.Query().Get("filters")), &filter)
		filtered := []swarm.Task{}
		for _, task := range e.tasks {
			if task.ServiceID == filter["service"][0] {
				filtered = append(filtered, task)
			}
		}
		if len(filtered) > 0 {
			return encodeFixture(filtered, output)
		}
		value := e.services[filter["service"][0]]
		task := swarm.Task{ID: "task-" + value.ID, ServiceID: value.ID, NodeID: "node-one", Spec: value.Spec.TaskTemplate, Status: swarm.TaskStatus{State: swarm.TaskStateRunning}}
		return encodeFixture([]swarm.Task{task}, output)
	case strings.HasPrefix(parsed.Path, "/tasks/"):
		for _, task := range e.tasks {
			if task.ID == strings.TrimPrefix(parsed.Path, "/tasks/") {
				return encodeFixture(task, output)
			}
		}
		return os.ErrNotExist
	}
	return fmt.Errorf("unexpected typed request %s %s", method, path)
}
func release(digit string) bootstrap.Release {
	image := "registry.example.test/nsc@sha256:" + strings.Repeat(digit, 64)
	return bootstrap.Release{Version: "v0.1.0-fixture-" + digit, Images: bootstrap.Images{Controller: image, Gateway: image, PostgreSQL: "registry.example.test/postgres@sha256:" + strings.Repeat("c", 64)}, Protocol: 2, Schema: 18, Configuration: 1, PostgreSQLMajor: 18, Architectures: []string{"amd64", "arm64"}}
}
func maintenanceFixture(t *testing.T) (*Driver, *fixtureEngine, *memoryJournal) {
	t.Helper()
	one := uint64(1)
	initial := release("a")
	record := &bootstrap.Record{ID: "install-fixture", Revision: 2, ClusterID: "cluster-fixture", PoolID: "pool-fixture", RuntimeID: "runtime-fixture", ConfigDigest: "frozen-config", MaintenanceAnchor: "anchor", Resources: map[string]bootstrap.Resource{}, Nodes: map[string]bootstrap.Identity{"one": {InstanceID: "controller-one", NodeID: "node-one", EngineID: "engine-one"}}, Operations: []bootstrap.Operation{}, Topology: bootstrap.ManagerTopology{Managers: 1, Reachable: 1, Majority: 1, QuorumAvailable: true, NodeIDs: []string{"node-one"}}, Release: &initial, History: []bootstrap.ReleaseReceipt{}, Database: bootstrap.DatabasePin{NodeID: "node-one", EngineID: "engine-one", ServiceID: "postgresql", Image: initial.Images.PostgreSQL, Volume: "volume-original", SinglePointOfFailure: true}}
	engine := &fixtureEngine{record: record, services: map[string]swarm.Service{}, health: true, schema: true}
	engine.nodes = []swarm.Node{{ID: "node-one", Description: swarm.NodeDescription{Platform: swarm.Platform{OS: "linux", Architecture: "x86_64"}}, Status: swarm.NodeStatus{State: swarm.NodeStateReady}, Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager, Availability: swarm.NodeAvailabilityActive}, ManagerStatus: &swarm.ManagerStatus{Reachability: swarm.ReachabilityReachable, Leader: true}}}
	for _, key := range []string{"controller", "nginx", "nginx-sandbox", "postgresql"} {
		image := initial.Images.Controller
		if key == "postgresql" {
			image = initial.Images.PostgreSQL
		}
		service := swarm.Service{ID: key, Meta: swarm.Meta{Version: swarm.Version{Index: 10}}, Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: key}, Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &one}}, TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Image: image}, Placement: &swarm.Placement{Constraints: []string{"node.id == node-one"}}}}}
		if key == "postgresql" {
			service.Spec.TaskTemplate.ContainerSpec.Mounts = []mount.Mount{{Type: mount.TypeVolume, Source: "volume-original", Target: "/var/lib/postgresql"}}
		}
		engine.services[key] = service
		prefix := "reserve:"
		if key == "postgresql" {
			prefix = "service:"
		}
		record.Resources[prefix+key] = Receipt(service)
	}
	journal := &memoryJournal{}
	runtime := &DockerRuntime{Engine: engine, Record: record, Physical: func(context.Context) error { return nil }, Schema: func(context.Context) error {
		if !engine.schema {
			return errors.New("incompatible schema")
		}
		return nil
	}, Health: func(context.Context) error {
		if !engine.health {
			return errors.New("stale loaded evidence")
		}
		return nil
	}}
	return &Driver{Record: record, Runtime: runtime, Journal: journal}, engine, journal
}
func runPlan(t *testing.T, driver *Driver, action bootstrap.Action, target *bootstrap.Release, key string) bootstrap.Operation {
	t.Helper()
	plan, err := driver.Plan(t.Context(), bootstrap.PlanRequest{Action: action, Revision: driver.Record.Revision, Release: target})
	if err != nil {
		t.Fatal(err)
	}
	driver.Target = plan.Scope.Release
	operation, err := (bootstrap.Executor{Journal: driver.Journal, Driver: driver}).Execute(t.Context(), driver.Record, plan, plan.Hash, key)
	if err != nil {
		t.Fatal(err)
	}
	return operation
}
func TestCompatibleUpgradeRollbackAndDataRetention(t *testing.T) {
	driver, engine, journal := maintenanceFixture(t)
	target := release("b")
	before := driver.Record.Database
	operation := runPlan(t, driver, bootstrap.Upgrade, &target, "upgrade-compatible")
	if operation.State != bootstrap.Succeeded || driver.Record.Release.Version != target.Version || len(driver.Record.History) != 1 || driver.Record.Database != before || journal.saved < 10 {
		t.Fatal("upgrade did not retain durable compatible ownership")
	}
	for _, key := range []string{"controller", "nginx", "nginx-sandbox"} {
		if engine.services[key].Spec.TaskTemplate.ContainerSpec.Image != target.Images.Controller {
			t.Fatal("component did not upgrade", key)
		}
	}
	runPlan(t, driver, bootstrap.Rollback, nil, "rollback-compatible")
	if driver.Record.Release.Version != release("a").Version || driver.Record.Database != before {
		t.Fatal("rollback changed data ownership or did not restore known images")
	}
	if err := driver.Runtime.Verify(t.Context(), "arbitrary-shell"); err == nil {
		t.Fatal("arbitrary maintenance action accepted")
	}
}
func TestLostUpdateAcknowledgementReconcilesOnlyRecordedService(t *testing.T) {
	for _, applied := range []bool{true, false} {
		t.Run(fmt.Sprint(applied), func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			target := release("b")
			plan, err := driver.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Upgrade, Revision: 2, Release: &target})
			if err != nil {
				t.Fatal(err)
			}
			driver.Target = &target
			if applied {
				engine.lost = true
			} else {
				engine.fail = "POST /services/controller/update"
			}
			executor := bootstrap.Executor{Journal: driver.Journal, Driver: driver}
			op, err := executor.Execute(t.Context(), driver.Record, plan, plan.Hash, "lost-response-fixture")
			if err == nil || op.State != bootstrap.Unknown {
				t.Fatal("lost response was not UNKNOWN", op.State, err)
			}
			engine.fail = ""
			op, err = executor.Resume(t.Context(), driver.Record, op.ID)
			if err != nil || op.State != bootstrap.Succeeded {
				t.Fatal("same-object reconciliation failed", op.State, err)
			}
			if len(driver.Record.History) != 1 {
				t.Fatal("resume duplicated release retention")
			}
		})
	}
}
func TestMaintenanceRejectsStaleForeignUnhealthyOrIncompatibleEvidence(t *testing.T) {
	cases := []string{"revision", "unsupported", "missing-release", "protocol", "database-image", "registry", "health", "schema", "cluster", "pin", "spec", "missing-receipt", "quorum", "rollback-missing", "rollback-config", "rollback-spec", "component", "duplicate"}
	for _, scenario := range cases {
		t.Run(scenario, func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			target := release("b")
			input := bootstrap.PlanRequest{Action: bootstrap.Upgrade, Revision: 2, Release: &target}
			switch scenario {
			case "revision":
				input.Revision--
			case "unsupported":
				input.Action = bootstrap.Init
			case "missing-release":
				input.Release = nil
			case "protocol":
				target.Protocol++
			case "database-image":
				target.Images.PostgreSQL = target.Images.Controller
			case "registry":
				target.Images.Controller = strings.Replace(target.Images.Controller, "registry.example.test", "foreign.example.test", 1)
			case "health":
				engine.health = false
			case "schema":
				engine.schema = false
			case "cluster":
				engine.record.ClusterID = "changed"
				wrapped := driver.Runtime.(*DockerRuntime)
				copy := *driver.Record
				copy.ClusterID = "original"
				driver.Record = &copy
				wrapped.Record = engine.record
			case "pin":
				value := engine.services["postgresql"]
				value.Spec.TaskTemplate.ContainerSpec.Mounts = nil
				engine.services["postgresql"] = value
			case "spec":
				value := engine.services["controller"]
				value.Spec.Name = "external-change"
				engine.services["controller"] = value
			case "missing-receipt":
				delete(driver.Record.Resources, "reserve:controller")
			case "quorum":
				engine.nodes[0].ManagerStatus = nil
			case "rollback-missing":
				input.Action = bootstrap.Rollback
			case "rollback-config", "rollback-spec":
				input.Action = bootstrap.Rollback
				driver.Record.History = []bootstrap.ReleaseReceipt{{Release: target, ConfigurationDigest: "changed", NonImageDigests: map[string]string{"controller": "changed"}}}
				if scenario == "rollback-spec" {
					driver.Record.History[0].ConfigurationDigest = driver.Record.ConfigDigest
				}
			case "component":
				input.Components = []string{"external"}
			case "duplicate":
				input.Components = []string{"controller", "controller"}
			}
			if _, err := driver.Plan(t.Context(), input); err == nil {
				t.Fatal("unverified maintenance plan accepted")
			}
			for _, call := range engine.calls {
				if strings.HasPrefix(call, "POST ") {
					t.Fatal("preflight changed infrastructure", call)
				}
			}
		})
	}
}
func TestVerifyRepairAndBadImagePause(t *testing.T) {
	driver, engine, _ := maintenanceFixture(t)
	runPlan(t, driver, bootstrap.Verify, nil, "verify-original")
	before := engine.services["controller"].Spec.TaskTemplate.ForceUpdate
	runPlan(t, driver, bootstrap.Repair, nil, "repair-original")
	if engine.services["controller"].Spec.TaskTemplate.ForceUpdate != before+1 {
		t.Fatal("repair did not restart the same owned spec")
	}
	engine.paused = true
	if err := driver.Runtime.Verify(t.Context(), "controller"); err == nil {
		t.Fatal("paused rollout marked healthy")
	}
	engine.paused = false
	engine.health = false
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := driver.Runtime.Verify(ctx, "installation"); err == nil {
		t.Fatal("expired readiness marked healthy")
	}
}
func sqlRepository(t *testing.T, driver *Driver, engine *fixtureEngine) Repository {
	t.Helper()
	store, err := storage.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err = store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	value := bootstrap.Installation{ID: driver.Record.ID, Revision: driver.Record.Revision, Mode: "single", State: bootstrap.Succeeded, ClusterID: driver.Record.ClusterID, PoolID: driver.Record.PoolID, RuntimeID: driver.Record.RuntimeID, MaintenanceAnchor: driver.Record.MaintenanceAnchor, Database: driver.Record.Database, Nodes: driver.Record.Nodes, Topology: driver.Record.Topology, Release: driver.Record.Release, History: driver.Record.History, ConfigDigest: driver.Record.ConfigDigest, Components: map[string]bootstrap.Resource{}, ConsoleURLs: []string{"https://192.0.2.1:8443"}, AllowedOperations: []bootstrap.Action{}, Drift: []string{}, MaintenanceNodeID: "node-one", MaintenanceID: "maintenance-one", MaintenanceExpiresAt: &expires, MetadataLayout: 1, CredentialSecret: bootstrap.Resource{ID: "secret-one", Kind: "secret", SpecDigest: "secret-digest"}, Control: bootstrap.Resource{Kind: "config", ID: "config-one", SpecDigest: bootstrap.Digest([]byte("checkpoint"))}}
	for _, key := range []string{"controller", "nginx", "nginx-sandbox", "postgresql"} {
		value.Components[key] = component(driver.Record, key)
	}
	one := uint64(1)
	executor := swarm.Service{ID: "executor", Meta: swarm.Meta{Version: swarm.Version{Index: 10}}, Spec: swarm.ServiceSpec{TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Image: driver.Record.Release.Images.Controller, Args: []string{"version"}}, Placement: &swarm.Placement{Constraints: []string{"node.id == 0000000000000000000000000"}}}, Mode: swarm.ServiceMode{ReplicatedJob: &swarm.ReplicatedJob{MaxConcurrent: &one, TotalCompletions: &one}}}}
	engine.services[executor.ID] = executor
	value.Executor = Receipt(executor)
	driver.Record.Resources["reserve:executor"] = value.Executor
	engine.secret = map[string]any{"ID": "secret-one", "Spec": map[string]any{"Labels": map[string]string{"io.nsc.installation": value.ID}}}
	engine.control = map[string]any{"ID": "config-one", "Version": map[string]uint64{"Index": 1}, "Spec": map[string]any{"Name": "checkpoint", "Labels": map[string]string{"io.nsc.installation": value.ID}, "Data": []byte("checkpoint")}}
	if err = store.WriteControlDocuments(t.Context(), []storage.ControlWrite{write(bootstrap.InstallationDocumentID, bootstrap.InstallationDocumentKind, value.ID, value, 0)}); err != nil {
		t.Fatal(err)
	}
	return Repository{Store: store}
}
func TestWebPlanApprovalIdempotencyAndExactExecutorReceipt(t *testing.T) {
	driver, engine, _ := maintenanceFixture(t)
	repository := sqlRepository(t, driver, engine)
	provider := Provider{Repository: repository, Engine: engine, Directory: "/var/lib/nsc-bootstrap", Runtime: func(*bootstrap.Record) Runtime { return driver.Runtime }}
	view, err := provider.View(t.Context())
	if err != nil || len(view.AllowedOperations) != 3 {
		t.Fatal(view, err)
	}
	target := release("b")
	plan, err := provider.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Upgrade, Revision: 2, Release: &target})
	if err != nil {
		t.Fatal(err)
	}
	request := bootstrap.ExecuteRequest{PlanHash: plan.Hash, Revision: 2, IdempotencyKey: "approve-web-fixture", Actor: "admin"}
	job, err := provider.Start(t.Context(), request)
	if err != nil || !job.Acknowledged || job.Runner.ID != "executor" {
		t.Fatal(job, err)
	}
	first := len(engine.calls)
	duplicate, err := provider.Start(t.Context(), request)
	if err != nil || duplicate.ID != job.ID {
		t.Fatal("duplicate lost receipt", err)
	}
	for _, call := range engine.calls[first:] {
		if strings.HasPrefix(call, "POST ") {
			t.Fatal("duplicate launched another runner")
		}
	}
	request.PlanHash = "other-plan"
	if _, err = provider.Start(t.Context(), request); err == nil {
		t.Fatal("key reuse accepted")
	}
	if _, err = provider.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: 2}); err == nil {
		t.Fatal("concurrent job accepted")
	}
	view, err = provider.View(t.Context())
	if err != nil || len(view.AllowedOperations) != 0 || view.OperationID != job.ID {
		t.Fatal("active operation authority lost", view, err)
	}
	if _, err = provider.Operation(t.Context(), "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
}
func TestCheckpointPersistsBeforeVersionedSwarmMirror(t *testing.T) {
	driver, engine, journal := maintenanceFixture(t)
	repository := sqlRepository(t, driver, engine)
	plan, err := driver.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Verify, Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	job, err := repository.ClaimCLI(t.Context(), plan, "cli-maintenance-key", "cli")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := &Checkpointer{Journal: journal, Repository: repository, Engine: engine, Job: &job}
	driver.Journal = checkpoint
	op, err := (bootstrap.Executor{Journal: checkpoint, Driver: driver}).Execute(t.Context(), driver.Record, plan, plan.Hash, job.Key)
	if err != nil || op.State != bootstrap.Succeeded {
		t.Fatal(op.State, err)
	}
	installed, _, err := repository.Installation(t.Context())
	if err != nil || installed.OperationID != "" || installed.Revision != 3 || installed.Health != "VERIFIED" {
		t.Fatal(installed, err)
	}
	saved, _, err := repository.Job(t.Context(), job.ID)
	if err != nil || saved.State != bootstrap.Succeeded || saved.Operation == nil {
		t.Fatal(saved, err)
	}
	if engine.control["Spec"].(map[string]any)["Labels"].(map[string]any)["io.nsc.state"] != "SUCCEEDED" {
		t.Fatal("Swarm checkpoint lost terminal state")
	}
	journal.fail = true
	if err = checkpoint.Save(driver.Record); err == nil {
		t.Fatal("missing local checkpoint was ignored")
	}
}

func TestWebRunnerLostAcknowledgementAndStoppedTaskResume(t *testing.T) {
	for _, applied := range []bool{false, true} {
		t.Run(fmt.Sprint(applied), func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			repository := sqlRepository(t, driver, engine)
			provider := Provider{Repository: repository, Engine: engine, Directory: "/var/lib/nsc-bootstrap", Runtime: func(*bootstrap.Record) Runtime { return driver.Runtime }}
			plan, err := provider.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: 2})
			if err != nil {
				t.Fatal(err)
			}
			request := bootstrap.ExecuteRequest{PlanHash: plan.Hash, Revision: 2, IdempotencyKey: "lost-web-launch-fixture"}
			if applied {
				engine.lost = true
			} else {
				engine.fail = "POST /services/executor/update"
			}
			job, err := provider.Start(t.Context(), request)
			if err == nil || job.State != bootstrap.Unknown {
				t.Fatal("uncertain job launch was hidden", job.State, err)
			}
			engine.fail = ""
			observed, err := provider.Operation(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if applied && !observed.Acknowledged {
				t.Fatal("known intended Service ID was not reconciled")
			}
			job = observed
			if !applied {
				job, err = provider.Start(t.Context(), request)
				if err != nil || !job.Acknowledged {
					t.Fatal("unchanged original job could not retry", err)
				}
				return
			}
			if _, err = provider.Start(t.Context(), request); err == nil {
				t.Fatal("unconfirmed old executor was replaced")
			}
			value := engine.services["executor"]
			engine.tasks = []swarm.Task{{ID: "stopped-task", ServiceID: "executor", JobIteration: &value.JobStatus.JobIteration, Spec: value.Spec.TaskTemplate, Status: swarm.TaskStatus{State: swarm.TaskStateFailed}}}
			job.State = bootstrap.Unknown
			job.UpdatedAt = time.Now().Add(-time.Minute)
			if err = repository.SaveJob(t.Context(), &job); err != nil {
				t.Fatal(err)
			}
			request.OperationID = job.ID
			job, err = provider.Start(t.Context(), request)
			if err != nil || !job.Acknowledged {
				t.Fatal("confirmed stopped executor did not resume", err)
			}
		})
	}
}
func TestRepositoryAndProviderRejectMissingOrChangedAuthority(t *testing.T) {
	for _, scenario := range []string{"metadata", "expiry", "secret", "directory", "drift", "observation", "stale", "key", "foreign-job", "changed-control"} {
		t.Run(scenario, func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			repository := sqlRepository(t, driver, engine)
			provider := Provider{Repository: repository, Engine: engine, Directory: "/var/lib/nsc-bootstrap", Runtime: func(*bootstrap.Record) Runtime { return driver.Runtime }}
			installed, document, err := repository.Installation(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "metadata":
				installed.MetadataLayout = 0
			case "expiry":
				expired := time.Now().Add(-time.Hour)
				installed.MaintenanceExpiresAt = &expired
			case "secret":
				engine.secret["ID"] = "foreign"
			case "directory":
				provider.Directory = "relative"
			case "drift":
				value := engine.services["nginx"]
				value.Spec.Name = "external"
				engine.services[value.ID] = value
			case "observation":
				engine.fail = "GET /info"
			case "stale", "key", "foreign-job", "changed-control":
			}
			if err = repository.Store.WriteControlDocuments(t.Context(), []storage.ControlWrite{write(document.ID, document.Kind, document.ParentID, installed, document.Revision)}); err != nil {
				t.Fatal(err)
			}
			if scenario == "changed-control" {
				engine.control["ID"] = "foreign"
				if err = mirrorControl(t.Context(), engine, installed, driver.Record); err == nil {
					t.Fatal("foreign checkpoint overwritten")
				}
				return
			}
			plan, err := provider.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: 2})
			if scenario != "stale" && scenario != "key" && scenario != "foreign-job" {
				if err == nil {
					t.Fatal("unverified plan was admitted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			input := bootstrap.ExecuteRequest{PlanHash: plan.Hash, Revision: 2, IdempotencyKey: "valid-maintenance-key"}
			if scenario == "stale" {
				input.Revision--
			}
			if scenario == "key" {
				input.IdempotencyKey = "short"
			}
			if scenario == "foreign-job" {
				input.OperationID = "missing"
			}
			if _, err = provider.Start(t.Context(), input); err == nil {
				t.Fatal("unverified execution admitted")
			}
		})
	}
}
