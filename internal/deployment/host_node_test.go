package deployment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestHostAdoptionRetainsObservedIngressWithoutRoutes(t *testing.T) {
	for _, backend := range []model.RuntimeType{model.RuntimeDocker, model.RuntimeSwarm} {
		for _, scenario := range []string{"attached", "unverified", "removed from revision"} {
			t.Run(string(backend)+"/"+scenario, func(t *testing.T) {
				f := newWorkerFixture(t)
				ctx := context.Background()
				runtime := f.runtime
				now := time.Now().UTC()
				key := "existing/web"
				service := model.Service{ID: "svc-ingress", RuntimeID: runtime.ID, ServiceKey: key, Name: "web", Namespace: "existing", DiscoveredProtocol: "http", DNSStatus: model.DNSReady, RoutingEligible: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
				if scenario == "unverified" {
					service.DNSStatus = model.DNSMissing
					service.RoutingEligible = false
				}
				if err := f.store.UpsertService(ctx, &service); err != nil {
					t.Fatal(err)
				}
				app, _, _, err := f.core.InstallApplication(ctx, core.InstallApplicationInput{ApplicationPreflightInput: core.ApplicationPreflightInput{RuntimeID: runtime.ID, ComposeYAML: "services:\n  web:\n    image: nginx:alpine\n"}, Name: "Existing", ProjectName: "existing", Adopt: true})
				if err != nil {
					t.Fatal(err)
				}
				worker := NewWorkerWithRunner(f.store, lease.New(f.store.DB(), f.store.Dialect()), f.keyring, f.config, nil, &recordingRunner{})
				if err := worker.claimAndRun(ctx); err != nil {
					t.Fatal(err)
				}
				if backend == model.RuntimeSwarm {
					// SQLite cannot provision Swarm runtimes. Exercise the Swarm
					// material renderer using the same adopted service fixture.
					stored, err := f.store.GetService(ctx, service.ID)
					if err != nil {
						t.Fatal(err)
					}
					stored.ServiceKey = "existing_web"
					worker.store = &activationExecutionStore{Store: f.store, servicesSet: true, services: []model.Service{stored}}
					runtime.Type = backend
				}
				specs := []applicationx.ServiceSpec{{Name: "web"}}
				if scenario == "removed from revision" {
					specs = nil
				}
				override, err := worker.networkOverride(ctx, app, runtime, specs)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "attached" {
					if !strings.Contains(override, "nsc-ingress") || !strings.Contains(override, `"web"`) || backend == model.RuntimeDocker && !strings.Contains(override, "existing_web") {
						t.Fatal("adopted service lost its ingress", override)
					}
				} else if override != "" {
					t.Fatal("unverified or removed service gained network access", override)
				}
			})
		}
	}
}

func TestNodeExecutionRequiresLocalProvisionedAndLiveIdentity(t *testing.T) {
	for _, scenario := range []string{"local", "foreign controller", "foreign engine", "foreign cluster", "foreign node", "unavailable", "invalid response", "credential override"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Controller.InstanceID = "controller-one"
			cfg.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: "controller-one", NodeID: "node-one", EngineID: "engine-one", ClusterID: "cluster-one"}}
			runtime := model.Runtime{Endpoint: "node://node-one"}
			response := `{"ID":"engine-one","Swarm":{"NodeID":"node-one","Cluster":{"ID":"cluster-one"}}}`
			if scenario == "foreign controller" {
				cfg.Controller.InstanceID = "controller-two"
			}
			if scenario == "foreign engine" {
				response = strings.ReplaceAll(response, "engine-one", "engine-two")
			}
			if scenario == "foreign cluster" {
				response = strings.ReplaceAll(response, "cluster-one", "cluster-two")
			}
			if scenario == "foreign node" {
				response = strings.ReplaceAll(response, "node-one", "node-two")
			}
			if scenario == "invalid response" {
				response = "broken"
			}
			if scenario == "credential override" {
				runtime.CredentialRef = "foreign"
			}
			runner := &recordingRunner{run: func(call runnerCall) (string, error) {
				if len(call.environment) != 0 || strings.Join(call.arguments[1:], " ") != "--host unix:///var/run/docker.sock info --format {{json .}}" {
					t.Fatal("unexpected command while verifying host", call.arguments)
				}
				if scenario == "unavailable" {
					return "sensitive diagnostic", errors.New("offline")
				}
				return response, nil
			}}
			worker := NewWorkerWithRunner(nil, nil, nil, cfg, nil, runner)
			err := worker.verifyExecutionNode(context.Background(), runtime)
			if (err == nil) != (scenario == "local") {
				t.Fatal("wrong identity result", err)
			}
			if err != nil && strings.Contains(err.Error(), "sensitive") {
				t.Fatal("raw Docker diagnostic leaked")
			}
			if (scenario == "foreign controller" || scenario == "credential override") && len(runner.calls) != 0 {
				t.Fatal("foreign host reached Docker")
			}
			if scenario == "local" && worker.dockerArgs(runtime)[2] != localDockerEndpoint {
				t.Fatal("node URI passed to Docker CLI")
			}
		})
	}
	worker := NewWorkerWithRunner(nil, nil, nil, config.Defaults(), nil, &recordingRunner{})
	if err := worker.verifyExecutionNode(context.Background(), model.Runtime{Endpoint: "unix:///private-fixture.sock"}); err != nil {
		t.Fatal("legacy endpoint regressed", err)
	}
}

