package api

import (
	"context"
	"encoding/json"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCurrentNginxPreviewAPI(t *testing.T) {
	server, handler := newTestServerWithConfig(t, func(cfg *config.Config) { cfg.Management.Enabled = true })
	pool, e := server.core.CreatePool(context.Background(), core.CreatePoolInput{Name: "preview", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080"})
	if e != nil {
		t.Fatal(e)
	}
	domain, e := server.core.CreateDomain(context.Background(), core.CreateDomainInput{PoolID: pool.ID, DomainConfigInput: core.DomainConfigInput{Host: "one.preview.test", EntryPoints: []string{core.EntryPointHTTP}}})
	if e != nil {
		t.Fatal(e)
	}
	_, e = server.core.CreateDomain(context.Background(), core.CreateDomainInput{PoolID: pool.ID, DomainConfigInput: core.DomainConfigInput{Host: "two.preview.test", EntryPoints: []string{core.EntryPointHTTP}}})
	if e != nil {
		t.Fatal(e)
	}
	cookie, csrf := browserLogin(t, handler)
	path := "/api/v1/gateway-pools/" + pool.ID + "/nginx/preview"
	body := `{"snippets":[{"scope":"http","content":"gzip on;"}]}`
	if response := browserRequest(handler, "POST", path, body, cookie, ""); response.Code != 403 {
		t.Fatal(response.Code, response.Body)
	}
	response := browserRequest(handler, "POST", path, body, cookie, csrf)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(response.Code, response.Body)
	}
	var result struct {
		Data core.NginxPreview `json:"data"`
	}
	if e = json.Unmarshal(response.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(result.Data.Candidate.Content, "gzip on;") || strings.Contains(result.Data.Active.Content, "gzip on;") {
		t.Fatal(response.Body)
	}
	if result.Data.DomainID != domain.ID || !strings.Contains(result.Data.Candidate.Content, "server_name one.preview.test;") || strings.Contains(result.Data.Candidate.Content, "server_name two.preview.test;") {
		t.Fatal(response.Body)
	}
	if strings.Contains(response.Body.String(), "PRIVATE KEY") {
		t.Fatal("preview exposed private material")
	}
	invalid := browserRequest(handler, "POST", path, `{"snippets":[{"scope":"http","content":"gzip on;\ninclude /tmp/no;"}]}`, cookie, csrf)
	if invalid.Code != 400 || !strings.Contains(invalid.Body.String(), `"pointer":"/snippets/0/content"`) || !strings.Contains(invalid.Body.String(), `"line":2`) {
		t.Fatal(invalid.Code, invalid.Body)
	}
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("Authorization", "Bearer invalid")
	req.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	draft, _ := server.core.NginxConfiguration(context.Background(), pool.ID, "nginx-draft")
	if draft.Revision != 0 {
		t.Fatal("preview saved a draft")
	}
}
