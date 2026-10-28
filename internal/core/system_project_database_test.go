package core

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// Use disposable databases: the Gateway marker is deliberately unique per control plane.
func TestGatewayExternalDatabaseInitialization(t *testing.T) {
	for _, fixture := range []struct{ driver, variable string }{{"postgres", "NSC_TEST_POSTGRES_DSN"}, {"mysql", "NSC_TEST_MYSQL_DSN"}} {
		t.Run(fixture.driver, func(t *testing.T) {
			dsn := os.Getenv(fixture.variable)
			if dsn == "" {
				t.Skip("requires isolated database fixture")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			s, err := storage.Open(fixture.driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			pool := model.GatewayPool{ID: id.New("pool"), Name: "system-fixture", BackendNetwork: "fixture", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
			if err = s.CreatePool(ctx, &pool); err != nil {
				t.Fatal(err)
			}
			rt := model.Runtime{ID: id.New("rt"), PoolID: pool.ID, Name: "fixture", Type: model.RuntimeSwarm, Endpoint: "node://fixture", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
			if err = s.CreateRuntime(ctx, &rt); err != nil {
				t.Fatal(err)
			}
			reg := config.SystemProjectConfig{RuntimeID: rt.ID, ClusterID: "fixture-cluster", Services: []config.SystemServiceConfig{{ObjectID: "fixture-controller", Environment: "prod"}}}
			cfg := config.Defaults()
			cfg.Management.Enabled = false
			var wg sync.WaitGroup
			for range 4 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					connection, err := storage.Open(fixture.driver, dsn)
					if err != nil {
						t.Error(err)
						return
					}
					defer connection.Close()
					c := New(connection, cfg, nil)
					for attempt := 0; attempt < 5; attempt++ {
						if _, err = c.EnsureGatewayProject(ctx, reg, reg.ClusterID, map[string]bool{"fixture-controller": true}); err == nil {
							return
						}
						select {
						case <-ctx.Done():
							t.Error(ctx.Err())
							return
						case <-time.After(10 * time.Millisecond):
						}
					}
					t.Error(err)
				}()
			}
			wg.Wait()
			c := New(s, cfg, nil)
			projects, err := c.ListProjects(ctx, storage.Page{Limit: 200})
			if err != nil || len(projects) != 1 || !projects[0].System {
				t.Fatal(projects, err)
			}
			envs, err := c.ListEnvironments(ctx, projects[0].ID, storage.Page{Limit: 200})
			if err != nil || len(envs) != 1 || !envs[0].System {
				t.Fatal(envs, err)
			}
			restarted := New(s, cfg, nil)
			p, err := restarted.EnsureGatewayProject(ctx, reg, reg.ClusterID, map[string]bool{"fixture-controller": true})
			if err != nil || p.ID != projects[0].ID || p.Revision != projects[0].Revision {
				t.Fatal(p, err)
			}
			svc := model.Service{ID: id.New("svc"), RuntimeID: rt.ID, RuntimeObjectID: "fixture-controller", ServiceKey: "fixture", ExternalID: "fixture", Name: "Fixture controller", Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			if err = s.UpsertService(ctx, &svc); err != nil {
				t.Fatal(err)
			}
			refs, err := s.ServiceEnvironments(ctx, []string{svc.ID})
			if err != nil || !refs[svc.ID].System || refs[svc.ID].ProjectID != p.ID {
				t.Fatal(refs, err)
			}
			if err = s.CheckProjectResource(ctx, svc.ID); err != storage.ErrSystemProtected {
				t.Fatal(err)
			}
			definitions := []config.SystemApplicationConfig{{Key: "controller", Name: "Controller", Environment: "prod", RuntimeID: rt.ID, ObjectIDs: []string{"fixture-controller"}}}
			for range 4 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					connection, e := storage.Open(fixture.driver, dsn)
					if e != nil {
						t.Error(e)
						return
					}
					defer connection.Close()
					worker := New(connection, cfg, nil)
					for attempt := 0; attempt < 20; attempt++ {
						e = worker.ReconcileGatewayApplications(ctx, p, reg.ClusterID, definitions, map[string]map[string]bool{rt.ID: {"fixture-controller": true}})
						if e == nil {
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
					t.Error(e)
				}()
			}
			wg.Wait()
			apps, err := s.ListApplications(ctx, "", storage.Page{Limit: 200})
			if err != nil || len(apps) != 1 || !apps[0].System {
				t.Fatal(apps, err)
			}
			associated, err := s.GetService(ctx, svc.ID)
			if err != nil || associated.ApplicationID != apps[0].ID {
				t.Fatal(associated, err)
			}
			if _, err = restarted.EnsureGatewayProject(ctx, reg, reg.ClusterID, map[string]bool{"fixture-controller": true}); err != nil {
				t.Fatal(err)
			}

		})
	}
}
