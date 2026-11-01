package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/storage"
)

func browserRequest(handler http.Handler, method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}
func browserLogin(t *testing.T, handler http.Handler) (*http.Cookie, string) {
	t.Helper()
	res := browserRequest(handler, "POST", "/api/v1/auth/login", `{"username":"admin","password":"test-admin-token"}`, nil, "")
	if res.Code != 200 {
		t.Fatalf("login: %d %s", res.Code, res.Body)
	}
	var body struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		}
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == "nsc_session" {
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
				t.Fatal("unsafe session cookie")
			}
			return cookie, body.Data.CSRF
		}
	}
	t.Fatal("missing session cookie")
	return nil, ""
}
func TestBrowserSessionAuthenticationAndCSRF(t *testing.T) {
	server, handler := newTestServer(t)
	guest := browserRequest(handler, "GET", "/api/v1/auth/session", "", nil, "")
	if guest.Code != 200 || !strings.Contains(guest.Body.String(), `"authenticated":false`) || strings.Contains(guest.Body.String(), "csrf_token") {
		t.Fatalf("guest session: %s", guest.Body)
	}
	cookie, csrf := browserLogin(t, handler)
	read := browserRequest(handler, "GET", "/api/v1/gateway-pools", "", cookie, "")
	if read.Code != 200 {
		t.Fatalf("cookie read: %d %s", read.Code, read.Body)
	}
	input := `{"name":"browser-pool","backend_network":"nsc-ingress","challenge_upstream":"http://controller:8080"}`
	for _, token := range []string{"", "wrong"} {
		res := browserRequest(handler, "POST", "/api/v1/gateway-pools", input, cookie, token)
		if res.Code != 403 {
			t.Fatalf("CSRF: %d", res.Code)
		}
	}
	created := browserRequest(handler, "POST", "/api/v1/gateway-pools", input, cookie, csrf)
	if created.Code != 201 {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	var actor, details string
	if err := server.store.(*storage.SQLStore).DB().QueryRow(`SELECT actor, details FROM audit_events ORDER BY created_at DESC LIMIT 1`).Scan(&actor, &details); err != nil {
		t.Fatal(err)
	}
	if actor != "user_bootstrap" || strings.Contains(details, "browser-pool") {
		t.Fatalf("incorrect browser audit: %s %s", actor, details)
	}
	for _, authorization := range []string{"", "Bearer invalid", "Basic invalid"} {
		req := httptest.NewRequest("GET", "/api/v1/gateway-pools", nil)
		req.AddCookie(cookie)
		req.Header["Authorization"] = []string{authorization}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != 401 {
			t.Fatalf("invalid explicit authorization fell back to Cookie: %d", res.Code)
		}
	}
	sessionResult := browserRequest(handler, "GET", "/api/v1/auth/session", "", cookie, "")
	if !strings.Contains(sessionResult.Body.String(), csrf) || sessionResult.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("session bootstrap or caching incorrect")
	}
	stale, _ := server.signSession(session{Expires: time.Now().Add(-time.Second).Unix(), CSRF: csrf})
	expired := browserRequest(handler, "GET", "/api/v1/services", "", &http.Cookie{Name: "nsc_session", Value: stale}, "")
	if expired.Code != 401 || expired.Header().Get("Location") != "" {
		t.Fatal("JSON auth must return 401 without redirect")
	}
	if res := browserRequest(handler, "POST", "/api/v1/auth/logout", "{}", cookie, ""); res.Code != 403 {
		t.Fatal("logout accepted without CSRF")
	}
	if res := browserRequest(handler, "POST", "/api/v1/auth/logout", "{}", cookie, csrf); res.Code != 204 || res.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout did not clear session")
	}
}
func TestBrowserLocaleAndOrigin(t *testing.T) {
	_, handler := newTestServer(t)
	cookie, csrf := browserLogin(t, handler)
	for _, lang := range []string{"en", "ja", "de", "zh-CN", "zh-TW", "fr", "ru", "es"} {
		res := browserRequest(handler, "POST", "/api/v1/locale", `{"lang":"`+lang+`"}`, cookie, csrf)
		if res.Code != 200 || res.Header().Get("Content-Language") != lang {
			t.Fatalf("locale %s: %s", lang, res.Body)
		}
	}
	if res := browserRequest(handler, "POST", "/api/v1/locale", `{"lang":"xx"}`, cookie, csrf); res.Code != 400 {
		t.Fatal("unsupported locale accepted")
	}
	if res := browserRequest(handler, "POST", "/api/v1/locale", `{"lang":"de"}`, cookie, ""); res.Code != 403 {
		t.Fatal("authenticated locale without CSRF")
	}
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"test-admin-token"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.invalid")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != 403 {
		t.Fatal("cross-origin login accepted")
	}
}
func TestSPAAssetsAndRouting(t *testing.T) {
	_, handler := newTestServer(t)
	cookie, _ := browserLogin(t, handler)
	root := browserRequest(handler, "GET", "/", "", cookie, "")
	if root.Code != http.StatusFound || root.Header().Get("Location") != "/projects" {
		t.Fatalf("product root redirect: %d %q", root.Code, root.Header().Get("Location"))
	}
	first := browserRequest(handler, "GET", "/services/domains", "", cookie, "")
	second := browserRequest(handler, "GET", "/services/domains", "", cookie, "")
	if first.Code != 200 || !strings.Contains(first.Body.String(), `id="app"`) || strings.Contains(first.Body.String(), "htmx") || strings.Contains(first.Body.String(), "__NSC_") {
		t.Fatalf("SPA shell: %d %s", first.Code, first.Body)
	}
	if first.Header().Get("Cache-Control") != "no-store" || first.Header().Get("Content-Security-Policy") == second.Header().Get("Content-Security-Policy") {
		t.Fatal("nonce must vary and HTML must not cache")
	}
	for _, path := range []string{
		"/projects/project-1/variables",
		"/projects/overview",
		"/projects/project-1/environments/environment-1/applications/application-1/compose",
		"/projects/project-1/environments/environment-1/services/service-1",
		"/projects/project-1/environments/environment-1/domains/domain-1/nginx",
		"/infrastructure/runtimes/runtime-1/nodes/node-1",
		"/infrastructure/runtimes/runtime-1/instances/instance-1",
		"/infrastructure/installation",
		"/infrastructure/installation?job=recorded-operation",
		"/operations/operation-1",
		"/market/catalog-1",
	} {
		res := browserRequest(handler, "GET", path, "", cookie, "")
		if res.Code != 200 || !strings.Contains(res.Body.String(), `id="app"`) {
			t.Fatalf("known SPA route %s: %d", path, res.Code)
		}
	}
	for _, path := range []string{"/ui", "/api/v1/nonexistent", "/assets/missing.js", "/services/domains/id/unknown"} {
		res := browserRequest(handler, "GET", path, "", cookie, "")
		if res.Code != 404 {
			t.Fatalf("%s: %d", path, res.Code)
		}
	}
	if res := browserRequest(handler, "POST", "/infrastructure/gateway-pools", "{}", cookie, ""); res.Code == 200 || res.Code == 201 {
		t.Fatal("legacy mutation route still active")
	}
	entries, err := frontendFiles.ReadDir("frontend-dist/assets")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".js") {
			res := browserRequest(handler, "GET", "/assets/"+entry.Name(), "", nil, "")
			if res.Code != 200 || !strings.Contains(res.Header().Get("Cache-Control"), "immutable") {
				t.Fatal("asset cache policy")
			}
			return
		}
	}
	t.Fatal("missing built JavaScript")
}

