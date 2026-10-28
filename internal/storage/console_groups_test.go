package storage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestConsoleGroups(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			var store *SQLStore
			if driver == "sqlite" {
				store = newSQLiteStore(t)
			} else {
				env := "NSC_TEST_POSTGRES_DSN"
				if driver == "mysql" {
					env = "NSC_TEST_MYSQL_DSN"
				}
				dsn := os.Getenv(env)
				if dsn == "" {
					t.Skip("external database not configured")
				}
				var err error
				store, err = Open(driver, dsn)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { store.Close() })
				if err = store.Migrate(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			now := time.Now().UTC()
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			pool := model.GatewayPool{ID: id.New(id.Pool), Name: "tree", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
			must(store.CreatePool(ctx, &pool))
			rt := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
			must(store.CreateRuntime(ctx, &rt))
			other := rt
			other.ID = id.New(id.Runtime)
			other.Name = "other-runtime"
			must(store.CreateRuntime(ctx, &other))
			var first model.Service
			for i := 0; i < 207; i++ {
				project := "same_project"
				if i >= 205 {
					project = fmt.Sprintf("other_%d", i)
				}
				runtimeID := rt.ID
				if i == 206 {
					runtimeID = other.ID
					project = "same_project"
				}
				v := model.Service{ID: id.New(id.Service), RuntimeID: runtimeID, ServiceKey: fmt.Sprintf("%s/web%d", project, i), ExternalID: fmt.Sprintf("%s/web%d", project, i), Name: fmt.Sprintf("web%d", i), Namespace: project, QualifiedName: fmt.Sprintf("%s_web%d", project, i), DNSName: fmt.Sprintf("%s_web%d", project, i), DNSStatus: model.DNSReady, RoutingEligible: true, DiscoveredProtocol: "http", DiscoveredPort: 8080, DiscoveredEnabled: true, Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
				must(store.UpsertService(ctx, &v))
				if i == 0 {
					first = v
				}
			}
			groups, err := store.ListServiceGroups(ctx, "services", ConsoleFilter{ServiceFilter: ServiceFilter{RuntimeID: rt.ID}}, Page{Limit: 1})
			must(err)
			if len(groups) != 1 {
				t.Fatal(groups)
			}
			next, err := store.ListServiceGroups(ctx, "services", ConsoleFilter{ServiceFilter: ServiceFilter{RuntimeID: rt.ID}}, Page{Limit: 1, After: groups[0].Key})
			must(err)
			if len(next) != 1 || next[0].Key == groups[0].Key {
				t.Fatal(next)
			}
			key := GroupKey(rt.ID, "docker", "same_project")
			if key == GroupKey(other.ID, "docker", "same_project") {
				t.Fatal("colliding runtimes")
			}
			seen := map[string]bool{}
			cursor := ""
			for {
				rows, err := store.ListServicesFiltered(ctx, ServiceFilter{GroupKey: key}, Page{Limit: 50, After: cursor})
				must(err)
				for _, v := range rows {
					if seen[v.ID] {
						t.Fatal("duplicate")
					}
					seen[v.ID] = true
				}
				if len(rows) < 50 {
					break
				}
				cursor = rows[len(rows)-1].ID
			}
			if len(seen) != 205 {
				t.Fatalf("got %d services", len(seen))
			}
			filtered, err := store.ListServiceGroups(ctx, "services", ConsoleFilter{ServiceFilter: ServiceFilter{GroupKey: key, Search: "web204"}}, Page{})
			must(err)
			if len(filtered) != 1 || filtered[0].Count != 1 {
				t.Fatal(filtered)
			}
			domain := model.Domain{ID: id.New(id.Domain), PoolID: pool.ID, Host: pool.ID + ".example.test", TLSMode: model.TLSDisabled, HTTPEnabled: true, Enabled: true, Source: model.SourceAPI, Revision: 1, CreatedAt: now, UpdatedAt: now}
			must(store.CreateDomain(ctx, &domain))
			route := model.Route{ID: id.New(id.Route), DomainID: domain.ID, ServiceID: first.ID, Path: "/", UpstreamProtocol: "http", Enabled: true, Status: model.RouteActive, Source: model.SourceAPI, Revision: 1, CreatedAt: now, UpdatedAt: now}
			must(store.CreateRoute(ctx, &route))
			f := ConsoleFilter{ServiceFilter: ServiceFilter{GroupKey: key, Status: "ACTIVE"}, DomainID: domain.ID, ServiceID: first.ID}
			routes, err := store.ListRoutesFiltered(ctx, f, Page{})
			must(err)
			if len(routes) != 1 {
				t.Fatal(routes)
			}
			rg, err := store.ListServiceGroups(ctx, "routes", f, Page{})
			must(err)
			if len(rg) != 1 || rg[0].Count != 1 {
				t.Fatal(rg)
			}
			refs, err := store.ConsoleReferences(ctx, "domains", []string{domain.ID})
			must(err)
			if refs[domain.ID].Host != domain.Host || len(refs[domain.ID].Entrypoints) != 1 {
				t.Fatal(refs)
			}
			for i := 0; i < 3; i++ {
				v := model.Endpoint{ID: id.New(id.Endpoint), ServiceID: first.ID, ExternalID: fmt.Sprint(i), Address: "10.0.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthHealthy, ReadyState: model.Ready, FirstSeenAt: now, LastSeenAt: now, Revision: 1, CreatedAt: now, UpdatedAt: now}
				must(store.UpsertEndpoint(ctx, &v))
			}
			instances, err := store.ListEndpointsPage(ctx, first.ID, Page{Limit: 2})
			must(err)
			rest, err := store.ListEndpointsPage(ctx, first.ID, Page{Limit: 2, After: instances[1].ID})
			must(err)
			all, err := store.ListEndpoints(ctx, first.ID)
			must(err)
			if len(instances) != 2 || len(rest) != 1 || len(all) != 3 {
				t.Fatal("instance pagination")
			}
			for _, bad := range []string{"!", "W10", "WyJydCIsImJhZCIsIm4iXQ"} {
				if _, err := ParseGroupKey(bad); err == nil {
					t.Fatal("accepted invalid key")
				}
			}
		})
	}
}

func TestStackNamespaceRefreshPreservesGatewayIdentity(t *testing.T) {
	store := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, store)
	service := model.Service{ID: id.New(id.Service), RuntimeID: runtime.ID, ServiceKey: "my_stack_web", ExternalID: "my_stack_web", Name: "my_stack_web", Namespace: "swarm", QualifiedName: "my_stack_web", DNSName: "my_stack_web", DNSStatus: model.DNSReady, RoutingEligible: true, DiscoveredEnabled: true, DiscoveredProtocol: "http", DiscoveredPort: 80, Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	before := currentPoolRevision(t, store, runtime.PoolID)
	discovered := model.DiscoveredService{ServiceKey: service.ServiceKey, ExternalID: service.ExternalID, Name: service.Name, Namespace: "my_stack", QualifiedName: service.QualifiedName, DNSName: service.DNSName, DNSStatus: service.DNSStatus, RoutingEligible: true, Enabled: true, Protocol: "http", TargetPort: 80}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	updated, changed, err := upsertDiscoveredServiceTx(ctx, store, tx, model.RuntimeSnapshot{RuntimeID: runtime.ID, ObservedAt: now.Add(time.Second)}, discovered, map[string]model.Service{service.ServiceKey: service})
	if err != nil {
		t.Fatal(err)
	}
	if changed || updated.ID != service.ID || updated.ServiceKey != service.ServiceKey || updated.DNSName != service.DNSName || updated.Namespace != "my_stack" {
		t.Fatalf("metadata changed gateway identity: %#v changed=%v", updated, changed)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if currentPoolRevision(t, store, runtime.PoolID) != before {
		t.Fatal("metadata changed desired gateway revision")
	}
}
