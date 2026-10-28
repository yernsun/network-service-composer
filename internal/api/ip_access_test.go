package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
)

func TestIPAccessDraftAPIAuthorizationAndErrors(t *testing.T) {
	server, handler := newTestServerWithConfig(t, func(cfg *config.Config) { cfg.Management.Enabled = true })
	ctx := context.Background()
	pool, err := server.core.CreatePool(ctx, core.CreatePoolInput{Name: "acl", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080"})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := server.core.CreateDomain(ctx, core.CreateDomainInput{PoolID: pool.ID, DomainConfigInput: core.DomainConfigInput{Host: "acl.api.test", EntryPoints: []string{core.EntryPointHTTP}}})
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf := browserLogin(t, handler)
	path := "/api/v1/gateway-pools/" + pool.ID + "/nginx/draft"
	raw := " 192.168.1.10-192.168.1.20 "
	bodyBytes, _ := json.Marshal(map[string]any{"snippets": []any{}, "access_controls": []any{map[string]any{"scope": "server", "resource_id": domain.ID, "access": []string{raw}, "forbidden": []string{"192.168.1.15"}, "client_ip": map[string]any{"header": "X-Forwarded-For", "trusted_proxies": []string{" 10.0.0.1-10.0.0.9 "}}}}})
	send := func(body, etag, token string, authenticated bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PUT", path, strings.NewReader(body))
		req.Header.Set("If-Match", etag)
		req.Header.Set("X-CSRF-Token", token)
		if authenticated {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := send(string(bodyBytes), `"rev-0"`, csrf, false); response.Code != 401 {
		t.Fatal(response.Code, response.Body)
	}
	if response := send(string(bodyBytes), `"rev-0"`, "", true); response.Code != 403 {
		t.Fatal(response.Code, response.Body)
	}
	response := send(string(bodyBytes), `"rev-0"`, csrf, true)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	var saved struct {
		Data core.NginxConfiguration `json:"data"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &saved); err != nil || len(saved.Data.AccessControls) != 1 || saved.Data.AccessControls[0].Access[0] != raw {
		t.Fatal(response.Body, err)
	}
	if source := saved.Data.AccessControls[0].ClientIP; source == nil || source.Header != "X-Forwarded-For" || source.TrustedProxies[0] != " 10.0.0.1-10.0.0.9 " {
		t.Fatal("client IP source lost in API round trip", source)
	}
	if response = send(string(bodyBytes), `"rev-99"`, csrf, true); response.Code != 412 {
		t.Fatal(response.Code, response.Body)
	}
	invalid := strings.Replace(string(bodyBytes), "192.168.1.15", "192.168.1.20-192.168.1.10", 1)
	response = send(invalid, `"rev-1"`, csrf, true)
	if response.Code != 400 || !strings.Contains(response.Body.String(), `"pointer":"/access_controls/0/forbidden/0"`) {
		t.Fatal(response.Code, response.Body)
	}
	for _, tc := range []struct{ old, value, pointer string }{
		{"X-Forwarded-For", "X;deny", "/access_controls/0/client_ip/header"},
		{" 10.0.0.1-10.0.0.9 ", "10.0.0.9-10.0.0.1", "/access_controls/0/client_ip/trusted_proxies/0"},
	} {
		response = send(strings.Replace(string(bodyBytes), tc.old, tc.value, 1), `"rev-1"`, csrf, true)
		if response.Code != 400 || !strings.Contains(response.Body.String(), `"pointer":"`+tc.pointer+`"`) {
			t.Fatal(response.Code, response.Body)
		}
	}
	active, err := server.core.BuildGatewaySnapshot(ctx, pool.ID)
	if err != nil || len(active.AccessControls) != 0 {
		t.Fatal("API save changed data plane", err)
	}
	current, err := server.core.NginxConfiguration(ctx, pool.ID, "nginx-draft")
	if err != nil || current.Revision != 1 || current.AccessControls[0].Access[0] != raw {
		t.Fatal(current, err)
	}
}
