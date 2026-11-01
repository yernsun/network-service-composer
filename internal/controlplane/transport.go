package controlplane

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const controllerURIPrefix = "spiffe://nsc.local/controllers/"

func IdentityURI(id string) (*url.URL, error) {
	if id == "" || len(id) > 128 {
		return nil, errors.New("invalid controller instance ID")
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return nil, errors.New("invalid controller instance ID")
		}
	}
	return url.Parse(controllerURIPrefix + id)
}

func CertificateIdentity(cert *x509.Certificate) (string, error) {
	if cert == nil || len(cert.URIs) != 1 {
		return "", errors.New("controller certificate requires one instance URI")
	}
	id := strings.TrimPrefix(cert.URIs[0].String(), controllerURIPrefix)
	uri, err := IdentityURI(id)
	if err != nil || uri.String() != cert.URIs[0].String() {
		return "", errors.New("invalid controller certificate identity")
	}
	return id, nil
}

// InternalTLS deliberately rejects gateway certificates issued by the same CA.
func InternalTLS(caPath, certPath, keyPath, instance string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	id, err := CertificateIdentity(leaf)
	if err != nil || id != instance {
		return nil, errors.New("internal certificate does not identify this Controller")
	}
	raw, err := os.ReadFile(caPath)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		return nil, errors.New("invalid internal CA")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, RootCAs: roots, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, VerifyConnection: func(state tls.ConnectionState) error {
		if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
			return errors.New("unverified controller connection")
		}
		if _, err := CertificateIdentity(state.PeerCertificates[0]); err == nil {
			return nil
		}
		_, _, err := MaintenanceCertificateIdentity(state.PeerCertificates[0])
		return err
	}}, nil
}

type Forwarder struct {
	Election    *Election
	TLS         *tls.Config
	Unavailable http.Handler
}

func (f Forwarder) fail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "1")
	if f.Unavailable != nil {
		f.Unavailable.ServeHTTP(w, r)
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
}

func (f Forwarder) asLeader(w http.ResponseWriter, r *http.Request, next http.Handler, expected int64) {
	t := f.Election.Active()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	err := t.Check(ctx)
	cancel()
	if err != nil || expected != 0 && t.State.Term != expected {
		f.fail(w, r)
		return
	}
	ctx, cancel = context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(t.Context, cancel)
	defer stop()
	if ctx.Err() != nil {
		f.fail(w, r)
		return
	}
	next.ServeHTTP(w, r.WithContext(ctx))
}

func (f Forwarder) Public(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if f.Election.Active() != nil {
			f.asLeader(w, r, next, 0)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		leader, err := f.Election.Store.Current(ctx)
		if err == nil {
			err = f.Election.Store.Validate(ctx, leader.ID, leader.Term)
		}
		cancel()
		if err != nil || leader.ID == f.Election.Identity.ID || f.TLS == nil {
			f.fail(w, r)
			return
		}
		target, err := url.Parse(leader.URL)
		if err != nil || target.Scheme != "https" || target.Host == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || target.Path != "" && target.Path != "/" {
			f.fail(w, r)
			return
		}
		target.Path = "/forward"
		tlsConfig := f.TLS.Clone()
		verify := tlsConfig.VerifyConnection
		tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
			if verify != nil {
				if err := verify(state); err != nil {
					return err
				}
			}
			if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
				return errors.New("unverified forwarding target")
			}
			id, err := CertificateIdentity(state.PeerCertificates[0])
			if err != nil || id != leader.ID {
				return errors.New("forwarding target is not the elected Controller")
			}
			return nil
		}
		// A fresh connection and no GetBody prevent transport-level mutation replay.
		transport := &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig, DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 30 * time.Second}
		defer transport.CloseIdleConnections()
		proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(target)
			p.Out.GetBody = nil
			p.Out.Header.Del("Forwarded")
			p.Out.Header.Del("X-Forwarded-For")
			p.Out.Header.Del("X-Forwarded-Host")
			p.Out.Header.Del("X-Forwarded-Proto")
			p.Out.Header.Set("X-NSC-Original-Host", p.In.Host)
			p.Out.Header.Set("X-NSC-Leader-Term", strconv.FormatInt(leader.Term, 10))
		}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, _ error) { f.fail(w, r) }}
		proxy.ServeHTTP(w, r)
	})
}

func (f Forwarder) Internal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if _, err := CertificateIdentity(r.TLS.PeerCertificates[0]); err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/forward/") {
			http.NotFound(w, r)
			return
		}
		t, err := strconv.ParseInt(r.Header.Get("X-NSC-Leader-Term"), 10, 64)
		if err != nil || t <= 0 {
			f.fail(w, r)
			return
		}
		r.Host = r.Header.Get("X-NSC-Original-Host")
		if r.Host == "" || strings.ContainsAny(r.Host, "/\\\r\n") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		r.Header.Del("X-NSC-Original-Host")
		r.Header.Del("X-NSC-Leader-Term")
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/forward")
		if r.URL.RawPath != "" {
			r.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, "/forward")
		}
		r.RequestURI = r.URL.RequestURI()
		f.asLeader(w, r, next, t)
	})
}

func ValidateInternalURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return fmt.Errorf("controller internal URL must be an HTTPS origin")
	}
	return nil
}
