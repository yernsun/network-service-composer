package certificate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestIssuedChainRejectsUnusableLeafAndIssuer(t *testing.T) {
	for _, name := range []string{"valid", "no EKU", "any EKU", "client then server EKU", "client only", "CA leaf", "future leaf", "expired leaf", "invalid key", "wrong key", "malformed issuer", "issuer not CA", "issuer constraints missing", "future issuer", "expired issuer", "issuer cannot sign", "issuer no key usage", "wrong issuer signature"} {
		t.Run(name, func(t *testing.T) {
			newKey := func() *ecdsa.PrivateKey {
				k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				return k
			}
			issuerKey, leafKey := newKey(), newKey()
			now := time.Now().UTC()
			issuer := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageCertSign}
			leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"app.example.test"}, BasicConstraintsValid: true, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
			valid := false
			switch name {
			case "valid":
				valid = true
			case "no EKU":
				leaf.ExtKeyUsage = nil
				valid = true
			case "any EKU":
				leaf.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageAny}
				valid = true
			case "client then server EKU":
				leaf.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}
				valid = true
			case "client only":
				leaf.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
			case "CA leaf":
				leaf.IsCA = true
			case "future leaf":
				leaf.NotBefore = now.Add(time.Hour)
			case "expired leaf":
				leaf.NotAfter = now.Add(-time.Minute)
			case "issuer not CA":
				issuer.IsCA = false
			case "issuer constraints missing":
				issuer.BasicConstraintsValid = false
			case "future issuer":
				issuer.NotBefore = now.Add(time.Hour)
			case "expired issuer":
				issuer.NotAfter = now.Add(-time.Minute)
			case "issuer cannot sign":
				issuer.KeyUsage = x509.KeyUsageDigitalSignature
			case "issuer no key usage":
				issuer.KeyUsage = 0
				valid = true
			}
			encode := func(cert, parent *x509.Certificate, key *ecdsa.PrivateKey, signing *ecdsa.PrivateKey) []byte {
				der, err := x509.CreateCertificate(rand.Reader, cert, parent, &key.PublicKey, signing)
				if err != nil {
					t.Fatal(err)
				}
				return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
			}
			leafPEM := encode(leaf, issuer, leafKey, issuerKey)
			if name == "wrong issuer signature" {
				issuerKey = newKey()
			}
			issuerPEM := encode(issuer, issuer, issuerKey, issuerKey)
			if name == "malformed issuer" {
				issuerPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid DER")})
			}
			if name == "wrong key" {
				leafKey = newKey()
			}
			der, err := x509.MarshalPKCS8PrivateKey(leafKey)
			if err != nil {
				t.Fatal(err)
			}
			keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
			if name == "invalid key" {
				keyPEM = []byte("invalid key")
			}
			chain := append(leafPEM, issuerPEM...)
			result, err := validateIssued(chain, keyPEM, []string{"app.example.test"})
			if (err == nil) != valid {
				t.Fatalf("chain validation valid=%t: %v", valid, err)
			}
			if valid && (result == nil || result.IsCA || result.VerifyHostname("app.example.test") != nil) {
				t.Fatal("usable leaf was not returned")
			}
			if !valid && result != nil {
				t.Fatal("invalid chain returned an accepted leaf")
			}
			if err != nil && strings.Contains(err.Error(), string(keyPEM)) {
				t.Fatal("validation error exposed private key")
			}
		})
	}
}
