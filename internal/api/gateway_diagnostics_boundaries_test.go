package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type diagnosticStore struct {
	storage.Store
	pool     model.GatewayPool
	domains  []model.Domain
	routes   []model.Route
	gateways []model.Gateway
	service  model.Service
	runtime  model.Runtime
	snapshot model.Snapshot
	fail     string
}

var diagnosticFailure = errors.New("isolated diagnostic store failure")

func (s *diagnosticStore) GetPool(context.Context, string) (model.GatewayPool, error) {
	if s.fail == "pool" {
		return model.GatewayPool{}, diagnosticFailure
	}
	return s.pool, nil
}
func (s *diagnosticStore) GetService(context.Context, string) (model.Service, error) {
	if s.fail == "service" {
		return model.Service{}, diagnosticFailure
	}
	return s.service, nil
}
func (s *diagnosticStore) GetRuntime(context.Context, string) (model.Runtime, error) {
	if s.fail == "runtime" {
		return model.Runtime{}, diagnosticFailure
	}
	return s.runtime, nil
}
func (s *diagnosticStore) BuildSnapshot(context.Context, string) (model.Snapshot, error) {
	if s.fail == "snapshot" {
		return model.Snapshot{}, diagnosticFailure
	}
	return s.snapshot, nil
}
func diagnosticPage[T any](values []T, p storage.Page, key func(T) string) []T {
	result := []T{}
	for _, v := range values {
		if key(v) > p.After {
			result = append(result, v)
			if len(result) == p.Limit {
				break
			}
		}
	}
	return result
}
func (s *diagnosticStore) ListDomains(_ context.Context, p storage.Page) ([]model.Domain, error) {
	if s.fail == "domains" {
		return nil, diagnosticFailure
	}
	return diagnosticPage(s.domains, p, func(v model.Domain) string { return v.ID }), nil
}
func (s *diagnosticStore) ListRoutes(_ context.Context, p storage.Page) ([]model.Route, error) {
	if s.fail == "routes" {
		return nil, diagnosticFailure
	}
	return diagnosticPage(s.routes, p, func(v model.Route) string { return v.ID }), nil
}
func (s *diagnosticStore) ListGateways(_ context.Context, p storage.Page) ([]model.Gateway, error) {
	if s.fail == "gateways" {
		return nil, diagnosticFailure
	}
	return diagnosticPage(s.gateways, p, func(v model.Gateway) string { return v.ID }), nil
}
func diagnosticFixture(s storage.Store) *diagnosticStore {
	return &diagnosticStore{Store: s, pool: model.GatewayPool{ID: "pool_test", DesiredRevision: 7, ProbeRevision: 3},
		domains: []model.Domain{{ID: "domain_test", PoolID: "pool_test", Host: "app.example.test", HTTPEnabled: true, HTTPSEnabled: true, Enabled: true}},
		routes:  []model.Route{{ID: "route_test", DomainID: "domain_test", ServiceID: "service_test", Path: "/", Enabled: true, Status: model.RouteActive}},
		service: model.Service{ID: "service_test", RuntimeID: "runtime_test", Name: "api", ServiceKey: "project/api", DNSName: "project_api", DNSStatus: model.DNSReady, DiscoveredPort: 8080, DiscoveredProtocol: "http", Status: model.ServiceAvailable},
		runtime: model.Runtime{ID: "runtime_test"}, gateways: []model.Gateway{{ID: "gateway_a", PoolID: "pool_test"}, {ID: "gateway_b", PoolID: "other"}},
	}
}
func diagnosticRequest(t *testing.T, h http.Handler, endpoint string) (int, map[string]json.RawMessage) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/gateway-pools/pool_test/"+endpoint, nil)
	r.Header.Set("Authorization", "Bearer test-admin-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid diagnostic response %d: %s", w.Code, w.Body.String())
	}
	return w.Code, envelope.Data
}
func TestGatewayRouteTraceExplainsEachUnavailableState(t *testing.T) {
	s, h := newTestServer(t)
	for _, tc := range []struct {
		name   string
		mutate func(*diagnosticStore)
		reason string
	}{
		{"ready", func(*diagnosticStore) {}, "ready"},
		{"foreign pool", func(s *diagnosticStore) { s.domains[0].PoolID = "other" }, "domain_not_found"},
		{"foreign host", func(s *diagnosticStore) { s.domains[0].Host = "other.example.test" }, "domain_not_found"},
		{"no matching route", func(s *diagnosticStore) { s.routes[0].Path = "/private" }, "route_not_found"},
		{"route belongs to another domain", func(s *diagnosticStore) { s.routes[0].DomainID = "other" }, "route_not_found"},
		{"domain flag disabled", func(s *diagnosticStore) { s.domains[0].Enabled = false }, "domain_disabled"},
		{"domain status disabled", func(s *diagnosticStore) { s.domains[0].Status = model.DomainDisabled }, "domain_disabled"},
		{"paused route", func(s *diagnosticStore) { s.routes[0].Status = model.RoutePaused }, "route_paused"},
		{"route flag disabled", func(s *diagnosticStore) { s.routes[0].Enabled = false }, "route_disabled"},
		{"route status disabled", func(s *diagnosticStore) { s.routes[0].Status = model.RouteDisabled }, "route_disabled"},
		{"pending route", func(s *diagnosticStore) { s.routes[0].Status = model.RouteStatus("PENDING") }, "pending"},
		{"missing DNS identity", func(s *diagnosticStore) { s.service.DNSName = "" }, "invalid_backend_identity"},
		{"missing target port", func(s *diagnosticStore) { s.service.DiscoveredPort = 0 }, "invalid_backend_identity"},
		{"invalid target port", func(s *diagnosticStore) { s.routes[0].TargetPort = 65536 }, "invalid_backend_identity"},
		{"unsupported upstream", func(s *diagnosticStore) { s.routes[0].UpstreamProtocol = "tcp" }, "invalid_backend_identity"},
		{"missing qualified identity", func(s *diagnosticStore) { s.service.Name = "" }, "invalid_backend_identity"},
		{"DNS not ready", func(s *diagnosticStore) { s.service.DNSStatus = model.DNSStatus("UNKNOWN") }, "dns_unknown"},
		{"explicit TLS target", func(s *diagnosticStore) {
			s.routes[0].TargetPort = 8443
			s.routes[0].UpstreamProtocol = "https"
			s.domains[0].HTTPEnabled = false
			s.domains[0].HTTPSEnabled = false
		}, "ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := diagnosticFixture(s.store)
			tc.mutate(store)
			s.store = store
			status, data := diagnosticRequest(t, h, "route-trace?host=app.example.test&path=/api/value")
			if status != 200 || string(data["reason"]) != fmt.Sprintf("%q", tc.reason) {
				t.Fatalf("trace=%d %#v, expected %s", status, data, tc.reason)
			}
			if tc.reason == "ready" {
				var gateways []model.Gateway
				if err := json.Unmarshal(data["gateways"], &gateways); err != nil || len(gateways) != 1 || gateways[0].ID != "gateway_a" {
					t.Fatalf("trace crossed pool scope: %#v, %v", gateways, err)
				}
				if string(data["upstream_name"]) == `""` {
					t.Fatal("ready trace omitted upstream identity")
				}
			}
		})
	}
}

