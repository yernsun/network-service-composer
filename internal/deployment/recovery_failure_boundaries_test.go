package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

type recoveryFailureStore struct {
	*materialBoundaryStore
}

func (s *recoveryFailureStore) GetApplication(ctx context.Context, key string) (model.Application, error) {
	if s.stage == "application" {
		return model.Application{}, s.failure
	}
	return s.Store.GetApplication(ctx, key)
}
func (s *recoveryFailureStore) SetApplicationStatus(ctx context.Context, key string, status model.ApplicationStatus, message string, now time.Time) error {
	if s.stage == "status" || s.stage == "status and routes" {
		return s.failure
	}
	return s.Store.SetApplicationStatus(ctx, key, status, message, now)
}
func (s *recoveryFailureStore) SetApplicationRouteStatus(ctx context.Context, key string, status model.RouteStatus, message string, now time.Time) error {
	if s.stage == "status and routes" {
		return s.failure
	}
	return s.Store.SetApplicationRouteStatus(ctx, key, status, message, now)
}
func (s *recoveryFailureStore) SetApplicationRouteStatusByID(ctx context.Context, app, key string, status model.RouteStatus, message string, now time.Time) error {
	if s.stage == "route" {
		return s.failure
	}
	return s.Store.SetApplicationRouteStatusByID(ctx, app, key, status, message, now)
}
func (s *recoveryFailureStore) ActivateApplicationRevision(ctx context.Context, runtime, app, active, completed string, keys []string, overrides map[string]int, message string, now time.Time) error {
	if s.stage == "activation" {
		return s.failure
	}
	return s.Store.ActivateApplicationRevision(ctx, runtime, app, active, completed, keys, overrides, message, now)
}
func (s *recoveryFailureStore) FinishDeploymentOperation(ctx context.Context, op *model.DeploymentOperation, now time.Time) error {
	if s.stage == "finish" {
		return s.failure
	}
	return s.Store.FinishDeploymentOperation(ctx, op, now)
}

func TestDeploymentRecoveryFailuresPreserveIndependentResults(t *testing.T) {
	for _, stage := range []string{"application", "restart", "domain detach", "domain input corrupt", "domain result persistence", "rollback command", "rollback activation", "rollback material", "install cleanup", "status and routes", "same revision status", "finish"} {
		t.Run(stage, func(t *testing.T) {
			f := newWorkerFixture(t)
			a, initial := installFixtureApplication(t, f, "services:\n  web:\n    image: example.test/web:fixed\n")
			ctx := context.Background()
			active := stage != "install cleanup" && stage != "status and routes"
			if active {
				a.ActiveRevisionID = initial.RevisionID
				a.Status = model.ApplicationRunning
				if e := f.store.UpdateApplication(ctx, &a, a.Revision); e != nil {
					t.Fatal(e)
				}
			}
			op, e := f.store.ClaimDeploymentOperation(ctx, "test-worker", time.Now(), time.Now().Add(-time.Hour))
			if e != nil {
				t.Fatal(e)
			}
			failure := errors.New("recovery persistence unavailable")
			s := &recoveryFailureStore{materialBoundaryStore: &materialBoundaryStore{Store: f.store, failure: failure}}
			runner := &recordingRunner{}
			w := NewWorkerWithRunner(s, nil, f.keyring, f.config, nil, runner)
			op.Type = "UPGRADE"
			op.RevisionID = "candidate"
			want := model.OperationFailed
			fragment := ""
			switch stage {
			case "application":
				s.stage = "application"
			case "restart":
				op.Type = "RESTART"
			case "domain detach":
				op.Type = "DOMAIN_DETACH"
			case "domain input corrupt":
				op.Type = "DOMAIN_ATTACH"
				op.EncryptedInput = []byte("bad")
			case "domain result persistence":
				op.Type = "DOMAIN_ATTACH"
				raw, _ := json.Marshal(operationInput{RouteID: "route"})
				op.EncryptedInput, e = f.keyring.Encrypt(raw, []byte("deployment-operation:"+op.ID))
				if e != nil {
					t.Fatal(e)
				}
				s.stage = "route"
				fragment = "persist binding failure"
			case "rollback command":
				runner.run = func(runnerCall) (string, error) { return "rollback failed", errors.New("old image unavailable") }
				want = model.OperationRollbackFailed
				fragment = "rollback:"
			case "rollback activation":
				s.stage = "activation"
				want = model.OperationRollbackFailed
				fragment = "rollback:"
			case "rollback material":
				s.stage = "revision"
				want = model.OperationRollbackFailed
				fragment = "load rollback revision"
			case "install cleanup":
				op.Type = "INSTALL"
				op.RevisionID = initial.RevisionID
				runner.run = func(runnerCall) (string, error) { return "cleanup failed", errors.New("cleanup unavailable") }
				fragment = "cleanup:"
			case "status and routes":
				op.Type = "INSTALL"
				op.RevisionID = initial.RevisionID
				s.stage = "status and routes"
				fragment = "persist route failure"
			case "same revision status":
				op.Type = "REDEPLOY"
				op.RevisionID = initial.RevisionID
				s.stage = "status"
				fragment = "persist application failure"
			case "finish":
				op.Type = "RESTART"
				s.stage = "finish"
			}
			original := errors.New("candidate failed")
			e = w.handleExecutionFailure(ctx, f.runtime, &op, original)
			if stage == "finish" {
				if !errors.Is(e, failure) {
					t.Fatal("finish failure lost", e)
				}
				stored, e := f.store.GetDeploymentOperation(ctx, op.ID)
				if e != nil || stored.Status != model.OperationRunning {
					t.Fatal("unpersisted result reported complete", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			stored, e := f.store.GetDeploymentOperation(ctx, op.ID)
			if e != nil || stored.Status != want || stored.ErrorCode != "deployment_failed" || !strings.Contains(stored.ErrorMessage, original.Error()) || fragment != "" && !strings.Contains(stored.ErrorMessage, fragment) {
				t.Fatalf("failure evidence lost: %+v %v", stored, e)
			}
			current, e := f.store.GetApplication(ctx, a.ID)
			if e != nil {
				t.Fatal(e)
			}
			if strings.HasPrefix(stage, "domain") || stage == "restart" || stage == "same revision status" || stage == "application" {
				if current.Status != model.ApplicationRunning || current.ActiveRevisionID != a.ActiveRevisionID || len(runner.calls) != 0 {
					t.Fatal("independent failure destroyed working application")
				}
			}
			if strings.HasPrefix(stage, "rollback") && current.ActiveRevisionID != a.ActiveRevisionID {
				t.Fatal("failed rollback falsely confirmed candidate")
			}
			if stage == "install cleanup" {
				for _, call := range runner.calls {
					for _, arg := range call.arguments {
						if arg == "--volumes" || arg == "--rmi" {
							t.Fatal("installation cleanup deleted business data")
						}
					}
				}
			}
		})
	}
}
