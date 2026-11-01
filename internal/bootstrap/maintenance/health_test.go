package maintenance

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/pki"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
func healthCredential(t *testing.T) (bootstrap.Installation, bootstrap.MaintenanceCredential) {
	t.Helper()
	dir := t.TempDir()
	ca, key := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	if err := pki.InitCA(ca, key, "isolated health CA", time.Hour); err != nil {
		t.Fatal(err)
	}
	uri, _ := controlplane.MaintenanceIdentityURI("installation", "maintenance")
	cert, private := filepath.Join(dir, "maintenance.crt"), filepath.Join(dir, "maintenance.key")
	if err := pki.Issue(ca, key, cert, private, pki.CertificateOptions{CommonName: "maintenance", Client: true, Duration: time.Hour, URIs: []*url.URL{uri}}); err != nil {
		t.Fatal(err)
	}
	certificate, _ := os.ReadFile(cert)
	privateKey, _ := os.ReadFile(private)
	caPEM, _ := os.ReadFile(ca)
	root, err := rootsCertificate(caPEM)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(root.Raw)
	value := bootstrap.Installation{ID: "installation", MaintenanceID: "maintenance", TrustSHA256: hex.EncodeToString(sum[:]), ConsoleURLs: []string{"https://192.0.2.1:8443"}, Nodes: map[string]bootstrap.Identity{"one": {InstanceID: "controller"}}}
	credential := bootstrap.MaintenanceCredential{Certificate: string(certificate), PrivateKey: string(privateKey), CA: string(caPEM), ConsoleCertificate: string(caPEM)}
	return value, credential
}
func TestMaintenanceHealthTrustActualAccessAndIdentityScope(t *testing.T) {
	for _, scenario := range []string{"healthy", "binding-only", "internal-unavailable", "internal-status", "console-unavailable", "console-status", "gateway-unavailable", "gateway-status", "empty-addresses", "invalid-url", "unsupported-url", "no-peer", "untrusted-peer", "gateway-peer", "foreign-peer"} {
		t.Run(scenario, func(t *testing.T) {
			value, credential := healthCredential(t)
			binding := scenario == "binding-only"
			calls := 0
			if scenario == "empty-addresses" {
				value.ConsoleURLs = nil
			}
			if scenario == "invalid-url" {
				value.ConsoleURLs = []string{"https://%bad"}
			}
			if scenario == "unsupported-url" {
				value.ConsoleURLs = []string{"http://127.0.0.1"}
			}
			if scenario == "no-peer" {
				value.Nodes = nil
			}
			factory := func(config *tls.Config) http.RoundTripper {
				return roundTripFunc(func(request *http.Request) (*http.Response, error) {
					calls++
					status := 200
					if request.URL.Path == HealthPath {
						status = 204
						if config == nil || config.MinVersion < tls.VersionTLS12 || config.RootCAs == nil || len(config.Certificates) != 1 {
							t.Fatal("maintenance transport lost mutual TLS")
						}
						peerURI, _ := controlplane.IdentityURI("controller")
						peer := &x509.Certificate{URIs: []*url.URL{peerURI}}
						state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{peer}, VerifiedChains: [][]*x509.Certificate{{peer}}}
						switch scenario {
						case "untrusted-peer":
							state.VerifiedChains = nil
						case "gateway-peer":
							peer.URIs = []*url.URL{{Scheme: "spiffe", Host: "nsc.local", Path: "/gateway"}}
						case "foreign-peer":
							other, _ := controlplane.IdentityURI("foreign")
							peer.URIs = []*url.URL{other}
						}
						if err := config.VerifyConnection(state); err != nil {
							return nil, err
						}
						if binding && request.URL.Query().Get("scope") != "identity" {
							t.Fatal("physical check required healthy Controllers")
						}
						if scenario == "internal-unavailable" {
							return nil, errors.New("unavailable")
						}
						if scenario == "internal-status" {
							status = 503
						}
					} else if request.URL.Path == "/login" {
						if scenario == "console-unavailable" {
							return nil, errors.New("unavailable")
						}
						if scenario == "console-status" {
							status = 302
						}
					} else {
						if scenario == "gateway-unavailable" {
							return nil, errors.New("unavailable")
						}
						if scenario == "gateway-status" {
							status = 503
						}
					}
					if request.Header.Get("Authorization") != "" {
						t.Fatal("maintenance reused an administrator password")
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("fixture")), Request: request}, nil
				})
			}
			check, err := newHealthClient(value, credential, binding, factory)
			if err != nil {
				t.Fatal(err)
			}
			err = check(t.Context())
			if scenario == "healthy" || binding {
				if err != nil {
					t.Fatal(err)
				}
				if binding && calls != 1 {
					t.Fatal("physical identity check probed unrelated health")
				}
			} else if err == nil {
				t.Fatal("incomplete or foreign evidence passed", scenario)
			}
		})
	}
}
func TestMaintenanceHealthCredentialCannotChangeTrustOrIdentity(t *testing.T) {
	for _, scenario := range []string{"key", "identity", "maintenance", "ca", "anchor", "console"} {
		t.Run(scenario, func(t *testing.T) {
			value, credential := healthCredential(t)
			switch scenario {
			case "key":
				credential.PrivateKey = "missing"
			case "identity":
				value.ID = "other"
			case "maintenance":
				value.MaintenanceID = "other"
			case "ca":
				credential.CA = "invalid"
			case "anchor":
				value.TrustSHA256 = "other"
			case "console":
				credential.ConsoleCertificate = "invalid"
			}
			if _, err := HealthClient(value, credential); err == nil {
				t.Fatal("untrusted maintenance identity loaded")
			}
		})
	}
	value, credential := healthCredential(t)
	if _, err := BindingClient(value, credential); err != nil {
		t.Fatal(err)
	}
	if _, err := rootsCertificate([]byte("invalid")); err == nil {
		t.Fatal("invalid CA accepted")
	}
	if _, err := rootsCertificate([]byte("-----BEGIN CERTIFICATE-----\nAA==\n-----END CERTIFICATE-----")); err == nil {
		t.Fatal("corrupt CA accepted")
	}
}
