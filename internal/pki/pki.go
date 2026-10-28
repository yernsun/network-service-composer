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
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
)

type CertificateOptions struct {
	CommonName  string
	DNSNames    []string
	IPAddresses []net.IP
	URIs        []*url.URL
	Duration    time.Duration
	Client      bool
	Server      bool
}

func InitCA(certPath, keyPath, commonName string, duration time.Duration) error {
	if commonName == "" {
		commonName = "Network Service Composer Offline CA"
	}
	if duration <= 0 {
		duration = 10 * 365 * 24 * time.Hour
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}
	now := time.Now().UTC()
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: commonName, Organization: []string{"Network Service Composer"}}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(duration), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature, MaxPathLen: 0}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create CA certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err = writeExclusive(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	if err = writeExclusive(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		_ = os.Remove(keyPath)
		return err
	}
	return nil
}

func Issue(caCertPath, caKeyPath, certPath, keyPath string, options CertificateOptions) error {
	caCertificate, caKey, err := loadCA(caCertPath, caKeyPath)
	if err != nil {
		return err
	}
	if options.CommonName == "" {
		return fmt.Errorf("certificate common name is required")
	}
	if options.Duration <= 0 {
		options.Duration = 365 * 24 * time.Hour
	}
	if !options.Client && !options.Server {
		return fmt.Errorf("certificate must allow client or server authentication")
	}
	for _, address := range options.IPAddresses {
		if address.To16() == nil {
			return fmt.Errorf("certificate IP address is invalid")
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate certificate key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: options.CommonName, Organization: []string{"Network Service Composer"}}, NotBefore: now.Add(-5 * time.Minute), NotAfter: minTime(now.Add(options.Duration), caCertificate.NotAfter), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, DNSNames: options.DNSNames, IPAddresses: options.IPAddresses, URIs: options.URIs}
	if options.Client {
		template.ExtKeyUsage = append(template.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
	}
	if options.Server {
		template.ExtKeyUsage = append(template.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCertificate, &key.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("issue certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err = writeExclusive(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertificate.Raw})...)
	if err = writeExclusive(certPath, chain, 0o644); err != nil {
		_ = os.Remove(keyPath)
		return err
	}
	return nil
}

func GatewayIdentityURI(poolID, gatewayID string) (*url.URL, error) {
	if err := id.Validate(poolID, id.Pool); err != nil {
		return nil, err
	}
	if err := id.Validate(gatewayID, id.Gateway); err != nil {
		return nil, err
	}
	return url.Parse("spiffe://nsc.local/gateway-pools/" + poolID + "/gateways/" + gatewayID)
}

func loadCA(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read CA certificate: %w", err)
	}
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, nil, fmt.Errorf("CA certificate file is invalid")
	}
	certificate, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA certificate: %w", err)
	}
	now := time.Now().UTC()
	if !certificate.IsCA || !certificate.BasicConstraintsValid || certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, nil, fmt.Errorf("certificate is not a valid signing CA")
	}
	if now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter) {
		return nil, nil, fmt.Errorf("CA certificate is outside its validity period")
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read CA private key: %w", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("CA private key file is invalid")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA private key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, fmt.Errorf("unsupported CA private key type")
	}
	if !key.PublicKey.Equal(certificate.PublicKey) {
		return nil, nil, fmt.Errorf("CA private key does not match the CA certificate")
	}
	return certificate, key, nil
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate certificate serial: %w", err)
	}
	return serial, nil
}

func writeExclusive(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

func minTime(first, second time.Time) time.Time {
	if first.Before(second) {
		return first
	}
	return second
}
