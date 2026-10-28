package certificate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yernsun/network-service-composer/internal/config"
	metricsx "github.com/yernsun/network-service-composer/internal/metrics"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type accountBoundaryStore struct {
	storage.Store
	account               model.ACMEAccount
	readError, writeError error
	writes                int
	serve                 func(http.ResponseWriter, *http.Request) bool
}

func (s *accountBoundaryStore) GetControlDocument(context.Context, string) (storage.ControlDocument, error) {
	return storage.ControlDocument{}, storage.ErrNotFound
}

func (s *accountBoundaryStore) GetACMEAccount(context.Context, string, string) (model.ACMEAccount, error) {
	return s.account, s.readError
}
func (s *accountBoundaryStore) UpsertACMEAccount(_ context.Context, a *model.ACMEAccount) error {
	s.writes++
	if s.writeError != nil {
		return s.writeError
	}
	s.account = *a
	s.readError = nil
	return nil
}

func accountBoundaryFixture(t *testing.T, directoryFailure, registrationFailure bool) (*Worker, *accountBoundaryStore, *atomic.Int64) {
	t.Helper()
	registrations := &atomic.Int64{}
	s := &accountBoundaryStore{readError: storage.ErrNotFound}
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Replay-Nonce", base64.RawURLEncoding.EncodeToString([]byte("isolated-nsc-acme-nonce")))
		w.Header().Set("Content-Type", "application/json")
		if s.serve != nil && s.serve(w, r) {
			return
		}
		switch r.URL.Path {
		case "/directory":
			if directoryFailure {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"type":"urn:ietf:params:acme:error:malformed","detail":"invalid directory"}`)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"newNonce": server.URL + "/nonce", "newAccount": server.URL + "/account", "newOrder": server.URL + "/order", "revokeCert": server.URL + "/revoke", "keyChange": server.URL + "/key-change"})
		case "/nonce":
			w.WriteHeader(200)
		case "/account":
			registrations.Add(1)
			if registrationFailure {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"type":"urn:ietf:params:acme:error:unauthorized","detail":"account rejected"}`)
				return
			}
			w.Header().Set("Location", server.URL+"/account/1")
			w.WriteHeader(201)
			fmt.Fprint(w, `{"status":"valid","contact":["mailto:fixture@example.test"]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	trust := filepath.Join(t.TempDir(), "fixture-ca.pem")
	if err := os.WriteFile(trust, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEGO_CA_CERTIFICATES", trust)
	t.Setenv("LEGO_CA_SYSTEM_CERT_POOL", "false")
	t.Setenv("LEGO_CA_SERVER_NAME", "")
	ring, err := security.NewKeyring("test", map[string]string{"test": base64.StdEncoding.EncodeToString(make([]byte, 32))})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.ACME.AcceptTerms = true
	cfg.ACME.Email = "fixture@example.test"
	cfg.ACME.DirectoryURL = server.URL + "/directory"
	return NewWorker(s, &maintenanceLocker{}, ring, cfg, metricsx.New(), nil), s, registrations
}

func seedAccountBoundary(t *testing.T, w *Worker, s *accountBoundaryStore, registered bool) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	s.account = model.ACMEAccount{ID: "account", Email: w.config.ACME.Email, DirectoryURL: w.config.ACME.DirectoryURL}
	s.account.EncryptedPrivateKey, err = w.keyring.Encrypt(der, []byte(s.account.ID))
	if err != nil {
		t.Fatal(err)
	}
	if registered {
		s.account.Registration = `{"uri":"` + strings.TrimSuffix(w.config.ACME.DirectoryURL, "/directory") + `/account/1","body":{"status":"valid"}}`
	}
	s.readError = nil
}

func TestACMEAccountRegistrationRecoveryAndFailures(t *testing.T) {
	for _, name := range []string{"new", "new directory failure", "new registration failure", "new save failure", "new encryption failure", "stored", "stored unregistered", "stored directory failure", "stored registration failure", "stored save failure", "read failure", "corrupt envelope", "invalid key DER", "invalid registration"} {
		t.Run(name, func(t *testing.T) {
			w, s, requests := accountBoundaryFixture(t, strings.Contains(name, "directory failure"), strings.Contains(name, "registration failure"))
			if !strings.HasPrefix(name, "new") {
				seedAccountBoundary(t, w, s, name != "stored unregistered" && name != "stored registration failure" && name != "stored save failure")
			}
			switch name {
			case "new save failure", "stored save failure":
				s.writeError = errors.New("account persistence unavailable")
			case "new encryption failure":
				w.keyring = &security.Keyring{}
			case "read failure":
				s.readError = errors.New("account read unavailable")
			case "corrupt envelope":
				s.account.EncryptedPrivateKey = []byte("corrupt")
			case "invalid key DER":
				var err error
				s.account.EncryptedPrivateKey, err = w.keyring.Encrypt([]byte("invalid DER"), []byte(s.account.ID))
				if err != nil {
					t.Fatal(err)
				}
			case "invalid registration":
				s.account.Registration = "{"
			}
			client, err := w.clientWithAccountLock(context.Background())
			valid := name == "new" || name == "stored" || name == "stored unregistered"
			if (err == nil) != valid {
				t.Fatalf("account recovery %s: %v", name, err)
			}
			if valid {
				if client == nil || s.account.Registration == "" {
					t.Fatal("account recovery omitted durable registration")
				}
				plain, _, err := w.keyring.Decrypt(s.account.EncryptedPrivateKey, []byte(s.account.ID))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = x509.ParsePKCS8PrivateKey(plain); err != nil {
					t.Fatal("persisted account key unusable", err)
				}
				want := int64(1)
				if name == "stored" {
					want = 0
				}
				if requests.Load() != want {
					t.Fatalf("registration requests=%d want=%d", requests.Load(), want)
				}
			} else if client != nil {
				t.Fatal("failed registration exposed usable client")
			}
		})
	}
}

func TestACMEClientChallengeAndLeasePreconditions(t *testing.T) {
	w, s, _ := accountBoundaryFixture(t, false, false)
	seedAccountBoundary(t, w, s, true)
	for _, name := range []string{"terms", "email", "directory", "lock", "lease", "without challenge", "invalid challenge", "HTTP", "DNS missing profile", "DNS invalid profile", "DNS valid profile"} {
		t.Run(name, func(t *testing.T) {
			cfg := w.config
			locker := &maintenanceLocker{}
			worker := NewWorker(s, locker, w.keyring, cfg, metricsx.New(), nil)
			cert := model.Certificate{ID: "certificate", ACMEChallenge: model.ACMEHTTP01}
			configure := true
			switch name {
			case "terms":
				worker.config.ACME.AcceptTerms = false
			case "email":
				worker.config.ACME.Email = ""
			case "directory":
				worker.config.ACME.DirectoryURL = ""
			case "lock":
				locker.lockError = errors.New("account lock unavailable")
			case "lease":
				locker.checkError = errors.New("account lease lost")
			case "without challenge":
				configure = false
			case "invalid challenge":
				cert.ACMEChallenge = "unknown"
			case "DNS missing profile", "DNS invalid profile", "DNS valid profile":
				cert.ACMEChallenge = model.ACMEDNS01
				cert.DNSProviderRef = "dns"
				if name == "DNS invalid profile" {
					worker.config.ACME.DNSProviders = map[string]config.DNSProviderProfile{"dns": {Provider: "unknown"}}
				}
				if name == "DNS valid profile" {
					worker.config.ACME.DNSProviders = map[string]config.DNSProviderProfile{"dns": {Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: "fixture-token"}}}
				}
			}
			client, err := worker.client(context.Background(), cert, configure)
			valid := name == "HTTP" || name == "without challenge" || name == "DNS valid profile"
			if (err == nil) != valid || (valid && client == nil) {
				t.Fatalf("client boundary %s: %v", name, err)
			}
			wantRelease := 1
			if name == "terms" || name == "email" || name == "directory" || name == "lock" {
				wantRelease = 0
			}
			if locker.releases != wantRelease {
				t.Fatalf("account lock releases=%d want=%d", locker.releases, wantRelease)
			}
		})
	}
}
