package core

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestDirectRouteValidationPreservesExistingBindings(t *testing.T) {
	for _, mode := range []string{"create", "update"} {
		for _, kind := range []string{"missing service", "missing domain", "foreign pool", "DNS missing", "invalid path", "protocol", "negative port", "large port", "server name", "source", "discovery owned"} {
			if mode == "create" && (kind == "source" || kind == "discovery owned") {
				continue
			}
			t.Run(mode+"/"+kind, func(t *testing.T) {
				c, service, input := externalBindingFixture(t)
				ctx := context.Background()
				binding, _, err := c.CreateDomainBinding(ctx, service.ID, input)
				if err != nil {
					t.Fatal(err)
				}
				candidate := binding.Route
				if mode == "create" {
					candidate.Path = "/additional/"
				}
				switch kind {
				case "missing service":
					candidate.ServiceID = ""
				case "missing domain":
					candidate.DomainID = id.New(id.Domain)
				case "foreign pool":
					p, e := c.CreatePool(ctx, CreatePoolInput{Name: "foreign", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080"})
					if e != nil {
						t.Fatal(e)
					}
					d, e := c.CreateDomain(ctx, CreateDomainInput{PoolID: p.ID, DomainConfigInput: *input.Domain})
					if e != nil {
						t.Fatal(e)
					}
					candidate.DomainID = d.ID
				case "DNS missing":
					service.DNSStatus = model.DNSMissing
					if err = c.store.UpsertService(ctx, &service); err != nil {
						t.Fatal(err)
					}
				case "invalid path":
					candidate.Path = "/bad?query"
				case "protocol":
					candidate.UpstreamProtocol = "tcp"
				case "negative port":
					candidate.TargetPort = -1
				case "large port":
					candidate.TargetPort = 65536
				case "server name":
					candidate.UpstreamServerName = "127.0.0.1"
				case "source":
					candidate.Source = model.SourceDiscovery
				case "discovery owned":
					candidate.Source = model.SourceDiscovery
					if _, err = c.store.(*storage.SQLStore).DB().Exec("UPDATE routes SET source=? WHERE id=?", model.SourceDiscovery, candidate.ID); err != nil {
						t.Fatal(err)
					}
				}
				before, err := c.store.GetRoute(ctx, binding.Route.ID)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "create" {
					_, err = c.CreateRoute(ctx, CreateRouteInput{DomainID: candidate.DomainID, RouteConfigInput: RouteConfigInput{Path: candidate.Path, Backend: RouteBackendInput{ServiceID: candidate.ServiceID, Port: candidate.TargetPort, Protocol: candidate.UpstreamProtocol, ServerName: candidate.UpstreamServerName}}})
				} else {
					_, err = c.UpdateRoute(ctx, candidate, candidate.Revision)
				}
				if err == nil {
					t.Fatal("invalid route accepted")
				}
				after, e := c.store.GetRoute(ctx, binding.Route.ID)
				if e != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("rejected update changed active binding: %+v %v", after, e)
				}
				routes, e := c.store.ListRoutes(ctx, storage.Page{})
				if e != nil || len(routes) != 1 {
					t.Fatalf("invalid create persisted route: %+v %v", routes, e)
				}
			})
		}
	}
}

func TestDirectRouteUsesServiceDefaultsAndCanPauseMissingTarget(t *testing.T) {
	c, service, input := externalBindingFixture(t)
	ctx := context.Background()
	binding, _, err := c.CreateDomainBinding(ctx, service.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	route, err := c.CreateRoute(ctx, CreateRouteInput{DomainID: binding.Domain.ID, RouteConfigInput: RouteConfigInput{Path: "/api", Backend: RouteBackendInput{ServiceID: service.ID}}})
	if err != nil || route.TargetPort != 8080 || route.UpstreamProtocol != "http" || route.Path != "/api/" {
		t.Fatalf("service defaults lost: %+v %v", route, err)
	}
	route.UpstreamProtocol = " HTTPS "
	route.UpstreamServerName = "ORIGIN.Example."
	route.TargetPort = 443
	route.WebsocketEnabled = true
	route.StripPath = true
	route.Priority = 5
	route, err = c.UpdateRoute(ctx, route, route.Revision)
	if err != nil || route.UpstreamProtocol != "https" || route.UpstreamServerName != "origin.example" || !route.WebsocketEnabled || !route.StripPath || route.Priority != 5 {
		t.Fatalf("route update lost settings: %+v %v", route, err)
	}
	service.DNSStatus = model.DNSMissing
	if err = c.store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	route.Enabled = false
	route, err = c.UpdateRoute(ctx, route, route.Revision)
	if err != nil || route.Status != model.RoutePaused {
		t.Fatalf("could not pause lost target: %+v %v", route, err)
	}
	route.Enabled = true
	if _, err = c.UpdateRoute(ctx, route, route.Revision); err == nil {
		t.Fatal("lost target enabled")
	}
	service.DNSStatus = model.DNSReady
	if err = c.store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	route, err = c.UpdateRoute(ctx, route, route.Revision)
	if err != nil || route.Status != model.RouteActive {
		t.Fatalf("recovered target could not enable: %+v %v", route, err)
	}
	if _, err = c.UpdateRoute(ctx, route, route.Revision-1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale route overwritten: %v", err)
	}
}

func TestServiceOverridePatchPreservesOmittedAndClearsExplicitNull(t *testing.T) {
	c, service, _ := externalBindingFixture(t)
	ctx := context.Background()
	protocol, port, enabled := " HTTPS ", 8443, false
	readiness := model.ReadinessPolicy{Mode: model.ReadinessHTTP, Path: "/health", Scheme: " HTTPS ", Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1}
	got, err := c.UpdateService(ctx, service.ID, ServicePatch{ProtocolOverride: Nullable[string]{Set: true, Value: &protocol}, TargetPortOverride: Nullable[int]{Set: true, Value: &port}, EnabledOverride: Nullable[bool]{Set: true, Value: &enabled}, ReadinessOverride: Nullable[model.ReadinessPolicy]{Set: true, Value: &readiness}}, service.Revision)
	if err != nil || got.ProtocolOverride == nil || *got.ProtocolOverride != "https" || got.TargetPortOverride == nil || *got.TargetPortOverride != 8443 || got.EnabledOverride == nil || *got.EnabledOverride || got.ReadinessOverride == nil || got.ReadinessOverride.Scheme != "https" {
		t.Fatalf("override patch: %+v %v", got, err)
	}
	preserved, err := c.UpdateService(ctx, service.ID, ServicePatch{}, got.Revision)
	if err != nil || !reflect.DeepEqual(preserved.ProtocolOverride, got.ProtocolOverride) || !reflect.DeepEqual(preserved.ReadinessOverride, got.ReadinessOverride) || !reflect.DeepEqual(preserved.TargetPortOverride, got.TargetPortOverride) || !reflect.DeepEqual(preserved.EnabledOverride, got.EnabledOverride) {
		t.Fatalf("omitted override lost: %+v %v", preserved, err)
	}
	cleared, err := c.UpdateService(ctx, service.ID, ServicePatch{ProtocolOverride: Nullable[string]{Set: true}, TargetPortOverride: Nullable[int]{Set: true}, EnabledOverride: Nullable[bool]{Set: true}, ReadinessOverride: Nullable[model.ReadinessPolicy]{Set: true}}, preserved.Revision)
	if err != nil || cleared.ProtocolOverride != nil || cleared.TargetPortOverride != nil || cleared.EnabledOverride != nil || cleared.ReadinessOverride != nil || cleared.EffectivePort() != service.EffectivePort() || cleared.EffectiveProtocol() != service.EffectiveProtocol() {
		t.Fatalf("null did not restore discovery defaults: %+v %v", cleared, err)
	}
	if _, err = c.UpdateService(ctx, service.ID, ServicePatch{}, got.Revision); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale service overwritten: %v", err)
	}
}

func TestServiceInvalidOverridesAreAtomic(t *testing.T) {
	for _, kind := range []string{"protocol", "zero port", "large port", "readiness"} {
		t.Run(kind, func(t *testing.T) {
			c, service, _ := externalBindingFixture(t)
			ctx := context.Background()
			original, err := c.store.GetService(ctx, service.ID)
			if err != nil {
				t.Fatal(err)
			}
			protocol := "https"
			patch := ServicePatch{ProtocolOverride: Nullable[string]{Set: true, Value: &protocol}}
			switch kind {
			case "protocol":
				protocol = "tcp"
			case "zero port", "large port":
				port := 0
				if kind == "large port" {
					port = 65536
				}
				patch.TargetPortOverride = Nullable[int]{Set: true, Value: &port}
			case "readiness":
				patch.ReadinessOverride = Nullable[model.ReadinessPolicy]{Set: true, Value: &model.ReadinessPolicy{Mode: "invalid"}}
			}
			if _, err = c.UpdateService(ctx, service.ID, patch, service.Revision); err == nil {
				t.Fatal("invalid override accepted")
			}
			got, e := c.store.GetService(ctx, service.ID)
			if e != nil || !reflect.DeepEqual(got, original) {
				t.Fatalf("partial override committed: %+v %v", got, e)
			}
		})
	}
}
