package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func certificateFixtureKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
func certificateFixturePEM(t *testing.T, template, parent *x509.Certificate, key, signer *ecdsa.PrivateKey) ([]byte, []byte) {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: raw})
}
func certificateTemplate(now time.Time, isCA bool) *x509.Certificate {
	return &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "app.example.test"}, DNSNames: []string{"app.example.test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), BasicConstraintsValid: true, IsCA: isCA, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign}
}

func TestCertificateKeyPairValidityAndChainBoundaries(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	rootKey := certificateFixtureKey(t)
	leafKey := certificateFixtureKey(t)
	for _, tc := range []struct {
		name, want  string
		leaf        func(*x509.Certificate)
		issuer      func(*x509.Certificate)
		wrongIssuer bool
	}{
		{"valid chain", "", nil, nil, false},
		{"leaf is CA", "must not be a CA", func(c *x509.Certificate) { c.IsCA = true }, nil, false},
		{"client-only leaf", "server authentication", func(c *x509.Certificate) { c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth} }, nil, false},
		{"future leaf", "not valid yet", func(c *x509.Certificate) { c.NotBefore = now.Add(time.Minute) }, nil, false},
		{"expired leaf", "expired", func(c *x509.Certificate) { c.NotAfter = now.Add(-time.Minute) }, nil, false},
		{"issuer is not CA", "not a CA", nil, func(c *x509.Certificate) { c.IsCA = false }, false},
		{"issuer lacks constraints", "not a CA", nil, func(c *x509.Certificate) { c.BasicConstraintsValid = false }, false},
		{"future issuer", "validity period", nil, func(c *x509.Certificate) { c.NotBefore = now.Add(time.Minute) }, false},
		{"expired issuer", "validity period", nil, func(c *x509.Certificate) { c.NotAfter = now.Add(-time.Minute) }, false},
		{"issuer cannot sign", "cannot sign", nil, func(c *x509.Certificate) { c.KeyUsage = x509.KeyUsageDigitalSignature }, false},
		{"issuer unspecified usage", "", nil, func(c *x509.Certificate) { c.KeyUsage = 0 }, false},
		{"wrong signature", "signature", nil, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, leaf := certificateTemplate(now, true), certificateTemplate(now, false)
			if tc.leaf != nil {
				tc.leaf(leaf)
			}
			if tc.issuer != nil {
				tc.issuer(root)
			}
			rootPEM, _ := certificateFixturePEM(t, root, root, rootKey, rootKey)
			leafPEM, keyPEM := certificateFixturePEM(t, leaf, root, leafKey, rootKey)
			if tc.wrongIssuer {
				other := certificateFixtureKey(t)
				rootPEM, _ = certificateFixturePEM(t, root, root, other, other)
			}
			parsed, err := validateKeyPair(append(leafPEM, rootPEM...), keyPEM, now)
			if tc.want == "" {
				if err != nil || parsed == nil || parsed.IsCA {
					t.Fatalf("valid server chain rejected: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid chain accepted or misdiagnosed: %v", err)
			}
		})
	}
	leaf := certificateTemplate(now, false)
	certificate, key := certificateFixturePEM(t, leaf, leaf, leafKey, leafKey)
	for _, at := range []time.Time{leaf.NotBefore, leaf.NotAfter} {
		if _, err := validateKeyPair(certificate, key, at); err != nil {
			t.Fatalf("certificate validity boundary rejected: %v", err)
		}
	}
	if _, err := validateKeyPair([]byte("invalid"), key, now); err == nil {
		t.Fatal("invalid certificate PEM accepted")
	}
	other := certificateFixtureKey(t)
	_, wrongKey := certificateFixturePEM(t, leaf, leaf, other, other)
	if _, err := validateKeyPair(certificate, wrongKey, now); err == nil {
		t.Fatal("mismatched private key accepted")
	}
	badChain := append(append([]byte{}, certificate...), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("corrupt DER")})...)
	if _, err := validateKeyPair(badChain, key, now); err == nil || !strings.Contains(err.Error(), "chain") {
		t.Fatalf("malformed issuer accepted: %v", err)
	}
	for _, usages := range [][]x509.ExtKeyUsage{nil, {x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}, {x509.ExtKeyUsageAny}} {
		if !allowsServerAuthentication(&x509.Certificate{ExtKeyUsage: usages}) {
			t.Fatal("server authentication usage rejected")
		}
	}
}

type certificateBoundaryStore struct {
	storage.Store
	certificate                 model.Certificate
	snapshot                    model.Snapshot
	certificateErr, snapshotErr error
}

func (s *certificateBoundaryStore) GetCertificate(context.Context, string) (model.Certificate, error) {
	return s.certificate, s.certificateErr
}
func (s *certificateBoundaryStore) BuildSnapshot(context.Context, string) (model.Snapshot, error) {
	return s.snapshot, s.snapshotErr
}

