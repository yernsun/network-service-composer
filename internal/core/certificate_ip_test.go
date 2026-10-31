package core

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestIPCertificateAssetsImportBindingAndProtection(t *testing.T) {
	c, domain := domainUpdateFixture(t)
	cert, err := c.CreateSelfSignedCertificate(t.Context(), SelfSignedCertificateInput{PoolID: domain.PoolID, IPAddresses: []string{"192.0.2.7", "2001:db8::7", "::ffff:192.0.2.7"}})
	if err != nil {
		t.Fatal(err)
	}
	if cert.PrimaryDomain != "192.0.2.7" || len(cert.IPAddresses) != 2 || len(cert.Domains) != 0 || cert.Domains == nil {
		t.Fatal("IP SAN metadata lost", cert)
	}
	key, _, err := c.keyring.Decrypt(cert.EncryptedPrivateKey, []byte(cert.ID))
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair([]byte(cert.CertificatePEM), key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.VerifyHostname("2001:db8::7") != nil {
		t.Fatal("IP SAN not encoded", err)
	}
	imported, err := c.CreateCustomCertificate(t.Context(), CustomCertificateInput{PoolID: domain.PoolID, CertificatePEM: cert.CertificatePEM, PrivateKeyPEM: string(key)})
	if err != nil || len(imported.IPAddresses) != 2 {
		t.Fatal("IP-only import failed", err)
	}
	domain.HTTPSEnabled = true
	domain.TLSMode = model.TLSCustom
	domain.CertificateID = cert.ID
	if _, err = c.UpdateDomain(t.Context(), domain, domain.Revision); err == nil {
		t.Fatal("IP-only certificate bound to a DNS Domain")
	}
	if _, err = NormalizeHost("192.0.2.7"); err == nil {
		t.Fatal("Domain model accepted an IP")
	}
	rows, err := c.store.ListCertificates(t.Context(), storage.Page{Limit: 200})
	if err != nil || len(rows) != 2 || len(rows[0].IPAddresses) != 2 || rows[0].Domains == nil {
		t.Fatal("IP metadata lost on list", err)
	}
	for _, ip := range []string{"invalid", "0.0.0.0", "224.0.0.1", "fe80::1%eth0"} {
		if _, err = c.CreateSelfSignedCertificate(t.Context(), SelfSignedCertificateInput{PoolID: domain.PoolID, IPAddresses: []string{ip}}); err == nil {
			t.Fatal("invalid IP SAN accepted", ip)
		}
	}
	if _, err = c.RevokeCertificate(t.Context(), imported.ID); err != nil {
		t.Fatal("ordinary asset revocation blocked", err)
	}
	installation := bootstrap.Installation{ID: "install-test", CertificateID: cert.ID}
	payload, _ := json.Marshal(installation)
	if err = c.store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: storage.ControlDocument{ID: bootstrap.InstallationDocumentID, Kind: bootstrap.InstallationDocumentKind, ParentID: installation.ID, Payload: payload, CreatedAt: c.now(), UpdatedAt: c.now()}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.RevokeCertificate(t.Context(), cert.ID); err != storage.ErrSystemProtected {
		t.Fatal("system console identity revocation allowed", err)
	}
	raw, _ := json.Marshal(cert)
	if !strings.Contains(string(raw), `"domains":[]`) {
		t.Fatal("IP-only certificate violates the array contract")
	}
	if strings.Contains(string(raw), "PRIVATE KEY") {
		t.Fatal("key leaked")
	}
}

type installationCertificateStore struct {
	storage.Store
	document storage.ControlDocument
	failure  error
}

func (s installationCertificateStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if key == bootstrap.InstallationDocumentID {
		return s.document, s.failure
	}
	return s.Store.GetControlDocument(ctx, key)
}

func TestCertificateRevocationFailsClosedOnCorruptInstallationAuthority(t *testing.T) {
	c, domain := domainUpdateFixture(t)
	cert, err := c.CreateSelfSignedCertificate(t.Context(), SelfSignedCertificateInput{PoolID: domain.PoolID, IPAddresses: []string{"192.0.2.12"}})
	if err != nil {
		t.Fatal(err)
	}
	original := c.store
	for _, scenario := range []string{"kind", "payload", "unavailable"} {
		document := storage.ControlDocument{Kind: bootstrap.InstallationDocumentKind, Payload: json.RawMessage(`{}`)}
		var failure error
		switch scenario {
		case "kind":
			document.Kind = "foreign"
		case "payload":
			document.Payload = json.RawMessage(`{"id":true}`)
		case "unavailable":
			failure = fmt.Errorf("installation authority unavailable")
		}
		c.store = installationCertificateStore{Store: original, document: document, failure: failure}
		if _, err = c.RevokeCertificate(t.Context(), cert.ID); err == nil {
			t.Fatal("revocation ignored unverified system authority", scenario)
		}
		saved, err := original.GetCertificate(t.Context(), cert.ID)
		if err != nil || saved.Status != model.CertificateActive {
			t.Fatal("failed protection check changed certificate", err)
		}
	}
	c.store = installationCertificateStore{Store: original, document: storage.ControlDocument{Kind: bootstrap.InstallationDocumentKind, Payload: json.RawMessage(`{"id":"owned-installation","certificate_id":"other-system-certificate"}`)}}
	if _, err = c.RevokeCertificate(t.Context(), cert.ID); err != nil {
		t.Fatal("ordinary certificate blocked by another system asset", err)
	}
}