func TestSafeNextAllowsOnlyProductNavigationState(t *testing.T) {
	for _, value := range []string{
		"/projects/overview",
		"/projects/all/environments/all/services?perspective=ops",
		"/services?layout=flat&page=2&q=web&runtime_id=rt-1",
		"/operations/nginx?pool_id=pool-1&tab=publish&validation_id=validation-1",
	} {
		if got := safeNext(value); got != value {
			t.Fatalf("safe product destination rejected: %q -> %q", value, got)
		}
	}
	for _, value := range []string{
		"https://outside.invalid/services",
		"//outside.invalid/services",
		"projects",
		"%",
		"/ui",
		"/logout",
		"/services?secret=plain-text",
		"/projects?compose_yaml=services%3A%20%7B%7D",
		"/operations?command=docker%20rm",
		"/services#TOKEN=plain-text",
		"/services?q=" + strings.Repeat("x", 513),
	} {
		if got := safeNext(value); got != "/projects" {
			t.Fatalf("unsafe product destination accepted: %q -> %q", value, got)
		}
	}
}

func TestKnownSPAPathAllowlist(t *testing.T) {
	allowed := []string{
		"/projects/all/environments/all/applications",
		"/projects/all/environments/all/services",
		"/projects/project-1/environments/all/domains",
		"/projects/project-1/environments/environment-1/overview",
		"/projects/project-1/environments/environment-1/configs",
		"/projects/project-1/environments/environment-1/gateways",
		"/projects/project-1/environments/environment-1/events",
		"/projects/project-1/environments/environment-1/workloadHistory",
		"/login",
		"/logout",
		"/projects",
		"/projects/applications",
		"/projects/applications/application-1",
		"/projects/project-1",
		"/projects/project-1/variables",
		"/projects/project-1/environments",
		"/projects/project-1/environments/environment-1",
		"/projects/project-1/environments/environment-1/variables",
		"/projects/project-1/environments/environment-1/compose",
		"/projects/project-1/environments/environment-1/updates",
		"/projects/project-1/environments/environment-1/applications",
		"/projects/project-1/environments/environment-1/services",
		"/projects/project-1/environments/environment-1/domains",
		"/projects/project-1/environments/environment-1/routes",
		"/projects/project-1/environments/environment-1/certificates",
		"/projects/project-1/environments/environment-1/deployments",
		"/projects/project-1/environments/environment-1/applications/application-1",
		"/projects/project-1/environments/environment-1/services/service-1",
		"/projects/project-1/environments/environment-1/domains/domain-1",
		"/projects/project-1/environments/environment-1/routes/route-1",
		"/projects/project-1/environments/environment-1/deployments/deployment-1",
		"/projects/project-1/environments/environment-1/domains/domain-1/nginx",
		"/projects/project-1/environments/environment-1/applications/application-1/compose",
		"/services",
		"/services/service-1",
		"/services/routes/route-1",
		"/services/domains/domain-1",
		"/infrastructure",
		"/infrastructure/nodes",
		"/infrastructure/events",
		"/infrastructure/ha",
		"/infrastructure/installation",
		"/infrastructure/gateway-pools",
		"/infrastructure/gateway-pools/pool-1",
		"/infrastructure/gateways/gateway-1",
		"/infrastructure/certificates/certificate-1",
		"/infrastructure/runtimes",
		"/infrastructure/runtimes/runtime-1",
		"/infrastructure/runtimes/runtime-1/nodes",
		"/infrastructure/runtimes/runtime-1/containers",
		"/infrastructure/runtimes/runtime-1/tasks",
		"/infrastructure/runtimes/runtime-1/instances",
		"/infrastructure/runtimes/runtime-1/nodes/node-1",
		"/infrastructure/runtimes/runtime-1/tasks/task-1",
		"/infrastructure/runtimes/runtime-1/containers/container-1",
		"/infrastructure/runtimes/runtime-1/instances/instance-1",
		"/operations",
		"/operations/operation-1",
		"/market",
		"/market/catalog-1",
	}
	for _, value := range allowed {
		if !knownSPAPath(value) {
			t.Errorf("known product route rejected: %q", value)
		}
	}

	rejected := []string{
		"",
		"projects",
		"/projects/",
		"/projects\\project-1",
		"//projects",
		"/projects/applications/application-1/extra",
		"/projects/project-1/not-environments",
		"/projects/project-1/environments/environment-1/unknown",
		"/projects/project-1/environments/environment-1/certificates/certificate-1",
		"/projects/project-1/environments/environment-1/unknown/resource-1",
		"/projects/project-1/environments/environment-1/domains/domain-1/compose",
		"/projects/project-1/environments/environment-1/applications/application-1/nginx",
		"/projects/project-1/environments/environment-1/services/service-1/extra",
		"/projects/project-1/environments/environment-1/services/service-1/extra/again",
		"/services/unknown/resource-1",
		"/services/routes/route-1/extra",
		"/infrastructure/unknown",
		"/infrastructure/ha/extra",
		"/infrastructure/installation/extra",
		"/infrastructure/gateway-pools/pool-1/extra",
		"/infrastructure/runtimes/runtime-1/unknown",
		"/infrastructure/runtimes/runtime-1/unknown/resource-1",
		"/infrastructure/runtimes/runtime-1/nodes/node-1/extra",
		"/operations/operation-1/extra",
		"/market/catalog-1/extra",
		"/unknown",
		"/",
	}
	for _, value := range rejected {
		if knownSPAPath(value) {
			t.Errorf("unknown product route accepted: %q", value)
		}
	}
}

