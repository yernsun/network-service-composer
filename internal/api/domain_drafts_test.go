package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestDomainDraftHTTPKeepsLiveResourcesUnchangedAndHonorsRevisions(t *testing.T) {
	s, handler, runtime, _ := managedRuntimeFixture(t)
	ctx := context.Background()
	project, err := s.core.CreateProject(ctx, "Draft HTTP", "draft-http")
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.core.CreateEnvironment(ctx, project.ID, core.EnvironmentInput{Name: "prod", RuntimeID: runtime.ID})
	if err != nil {
		t.Fatal(err)
	}
	base := "/environments/" + environment.ID + "/domain-drafts"
	input := core.DomainDraftInput{Domain: core.DomainConfigInput{Host: "draft.example.test", EntryPoints: []string{core.EntryPointHTTP}, TLS: core.DomainTLSInput{Mode: "disabled"}}, Route: core.RouteConfigInput{Path: "/", Backend: core.RouteBackendInput{Port: 8080, Protocol: "http"}}}
	if got := managementRequest(t, handler, http.MethodGet, "/environments/missing/domain-drafts", nil, nil); got.Code != http.StatusNotFound {
		t.Fatalf("unknown environment listed drafts: %d %s", got.Code, got.Body)
	}
	if got := managementRequest(t, handler, http.MethodGet, "/domain-drafts/missing", nil, nil); got.Code != http.StatusNotFound {
		t.Fatalf("unknown draft was readable: %d %s", got.Code, got.Body)
	}
	if got := managementRequest(t, handler, http.MethodPost, base, "{", nil); got.Code != http.StatusBadRequest {
		t.Fatalf("malformed draft accepted: %d", got.Code)
	}
	bad := input
	bad.Domain.Host = "https://invalid.example.test"
	if got := managementRequest(t, handler, http.MethodPost, base, bad, nil); got.Code < 400 || got.Code >= 500 {
		t.Fatalf("invalid domain draft accepted: %d %s", got.Code, got.Body)
	}
	draft := responseData[core.DomainDraft](t, managementRequest(t, handler, http.MethodPost, base, input, nil), http.StatusCreated)
	if draft.EnvironmentID != environment.ID || draft.Status != "draft" {
		t.Fatalf("wrong draft scope: %+v", draft)
	}
	get := responseData[core.DomainDraft](t, managementRequest(t, handler, http.MethodGet, "/domain-drafts/"+draft.ID, nil, nil), http.StatusOK)
	if get.ID != draft.ID || get.Revision != draft.Revision {
		t.Fatalf("draft read changed identity: %+v", get)
	}
	list := responseData[[]core.DomainDraft](t, managementRequest(t, handler, http.MethodGet, base, nil, nil), http.StatusOK)
	if len(list) != 1 || list[0].ID != draft.ID {
		t.Fatalf("draft list did not retain environment scope: %+v", list)
	}
	path := "/domain-drafts/" + draft.ID
	if got := managementRequest(t, handler, http.MethodPut, path, input, nil); got.Code != http.StatusPreconditionRequired {
		t.Fatalf("draft update without revision accepted: %d %s", got.Code, got.Body)
	}
	if got := managementRequest(t, handler, http.MethodPut, path, "{", map[string]string{"If-Match": `"rev-1"`}); got.Code != http.StatusBadRequest {
		t.Fatalf("malformed draft update accepted: %d %s", got.Code, got.Body)
	}
	if got := managementRequest(t, handler, http.MethodPut, path, input, map[string]string{"If-Match": `"rev-99"`}); got.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale draft update accepted: %d %s", got.Code, got.Body)
	}
	input.Domain.Host = "edited.example.test"
	draft = responseData[core.DomainDraft](t, managementRequest(t, handler, http.MethodPut, path, input, map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, draft.Revision)}), http.StatusOK)
	if draft.Domain.Host != input.Domain.Host {
		t.Fatalf("draft edit missing: %+v", draft)
	}
	if got := managementRequest(t, handler, http.MethodPost, path+"/publish", nil, map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, draft.Revision)}); got.Code != http.StatusBadRequest {
		t.Fatalf("draft without service published: %d %s", got.Code, got.Body)
	}
	domains, err := s.store.ListDomains(ctx, storage.Page{Limit: 10})
	if err != nil || len(domains) != 0 {
		t.Fatalf("draft reached live domains: %d %v", len(domains), err)
	}
	if got := managementRequest(t, handler, http.MethodDelete, path, nil, map[string]string{"If-Match": `"rev-1"`}); got.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale draft delete accepted: %d %s", got.Code, got.Body)
	}
	if got := managementRequest(t, handler, http.MethodDelete, path, nil, nil); got.Code != http.StatusPreconditionRequired {
		t.Fatalf("draft delete without revision accepted: %d %s", got.Code, got.Body)
	}
	if got := managementRequest(t, handler, http.MethodDelete, path, nil, map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, draft.Revision)}); got.Code != http.StatusNoContent {
		t.Fatalf("draft delete failed: %d %s", got.Code, got.Body)
	}
	list = responseData[[]core.DomainDraft](t, managementRequest(t, handler, http.MethodGet, base, nil, nil), http.StatusOK)
	if len(list) != 0 {
		t.Fatalf("deleted draft remained listed: %+v", list)
	}
}

