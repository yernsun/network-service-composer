package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/core"
)

func TestReadinessHTTPIsAuthorizedUncachedAndReadOnly(t *testing.T) {
	s, handler, runtime, _ := managedRuntimeFixture(t)
	_, operation, _, err := s.core.InstallApplication(t.Context(), core.InstallApplicationInput{ApplicationPreflightInput: core.ApplicationPreflightInput{RuntimeID: runtime.ID, ComposeYAML: "services:\n  web:\n    image: example.test/web:fixed\n    expose: [8080]\n"}, Name: "Evidence", ProjectName: "evidence", IdempotencyKey: "readiness-api"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/operations/" + operation.ID + "/readiness"
	before, err := s.store.GetDeploymentOperation(t.Context(), operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	w := managementRequest(t, handler, http.MethodGet, path, nil, nil)
	v := responseData[core.OperationReadiness](t, w, http.StatusOK)
	if v.OperationID != operation.ID || len(v.Stages) != 5 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("ETag") == "" {
		t.Fatal("missing readiness contract", v, w.Header())
	}
	after, err := s.store.GetDeploymentOperation(t.Context(), operation.ID)
	if err != nil || after.Status != before.Status || after.Revision != before.Revision {
		t.Fatal("readiness GET changed execution", after, err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1"+path, nil)
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, r)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatal("anonymous readiness read allowed", unauthenticated.Code)
	}
	user, err := s.core.SaveUser(t.Context(), "", core.UserInput{Username: "readiness-viewer", Role: "viewer", Enabled: true, Password: "readiness-password-fixture"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.core.RotateUserToken(t.Context(), user.ID, user.Revision)
	if err != nil {
		t.Fatal(err)
	}
	viewer := managementRequest(t, handler, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + token})
	responseData[core.OperationReadiness](t, viewer, http.StatusOK)
	for _, marker := range []string{"encrypted_input", "encryption_key", "readiness-password-fixture", "unix:///", token} {
		if strings.Contains(viewer.Body.String(), marker) {
			t.Fatal("viewer response leaked confidential material", marker)
		}
	}
	missing := managementRequest(t, handler, http.MethodGet, "/operations/missing/readiness", nil, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatal("missing operation was not 404", missing.Code)
	}
}
