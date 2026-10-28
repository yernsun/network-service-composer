package storage

import (
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"testing"
	"time"
)

func TestVolumeOperationsAreNotPendingConfigOrSecretConsumers(t *testing.T) {
	store := newSQLiteStore(t)
	now := time.Now().UTC()
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "volume-fixture", BackendNetwork: "fixture", ChallengeUpstream: "http://fixture:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(t.Context(), &pool); err != nil {
		t.Fatal(err)
	}
	rt := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "fixture", Type: model.RuntimeDocker, Endpoint: "unix:///nonexistent-fixture.sock", ClusterID: "fixture-cluster", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(t.Context(), &rt); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"VOLUME_CREATE", "VOLUME_DELETE", "DOCKER_RESOURCE_CREATE", "DOCKER_RESOURCE_DELETE"} {
		op := model.DeploymentOperation{ID: id.New(id.Operation), RuntimeID: rt.ID, Type: kind, Status: model.OperationQueued, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
		if _, err := store.CreateDeploymentOperation(t.Context(), &op); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := store.HasPendingDockerResourceConsumer(t.Context(), rt.ClusterID, "")
	if err != nil || pending {
		t.Fatalf("volume maintenance falsely blocked immutable resources: %t %v", pending, err)
	}
	op := model.DeploymentOperation{ID: id.New(id.Operation), RuntimeID: rt.ID, Type: "APPLICATION_START", Status: model.OperationQueued, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if _, err := store.CreateDeploymentOperation(t.Context(), &op); err != nil {
		t.Fatal(err)
	}
	pending, err = store.HasPendingDockerResourceConsumer(t.Context(), rt.ClusterID, "")
	if err != nil || !pending {
		t.Fatalf("application deployment was not protected: %t %v", pending, err)
	}
	pending, err = store.HasPendingDockerResourceConsumer(t.Context(), rt.ClusterID, op.ID)
	if err != nil || pending {
		t.Fatalf("own operation not excluded: %t %v", pending, err)
	}
}
