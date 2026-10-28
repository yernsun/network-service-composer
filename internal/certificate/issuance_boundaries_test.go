package certificate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type issuanceBoundaryStore struct {
	storage.Store
	account          *accountBoundaryStore
	activateError    error
	afterAccountSave func()
}

func (s *issuanceBoundaryStore) GetACMEAccount(ctx context.Context, url, email string) (model.ACMEAccount, error) {
	return s.account.GetACMEAccount(ctx, url, email)
}
func (s *issuanceBoundaryStore) UpsertACMEAccount(ctx context.Context, a *model.ACMEAccount) error {
	err := s.account.UpsertACMEAccount(ctx, a)
	if err == nil && s.afterAccountSave != nil {
		s.afterAccountSave()
	}
	return err
}
func (s *issuanceBoundaryStore) ActivateCertificate(ctx context.Context, v *model.Certificate, rev int64, now time.Time) error {
	if s.activateError != nil {
		return s.activateError
	}
	return s.Store.ActivateCertificate(ctx, v, rev, now)
}

func TestCertificateIssuancePublishesOnlyValidatedMaterial(t *testing.T) {
	for _, name := range []string{"new", "renewal", "without metrics", "order failure", "expired result", "encryption failure", "activation failure"} {
		t.Run(name, func(t *testing.T) {
			w, account, _ := accountBoundaryFixture(t, false, false)
			_, db, original := certificateJobFixture(t, func(c *model.Certificate) {
				if name == "renewal" {
					c.Status = model.CertificateActive
					c.CertificatePEM = "previous valid certificate"
					c.EncryptedPrivateKey = []byte("previous encrypted key")
				}
			})
			domain := model.Domain{ID: "domain", PoolID: original.PoolID, Host: original.PrimaryDomain, TLSMode: model.TLSManaged, CertificateID: original.ID, HTTPEnabled: true, HTTPSEnabled: true, Enabled: true, Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			if err := db.CreateDomain(context.Background(), &domain); err != nil {
				t.Fatal(err)
			}
			store := &issuanceBoundaryStore{Store: db, account: account}
			w.store = store
			if name == "encryption failure" {
				// Lose encryption availability after account registration, on the
				// worker goroutine, before newly issued material can be persisted.
				store.afterAccountSave = func() { w.keyring = &security.Keyring{} }
			}
			if name == "without metrics" {
				w.metrics = nil
			}
			if name == "activation failure" {
				store.activateError = errors.New("activation transaction unavailable")
			}
			issuerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			issuer := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageCertSign}
			issuerDER, err := x509.CreateCertificate(rand.Reader, issuer, issuer, &issuerKey.PublicKey, issuerKey)
			if err != nil {
				t.Fatal(err)
			}
			var issued []byte
			base := strings.TrimSuffix(w.config.ACME.DirectoryURL, "/directory")
			account.serve = func(rw http.ResponseWriter, r *http.Request) bool {
				switch r.URL.Path {
				case "/order":
					if name == "order failure" {
						rw.WriteHeader(400)
						fmt.Fprint(rw, `{"type":"urn:ietf:params:acme:error:rejectedIdentifier","detail":"fixture order rejected"}`)
						return true
					}
					rw.Header().Set("Location", base+"/order/1")
					rw.WriteHeader(201)
					json.NewEncoder(rw).Encode(map[string]any{"status": "ready", "identifiers": []map[string]string{{"type": "dns", "value": original.PrimaryDomain}}, "authorizations": []string{}, "finalize": base + "/finalize"})
					return true
				case "/finalize":
					var jws struct {
						Payload string `json:"payload"`
					}
					if err := json.NewDecoder(r.Body).Decode(&jws); err != nil {
						t.Error(err)
						rw.WriteHeader(400)
						return true
					}
					payload, err := base64.RawURLEncoding.DecodeString(jws.Payload)
					if err != nil {
						t.Error(err)
						rw.WriteHeader(400)
						return true
					}
					var request struct {
						CSR string `json:"csr"`
					}
					if err = json.Unmarshal(payload, &request); err != nil {
						t.Error(err)
						rw.WriteHeader(400)
						return true
					}
					der, err := base64.RawURLEncoding.DecodeString(request.CSR)
					if err != nil {
						t.Error(err)
						rw.WriteHeader(400)
						return true
					}
					csr, err := x509.ParseCertificateRequest(der)
					if err != nil {
						t.Error(err)
						rw.WriteHeader(400)
						return true
					}
					if err = csr.CheckSignature(); err != nil {
						t.Error(err)
						rw.WriteHeader(400)
						return true
					}
					leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: csr.DNSNames, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(12 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
					if name == "expired result" {
						leaf.NotAfter = now.Add(-time.Minute)
					}
					der, err = x509.CreateCertificate(rand.Reader, leaf, issuer, csr.PublicKey, issuerKey)
					if err != nil {
						t.Error(err)
						rw.WriteHeader(400)
						return true
					}
					issued = append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issuerDER})...)
					json.NewEncoder(rw).Encode(map[string]string{"status": "valid", "certificate": base + "/certificate"})
					return true
				case "/certificate":
					rw.Header().Set("Content-Type", "application/pem-certificate-chain")
					rw.Write(issued)
					return true
				}
				return false
			}
			err = w.process(context.Background(), original.ID)
			valid := name == "new" || name == "renewal" || name == "without metrics"
			if (err == nil) != valid {
				t.Fatalf("issuance %s: %v", name, err)
			}
			actual, readErr := db.GetCertificate(context.Background(), original.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if valid {
				if actual.Status != model.CertificateActive || actual.AttemptCount != 0 || actual.NextAttemptAt != nil || actual.Fingerprint == "" || actual.NotAfter == nil {
					t.Fatalf("issued certificate lacks active evidence: %+v", actual)
				}
				plain, _, err := w.keyring.Decrypt(actual.EncryptedPrivateKey, []byte(actual.ID))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = tls.X509KeyPair([]byte(actual.CertificatePEM), plain); err != nil {
					t.Fatal("published certificate and key mismatch", err)
				}
				d, err := db.GetDomain(context.Background(), domain.ID)
				if err != nil || d.Status != model.DomainReady {
					t.Fatal("activation omitted domain readiness", err)
				}
			} else if actual.CertificatePEM != original.CertificatePEM || string(actual.EncryptedPrivateKey) != string(original.EncryptedPrivateKey) {
				t.Fatal("failed issuance replaced previously retained material")
			}
		})
	}
}
