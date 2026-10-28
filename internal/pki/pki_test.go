package pki

import (
	"crypto/x509"
	"encoding/pem"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
)

func TestIssueGatewayCertificateBindsIdentity(t *testing.T) {
	directory := t.TempDir()
	caCert, caKey := filepath.Join(directory, "ca.crt"), filepath.Join(directory, "ca.key")
	if err := InitCA(caCert, caKey, "test CA", 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	poolID, gatewayID := id.New(id.Pool), id.New(id.Gateway)
	identity, err := GatewayIdentityURI(poolID, gatewayID)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(directory, "agent.crt"), filepath.Join(directory, "agent.key")
	if err = Issue(caCert, caKey, certPath, keyPath, CertificateOptions{CommonName: gatewayID, URIs: []*url.URL{identity}, Client: true, Duration: time.Hour}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(certificate.URIs) != 1 || certificate.URIs[0].String() != identity.String() {
		t.Fatalf("unexpected identity URI: %v", certificate.URIs)
	}
	if len(certificate.ExtKeyUsage) != 1 || certificate.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatalf("unexpected key usage: %v", certificate.ExtKeyUsage)
	}
	if len(certificate.Subject.Organization) != 1 || certificate.Subject.Organization[0] != "Network Service Composer" {
		t.Fatalf("unexpected product organization: %v", certificate.Subject.Organization)
	}
}

func TestDefaultCABrand(t *testing.T) {
	directory := t.TempDir()
	certPath, keyPath := filepath.Join(directory, "ca.crt"), filepath.Join(directory, "ca.key")
	if err := InitCA(certPath, keyPath, "", time.Hour); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if certificate.Subject.CommonName != "Network Service Composer Offline CA" || len(certificate.Subject.Organization) != 1 || certificate.Subject.Organization[0] != "Network Service Composer" {
		t.Fatalf("unexpected CA brand: %v", certificate.Subject)
	}
}

func TestIssueRejectsMismatchedCAKey(t *testing.T) {
	directory := t.TempDir()
	firstCert, firstKey := filepath.Join(directory, "first.crt"), filepath.Join(directory, "first.key")
	secondCert, secondKey := filepath.Join(directory, "second.crt"), filepath.Join(directory, "second.key")
	if err := InitCA(firstCert, firstKey, "first", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := InitCA(secondCert, secondKey, "second", time.Hour); err != nil {
		t.Fatal(err)
	}
	err := Issue(firstCert, secondKey, filepath.Join(directory, "leaf.crt"), filepath.Join(directory, "leaf.key"), CertificateOptions{CommonName: "leaf", Client: true, Duration: time.Minute})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Issue() error = %v, want mismatched CA key rejection", err)
	}
}
