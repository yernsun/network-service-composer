package core

import (
	"context"
	"errors"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"testing"
)

func TestDomainCandidateRejectsConflictingTLSAndEntrypoints(t *testing.T) {
	c, _, pool := controlFixture(t)
	for _, tc := range []struct {
		name   string
		mutate func(*DomainConfigInput)
		code   string
	}{
		{"bad host", func(in *DomainConfigInput) { in.Host = "http://bad" }, ""},
		{"no entrypoint", func(in *DomainConfigInput) { in.EntryPoints = nil }, "validation.entrypoints_required"},
		{"HTTP3 without HTTPS", func(in *DomainConfigInput) { in.HTTP3Enabled = true }, "validation.http3_requires_https"},
		{"unknown TLS mode", func(in *DomainConfigInput) { in.TLS.Mode = "unknown" }, "validation.tls_mode_invalid"},
		{"HTTPS without TLS", func(in *DomainConfigInput) { in.EntryPoints = []string{"https"} }, "validation.https_requires_tls"},
		{"TLS without HTTPS", func(in *DomainConfigInput) { in.TLS.Mode = model.TLSManaged }, "validation.tls_requires_https"},
		{"redirect HTTP only", func(in *DomainConfigInput) { in.RedirectHTTPToHTTPS = true }, "validation.redirect_requires_both_entrypoints"},
		{"redirect HTTPS only", func(in *DomainConfigInput) {
			in.EntryPoints = []string{"https"}
			in.TLS.Mode = model.TLSManaged
			in.RedirectHTTPToHTTPS = true
		}, "validation.redirect_requires_both_entrypoints"},
		{"ACME with disabled TLS", func(in *DomainConfigInput) { in.TLS.ACMEChallenge = model.ACMEHTTP01 }, "validation.acme_requires_managed_tls"},
		{"DNS with disabled TLS", func(in *DomainConfigInput) { in.TLS.DNSProviderRef = "provider" }, "validation.acme_requires_managed_tls"},
		{"invalid ACME challenge", func(in *DomainConfigInput) {
			in.EntryPoints = []string{"https"}
			in.TLS.Mode = model.TLSManaged
			in.TLS.ACMEChallenge = "invalid"
		}, "validation.acme_challenge_invalid"},
		{"managed external certificate", func(in *DomainConfigInput) {
			in.EntryPoints = []string{"https"}
			in.TLS.Mode = model.TLSManaged
			in.TLS.CertificateID = "external"
		}, "validation.managed_certificate_controller_owned"},
		{"disabled certificate", func(in *DomainConfigInput) { in.TLS.CertificateID = "external" }, "validation.disabled_tls_certificate"},
		{"custom missing certificate", func(in *DomainConfigInput) { in.EntryPoints = []string{"https"}; in.TLS.Mode = model.TLSCustom }, "validation.custom_tls_certificate_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := DomainConfigInput{Host: "app.example.test", EntryPoints: []string{"http"}}
			tc.mutate(&in)
			domain, cert, e := c.buildDomain(context.Background(), pool.ID, in, c.now())
			var invalid ValidationError
			if e == nil || !errors.As(e, &invalid) || tc.code != "" && invalid.Code != tc.code || domain.ID != "" || cert != nil {
				t.Fatalf("unsafe candidate or incorrect reason: %+v %v", domain, e)
			}
		})
	}
	for _, entries := range [][]string{{"http", " HTTP "}, {"https", "HTTPS"}, {"http", "tcp"}} {
		if _, _, e := NormalizeEntryPoints(entries); e == nil {
			t.Fatal("ambiguous or unsupported entrypoint accepted")
		}
	}
}
func TestDomainCandidatePreservesCustomAndManagedCertificateOwnership(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	key := certificateFixtureKey(t)
	leaf := certificateTemplate(c.now(), false)
	pem, _ := certificateFixturePEM(t, leaf, leaf, key, key)
	cert := model.Certificate{ID: "custom", PoolID: pool.ID, Provider: "custom", Status: model.CertificateActive, CertificatePEM: string(pem)}
	c.store = &certificateBoundaryStore{Store: c.store, certificate: cert}
	c.config.ACME.DNSProviders = map[string]config.DNSProviderProfile{"provider": {Provider: "cloudflare"}}
	for _, mode := range []model.TLSMode{model.TLSDisabled, model.TLSCustom, model.TLSManaged} {
		input := DomainConfigInput{Host: "APP.EXAMPLE.TEST.", EntryPoints: []string{"http"}, TLS: DomainTLSInput{Mode: mode}}
		if mode != model.TLSDisabled {
			input.EntryPoints = []string{"http", "https"}
			input.RedirectHTTPToHTTPS = true
		}
		if mode == model.TLSCustom {
			input.TLS.CertificateID = cert.ID
		}
		if mode == model.TLSManaged {
			input.TLS.DNSProviderRef = " provider "
		}
		domain, pending, e := c.buildDomain(ctx, pool.ID, input, c.now())
		if e != nil || domain.Host != "app.example.test" || domain.PoolID != pool.ID || !domain.Enabled {
			t.Fatalf("valid candidate changed identity: %+v %v", domain, e)
		}
		if mode == model.TLSManaged {
			if pending == nil || pending.Status != model.CertificatePending || pending.ID != domain.CertificateID || pending.PoolID != pool.ID || pending.DNSProviderRef != "provider" || pending.ACMEChallenge != model.ACMEDNS01 || domain.Status != model.DomainCertPending {
				t.Fatal("managed certificate is not a pending owned asset")
			}
		} else if pending != nil || domain.Status != model.DomainReady {
			t.Fatal("custom/plain domain unexpectedly provisioned certificate")
		}
		if mode == model.TLSCustom && domain.CertificateID != cert.ID {
			t.Fatal("custom certificate replaced")
		}
	}
	c.store = &certificateBoundaryStore{Store: c.store, certificateErr: storage.ErrNotFound}
	if domain, cert, e := c.buildDomain(ctx, pool.ID, DomainConfigInput{Host: "app.example.test", EntryPoints: []string{"https"}, TLS: DomainTLSInput{Mode: model.TLSCustom, CertificateID: "missing"}}, c.now()); !errors.Is(e, storage.ErrNotFound) || domain.ID != "" || cert != nil {
		t.Fatal("missing custom certificate accepted", e)
	}
	if got := entryPoints(false, false); len(got) != 0 {
		t.Fatal("invented entrypoint")
	}
	if got := entryPoints(false, true); len(got) != 1 || got[0] != "https" {
		t.Fatal("HTTPS-only entrypoint lost")
	}
}
