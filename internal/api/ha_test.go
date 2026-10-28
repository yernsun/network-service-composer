package api

import (
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestSystemHAReadOnlyAdminAndNoProbeInput(t *testing.T) {
	s, handler, _, _ := managedRuntimeFixture(t)
	before, err := s.store.ListControlDocuments(t.Context(), "", "", storage.Page{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	reply := managementRequest(t, handler, http.MethodGet, "/system/ha", nil, nil)
	value := responseData[core.HAOverview](t, reply, 200)
	if value.EndToEndVerified || len(value.Layers) != 6 || reply.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(value, reply.Header())
	}
	for _, secret := range []string{"unix:///", "session_key", "encrypted_input", "collector_owner"} {
		if strings.Contains(reply.Body.String(), secret) {
			t.Fatal("private HA output", reply.Body.String())
		}
	}
	query := managementRequest(t, handler, http.MethodGet, "/system/ha?target=https://127.0.0.1", nil, nil)
	if query.Code != 400 {
		t.Fatal("probe input accepted", query.Code)
	}
	after, err := s.store.ListControlDocuments(t.Context(), "", "", storage.Page{Limit: 200})
	if err != nil || len(before) != len(after) {
		t.Fatal("GET changed stored state", err)
	}
	unauth := httptest.NewRecorder()
	handler.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/api/v1/system/ha", nil))
	if unauth.Code != 401 {
		t.Fatal(unauth.Code)
	}
	viewer, err := s.core.SaveUser(t.Context(), "", core.UserInput{Username: "ha-viewer", Role: "viewer", Enabled: true, Password: "isolated-ha-viewer-password"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.core.RotateUserToken(t.Context(), viewer.ID, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/ha", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, req)
	if denied.Code != 403 {
		t.Fatal("non-admin received infrastructure evidence", denied.Code)
	}
}

func TestSystemHAHandlerRequiresAnAuthenticatedPrincipal(t *testing.T) {
	s, _ := newTestServer(t)
	router := chi.NewRouter()
	s.registerSystemHA(router)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/system/ha", nil))
	if response.Code != http.StatusForbidden || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("handler disclosed HA without a principal", response.Code)
	}
}
