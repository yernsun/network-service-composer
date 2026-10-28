package storage

import (
	"github.com/yernsun/network-service-composer/internal/model"
	"testing"
)

func TestResolvedDNSConflictRequiresUsableRemainingServiceEvidence(t *testing.T) {
	for _, mode := range []string{"ready", "missing name", "remaining issue", "configuration error", "missing port", "disabled", "different conflict", "same conflict"} {
		t.Run(mode, func(t *testing.T) {
			s := newSQLiteStore(t)
			ctx, rt, now := managedRuntimeFixture(t, s)
			v := model.Service{ID: "service", RuntimeID: rt.ID, ServiceKey: "project/web", DNSName: "project_web", DNSStatus: model.DNSConflict, DNSLastError: serviceDNSConflictPrefix + " previous duplicate", RoutingIssues: []string{serviceDNSConflictPrefix + " previous duplicate"}, DiscoveredEnabled: true, DiscoveredPort: 8080, Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
			switch mode {
			case "missing name":
				v.DNSName = ""
			case "remaining issue":
				v.RoutingIssues = append(v.RoutingIssues, "network unavailable")
			case "configuration error":
				v.ConfigurationError = "invalid labels"
			case "missing port":
				v.DiscoveredPort = 0
			case "disabled":
				v.DiscoveredEnabled = false
			case "different conflict":
				v.DNSLastError = "external ownership conflict"
				v.RoutingIssues = []string{"external ownership conflict"}
			case "same conflict":
				v.RoutingIssues = nil
			}
			if e := s.UpsertService(ctx, &v); e != nil {
				t.Fatal(e)
			}
			if mode == "same conflict" {
				other := v
				other.ID = "other"
				other.ServiceKey = "other/web"
				other.ExternalID = "other/web"
				if e := s.UpsertService(ctx, &other); e != nil {
					t.Fatal(e)
				}
			}
			tx, e := s.db.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			if e = resolveServiceDNSConflictsTx(ctx, s, tx, rt.PoolID, now); e != nil {
				t.Fatal(e)
			}
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
			got, e := s.GetService(ctx, v.ID)
			if e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "ready":
				if got.DNSStatus != model.DNSReady || !got.RoutingEligible || got.DNSLastError != "" || len(got.RoutingIssues) != 0 {
					t.Fatal("cleared conflict did not recover usable service", got)
				}
			case "disabled":
				if got.DNSStatus != model.DNSReady || got.RoutingEligible {
					t.Fatal("recovery enabled disabled service", got)
				}
			case "different conflict", "same conflict":
				if got.DNSStatus != model.DNSConflict || got.RoutingEligible || got.DNSLastError == "" {
					t.Fatal("unresolved conflict disappeared", got)
				}
			default:
				if got.DNSStatus != model.DNSMissing || got.RoutingEligible {
					t.Fatal("incomplete evidence recovered service", got)
				}
			}
			if mode == "remaining issue" && (len(got.RoutingIssues) != 1 || got.RoutingIssues[0] != "network unavailable") {
				t.Fatal("unrelated issue discarded", got.RoutingIssues)
			}
		})
	}
}
func TestHTTPProbeReconciliationSeparatesInstanceReadinessFromServiceReachability(t *testing.T) {
	for _, mode := range []string{"new targets", "address change", "missing target", "invalid service", "unhealthy ready", "no effective port", "unready transition"} {
		t.Run(mode, func(t *testing.T) {
			s := newSQLiteStore(t)
			ctx, rt, now := managedRuntimeFixture(t, s)
			service := model.Service{ID: "service", RuntimeID: rt.ID, ServiceKey: "web", DiscoveredPort: 8080, DiscoveredEnabled: true, Readiness: model.ReadinessPolicy{Mode: model.ReadinessHTTP, Path: "/ready"}, DNSName: "web", DNSStatus: model.DNSUnreachable, DesiredInstances: 1, RunningInstances: 1, ReadyInstances: 1, RuntimeReadyInstances: 1, Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
			current := model.Endpoint{ID: "endpoint", ServiceID: service.ID, ExternalID: "container", Address: "192.0.2.1", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, ReadyState: model.Ready, ReadinessSource: model.ReadinessHTTP, Weight: 1, FirstSeenAt: now, LastSeenAt: now, Revision: 1, CreatedAt: now, UpdatedAt: now}
			observed := model.DiscoveredEndpoint{ExternalID: current.ExternalID, Address: current.Address, Port: 8080, RuntimeState: current.RuntimeState, HealthState: current.HealthState, Weight: 1}
			existing := map[string]model.Endpoint{}
			discovered := model.DiscoveredService{DesiredInstances: 1, RunningInstances: 1, ReadyInstances: 1}
			switch mode {
			case "address change":
				observed.Address = "192.0.2.2"
			case "invalid service":
				service.ConfigurationError = "invalid routing"
			case "unhealthy ready":
				observed.HealthState = model.HealthUnhealthy
			case "no effective port":
				service.DiscoveredPort = 0
				observed.Port = 0
			case "unready transition":
				current.ReadyState = model.ReadyStarting
				observed.Address = "invalid"
			}
			if e := s.UpsertService(ctx, &service); e != nil {
				t.Fatal(e)
			}
			if mode != "new targets" {
				if e := s.UpsertEndpoint(ctx, &current); e != nil {
					t.Fatal(e)
				}
				existing[current.ExternalID] = current
			}
			if mode != "missing target" && mode != "invalid service" {
				discovered.Endpoints = []model.DiscoveredEndpoint{observed}
				if mode == "new targets" {
					other := observed
					other.ExternalID = "second"
					discovered.Endpoints = append(discovered.Endpoints, other)
					discovered.DesiredInstances = 2
				}
			}
			tx, e := s.db.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			got, _, probeChanged, e := reconcileEndpointsTx(ctx, s, tx, service, discovered, existing, now)
			if e != nil {
				t.Fatal(e)
			}
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
			// Docker health changes replica eligibility, but does not change
			// an explicitly configured HTTP probe's address or policy.
			wantProbe := mode != "invalid service" && mode != "unhealthy ready"
			if probeChanged != wantProbe {
				t.Fatal("probe plan change lost", probeChanged, mode)
			}
			if got.DNSStatus != model.DNSUnreachable {
				t.Fatal("instance health overwrote gateway reachability")
			}
			if mode == "unhealthy ready" && got.ReadyInstances != 0 {
				t.Fatal("Docker-unhealthy replica counted as healthy")
			}
			if mode == "unready transition" {
				endpoints, e := s.ListEndpoints(ctx, service.ID)
				if e != nil || len(endpoints) != 1 || endpoints[0].DrainSince != nil {
					t.Fatal("never-ready replica claimed draining history", e)
				}
			}
		})
	}
}
