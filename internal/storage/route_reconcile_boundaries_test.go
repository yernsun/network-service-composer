package storage

import (
	"github.com/yernsun/network-service-composer/internal/model"
	"testing"
)

func TestDiscoveredRouteReconciliationPreservesPolicyAndOwnership(t *testing.T) {
	for _, name := range []string{"unchanged", "path", "priority", "strip", "port", "protocol", "server name", "websocket", "disabled", "wrong status", "stale error", "domain changed", "missing", "missing conflict", "missing disabled", "DNS pending new", "DNS pending inactive", "DNS pending active", "precedence conflict", "external domain", "domain disabled", "enable managed TLS", "disable managed TLS", "redirect", "HTTP entrypoint", "HTTPS entrypoint"} {
		t.Run(name, func(t *testing.T) {
			s := newSQLiteStore(t)
			ctx, rt, now := managedRuntimeFixture(t, s)
			svc := model.Service{ID: "service", RuntimeID: rt.ID, ServiceKey: "web", DiscoveredEnabled: true, RoutingEligible: true, DiscoveredPort: 8080, Revision: 1, CreatedAt: now, UpdatedAt: now}
			if e := s.UpsertService(ctx, &svc); e != nil {
				t.Fatal(e)
			}
			d := model.Domain{ID: "domain", PoolID: rt.PoolID, Host: "app.example.test", TLSMode: model.TLSDisabled, HTTPEnabled: true, Status: model.DomainReady, Source: model.SourceDiscovery, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
			route := model.Route{ID: "route", DomainID: d.ID, ServiceID: svc.ID, ExternalKey: "route-key", Path: "/", TargetPort: 8080, UpstreamProtocol: "http", Enabled: true, Status: model.RouteActive, Source: model.SourceDiscovery, Revision: 1, CreatedAt: now, UpdatedAt: now}
			observed := model.DiscoveredRoute{ExternalKey: route.ExternalKey, Host: d.Host, Path: route.Path, TargetPort: route.TargetPort, UpstreamProtocol: route.UpstreamProtocol, TLSMode: d.TLSMode, HTTPEnabled: true}
			wantEnabled, wantChanged := true, true
			switch name {
			case "unchanged":
				wantChanged = false
			case "path":
				observed.Path = "/api/"
			case "priority":
				observed.Priority = 10
			case "strip":
				observed.StripPath = true
			case "port":
				observed.TargetPort = 9090
			case "protocol":
				observed.UpstreamProtocol = "https"
			case "server name":
				observed.UpstreamServerName = "upstream.example.test"
			case "websocket":
				observed.WebsocketEnabled = true
			case "disabled":
				route.Enabled = false
			case "wrong status":
				route.Status = model.RouteError
			case "stale error":
				route.LastError = "old error"
			case "domain changed":
				route.DomainID = "old-domain"
			case "missing":
				wantEnabled = false
			case "missing conflict":
				route.Enabled = false
				route.LastError = discoveryRouteConflictPrefix + " owner conflict"
				wantEnabled = false
			case "missing disabled":
				route.Enabled = false
				wantEnabled = false
				wantChanged = false
			case "DNS pending new":
				svc.RoutingEligible = false
				wantEnabled = false
			case "DNS pending inactive":
				svc.RoutingEligible = false
				route.Enabled = false
				wantEnabled = false
			case "DNS pending active":
				svc.RoutingEligible = false
				wantChanged = false
			case "precedence conflict":
				route.Enabled = false
				route.Status = model.RouteError
				route.LastError = discoveryRouteConflictPrefix + " owner conflict"
				wantEnabled = false
				wantChanged = false
			case "external domain":
				d.Source = model.SourceAPI
				observed.TLSMode = model.TLSManaged
				observed.HTTPSEnabled = true
				wantChanged = false
			case "domain disabled":
				d.Enabled = false
				wantChanged = false
			case "enable managed TLS":
				observed.TLSMode = model.TLSManaged
				observed.HTTPSEnabled = true
				wantChanged = false
			case "disable managed TLS":
				d.TLSMode = model.TLSManaged
				d.ACMEChallenge = model.ACMEDNS01
				d.DNSProviderRef = "provider"
				d.HTTPSEnabled = true
				wantChanged = false
			case "redirect":
				observed.HTTPSRedirect = true
				wantChanged = false
			case "HTTP entrypoint":
				d.HTTPEnabled = false
				wantChanged = false
			case "HTTPS entrypoint":
				d.HTTPSEnabled = true
				wantChanged = false
			}
			if e := s.CreateDomain(ctx, &d); e != nil {
				t.Fatal(e)
			}
			if name == "domain changed" {
				old := d
				old.ID = "old-domain"
				old.Host = "old.example.test"
				if e := s.CreateDomain(ctx, &old); e != nil {
					t.Fatal(e)
				}
			}
			existing := map[string]model.Route{}
			if name != "DNS pending new" {
				if e := s.CreateRoute(ctx, &route); e != nil {
					t.Fatal(e)
				}
				existing[route.ExternalKey] = route
			}
			routes := []model.DiscoveredRoute{observed}
			if name == "missing" || name == "missing conflict" || name == "missing disabled" {
				routes = nil
			}
			domains := map[string]model.Domain{d.Host: d}
			tx, e := s.db.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			changed, e := reconcileRoutesTx(ctx, s, tx, rt.PoolID, svc, routes, domains, existing, now)
			if e != nil {
				t.Fatal(e)
			}
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
			if changed != wantChanged {
				t.Fatalf("route changed=%t want=%t", changed, wantChanged)
			}
			actual := existing[route.ExternalKey]
			stored, e := s.GetRoute(ctx, actual.ID)
			if e != nil {
				t.Fatal(e)
			}
			if stored.Enabled != wantEnabled || stored.Enabled != actual.Enabled || stored.LastError != actual.LastError {
				t.Fatalf("route state not retained: %+v", stored)
			}
			if len(routes) > 0 && (stored.Path != observed.Path || stored.TargetPort != observed.TargetPort || stored.UpstreamProtocol != observed.UpstreamProtocol || stored.StripPath != observed.StripPath || stored.Priority != observed.Priority || stored.WebsocketEnabled != observed.WebsocketEnabled || stored.UpstreamServerName != observed.UpstreamServerName || stored.DomainID != d.ID) {
				t.Fatal("route update lost matching or upstream behavior")
			}
			domain, e := s.GetDomain(ctx, d.ID)
			if e != nil {
				t.Fatal(e)
			}
			if name == "external domain" {
				if domain.TLSMode != d.TLSMode || domain.HTTPSEnabled != d.HTTPSEnabled {
					t.Fatal("discovery overwrote externally owned domain")
				}
			} else if len(routes) > 0 {
				if domain.TLSMode != observed.TLSMode || !domain.Enabled {
					t.Fatal("discovery policy not reconciled")
				}
			}
			if name == "enable managed TLS" {
				cert, e := s.GetCertificate(ctx, domain.CertificateID)
				if e != nil || cert.Status != model.CertificatePending || cert.PrimaryDomain != d.Host || cert.ACMEChallenge != model.ACMEHTTP01 {
					t.Fatal("TLS enabled without tracked pending certificate", e)
				}
			}
			if name == "disable managed TLS" && (domain.CertificateID != "" || domain.ACMEChallenge != "" || domain.DNSProviderRef != "") {
				t.Fatal("disabled TLS retained issuer responsibility")
			}
		})
	}
}
