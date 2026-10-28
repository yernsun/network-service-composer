package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestRecoveryRestoredSessionCookieFlowsDoNotWriteDatabaseOrAudit(t *testing.T) {
	for _, mode := range []string{"form", "json"} {
		t.Run(mode, func(t *testing.T) {
			original, originalHandler := newTestServer(t)
			request := func(handler http.Handler, method, path, body, contentType string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
				t.Helper()
				r := httptest.NewRequest(method, "http://controller:8080"+path, strings.NewReader(body))
				if contentType != "" {
					r.Header.Set("Content-Type", contentType)
				}
				if cookie != nil {
					r.AddCookie(cookie)
				}
				if csrf != "" {
					r.Header.Set("X-CSRF-Token", csrf)
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			cookieNamed := func(w *httptest.ResponseRecorder, name string) *http.Cookie {
				t.Helper()
				for _, cookie := range w.Result().Cookies() {
					if cookie.Name == name {
						return cookie
					}
				}
				t.Fatalf("response omitted cookie %s", name)
				return nil
			}
			loginBody := `{"username":"admin","password":"test-admin-token"}`
			login := request(originalHandler, http.MethodPost, "/api/v1/auth/login", loginBody, "application/json", nil, "")
			if login.Code != http.StatusOK {
				t.Fatal("fixture could not create the original authenticated session")
			}
			inheritedCookie := cookieNamed(login, "nsc_session")
			cfg := original.config
			cfg.Controller.RecoveryMode, cfg.Storage.AutoMigrate = true, false
			cfg.Security.AdminToken = "different-recovery-bootstrap-token"
			recovered, err := New(original.core, cfg, original.logger, func() bool { return true })
			if err != nil {
				t.Fatal(err)
			}
			handler := recovered.Handler()
			store := original.store.(*storage.SQLStore)
			before, err := storage.RecoveryFingerprint(t.Context(), store.DB())
			if err != nil {
				t.Fatal(err)
			}
			audits, err := store.ListAuditEvents(t.Context(), storage.Page{Limit: 200})
			if err != nil {
				t.Fatal(err)
			}
			inherited := request(handler, http.MethodGet, "/api/v1/auth/session", "", "", inheritedCookie, "")
			if inherited.Code != http.StatusOK || !strings.Contains(inherited.Body.String(), `"authenticated":true`) {
				t.Fatal("restored signed session could not inspect its restored identity")
			}
			path, body, contentType, want := "/api/v1/auth/login", loginBody, "application/json", http.StatusOK
			if mode == "form" {
				path, body, contentType, want = "/login", url.Values{"username": {"admin"}, "password": {"test-admin-token"}, "next": {"/projects"}}.Encode(), "application/x-www-form-urlencoded", http.StatusFound
			}
			login = request(handler, http.MethodPost, path, body, contentType, nil, "")
			if login.Code != want || login.Header().Get("X-NSC-Recovery-Mode") != "read-only" {
				t.Fatalf("restored %s login was unavailable: %d", mode, login.Code)
			}
			cookie := cookieNamed(login, "nsc_session")
			if cookie.Value == "" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
				t.Fatal("recovery login did not issue a protected signed cookie")
			}
			sessionResponse := request(handler, http.MethodGet, "/api/v1/auth/session", "", "", cookie, "")
			var sessionBody struct {
				Data struct {
					Authenticated bool   `json:"authenticated"`
					CSRF          string `json:"csrf_token"`
					Username      string `json:"username"`
				} `json:"data"`
			}
			if err := json.Unmarshal(sessionResponse.Body.Bytes(), &sessionBody); err != nil || !sessionBody.Data.Authenticated || sessionBody.Data.Username != "admin" || sessionBody.Data.CSRF == "" {
				t.Fatal("recovery session did not retain authenticated identity and CSRF protection", err)
			}
			for _, readPath := range []string{"/projects", "/api/v1/projects"} {
				w := request(handler, http.MethodGet, readPath, "", "", cookie, "")
				if w.Code != http.StatusOK || w.Header().Get("X-NSC-Recovery-Mode") != "read-only" {
					t.Fatalf("restored cookie read %s: %d", readPath, w.Code)
				}
			}
			uiLocale := request(handler, http.MethodPost, "/locale", url.Values{"lang": {"zh-CN"}, "next": {"/projects"}}.Encode(), "application/x-www-form-urlencoded", cookie, "")
			if uiLocale.Code != http.StatusSeeOther || cookieNamed(uiLocale, localeCookieName).Value != "zh-CN" {
				t.Fatal("recovery UI locale cookie could not be changed")
			}
			apiLocale := request(handler, http.MethodPost, "/api/v1/locale", `{"lang":"fr"}`, "application/json", cookie, sessionBody.Data.CSRF)
			if apiLocale.Code != http.StatusOK || apiLocale.Header().Get("Content-Language") != "fr" || cookieNamed(apiLocale, localeCookieName).Value != "fr" {
				t.Fatal("recovery API locale cookie could not be changed")
			}
			invalidCSRF := request(handler, http.MethodPost, "/api/v1/locale", `{"lang":"fr"}`, "application/json", cookie, "incorrect-csrf")
			if invalidCSRF.Code != http.StatusForbidden || len(invalidCSRF.Result().Cookies()) != 0 {
				t.Fatal("recovery locale exception bypassed session CSRF protection")
			}
			// The router has no HEAD/OPTIONS resource handlers. Their ordinary 405
			// remains distinct from a recovery mutation refusal and performs no write.
			for _, method := range []string{http.MethodHead, http.MethodOptions} {
				w := request(handler, method, "/api/v1/projects", "", "", cookie, "")
				if w.Code != http.StatusMethodNotAllowed || w.Header().Get("X-NSC-Recovery-Mode") != "read-only" {
					t.Fatalf("read-only method %s was treated as a recovery write: %d", method, w.Code)
				}
			}
			mutation := request(handler, http.MethodPost, "/api/v1/projects", `{"name":"must-not-be-written","slug":"recovery-forbidden"}`, "application/json", cookie, sessionBody.Data.CSRF)
			if mutation.Code != http.StatusServiceUnavailable || !strings.Contains(mutation.Body.String(), `"code":"recovery.read_only"`) {
				t.Fatal("authenticated resource mutation escaped recovery protection")
			}
			path, body, contentType, want = "/api/v1/auth/logout", "", "", http.StatusNoContent
			if mode == "form" {
				path, body, contentType, want = "/logout", url.Values{"csrf_token": {sessionBody.Data.CSRF}}.Encode(), "application/x-www-form-urlencoded", http.StatusFound
			}
			logout := request(handler, http.MethodPost, path, body, contentType, cookie, sessionBody.Data.CSRF)
			cleared := cookieNamed(logout, "nsc_session")
			if logout.Code != want || cleared.Value != "" || cleared.MaxAge >= 0 {
				t.Fatal("recovery logout did not clear its client cookie")
			}
			guest := request(handler, http.MethodGet, "/api/v1/auth/session", "", "", cleared, "")
			if guest.Code != http.StatusOK || !strings.Contains(guest.Body.String(), `"authenticated":false`) {
				t.Fatal("cleared recovery cookie retained authenticated state")
			}
			after, err := storage.RecoveryFingerprint(t.Context(), store.DB())
			if err != nil || after != before {
				t.Fatal("recovery session, locale or denied write changed restored database state", err)
			}
			afterAudits, err := store.ListAuditEvents(t.Context(), storage.Page{Limit: 200})
			if err != nil || !reflect.DeepEqual(afterAudits, audits) {
				t.Fatal("recovery browser flows wrote an audit event", err)
			}
		})
	}
}

func TestNormalModeFalseReadyCallbackDoesNotChangeDatabase(t *testing.T) {
	original, _ := newTestServer(t)
	called := false
	server, err := New(original.core, original.config, original.logger, func() bool { called = true; return false })
	if err != nil {
		t.Fatal(err)
	}
	store := original.store.(*storage.SQLStore)
	before, err := storage.RecoveryFingerprint(t.Context(), store.DB())
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if !called || w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"code":"system.not_ready"`) || w.Header().Get("X-NSC-Recovery-Mode") != "" {
		t.Fatal("normal-mode readiness did not honor its unavailable runtime callback")
	}
	after, err := storage.RecoveryFingerprint(t.Context(), store.DB())
	if err != nil || after != before {
		t.Fatal("normal-mode readiness probe changed database state", err)
	}
}
