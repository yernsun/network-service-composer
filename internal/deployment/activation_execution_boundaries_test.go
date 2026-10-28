package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type activationExecutionStore struct {
	storage.Store
	application  *model.Application
	services     []model.Service
	servicesSet  bool
	failureStage string
	failure      error
	placeholders []model.Service
	activated    []string
	replicaName  string
	replicaCount int
	status       model.ApplicationStatus
}

func (s *activationExecutionStore) GetApplication(ctx context.Context, key string) (model.Application, error) {
	if s.failureStage == "application" {
		return model.Application{}, s.failure
	}
	if s.application != nil {
		return *s.application, nil
	}
	return s.Store.GetApplication(ctx, key)
}
func (s *activationExecutionStore) ListAllServicesFiltered(ctx context.Context, f storage.ServiceFilter) ([]model.Service, error) {
	if s.failureStage == "services" {
		return nil, s.failure
	}
	if s.servicesSet {
		return s.services, nil
	}
	return s.Store.ListAllServicesFiltered(ctx, f)
}
func (s *activationExecutionStore) UpsertService(_ context.Context, v *model.Service) error {
	if s.failureStage == "placeholder" {
		return s.failure
	}
	s.placeholders = append(s.placeholders, *v)
	return nil
}
func (s *activationExecutionStore) ActivateApplicationRevision(_ context.Context, runtime, app, active, completed string, keys []string, overrides map[string]int, message string, now time.Time) error {
	if s.failureStage == "activation" {
		return s.failure
	}
	s.activated = append([]string(nil), keys...)
	return nil
}
func (s *activationExecutionStore) SetApplicationReplicaOverride(_ context.Context, app, name string, replicas int, now time.Time) error {
	if s.failureStage == "replicas" {
		return s.failure
	}
	s.replicaName = name
	s.replicaCount = replicas
	return nil
}
func (s *activationExecutionStore) SetApplicationStatus(_ context.Context, app string, status model.ApplicationStatus, message string, now time.Time) error {
	s.status = status
	return nil
}
func TestActivationAndAdoptionRevalidateDiscoveredOwnership(t *testing.T) {
	for _, runtimeType := range []model.RuntimeType{model.RuntimeDocker, model.RuntimeSwarm} {
		for _, mode := range []string{"new placeholder", "placeholder failed", "inventory failed", "existing preserved", "adopt external", "adopt same owner", "adopt missing", "adopt removed", "adopt foreign", "adopt inventory failed"} {
			t.Run(string(runtimeType)+"/"+mode, func(t *testing.T) {
				f := newWorkerFixture(t)
				failure := errors.New("activation unavailable")
				runtime := f.runtime
				runtime.Type = runtimeType
				app := model.Application{ID: "app", ProjectName: "project", ReplicaOverrides: map[string]int{"web": 2, "obsolete": 3}}
				key := "project/web"
				if runtimeType == model.RuntimeSwarm {
					key = "project_web"
				}
				store := &activationExecutionStore{Store: f.store, servicesSet: true, failure: failure}
				service := model.Service{ID: "discovered", ServiceKey: key, RuntimeID: runtime.ID, RuntimeObjectID: "retained", Status: model.ServiceAvailable, RoutingEligible: true, ReadyInstances: 2}
				expectedFailure := false
				switch mode {
				case "placeholder failed":
					store.failureStage = "placeholder"
					expectedFailure = true
				case "inventory failed", "adopt inventory failed":
					store.failureStage = "services"
					expectedFailure = true
				case "existing preserved", "adopt external":
					store.services = []model.Service{service}
				case "adopt same owner":
					service.Managed = true
					service.ApplicationID = app.ID
					store.services = []model.Service{service}
				case "adopt missing":
					expectedFailure = true
				case "adopt removed":
					service.Status = model.ServiceRemoved
					store.services = []model.Service{service}
					expectedFailure = true
				case "adopt foreign":
					service.Managed = true
					service.ApplicationID = "other"
					store.services = []model.Service{service}
					expectedFailure = true
				}
				runner := &recordingRunner{}
				w := NewWorkerWithRunner(store, nil, f.keyring, f.config, nil, runner)
				var e error
				if strings.HasPrefix(mode, "adopt") {
					e = w.adoptApplication(context.Background(), runtime, app, "revision", []string{"web"})
				} else {
					e = w.activateApplication(context.Background(), runtime, app, "revision", []applicationx.ServiceSpec{{Name: "web", TargetPort: 8080}})
				}
				if (e != nil) != expectedFailure {
					t.Fatal("activation ownership result", e)
				}
				if expectedFailure && len(store.activated) != 0 || !expectedFailure && (len(store.activated) != 1 || store.activated[0] != key) {
					t.Fatal("activation escaped ownership or lost stable key", store.activated)
				}
				if store.failureStage != "" && !errors.Is(e, failure) {
					t.Fatal("activation failure identity lost", e)
				}
				if mode == "new placeholder" {
					if len(store.placeholders) != 1 {
						t.Fatal("missing service not represented")
					}
					p := store.placeholders[0]
					if p.ServiceKey != key || p.ApplicationID != app.ID || !p.Managed || p.RoutingEligible || p.ReadyInstances != 0 || p.DNSStatus != model.DNSMissing || p.Status != model.ServiceUnavailable {
						t.Fatal("placeholder claimed discovery or readiness", p)
					}
				} else if len(store.placeholders) != 0 {
					t.Fatal("existing or unauthorized service overwritten")
				}
				if len(runner.calls) != 0 {
					t.Fatal("adoption or activation unexpectedly executed Docker")
				}
			})
		}
	}
}
func TestExecutionLifecyclePreservesStoppedStateAndIndependentFailures(t *testing.T) {
	for _, mode := range []string{"application unavailable", "scale stopped", "scale down", "scale invalid state", "scale command failed", "restart command", "stop failed", "swarm already stopped", "active revision fallback", "missing attach route", "activation failed"} {
		t.Run(mode, func(t *testing.T) {
			f := newWorkerFixture(t)
			app, op := installFixtureApplication(t, f, "services:\n  web:\n    image: example.test/web:fixed\n")
			app.ActiveRevisionID = op.RevisionID
			app.Status = model.ApplicationRunning
			failure := errors.New("lifecycle dependency unavailable")
			store := &activationExecutionStore{Store: f.store, application: &app, failure: failure}
			runner := &recordingRunner{}
			input := operationInput{ServiceName: "web", Replicas: 3}
			op.Type = "SCALE"
			runtime := f.runtime
			wantFailure := false
			switch mode {
			case "application unavailable":
				store.failureStage = "application"
				wantFailure = true
			case "scale stopped":
				app.Status = model.ApplicationStopped
				wantFailure = true
			case "scale down":
				app.Status = model.ApplicationDown
				wantFailure = true
			case "scale invalid state":
				app.Status = model.ApplicationFailed
				wantFailure = true
			case "scale command failed":
				runner.run = func(runnerCall) (string, error) { return "failed", failure }
				wantFailure = true
			case "restart command":
				op.Type = "RESTART"
			case "stop failed":
				op.Type = "STOP"
				runner.run = func(runnerCall) (string, error) { return "failed", failure }
				wantFailure = true
			case "swarm already stopped":
				op.Type = "DOWN"
				app.Status = model.ApplicationStopped
				runtime.Type = model.RuntimeSwarm
			case "active revision fallback":
				op.Type = "REDEPLOY"
				op.RevisionID = ""
				app.DesiredRevisionID = ""
			case "missing attach route":
				op.Type = "DOMAIN_ATTACH"
				wantFailure = true
			case "activation failed":
				op.Type = "REDEPLOY"
				store.failureStage = "activation"
				wantFailure = true
			}
			raw, e := json.Marshal(input)
			if e != nil {
				t.Fatal(e)
			}
			op.EncryptedInput, e = f.keyring.Encrypt(raw, []byte("deployment-operation:"+op.ID))
			if e != nil {
				t.Fatal(e)
			}
			w := NewWorkerWithRunner(store, nil, f.keyring, f.config, nil, runner)
			if mode == "swarm already stopped" {
				w.lifecycle = &stopRuntimeFixture{}
			}
			e = w.execute(context.Background(), runtime, &op)
			if (e != nil) != wantFailure {
				t.Fatal("lifecycle result mismatch", e)
			}
			if store.failureStage != "" && !errors.Is(e, failure) {
				t.Fatal("dependency error replaced", e)
			}
			if mode == "scale stopped" || mode == "scale down" {
				if store.replicaName != "" || len(runner.calls) != 0 {
					t.Fatal("temporary scale started a stopped workload or saved an override")
				}
			}
			if mode == "swarm already stopped" && (store.status != model.ApplicationDown || len(runner.calls) != 0) {
				t.Fatal("verified absent workloads did not record DOWN without stack removal")
			}
			if mode == "restart command" && (len(runner.calls) != 1 || !strings.Contains(strings.Join(runner.calls[0].arguments, " "), "restart web")) {
				t.Fatal("restart targeted wrong service")
			}
			if wantFailure && len(store.activated) != 0 {
				t.Fatal("failed lifecycle marked application active")
			}
		})
	}
}
