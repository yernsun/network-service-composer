package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/dnsprofile"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestDNSProfileSecretsAreEncryptedAndOnlyManagedDNSUsesThem(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	secret := "private-dns-token-for-test"
	input := DNSProfileInput{Reference: "edge-dns", Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: secret}}
	profile, err := c.SaveDNSProfile(ctx, input.Reference, input, 0)
	if err != nil || profile.Source != "database" || profile.Revision != 1 {
		t.Fatalf("profile create: %+v %v", profile, err)
	}
	doc, err := c.store.GetControlDocument(ctx, dnsprofile.Key(input.Reference))
	if err != nil || strings.Contains(string(doc.Payload), secret) {
		t.Fatalf("unencrypted credential payload: %v", err)
	}
	public, err := json.Marshal(profile)
	if err != nil || strings.Contains(string(public), secret) {
		t.Fatalf("credential leaked in response: %v", err)
	}
	resolved, err := c.resolveDNSProfile(ctx, input.Reference)
	if err != nil || resolved.Credentials.APIToken != secret {
		t.Fatalf("dynamic profile unavailable to ACME: %v", err)
	}
	if _, _, err = c.validateACMESelection(ctx, model.TLSManaged, model.ACMEHTTP01, input.Reference); err == nil {
		t.Fatal("HTTP-01 accepted a DNS provider")
	}
	if _, _, err = c.validateACMESelection(ctx, model.TLSManaged, model.ACMEDNS01, input.Reference); err != nil {
		t.Fatal(err)
	}
	input.Credentials.APIToken = "replacement-token"
	if _, err = c.SaveDNSProfile(ctx, input.Reference, input, profile.Revision+1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale rotation accepted: %v", err)
	}
	domain, err := c.CreateDomain(ctx, CreateDomainInput{PoolID: pool.ID, DomainConfigInput: DomainConfigInput{Host: "profile.example.test", EntryPoints: []string{EntryPointHTTPS}, TLS: DomainTLSInput{Mode: model.TLSManaged, ACMEChallenge: model.ACMEDNS01, DNSProviderRef: input.Reference}}})
	if err != nil || domain.DNSProviderRef != input.Reference {
		t.Fatalf("DNS-01 domain was rejected: %+v %v", domain, err)
	}
	if err = c.DeleteDNSProfile(ctx, input.Reference, profile.Revision); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("referenced profile deleted: %v", err)
	}
}

