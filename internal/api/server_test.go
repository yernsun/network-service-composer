package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/i18n"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func newTestServer(t *testing.T) (*Server, http.Handler) {
	return newTestServerWithConfig(t, nil)
}

func newTestServerWithConfig(t *testing.T, mutate func(*config.Config)) (*Server, http.Handler) {
	t.Helper()
	store, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "api.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Storage.Driver = "sqlite"
	cfg.Storage.DSN = "unused"
	cfg.Server.GRPCListen = ""
	cfg.Server.PublicURL = "http://controller:8080"
	cfg.Security.AdminToken = "test-admin-token"
	cfg.Security.SessionKey = strings.Repeat("s", 32)
	cfg.Security.PrimaryKeyID = "v1"
	cfg.Security.EncryptionKeys = map[string]string{"v1": base64.StdEncoding.EncodeToString(make([]byte, 32))}
	if mutate != nil {
		mutate(&cfg)
	}
	ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
	if err != nil {
		t.Fatal(err)
	}
	application := core.New(store, cfg, ring)
	server, err := New(application, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	return server, server.Handler()
}

func TestCreateDNS01DomainAndRedactedSettings(t *testing.T) {
	server, handler := newTestServerWithConfig(t, func(cfg *config.Config) {
		cfg.ACME.DNSProviders["cloudflare-prod"] = config.DNSProviderProfile{
			Provider:    "cloudflare",
			Credentials: config.DNSProviderCredentials{APIToken: "must-never-be-returned"},
		}
	})
	pool, err := server.core.CreatePool(context.Background(), core.CreatePoolInput{Name: "dns", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/domains", strings.NewReader(`{"pool_id":"`+pool.ID+`","host":"dns.example.com","entrypoints":["https"],"redirect_http_to_https":false,"tls":{"mode":"managed","acme_challenge":"dns-01","dns_provider_ref":"cloudflare-prod"}}`))
	request.Header.Set("Authorization", "Bearer test-admin-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create DNS-01 domain returned %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Data model.Domain `json:"data"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	certificate, err := server.store.GetCertificate(context.Background(), body.Data.CertificateID)
	if err != nil {
		t.Fatal(err)
	}
	if body.Data.ACMEChallenge != model.ACMEDNS01 || body.Data.DNSProviderRef != "cloudflare-prod" || certificate.ACMEChallenge != model.ACMEDNS01 || certificate.DNSProviderRef != "cloudflare-prod" {
		t.Fatalf("DNS selection was not persisted: domain=%#v certificate=%#v", body.Data, certificate)
	}
	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/v1/domains/"+body.Data.ID, strings.NewReader(`{"tls":{"mode":"managed","acme_challenge":"http-01"}}`))
	patchRequest.Header.Set("Authorization", "Bearer test-admin-token")
	patchRequest.Header.Set("If-Match", `"rev-1"`)
	patchResponse := httptest.NewRecorder()
	handler.ServeHTTP(patchResponse, patchRequest)
	if patchResponse.Code != http.StatusOK {
		t.Fatalf("switch challenge returned %d: %s", patchResponse.Code, patchResponse.Body.String())
	}
	var patched struct {
		Data model.Domain `json:"data"`
	}
	if err = json.Unmarshal(patchResponse.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.Data.ACMEChallenge != model.ACMEHTTP01 || patched.Data.DNSProviderRef != "" || patched.Data.CertificateID == body.Data.CertificateID {
		t.Fatalf("challenge switch did not provision a new HTTP-01 certificate: %#v", patched.Data)
	}
	oldCertificate, err := server.store.GetCertificate(context.Background(), body.Data.CertificateID)
	if err != nil || oldCertificate.Status != model.CertificateRevoked {
		t.Fatalf("old DNS certificate was not queued for revocation: %#v err=%v", oldCertificate, err)
	}

	settingsRequest := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	settingsRequest.Header.Set("Authorization", "Bearer test-admin-token")
	settingsResponse := httptest.NewRecorder()
	handler.ServeHTTP(settingsResponse, settingsRequest)
	if settingsResponse.Code != http.StatusOK || !strings.Contains(settingsResponse.Body.String(), "cloudflare-prod") || !strings.Contains(settingsResponse.Body.String(), `"supported_languages":["en","ja","de","zh-CN","zh-TW","fr","ru","es"]`) || strings.Contains(settingsResponse.Body.String(), "must-never-be-returned") {
		t.Fatalf("settings were not correctly redacted: %d %s", settingsResponse.Code, settingsResponse.Body.String())
	}
}

func TestHTTP01ChallengeRequiresExactHost(t *testing.T) {
	server, handler := newTestServer(t)
	now := time.Now().UTC()
	pool, err := server.core.CreatePool(context.Background(), core.CreatePoolInput{Name: "acme", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080"})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := server.core.CreateDomain(context.Background(), core.CreateDomainInput{PoolID: pool.ID, DomainConfigInput: core.DomainConfigInput{Host: "acme.example.com", EntryPoints: []string{core.EntryPointHTTPS}, TLS: core.DomainTLSInput{Mode: model.TLSManaged}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.store.PutChallenge(context.Background(), storage.Challenge{
		ID: id.New(id.Challenge), CertificateID: domain.CertificateID, Domain: domain.Host, Type: "http-01",
		Token: "challenge-token", KeyAuthorization: "challenge-token.thumbprint", Status: "PRESENTED",
		ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	request := func(host string) *httptest.ResponseRecorder {
		value := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/challenge-token", nil)
		value.Host = host
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, value)
		return response
	}
	if response := request("wrong.example.com"); response.Code != http.StatusNotFound {
		t.Fatalf("wrong host exposed challenge: %d %s", response.Code, response.Body.String())
	}
	if response := request("ACME.EXAMPLE.COM:8080"); response.Code != http.StatusOK || response.Body.String() != "challenge-token.thumbprint" {
		t.Fatalf("exact challenge host failed: %d %s", response.Code, response.Body.String())
	}
}

func TestAPIAuthETagAndPagination(t *testing.T) {
	_, handler := newTestServer(t)
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/gateway-pools", nil))
	if unauthorized.Code != http.StatusUnauthorized || unauthorized.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("unexpected unauthorized response: %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	create := func(name string) map[string]any {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/gateway-pools", strings.NewReader(`{"name":"`+name+`","backend_network":"nsc-ingress","challenge_upstream":"http://controller:8080"}`))
		request.Header.Set("Authorization", "Bearer test-admin-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusCreated || response.Header().Get("ETag") != `"rev-1"` {
			t.Fatalf("unexpected create response: %d %s", response.Code, response.Body.String())
		}
		var body map[string]map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body["data"]
	}
	first := create("edge-a")
	_ = create("edge-b")
	id := first["id"].(string)
	patch := func(etag string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPatch, "/api/v1/gateway-pools/"+id, strings.NewReader(`{"name":"renamed"}`))
		request.Header.Set("Authorization", "Bearer test-admin-token")
		request.Header.Set("If-Match", etag)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := patch(`"rev-1"`); response.Code != http.StatusOK || response.Header().Get("ETag") != `"rev-2"` {
		t.Fatalf("unexpected patch response: %d %s", response.Code, response.Body.String())
	}
	if response := patch(`"rev-1"`); response.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected 412, got %d: %s", response.Code, response.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/gateway-pools?limit=1", nil)
	request.Header.Set("Authorization", "Bearer test-admin-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"next_cursor"`) {
		t.Fatalf("unexpected collection response: %d %s", response.Code, response.Body.String())
	}
}

func TestMutationAuditStoresOnlyRequestDigest(t *testing.T) {
	server, handler := newTestServer(t)
	body := `{"name":"audit-pool","backend_network":"nsc-ingress","challenge_upstream":"http://controller:8080/very-secret-value"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/gateway-pools", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer test-admin-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("audited mutation returned %d: %s", response.Code, response.Body.String())
	}
	sqlStore, ok := server.store.(*storage.SQLStore)
	if !ok {
		t.Fatalf("test store has unexpected type %T", server.store)
	}
	var resourceType, details string
	if err := sqlStore.DB().QueryRow(`SELECT resource_type,details FROM audit_events ORDER BY created_at DESC LIMIT 1`).Scan(&resourceType, &details); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(body))
	if resourceType != "gateway-pools" || strings.Contains(details, "very-secret-value") || !strings.Contains(details, fmt.Sprintf("%x", want)) {
		t.Fatalf("audit event retained request content or lost its digest: resource=%q details=%s", resourceType, details)
	}
}

func TestProblemResponsesAreLocalizedWithoutChangingMachineContract(t *testing.T) {
	server, handler := newTestServer(t)
	pool, err := server.core.CreatePool(context.Background(), core.CreatePoolInput{Name: "i18n", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		accept string
		locale i18n.Locale
	}{
		{accept: "en", locale: i18n.English},
		{accept: "ja-JP", locale: i18n.Japanese},
		{accept: "de-DE", locale: i18n.German},
		{accept: "zh-Hans-SG", locale: i18n.SimplifiedChinese},
		{accept: "zh-Hant-HK", locale: i18n.TraditionalChinese},
		{accept: "fr-FR", locale: i18n.French},
	}
	details := make(map[string]struct{})
	for _, test := range tests {
		t.Run(test.accept, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/domains", strings.NewReader(`{"pool_id":"`+pool.ID+`","host":"i18n.example.com","entrypoints":["https"],"redirect_http_to_https":false,"tls":{"mode":"managed","acme_challenge":"dns-01"}}`))
			request.Header.Set("Authorization", "Bearer test-admin-token")
			request.Header.Set("Accept-Language", test.accept)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			var body problem
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != "validation.dns_ref_required_dns01" || body.Status != http.StatusBadRequest || body.Type != "urn:nsc:problem:validation.dns_ref_required_dns01" {
				t.Fatalf("unstable problem contract: %#v", body)
			}
			if len(body.Errors) != 1 || body.Errors[0].Pointer != "/tls/dns_provider_ref" || body.Errors[0].Code != body.Code {
				t.Fatalf("invalid field errors: %#v", body.Errors)
			}
			if body.Title != i18n.Text(test.locale, "problem.title.validation", nil) || body.Detail != i18n.Text(test.locale, "problem.validation.dns_ref_required_dns01", nil) {
				t.Fatalf("wrong localization for %s: %#v", test.locale, body)
			}
			if got := response.Header().Get("Content-Language"); got != string(test.locale) {
				t.Fatalf("Content-Language = %q", got)
			}
			vary := strings.Join(response.Header().Values("Vary"), ",")
			if !strings.Contains(vary, "Accept-Language") || !strings.Contains(vary, "Cookie") {
				t.Fatalf("Vary = %q", vary)
			}
			details[body.Detail] = struct{}{}
		})
	}
	if len(details) != len(tests) {
		t.Fatalf("expected six localized details, got %d", len(details))
	}
}

func TestLocaleCookieOverridesHeaderAndDoesNotInvalidateSession(t *testing.T) {
	_, handler := newTestServer(t)
	form := url.Values{"username": {"admin"}, "password": {"test-admin-token"}, "next": {"/projects"}}
	login := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	var sessionCookie *http.Cookie
	for _, cookie := range loginResponse.Result().Cookies() {
		if cookie.Name == "nsc_session" {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatal("login did not create a session")
	}

	localeForm := url.Values{"lang": {"de"}, "next": {"/projects"}}
	setLocale := httptest.NewRequest(http.MethodPost, "/locale", strings.NewReader(localeForm.Encode()))
	setLocale.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setLocale.AddCookie(sessionCookie)
	localeResponse := httptest.NewRecorder()
	handler.ServeHTTP(localeResponse, setLocale)
	if localeResponse.Code != http.StatusSeeOther || localeResponse.Header().Get("Location") != "/projects" {
		t.Fatalf("locale update failed: %d %s", localeResponse.Code, localeResponse.Header().Get("Location"))
	}
	var localeCookie *http.Cookie
	for _, cookie := range localeResponse.Result().Cookies() {
		if cookie.Name == localeCookieName {
			localeCookie = cookie
		}
	}
	if localeCookie == nil || localeCookie.Value != "de" || localeCookie.MaxAge != 365*24*60*60 || localeCookie.SameSite != http.SameSiteLaxMode || !localeCookie.HttpOnly {
		t.Fatalf("unexpected locale cookie: %#v", localeCookie)
	}

	page := httptest.NewRequest(http.MethodGet, "/projects", nil)
	page.Header.Set("Accept-Language", "fr")
	page.AddCookie(sessionCookie)
	page.AddCookie(localeCookie)
	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, page)
	if pageResponse.Code != http.StatusOK || pageResponse.Header().Get("Content-Language") != "de" || !strings.Contains(pageResponse.Body.String(), `lang="de"`) {
		t.Fatalf("localized authenticated page failed: %d %s", pageResponse.Code, pageResponse.Body.String())
	}
}

func TestCatalogAndInvalidJSONUseNegotiatedLanguageWithoutDiagnostics(t *testing.T) {
	_, handler := newTestServer(t)
	catalogRequest := httptest.NewRequest(http.MethodGet, "/static/i18n/catalog.json", nil)
	catalogRequest.Header.Set("Accept-Language", "zh-SG")
	catalogResponse := httptest.NewRecorder()
	handler.ServeHTTP(catalogResponse, catalogRequest)
	if catalogResponse.Code != http.StatusOK || catalogResponse.Header().Get("Content-Language") != "zh-CN" || !strings.Contains(catalogResponse.Body.String(), `"nav.overview":"概览"`) {
		t.Fatalf("unexpected catalog response: %d %s", catalogResponse.Code, catalogResponse.Body.String())
	}
	traditionalCatalogRequest := httptest.NewRequest(http.MethodGet, "/static/i18n/catalog.json", nil)
	traditionalCatalogRequest.Header.Set("Accept-Language", "zh-HK")
	traditionalCatalogResponse := httptest.NewRecorder()
	handler.ServeHTTP(traditionalCatalogResponse, traditionalCatalogRequest)
	if traditionalCatalogResponse.Code != http.StatusOK || traditionalCatalogResponse.Header().Get("Content-Language") != "zh-TW" || !strings.Contains(traditionalCatalogResponse.Body.String(), `"nav.overview":"總覽"`) {
		t.Fatalf("unexpected traditional Chinese catalog response: %d %s", traditionalCatalogResponse.Code, traditionalCatalogResponse.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/gateway-pools", strings.NewReader(`{"name":`))
	request.Header.Set("Authorization", "Bearer test-admin-token")
	request.Header.Set("Accept-Language", "fr")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"request.invalid_json"`) {
		t.Fatalf("unexpected invalid JSON response: %d %s", response.Code, response.Body.String())
	}
	for _, leaked := range []string{"unexpected EOF", "syntax error", "offset"} {
		if strings.Contains(response.Body.String(), leaked) {
			t.Fatalf("parser diagnostic %q leaked: %s", leaked, response.Body.String())
		}
	}
}

func TestProblemParamsAndInternalDiagnosticsAreSeparated(t *testing.T) {
	server, handler := newTestServer(t)
	pool, err := server.core.CreatePool(context.Background(), core.CreatePoolInput{Name: "params", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/domains", strings.NewReader(`{"pool_id":"`+pool.ID+`","host":"params.example.com","entrypoints":["https"],"redirect_http_to_https":false,"tls":{"mode":"managed","acme_challenge":"dns-01","dns_provider_ref":"missing-profile"}}`))
	request.Header.Set("Authorization", "Bearer test-admin-token")
	request.Header.Set("Accept-Language", "de")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var body problem
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "validation.dns_ref_unconfigured" || body.Params["reference"] != "missing-profile" || !strings.Contains(body.Detail, "missing-profile") || len(body.Errors) != 1 || body.Errors[0].Params["reference"] != "missing-profile" {
		t.Fatalf("problem params were not preserved: %#v", body)
	}

	secretDiagnostic := "database failed with password super-secret-value"
	internalRequest := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	internalRequest = internalRequest.WithContext(i18n.WithLocale(internalRequest.Context(), i18n.French))
	internalResponse := httptest.NewRecorder()
	server.handleError(internalResponse, internalRequest, errors.New(secretDiagnostic))
	if strings.Contains(internalResponse.Body.String(), secretDiagnostic) || strings.Contains(internalResponse.Body.String(), "super-secret-value") || !strings.Contains(internalResponse.Body.String(), `"code":"internal.operation_failed"`) {
		t.Fatalf("internal diagnostic leaked or stable code missing: %s", internalResponse.Body.String())
	}
}

func TestUISessionRequiresCSRFForLogout(t *testing.T) {
	server, handler := newTestServer(t)
	wrongUser := url.Values{"username": {"not-admin"}, "password": {"test-admin-token"}, "next": {"/projects"}}
	wrongLogin := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(wrongUser.Encode()))
	wrongLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wrongResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongResponse, wrongLogin)
	if wrongResponse.Code != http.StatusUnauthorized {
		t.Fatalf("wrong username returned %d", wrongResponse.Code)
	}
	form := url.Values{"username": {"admin"}, "password": {"test-admin-token"}, "next": {"/projects"}}
	login := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusFound {
		t.Fatalf("login failed: %d %s", loginResponse.Code, loginResponse.Body.String())
	}
	result := loginResponse.Result()
	var cookie *http.Cookie
	for _, candidate := range result.Cookies() {
		if candidate.Name == "nsc_session" {
			cookie = candidate
		}
	}
	if cookie == nil {
		t.Fatal("login did not set a session cookie")
	}
	missingCSRF := httptest.NewRequest(http.MethodPost, "/logout", nil)
	missingCSRF.AddCookie(cookie)
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missingCSRF)
	if missingResponse.Code != http.StatusForbidden {
		t.Fatalf("expected CSRF rejection, got %d", missingResponse.Code)
	}
	sessionValue, ok := server.readSession(withCookie(httptest.NewRequest(http.MethodGet, "/", nil), cookie))
	if !ok {
		t.Fatal("could not read issued session")
	}
	logoutForm := url.Values{"csrf_token": {sessionValue.CSRF}}
	logout := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(logoutForm.Encode()))
	logout.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	logout.AddCookie(cookie)
	logoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusFound {
		t.Fatalf("logout failed: %d %s", logoutResponse.Code, logoutResponse.Body.String())
	}
}

func withCookie(request *http.Request, cookie *http.Cookie) *http.Request {
	request.AddCookie(cookie)
	return request
}
