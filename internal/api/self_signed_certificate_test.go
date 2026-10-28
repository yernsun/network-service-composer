package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestSelfSignedCertificateHTTPAuthorizationAndRedaction(t *testing.T) {
	s, h := newTestServer(t)
	pool := responseData[model.GatewayPool](t, managementRequest(t, h, "POST", "/gateway-pools", map[string]any{"name": "self-signed-pool"}, nil), 201)
	body := map[string]any{"pool_id": pool.ID, "domains": []string{"private.example.test", "second.example.test"}}
	res := managementRequest(t, h, "POST", "/certificates/self-signed", body, nil)
	cert := responseData[model.Certificate](t, res, 201)
	if cert.Provider != "self-signed" || len(cert.Domains) != 2 || res.Header().Get("ETag") == "" {
		t.Fatal("metadata missing", res.Body)
	}
	for _, path := range []string{"/certificates", "/certificates/" + cert.ID} {
		read := managementRequest(t, h, "GET", path, nil, nil)
		if read.Code != 200 || strings.Contains(read.Body.String(), "PRIVATE KEY") || strings.Contains(read.Body.String(), "certificate_pem") || !strings.Contains(read.Body.String(), "second.example.test") {
			t.Fatal("metadata leak or SAN loss", read.Body)
		}
	}
	for _, invalid := range []any{"{", map[string]any{"pool_id": pool.ID, "domains": []string{"*.example.test"}}, map[string]any{"pool_id": pool.ID, "domains": []string{"example.test"}, "validity_years": 0}} {
		if res := managementRequest(t, h, "POST", "/certificates/self-signed", invalid, nil); res.Code < 400 {
			t.Fatal("invalid input accepted", res.Code)
		}
	}
	viewer, err := s.core.SaveUser(t.Context(), "", core.UserInput{Username: "certificate-viewer", Role: "viewer", Enabled: true, Password: "test-viewer-password-123"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.core.RotateUserToken(t.Context(), viewer.ID, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for _, auth := range []string{"", token} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/certificates/self-signed", strings.NewReader(`{"pool_id":"`+pool.ID+`","domains":["example.test"]}`))
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != 401 && res.Code != 403 {
			t.Fatal("unauthorized signing", res.Code)
		}
	}
}
