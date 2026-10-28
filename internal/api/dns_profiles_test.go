package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDNSProfileCredentialWritesRequireReauthenticationAndStayRedacted(t *testing.T) {
	_, handler := newTestServer(t)
	const secret = "private-dns-token-for-api-test"
	const body = `{"reference":"edge-dns","provider":"cloudflare","credentials":{"api_token":"` + secret + `"}}`
	for _, candidate := range []struct {
		password string
		want     int
	}{{"", 403}, {"wrong", 403}, {"test-admin-token", 201}} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/dns-provider-profiles", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-admin-token")
		req.Header.Set("X-NSC-Reauthenticate", candidate.password)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != candidate.want || strings.Contains(w.Body.String(), secret) {
			t.Fatalf("credential response %d: %d %s", candidate.want, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/dns-provider-profiles", "/api/v1/dns-provider-profiles/edge-dns", "/api/v1/settings"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer test-admin-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != 200 || strings.Contains(w.Body.String(), secret) || !strings.Contains(w.Body.String(), "edge-dns") {
			t.Fatalf("profile leaked or missing in %s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestDNSProfileHTTPRotationAndDeletionRequireCurrentRevision(t *testing.T) {
	_, handler := newTestServer(t)
	const path = "/dns-provider-profiles/rotate-me"
	const oldSecret = "old-private-token"
	const newSecret = "new-private-token"
	if response := managementRequest(t, handler, http.MethodPost, "/dns-provider-profiles",
		map[string]any{"reference": "invalid-profile", "provider": "cloudflare", "credentials": map[string]string{}},
		map[string]string{"X-NSC-Reauthenticate": "test-admin-token"}); response.Code < 400 || response.Code >= 500 {
		t.Fatalf("profile without credentials accepted: %d %s", response.Code, response.Body)
	}
	created := responseData[struct {
		Revision int64 `json:"revision"`
	}](t, managementRequest(t, handler, http.MethodPost, "/dns-provider-profiles",
		map[string]any{"reference": "rotate-me", "provider": "cloudflare", "credentials": map[string]string{"api_token": oldSecret}},
		map[string]string{"X-NSC-Reauthenticate": "test-admin-token"}), http.StatusCreated)
	rotatedBody := map[string]any{"reference": "rotate-me", "provider": "cloudflare", "credentials": map[string]string{"api_token": newSecret}}
	if response := managementRequest(t, handler, http.MethodPut, path, rotatedBody, map[string]string{"X-NSC-Reauthenticate": "test-admin-token"}); response.Code != http.StatusPreconditionRequired {
		t.Fatalf("rotation without revision accepted: %d %s", response.Code, response.Body)
	}
	if response := managementRequest(t, handler, http.MethodPut, path, "{", map[string]string{"X-NSC-Reauthenticate": "test-admin-token", "If-Match": `"rev-1"`}); response.Code != http.StatusBadRequest {
		t.Fatalf("malformed rotation accepted: %d %s", response.Code, response.Body)
	}
	if response := managementRequest(t, handler, http.MethodPut, path, rotatedBody, map[string]string{"X-NSC-Reauthenticate": "test-admin-token", "If-Match": `"rev-99"`}); response.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale rotation accepted: %d %s", response.Code, response.Body)
	}
	updated := responseData[struct {
		Revision int64 `json:"revision"`
	}](t, managementRequest(t, handler, http.MethodPut, path, rotatedBody,
		map[string]string{"X-NSC-Reauthenticate": "test-admin-token", "If-Match": `"rev-1"`}), http.StatusOK)
	if updated.Revision <= created.Revision {
		t.Fatalf("rotation revision did not advance: %d -> %d", created.Revision, updated.Revision)
	}
	if response := managementRequest(t, handler, http.MethodPut, path,
		map[string]any{"reference": "another-profile", "provider": "cloudflare", "credentials": map[string]string{"api_token": newSecret}},
		map[string]string{"X-NSC-Reauthenticate": "test-admin-token", "If-Match": `"rev-2"`}); response.Code < 400 || response.Code >= 500 {
		t.Fatalf("profile identity changed through rotation: %d %s", response.Code, response.Body)
	}
	for _, endpoint := range []string{path, "/dns-provider-profiles", "/settings"} {
		response := managementRequest(t, handler, http.MethodGet, endpoint, nil, nil)
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), oldSecret) || strings.Contains(response.Body.String(), newSecret) {
			t.Fatalf("profile read leaked credentials: %s %d %s", endpoint, response.Code, response.Body)
		}
	}
	if response := managementRequest(t, handler, http.MethodDelete, path, nil, map[string]string{"X-NSC-Reauthenticate": "wrong", "If-Match": `"rev-2"`}); response.Code != http.StatusForbidden {
		t.Fatalf("delete without reauthentication accepted: %d", response.Code)
	}
	if response := managementRequest(t, handler, http.MethodDelete, path, nil, map[string]string{"X-NSC-Reauthenticate": "test-admin-token", "If-Match": `"rev-1"`}); response.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale delete accepted: %d %s", response.Code, response.Body)
	}
	if response := managementRequest(t, handler, http.MethodDelete, path, nil, map[string]string{"X-NSC-Reauthenticate": "test-admin-token"}); response.Code != http.StatusPreconditionRequired {
		t.Fatalf("delete without revision accepted: %d %s", response.Code, response.Body)
	}
	if response := managementRequest(t, handler, http.MethodDelete, path, nil, map[string]string{"X-NSC-Reauthenticate": "test-admin-token", "If-Match": `"rev-2"`}); response.Code != http.StatusNoContent {
		t.Fatalf("delete failed: %d %s", response.Code, response.Body)
	}
	if response := managementRequest(t, handler, http.MethodGet, path, nil, nil); response.Code != http.StatusNotFound {
		t.Fatalf("deleted profile still readable: %d %s", response.Code, response.Body)
	}
}
