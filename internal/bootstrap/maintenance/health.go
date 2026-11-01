package maintenance

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/controlplane"
)

const HealthPath = "/internal/v1/installation/health"

// HealthClient verifies the exact registered Controller peer and independent
// console certificate. No original administrator password is used on day two.
func HealthClient(value bootstrap.Installation, credential bootstrap.MaintenanceCredential) (func(context.Context) error, error) {
	return healthClient(value, credential, false)
}
func BindingClient(value bootstrap.Installation, credential bootstrap.MaintenanceCredential) (func(context.Context) error, error) {
	return healthClient(value, credential, true)
}
func healthClient(value bootstrap.Installation, credential bootstrap.MaintenanceCredential, bindingOnly bool) (func(context.Context) error, error) {
	return newHealthClient(value, credential, bindingOnly, func(cfg *tls.Config) http.RoundTripper { return &http.Transport{Proxy: nil, TLSClientConfig: cfg} })
}
func newHealthClient(value bootstrap.Installation, credential bootstrap.MaintenanceCredential, bindingOnly bool, transport func(*tls.Config) http.RoundTripper) (func(context.Context) error, error) {
	pair, err := tls.X509KeyPair([]byte(credential.Certificate), []byte(credential.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("maintenance identity cannot be loaded")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	install, maintenance, err := controlplane.MaintenanceCertificateIdentity(leaf)
	if err != nil || install != value.ID || maintenance != value.MaintenanceID {
		return nil, fmt.Errorf("maintenance credential identity differs")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(credential.CA)) {
		return nil, fmt.Errorf("maintenance CA is invalid")
	}
	parsed, err := rootsCertificate([]byte(credential.CA))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(parsed.Raw)
	if hex.EncodeToString(sum[:]) != value.TrustSHA256 {
		return nil, fmt.Errorf("maintenance trust anchor differs")
	}
	consoleRoots := x509.NewCertPool()
	if !consoleRoots.AppendCertsFromPEM([]byte(credential.ConsoleCertificate)) {
		return nil, fmt.Errorf("console certificate is invalid")
	}
	return func(ctx context.Context) error {
		for _, address := range value.ConsoleURLs {
			endpoint, err := url.Parse(address)
			if err != nil {
				return err
			}
			if endpoint.Scheme != "https" || net.ParseIP(endpoint.Hostname()) == nil || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
				return fmt.Errorf("console endpoint is not a registered IP HTTPS listener")
			}
			expected := ""
			for _, identity := range value.Nodes {
				if identity.InstanceID != "" {
					expected = "registered"
					break
				}
			}
			if expected == "" {
				return fmt.Errorf("no registered Controller peer")
			}
			internal := *endpoint
			internal.Host = net.JoinHostPort(endpoint.Hostname(), "9444")
			internal.Path = HealthPath
			if bindingOnly {
				internal.RawQuery = "scope=identity"
			}
			client := &http.Client{Transport: transport(&tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}, RootCAs: roots, VerifyConnection: controllerVerifier(value.Nodes)}), Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			request, _ := http.NewRequestWithContext(ctx, "GET", internal.String(), nil)
			response, err := client.Do(request)
			if err != nil {
				client.CloseIdleConnections()
				return fmt.Errorf("typed installation health could not be verified")
			}
			response.Body.Close()
			client.CloseIdleConnections()
			if response.StatusCode != http.StatusNoContent {
				return fmt.Errorf("installation has no fresh complete health evidence")
			}
			if bindingOnly {
				continue
			}
			console := &http.Client{Transport: transport(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: consoleRoots}), Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			request, _ = http.NewRequestWithContext(ctx, "GET", strings.TrimRight(address, "/")+"/login", nil)
			response, err = console.Do(request)
			if err != nil {
				console.CloseIdleConnections()
				return fmt.Errorf("console HTTPS access could not be verified")
			}
			io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			console.CloseIdleConnections()
			if response.StatusCode != http.StatusOK {
				return fmt.Errorf("console access is unavailable")
			}
			ingress := &http.Client{Transport: transport(nil), Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			request, _ = http.NewRequestWithContext(ctx, "GET", "http://"+net.JoinHostPort(endpoint.Hostname(), "80")+"/", nil)
			response, err = ingress.Do(request)
			if err != nil {
				ingress.CloseIdleConnections()
				return fmt.Errorf("Gateway HTTP access could not be verified")
			}
			response.Body.Close()
			ingress.CloseIdleConnections()
			if response.StatusCode < 200 || response.StatusCode >= 500 {
				return fmt.Errorf("Gateway access is unavailable")
			}
		}
		if len(value.ConsoleURLs) == 0 {
			return fmt.Errorf("installation has no console address")
		}
		return nil
	}, nil
}

func rootsCertificate(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("maintenance CA is invalid")
	}
	return x509.ParseCertificate(block.Bytes)
}

func controllerVerifier(nodes map[string]bootstrap.Identity) func(tls.ConnectionState) error {
	return func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 || len(state.VerifiedChains) == 0 {
			return fmt.Errorf("Controller peer trust is unverified")
		}
		peer, err := controlplane.CertificateIdentity(state.PeerCertificates[0])
		if err != nil {
			return err
		}
		for _, identity := range nodes {
			if identity.InstanceID == peer {
				return nil
			}
		}
		return fmt.Errorf("Controller identity is not registered")
	}
}
