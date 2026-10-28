package deployment

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type systemTargetStore struct {
	storage.Store
	app    model.Application
	target model.Application
}

func (s systemTargetStore) GetApplication(_ context.Context, key string) (model.Application, error) {
	if key == s.target.ID {
		return s.target, nil
	}
	return s.app, nil
}
func (s systemTargetStore) ListAllServicesFiltered(context.Context, storage.ServiceFilter) ([]model.Service, error) {
	return []model.Service{{ID: "gateway", Namespace: s.app.ProjectName, ApplicationID: s.target.ID}}, nil
}
func TestSystemApplicationCannotExecuteThroughOrdinaryNamespace(t *testing.T) {
	f := newWorkerFixture(t)
	for _, direct := range []bool{false, true} {
		app := model.Application{ID: "ordinary", ProjectName: "existing-gateway", RuntimeID: f.runtime.ID, Managed: true, System: direct}
		store := systemTargetStore{Store: f.store, app: app, target: model.Application{ID: "system", System: true}}
		runner := &recordingRunner{}
		worker := NewWorkerWithRunner(store, nil, f.keyring, f.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
		for _, action := range []string{"ADOPT", "START", "STOP", "DOWN", "REDEPLOY", "UPGRADE"} {
			op := model.DeploymentOperation{ApplicationID: app.ID, Type: action}
			if err := worker.execute(context.Background(), f.runtime, &op); !errors.Is(err, storage.ErrSystemProtected) {
				t.Fatal(action, direct, err)
			}
		}
		if len(runner.calls) != 0 {
			t.Fatal("protected workload reached Docker", runner.calls)
		}
	}
}