func TestSafeSPAQueryBounds(t *testing.T) {
	accepted := []string{
		"",
		"job=recorded-operation",
		"q=web&layout=flat&page=2",
		"selected=instance-1&selected_node=node-1&kind=container",
	}
	for _, value := range accepted {
		if !safeSPAQuery(value) {
			t.Errorf("safe query rejected: %q", value)
		}
	}
	rejected := []string{
		"%",
		"secret=value",
		"q=" + strings.Repeat("x", 513),
		"q=%00",
		"q=%0A",
	}
	rejected = append(rejected, strings.Repeat("q=x&", 32)+"q=x")
	for _, value := range rejected {
		if safeSPAQuery(value) {
			t.Errorf("unsafe query accepted: %q", value)
		}
	}
}

func TestCurrentPathDefaultsToProductRoot(t *testing.T) {
	if got := currentPath(&http.Request{URL: &url.URL{}}); got != "/projects" {
		t.Fatalf("empty request path: %q", got)
	}
	request := &http.Request{URL: &url.URL{Path: "/services", RawQuery: "q=web&error=hidden"}}
	if got := currentPath(request); got != "/services?q=web" {
		t.Fatalf("safe request path: %q", got)
	}
	if got := currentPath(&http.Request{URL: &url.URL{Path: "/services"}}); got != "/services" {
		t.Fatalf("request path without query: %q", got)
	}
}
