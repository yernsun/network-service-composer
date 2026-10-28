package controlplane

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

const ReadyPath = "/internal/v1/ready"

// ReadyHandler proves that this exact term has live scheduling workers. It
// bypasses public readiness and never forwards a probe back to another peer.
func (f Forwarder) ReadyHandler(scheduling func(int64) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
			w.WriteHeader(403)
			return
		}
		if _, err := CertificateIdentity(r.TLS.PeerCertificates[0]); err != nil {
			w.WriteHeader(403)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			w.WriteHeader(405)
			return
		}
		term, err := strconv.ParseInt(r.Header.Get("X-NSC-Leader-Term"), 10, 64)
		active := f.Election.Active()
		if err != nil || term <= 0 || active == nil || active.State.Term != term || scheduling == nil || !scheduling(term) || active.Check(r.Context()) != nil {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("X-NSC-Leader-Term", strconv.FormatInt(term, 10))
		w.WriteHeader(http.StatusNoContent)
	})
}

// ProbeForwarding checks only the elected, provisioned mTLS identity. A database
// lease by itself cannot establish that a Follower can reach its write path.
func (f Forwarder) ProbeForwarding(ctx context.Context, leader State) error {
	if f.TLS == nil || f.Election == nil || leader.ID == f.Election.Identity.ID || ValidateInternalURL(leader.URL) != nil {
		return ErrNotLeader
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(leader.URL, "/")+ReadyPath, nil)
	if err != nil {
		return ErrNotLeader
	}
	request.Header.Set("X-NSC-Leader-Term", strconv.FormatInt(leader.Term, 10))
	tlsConfig := f.TLS.Clone()
	verify := tlsConfig.VerifyConnection
	tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
		if verify != nil {
			if err := verify(state); err != nil {
				return err
			}
		}
		if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
			return ErrNotLeader
		}
		id, err := CertificateIdentity(state.PeerCertificates[0])
		if err != nil || id != leader.ID {
			return ErrNotLeader
		}
		return nil
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("readiness redirects are forbidden") }}
	response, err := client.Do(request)
	if err != nil {
		return ErrNotLeader
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent || response.Header.Get("X-NSC-Leader-Term") != strconv.FormatInt(leader.Term, 10) {
		return ErrNotLeader
	}
	return f.Election.Store.Validate(ctx, leader.ID, leader.Term)
}
