package controller

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/pki"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type readinessAuthority struct {
	controlplane.StateStore
	mu          sync.Mutex
	state       controlplane.State
	unavailable bool
}

func (s *readinessAuthority) Current(context.Context) (controlplane.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable {
		return controlplane.State{}, errors.New("unavailable")
	}
	return s.state, nil
}
func (s *readinessAuthority) Validate(_ context.Context, id string, term int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable || s.state.ID != id || s.state.Term != term {
		return controlplane.ErrNotLeader
	}
	return nil
}

func TestFollowerControllerReadinessNeedsForwardingAndCurrentAuthority(t *testing.T) {
	cfg := controllerConfigFixture(t)
	cfg.Controller.InstanceID = "follower"
	c, err := New(t.Context(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.store.Close()
	defer c.processLock.Close()
	dir := t.TempDir()
	ca, caKey := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	if err = pki.InitCA(ca, caKey, "readiness fixture", time.Hour); err != nil {
		t.Fatal(err)
	}
	peer := func(id string) *tls.Config {
		identity, _ := controlplane.IdentityURI(id)
		cert, key := filepath.Join(dir, id+".crt"), filepath.Join(dir, id+".key")
		if err := pki.Issue(ca, caKey, cert, key, pki.CertificateOptions{CommonName: id, URIs: []*url.URL{identity}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, Client: true, Server: true, Duration: time.Hour}); err != nil {
			t.Fatal(err)
		}
		v, err := controlplane.InternalTLS(ca, cert, key, id)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	authority := &readinessAuthority{state: controlplane.State{ID: "leader", Term: 9}}
	mode := "ready"
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		authority.mu.Lock()
		term := authority.state.Term
		responseMode := mode
		if responseMode == "replaced" {
			authority.state.Term++
		}
		authority.mu.Unlock()
		if responseMode == "unavailable" {
			w.WriteHeader(503)
			return
		}
		if responseMode == "wrong-term" {
			term++
		}
		w.Header().Set("X-NSC-Leader-Term", strconv.FormatInt(term, 10))
		w.WriteHeader(204)
	}))
	server.TLS = peer("leader")
	server.StartTLS()
	defer server.Close()
	authority.state.URL = server.URL
	c.election.Store = authority
	c.internalTLS = peer("follower")
	c.ready.Store(true)
	if got := c.haController(t.Context()); got.State != "READY" || got.Reason != "follower_forwarding" || got.ObservedAt == nil {
		t.Fatal(got)
	}
	for _, failed := range []string{"unavailable", "wrong-term", "replaced"} {
		authority.mu.Lock()
		mode = failed
		authority.mu.Unlock()
		if c.isReady() {
			t.Fatal("invalid remote scheduler established readiness", failed)
		}
	}
	authority.mu.Lock()
	mode = "ready"
	authority.unavailable = true
	authority.mu.Unlock()
	if c.isReady() {
		t.Fatal("lost authority established readiness")
	}
	authority.mu.Lock()
	authority.unavailable = false
	authority.state.ID = cfg.Controller.InstanceID
	authority.mu.Unlock()
	if c.isReady() {
		t.Fatal("self-named lease without workers established readiness")
	}
	if err = c.store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: storage.ControlDocument{ID: storage.RecoveryBarrierID, Kind: "recovery-barrier", Payload: []byte("{}")}}}); err != nil {
		t.Fatal(err)
	}
	if got := c.haController(t.Context()); got.State == "READY" || got.Reason != "recovery_or_storage_unavailable" {
		t.Fatal(got)
	}
	c.election = nil
	if c.isReady() {
		t.Fatal("missing election established readiness")
	}
}
