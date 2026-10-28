package dockerresource

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// This test requires a disposable PostgreSQL database and disposable Swarm
// manager. It never targets the operator's production Docker socket.
func TestIsolatedSwarmConfigAndSecretOwnershipAndRetry(t *testing.T) {
	for _, kind := range []string{"config", "secret"} {
		t.Run(kind, func(t *testing.T) { runIsolatedSwarmResource(t, kind) })
	}
}

func runIsolatedSwarmResource(t *testing.T, kind string) {
	dsn, endpoint := os.Getenv("NSC_TEST_POSTGRES_DSN"), os.Getenv("NSC_TEST_DOCKER_ENDPOINT")
	if dsn == "" || endpoint == "" {
		t.Skip("set isolated PostgreSQL and Docker endpoint")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	store, err := storage.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	cli, err := engine.NewManagementClient(model.Runtime{Endpoint: endpoint}, engine.TLSFiles{})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	info, err := cli.Info(ctx, client.InfoOptions{})
	if err != nil || info.Info.Swarm.Cluster == nil || !info.Info.Swarm.ControlAvailable {
		t.Fatalf("not an isolated Swarm manager: %v", err)
	}
	cluster := info.Info.Swarm.Cluster.ID
	now := time.Now().UTC()
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "resource-e2e-" + id.New(id.Pool), BackendNetwork: "fixture", ChallengeUpstream: "http://controller:8080", Enabled: true, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtime := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "resource-e2e-" + id.New(id.Runtime), Type: model.RuntimeSwarm, Endpoint: endpoint, ClusterID: cluster, Enabled: true, ManagementEnabled: true, Status: model.RuntimeOnline, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := store.CreateRuntime(ctx, &runtime); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	manager := Manager{Store: store, Config: cfg}
	operation := func(kind string) model.DeploymentOperation {
		value := model.DeploymentOperation{ID: id.New(id.Operation), RuntimeID: runtime.ID, Type: kind, Status: model.OperationQueued, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
		if _, err := store.CreateDeploymentOperation(ctx, &value); err != nil {
			t.Fatal(err)
		}
		claimed, err := store.ClaimDeploymentOperation(ctx, "fixture-worker", time.Now().UTC(), time.Now().Add(-time.Hour))
		if err != nil || claimed.ID != value.ID {
			t.Fatalf("claim %s: %#v %v", kind, claimed, err)
		}
		return claimed
	}
	name := "nsc-e2e-" + strings.ToLower(id.New(id.Operation))
	create := operation("DOCKER_RESOURCE_CREATE")
	request := Request{RuntimeID: runtime.ID, Action: "create", Kind: kind, Name: name, Content: []byte("private-e2e-content"), OperationID: create.ID}
	first, err := manager.Do(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if kind == "config" {
			_, _ = cli.ConfigRemove(context.Background(), first.ID, client.ConfigRemoveOptions{})
		} else {
			_, _ = cli.SecretRemove(context.Background(), first.ID, client.SecretRemoveOptions{})
		}
	})
	repeated, err := manager.Do(ctx, request)
	if err != nil || repeated.ID != first.ID {
		t.Fatalf("lost create response duplicated resource: %#v %v", repeated, err)
	}
	if err := store.RecordDockerResource(ctx, storage.DockerResource{ClusterID: cluster, Kind: kind, DockerID: first.ID, Name: name, CreateOperationID: create.ID, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	listed, err := manager.Do(ctx, Request{RuntimeID: runtime.ID, Action: "list"})
	if err != nil {
		t.Fatal(err)
	}
	owned := false
	for _, item := range listed.Items {
		if item.ID == first.ID {
			owned = item.Owned && item.Name == name && !strings.Contains(item.Name, "private-e2e-content")
		}
	}
	if !owned {
		t.Fatalf("owned Config missing from redacted list: %#v", listed.Items)
	}
	externalName := name + "-external"
	var externalID string
	if kind == "config" {
		value, createErr := cli.ConfigCreate(ctx, client.ConfigCreateOptions{Spec: swarm.ConfigSpec{Annotations: swarm.Annotations{Name: externalName}, Data: []byte("external-private-content")}})
		err = createErr
		externalID = value.ID
	} else {
		value, createErr := cli.SecretCreate(ctx, client.SecretCreateOptions{Spec: swarm.SecretSpec{Annotations: swarm.Annotations{Name: externalName}, Data: []byte("external-private-content")}})
		err = createErr
		externalID = value.ID
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if kind == "config" {
			_, _ = cli.ConfigRemove(context.Background(), externalID, client.ConfigRemoveOptions{})
		} else {
			_, _ = cli.SecretRemove(context.Background(), externalID, client.SecretRemoveOptions{})
		}
	})
	listed, err = manager.Do(ctx, Request{RuntimeID: runtime.ID, Action: "list", Kind: kind})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range listed.Items {
		if item.ID == externalID && item.Owned {
			t.Fatal("external Config claimed as NSC-owned")
		}
	}
	remove := operation("DOCKER_RESOURCE_DELETE")
	deletion := Request{RuntimeID: runtime.ID, Action: "delete", Kind: kind, ID: first.ID, OperationID: remove.ID, AcknowledgeRestoreRisk: true}
	zero := uint64(0)
	container := &swarm.ContainerSpec{Image: "busybox:latest"}
	if kind == "config" {
		container.Configs = []*swarm.ConfigReference{{ConfigID: first.ID, ConfigName: name, File: &swarm.ConfigReferenceFileTarget{Name: "/tmp/config"}}}
	} else {
		container.Secrets = []*swarm.SecretReference{{SecretID: first.ID, SecretName: name, File: &swarm.SecretReferenceFileTarget{Name: "/tmp/secret"}}}
	}
	service, err := cli.ServiceCreate(ctx, client.ServiceCreateOptions{Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: name + "-consumer"},
		TaskTemplate: swarm.TaskSpec{ContainerSpec: container}, Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &zero}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = cli.ServiceRemove(context.Background(), service.ID, client.ServiceRemoveOptions{}) })
	if _, err := manager.Do(ctx, deletion); !errors.Is(err, ErrInUse) {
		t.Fatalf("resource used by a Service was deletable: %v", err)
	}
	if _, err := cli.ServiceRemove(ctx, service.ID, client.ServiceRemoveOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Do(ctx, deletion); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Do(ctx, deletion); err != nil {
		t.Fatalf("lost delete response failed retry: %v", err)
	}
	if err := store.DeleteDockerResource(ctx, cluster, kind, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Do(ctx, deletion); err != nil {
		t.Fatalf("retry after DB evidence removal failed: %v", err)
	}
}