func TestDomainCertificateMatchingAndPendingACMEBoundaries(t *testing.T) {
	c, _, pool := controlFixture(t)
	base := c.store
	ctx := context.Background()
	key := certificateFixtureKey(t)
	leaf := certificateTemplate(c.now(), false)
	certPEM, _ := certificateFixturePEM(t, leaf, leaf, key, key)
	valid := model.Certificate{ID: "cert_fixture", PoolID: pool.ID, Provider: "custom", Status: model.CertificateActive, CertificatePEM: string(certPEM)}
	for _, tc := range []struct {
		name   string
		mode   model.TLSMode
		mutate func(*model.Certificate)
		valid  bool
	}{
		{"custom valid", model.TLSCustom, func(*model.Certificate) {}, true},
		{"wrong pool", model.TLSCustom, func(c *model.Certificate) { c.PoolID = "other" }, false},
		{"managed requires ACME", model.TLSManaged, func(*model.Certificate) {}, false},
		{"custom requires custom", model.TLSCustom, func(c *model.Certificate) { c.Provider = "acme" }, false},
		{"custom requires active", model.TLSCustom, func(c *model.Certificate) { c.Status = model.CertificateRevoked }, false},
		{"disabled TLS rejects certificate", model.TLSDisabled, func(*model.Certificate) {}, false},
		{"missing PEM", model.TLSCustom, func(c *model.Certificate) { c.CertificatePEM = "" }, false},
		{"wrong PEM type", model.TLSCustom, func(c *model.Certificate) {
			c.CertificatePEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("invalid")}))
		}, false},
		{"invalid DER", model.TLSCustom, func(c *model.Certificate) {
			c.CertificatePEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid")}))
		}, false},
		{"pending matching ACME", model.TLSManaged, func(c *model.Certificate) {
			c.Provider = "acme"
			c.Status = model.CertificatePending
			c.CertificatePEM = ""
			c.Domains = []string{"other.example.test", "app.example.test"}
		}, true},
		{"pending nonmatching ACME", model.TLSManaged, func(c *model.Certificate) {
			c.Provider = "acme"
			c.Status = model.CertificatePending
			c.CertificatePEM = ""
			c.Domains = []string{"other.example.test"}
		}, false},
		{"active ACME missing leaf", model.TLSManaged, func(c *model.Certificate) { c.Provider = "acme"; c.CertificatePEM = "" }, false},
		{"active ACME", model.TLSManaged, func(c *model.Certificate) { c.Provider = "acme" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cert := valid
			tc.mutate(&cert)
			c.store = &certificateBoundaryStore{Store: base, certificate: cert}
			_, err := c.validateDomainCertificate(ctx, pool.ID, "app.example.test", cert.ID, tc.mode)
			if (err == nil) != tc.valid {
				t.Fatalf("certificate policy=%v, expected valid=%t", err, tc.valid)
			}
		})
	}
	c.store = &certificateBoundaryStore{Store: base, certificate: valid}
	if _, err := c.validateDomainCertificate(ctx, pool.ID, "other.example.test", valid.ID, model.TLSCustom); err == nil {
		t.Fatal("certificate accepted for uncovered host")
	}
	c.store = &certificateBoundaryStore{Store: base, certificateErr: storage.ErrNotFound}
	if _, err := c.validateDomainCertificate(ctx, pool.ID, "app.example.test", valid.ID, model.TLSCustom); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing certificate=%v", err)
	}
}

func TestGatewaySnapshotDecryptsOnlyAtDistributionBoundary(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	base := c.store
	secret := "fixture-private-material"
	cipher, err := c.keyring.Encrypt([]byte(secret), []byte("cert_fixture"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := model.Snapshot{PoolID: pool.ID, Certificates: map[string]model.SnapshotCertificate{"cert_fixture": {ID: "cert_fixture", CertificatePEM: "fixture-public"}}}
	boundary := &certificateBoundaryStore{Store: base, snapshot: snapshot, certificate: model.Certificate{ID: "cert_fixture", EncryptedPrivateKey: cipher}}
	c.store = boundary
	result, err := c.BuildGatewaySnapshot(ctx, pool.ID)
	if err != nil || result.Certificates["cert_fixture"].PrivateKeyPEM != secret {
		t.Fatalf("distribution decryption failed: %v", err)
	}
	public, err := json.Marshal(result)
	if err != nil || strings.Contains(string(public), secret) {
		t.Fatal("ordinary snapshot JSON leaked private key")
	}
	boundary.certificate.EncryptedPrivateKey = []byte("corrupt")
	if result, err := c.BuildGatewaySnapshot(ctx, pool.ID); err == nil || result.Certificates != nil {
		t.Fatal("failed key decryption returned partial snapshot")
	}
	boundary.certificateErr = storage.ErrNotFound
	if _, err := c.BuildGatewaySnapshot(ctx, pool.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing key material=%v", err)
	}
	boundary.snapshotErr = storage.ErrConflict
	if _, err := c.BuildGatewaySnapshot(ctx, pool.ID); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("snapshot failure=%v", err)
	}
}
