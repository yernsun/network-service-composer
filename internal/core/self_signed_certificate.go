package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

type SelfSignedCertificateInput struct {
	PoolID        string   `json:"pool_id"`
	Domains       []string `json:"domains"`
	DomainIDs     []string `json:"domain_ids,omitempty"`
	ValidityYears *int     `json:"validity_years,omitempty"`
}

// Self-signed assets use custom TLS binding and never enter the ACME worker.
func (c *Core) CreateSelfSignedCertificate(ctx context.Context, input SelfSignedCertificateInput) (model.Certificate, error) {
	years := 20
	if input.ValidityYears != nil {
		years = *input.ValidityYears
	}
	if years < 1 || years > 100 || len(input.Domains) < 1 || len(input.Domains) > 100 {
		return model.Certificate{}, invalid("validation.self_signed_invalid", "domains", "provide 1–100 exact DNS names and a validity of 1–100 years")
	}
	if _, err := c.store.GetPool(ctx, input.PoolID); err != nil {
		return model.Certificate{}, err
	}
	names := make([]string, 0, len(input.Domains))
	seen := map[string]bool{}
	for _, name := range input.Domains {
		host, err := NormalizeHost(name)
		if err != nil {
			return model.Certificate{}, invalid("validation.self_signed_invalid", "domains", "provide exact DNS names")
		}
		if !seen[host] {
			names = append(names, host)
			seen[host] = true
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return model.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return model.Certificate{}, err
	}
	serial.Add(serial, big.NewInt(1))
	now := c.now().UTC().Truncate(time.Second)
	leaf := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(years, 0, 0), BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, leaf, &key.PublicKey, key)
	if err != nil {
		return model.Certificate{}, err
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return model.Certificate{}, err
	}
	defer clear(private)
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private})
	defer clear(privatePEM)
	return c.installCertificate(ctx, CustomCertificateInput{PoolID: input.PoolID, DomainIDs: input.DomainIDs,
		CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), PrivateKeyPEM: string(privatePEM)}, "self-signed")
}
