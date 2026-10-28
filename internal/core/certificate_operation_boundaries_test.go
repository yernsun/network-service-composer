package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func customCertificateInputFixture(t *testing.T, c *Core, d model.Domain) CustomCertificateInput {
	t.Helper()
	key := certificateFixtureKey(t)
	leaf := certificateTemplate(c.now(), false)
	leaf.DNSNames = []string{d.Host}
	certificate, privateKey := certificateFixturePEM(t, leaf, leaf, key, key)
	return CustomCertificateInput{PoolID: d.PoolID, DomainIDs: []string{d.ID}, CertificatePEM: string(certificate), PrivateKeyPEM: string(privateKey)}
}

func TestCustomCertificateImportAssociatesEncryptedMaterial(t *testing.T) {
	c, d := domainUpdateFixture(t)
	ctx := context.Background()
	input := customCertificateInputFixture(t, c, d)
	certificate, err := c.CreateCustomCertificate(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if certificate.Provider != "custom" || certificate.Status != model.CertificateActive || certificate.PrimaryDomain != d.Host || len(certificate.Domains) != 1 || certificate.Domains[0] != d.Host || certificate.Fingerprint == "" || certificate.SerialNumber == "" || certificate.NotBefore == nil || certificate.NotAfter == nil {
		t.Fatalf("certificate identity incomplete: %+v", certificate)
	}
	if len(certificate.EncryptedPrivateKey) == 0 || strings.Contains(string(certificate.EncryptedPrivateKey), "PRIVATE KEY") {
		t.Fatal("private key stored without encryption")
	}
	plain, _, err := c.keyring.Decrypt(certificate.EncryptedPrivateKey, []byte(certificate.ID))
	if err != nil || string(plain) != input.PrivateKeyPEM {
		t.Fatalf("encrypted key cannot be recovered for this certificate: %v", err)
	}
	if _, _, err = c.keyring.Decrypt(certificate.EncryptedPrivateKey, []byte("foreign")); err == nil {
		t.Fatal("encrypted key not bound to certificate identity")
	}
	d, err = c.store.GetDomain(ctx, d.ID)
	if err != nil || d.CertificateID != certificate.ID || d.TLSMode != model.TLSCustom || d.Status != model.DomainReady {
		t.Fatalf("certificate/domain relation missing: %+v %v", d, err)
	}
	d.HTTPSRedirect = false
	d, err = c.UpdateDomain(ctx, d, d.Revision)
	if err != nil || d.Status != model.DomainReady || d.HTTPSRedirect {
		t.Fatalf("custom domain update lost active certificate: %+v %v", d, err)
	}
	if _, err = c.MarkCertificateForRenewal(ctx, certificate.ID); err == nil {
		t.Fatal("custom certificate sent to ACME renewal")
	}
	revoked, err := c.RevokeCertificate(ctx, certificate.ID)
	if err != nil || revoked.Status != model.CertificateRevoked || revoked.NextAttemptAt == nil || revoked.CertificatePEM != certificate.CertificatePEM {
		t.Fatalf("revocation request lost material/history: %+v %v", revoked, err)
	}
}

func TestCustomCertificateAssetCanPrecedeDomain(t *testing.T) {
	c, existing := domainUpdateFixture(t)
	ctx := context.Background()
	identity := existing
	identity.Host = "new.example.test"
	input := customCertificateInputFixture(t, c, identity)
	input.DomainIDs = nil
	certificate, err := c.CreateCustomCertificate(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if certificate.PrimaryDomain != identity.Host || len(certificate.Domains) != 1 || certificate.Domains[0] != identity.Host {
		t.Fatalf("unbound SANs lost: %+v", certificate)
	}
	stored, err := c.store.GetDomain(ctx, existing.ID)
	if err != nil || stored.Revision != existing.Revision || stored.CertificateID != "" {
		t.Fatalf("asset import mutated existing domain: %+v %v", stored, err)
	}
	listed, err := c.store.ListCertificates(ctx, storage.Page{})
	if err != nil || len(listed) != 1 || len(listed[0].Domains) != 1 || listed[0].Domains[0] != identity.Host {
		t.Fatalf("asset unavailable for selection: %+v %v", listed, err)
	}
	domain, err := c.CreateDomain(ctx, CreateDomainInput{PoolID: existing.PoolID, DomainConfigInput: DomainConfigInput{Host: identity.Host, EntryPoints: []string{EntryPointHTTPS}, TLS: DomainTLSInput{Mode: model.TLSCustom, CertificateID: certificate.ID}}})
	if err != nil || domain.CertificateID != certificate.ID || domain.Status != model.DomainReady {
		t.Fatalf("new domain could not select asset: %+v %v", domain, err)
	}
	if _, err := c.validateDomainCertificate(ctx, existing.PoolID, "wrong.example.test", certificate.ID, model.TLSCustom); err == nil {
		t.Fatal("accepted wrong hostname")
	}
	if _, err := c.validateDomainCertificate(ctx, "foreign-pool", identity.Host, certificate.ID, model.TLSCustom); err == nil {
		t.Fatal("accepted foreign pool")
	}
	c.now = func() time.Time { return certificate.NotAfter.Add(time.Second) }
	if _, err := c.validateDomainCertificate(ctx, existing.PoolID, identity.Host, certificate.ID, model.TLSCustom); err == nil {
		t.Fatal("accepted expired asset")
	}
}

func TestCustomCertificateInvalidImportDoesNotMutateDomain(t *testing.T) {
	for _, kind := range []string{"duplicate", "missing domain", "foreign pool", "invalid keypair", "hostname mismatch", "encryption unavailable", "install unavailable", "read unavailable"} {
		t.Run(kind, func(t *testing.T) {
			c, d := domainUpdateFixture(t)
			ctx := context.Background()
			input := customCertificateInputFixture(t, c, d)
			original := c.store
			failure := errors.New("certificate storage unavailable")
			switch kind {
			case "duplicate":
				input.DomainIDs = append(input.DomainIDs, d.ID)
			case "missing domain":
				input.DomainIDs = []string{id.New(id.Domain)}
			case "foreign pool":
				input.PoolID = id.New(id.Pool)
			case "invalid keypair":
				input.PrivateKeyPEM = "invalid"
			case "hostname mismatch":
				other := d
				other.Host = "other.test"
				input = customCertificateInputFixture(t, c, other)
			case "encryption unavailable":
				c.keyring = &security.Keyring{}
			case "install unavailable":
				c.store = &resourceBoundaryStore{&nginxBoundaryStore{Store: original, method: "install certificate", failure: failure}}
			case "read unavailable":
				c.store = &resourceBoundaryStore{&nginxBoundaryStore{Store: original, method: "domain", failure: failure}}
			}
			if _, err := c.CreateCustomCertificate(ctx, input); err == nil {
				t.Fatal("invalid certificate import accepted")
			}
			stored, err := original.GetDomain(ctx, d.ID)
			if err != nil || stored.Revision != d.Revision || stored.CertificateID != "" || stored.TLSMode != model.TLSDisabled {
				t.Fatalf("failed import altered domain: %+v %v", stored, err)
			}
			certs, err := original.ListCertificates(ctx, storage.Page{})
			if err != nil || len(certs) != 0 {
				t.Fatalf("failed import left certificate: %+v %v", certs, err)
			}
		})
	}
}

func TestCertificateRenewalAndRevocationStorageFailures(t *testing.T) {
	failure := errors.New("certificate write interrupted")
	ctx := context.Background()
	for _, operation := range []string{"renew", "revoke"} {
		for _, fault := range []string{"read", "write", "refresh", "success"} {
			t.Run(operation+"/"+fault, func(t *testing.T) {
				c, d := domainUpdateFixture(t)
				d.TLSMode = model.TLSManaged
				d.HTTPSEnabled = true
				d, err := c.UpdateDomain(ctx, d, d.Revision)
				if err != nil {
					t.Fatal(err)
				}
				original := c.store
				method, nth := "certificate", 1
				if fault == "write" {
					method = "update certificate"
					nth = 0
				}
				if fault == "refresh" {
					nth = 2
				}
				if fault != "success" {
					c.store = &resourceBoundaryStore{&nginxBoundaryStore{Store: original, method: method, nth: nth, failure: failure}}
				}
				var got model.Certificate
				if operation == "renew" {
					got, err = c.MarkCertificateForRenewal(ctx, d.CertificateID)
				} else {
					got, err = c.RevokeCertificate(ctx, d.CertificateID)
				}
				if fault != "success" {
					if !errors.Is(err, failure) {
						t.Fatalf("operation hid storage failure: %v", err)
					}
				} else {
					want := model.CertificateRenewing
					if operation == "revoke" {
						want = model.CertificateRevoked
					}
					if err != nil || got.Status != want || got.NextAttemptAt == nil || got.LastError != "" {
						t.Fatalf("operation not queued: %+v %v", got, err)
					}
				}
				if fault == "read" || fault == "write" {
					stored, e := original.GetCertificate(ctx, d.CertificateID)
					if e != nil || stored.Status != model.CertificatePending {
						t.Fatalf("failed operation changed status: %+v %v", stored, e)
					}
				}
			})
		}
	}
}
