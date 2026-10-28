package controlplane

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/pki"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func peerTLS(t *testing.T) func(string) *tls.Config {
	t.Helper()
	dir := t.TempDir()
	ca, key := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	if err := pki.InitCA(ca, key, "isolated internal CA", time.Hour); err != nil {
		t.Fatal(err)
	}
	return func(id string) *tls.Config {
		t.Helper()
		uri, err := IdentityURI(id)
		if err != nil {
			t.Fatal(err)
		}
		certPath, keyPath := filepath.Join(dir, id+".pem"), filepath.Join(dir, id+".key")
		if err := pki.Issue(ca, key, certPath, keyPath, pki.CertificateOptions{CommonName: id, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, URIs: []*url.URL{uri}, Server: true, Client: true, Duration: time.Hour}); err != nil {
			t.Fatal(err)
		}
		cfg, err := InternalTLS(ca, certPath, keyPath, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := InternalTLS(ca, certPath, keyPath, "different"); err == nil {
			t.Fatal("foreign local identity accepted")
		}
		return cfg
	}
}

func TestForwardingPreservesMutationAndRejectsStaleTerm(t *testing.T) {
	ctx := context.Background()
	state := electionStore(t)
	peer := peerTLS(t)
	leaderTLS, followerTLS := peer("leader"), peer("follower")
	e := &Election{Identity: Identity{ID: "leader"}, Store: state}
	var writes atomic.Int32
	internal := Forwarder{Election: e, TLS: leaderTLS}
	server := httptest.NewUnstartedServer(internal.Internal(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != "payload" || r.Host != "public.example:7080" || r.URL.RequestURI() != "/api/v1/action?test=1" || r.Header.Get("Idempotency-Key") != "request-one" || r.Header.Get("Cookie") != "session=fixture" || r.Header.Get("Origin") != "http://public.example:7080" {
			t.Error("forwarding changed request semantics")
		}
		writes.Add(1)
		w.WriteHeader(http.StatusAccepted)
	})))
	server.TLS = leaderTLS
	server.StartTLS()
	defer server.Close()
	held, err := lease.New(nil, storage.SQLite).TryLock(ctx, leaderKey)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release(ctx)
	v, err := state.Claim(ctx, Identity{ID: "leader", URL: server.URL}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	termCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.setActive(&Term{State: v, Context: termCtx, store: state, held: held})
	follower := Forwarder{Election: &Election{Identity: Identity{ID: "follower"}, Store: state}, TLS: followerTLS}
	public := follower.Public(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("follower executed a mutation") }))
	request := httptest.NewRequest(http.MethodPost, "http://public.example:7080/api/v1/action?test=1", strings.NewReader("payload"))
	request.Header.Set("Cookie", "session=fixture")
	request.Header.Set("Origin", "http://public.example:7080")
	request.Header.Set("Idempotency-Key", "request-one")
	reply := httptest.NewRecorder()
	public.ServeHTTP(reply, request)
	if reply.Code != http.StatusAccepted || writes.Load() != 1 {
		t.Fatalf("mutation not forwarded exactly once: %d, %d", reply.Code, writes.Load())
	}
	// A delayed request reaching the same process must not use an old term.
	if _, err := state.Claim(ctx, Identity{ID: "successor", URL: server.URL}, time.Minute); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: followerTLS}}
	defer client.CloseIdleConnections()
	stale, err := http.NewRequest(http.MethodPost, server.URL+"/forward/api/v1/action?test=1", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	stale.Header.Set("X-NSC-Original-Host", "public.example:7080")
	stale.Header.Set("X-NSC-Leader-Term", strconv.FormatInt(v.Term, 10))
	response, err := client.Do(stale)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable || writes.Load() != 1 {
		t.Fatal("stale leader executed mutation")
	}
	// The persisted leader name must match the TLS identity, even with the same CA.
	reply = httptest.NewRecorder()
	public.ServeHTTP(reply, request.Clone(ctx))
	if reply.Code != http.StatusServiceUnavailable || writes.Load() != 1 {
		t.Fatal("wrong instance certificate accepted")
	}
}

func TestInternalTransportRejectsGatewayIdentity(t *testing.T) {
	config := peerTLS(t)("controller")
	uri, _ := url.Parse("spiffe://nsc.local/gateway-pools/pool/gateways/gateway")
	cert := &x509.Certificate{URIs: []*url.URL{uri}}
	state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert}}}
	if err := config.VerifyConnection(state); err == nil {
		t.Fatal("gateway gained controller authority")
	}
	if err := config.VerifyConnection(tls.ConnectionState{}); err == nil {
		t.Fatal("unverified connection accepted")
	}
}