func TestDomainDraftDoesNotPublishUntilExplicitAtomicBinding(t *testing.T) {
	c, service, _ := managedBindingFixture(t)
	ctx := context.Background()
	project, err := c.CreateProject(ctx, "Draft Project", "draft-project")
	if err != nil {
		t.Fatal(err)
	}
	env, err := c.CreateEnvironment(ctx, project.ID, EnvironmentInput{Name: "prod", RuntimeID: service.RuntimeID, ApplicationID: service.ApplicationID})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := c.store.GetRuntime(ctx, service.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	now := c.now()
	gateway := model.Gateway{ID: id.New(id.Gateway), PoolID: runtime.PoolID, Name: "edge", Address: "192.0.2.20", Enabled: true, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err = c.store.UpsertGateway(ctx, &gateway); err != nil {
		t.Fatal(err)
	}
	before, err := c.store.GetPool(ctx, runtime.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	input := DomainDraftInput{Domain: DomainConfigInput{Host: "draft.example.test", EntryPoints: []string{EntryPointHTTP}, TLS: DomainTLSInput{Mode: model.TLSDisabled}}, Route: RouteConfigInput{Path: "/", Backend: RouteBackendInput{Port: 8080, Protocol: "http"}}}
	draft, err := c.CreateDomainDraft(ctx, env.ID, input)
	if err != nil || draft.Status != "draft" || draft.ProjectID != project.ID || draft.EnvironmentID != env.ID {
		t.Fatalf("draft save: %+v %v", draft, err)
	}
	if _, err = c.PublishDomainDraft(ctx, draft.ID, draft.Revision); err == nil {
		t.Fatal("draft without service was published")
	}
	domains, err := c.store.ListDomains(ctx, storage.Page{Limit: 100})
	if err != nil || len(domains) != 0 {
		t.Fatalf("draft created live domain: %d %v", len(domains), err)
	}
	routes, err := c.store.ListRoutes(ctx, storage.Page{Limit: 100})
	if err != nil || len(routes) != 0 {
		t.Fatalf("draft created live route: %d %v", len(routes), err)
	}
	pool, err := c.store.GetPool(ctx, runtime.PoolID)
	if err != nil || pool.DesiredRevision != before.DesiredRevision {
		t.Fatalf("draft changed gateway revision: %d -> %d, %v", before.DesiredRevision, pool.DesiredRevision, err)
	}
	input.ServiceID = service.ID
	draft, err = c.UpdateDomainDraft(ctx, draft.ID, input, draft.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.PublishDomainDraft(ctx, draft.ID, draft.Revision-1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale publish accepted: %v", err)
	}
	committed, err := c.PublishDomainDraft(ctx, draft.ID, draft.Revision)
	if err != nil || committed.Status != "published" || committed.PublishedDomainID == "" || committed.PublishedRouteID == "" || committed.OperationID == "" {
		t.Fatalf("publish receipt: %+v %v", committed, err)
	}
	if _, err = c.store.GetDomain(ctx, committed.PublishedDomainID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.store.GetRoute(ctx, committed.PublishedRouteID); err != nil {
		t.Fatal(err)
	}
	pool, err = c.store.GetPool(ctx, runtime.PoolID)
	if err != nil || pool.DesiredRevision <= before.DesiredRevision {
		t.Fatalf("publish failed to update gateway revision: %d -> %d, %v", before.DesiredRevision, pool.DesiredRevision, err)
	}
	redelivered, err := c.PublishDomainDraft(ctx, draft.ID, committed.Revision)
	if err != nil || redelivered.PublishedRouteID != committed.PublishedRouteID {
		t.Fatalf("retry duplicated publication: %+v %v", redelivered, err)
	}
	if err = c.DeleteDomainDraft(ctx, draft.ID, committed.Revision); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("published receipt removed: %v", err)
	}
	duplicate, err := c.CreateDomainDraft(ctx, env.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.PublishDomainDraft(ctx, duplicate.ID, duplicate.Revision); err == nil {
		t.Fatal("duplicate host was published")
	}
	unchanged, err := c.GetDomainDraft(ctx, duplicate.ID)
	if err != nil || unchanged.Status != "draft" || unchanged.PublishedDomainID != "" {
		t.Fatalf("failed publication changed receipt: %+v %v", unchanged, err)
	}
	afterFailure, err := c.store.GetPool(ctx, runtime.PoolID)
	if err != nil || afterFailure.DesiredRevision != pool.DesiredRevision {
		t.Fatalf("failed publication advanced gateway revision: %d -> %d, %v", pool.DesiredRevision, afterFailure.DesiredRevision, err)
	}
}

func TestDNSProfileDraftReferenceAndEnvironmentOwnership(t *testing.T) {
	c, service, _ := managedBindingFixture(t)
	ctx := context.Background()
	project, err := c.CreateProject(ctx, "Scoped DNS", "scoped-dns")
	if err != nil {
		t.Fatal(err)
	}
	env, err := c.CreateEnvironment(ctx, project.ID, EnvironmentInput{Name: "prod", RuntimeID: service.RuntimeID, ApplicationID: service.ApplicationID})
	if err != nil {
		t.Fatal(err)
	}
	other, err := c.CreateEnvironment(ctx, project.ID, EnvironmentInput{Name: "dev", RuntimeID: service.RuntimeID})
	if err != nil {
		t.Fatal(err)
	}
	c.config.ACME.DNSProviders["configured-dns"] = config.DNSProviderProfile{Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: "fixture-config-token"}}
	configured, err := c.GetDNSProfile(ctx, "configured-dns")
	if err != nil || configured.Source != "config" {
		t.Fatalf("configured profile not listed as read-only: %+v %v", configured, err)
	}
	if _, err = c.SaveDNSProfile(ctx, "configured-dns", DNSProfileInput{Reference: "configured-dns", Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: "replacement"}}, 0); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("configured profile overwritten: %v", err)
	}
	if err = c.DeleteDNSProfile(ctx, "configured-dns", 0); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("configured profile deleted: %v", err)
	}
	profileInput := DNSProfileInput{Reference: "draft-dns", Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: "fixture-private-token"}}
	profile, err := c.SaveDNSProfile(ctx, profileInput.Reference, profileInput, 0)
	if err != nil {
		t.Fatal(err)
	}
	input := DomainDraftInput{Domain: DomainConfigInput{Host: "scoped.example.test", EntryPoints: []string{EntryPointHTTPS}, TLS: DomainTLSInput{Mode: model.TLSManaged, ACMEChallenge: model.ACMEDNS01, DNSProviderRef: profile.Reference}}, ServiceID: service.ID,
		Route: RouteConfigInput{Path: "/", Backend: RouteBackendInput{Port: 8080, Protocol: "http"}}}
	if _, err = c.CreateDomainDraft(ctx, other.ID, input); err == nil {
		t.Fatal("service was attached to another environment's draft")
	}
	draft, err := c.CreateDomainDraft(ctx, env.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.PublishDomainDraft(ctx, draft.ID, draft.Revision); err == nil {
		t.Fatal("draft published without an enabled gateway DNS target")
	}
	unchanged, err := c.GetDomainDraft(ctx, draft.ID)
	if err != nil || unchanged.Status != "draft" || unchanged.Revision != draft.Revision {
		t.Fatalf("failed gateway preflight changed draft: %+v %v", unchanged, err)
	}
	certificates, err := c.store.ListCertificates(ctx, storage.Page{Limit: 10})
	if err != nil || len(certificates) != 0 {
		t.Fatalf("DNS draft issued a certificate before publication: %d %v", len(certificates), err)
	}
	if err = c.DeleteDNSProfile(ctx, profile.Reference, profile.Revision); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("draft reference did not protect DNS profile: %v", err)
	}
	input.Domain.EntryPoints = []string{EntryPointHTTP}
	input.Domain.TLS = DomainTLSInput{Mode: model.TLSDisabled}
	draft, err = c.UpdateDomainDraft(ctx, draft.ID, input, draft.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.DeleteDNSProfile(ctx, profile.Reference, profile.Revision); err != nil {
		t.Fatalf("unreferenced profile could not be deleted: %v", err)
	}
	if _, err = c.GetDNSProfile(ctx, profile.Reference); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deleted DNS profile still available: %v", err)
	}
	if draft.Status != "draft" {
		t.Fatalf("draft changed after profile removal: %+v", draft)
	}
}

func TestDNSProfileCannotBeDeletedWhileCertificateNeedsIt(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	input := DNSProfileInput{Reference: "renewal-dns", Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: "fixture-private-token"}}
	profile, err := c.SaveDNSProfile(ctx, input.Reference, input, 0)
	if err != nil {
		t.Fatal(err)
	}
	now := c.now()
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "acme", Status: model.CertificatePending,
		ACMEChallenge: model.ACMEDNS01, DNSProviderRef: profile.Reference, PrimaryDomain: "renewal.example.test", Domains: []string{"renewal.example.test"},
		Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err = c.store.CreateCertificate(ctx, &certificate); err != nil {
		t.Fatal(err)
	}
	if err = c.DeleteDNSProfile(ctx, profile.Reference, profile.Revision); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("certificate lost its DNS renewal credentials: %v", err)
	}
}