func TestGatewayDiagnosticsPropagateReadAndInputFailures(t *testing.T) {
	s, h := newTestServer(t)
	for _, operation := range []string{"pool", "domains", "routes", "service", "runtime", "gateways"} {
		t.Run(operation, func(t *testing.T) {
			store := diagnosticFixture(s.store)
			store.fail = operation
			s.store = store
			status, _ := diagnosticRequest(t, h, "route-trace?host=app.example.test&path=/")
			if status != 500 {
				t.Fatalf("read failure became success: %d", status)
			}
		})
	}
	for _, endpoint := range []string{"route-trace?host=bad%2Fhost&path=/", "route-trace?host=app.example.test&path=relative", "config-preview?host=bad%2Fhost"} {
		s.store = diagnosticFixture(s.store)
		status, _ := diagnosticRequest(t, h, endpoint)
		if status != 400 {
			t.Fatalf("invalid input status=%d for %s", status, endpoint)
		}
	}
	for _, operation := range []string{"pool", "snapshot"} {
		store := diagnosticFixture(s.store)
		store.fail = operation
		s.store = store
		status, _ := diagnosticRequest(t, h, "config-preview?host=app.example.test")
		if status != 500 {
			t.Fatalf("preview failure became success: %d", status)
		}
	}
	s.store = diagnosticFixture(s.store)
	status, _ := diagnosticRequest(t, h, "config-preview?host=missing.example.test")
	if status != 404 {
		t.Fatalf("unknown host preview=%d", status)
	}
}

