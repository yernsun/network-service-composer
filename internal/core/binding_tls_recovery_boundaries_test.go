package core

import (
	"context"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"testing"
)

func TestBindingManagedCertificateChangesPreservePriorRecoveryAssets(t *testing.T) {
	c, service, in := externalBindingFixture(t)
	ctx := context.Background()
	c.config.ACME.DNSProviders = map[string]config.DNSProviderProfile{"first": {Provider: "cloudflare"}, "second": {Provider: "cloudflare"}}
	in.Domain.EntryPoints = []string{EntryPointHTTP, EntryPointHTTPS}
	in.Domain.TLS = DomainTLSInput{Mode: model.TLSManaged}
	b, _, e := c.CreateDomainBinding(ctx, service.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"challenge", "provider", "host"} {
		t.Run(mode, func(t *testing.T) {
			old := b.Domain.CertificateID
			patch := DomainConfigPatch{}
			if mode == "challenge" {
				patch.TLS = &DomainTLSInput{Mode: model.TLSManaged, ACMEChallenge: model.ACMEDNS01, DNSProviderRef: "first"}
			}
			if mode == "provider" {
				patch.TLS = &DomainTLSInput{Mode: model.TLSManaged, ACMEChallenge: model.ACMEDNS01, DNSProviderRef: "second"}
			}
			if mode == "host" {
				host := "renamed.example.test"
				patch.Host = &host
			}
			b, e = c.UpdateDomainBinding(ctx, service.ID, b.ID, UpdateDomainBindingInput{DomainRevision: b.Domain.Revision, Domain: &patch}, b.Route.Revision)
			if e != nil || b.Domain.CertificateID == "" || b.Domain.CertificateID == old || b.Domain.Status != model.DomainCertPending {
				t.Fatal("changed validation context reused certificate", b, e)
			}
			if _, e = c.store.GetCertificate(ctx, old); e != nil {
				t.Fatal("old recovery certificate removed", e)
			}
		})
	}
}
func TestBindingTLSUpdateRequiresUsableReferencedCertificate(t *testing.T) {
	for _, mode := range []string{"custom valid", "custom missing", "managed missing", "invalid challenge", "redirect HTTPS only", "persisted no entrypoints"} {
		t.Run(mode, func(t *testing.T) {
			c, s, in := externalBindingFixture(t)
			ctx := context.Background()
			if mode == "managed missing" {
				in.Domain.EntryPoints = []string{EntryPointHTTP, EntryPointHTTPS}
				in.Domain.TLS = DomainTLSInput{Mode: model.TLSManaged}
			}
			b, _, e := c.CreateDomainBinding(ctx, s.ID, in)
			if e != nil {
				t.Fatal(e)
			}
			patch := DomainConfigPatch{}
			https := []string{EntryPointHTTPS}
			yes := true
			switch mode {
			case "custom valid", "custom missing":
				patch.EntryPoints = &https
				certID := "missing"
				if mode == "custom valid" {
					key := certificateFixtureKey(t)
					leaf := certificateTemplate(c.now(), false)
					leaf.DNSNames = []string{b.Domain.Host}
					pem, _ := certificateFixturePEM(t, leaf, leaf, key, key)
					cert := model.Certificate{ID: "cert_custom_binding", PoolID: b.Domain.PoolID, Provider: "custom", Status: model.CertificateActive, CertificatePEM: string(pem), Metadata: model.Metadata{Revision: 1, CreatedAt: c.now(), UpdatedAt: c.now()}}
					if e = c.store.CreateCertificate(ctx, &cert); e != nil {
						t.Fatal(e)
					}
					certID = cert.ID
				}
				patch.TLS = &DomainTLSInput{Mode: model.TLSCustom, CertificateID: certID}
			case "managed missing":
				patch.TLS = &DomainTLSInput{Mode: model.TLSManaged, CertificateID: "missing"}
			case "invalid challenge":
				patch.TLS = &DomainTLSInput{Mode: model.TLSManaged, ACMEChallenge: "unsupported"}
			case "redirect HTTPS only":
				patch.EntryPoints = &https
				patch.TLS = &DomainTLSInput{Mode: model.TLSManaged}
				patch.RedirectHTTPToHTTPS = &yes
			case "persisted no entrypoints":
				d := b.Domain
				d.HTTPEnabled = false
				d.HTTPSEnabled = false
				if e = c.store.UpdateDomain(ctx, &d, b.Domain.Revision); e != nil {
					t.Fatal(e)
				}
				b.Domain, e = c.store.GetDomain(ctx, d.ID)
				if e != nil {
					t.Fatal(e)
				}
			}
			got, e := c.UpdateDomainBinding(ctx, s.ID, b.ID, UpdateDomainBindingInput{DomainRevision: b.Domain.Revision, Domain: &patch}, b.Route.Revision)
			if mode == "custom valid" {
				if e != nil || got.Domain.TLSMode != model.TLSCustom || got.Domain.Status != model.DomainReady {
					t.Fatal("valid custom certificate rejected", got, e)
				}
			} else {
				if e == nil {
					t.Fatal("unusable TLS update accepted", got)
				}
			}
		})
	}
}
