package controlplane

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestFollowerReadinessRequiresLiveAuthenticatedScheduler(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	state := electionStore(t)
	peers := peerTLS(t)
	election := &Election{Identity: Identity{ID: "leader"}, Store: state}
	var scheduling atomic.Bool
	scheduling.Store(true)
	forward := Forwarder{Election: election, TLS: peers("leader")}
	server := httptest.NewUnstartedServer(forward.ReadyHandler(func(int64) bool { return scheduling.Load() }))
	server.TLS = forward.TLS
	server.StartTLS()
	defer server.Close()
	held, err := lease.New(nil, storage.SQLite).TryLock(ctx, leaderKey)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release(context.Background())
	leader, err := state.Claim(ctx, Identity{ID: "leader", URL: server.URL}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	election.setActive(&Term{State: leader, Context: ctx, store: state, held: held})
	follower := Forwarder{Election: &Election{Identity: Identity{ID: "follower"}, Store: state}, TLS: peers("follower")}
	if err = follower.ProbeForwarding(ctx, leader); err != nil {
		t.Fatal("valid Leader could not receive forwarded work", err)
	}
	trailing := leader
	trailing.URL += "/"
	if err = follower.ProbeForwarding(ctx, trailing); err != nil {
		t.Fatal("valid origin slash broke readiness", err)
	}
	scheduling.Store(false)
	if err = follower.ProbeForwarding(ctx, leader); err == nil {
		t.Fatal("lease without scheduler was ready")
	}
	scheduling.Store(true)
	wrong := leader
	wrong.Term++
	if err = follower.ProbeForwarding(ctx, wrong); err == nil {
		t.Fatal("stale term became ready")
	}
	foreign := leader
	foreign.ID = "foreign"
	if err = follower.ProbeForwarding(ctx, foreign); err == nil {
		t.Fatal("foreign TLS identity became ready")
	}
	bad := leader
	bad.URL = "http://127.0.0.1:1"
	if err = follower.ProbeForwarding(ctx, bad); err == nil {
		t.Fatal("unsafe origin accepted")
	}
	missing := follower
	missing.TLS = nil
	if err = missing.ProbeForwarding(ctx, leader); err == nil {
		t.Fatal("missing peer transport was ready")
	}
	// The private endpoint is read-only and requires both peer authentication
	// and the exact active scheduling term.
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: follower.TLS}}
	defer client.CloseIdleConnections()
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		req, _ := http.NewRequestWithContext(ctx, method, server.URL+ReadyPath, nil)
		if method == http.MethodPost {
			req.Header.Set("X-NSC-Leader-Term", strconv.FormatInt(leader.Term, 10))
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		want := 503
		if method == http.MethodPost {
			want = 405
		}
		if resp.StatusCode != want {
			t.Fatal(method, resp.StatusCode)
		}
	}
	noPeer := httptest.NewRequest(http.MethodGet, ReadyPath, nil)
	rec := httptest.NewRecorder()
	forward.ReadyHandler(func(int64) bool { return true }).ServeHTTP(rec, noPeer)
	if rec.Code != 403 {
		t.Fatal("anonymous internal readiness accepted", rec.Code)
	}
	// Returning a redirect never makes the Follower ready or causes a probe to
	// a replacement destination.
	redirect := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL+ReadyPath, http.StatusTemporaryRedirect)
	}))
	redirect.TLS = forward.TLS
	redirect.StartTLS()
	defer redirect.Close()
	redirectLeader := leader
	redirectLeader.URL = redirect.URL
	if err = follower.ProbeForwarding(ctx, redirectLeader); err == nil {
		t.Fatal("redirected readiness was trusted")
	}
}

func TestReadinessEndpointRejectsUnverifiedIdentityAndNonpositiveTerm(t *testing.T) {
	state := electionStore(t)
	forward := Forwarder{Election: &Election{Store: state, Identity: Identity{ID: "leader"}}}
	valid, _ := IdentityURI("caller")
	cert := &x509.Certificate{URIs: []*url.URL{valid}}
	verified := &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{cert}}, PeerCertificates: []*x509.Certificate{cert}}
	for _, connection := range []*tls.ConnectionState{{}, {PeerCertificates: []*x509.Certificate{cert}}, {VerifiedChains: [][]*x509.Certificate{{cert}}}, {VerifiedChains: [][]*x509.Certificate{{cert}}, PeerCertificates: []*x509.Certificate{{}}}} {
		r := httptest.NewRequest(http.MethodGet, ReadyPath, nil)
		r.TLS = connection
		w := httptest.NewRecorder()
		forward.ReadyHandler(nil).ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("unverified internal peer read readiness", w.Code)
		}
	}
	for _, term := range []string{"0", "-1", "12"} {
		r := httptest.NewRequest(http.MethodGet, ReadyPath, nil)
		r.TLS = verified
		r.Header.Set("X-NSC-Leader-Term", term)
		w := httptest.NewRecorder()
		forward.ReadyHandler(nil).ServeHTTP(w, r)
		if w.Code != 503 {
			t.Fatal("missing scheduling authority became ready", term, w.Code)
		}
	}
	peer := peerTLS(t)("caller")
	if err := (Forwarder{TLS: peer}).ProbeForwarding(t.Context(), State{ID: "leader", URL: "https://leader.example.test"}); err == nil {
		t.Fatal("missing election allowed forwarding")
	}
	if err := (Forwarder{TLS: peer, Election: forward.Election}).ProbeForwarding(t.Context(), State{ID: "leader", URL: "https://leader.example.test"}); err == nil {
		t.Fatal("self-forwarding probe allowed")
	}
}

func TestReadinessTransportRequiresVerifiedChainsAndPreservesBaseVerification(t *testing.T) {
	state := electionStore(t)
	peers := peerTLS(t)
	var requests atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("X-NSC-Leader-Term", r.Header.Get("X-NSC-Leader-Term"))
		w.WriteHeader(204)
	}))
	server.TLS = peers("leader")
	server.StartTLS()
	defer server.Close()
	leader, err := state.Claim(t.Context(), Identity{ID: "leader", URL: server.URL}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f := Forwarder{Election: &Election{Identity: Identity{ID: "follower"}, Store: state}, TLS: peers("follower")}
	if err = f.ProbeForwarding(nil, leader); err == nil {
		t.Fatal("nil request context reached transport")
	}
	strict := f.TLS.Clone()
	strict.VerifyConnection = func(tls.ConnectionState) error { return ErrNotLeader }
	f.TLS = strict
	if err = f.ProbeForwarding(t.Context(), leader); err == nil || requests.Load() != 0 {
		t.Fatal("base verifier rejection was bypassed", err)
	}
	plain := strict.Clone()
	plain.VerifyConnection = nil
	f.TLS = plain
	if err = f.ProbeForwarding(t.Context(), leader); err != nil {
		t.Fatal("default verified chain and exact peer identity were rejected", err)
	}
	// This deliberately unsafe test client proves the wrapper still rejects a
	// connection without verified chains; it is never a production setting.
	unverified := plain.Clone()
	unverified.InsecureSkipVerify = true
	f.TLS = unverified
	if err = f.ProbeForwarding(t.Context(), leader); err == nil || requests.Load() != 1 {
		t.Fatal("unverified chain established readiness", err)
	}
}
