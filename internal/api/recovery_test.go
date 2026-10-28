package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestRecoveryManagementUsesRestoredIdentitiesWithoutAnyDatabaseWrites(t *testing.T) {
	original, _ := newTestServer(t)
	cfg := original.config
	cfg.Controller.RecoveryMode = true
	cfg.Storage.AutoMigrate = false
	cfg.Security.AdminToken = "different-configured-bootstrap-token"
	recovered, err := New(original.core, cfg, original.logger, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	handler := recovered.Handler()
	store := original.store.(*storage.SQLStore)
	ctx := context.Background()
	before, err := storage.RecoveryFingerprint(ctx, store.DB())
	if err != nil {
		t.Fatal(err)
	}
	audits, err := store.ListAuditEvents(ctx, storage.Page{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []struct {
		path, token string
		status      int
	}{
		{"/api/v1/projects", "", 401},
		{"/api/v1/projects", "test-admin-token", 200},
		{"/api/v1/settings", "test-admin-token", 200},
		{"/api/v1/projects", cfg.Security.AdminToken, 401},
		{"/healthz", "", 200}, {"/readyz", "", 503},
		{"/.well-known/acme-challenge/old-token", "", 503},
	} {
		r := httptest.NewRequest(http.MethodGet, candidate.path, nil)
		if candidate.token != "" {
			r.Header.Set("Authorization", "Bearer "+candidate.token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != candidate.status || w.Header().Get("X-NSC-Recovery-Mode") != "read-only" {
			t.Fatalf("recovery read %s: %d %s", candidate.path, w.Code, w.Body)
		}
	}
	checked := 0
	if err := chi.Walk(handler.(chi.Routes), func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions || path == "/login" || path == "/logout" || path == "/locale" || path == "/api/v1/locale" || strings.HasPrefix(path, "/api/v1/auth/") {
			return nil
		}
		checked++
		r := httptest.NewRequest(method, path, strings.NewReader(`{"invalid":`))
		r.Header.Set("Authorization", "Bearer test-admin-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 503 || !strings.Contains(w.Body.String(), `"code":"recovery.read_only"`) {
			t.Fatalf("resource mutation escaped restore protection: %s %s %d %s", method, path, w.Code, w.Body)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if checked < 20 {
		t.Fatal("recovery write guard did not cover actual router")
	}
	for _, password := range []string{"test-admin-token", "wrong-password"} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"`+password+`"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := 401
		if password == "test-admin-token" {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("restored login failed: %d %s", w.Code, w.Body)
		}
	}
	after, err := storage.RecoveryFingerprint(ctx, store.DB())
	if err != nil || after != before {
		t.Fatal("inspection or authentication changed restored state", err)
	}
	afterAudits, err := store.ListAuditEvents(ctx, storage.Page{Limit: 200})
	if err != nil || len(afterAudits) != len(audits) {
		t.Fatal("recovery authentication persisted audit writes", err)
	}
}

func TestRecoveryAPIConstructionDoesNotImportMissingBootstrapIdentity(t *testing.T) {
	s, handler := newTestServerWithConfig(t, func(cfg *config.Config) { cfg.Controller.RecoveryMode = true })
	if _, err := s.store.GetControlDocument(context.Background(), "community:identities"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("recovery API imported an installation identity", err)
	}
	if w := managementRequest(t, handler, http.MethodGet, "/projects", nil, nil); w.Code != 401 {
		t.Fatal("unrestored bootstrap credential gained access", w.Code)
	}
}

func TestRecoveryMethodGuardAllowsOnlyReadsAndCookieAuthentication(t *testing.T) {
	s, _ := newTestServerWithConfig(t, func(cfg *config.Config) { cfg.Controller.RecoveryMode = true })
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		called := false
		handler := s.recoveryReadOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(204) }))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "/api/v1/projects", nil))
		if !called || w.Code != 204 {
			t.Fatal("readonly method blocked", method, w.Code)
		}
	}
	for _, path := range []string{"/login", "/logout", "/locale", "/api/v1/auth/login", "/api/v1/auth/logout", "/api/v1/locale", "/api/v1/projects"} {
		called := false
		handler := s.recoveryReadOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(204) }))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		if path == "/api/v1/projects" {
			if called || w.Code != 503 {
				t.Fatal("non-cookie write reached a handler", path, w.Code)
			}
		} else if !called || w.Code != 204 {
			t.Fatal("cookie-only authentication was unavailable", path, w.Code)
		}
	}
}
