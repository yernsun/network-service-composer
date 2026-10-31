package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

// SelfSigned creates a server leaf with exact DNS/IP SANs. Callers normalize DNS
// names according to their own asset contract; this does not relax Domain or
// ACME's DNS-only model. Validity uses calendar years, not 365-day approximations.
func SelfSigned(names []string, addresses []net.IP, years int, now time.Time) ([]byte, []byte, error) {
	if years < 1 || years > 100 || len(names)+len(addresses) < 1 || len(names)+len(addresses) > 100 {
		return nil, nil, fmt.Errorf("certificate requires 1–100 SANs and 1–100 years")
	}
	for _, ip := range addresses {
		if ip.To16() == nil {
			return nil, nil, fmt.Errorf("certificate IP SAN is invalid")
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	serial.Add(serial, new(big.Int).SetInt64(1))
	commonName := ""
	if len(names) > 0 {
		commonName = names[0]
	} else {
		commonName = addresses[0].String()
	}
	now = now.UTC().Truncate(time.Second)
	leaf := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: commonName}, DNSNames: names, IPAddresses: addresses, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(years, 0, 0), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, leaf, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	defer clear(private)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}), nil
}