func TestDiagnosticPaginationIncludesAllResources(t *testing.T) {
	store := &diagnosticStore{}
	for i := 0; i < 201; i++ {
		key := fmt.Sprintf("%03d", i)
		store.domains = append(store.domains, model.Domain{ID: key})
		store.routes = append(store.routes, model.Route{ID: key})
		store.gateways = append(store.gateways, model.Gateway{ID: key})
	}
	domains, err := listAllDomains(context.Background(), store)
	if err != nil || len(domains) != 201 || domains[200].ID != "200" {
		t.Fatalf("domain pagination=%d, %v", len(domains), err)
	}
	routes, err := listAllRoutes(context.Background(), store)
	if err != nil || len(routes) != 201 || routes[200].ID != "200" {
		t.Fatalf("route pagination=%d, %v", len(routes), err)
	}
	gateways, err := listAllGateways(context.Background(), store)
	if err != nil || len(gateways) != 201 || gateways[200].ID != "200" {
		t.Fatalf("gateway pagination=%d, %v", len(gateways), err)
	}
}

func TestPreviewFragmentContentDigestAndReadFailure(t *testing.T) {
	root := t.TempDir()
	content := []byte("server { return 503; }\n")
	if err := os.WriteFile(filepath.Join(root, "domain.conf"), content, 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := readPreviewFragment(root, "domain.conf")
	sum := sha256.Sum256(content)
	if err != nil || preview.Config != string(content) || preview.SHA256 != hex.EncodeToString(sum[:]) || preview.File != "domain.conf" {
		t.Fatalf("preview evidence mismatch: %#v, %v", preview, err)
	}
	if _, err := readPreviewFragment(root, "missing"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing fragment reported as valid: %v", err)
	}
}

func TestGatewayConfigPreviewMatchesRenderedAndPausedRoutes(t *testing.T) {
	s, h := newTestServer(t)
	base := s.store
	for _, paused := range []bool{false, true} {
		t.Run(fmt.Sprintf("paused=%t", paused), func(t *testing.T) {
			store := diagnosticFixture(base)
			store.snapshot = model.Snapshot{PoolID: "pool_test", Revision: 7, ChallengeUpstream: "http://controller:8080",
				Domains:  []model.SnapshotDomain{{ID: "domain_other", Host: "other.example.test", HTTPEnabled: true, TLSMode: model.TLSDisabled}, {ID: "domain_test", Host: "app.example.test", HTTPEnabled: true, TLSMode: model.TLSDisabled}},
				Services: map[string]model.SnapshotService{"service_test": {ID: "service_test", QualifiedName: "project_api", DNSName: "project_api", Protocol: "http"}},
				Routes:   []model.SnapshotRoute{{ID: "route_test", Host: "app.example.test", Path: "/", ServiceID: "service_test", TargetPort: 8080, UpstreamProtocol: "http", UpstreamName: "nsc_project_api_01234567", Paused: paused}, {ID: "route_other", Host: "other.example.test", Path: "/", ServiceID: "service_test", TargetPort: 8080, UpstreamProtocol: "http", UpstreamName: "nsc_project_api_01234567"}},
			}
			s.store = store
			status, data := diagnosticRequest(t, h, "config-preview?host=app.example.test")
			if status != 200 {
				t.Fatalf("preview status=%d", status)
			}
			var domain configFragmentPreview
			var upstreams []configFragmentPreview
			if err := json.Unmarshal(data["domain"], &domain); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data["upstreams"], &upstreams); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(domain.Config, "server_name app.example.test") || strings.Contains(domain.Config, "other.example.test") {
				t.Fatalf("preview crossed domain scope: %s", domain.Config)
			}
			digest := sha256.Sum256([]byte(domain.Config))
			if domain.SHA256 != hex.EncodeToString(digest[:]) {
				t.Fatal("rendered domain digest mismatch")
			}
			if paused {
				if len(upstreams) != 0 || !strings.Contains(domain.Config, "return 503") {
					t.Fatalf("paused preview tried to proxy: %#v", upstreams)
				}
			} else if len(upstreams) != 1 || !strings.Contains(upstreams[0].Config, "project_api:8080") {
				t.Fatalf("upstream preview mismatch: %#v", upstreams)
			}
			if string(data["config_revision"]) != "7" || string(data["model_digest"]) == `""` {
				t.Fatal("preview omitted version evidence")
			}
		})
	}
	t.Run("candidate rendering failure", func(t *testing.T) {
		store := diagnosticFixture(base)
		store.snapshot = model.Snapshot{Domains: []model.SnapshotDomain{{ID: "domain_test", Host: "app.example.test"}}}
		s.store = store
		status, _ := diagnosticRequest(t, h, "config-preview?host=app.example.test")
		if status != 500 {
			t.Fatalf("invalid candidate reported success: %d", status)
		}
	})
	t.Run("temporary directory unavailable", func(t *testing.T) {
		store := diagnosticFixture(base)
		store.snapshot = model.Snapshot{Domains: []model.SnapshotDomain{{ID: "domain_test", Host: "app.example.test"}}}
		s.store = store
		t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
		status, _ := diagnosticRequest(t, h, "config-preview?host=app.example.test")
		if status != 500 {
			t.Fatalf("unavailable preview workspace status=%d", status)
		}
	})
}

