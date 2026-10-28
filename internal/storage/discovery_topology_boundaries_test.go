package storage

import (
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
)

func topologyBoundaryFixture() (model.RuntimeSnapshot, discoveryState) {
	observed := model.DiscoveredService{ServiceKey: "project/web", ExternalID: "object", Enabled: true, TargetPort: 80, Protocol: "http", Routes: []model.DiscoveredRoute{{ExternalKey: "public", Host: "app.example.test", Path: "/", TLSMode: model.TLSDisabled, HTTPEnabled: true, UpstreamProtocol: "http"}}}
	service := model.Service{ID: "service", ServiceKey: observed.ServiceKey, DiscoveredEnabled: true, DiscoveredPort: 80, DiscoveredProtocol: "http", Status: model.ServiceAvailable}
	domain := model.Domain{ID: "domain", Host: "app.example.test", TLSMode: model.TLSDisabled, HTTPEnabled: true, Enabled: true, Source: model.SourceDiscovery}
	route := model.Route{ID: "route", DomainID: "domain", ServiceID: "service", ExternalKey: "public", Path: "/", UpstreamProtocol: "http", Enabled: true, Source: model.SourceDiscovery}
	return model.RuntimeSnapshot{Services: []model.DiscoveredService{observed}}, discoveryState{servicesByExternalID: map[string]model.Service{observed.ServiceKey: service}, domainsByHost: map[string]model.Domain{domain.Host: domain}, routesByServiceID: map[string]map[string]model.Route{service.ID: {"public": route}}}
}

func TestDiscoveryTopologyRecognizesRouteAndDomainChanges(t *testing.T) {
	for name, change := range map[string]func(*model.DiscoveredRoute){
		"new host":          func(r *model.DiscoveredRoute) { r.Host = "new.example.test" },
		"new route":         func(r *model.DiscoveredRoute) { r.ExternalKey = "additional" },
		"TLS":               func(r *model.DiscoveredRoute) { r.TLSMode = model.TLSManaged },
		"redirect":          func(r *model.DiscoveredRoute) { r.HTTPSRedirect = true },
		"HTTP disabled":     func(r *model.DiscoveredRoute) { r.HTTPEnabled = false; r.HTTPSEnabled = true },
		"HTTPS added":       func(r *model.DiscoveredRoute) { r.HTTPSEnabled = true },
		"path":              func(r *model.DiscoveredRoute) { r.Path = "/api/" },
		"priority":          func(r *model.DiscoveredRoute) { r.Priority = 10 },
		"strip":             func(r *model.DiscoveredRoute) { r.StripPath = true },
		"port":              func(r *model.DiscoveredRoute) { r.TargetPort = 8080 },
		"upstream protocol": func(r *model.DiscoveredRoute) { r.UpstreamProtocol = "https" },
		"SNI":               func(r *model.DiscoveredRoute) { r.UpstreamServerName = "origin.example.test" },
		"websocket":         func(r *model.DiscoveredRoute) { r.WebsocketEnabled = true },
	} {
		t.Run(name, func(t *testing.T) {
			snapshot, state := topologyBoundaryFixture()
			change(&snapshot.Services[0].Routes[0])
			if !discoveryTopologyMayChange(snapshot, state) {
				t.Fatal("request handling change classified as metadata")
			}
		})
	}
	for _, kind := range []string{"disabled domain", "missing managed certificate", "route domain", "route disabled"} {
		t.Run(kind, func(t *testing.T) {
			snapshot, state := topologyBoundaryFixture()
			domain := state.domainsByHost["app.example.test"]
			route := state.routesByServiceID["service"]["public"]
			switch kind {
			case "disabled domain":
				domain.Enabled = false
			case "missing managed certificate":
				domain.TLSMode = model.TLSManaged
				snapshot.Services[0].Routes[0].TLSMode = model.TLSManaged
			case "route domain":
				route.DomainID = "old-domain"
			case "route disabled":
				route.Enabled = false
			}
			state.domainsByHost[domain.Host] = domain
			state.routesByServiceID["service"]["public"] = route
			if !discoveryTopologyMayChange(snapshot, state) {
				t.Fatal("restored route dependency ignored")
			}
		})
	}
}

