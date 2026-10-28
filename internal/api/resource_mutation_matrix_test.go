package api

import (
	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResourceMutationHTTPValidationAndLifecycle(t *testing.T) {
	s, _, runtime, domain, route, _ := resourcePatchFixture(t)
	router := chi.NewRouter()
	router.Route("/api/v1", func(r chi.Router) { s.registerAPI(r) })
	match := func(n int64) map[string]string { return map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, n)} }
	for _, path := range []string{"/gateway-pools", "/runtimes", "/domains", "/routes", "/certificates"} {
		req := httptest.NewRequest("POST", "/api/v1"+path, strings.NewReader("{"))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != 400 {
			t.Fatalf("malformed %s not rejected: %d", path, response.Code)
		}
	}
	for _, tc := range []struct {
		path string
		body any
		want int
	}{
		{"/gateway-pools", map[string]any{}, 400},
		{"/runtimes", map[string]any{"pool_id": runtime.PoolID, "runtime_type": "unsupported", "name": "invalid", "endpoint": "unix:///fixture"}, 400},
		{"/routes", map[string]any{}, 400},
		{"/certificates", map[string]any{}, 400},
	} {
		w := managementRequest(t, router, "POST", tc.path, tc.body, nil)
		if w.Code != tc.want {
			t.Fatalf("invalid %s: %d %s", tc.path, w.Code, w.Body)
		}
	}
	pool := responseData[model.GatewayPool](t, managementRequest(t, router, "POST", "/gateway-pools", map[string]any{"name": "temporary", "backend_network": "fixture", "challenge_upstream": "http://controller:8080"}, nil), 201)
	for _, tc := range []struct {
		body    string
		headers map[string]string
		want    int
	}{{"{}", nil, 428}, {"{", match(pool.Revision), 400}} {
		req := httptest.NewRequest("PATCH", "/api/v1/gateway-pools/"+pool.ID, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("pool patch validation: %d %s", w.Code, w.Body)
		}
	}
	if response := managementRequest(t, router, "PATCH", "/gateway-pools/"+pool.ID, map[string]any{"backend_network": "renamed", "enabled": false}, match(pool.Revision)); response.Code != 400 {
		t.Fatal("network change bypassed the prior disable requirement", response.Code)
	}
	pool = responseData[model.GatewayPool](t, managementRequest(t, router, "PATCH", "/gateway-pools/"+pool.ID, map[string]any{"enabled": false}, match(pool.Revision)), 200)
	pool = responseData[model.GatewayPool](t, managementRequest(t, router, "PATCH", "/gateway-pools/"+pool.ID, map[string]any{"backend_network": "renamed", "challenge_upstream": "http://controller:8081", "enabled": false}, match(pool.Revision)), 200)
	if pool.Name != "temporary" || pool.BackendNetwork != "renamed" || pool.ChallengeUpstream != "http://controller:8081" || pool.Enabled {
		t.Fatal("pool patch changed omitted fields or lost changes")
	}
	pool = responseData[model.GatewayPool](t, managementRequest(t, router, "PATCH", "/gateway-pools/"+pool.ID, map[string]any{"enabled": true}, match(pool.Revision)), 200)
	newRuntime := responseData[model.Runtime](t, managementRequest(t, router, "POST", "/runtimes", map[string]any{"pool_id": pool.ID, "runtime_type": model.RuntimeDocker, "name": "new-isolated-runtime", "endpoint": "unix:///nonexistent-fixture.sock"}, nil), 201)
	if newRuntime.PoolID != pool.ID || newRuntime.ManagementEnabled {
		t.Fatal("runtime creation granted implicit management")
	}
	if w := managementRequest(t, router, "DELETE", "/runtimes/"+newRuntime.ID, nil, match(newRuntime.Revision)); w.Code != 204 {
		t.Fatalf("empty runtime deletion: %d %s", w.Code, w.Body)
	}
	disabledRuntime, err := s.store.GetRuntime(context.Background(), newRuntime.ID)
	if err != nil || disabledRuntime.Enabled || disabledRuntime.Status != model.RuntimeDisabled || disabledRuntime.Revision != newRuntime.Revision+1 {
		t.Fatal("runtime disconnection did not retain a disabled, revisioned record", err)
	}
	current, e := s.store.GetPool(context.Background(), pool.ID)
	if e != nil {
		t.Fatal(e)
	}
	if w := managementRequest(t, router, "DELETE", "/gateway-pools/"+pool.ID, nil, match(current.Revision)); w.Code != 409 {
		t.Fatalf("pool with retained runtime lost dependency protection: %d %s", w.Code, w.Body)
	}
	emptyPool := responseData[model.GatewayPool](t, managementRequest(t, router, "POST", "/gateway-pools", map[string]any{"name": "empty", "backend_network": "fixture", "challenge_upstream": "http://controller:8080"}, nil), 201)
	if w := managementRequest(t, router, "DELETE", "/gateway-pools/"+emptyPool.ID, nil, match(emptyPool.Revision)); w.Code != 204 {
		t.Fatalf("empty pool deletion: %d %s", w.Code, w.Body)
	}
	for _, tc := range []struct {
		body    string
		headers map[string]string
		want    int
	}{{"{}", nil, 428}, {"{", match(1), 400}, {`{"target_port_override":70000}`, match(1), 400}} {
		req := httptest.NewRequest("PATCH", "/api/v1/services/"+route.ServiceID, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("service patch validation: %d %s", w.Code, w.Body)
		}
	}
	service, e := s.store.GetService(context.Background(), route.ServiceID)
	if e != nil {
		t.Fatal(e)
	}
	service = responseData[model.Service](t, managementRequest(t, router, "PATCH", "/services/"+service.ID, map[string]any{"target_port_override": 9090}, match(service.Revision)), 200)
	if service.TargetPortOverride == nil || *service.TargetPortOverride != 9090 {
		t.Fatal("service override not persisted")
	}
	newRoute := responseData[model.Route](t, managementRequest(t, router, "POST", "/routes", map[string]any{"domain_id": domain.ID, "path": "/second/", "backend": map[string]any{"service_id": service.ID, "port": 8080, "protocol": "http"}}, nil), 201)
	if w := managementRequest(t, router, "DELETE", "/routes/"+newRoute.ID, nil, match(newRoute.Revision)); w.Code != 204 {
		t.Fatalf("route delete: %d %s", w.Code, w.Body)
	}
	unusedDomain := responseData[model.Domain](t, managementRequest(t, router, "POST", "/domains", map[string]any{"pool_id": runtime.PoolID, "host": "unused.example.test", "entrypoints": []string{"http"}}, nil), 201)
	if w := managementRequest(t, router, "DELETE", "/domains/"+unusedDomain.ID, nil, match(unusedDomain.Revision)); w.Code != 204 {
		t.Fatalf("unreferenced domain delete: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/gateway-pools/missing", "/runtimes/missing", "/domains/missing", "/routes/missing", "/certificates/missing", "/services/missing"} {
		if w := managementRequest(t, router, http.MethodGet, path, nil, nil); w.Code != 404 {
			t.Fatalf("missing identity %s: %d", path, w.Code)
		}
	}
	if _, e := s.store.GetDomain(context.Background(), domain.ID); e != nil {
		t.Fatal("unrelated active domain deleted", e)
	}
}