func TestRouteTraceSelectsConfiguredRuleBeforeFallbacks(t *testing.T) {
	s, h := newTestServer(t)
	base := s.store
	active := model.Route{ID: "route_a", DomainID: "domain_test", ServiceID: "service_test", Path: "/api/", Enabled: true, Status: model.RouteActive, Source: model.SourceAPI}
	for _, tc := range []struct {
		name      string
		other     model.Route
		otherWins bool
	}{
		{"disabled longer path", model.Route{Path: "/api/v1/", Status: model.RouteDisabled}, false},
		{"paused longer path", model.Route{Path: "/api/v1/", Status: model.RoutePaused}, true},
		{"enabled longer path", model.Route{Path: "/api/v1/", Status: model.RouteActive, Enabled: true}, true},
		{"shorter active path", model.Route{Path: "/", Status: model.RouteActive, Enabled: true}, false},
		{"higher source", model.Route{Path: "/api/", Status: model.RouteActive, Enabled: true, Source: model.SourceSystem}, true},
		{"lower source", model.Route{Path: "/api/", Status: model.RouteActive, Enabled: true, Source: model.SourceDiscovery}, false},
		{"higher priority", model.Route{Path: "/api/", Status: model.RouteActive, Enabled: true, Source: model.SourceAPI, Priority: 1}, true},
		{"lower priority", model.Route{Path: "/api/", Status: model.RouteActive, Enabled: true, Source: model.SourceAPI, Priority: -1}, false},
		{"stable identity tie", model.Route{Path: "/api/", Status: model.RouteActive, Enabled: true, Source: model.SourceAPI}, false},
	} {
		for _, reversed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reversed=%t", tc.name, reversed), func(t *testing.T) {
				store := diagnosticFixture(base)
				other := tc.other
				other.ID = "route_b"
				other.DomainID = active.DomainID
				other.ServiceID = active.ServiceID
				store.routes = []model.Route{active, other}
				if reversed {
					store.routes = []model.Route{other, active}
				}
				s.store = store
				status, data := diagnosticRequest(t, h, "route-trace?host=app.example.test&path=/api/v1/orders")
				var matched model.Route
				if err := json.Unmarshal(data["route"], &matched); err != nil {
					t.Fatal(err)
				}
				want := active.ID
				if tc.otherWins {
					want = other.ID
				}
				if status != 200 || matched.ID != want {
					t.Fatalf("selected=%s, expected %s (%d)", matched.ID, want, status)
				}
			})
		}
	}
}