func TestHostAdoptionPreservesObjectsAndRechecksRuntimeBinding(t *testing.T) {
	for _, drift := range []bool{false, true} {
		f, w, runner, _ := nodeWorkerFixture(t)
		ctx := context.Background()
		runtime := f.runtime
		now := time.Now().UTC()
		service := model.Service{ID: "svc-existing", RuntimeID: runtime.ID, ServiceKey: "existing/web", RuntimeObjectID: "container-preserved", ExternalID: "container-preserved", Name: "web", Namespace: "existing", DiscoveredProtocol: "http", Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := f.store.UpsertService(ctx, &service); err != nil {
			t.Fatal(err)
		}
		source := "services:\n  web:\n    image: nginx:alpine\n    volumes: ['/srv/app/data:/data:ro']\n    deploy:\n      placement:\n        constraints: ['node.id == original-node']\n"
		app, operation, _, err := f.core.InstallApplication(ctx, core.InstallApplicationInput{ApplicationPreflightInput: core.ApplicationPreflightInput{RuntimeID: runtime.ID, ComposeYAML: source}, Name: "Existing", ProjectName: "existing", Adopt: true})
		if err != nil {
			t.Fatal(err)
		}
		if drift {
			current, err := f.store.GetRuntime(ctx, runtime.ID)
			if err != nil {
				t.Fatal(err)
			}
			current.Endpoint = "node://node-two"
			if err := f.store.UpdateRuntime(ctx, &current, current.Revision); !errors.Is(err, storage.ErrConflict) {
				t.Fatal("runtime with a pending operation was rebound", err)
			}
			// Simulate restored or externally corrupted metadata: execution
			// still rejects drift even when ordinary admission was bypassed.
			if _, err := f.store.DB().ExecContext(ctx, `UPDATE runtimes SET endpoint=? WHERE id=?`, current.Endpoint, current.ID); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.claimAndRun(ctx); err != nil {
			t.Fatal(err)
		}
		w.WaitExecutors()
		stored, err := f.store.GetService(ctx, service.ID)
		if err != nil || stored.RuntimeObjectID != service.RuntimeObjectID || len(runner.calls) != 0 {
			t.Fatal("metadata adoption changed workload", err)
		}
		finished, err := f.store.GetDeploymentOperation(ctx, operation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if drift {
			if finished.Status != model.OperationRunning || stored.Managed {
				t.Fatal("runtime drift adopted a foreign mount")
			}
		} else if finished.Status != model.OperationSucceeded || !stored.Managed || stored.ApplicationID != app.ID {
			t.Fatal("bound adoption failed", finished.Status)
		}
	}
}

func TestForeignHostFailureNeverTriggersInstallCleanup(t *testing.T) {
	f := newWorkerFixture(t)
	ctx := context.Background()
	runtime := f.runtime
	runtime.Endpoint = "node://node-one"
	if err := f.store.UpdateRuntime(ctx, &runtime, runtime.Revision); err != nil {
		t.Fatal(err)
	}
	f.store.ConfigureOperationNodes(map[string]model.RuntimeExecutionIdentity{runtime.Endpoint: {NodeID: "node-one", EngineID: "engine-one"}})
	_, operation := installFixtureApplication(t, f, "services:\n  web:\n    image: nginx:alpine\n")
	runner := &recordingRunner{}
	w := NewWorkerWithRunner(f.store, lease.New(f.store.DB(), f.store.Dialect()), f.keyring, f.config, nil, runner)
	if err := w.claimAndRun(ctx); err != nil {
		t.Fatal(err)
	}
	finished, err := f.store.GetDeploymentOperation(ctx, operation.ID)
	if err != nil || finished.Status != model.OperationRunning || finished.ErrorCode != "" || len(runner.calls) != 0 {
		t.Fatal("foreign host failure invoked cleanup or deployment", err)
	}
	record, err := execution.Load(ctx, f.store, operation.ID, finished.Attempt)
	if err != nil || record.State != "WAITING" || record.Reason != "verified_executor_unavailable" {
		t.Fatal("unenrolled Controller did not retain a concrete waiting reason", record.State, err)
	}
}
