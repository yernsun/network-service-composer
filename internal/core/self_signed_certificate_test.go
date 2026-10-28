package core

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestSelfSignedCertificateValidityBindingAndDistribution(t *testing.T) {
	c, domain := domainUpdateFixture(t)
	now := time.Date(2028, 2, 29, 12, 30, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	cert, err := c.CreateSelfSignedCertificate(t.Context(), SelfSignedCertificateInput{PoolID: domain.PoolID, Domains: []string{strings.ToUpper(domain.Host), domain.Host, "other.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if cert.Provider != "self-signed" || cert.Status != model.CertificateActive || len(cert.Domains) != 2 || !cert.NotAfter.Equal(now.AddDate(20, 0, 0)) || !cert.NotBefore.Equal(now.Add(-5*time.Minute)) {
		t.Fatalf("wrong certificate metadata: %+v", cert)
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
	if err != nil {
		t.Fatal(err)
	}
	if leaf.IsCA || leaf.SerialNumber.Sign() <= 0 || len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Fatal("not a server leaf")
	}
	if err = leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature); err != nil {
		t.Fatal("not self signed", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: domain.Host, CurrentTime: now}); err != nil {
		t.Fatal("explicit trust failed", err)
	}
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: x509.NewCertPool(), DNSName: domain.Host, CurrentTime: now}); err == nil {
		t.Fatal("trusted without explicit trust")
	}
	rows, err := c.store.ListCertificates(t.Context(), storage.Page{Limit: 200})
	if err != nil || len(rows) != 1 || len(rows[0].Domains) != 2 {
		t.Fatalf("unbound SANs lost: %v %v", rows, err)
	}
	raw, _ := json.Marshal(cert)
	if strings.Contains(string(raw), "PRIVATE KEY") || strings.Contains(string(raw), "certificate_pem") {
		t.Fatal("material leaked in metadata")
	}
	domain.CertificateID, domain.TLSMode, domain.HTTPSEnabled = cert.ID, model.TLSCustom, true
	domain, err = c.UpdateDomain(t.Context(), domain, domain.Revision)
	if err != nil || domain.CertificateID != cert.ID {
		t.Fatal("binding rejected", err)
	}
	snapshot, err := c.BuildGatewaySnapshot(t.Context(), domain.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	material, ok := snapshot.Certificates[cert.ID]
	if !ok || material.PrivateKeyPEM != string(key) || material.CertificatePEM != cert.CertificatePEM {
		t.Fatal("Gateway material missing")
	}
	if _, err = c.MarkCertificateForRenewal(t.Context(), cert.ID); err == nil {
		t.Fatal("self signed entered ACME renewal")
	}
}

func TestSelfSignedCertificateExplicitValidityAndAtomicAttachment(t *testing.T) {
	c, domain := domainUpdateFixture(t)
	now := time.Date(2026, 10, 28, 12, 30, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	years := 3
	cert, err := c.CreateSelfSignedCertificate(t.Context(), SelfSignedCertificateInput{PoolID: domain.PoolID, Domains: []string{domain.Host}, DomainIDs: []string{domain.ID}, ValidityYears: &years})
	if err != nil || !cert.NotAfter.Equal(now.AddDate(3, 0, 0)) {
		t.Fatal(cert.NotAfter, err)
	}
	bound, err := c.store.GetDomain(t.Context(), domain.ID)
	if err != nil || bound.CertificateID != cert.ID || bound.TLSMode != model.TLSCustom {
		t.Fatal(bound, err)
	}
	if _, err = c.RevokeCertificate(t.Context(), cert.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.UpdateDomain(t.Context(), bound, bound.Revision); err == nil {
		t.Fatal("revoked self signed accepted")
	}
}

func TestSelfSignedCertificateRejectsInvalidInputWithoutWrites(t *testing.T) {
	for _, name := range []string{"zero", "negative", "too-long", "empty", "too-many", "ip", "wildcard", "missing-pool", "uncovered", "duplicate-binding", "missing-domain"} {
		t.Run(name, func(t *testing.T) {
			c, domain := domainUpdateFixture(t)
			in := SelfSignedCertificateInput{PoolID: domain.PoolID, Domains: []string{domain.Host}}
			years := 20
			switch name {
			case "zero":
				in.ValidityYears = new(int)
			case "negative":
				years = -1
				in.ValidityYears = &years
			case "too-long":
				years = 101
				in.ValidityYears = &years
			case "empty":
				in.Domains = nil
			case "too-many":
				in.Domains = make([]string, 101)
			case "ip":
				in.Domains = []string{"127.0.0.1"}
			case "wildcard":
				in.Domains = []string{"*.example.test"}
			case "missing-pool":
				in.PoolID = "missing"
			case "uncovered":
				in.Domains = []string{"other.example.test"}
				in.DomainIDs = []string{domain.ID}
			case "duplicate-binding":
				in.DomainIDs = []string{domain.ID, domain.ID}
			case "missing-domain":
				in.DomainIDs = []string{"missing"}
			}
			if _, err := c.CreateSelfSignedCertificate(t.Context(), in); err == nil {
				t.Fatal("invalid input accepted")
			}
			rows, err := c.store.ListCertificates(t.Context(), storage.Page{Limit: 200})
			if err != nil || len(rows) != 0 {
				t.Fatal("failed signing left assets", err)
			}
		})
	}
}
