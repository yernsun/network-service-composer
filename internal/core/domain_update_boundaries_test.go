package core

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func domainUpdateFixture(t *testing.T) (*Core, model.Domain) {
	t.Helper()
	c, _, pool := controlFixture(t)
	d, err := c.CreateDomain(context.Background(), CreateDomainInput{PoolID: pool.ID, DomainConfigInput: DomainConfigInput{Host: "app.example.test", EntryPoints: []string{EntryPointHTTP}, TLS: DomainTLSInput{Mode: model.TLSDisabled}}})
	if err != nil {
		t.Fatal(err)
	}
	d, err = c.store.GetDomain(context.Background(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return c, d
}

func TestDomainUpdateRejectsInvalidIdentityAndTLSWithoutMutation(t *testing.T) {
	for name, change := range map[string]func(*model.Domain){
		"pool":                          func(d *model.Domain) { d.PoolID = "foreign" },
		"source":                        func(d *model.Domain) { d.Source = model.SourceDiscovery },
		"host":                          func(d *model.Domain) { d.Host = "bad/name" },
		"http3 without TLS":             func(d *model.Domain) { d.HTTP3Enabled = true },
		"unknown TLS":                   func(d *model.Domain) { d.TLSMode = "unknown" },
		"challenge without managed TLS": func(d *model.Domain) { d.ACMEChallenge = model.ACMEHTTP01 },
		"provider without managed TLS":  func(d *model.Domain) { d.DNSProviderRef = "profile" },
		"no entrypoints":                func(d *model.Domain) { d.HTTPEnabled = false },
		"HTTPS without certificate":     func(d *model.Domain) { d.HTTPSEnabled = true },
		"TLS without HTTPS":             func(d *model.Domain) { d.TLSMode = model.TLSManaged },
		"custom without certificate":    func(d *model.Domain) { d.TLSMode = model.TLSCustom; d.HTTPSEnabled = true },
		"redirect HTTP only":            func(d *model.Domain) { d.HTTPSRedirect = true },
		"redirect HTTPS only": func(d *model.Domain) {
			d.HTTPEnabled = false
			d.HTTPSEnabled = true
			d.TLSMode = model.TLSManaged
			d.HTTPSRedirect = true
		},
		"unsupported ACME": func(d *model.Domain) {
			d.HTTPSEnabled = true
			d.TLSMode = model.TLSManaged
			d.ACMEChallenge = "tls-alpn-01"
		},
		"missing custom asset": func(d *model.Domain) {
			d.HTTPSEnabled = true
			d.TLSMode = model.TLSCustom
			d.CertificateID = id.New(id.Certificate)
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, original := domainUpdateFixture(t)
			candidate := original
			change(&candidate)
			if _, err := c.UpdateDomain(context.Background(), candidate, original.Revision); err == nil {
				t.Fatal("invalid domain accepted")
			}
			got, err := c.store.GetDomain(context.Background(), original.ID)
			if err != nil || !reflect.DeepEqual(got, original) {
				t.Fatalf("invalid update changed domain: %+v %v", got, err)
			}
		})
	}
}

func TestManagedDomainUpdateProvisioningAndReenableStates(t *testing.T) {
	c, d := domainUpdateFixture(t)
	ctx := context.Background()
	c.config.ACME.DNSProviders = map[string]config.DNSProviderProfile{"first": {Provider: "cloudflare"}, "second": {Provider: "cloudflare"}}
	d.TLSMode = model.TLSManaged
	d.HTTPSEnabled = true
	managed, err := c.UpdateDomain(ctx, d, d.Revision)
	if err != nil || managed.CertificateID == "" || managed.Status != model.DomainCertPending {
		t.Fatalf("managed transition: %+v %v", managed, err)
	}
	first := managed.CertificateID
	same, err := c.UpdateDomain(ctx, managed, managed.Revision)
	if err != nil || same.CertificateID != first {
		t.Fatalf("unchanged TLS replaced certificate: %+v %v", same, err)
	}
	for _, change := range []string{"host", "challenge", "provider"} {
		previous := same.CertificateID
		switch change {
		case "host":
			same.Host = "renamed.example.test"
		case "challenge":
			same.ACMEChallenge = model.ACMEDNS01
			same.DNSProviderRef = "first"
		case "provider":
			same.DNSProviderRef = "second"
		}
		same, err = c.UpdateDomain(ctx, same, same.Revision)
		if err != nil || same.CertificateID == "" || same.CertificateID == previous {
			t.Fatalf("%s did not provision new certificate: %+v %v", change, same, err)
		}
		if _, err = c.store.GetCertificate(ctx, previous); err != nil {
			t.Fatalf("previous certificate recovery dependency deleted: %v", err)
		}
	}
	for _, status := range []model.CertificateStatus{model.CertificatePending, model.CertificateActive, model.CertificateRenewing} {
		same.Enabled = false
		same, err = c.UpdateDomain(ctx, same, same.Revision)
		if err != nil || same.Status != model.DomainDisabled {
			t.Fatalf("disable domain: %+v %v", same, err)
		}
		cert, e := c.store.GetCertificate(ctx, same.CertificateID)
		if e != nil {
			t.Fatal(e)
		}
		cert.Status = status
		if status != model.CertificatePending {
			key := certificateFixtureKey(t)
			leaf := certificateTemplate(c.now(), false)
			leaf.DNSNames = []string{same.Host}
			certificate, _ := certificateFixturePEM(t, leaf, leaf, key, key)
			cert.CertificatePEM = string(certificate)
		}
		if e = c.store.UpdateCertificate(ctx, &cert, cert.Revision); e != nil {
			t.Fatal(e)
		}
		same.Enabled = true
		same, err = c.UpdateDomain(ctx, same, same.Revision)
		want := model.DomainReady
		if status == model.CertificatePending {
			want = model.DomainCertPending
		}
		if err != nil || same.Status != want {
			t.Fatalf("reenable %s: %+v %v", status, same, err)
		}
	}
	same.TLSMode = model.TLSDisabled
	same.HTTPSEnabled = false
	same.HTTPSRedirect = false
	same.CertificateID = ""
	same.ACMEChallenge = ""
	same.DNSProviderRef = ""
	same, err = c.UpdateDomain(ctx, same, same.Revision)
	if err != nil || same.CertificateID != "" || same.Status != model.DomainReady {
		t.Fatalf("disable TLS: %+v %v", same, err)
	}
	if _, err = c.UpdateDomain(ctx, same, same.Revision-1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale domain overwritten: %v", err)
	}
}

func TestDisabledManagedDomainDefersProvisioning(t *testing.T) {
	c, d := domainUpdateFixture(t)
	d.Enabled = false
	d.TLSMode = model.TLSManaged
	d.HTTPSEnabled = true
	got, err := c.UpdateDomain(context.Background(), d, d.Revision)
	if err != nil || got.CertificateID != "" || got.Status != model.DomainDisabled {
		t.Fatalf("disabled domain provisioned: %+v %v", got, err)
	}
	got.Enabled = true
	got, err = c.UpdateDomain(context.Background(), got, got.Revision)
	if err != nil || got.CertificateID == "" || got.Status != model.DomainCertPending {
		t.Fatalf("enabled managed domain not provisioned: %+v %v", got, err)
	}
}
