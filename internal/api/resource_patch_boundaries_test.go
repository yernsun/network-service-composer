package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
)

func resourcePatchFixture(t *testing.T) (*Server, http.Handler, model.Runtime, model.Domain, model.Route, model.Gateway) {
	t.Helper()
	s, h, rt, pool := managedRuntimeFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	service := model.Service{ID: id.New(id.Service), RuntimeID: rt.ID, ServiceKey: "web", Name: "web", DNSName: "web", DNSStatus: model.DNSReady, DiscoveredProtocol: "http", DiscoveredPort: 8080, DiscoveredEnabled: true, RoutingEligible: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	d, err := s.core.CreateDomain(ctx, core.CreateDomainInput{PoolID: pool.ID, DomainConfigInput: core.DomainConfigInput{Host: "app.example.test", EntryPoints: []string{core.EntryPointHTTP}, TLS: core.DomainTLSInput{Mode: model.TLSDisabled}}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.core.CreateRoute(ctx, core.CreateRouteInput{DomainID: d.ID, RouteConfigInput: core.RouteConfigInput{Path: "/", Backend: core.RouteBackendInput{ServiceID: service.ID, Port: 8080, Protocol: "http"}}})
	if err != nil {
		t.Fatal(err)
	}
	g := model.Gateway{ID: id.New(id.Gateway), PoolID: pool.ID, Name: "gateway", Enabled: true, Status: model.GatewayReady, Revision: 1, CreatedAt: now, UpdatedAt: now, LastSeenAt: &now}
	if err = s.store.UpsertGateway(ctx, &g); err != nil {
		t.Fatal(err)
	}
	return s, h, rt, d, r, g
}

func TestResourcePatchMapsFieldsWithoutDroppingOmittedValues(t *testing.T) {
	s, h, rt, d, r, g := resourcePatchFixture(t)
	ctx := context.Background()
	match := func(rev int64) map[string]string { return map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, rev)} }
	patch := map[string]any{"domain_id": d.ID, "path": "/api/", "priority": 7, "strip_path": true, "backend": map[string]any{"service_id": r.ServiceID, "port": 8443, "protocol": "https", "server_name": "UPSTREAM.Example.Test"}, "websocket_enabled": true, "enabled": true}
	updated := responseData[model.Route](t, managementRequest(t, h, "PATCH", "/routes/"+r.ID, patch, match(r.Revision)), 200)
	if updated.Path != "/api/" || updated.Priority != 7 || !updated.StripPath || updated.TargetPort != 8443 || updated.UpstreamProtocol != "https" || updated.UpstreamServerName != "upstream.example.test" || !updated.WebsocketEnabled || !updated.Enabled {
		t.Fatalf("route patch lost fields: %+v", updated)
	}
	omitted := responseData[model.Route](t, managementRequest(t, h, "PATCH", "/routes/"+r.ID, map[string]any{"backend": map[string]any{}}, match(updated.Revision)), 200)
	if omitted.TargetPort != updated.TargetPort || omitted.UpstreamServerName != updated.UpstreamServerName || omitted.Path != updated.Path {
		t.Fatal("empty patch cleared route fields")
	}
	for _, body := range []any{map[string]any{"path": "relative"}, map[string]any{"backend": map[string]any{"port": 65536}}} {
		w := managementRequest(t, h, "PATCH", "/routes/"+r.ID, body, match(omitted.Revision))
		if w.Code != 400 {
			t.Fatalf("invalid route patch HTTP %d: %s", w.Code, w.Body)
		}
	}
	d, err := s.store.GetDomain(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	domain := responseData[model.Domain](t, managementRequest(t, h, "PATCH", "/domains/"+d.ID, map[string]any{"host": "renamed.example.test", "entrypoints": []string{"http"}, "tls": map[string]any{"mode": model.TLSDisabled}, "redirect_http_to_https": false, "http3_enabled": false, "enabled": true}, match(d.Revision)), 200)
	if domain.Host != "renamed.example.test" || !domain.HTTPEnabled || domain.HTTPSEnabled || domain.HTTP3Enabled {
		t.Fatalf("domain patch lost entrypoint selection: %+v", domain)
	}
	runtime := responseData[model.Runtime](t, managementRequest(t, h, "PATCH", "/runtimes/"+rt.ID, map[string]any{"pool_id": rt.PoolID, "name": "Renamed", "endpoint": rt.Endpoint, "credential_ref": "", "enabled": false, "management_enabled": false}, match(rt.Revision)), 200)
	if runtime.Name != "Renamed" || runtime.Enabled || runtime.ManagementEnabled || runtime.Status != model.RuntimeDisabled {
		t.Fatalf("runtime patch lost authorization fields: %+v", runtime)
	}
	runtime = responseData[model.Runtime](t, managementRequest(t, h, "PATCH", "/runtimes/"+rt.ID, map[string]any{"enabled": true}, match(runtime.Revision)), 200)
	if runtime.Status != model.RuntimeInitializing || runtime.Name != "Renamed" || runtime.ManagementEnabled {
		t.Fatal("runtime reenable changed omitted settings")
	}
	g, err = s.store.GetGateway(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	gateway := responseData[model.Gateway](t, managementRequest(t, h, "PATCH", "/gateways/"+g.ID, map[string]any{"name": "Renamed gateway", "enabled": false}, match(g.Revision)), 200)
	if gateway.Enabled || gateway.Status != model.GatewayOffline || gateway.Name != "Renamed gateway" {
		t.Fatalf("disabled gateway falsely online: %+v", gateway)
	}
	gateway = responseData[model.Gateway](t, managementRequest(t, h, "PATCH", "/gateways/"+g.ID, map[string]any{"enabled": true}, match(gateway.Revision)), 200)
	if !gateway.Enabled || gateway.Name != "Renamed gateway" {
		t.Fatal("gateway patch erased name")
	}
	for _, path := range []string{"/gateways", "/gateways/" + g.ID, "/gateways/" + g.ID + "/evidence"} {
		w := managementRequest(t, h, "GET", path, nil, nil)
		if w.Code != 200 {
			t.Fatalf("gateway state unavailable: %d %s", w.Code, w.Body)
		}
	}
}

func TestResourcePatchAndDeleteRequireValidCurrentRevision(t *testing.T) {
	_, h, rt, d, r, g := resourcePatchFixture(t)
	for _, path := range []string{"/runtimes/" + rt.ID, "/domains/" + d.ID, "/routes/" + r.ID, "/gateways/" + g.ID} {
		for _, test := range []struct {
			name, body, match string
			want              int
		}{{"missing precondition", "{}", "", 428}, {"malformed body", "{", `"rev-1"`, 400}, {"stale", "{}", `"rev-999"`, 412}} {
			t.Run(path+"/"+test.name, func(t *testing.T) {
				w := managementRequest(t, h, "PATCH", path, test.body, map[string]string{"If-Match": test.match})
				if w.Code != test.want {
					t.Fatalf("HTTP %d want %d: %s", w.Code, test.want, w.Body)
				}
			})
		}
	}
	for _, kind := range []string{"runtimes", "domains", "routes", "gateways"} {
		w := managementRequest(t, h, "PATCH", "/"+kind+"/missing", "{}", map[string]string{"If-Match": `"rev-1"`})
		if w.Code != 404 {
			t.Fatalf("missing %s patch HTTP %d", kind, w.Code)
		}
	}
	for _, kind := range []string{"gateway-pools", "runtimes", "domains", "routes"} {
		for _, match := range []string{"", `"rev-1"`} {
			w := managementRequest(t, h, "DELETE", "/"+kind+"/missing", nil, map[string]string{"If-Match": match})
			want := 404
			if kind == "gateway-pools" {
				want = 412
			}
			if match == "" {
				want = 428
			}
			if w.Code != want {
				t.Fatalf("delete %s HTTP %d want %d: %s", kind, w.Code, want, w.Body)
			}
		}
	}
}
