package deployment

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type runnerCall struct {
	directory   string
	environment []string
	arguments   []string
}

type recordingRunner struct {
	calls []runnerCall
	run   func(runnerCall) (string, error)
}

func (r *recordingRunner) Run(_ context.Context, directory string, environment, arguments []string) (string, error) {
	call := runnerCall{directory: directory, environment: slices.Clone(environment), arguments: slices.Clone(arguments)}
	r.calls = append(r.calls, call)
	if r.run != nil {
		return r.run(call)
	}
	return "ok", nil
}

type workerFixture struct {
	store   *storage.SQLStore
	core    *core.Core
	keyring *security.Keyring
	config  config.Config
	runtime model.Runtime
}

func newWorkerFixture(t *testing.T) workerFixture {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "nsc.db") + "?_pragma=foreign_keys(1)&_time_format=sqlite"
	store, err := storage.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Storage.Driver = "sqlite"
	cfg.Storage.HA = false
	cfg.Management.Enabled = true
	cfg.Management.WorkDir = t.TempDir()
	cfg.Management.PollInterval = config.Duration(time.Millisecond)
	cfg.Management.HeartbeatInterval = config.Duration(10 * time.Millisecond)
	cfg.Management.DeployTimeout = config.Duration(5 * time.Second)
	cfg.Controller.InstanceID = "test-worker"
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	keyring, err := security.NewKeyring("v1", map[string]string{"v1": key})
	if err != nil {
		t.Fatal(err)
	}
	serviceCore := core.New(store, cfg, keyring)
	pool, err := serviceCore.CreatePool(context.Background(), core.CreatePoolInput{Name: "edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080"})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := serviceCore.CreateRuntime(context.Background(), core.CreateRuntimeInput{PoolID: pool.ID, Name: "local", RuntimeType: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", ManagementEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return workerFixture{store: store, core: serviceCore, keyring: keyring, config: cfg, runtime: runtime}
}

func installFixtureApplication(t *testing.T, fixture workerFixture, compose string) (model.Application, model.DeploymentOperation) {
	t.Helper()
	raw := ""
	application, operation, _, err := fixture.core.InstallApplication(context.Background(), core.InstallApplicationInput{
		ApplicationPreflightInput: core.ApplicationPreflightInput{RuntimeID: fixture.runtime.ID, ComposeYAML: compose, Environment: applicationx.EnvironmentInput{Raw: &raw}},
		Name:                      "Demo", ProjectName: "demo", Actor: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return application, operation
}

func TestWorkerRunsComposeWithSafeArgumentsAndNoInheritedEnvironment(t *testing.T) {
	fixture := newWorkerFixture(t)
	application, operation := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n    expose: [\"80\"]\n")
	runner := &recordingRunner{}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("commands = %#v", runner.calls)
	}
	call := runner.calls[0]
	joined := strings.Join(call.arguments, " ")
	if !strings.Contains(joined, "compose --project-name demo") || !strings.Contains(joined, "up -d --wait --no-build") {
		t.Fatalf("unsafe or incomplete Compose command: %q", joined)
	}
	if len(call.environment) != 0 {
		t.Fatalf("Controller environment was passed to runner: %#v", call.environment)
	}
	storedOperation, err := fixture.store.GetDeploymentOperation(context.Background(), operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedApplication, err := fixture.store.GetApplication(context.Background(), application.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedOperation.Status != model.OperationSucceeded || storedApplication.Status != model.ApplicationRunning || storedApplication.ActiveRevisionID == "" {
		t.Fatalf("deployment did not activate application: operation=%#v application=%#v", storedOperation, storedApplication)
	}
}

func TestApplicationActivationPreservesConcurrentDiscovery(t *testing.T) {
	fixture := newWorkerFixture(t)
	application, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n    expose: [\"80\"]\n")
	services, err := fixture.store.ListServicesFiltered(context.Background(), storage.ServiceFilter{ApplicationID: application.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("managed services = %#v, %v", services, err)
	}
	discovered := services[0]
	discovered.RuntimeObjectID = "container-object"
	discovered.ExternalID = "container-object"
	discovered.DiscoveredEnabled = true
	discovered.RoutingEligible = true
	discovered.RoutingIssues = nil
	discovered.DesiredInstances = 1
	discovered.RunningInstances = 1
	discovered.RuntimeReadyInstances = 1
	discovered.ReadyInstances = 1
	discovered.Status = model.ServiceAvailable
	discovered.UpdatedAt = time.Now().UTC()
	if err = fixture.store.UpsertService(context.Background(), &discovered); err != nil {
		t.Fatal(err)
	}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), &recordingRunner{})
	if err = worker.activateApplication(context.Background(), fixture.runtime, application, application.DesiredRevisionID,
		[]applicationx.ServiceSpec{{Name: "web", TargetPort: 80}}); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.store.GetService(context.Background(), discovered.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RuntimeObjectID != "container-object" || !stored.DiscoveredEnabled || !stored.RoutingEligible ||
		stored.RunningInstances != 1 || stored.ReadyInstances != 1 || stored.Status != model.ServiceAvailable {
		t.Fatalf("activation overwrote concurrent discovery: %#v", stored)
	}
}

func TestInitialDomainBindingAddsNetworkToFirstDeployment(t *testing.T) {
	fixture := newWorkerFixture(t)
	raw := ""
	application, _, _, err := fixture.core.InstallApplication(context.Background(), core.InstallApplicationInput{
		ApplicationPreflightInput: core.ApplicationPreflightInput{RuntimeID: fixture.runtime.ID,
			ComposeYAML: "services:\n  web:\n    image: nginx:1.29-alpine\n    expose: [\"80\"]\n", Environment: applicationx.EnvironmentInput{Raw: &raw}},
		Name: "Initial binding", ProjectName: "initial-binding", Actor: "test",
		DomainBinding: &core.InstallDomainBindingInput{Service: "web", CreateDomainBindingInput: core.CreateDomainBindingInput{
			Domain: &core.DomainConfigInput{Host: "initial.example.com", EntryPoints: []string{core.EntryPointHTTP}, TLS: core.DomainTLSInput{Mode: model.TLSDisabled}},
			Route:  core.RouteConfigInput{Path: "/", Backend: core.RouteBackendInput{Port: 80, Protocol: "http"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var override string
	runner := &recordingRunner{run: func(call runnerCall) (string, error) {
		contents, readErr := os.ReadFile(filepath.Join(call.directory, "nsc-network.override.yaml"))
		if readErr != nil {
			return "", readErr
		}
		override = string(contents)
		return "ok", nil
	}}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(override, `"web"`) || !strings.Contains(override, "nsc_backend") || !strings.Contains(override, "nsc-ingress") {
		t.Fatalf("first deployment omitted initial binding network override: %q", override)
	}
	services, err := fixture.store.ListServicesFiltered(context.Background(), storage.ServiceFilter{ApplicationID: application.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("managed services = %#v, %v", services, err)
	}
	bindings, err := fixture.store.ListServiceDomainBindings(context.Background(), services[0].ID)
	if err != nil || len(bindings) != 1 || bindings[0].Route.Status != model.RouteActive {
		t.Fatalf("initial Route was not activated after deployment: %#v, %v", bindings, err)
	}
}

func TestInstallIdempotencyReturnsOriginalApplicationAndOperation(t *testing.T) {
	fixture := newWorkerFixture(t)
	raw := ""
	input := core.InstallApplicationInput{
		ApplicationPreflightInput: core.ApplicationPreflightInput{RuntimeID: fixture.runtime.ID, ComposeYAML: "services:\n  web:\n    image: nginx:1.29-alpine\n", Environment: applicationx.EnvironmentInput{Raw: &raw}},
		Name:                      "Idempotent", ProjectName: "idempotent", IdempotencyKey: "install-request", Actor: "test",
	}
	firstApplication, firstOperation, _, err := fixture.core.InstallApplication(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	secondApplication, secondOperation, _, err := fixture.core.InstallApplication(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if firstApplication.ID != secondApplication.ID || firstOperation.ID != secondOperation.ID {
		t.Fatalf("idempotent install created duplicates: first=%s/%s second=%s/%s", firstApplication.ID, firstOperation.ID, secondApplication.ID, secondOperation.ID)
	}
	conflicting := input
	conflicting.ProjectName = "different-request"
	if _, _, _, err = fixture.core.InstallApplication(context.Background(), conflicting); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("same idempotency key accepted a different install request: %v", err)
	}
	applications, err := fixture.store.ListApplications(context.Background(), fixture.runtime.ID, storage.Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	operations, err := fixture.store.ListDeploymentOperations(context.Background(), firstApplication.ID, storage.Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(applications) != 1 || len(operations) != 1 {
		t.Fatalf("idempotency persisted duplicates: applications=%d operations=%d", len(applications), len(operations))
	}
}

func TestWorkerFailsClosedWhenRevisionCannotBeDecrypted(t *testing.T) {
	fixture := newWorkerFixture(t)
	_, operation := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n")
	if _, err := fixture.store.DB().Exec(`UPDATE application_revisions SET encrypted_resolved=? WHERE id=?`, []byte("corrupt"), operation.RevisionID); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.store.GetDeploymentOperation(context.Background(), operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.OperationFailed || len(runner.calls) != 0 || !strings.Contains(stored.ErrorMessage, "decrypt") {
		t.Fatalf("corrupt revision did not fail closed: operation=%#v calls=%#v", stored, runner.calls)
	}
}

func TestFailedUpgradeRedeploysPreviousRevision(t *testing.T) {
	fixture := newWorkerFixture(t)
	application, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n")
	runner := &recordingRunner{}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	services, err := fixture.store.ListServicesFiltered(context.Background(), storage.ServiceFilter{ApplicationID: application.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("managed services = %#v, %v", services, err)
	}
	if _, operation, bindingErr := fixture.core.CreateDomainBinding(context.Background(), services[0].ID, core.CreateDomainBindingInput{
		Domain: &core.DomainConfigInput{Host: "rollback.example.com", EntryPoints: []string{core.EntryPointHTTP}, TLS: core.DomainTLSInput{Mode: model.TLSDisabled}},
		Route:  core.RouteConfigInput{Path: "/", Backend: core.RouteBackendInput{Port: 80, Protocol: "http"}},
	}); bindingErr != nil || operation == nil {
		t.Fatalf("create managed binding operation=%#v err=%v", operation, bindingErr)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	attachedService, err := fixture.store.GetService(context.Background(), services[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	attachedSnapshot, err := fixture.store.BuildSnapshot(context.Background(), fixture.runtime.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	if attachedService.DNSStatus != model.DNSReady || !attachedService.RoutingEligible || len(attachedSnapshot.Routes) != 1 {
		t.Fatalf("successful network attachment was not published atomically: service=%#v snapshot=%#v", attachedService, attachedSnapshot)
	}
	active, err := fixture.store.GetApplication(context.Background(), application.ID)
	if err != nil {
		t.Fatal(err)
	}
	previousRevision := active.ActiveRevisionID
	raw := ""
	_, upgrade, _, err := fixture.core.CreateApplicationRevision(context.Background(), active.ID, core.CreateRevisionInput{
		ApplicationPreflightInput: core.ApplicationPreflightInput{ComposeYAML: "services:\n  web:\n    image: nginx:broken-candidate\n", Environment: applicationx.EnvironmentInput{Raw: &raw}}, Actor: "test",
	}, active.Revision)
	if err != nil {
		t.Fatal(err)
	}
	runner.run = func(call runnerCall) (string, error) {
		for index, argument := range call.arguments {
			if argument == "--file" && index+1 < len(call.arguments) {
				data, readErr := os.ReadFile(call.arguments[index+1])
				if readErr != nil {
					return "", readErr
				}
				if strings.Contains(string(data), "broken-candidate") {
					return "candidate failed", errors.New("simulated deployment failure")
				}
			}
		}
		return "rollback ok", nil
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	storedOperation, err := fixture.store.GetDeploymentOperation(context.Background(), upgrade.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedApplication, err := fixture.store.GetApplication(context.Background(), active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedOperation.Status != model.OperationRolledBack || storedApplication.ActiveRevisionID != previousRevision || storedApplication.DesiredRevisionID != previousRevision {
		t.Fatalf("upgrade was not rolled back: operation=%#v application=%#v", storedOperation, storedApplication)
	}
	bindings, err := fixture.store.ListServiceDomainBindings(context.Background(), services[0].ID)
	if err != nil || len(bindings) != 1 || bindings[0].Route.Status != model.RouteActive || !bindings[0].Route.Enabled {
		t.Fatalf("successful rollback disabled the existing route: bindings=%#v err=%v", bindings, err)
	}
}

func TestLastDomainDetachRemovesManagedDNSIntent(t *testing.T) {
	fixture := newWorkerFixture(t)
	application, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n    expose: [\"80\"]\n")
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config,
		slog.New(slog.NewTextHandler(io.Discard, nil)), &recordingRunner{})
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	services, err := fixture.store.ListServicesFiltered(context.Background(), storage.ServiceFilter{ApplicationID: application.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("managed services = %#v, %v", services, err)
	}
	binding, operation, err := fixture.core.CreateDomainBinding(context.Background(), services[0].ID, core.CreateDomainBindingInput{
		Domain: &core.DomainConfigInput{Host: "detach.example.com", EntryPoints: []string{core.EntryPointHTTP}, TLS: core.DomainTLSInput{Mode: model.TLSDisabled}},
		Route:  core.RouteConfigInput{Path: "/", Backend: core.RouteBackendInput{Port: 80, Protocol: "http"}},
	})
	if err != nil || operation == nil {
		t.Fatalf("attach operation=%#v err=%v", operation, err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	bindings, err := fixture.store.ListServiceDomainBindings(context.Background(), services[0].ID)
	if err != nil || len(bindings) != 1 || bindings[0].Route.Status != model.RouteActive {
		t.Fatalf("active binding = %#v err=%v", bindings, err)
	}
	detach, err := fixture.core.DeleteDomainBinding(context.Background(), services[0].ID, binding.Route.ID, bindings[0].Route.Revision, "detach-last")
	if err != nil || detach == nil {
		t.Fatalf("detach operation=%#v err=%v", detach, err)
	}
	// Discovery can still report the previous attachment while the explicit
	// detach is queued. It must not override the desired binding state.
	observed := services[0]
	observed.RoutingEligible, observed.DNSStatus = true, model.DNSReady
	if err := fixture.store.UpsertService(context.Background(), &observed); err != nil {
		t.Fatal(err)
	}
	override, err := worker.networkOverride(context.Background(), application, fixture.runtime, []applicationx.ServiceSpec{{Name: "web"}})
	if err != nil || strings.Contains(override, "nsc_backend") {
		t.Fatalf("last detach retained observed Gateway attachment: %s %v", override, err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.store.GetService(context.Background(), services[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DNSStatus != model.DNSMissing || stored.RoutingEligible {
		t.Fatalf("last detach retained generated DNS network intent: %#v", stored)
	}
	plan, err := fixture.store.BuildProbePlan(context.Background(), fixture.runtime.PoolID)
	if err != nil || len(plan.Targets) != 0 {
		t.Fatalf("last detach retained DNS probe targets: %#v err=%v", plan, err)
	}
}

func TestActivationPreservesNewerQueuedDesiredRevision(t *testing.T) {
	fixture := newWorkerFixture(t)
	application, install := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n    expose: [\"80\"]\n")
	current, err := fixture.store.GetApplication(context.Background(), application.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw := ""
	second, upgrade, _, err := fixture.core.CreateApplicationRevision(context.Background(), current.ID, core.CreateRevisionInput{
		ApplicationPreflightInput: core.ApplicationPreflightInput{ComposeYAML: "services:\n  web:\n    image: nginx:1.30-alpine\n    expose: [\"80\"]\n", Environment: applicationx.EnvironmentInput{Raw: &raw}}, Actor: "test",
	}, current.Revision)
	if err != nil {
		t.Fatal(err)
	}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config,
		slog.New(slog.NewTextHandler(io.Discard, nil)), &recordingRunner{})
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	afterInstall, err := fixture.store.GetApplication(context.Background(), application.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterInstall.ActiveRevisionID != install.RevisionID || afterInstall.DesiredRevisionID != second.ID || afterInstall.Status != model.ApplicationPending {
		t.Fatalf("first activation overwrote newer desired revision: %#v", afterInstall)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	afterUpgrade, err := fixture.store.GetApplication(context.Background(), application.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterUpgrade.ActiveRevisionID != second.ID || afterUpgrade.DesiredRevisionID != second.ID || afterUpgrade.Status != model.ApplicationRunning {
		t.Fatalf("queued upgrade did not become active: application=%#v operation=%#v", afterUpgrade, upgrade)
	}
}

func TestSuccessfulUpgradeRemovesObsoleteServiceWithoutRevivingItsBinding(t *testing.T) {
	fixture := newWorkerFixture(t)
	application, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n    expose: [\"80\"]\n  worker:\n    image: busybox:1.37\n    expose: [\"9000\"]\n")
	runner := &recordingRunner{}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	services, err := fixture.store.ListServicesFiltered(context.Background(), storage.ServiceFilter{ApplicationID: application.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 2 {
		t.Fatalf("managed services = %#v, %v", services, err)
	}
	var obsolete model.Service
	for _, service := range services {
		if service.Name == "worker" {
			obsolete = service
		}
	}
	if obsolete.ID == "" {
		t.Fatal("worker placeholder was not created")
	}
	binding, attach, err := fixture.core.CreateDomainBinding(context.Background(), obsolete.ID, core.CreateDomainBindingInput{
		Domain: &core.DomainConfigInput{Host: "obsolete.example.com", EntryPoints: []string{core.EntryPointHTTP}, TLS: core.DomainTLSInput{Mode: model.TLSDisabled}},
		Route:  core.RouteConfigInput{Path: "/", Backend: core.RouteBackendInput{Port: 9000, Protocol: "http"}},
	})
	if err != nil || attach == nil {
		t.Fatalf("create obsolete binding = %#v, operation=%#v, err=%v", binding, attach, err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.core.QueueServiceOperation(context.Background(), obsolete.ID, "scale", 3, "obsolete-scale"); err != nil {
		t.Fatal(err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := fixture.store.GetApplication(context.Background(), application.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw := ""
	_, upgrade, _, err := fixture.core.CreateApplicationRevision(context.Background(), current.ID, core.CreateRevisionInput{
		ApplicationPreflightInput: core.ApplicationPreflightInput{ComposeYAML: "services:\n  web:\n    image: nginx:1.31.5-alpine3.24\n    expose: [\"80\"]\n", Environment: applicationx.EnvironmentInput{Raw: &raw}}, Actor: "test",
	}, current.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	storedOperation, err := fixture.store.GetDeploymentOperation(context.Background(), upgrade.ID)
	if err != nil || storedOperation.Status != model.OperationSucceeded {
		t.Fatalf("upgrade operation = %#v, %v", storedOperation, err)
	}
	lastCommand := strings.Join(runner.calls[len(runner.calls)-1].arguments, " ")
	if !strings.Contains(lastCommand, "--remove-orphans") || strings.Contains(lastCommand, "--scale worker=") {
		t.Fatalf("candidate deployment retained obsolete runtime state: %q", lastCommand)
	}
	removed, err := fixture.store.GetService(context.Background(), obsolete.ID)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Managed || removed.ApplicationID != application.ID || removed.Status != model.ServiceRemoved || removed.DNSStatus != model.DNSMissing {
		t.Fatalf("obsolete service was not detached safely: %#v", removed)
	}
	bindings, err := fixture.store.ListServiceDomainBindings(context.Background(), obsolete.ID)
	if err != nil || len(bindings) != 1 || bindings[0].Route.Status != model.RouteError || bindings[0].Route.Enabled || !strings.Contains(bindings[0].Route.LastError, "removed") {
		t.Fatalf("obsolete binding was not retained as an error: %#v, %v", bindings, err)
	}
	updated, err := fixture.store.GetApplication(context.Background(), application.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := updated.ReplicaOverrides["worker"]; exists {
		t.Fatalf("obsolete replica override survived activation: %#v", updated.ReplicaOverrides)
	}
	snapshot, err := fixture.store.BuildSnapshot(context.Background(), fixture.runtime.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range snapshot.Routes {
		if route.ID == binding.Route.ID {
			t.Fatalf("obsolete Route remained in the Gateway snapshot: %#v", route)
		}
	}
}

func TestDomainOperationChangesOnlyItsOwnRoute(t *testing.T) {
	fixture := newWorkerFixture(t)
	application, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n    expose: [\"80\"]\n  api:\n    image: nginx:1.29-alpine\n    expose: [\"8080\"]\n")
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), &recordingRunner{})
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	services, err := fixture.store.ListServicesFiltered(context.Background(), storage.ServiceFilter{ApplicationID: application.ID}, storage.Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]model.Service)
	for _, service := range services {
		byName[service.Name] = service
	}
	failedBinding, _, err := fixture.core.CreateDomainBinding(context.Background(), byName["api"].ID, core.CreateDomainBindingInput{
		Domain: &core.DomainConfigInput{Host: "failed.example.com", EntryPoints: []string{core.EntryPointHTTP}, TLS: core.DomainTLSInput{Mode: model.TLSDisabled}},
		Route:  core.RouteConfigInput{Path: "/", Backend: core.RouteBackendInput{Port: 8080, Protocol: "http"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	failing := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), &recordingRunner{run: func(runnerCall) (string, error) {
		return "failed", errors.New("network deployment failed")
	}})
	if err = failing.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	successfulBinding, _, err := fixture.core.CreateDomainBinding(context.Background(), byName["web"].ID, core.CreateDomainBindingInput{
		Domain: &core.DomainConfigInput{Host: "working.example.com", EntryPoints: []string{core.EntryPointHTTP}, TLS: core.DomainTLSInput{Mode: model.TLSDisabled}},
		Route:  core.RouteConfigInput{Path: "/", Backend: core.RouteBackendInput{Port: 80, Protocol: "http"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed, err := fixture.store.GetRoute(context.Background(), failedBinding.Route.ID)
	if err != nil {
		t.Fatal(err)
	}
	succeeded, err := fixture.store.GetRoute(context.Background(), successfulBinding.Route.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != model.RouteError || failed.Enabled || succeeded.Status != model.RouteActive || !succeeded.Enabled {
		t.Fatalf("domain operation crossed Route boundaries: failed=%#v succeeded=%#v", failed, succeeded)
	}
}

func TestRevisionPreflightReturnsRedactedDiffWithoutQueuing(t *testing.T) {
	fixture := newWorkerFixture(t)
	oldRaw := "PASSWORD=old-secret\nOLD=value\n"
	manifest := "variables:\n  - key: PASSWORD\n    secret: true\n"
	application, _, _, err := fixture.core.InstallApplication(context.Background(), core.InstallApplicationInput{
		ApplicationPreflightInput: core.ApplicationPreflightInput{RuntimeID: fixture.runtime.ID, ComposeYAML: "services:\n  web:\n    image: nginx:1.29-alpine\n    environment: {PASSWORD: \"${PASSWORD}\", OLD: \"${OLD}\"}\n", Manifest: manifest, Environment: applicationx.EnvironmentInput{Raw: &oldRaw}},
		Name:                      "Preview", ProjectName: "preview", Actor: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	newRaw := "PASSWORD=new-secret\nNEW=value\n"
	preview, err := fixture.core.PreflightApplicationRevision(context.Background(), application.ID, core.ApplicationPreflightInput{
		ComposeYAML: "services:\n  web:\n    image: nginx:1.30-alpine\n    environment: {PASSWORD: \"${PASSWORD}\", NEW: \"${NEW}\"}\n  worker:\n    image: busybox:1.37\n", Manifest: manifest, Environment: applicationx.EnvironmentInput{Raw: &newRaw},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "old-secret") || strings.Contains(string(encoded), "new-secret") || !strings.Contains(string(encoded), "[redacted]") {
		t.Fatalf("revision preview leaked a secret: %s", encoded)
	}
	if !preview.Diff.ComposeChanged || !preview.Diff.ServiceConfigChanged || !slices.Contains(preview.Diff.EnvironmentAdded, "NEW") || !slices.Contains(preview.Diff.EnvironmentRemoved, "OLD") || !slices.Contains(preview.Diff.EnvironmentChanged, "PASSWORD") || !slices.Contains(preview.Diff.ServicesAdded, "worker") {
		t.Fatalf("unexpected revision diff: %#v", preview.Diff)
	}
	operations, err := fixture.store.ListDeploymentOperations(context.Background(), application.ID, storage.Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 {
		t.Fatalf("preflight queued an operation: %#v", operations)
	}
}

func TestEnvironmentOnlyKeyRevisionInheritsComposeAndProducesEnvironmentOnlyDiff(t *testing.T) {
	fixture := newWorkerFixture(t)
	raw := "NSC_STAGE=raw-v1\n"
	application, _, _, err := fixture.core.InstallApplication(context.Background(), core.InstallApplicationInput{
		ApplicationPreflightInput: core.ApplicationPreflightInput{
			RuntimeID:   fixture.runtime.ID,
			ComposeYAML: "services:\n  web:\n    image: nginx:1.29-alpine\n    environment:\n      NSC_STAGE: \"${NSC_STAGE:?required}\"\n    expose: [\"80\"]\n",
			Environment: applicationx.EnvironmentInput{Raw: &raw},
		},
		Name: "Environment only", ProjectName: "environment-only", Actor: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	var deployed []string
	runner := &recordingRunner{run: func(call runnerCall) (string, error) {
		for index, argument := range call.arguments {
			if argument == "--file" && index+1 < len(call.arguments) && strings.HasSuffix(call.arguments[index+1], "compose.yaml") {
				content, readErr := os.ReadFile(call.arguments[index+1])
				if readErr != nil {
					return "", readErr
				}
				deployed = append(deployed, string(content))
				break
			}
		}
		return "ok", nil
	}}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := fixture.store.GetApplication(context.Background(), application.ID)
	if err != nil {
		t.Fatal(err)
	}
	keyValue := "key-v2"
	values := []applicationx.EnvironmentVariable{{Key: "NSC_STAGE", Value: &keyValue}}
	input := core.ApplicationPreflightInput{Environment: applicationx.EnvironmentInput{Values: &values}}
	preview, err := fixture.core.PreflightApplicationRevision(context.Background(), application.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Diff.ComposeChanged || preview.Diff.ServiceConfigChanged || !slices.Equal(preview.Diff.EnvironmentChanged, []string{"NSC_STAGE"}) {
		t.Fatalf("environment-only preview reported unrelated changes: %#v", preview.Diff)
	}
	revision, operation, _, err := fixture.core.CreateApplicationRevision(context.Background(), application.ID,
		core.CreateRevisionInput{ApplicationPreflightInput: input, Actor: "test"}, current.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if revision.ParentRevisionID != current.ActiveRevisionID {
		t.Fatalf("environment-only revision parent=%q want %q", revision.ParentRevisionID, current.ActiveRevisionID)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	storedOperation, err := fixture.store.GetDeploymentOperation(context.Background(), operation.ID)
	if err != nil || storedOperation.Status != model.OperationSucceeded {
		t.Fatalf("environment-only operation=%#v err=%v", storedOperation, err)
	}
	if len(deployed) != 2 || !strings.Contains(deployed[1], "key-v2") || strings.Contains(deployed[1], "raw-v1") {
		t.Fatalf("environment-only deployment did not use inherited Compose with key environment: %#v", deployed)
	}
}

func TestTemporaryScaleDoesNotPersistAcrossStopAndStart(t *testing.T) {
	fixture := newWorkerFixture(t)
	application, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n")
	runner := &recordingRunner{}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	services, err := fixture.store.ListServicesFiltered(context.Background(), storage.ServiceFilter{ApplicationID: application.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("managed service discovery = %#v, %v", services, err)
	}
	if _, err = fixture.core.QueueServiceOperation(context.Background(), services[0].ID, "scale", 3, "scale-3"); err != nil {
		t.Fatal(err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.store.GetApplication(context.Background(), application.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, saved := stored.ReplicaOverrides["web"]; saved {
		t.Fatalf("temporary scale persisted an override: %#v", stored.ReplicaOverrides)
	}
	if _, err = fixture.core.QueueApplicationOperation(context.Background(), application.ID, "stop", "stop-once"); err != nil {
		t.Fatal(err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.core.QueueApplicationOperation(context.Background(), application.ID, "start", "start-once"); err != nil {
		t.Fatal(err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	last := strings.Join(runner.calls[len(runner.calls)-1].arguments, " ")
	if strings.Contains(last, "--scale web=3") || !strings.Contains(last, "up -d --wait --no-build --remove-orphans") {
		t.Fatalf("start retained a temporary scale: %q", last)
	}
}

func TestPersistentScalePublishesOnlySelectedComposeService(t *testing.T) {
	fixture := newWorkerFixture(t)
	app, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n    deploy:\n      replicas: 1\n  worker:\n    image: busybox:stable\n")
	runner := &recordingRunner{}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	services, err := fixture.store.ListServicesFiltered(context.Background(), storage.ServiceFilter{ApplicationID: app.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 2 {
		t.Fatalf("services = %#v, %v", services, err)
	}
	var web model.Service
	for _, service := range services {
		if service.Name == "web" {
			web = service
		}
	}
	if web.ID == "" {
		t.Fatal("web service missing")
	}
	for name, replicas := range map[string]int{"web": 4, "worker": 5} {
		if err := fixture.store.SetApplicationReplicaOverride(context.Background(), app.ID, name, replicas, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	_, err = fixture.core.QueueServiceOperation(context.Background(), web.ID, "scale", 3, "stale-persist", core.ServiceOperationOptions{Persist: true, ExpectedRevision: web.Revision + 1})
	if !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale service revision = %v", err)
	}
	op, err := fixture.core.QueueServiceOperation(context.Background(), web.ID, "scale", 3, "persist-web", core.ServiceOperationOptions{Persist: true, ExpectedRevision: web.Revision, Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := fixture.core.QueueServiceOperation(context.Background(), web.ID, "scale", 3, "persist-web", core.ServiceOperationOptions{Persist: true, ExpectedRevision: web.Revision, Actor: "test"})
	if err != nil || replayed.ID != op.ID {
		t.Fatalf("persistent scale replay = %#v, %v", replayed, err)
	}
	if _, err = fixture.core.QueueServiceOperation(context.Background(), web.ID, "scale", 4, "persist-web", core.ServiceOperationOptions{Persist: true, ExpectedRevision: web.Revision}); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("changed persistent retry was accepted: %v", err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.store.GetDeploymentOperation(context.Background(), op.ID)
	if err != nil || stored.Status != model.OperationSucceeded {
		t.Fatalf("persistent scale = %#v, %v", stored, err)
	}
	source, err := fixture.core.ApplicationSource(context.Background(), app.ID, op.RevisionID)
	if err != nil || !strings.Contains(source.ComposeYAML, "replicas: 3") || !strings.Contains(source.ComposeYAML, "worker:") {
		t.Fatalf("published source = %#v, %v", source, err)
	}
	current, err := fixture.store.GetApplication(context.Background(), app.ID)
	_, webOverride := current.ReplicaOverrides["web"]
	if err != nil || webOverride || current.ReplicaOverrides["worker"] != 5 || current.ActiveRevisionID != op.RevisionID {
		t.Fatalf("application after persistent scale = %#v, %v", current, err)
	}
	last := strings.Join(runner.calls[len(runner.calls)-1].arguments, " ")
	if !strings.Contains(last, "--scale web=3 web") || strings.Contains(last, "--remove-orphans") || strings.Contains(last, "worker") {
		t.Fatalf("persistent scale touched other services: %q", last)
	}
}

func TestRemoveRuntimeKeepsManagedServiceAndSavedSource(t *testing.T) {
	fixture := newWorkerFixture(t)
	app, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n")
	present := true
	runner := &recordingRunner{run: func(call runnerCall) (string, error) {
		if strings.Contains(strings.Join(call.arguments, " "), " ps --all --quiet web") {
			if present {
				return "container-1", nil
			}
			return "", nil
		}
		return "ok", nil
	}}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	services, err := fixture.store.ListServicesFiltered(context.Background(), storage.ServiceFilter{ApplicationID: app.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("services = %#v, %v", services, err)
	}
	service := services[0]
	op, err := fixture.core.QueueServiceOperation(context.Background(), service.ID, "remove_runtime", 0, "remove-web", core.ServiceOperationOptions{ExpectedRevision: service.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	replayed, err := fixture.core.QueueServiceOperation(context.Background(), service.ID, "remove_runtime", 0, "remove-web", core.ServiceOperationOptions{ExpectedRevision: service.Revision})
	if err != nil || replayed.ID != op.ID {
		t.Fatalf("runtime removal replay = %#v, %v", replayed, err)
	}
	stored, err := fixture.store.GetDeploymentOperation(context.Background(), op.ID)
	if err != nil || stored.Status != model.OperationSucceeded {
		t.Fatalf("remove operation = %#v, %v", stored, err)
	}
	removed, err := fixture.store.GetService(context.Background(), service.ID)
	if err != nil || !removed.Managed || removed.ApplicationID != app.ID || removed.Status != model.ServiceUnavailable || removed.RemovedAt == nil {
		t.Fatalf("removed service = %#v, %v", removed, err)
	}
	if _, err = fixture.core.QueueServiceOperation(context.Background(), service.ID, "scale", 2, "scale-removed"); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("temporary scale recreated removed service: %v", err)
	}
	source, err := fixture.core.ApplicationSource(context.Background(), app.ID, "")
	if err != nil || !strings.Contains(source.ComposeYAML, "web:") {
		t.Fatalf("saved source changed after removal: %#v, %v", source, err)
	}
	last := strings.Join(runner.calls[len(runner.calls)-1].arguments, " ")
	if !strings.Contains(last, "rm --stop --force web") || strings.Contains(last, "--volumes") {
		t.Fatalf("unsafe Compose removal: %q", last)
	}
	present = false
	if _, err = fixture.core.QueueServiceOperation(context.Background(), service.ID, "remove_runtime", 0, "remove-again", core.ServiceOperationOptions{ExpectedRevision: removed.Revision}); err != nil {
		t.Fatal(err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	again, err := fixture.store.GetService(context.Background(), service.ID)
	if err != nil || again.RemovedAt == nil || !again.RemovedAt.Equal(*removed.RemovedAt) {
		t.Fatalf("repeated removal changed deletion evidence: %#v, %v", again, err)
	}
}

func TestPersistentScaleWhileDownWaitsForExplicitStart(t *testing.T) {
	fixture := newWorkerFixture(t)
	app, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n")
	runner := &recordingRunner{}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.core.QueueApplicationOperation(context.Background(), app.ID, "down", "down-before-scale"); err != nil {
		t.Fatal(err)
	}
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	services, err := fixture.store.ListServicesFiltered(context.Background(), storage.ServiceFilter{ApplicationID: app.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("services = %#v, %v", services, err)
	}
	commandCount := len(runner.calls)
	op, err := fixture.core.QueueServiceOperation(context.Background(), services[0].ID, "scale", 2, "defer-scale", core.ServiceOperationOptions{Persist: true, ExpectedRevision: services[0].Revision})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != commandCount {
		t.Fatalf("deferred scale called Docker: %#v", runner.calls[commandCount:])
	}
	current, err := fixture.store.GetApplication(context.Background(), app.ID)
	if err != nil || current.Status != model.ApplicationDown || current.DesiredRevisionID != op.RevisionID || current.ActiveRevisionID == op.RevisionID {
		t.Fatalf("deferred application state = %#v, %v", current, err)
	}
	if _, err = fixture.core.QueueApplicationOperation(context.Background(), app.ID, "start", "start-after-scale"); err != nil {
		t.Fatal(err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err = fixture.store.GetApplication(context.Background(), app.ID)
	if err != nil || current.ActiveRevisionID != op.RevisionID || current.Status != model.ApplicationRunning {
		t.Fatalf("deferred scale not activated on start: %#v, %v", current, err)
	}
}

func TestQueuedServiceScaleRejectsChangedRuntimeIdentity(t *testing.T) {
	fixture := newWorkerFixture(t)
	app, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n")
	runner := &recordingRunner{}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	services, err := fixture.store.ListServicesFiltered(context.Background(), storage.ServiceFilter{ApplicationID: app.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("services = %#v, %v", services, err)
	}
	service := services[0]
	service.RuntimeObjectID = "runtime-v1"
	service.UpdatedAt = time.Now().UTC()
	if err := fixture.store.UpsertService(context.Background(), &service); err != nil {
		t.Fatal(err)
	}
	service, err = fixture.store.GetService(context.Background(), service.ID)
	if err != nil {
		t.Fatal(err)
	}
	op, err := fixture.core.QueueServiceOperation(context.Background(), service.ID, "scale", 2, "scale-before-edit", core.ServiceOperationOptions{ExpectedRevision: service.Revision})
	if err != nil {
		t.Fatal(err)
	}
	service.RuntimeObjectID = "runtime-v2"
	service.UpdatedAt = time.Now().UTC()
	if err := fixture.store.UpsertService(context.Background(), &service); err != nil {
		t.Fatal(err)
	}
	count := len(runner.calls)
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.store.GetDeploymentOperation(context.Background(), op.ID)
	if err != nil || stored.Status != model.OperationFailed || len(runner.calls) != count {
		t.Fatalf("stale scale changed Docker: operation=%#v calls=%#v err=%v", stored, runner.calls[count:], err)
	}
}

func TestFailedPersistentScaleKeepsActiveRevisionAndCanBeRetried(t *testing.T) {
	fixture := newWorkerFixture(t)
	app, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n")
	failDeploy := true
	runner := &recordingRunner{run: func(call runnerCall) (string, error) {
		if failDeploy && strings.Contains(strings.Join(call.arguments, " "), "--scale web=2 web") {
			failDeploy = false
			return "failed", errors.New("isolated Compose failure")
		}
		return "ok", nil
	}}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	services, err := fixture.store.ListServicesFiltered(context.Background(), storage.ServiceFilter{ApplicationID: app.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("services = %#v, %v", services, err)
	}
	before, err := fixture.store.GetApplication(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := fixture.core.QueueServiceOperation(context.Background(), services[0].ID, "scale", 2, "persist-fail", core.ServiceOperationOptions{Persist: true, ExpectedRevision: services[0].Revision})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed, err := fixture.store.GetDeploymentOperation(context.Background(), first.ID)
	if err != nil || failed.Status != model.OperationFailed {
		t.Fatalf("failed operation = %#v, %v", failed, err)
	}
	current, err := fixture.store.GetApplication(context.Background(), app.ID)
	if err != nil || current.ActiveRevisionID != before.ActiveRevisionID || current.Status != model.ApplicationRunning {
		t.Fatalf("failure replaced the active revision: %#v, %v", current, err)
	}
	service, err := fixture.store.GetService(context.Background(), services[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := fixture.core.QueueServiceOperation(context.Background(), service.ID, "scale", 2, "persist-retry", core.ServiceOperationOptions{Persist: true, ExpectedRevision: service.Revision})
	if err != nil {
		operations, _ := fixture.store.ListDeploymentOperations(context.Background(), app.ID, storage.Page{Limit: 20})
		statuses := make([]string, 0, len(operations))
		for _, operation := range operations {
			statuses = append(statuses, operation.RevisionID+":"+string(operation.Status))
		}
		t.Fatalf("retry queue failed: %v; active=%s desired=%s status=%s operations=%v", err, current.ActiveRevisionID, current.DesiredRevisionID, current.Status, statuses)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	succeeded, err := fixture.store.GetDeploymentOperation(context.Background(), retry.ID)
	if err != nil || succeeded.Status != model.OperationSucceeded {
		t.Fatalf("fresh persistent retry = %#v, %v", succeeded, err)
	}
}

func TestLifecycleOperationsAreTargetIdempotentAndDoNotImplicitlyStartServices(t *testing.T) {
	fixture := newWorkerFixture(t)
	application, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n")
	runner := &recordingRunner{}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	runNext := func() {
		t.Helper()
		if err := worker.claimAndRun(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	runNext() // INSTALL
	services, err := fixture.store.ListServicesFiltered(context.Background(), storage.ServiceFilter{ApplicationID: application.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("managed services = %#v, %v", services, err)
	}

	if _, err = fixture.core.QueueApplicationOperation(context.Background(), application.ID, "stop", "stop-1"); err != nil {
		t.Fatal(err)
	}
	runNext()
	commandsAfterStop := len(runner.calls)
	if _, err = fixture.core.QueueApplicationOperation(context.Background(), application.ID, "stop", "stop-2"); err != nil {
		t.Fatal(err)
	}
	runNext()
	if len(runner.calls) != commandsAfterStop {
		t.Fatalf("repeated STOP called Docker: %#v", runner.calls[commandsAfterStop:])
	}

	if _, err = fixture.core.QueueApplicationOperation(context.Background(), application.ID, "down", "down-1"); err != nil {
		t.Fatal(err)
	}
	runNext()
	commandsAfterDown := len(runner.calls)
	if _, err = fixture.core.QueueApplicationOperation(context.Background(), application.ID, "down", "down-2"); err != nil {
		t.Fatal(err)
	}
	runNext()
	if _, err = fixture.core.QueueApplicationOperation(context.Background(), application.ID, "stop", "stop-while-down"); err != nil {
		t.Fatal(err)
	}
	runNext()
	if len(runner.calls) != commandsAfterDown {
		t.Fatalf("already-DOWN operations called Docker: %#v", runner.calls[commandsAfterDown:])
	}
	stored, err := fixture.store.GetApplication(context.Background(), application.ID)
	if err != nil || stored.Status != model.ApplicationDown {
		t.Fatalf("STOP changed DOWN lifecycle state: %#v, %v", stored, err)
	}

	if _, err = fixture.core.QueueServiceOperation(context.Background(), services[0].ID, "scale", 4, "scale-while-down"); err == nil {
		t.Fatal("temporary scale was accepted while application was down")
	}
	if len(runner.calls) != commandsAfterDown {
		t.Fatal("scaling a down application unexpectedly called Docker")
	}
	stored, err = fixture.store.GetApplication(context.Background(), application.ID)
	if err != nil || len(stored.ReplicaOverrides) != 0 {
		t.Fatalf("down-state temporary scale changed saved overrides: %#v, %v", stored.ReplicaOverrides, err)
	}
	restart, err := fixture.core.QueueServiceOperation(context.Background(), services[0].ID, "restart", 0, "restart-while-down")
	if err != nil {
		t.Fatal(err)
	}
	runNext()
	storedRestart, err := fixture.store.GetDeploymentOperation(context.Background(), restart.ID)
	if err != nil || storedRestart.Status != model.OperationFailed || len(runner.calls) != commandsAfterDown {
		t.Fatalf("restart implicitly started a down application: operation=%#v calls=%d err=%v", storedRestart, len(runner.calls), err)
	}

	if _, err = fixture.core.QueueApplicationOperation(context.Background(), application.ID, "start", "start-1"); err != nil {
		t.Fatal(err)
	}
	runNext()
	if len(runner.calls) != commandsAfterDown+1 || strings.Contains(strings.Join(runner.calls[len(runner.calls)-1].arguments, " "), "--scale web=4") {
		t.Fatalf("START applied an unsaved replica override: %#v", runner.calls)
	}
	commandsAfterStart := len(runner.calls)
	if _, err = fixture.core.QueueApplicationOperation(context.Background(), application.ID, "start", "start-2"); err != nil {
		t.Fatal(err)
	}
	runNext()
	if len(runner.calls) != commandsAfterStart {
		t.Fatalf("repeated START called Docker: %#v", runner.calls[commandsAfterStart:])
	}
}

func TestSwarmManagedOverrideIncludesSavedReplicas(t *testing.T) {
	fixture := newWorkerFixture(t)
	application, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n")
	application.ReplicaOverrides = map[string]int{"web": 4}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), &recordingRunner{})
	runtime := fixture.runtime
	runtime.Type = model.RuntimeSwarm
	override, err := worker.networkOverride(context.Background(), application, runtime, []applicationx.ServiceSpec{{Name: "web"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(override, "replicas: 4") || !strings.Contains(override, `"web"`) || strings.Contains(override, `"demo_web"`) {
		t.Fatalf("Swarm override did not retain replicas: %q", override)
	}
}

func TestApplicationServiceNameNormalizesSwarmRuntimeIdentity(t *testing.T) {
	if got := applicationServiceName(model.RuntimeSwarm, "demo", "demo_web"); got != "web" {
		t.Fatalf("Swarm definition service name = %q, want web", got)
	}
	if got := applicationServiceName(model.RuntimeDocker, "demo", "demo/web"); got != "web" {
		t.Fatalf("Compose definition service name = %q, want web", got)
	}
}

func TestSwarmConvergenceRequiresNewServiceVersionAndCompletedUpdate(t *testing.T) {
	baseline := map[string]swarmServiceState{
		"demo_web": {name: "demo_web", version: 10, replicas: "2/2", updateState: "completed"},
	}

	changed, ready, err := evaluateSwarmConvergence(baseline, baseline)
	if err != nil || changed || !ready {
		t.Fatalf("unchanged healthy baseline = changed %v ready %v err %v", changed, ready, err)
	}

	updating := map[string]swarmServiceState{
		"demo_web": {name: "demo_web", version: 11, replicas: "2/2", updateState: "updating"},
	}
	changed, ready, err = evaluateSwarmConvergence(baseline, updating)
	if err != nil || !changed || ready {
		t.Fatalf("in-progress update = changed %v ready %v err %v", changed, ready, err)
	}

	paused := map[string]swarmServiceState{
		"demo_web": {name: "demo_web", version: 11, replicas: "2/2", updateState: "paused"},
	}
	if _, _, err = evaluateSwarmConvergence(baseline, paused); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("paused update was accepted: %v", err)
	}

	completed := map[string]swarmServiceState{
		"demo_web": {name: "demo_web", version: 11, replicas: "2/2", updateState: "completed"},
	}
	changed, ready, err = evaluateSwarmConvergence(baseline, completed)
	if err != nil || !changed || !ready {
		t.Fatalf("completed update = changed %v ready %v err %v", changed, ready, err)
	}
}

func TestSwarmConvergenceAcceptsNewServiceAndRejectsIncompleteReplicas(t *testing.T) {
	current := map[string]swarmServiceState{
		"demo_web": {name: "demo_web", version: 1, replicas: "1/1"},
	}
	changed, ready, err := evaluateSwarmConvergence(nil, current)
	if err != nil || !changed || !ready {
		t.Fatalf("new service = changed %v ready %v err %v", changed, ready, err)
	}
	current["demo_web"] = swarmServiceState{name: "demo_web", version: 1, replicas: "0/1"}
	changed, ready, err = evaluateSwarmConvergence(nil, current)
	if err != nil || !changed || ready {
		t.Fatalf("incomplete new service = changed %v ready %v err %v", changed, ready, err)
	}
}

func TestWorkerRevalidatesStoredRevisionForRuntimeBackend(t *testing.T) {
	fixture := newWorkerFixture(t)
	application, operation := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n    pull_policy: always\n")
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), &recordingRunner{})
	runtime := fixture.runtime
	runtime.Type = model.RuntimeSwarm
	if _, err := worker.loadMaterial(context.Background(), runtime, application, operation.RevisionID); err == nil || !strings.Contains(err.Error(), "not Swarm compatible") {
		t.Fatalf("stored Compose-only revision was accepted for Swarm: %v", err)
	}
}

func TestWorkerRevalidationPreservesContainerRuntimeVariables(t *testing.T) {
	fixture := newWorkerFixture(t)
	application, operation := installFixtureApplication(t, fixture, `services:
  web:
    image: busybox:1.37
    command: ["sh", "-c", "echo $${RUNTIME_REQUIRED:?set inside container}"]
`)
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), &recordingRunner{})
	material, err := worker.loadMaterial(context.Background(), fixture.runtime, application, operation.RevisionID)
	if err != nil {
		t.Fatalf("execution-time validation interpolated a container runtime variable twice: %v", err)
	}
	if !strings.Contains(material.resolved, "${RUNTIME_REQUIRED:?set inside container}") {
		t.Fatalf("resolved Compose lost the container runtime expression: %q", material.resolved)
	}
}

func TestFailedDomainAttachmentKeepsApplicationRunningAndRetryIntent(t *testing.T) {
	fixture := newWorkerFixture(t)
	application, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n    expose: [\"80\"]\n")
	successWorker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), &recordingRunner{})
	if err := successWorker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	services, err := fixture.store.ListServicesFiltered(context.Background(), storage.ServiceFilter{ApplicationID: application.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("managed services = %#v, %v", services, err)
	}
	binding, operation, err := fixture.core.CreateDomainBinding(context.Background(), services[0].ID, core.CreateDomainBindingInput{
		Domain: &core.DomainConfigInput{Host: "retry.example.com", EntryPoints: []string{core.EntryPointHTTP}, TLS: core.DomainTLSInput{Mode: model.TLSDisabled}},
		Route:  core.RouteConfigInput{Path: "/", Backend: core.RouteBackendInput{Port: 80, Protocol: "http"}}, IdempotencyKey: "attach-retry",
	})
	if err != nil || operation == nil || binding.Route.Status != model.RoutePending {
		t.Fatalf("pending binding = %#v, operation=%#v, err=%v", binding, operation, err)
	}
	failingRunner := &recordingRunner{run: func(runnerCall) (string, error) { return "failed output", errors.New("network attach failed") }}
	failingWorker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), failingRunner)
	if err = failingWorker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	storedOperation, err := fixture.store.GetDeploymentOperation(context.Background(), operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedApplication, err := fixture.store.GetApplication(context.Background(), application.ID)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := fixture.store.ListServiceDomainBindings(context.Background(), services[0].ID)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings = %#v, %v", bindings, err)
	}
	if storedOperation.Status != model.OperationFailed || storedApplication.Status != model.ApplicationRunning || bindings[0].Route.Status != model.RouteError || bindings[0].Route.Enabled {
		t.Fatalf("failed attachment state: operation=%#v application=%#v binding=%#v", storedOperation, storedApplication, bindings[0])
	}
	override, err := failingWorker.networkOverride(context.Background(), storedApplication, fixture.runtime, []applicationx.ServiceSpec{{Name: "web"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(override, "nsc_backend") || !strings.Contains(override, `"web"`) {
		t.Fatalf("failed binding lost retry network intent: %q", override)
	}
}

func TestExternalBindingUsesExplicitPortForAmbiguousDiscovery(t *testing.T) {
	fixture := newWorkerFixture(t)
	now := time.Now().UTC()
	service := model.Service{ID: "svc-external-port", RuntimeID: fixture.runtime.ID, ServiceKey: "external/web", RuntimeObjectID: "container-external", ExternalID: "external/web",
		Name: "web", Namespace: "external", QualifiedName: "external_web", DNSName: "external_web", DNSStatus: model.DNSReady, DiscoveredProtocol: "http", DiscoveredEnabled: false, RoutingEligible: false,
		RoutingIssues: []string{"nsc.port is required when the container does not expose exactly one TCP port"},
		Status:        model.ServiceUnavailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := fixture.store.UpsertService(context.Background(), &service); err != nil {
		t.Fatal(err)
	}
	binding, operation, err := fixture.core.CreateDomainBinding(context.Background(), service.ID, core.CreateDomainBindingInput{
		Domain: &core.DomainConfigInput{Host: "external.example.com", EntryPoints: []string{core.EntryPointHTTP}, TLS: core.DomainTLSInput{Mode: model.TLSDisabled}},
		Route:  core.RouteConfigInput{Path: "/", Backend: core.RouteBackendInput{Port: 8080, Protocol: "http"}},
	})
	if err != nil || operation != nil || binding.Route.TargetPort != 8080 {
		t.Fatalf("explicit external port binding = %#v, operation=%#v, err=%v", binding, operation, err)
	}
	stored, err := fixture.store.GetService(context.Background(), service.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.TargetPortOverride == nil || *stored.TargetPortOverride != 8080 || stored.EnabledOverride == nil || !*stored.EnabledOverride {
		t.Fatalf("explicit routing port was not persisted: %#v", stored)
	}

	blocked := service
	blocked.ID = "svc-external-network"
	blocked.ServiceKey = "external/blocked"
	blocked.ExternalID = "external/blocked"
	blocked.DNSStatus = model.DNSMissing
	blocked.DNSLastError = "project-qualified alias is missing"
	blocked.RoutingIssues = []string{`container is not attached to network "nsc-ingress"`}
	if err = fixture.store.UpsertService(context.Background(), &blocked); err != nil {
		t.Fatal(err)
	}
	if _, _, err = fixture.core.CreateDomainBinding(context.Background(), blocked.ID, core.CreateDomainBindingInput{Domain: &core.DomainConfigInput{Host: "blocked.example.com", EntryPoints: []string{core.EntryPointHTTP}, TLS: core.DomainTLSInput{Mode: model.TLSDisabled}}, Route: core.RouteConfigInput{Path: "/", Backend: core.RouteBackendInput{Port: 8080, Protocol: "http"}}}); err == nil || !strings.Contains(err.Error(), "DNS is") {
		t.Fatalf("network-ineligible external service was bound: %v", err)
	}
}

func TestAdoptionRequiresAnExactDiscoveredProjectAndDoesNotRunDocker(t *testing.T) {
	fixture := newWorkerFixture(t)
	raw := ""
	input := core.InstallApplicationInput{ApplicationPreflightInput: core.ApplicationPreflightInput{
		RuntimeID: fixture.runtime.ID, ComposeYAML: "services:\n  web:\n    image: nginx:1.29-alpine\n", Environment: applicationx.EnvironmentInput{Raw: &raw}},
		Name: "Adopted", ProjectName: "existing", Adopt: true, Actor: "test"}
	if _, _, _, err := fixture.core.InstallApplication(context.Background(), input); err == nil || !strings.Contains(err.Error(), "must already exist") {
		t.Fatalf("missing project was adopted: %v", err)
	}
	now := time.Now().UTC()
	service := model.Service{ID: "svc-existing", RuntimeID: fixture.runtime.ID, ServiceKey: "existing/web", RuntimeObjectID: "container-existing", ExternalID: "container-existing",
		Name: "web", Namespace: "existing", DiscoveredProtocol: "http", DiscoveredPort: 80, DiscoveredEnabled: true, RoutingEligible: true,
		Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := fixture.store.UpsertService(context.Background(), &service); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := fixture.core.InstallApplication(context.Background(), core.InstallApplicationInput{ApplicationPreflightInput: input.ApplicationPreflightInput,
		Name: "Collision", ProjectName: "existing", Actor: "test"}); err == nil || !strings.Contains(err.Error(), "explicit adoption") {
		t.Fatalf("install overwrote an existing external project: %v", err)
	}
	managedApplication, _, _, err := fixture.core.InstallApplication(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("adoption unexpectedly changed Docker: %#v", runner.calls)
	}
	storedService, err := fixture.store.GetService(context.Background(), service.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !storedService.Managed || storedService.ApplicationID != managedApplication.ID || storedService.RuntimeObjectID != "container-existing" {
		t.Fatalf("discovered service was not adopted in place: %#v", storedService)
	}
}

func TestRedactLogCoversShortSecrets(t *testing.T) {
	value := redactLog("command failed: PIN=123", map[string]string{"PIN": "123"}, []string{"PIN"})
	if strings.Contains(value, "123") || !strings.Contains(value, "[redacted]") {
		t.Fatalf("short secret leaked: %q", value)
	}
}