func TestDomainDraftHTTPPublishesManagedDNSOnlyAfterExplicitRequest(t *testing.T) {
	s, handler, runtime, pool := managedRuntimeFixture(t)
	ctx := context.Background()
	application, _, _, err := s.core.InstallApplication(ctx, core.InstallApplicationInput{
		ApplicationPreflightInput: core.ApplicationPreflightInput{RuntimeID: runtime.ID, ComposeYAML: "services:\n  web:\n    image: example.test/web:fixed\n    expose: [8080]"},
		Name:                      "Web", ProjectName: "draft-web", IdempotencyKey: "draft-web-install",
	})
	if err != nil {
		t.Fatal(err)
	}
	services, err := s.store.ListServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: application.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("managed service fixture: %d %v", len(services), err)
	}
	project, err := s.core.CreateProject(ctx, "Publish DNS", "publish-dns")
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.core.CreateEnvironment(ctx, project.ID, core.EnvironmentInput{Name: "prod", RuntimeID: runtime.ID, ApplicationID: application.ID})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	gateway := model.Gateway{ID: id.New(id.Gateway), PoolID: pool.ID, Name: "edge", Address: "192.0.2.20", Enabled: true,
		Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err = s.store.UpsertGateway(ctx, &gateway); err != nil {
		t.Fatal(err)
	}
	profile, err := s.core.SaveDNSProfile(ctx, "publish-dns", core.DNSProfileInput{Reference: "publish-dns", Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: "fixture-token"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	input := core.DomainDraftInput{Domain: core.DomainConfigInput{Host: "publish.example.test", EntryPoints: []string{core.EntryPointHTTPS},
		TLS: core.DomainTLSInput{Mode: model.TLSManaged, ACMEChallenge: model.ACMEDNS01, DNSProviderRef: profile.Reference}}, ServiceID: services[0].ID,
		Route: core.RouteConfigInput{Path: "/", Backend: core.RouteBackendInput{Port: 8080, Protocol: "http"}}}
	base := "/environments/" + environment.ID + "/domain-drafts"
	draft := responseData[core.DomainDraft](t, managementRequest(t, handler, http.MethodPost, base, input, nil), http.StatusCreated)
	certificates, err := s.store.ListCertificates(ctx, storage.Page{Limit: 10})
	if err != nil || len(certificates) != 0 {
		t.Fatalf("saving a DNS draft queued a certificate: %d %v", len(certificates), err)
	}
	path := "/domain-drafts/" + draft.ID + "/publish"
	if got := managementRequest(t, handler, http.MethodPost, path, nil, nil); got.Code != http.StatusPreconditionRequired {
		t.Fatalf("publication without revision accepted: %d %s", got.Code, got.Body)
	}
	if got := managementRequest(t, handler, http.MethodPost, path, nil, map[string]string{"If-Match": `"rev-99"`}); got.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale publication accepted: %d %s", got.Code, got.Body)
	}
	published := responseData[core.DomainDraft](t, managementRequest(t, handler, http.MethodPost, path, nil,
		map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, draft.Revision)}), http.StatusAccepted)
	if published.Status != "published" || published.PublishedDomainID == "" || published.PublishedRouteID == "" || published.OperationID == "" {
		t.Fatalf("publication receipt incomplete: %+v", published)
	}
	domain, err := s.store.GetDomain(ctx, published.PublishedDomainID)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := s.store.GetCertificate(ctx, domain.CertificateID)
	if err != nil || certificate.Status != model.CertificatePending || certificate.ACMEChallenge != model.ACMEDNS01 || certificate.DNSProviderRef != profile.Reference {
		t.Fatalf("managed DNS certificate selection: %+v %v", certificate, err)
	}
	again := responseData[core.DomainDraft](t, managementRequest(t, handler, http.MethodPost, path, nil,
		map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, published.Revision)}), http.StatusAccepted)
	if again.PublishedDomainID != published.PublishedDomainID || again.OperationID != published.OperationID {
		t.Fatalf("publish retry duplicated resources: %+v", again)
	}
}
