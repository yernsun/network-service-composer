package storage

import (
	"context"
	"fmt"
	"github.com/yernsun/network-service-composer/internal/model"
	"testing"
	"time"
)

func TestDiscoveryReconciliationStorageFailuresRollBack(t *testing.T) {
	for _, kind := range []string{"new route", "new managed route", "update route", "remove route", "domain policy", "existing managed certificate", "insert endpoint", "update endpoint", "remove endpoint", "service counts"} {
		for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
			t.Run(kind+"/"+string(dialect), func(t *testing.T) {
				steps := []transactionBoundaryStep{}
				certSteps := []transactionBoundaryStep{boundaryRead("SELECT desired_revision", []string{"desired_revision"}, int64(3)), boundaryWrite("INSERT INTO certificates"), boundaryWrite("INSERT INTO certificate_domains"), boundaryWrite("UPDATE domains SET certificate_id")}
				switch kind {
				case "new route":
					steps = []transactionBoundaryStep{boundaryWrite("INSERT INTO domains"), boundaryWrite("INSERT INTO routes")}
				case "new managed route":
					steps = append(steps, boundaryWrite("INSERT INTO domains"))
					steps = append(steps, certSteps...)
					steps = append(steps, boundaryWrite("INSERT INTO routes"))
				case "update route":
					steps = []transactionBoundaryStep{boundaryWrite("UPDATE routes SET domain_id")}
				case "remove route":
					steps = []transactionBoundaryStep{boundaryWrite("UPDATE routes SET enabled")}
				case "domain policy":
					steps = []transactionBoundaryStep{boundaryRead("SELECT COUNT(*)", []string{"count"}, int64(0)), boundaryWrite("UPDATE domains SET tls_mode")}
				case "existing managed certificate":
					steps = append(steps, certSteps...)
				case "insert endpoint":
					steps = []transactionBoundaryStep{boundaryWrite("INSERT INTO service_instances"), boundaryWrite("UPDATE services SET desired_instances")}
				case "update endpoint":
					steps = []transactionBoundaryStep{boundaryWrite("UPDATE service_instances SET address"), boundaryWrite("UPDATE services SET desired_instances")}
				case "remove endpoint":
					steps = []transactionBoundaryStep{boundaryWrite("UPDATE service_instances SET runtime_state")}
				case "service counts":
					steps = []transactionBoundaryStep{boundaryWrite("UPDATE services SET desired_instances")}
				}
				for fault := -1; fault <= len(steps); fault++ {
					t.Run(fmt.Sprint(fault), func(t *testing.T) {
						runTransactionBoundary(t, dialect, steps, fault, func(s *SQLStore) error {
							ctx := context.Background()
							now := time.Now().UTC()
							tx, e := s.db.BeginTx(ctx, nil)
							if e != nil {
								return e
							}
							defer tx.Rollback()
							svc := model.Service{ID: "service", RoutingEligible: true, DiscoveredPort: 8080, Readiness: model.ReadinessPolicy{Mode: model.ReadinessDocker}, Status: model.ServiceUnavailable}
							d := model.Domain{ID: "domain", PoolID: "pool", Host: "app.example.test", TLSMode: model.TLSDisabled, HTTPEnabled: true, Enabled: true, Status: model.DomainReady, Source: model.SourceDiscovery}
							route := model.Route{ID: "route", DomainID: d.ID, ServiceID: svc.ID, ExternalKey: "route-key", Path: "/", Enabled: true, Status: model.RouteActive, Source: model.SourceDiscovery}
							observed := model.DiscoveredRoute{ExternalKey: route.ExternalKey, Host: d.Host, Path: "/", TLSMode: model.TLSDisabled, HTTPEnabled: true}
							domains := map[string]model.Domain{d.Host: d}
							existing := map[string]model.Route{route.ExternalKey: route}
							routes := []model.DiscoveredRoute{observed}
							endpoint := model.Endpoint{ID: "endpoint", ServiceID: svc.ID, ExternalID: "container", Address: "10.0.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthHealthy, ReadyState: model.Ready, ReadinessSource: model.ReadinessDocker, Weight: 1}
							endpoints := map[string]model.Endpoint{endpoint.ExternalID: endpoint}
							ds := model.DiscoveredService{Endpoints: []model.DiscoveredEndpoint{{ExternalID: endpoint.ExternalID, Address: endpoint.Address, Port: endpoint.Port, RuntimeState: endpoint.RuntimeState, HealthState: endpoint.HealthState, Weight: 1}}}
							endpointMode := false
							switch kind {
							case "new route", "new managed route":
								domains = map[string]model.Domain{}
								existing = map[string]model.Route{}
								if kind == "new managed route" {
									routes[0].TLSMode = model.TLSManaged
									routes[0].HTTPSEnabled = true
								}
							case "update route":
								routes[0].Path = "/api/"
							case "remove route":
								routes = nil
							case "domain policy":
								routes[0].HTTPSRedirect = true
							case "existing managed certificate":
								d.TLSMode = model.TLSManaged
								d.HTTPSEnabled = true
								domains[d.Host] = d
								routes[0].TLSMode = model.TLSManaged
								routes[0].HTTPSEnabled = true
							case "insert endpoint":
								endpointMode = true
								endpoints = map[string]model.Endpoint{}
							case "update endpoint":
								endpointMode = true
								ds.Endpoints[0].Address = "10.0.0.3"
							case "remove endpoint":
								endpointMode = true
								ds.Endpoints = nil
							case "service counts":
								endpointMode = true
								endpoints = map[string]model.Endpoint{}
								ds.Endpoints = nil
								ds.DesiredInstances = 2
							}
							if endpointMode {
								_, _, _, e = reconcileEndpointsTx(ctx, s, tx, svc, ds, endpoints, now)
							} else {
								_, e = reconcileRoutesTx(ctx, s, tx, "pool", svc, routes, domains, existing, now)
							}
							if e != nil {
								return e
							}
							return tx.Commit()
						})
					})
				}
			})
		}
	}
}
