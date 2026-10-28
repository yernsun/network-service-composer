package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// Override only the runtime read: SQLite fixtures exercise metadata transactions,
// not Swarm execution (which is intentionally forbidden on SQLite).
type systemRuntimeStore struct {
	storage.Store
	runtime model.Runtime
}

func (s systemRuntimeStore) GetRuntime(ctx context.Context, key string) (model.Runtime, error) {
	if key == s.runtime.ID {
		return s.runtime, nil
	}
	return s.Store.GetRuntime(ctx, key)
}
func systemFixture(t *testing.T) (*Core, config.SystemProjectConfig, map[string]bool) {
	t.Helper()
	c, rt, _ := controlFixture(t)
	rt.Type = model.RuntimeSwarm
	rt.ManagementEnabled = false
	c.store = systemRuntimeStore{Store: c.store, runtime: rt}
	c.config.Management.Enabled = false
	reg := config.SystemProjectConfig{RuntimeID: rt.ID, ClusterID: "cluster-a", Services: []config.SystemServiceConfig{{ObjectID: "controller-object", Environment: "prod"}, {ObjectID: "database-object", Environment: "prod"}}}
	return c, reg, map[string]bool{"controller-object": true, "database-object": true, "unrelated": true}
}
func TestGatewayBootstrapOwnershipAndProtection(t *testing.T) {
	c, reg, objects := systemFixture(t)
	ctx := context.Background()
	p, err := c.EnsureGatewayProject(ctx, reg, reg.ClusterID, objects)
	if err != nil {
		t.Fatal(err)
	}
	if !p.System || p.Name != "Gateway" || p.DefaultRuntimeID != reg.RuntimeID {
		t.Fatal(p)
	}
	e, err := c.GetEnvironment(ctx, p.InitialEnvironmentID)
	if err != nil || !e.System || e.Name != "prod" || e.Kind != "production" {
		t.Fatal(e, err)
	}
	again, err := c.EnsureGatewayProject(ctx, reg, reg.ClusterID, objects)
	if err != nil || again.ID != p.ID || again.Revision != p.Revision {
		t.Fatal(again, err)
	}
	now := time.Now().UTC()
	// Actual observed IDs carry ownership; similar names and other objects do not.
	for i, object := range []string{"controller-object", "unrelated"} {
		svc := model.Service{ID: []string{"svc-system", "svc-other"}[i], RuntimeID: reg.RuntimeID, RuntimeObjectID: object, ExternalID: object, ServiceKey: object, Name: "nsc-controller", Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
		if err := c.store.UpsertService(ctx, &svc); err != nil {
			t.Fatal(err)
		}
	}
	refs, err := c.store.ServiceEnvironments(ctx, []string{"svc-system", "svc-other"})
	if err != nil || len(refs) != 1 || !refs["svc-system"].System || refs["svc-system"].ProjectID != p.ID {
		t.Fatal(refs, err)
	}
	if err := c.store.CheckProjectResource(ctx, "svc-system"); !errors.Is(err, storage.ErrSystemProtected) {
		t.Fatal(err)
	}
	if err := c.store.CheckProjectResource(ctx, "svc-other"); err != nil {
		t.Fatal(err)
	}
	c.config.Management.Enabled = true
	for _, force := range []bool{false, true} {
		if _, err := c.PreviewProjectDeletion(ctx, p.ID, force); !errors.Is(err, storage.ErrSystemProtected) {
			t.Fatal(force, err)
		}
	}
	if err := c.DeleteEmptyEnvironment(ctx, e.ID, e.Revision); !errors.Is(err, storage.ErrSystemProtected) {
		t.Fatal(err)
	}
	if _, err := c.UpdateProject(ctx, p.ID, ProjectSettings{Name: "changed"}, p.Revision); !errors.Is(err, storage.ErrSystemProtected) {
		t.Fatal(err)
	}
	if _, err := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "other"}); !errors.Is(err, storage.ErrSystemProtected) {
		t.Fatal(err)
	}
	if _, _, err := c.EnvironmentPreflightInput(ctx, e.ID, "services: {}"); !errors.Is(err, storage.ErrSystemProtected) {
		t.Fatal(err)
	}
	// Registration of a replacement requires fresh evidence; restart cannot infer it from a name.
	reg.Services[0].ObjectID = "replacement"
	if _, err := c.EnsureGatewayProject(ctx, reg, reg.ClusterID, objects); err == nil {
		t.Fatal("unobserved replacement accepted")
	}
	objects["replacement"] = true
	if _, err := c.EnsureGatewayProject(ctx, reg, reg.ClusterID, objects); err != nil {
		t.Fatal(err)
	}
	if err := c.store.CheckProjectResource(ctx, "svc-system"); !errors.Is(err, storage.ErrSystemProtected) {
		t.Fatal("retired object lost protection", err)
	}
	reg.Services = append(reg.Services, config.SystemServiceConfig{ObjectID: "sandbox", Environment: "sandbox"})
	objects["sandbox"] = true
	if _, err := c.EnsureGatewayProject(ctx, reg, reg.ClusterID, objects); err != nil {
		t.Fatal(err)
	}
	envs, err := c.ListEnvironments(ctx, p.ID, storage.Page{Limit: 200})
	if err != nil || len(envs) != 2 {
		t.Fatal(envs, err)
	}
}
func TestGatewayRegistrationRejectsWrongClusterAndSlugCollision(t *testing.T) {
	c, reg, objects := systemFixture(t)
	ctx := context.Background()
	if _, err := c.EnsureGatewayProject(ctx, reg, "other-cluster", objects); err == nil {
		t.Fatal("cross-cluster accepted")
	}
	c.config.Management.Enabled = true
	ordinary, err := c.CreateProject(ctx, "Legacy", "gateway")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.EnsureGatewayProject(ctx, reg, reg.ClusterID, objects); !errors.Is(err, storage.ErrConflict) {
		t.Fatal(err)
	}
	projects, _ := c.ListProjects(ctx, storage.Page{Limit: 200})
	envs, _ := c.store.ListControlDocuments(ctx, "environment", "", storage.Page{Limit: 200})
	if len(projects) != 1 || projects[0].ID != ordinary.ID || projects[0].System || len(envs) != 0 {
		t.Fatal("partial bootstrap or adoption", projects, envs)
	}
}
func TestGatewayConcurrentBootstrap(t *testing.T) {
	c, reg, objects := systemFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 8 {
				_, err := c.EnsureGatewayProject(ctx, reg, reg.ClusterID, objects)
				if err == nil {
					return
				}
				if ctx.Err() != nil {
					return
				}
			}
			t.Error("concurrent bootstrap did not converge")
		}()
	}
	wg.Wait()
	projects, err := c.ListProjects(ctx, storage.Page{Limit: 200})
	if err != nil || len(projects) != 1 {
		t.Fatal(projects, err)
	}
	envs, err := c.ListEnvironments(ctx, projects[0].ID, storage.Page{Limit: 200})
	if err != nil || len(envs) != 1 {
		t.Fatal(envs, err)
	}
}

