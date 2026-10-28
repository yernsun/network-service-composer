package storage

import (
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func TestEndpointReconciliationPreservesReadinessAndLifecycleFacts(t *testing.T) {
	for _, name := range []string{"unchanged", "address", "port", "node", "weight", "stale", "becomes unhealthy", "recovers", "stopping", "missing", "already draining", "removed", "new ready", "new draining", "new not ready", "fallback port", "degraded replicas", "reported running"} {
		t.Run(name, func(t *testing.T) {
			s := newSQLiteStore(t)
			ctx, rt, now := managedRuntimeFixture(t, s)
			service := model.Service{ID: "service", RuntimeID: rt.ID, ServiceKey: "web", DiscoveredPort: 8080, DiscoveredEnabled: true, Readiness: model.ReadinessPolicy{Mode: model.ReadinessDocker}, DesiredInstances: 1, RunningInstances: 1, RuntimeReadyInstances: 1, ReadyInstances: 1, Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
			current := model.Endpoint{ID: "endpoint", ServiceID: service.ID, ExternalID: "container", Address: "10.0.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthHealthy, ReadyState: model.Ready, ReadinessSource: model.ReadinessDocker, Weight: 1, FirstSeenAt: now.Add(-time.Hour), LastSeenAt: now, Revision: 1, CreatedAt: now, UpdatedAt: now, ReadySince: &now}
			observed := model.DiscoveredEndpoint{ExternalID: current.ExternalID, Address: current.Address, Port: 8080, RuntimeState: current.RuntimeState, HealthState: current.HealthState, Weight: 1}
			existing := map[string]model.Endpoint{}
			discovered := model.DiscoveredService{DesiredInstances: 1, RunningInstances: 1, ReadyInstances: 1}
			wantState := model.Ready
			wantService := model.ServiceAvailable
			wantReady := 1
			wantChanged := false
			switch name {
			case "address":
				observed.Address = "10.0.0.3"
				wantChanged = true
			case "port":
				observed.Port = 9090
				wantChanged = true
			case "node":
				observed.NodeID = "node-2"
			case "weight":
				observed.Weight = 2
				wantChanged = true
			case "stale":
				current.StaleSince = &now
			case "becomes unhealthy":
				observed.HealthState = model.HealthUnhealthy
				wantState = model.NotReady
				wantService = model.ServiceUnavailable
				wantReady = 0
				wantChanged = true
			case "recovers":
				current.ReadyState = model.NotReady
				current.HealthState = model.HealthUnhealthy
				current.DrainSince = &now
				wantChanged = true
			case "stopping":
				observed.RuntimeState = model.EndpointStopping
				wantState = model.Draining
				wantService = model.ServiceUnavailable
				wantReady = 0
				wantChanged = true
			case "missing":
				wantState = model.Draining
				wantService = model.ServiceUnavailable
				wantReady = 0
				wantChanged = true
			case "already draining":
				current.RuntimeState = model.EndpointStopping
				current.ReadyState = model.Draining
				current.DrainSince = &now
				wantState = model.Draining
				wantService = model.ServiceUnavailable
				wantReady = 0
			case "removed":
				current.ReadyState = model.Removed
				wantState = model.Removed
				wantService = model.ServiceUnavailable
				wantReady = 0
			case "new ready":
				wantChanged = true
			case "new draining":
				observed.RuntimeState = model.EndpointStopping
				wantState = model.Draining
				wantService = model.ServiceUnavailable
				wantReady = 0
			case "new not ready":
				observed.HealthState = model.HealthUnhealthy
				wantState = model.NotReady
				wantService = model.ServiceUnavailable
				wantReady = 0
			case "fallback port":
				observed.Port = 0
			case "degraded replicas":
				discovered.DesiredInstances = 2
				wantService = model.ServiceDegraded
			case "reported running":
				discovered.RunningInstances = 3
			}
			if err := s.UpsertService(ctx, &service); err != nil {
				t.Fatal(err)
			}
			isNew := name == "new ready" || name == "new draining" || name == "new not ready"
			if !isNew {
				if err := s.UpsertEndpoint(ctx, &current); err != nil {
					t.Fatal(err)
				}
				existing[current.ExternalID] = current
			}
			if name != "missing" && name != "already draining" && name != "removed" {
				discovered.Endpoints = []model.DiscoveredEndpoint{observed}
			}
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			actual, changed, _, err := reconcileEndpointsTx(ctx, s, tx, service, discovered, existing, now.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if actual.Status != wantService || actual.ReadyInstances != wantReady || changed != wantChanged {
				t.Fatalf("reconcile status=%s ready=%d changed=%t want=%s/%d/%t", actual.Status, actual.ReadyInstances, changed, wantService, wantReady, wantChanged)
			}
			endpoints, err := s.ListEndpoints(ctx, service.ID)
			if err != nil || len(endpoints) != 1 {
				t.Fatal("endpoint identity lost", err)
			}
			e := endpoints[0]
			if e.ReadyState != wantState {
				t.Fatalf("readiness=%s want=%s", e.ReadyState, wantState)
			}
			if name == "recovers" && (e.DrainSince != nil || e.ReadySince == nil) {
				t.Fatal("recovery left draining evidence")
			}
			if wantState == model.Draining && e.DrainSince == nil {
				t.Fatal("drain transition lacked timestamp")
			}
			if name == "stale" && e.StaleSince != nil {
				t.Fatal("fresh observation remained stale")
			}
			if name == "reported running" && actual.RunningInstances != 3 {
				t.Fatal("runtime observation count discarded")
			}
			if name == "fallback port" && e.Port != 8080 {
				t.Fatal("logical service port was not inherited")
			}
		})
	}
}
