package api

import (
	"context"
	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestResourceReadHandlersPreserveIdentityAndReportUnavailableStorage(t *testing.T) {
	s, _, runtime, domain, route, gateway := resourcePatchFixture(t)
	now := time.Now().UTC()
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: runtime.PoolID, Provider: "fixture", Status: model.CertificateActive, EncryptedPrivateKey: []byte("private-key-must-not-leak"), CreatedAt: now, UpdatedAt: now, Revision: 1}
	if e := s.store.CreateCertificate(context.Background(), &certificate); e != nil {
		t.Fatal(e)
	}
	router := chi.NewRouter()
	router.Route("/api/v1", func(r chi.Router) { s.registerAPI(r) })
	cases := []struct{ path, identity string }{
		{"/gateway-pools", runtime.PoolID}, {"/gateway-pools/" + runtime.PoolID, runtime.PoolID},
		{"/runtimes", runtime.ID}, {"/runtimes/" + runtime.ID, runtime.ID},
		{"/services", route.ServiceID}, {"/services/" + route.ServiceID, route.ServiceID},
		{"/domains", domain.ID}, {"/domains/" + domain.ID, domain.ID},
		{"/routes", route.ID}, {"/routes/" + route.ID, route.ID},
		{"/certificates", certificate.ID}, {"/certificates/" + certificate.ID, certificate.ID},
		{"/gateways", gateway.ID}, {"/gateways/" + gateway.ID, gateway.ID},
	}
	for _, tc := range cases {
		response := managementRequest(t, router, http.MethodGet, tc.path, nil, nil)
		if response.Code != 200 || !strings.Contains(response.Body.String(), tc.identity) || strings.Contains(response.Body.String(), "private-key-must-not-leak") {
			t.Fatalf("resource read %s: %d %s", tc.path, response.Code, response.Body)
		}
	}
	for _, path := range []string{"/services/" + route.ServiceID + "/instances?cursor=missing", "/services/" + route.ServiceID + "/instances"} {
		response := managementRequest(t, router, "GET", path, nil, nil)
		if response.Code != 200 {
			t.Fatalf("empty instance collection misreported: %d %s", response.Code, response.Body)
		}
	}
	s.store.(*storage.SQLStore).Close()
	for _, tc := range cases {
		response := managementRequest(t, router, http.MethodGet, tc.path, nil, nil)
		if response.Code != 500 || strings.Contains(response.Body.String(), `"data"`) || strings.Contains(response.Body.String(), "database is closed") {
			t.Fatalf("unavailable read %s returned data or raw error: %d %s", tc.path, response.Code, response.Body)
		}
	}
}