func TestGatewayRejectsRegistrationMistakesAndOldDeletionTasks(t *testing.T) {
	c, reg, objects := systemFixture(t)
	ctx := context.Background()
	invalid := reg
	invalid.Services = append(append([]config.SystemServiceConfig{}, reg.Services...), reg.Services[0])
	if _, err := c.EnsureGatewayProject(ctx, invalid, reg.ClusterID, objects); err == nil {
		t.Fatal("duplicate registration accepted")
	}
	p, err := c.EnsureGatewayProject(ctx, reg, reg.ClusterID, objects)
	if err != nil {
		t.Fatal(err)
	}
	other := reg
	other.ClusterID = "other-cluster"
	if _, err := c.EnsureGatewayProject(ctx, other, other.ClusterID, objects); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("system marker rebound", err)
	}
	task := ProjectDeletion{ProjectID: p.ID, Status: "QUEUED", Phase: "WORKLOADS"}
	task.ID = "prjdel_old"
	if err := c.saveControl(storage.WithSystemBootstrap(ctx), task.ID, "project-deletion", p.ID, task, 0); err != nil {
		t.Fatal(err)
	}
	worker := ProjectDeletionWorker{Core: c}
	if err := worker.execute(ctx, task.ID, nil); !errors.Is(err, storage.ErrSystemProtected) {
		t.Fatal("old task bypassed protection", err)
	}
	saved, err := c.GetProjectDeletion(ctx, task.ID)
	if err != nil || saved.Status != "QUEUED" {
		t.Fatal(saved, err)
	}
	if _, err := c.GetProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
}