func TestDiscoveryTopologyServiceRemovalAndOwnershipBoundaries(t *testing.T) {
	for _, kind := range []string{"unchanged", "new unrouted service", "new routed service", "service disabled", "service removed", "already removed", "disabled removed route", "conflicted removed route", "route removed", "old disabled route", "old conflicted route", "active conflict unchanged", "external domain", "managed certificate retained", "runtime metadata"} {
		t.Run(kind, func(t *testing.T) {
			snapshot, state := topologyBoundaryFixture()
			want := false
			switch kind {
			case "new unrouted service":
				snapshot.Services = append(snapshot.Services, model.DiscoveredService{ServiceKey: "new"})
			case "new routed service":
				snapshot.Services = append(snapshot.Services, model.DiscoveredService{ServiceKey: "new", Routes: []model.DiscoveredRoute{{Host: "new.test"}}})
				want = true
			case "service disabled":
				snapshot.Services[0].Enabled = false
				want = true
			case "service removed":
				snapshot.Services = nil
				want = true
			case "already removed":
				snapshot.Services = nil
				v := state.servicesByExternalID["project/web"]
				v.Status = model.ServiceRemoved
				state.servicesByExternalID["project/web"] = v
			case "disabled removed route", "conflicted removed route":
				snapshot.Services = nil
				r := state.routesByServiceID["service"]["public"]
				r.Enabled = false
				if kind == "conflicted removed route" {
					r.LastError = discoveryRouteConflictPrefix + "another route"
					want = true
				}
				state.routesByServiceID["service"]["public"] = r
			case "route removed":
				snapshot.Services[0].Routes = nil
				want = true
			case "old disabled route", "old conflicted route":
				r := model.Route{ID: "removed", Enabled: false}
				if kind == "old conflicted route" {
					r.LastError = discoveryRouteConflictPrefix + "another route"
					want = true
				}
				state.routesByServiceID["service"]["removed"] = r
			case "active conflict unchanged":
				r := state.routesByServiceID["service"]["public"]
				r.Enabled = false
				r.LastError = discoveryRouteConflictPrefix + "another route"
				state.routesByServiceID["service"]["public"] = r
			case "external domain":
				d := state.domainsByHost["app.example.test"]
				d.Source = model.SourceAPI
				d.HTTPSEnabled = true
				state.domainsByHost[d.Host] = d
			case "managed certificate retained":
				d := state.domainsByHost["app.example.test"]
				d.TLSMode = model.TLSManaged
				d.CertificateID = "certificate"
				state.domainsByHost[d.Host] = d
				snapshot.Services[0].Routes[0].TLSMode = model.TLSManaged
			case "runtime metadata":
				snapshot.Services[0].Name = "new display name"
				snapshot.Services[0].RuntimeObjectID = "replacement container"
				snapshot.Services[0].RunningInstances = 3
			}
			if got := discoveryTopologyMayChange(snapshot, state); got != want {
				t.Fatalf("topology change=%t want %t", got, want)
			}
		})
	}
	if discoveryTopologyMayChange(model.RuntimeSnapshot{}, discoveryState{}) {
		t.Fatal("empty discovery invented a change")
	}
}

func TestDiscoveryServiceOverridesDetermineEffectiveTopology(t *testing.T) {
	for _, kind := range []string{"protocol", "port", "enable", "protocol override", "port override", "enable override", "was invalid", "becomes invalid", "remains invalid"} {
		t.Run(kind, func(t *testing.T) {
			snapshot, state := topologyBoundaryFixture()
			observed := snapshot.Services[0]
			service := state.servicesByExternalID[observed.ServiceKey]
			want := true
			switch kind {
			case "protocol":
				observed.Protocol = "https"
			case "port":
				observed.TargetPort = 8080
			case "enable":
				observed.Enabled = false
			case "protocol override":
				v := "https"
				service.ProtocolOverride = &v
				observed.Protocol = "invalid discovered protocol"
				want = false
			case "port override":
				v := 8080
				service.TargetPortOverride = &v
				observed.TargetPort = 9000
				want = false
			case "enable override":
				v := false
				service.EnabledOverride = &v
				observed.Enabled = true
				want = false
			case "was invalid":
				service.ConfigurationError = "invalid port"
			case "becomes invalid":
				observed.ConfigurationError = "invalid network"
			case "remains invalid":
				service.ConfigurationError = "invalid port"
				observed.ConfigurationError = "invalid network"
				observed.TargetPort = 9000
				want = false
			}
			if got := discoveredServiceChangesDataPlane(service, observed); got != want {
				t.Fatalf("effective topology=%t want %t", got, want)
			}
		})
	}
}
