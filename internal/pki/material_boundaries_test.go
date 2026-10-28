package pki

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"github.com/yernsun/network-service-composer/internal/id"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type serialFailureReader struct{}

func (serialFailureReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func TestCAMaterialValidationPreservesOutputOnFailure(t *testing.T) {
	for _, stage := range []string{"missing certificate", "bad PEM", "wrong PEM type", "bad DER", "not CA", "no signing usage", "future", "expired", "missing key", "bad key PEM", "bad key DER", "unsupported key", "mismatched key"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			certPath, keyPath := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
			key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if e != nil {
				t.Fatal(e)
			}
			now := time.Now()
			template := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
			switch stage {
			case "not CA":
				template.IsCA = false
			case "no signing usage":
				template.KeyUsage = x509.KeyUsageDigitalSignature
			case "future":
				template.NotBefore = now.Add(time.Hour)
				template.NotAfter = now.Add(2 * time.Hour)
			case "expired":
				template.NotAfter = now.Add(-time.Minute)
			}
			der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
			if e != nil {
				t.Fatal(e)
			}
			cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
			rawKey, e := x509.MarshalPKCS8PrivateKey(key)
			if e != nil {
				t.Fatal(e)
			}
			switch stage {
			case "bad PEM":
				cert = []byte("invalid")
			case "wrong PEM type":
				cert = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
			case "bad DER":
				cert = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid")})
			case "bad key DER":
				rawKey = []byte("invalid")
			case "unsupported key":
				_, ed, e := ed25519.GenerateKey(rand.Reader)
				if e != nil {
					t.Fatal(e)
				}
				rawKey, e = x509.MarshalPKCS8PrivateKey(ed)
				if e != nil {
					t.Fatal(e)
				}
			case "mismatched key":
				other, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if e != nil {
					t.Fatal(e)
				}
				rawKey, e = x509.MarshalPKCS8PrivateKey(other)
				if e != nil {
					t.Fatal(e)
				}
			}
			keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rawKey})
			if stage == "bad key PEM" {
				keyPEM = []byte("invalid")
			}
			if stage != "missing certificate" {
				if e = os.WriteFile(certPath, cert, 0600); e != nil {
					t.Fatal(e)
				}
			}
			if stage != "missing key" {
				if e = os.WriteFile(keyPath, keyPEM, 0600); e != nil {
					t.Fatal(e)
				}
			}
			outputCert, outputKey := filepath.Join(dir, "leaf.crt"), filepath.Join(dir, "leaf.key")
			e = Issue(certPath, keyPath, outputCert, outputKey, CertificateOptions{CommonName: "leaf", Client: true})
			if e == nil {
				t.Fatal("invalid CA accepted")
			}
			for _, path := range []string{outputCert, outputKey} {
				if _, e = os.Stat(path); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("failed CA validation produced an output", path, e)
				}
			}
		})
	}
}
func TestPKIExclusiveWritesAndCertificateOptions(t *testing.T) {
	for _, stage := range []string{"defaults", "missing name", "missing usage", "invalid IP", "occupied key", "occupied certificate", "parent is file"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			caCert, caKey := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
			if e := InitCA(caCert, caKey, "", 0); e != nil {
				t.Fatal(e)
			}
			cert, key := filepath.Join(dir, "leaf.crt"), filepath.Join(dir, "leaf.key")
			options := CertificateOptions{CommonName: "leaf", Client: true, Server: true, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}
			switch stage {
			case "missing name":
				options.CommonName = ""
			case "missing usage":
				options.Client = false
				options.Server = false
			case "invalid IP":
				options.IPAddresses = []net.IP{{1, 2, 3}}
			case "occupied key":
				if e := os.WriteFile(key, []byte("keep"), 0600); e != nil {
					t.Fatal(e)
				}
			case "occupied certificate":
				if e := os.WriteFile(cert, []byte("keep"), 0600); e != nil {
					t.Fatal(e)
				}
			case "parent is file":
				if e := os.WriteFile(key, []byte("keep"), 0600); e != nil {
					t.Fatal(e)
				}
				key = filepath.Join(key, "nested.key")
			}
			e := Issue(caCert, caKey, cert, key, options)
			if stage == "defaults" {
				if e != nil {
					t.Fatal(e)
				}
				data, e := os.ReadFile(cert)
				if e != nil {
					t.Fatal(e)
				}
				block, rest := pem.Decode(data)
				leaf, e := x509.ParseCertificate(block.Bytes)
				if e != nil || len(rest) == 0 || len(leaf.ExtKeyUsage) != 2 || time.Until(leaf.NotAfter) < 364*24*time.Hour {
					t.Fatal("default duration, chain or usage lost", e)
				}
				return
			}
			if e == nil {
				t.Fatal("invalid issuance accepted")
			}
			if stage == "occupied certificate" {
				data, e := os.ReadFile(cert)
				if e != nil || string(data) != "keep" {
					t.Fatal("existing certificate overwritten")
				}
				if _, e = os.Stat(key); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("failed issuance left orphan private key")
				}
			}
			if stage == "occupied key" {
				data, e := os.ReadFile(key)
				if e != nil || string(data) != "keep" {
					t.Fatal("existing key overwritten")
				}
			}
		})
	}
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	if e := os.WriteFile(cert, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := InitCA(cert, key, "CA", time.Hour); e == nil {
		t.Fatal("CA overwrote existing certificate")
	}
	if _, e := os.Stat(key); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("failed CA creation left private key")
	}
	if e := os.WriteFile(key, []byte("keep key"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := InitCA(filepath.Join(dir, "other.crt"), key, "CA", time.Hour); e == nil {
		t.Fatal("CA overwrote existing key")
	}
	for _, values := range [][2]string{{"invalid", id.New(id.Gateway)}, {id.New(id.Pool), "invalid"}} {
		if _, e := GatewayIdentityURI(values[0], values[1]); e == nil {
			t.Fatal("invalid gateway identity accepted")
		}
	}
	original := rand.Reader
	var e error
	func() {
		defer func() { rand.Reader = original }()
		rand.Reader = serialFailureReader{}
		_, e = randomSerial()
	}()
	if e == nil || !strings.Contains(e.Error(), "serial") {
		t.Fatal("serial entropy failure hidden", e)
	}
	now := time.Now()
	if !minTime(now.Add(time.Hour), now).Equal(now) {
		t.Fatal("leaf expiry exceeds CA expiry")
	}
}
