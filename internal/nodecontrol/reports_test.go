package nodecontrol

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/hoststats"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func reportsFixture(t *testing.T) (Reports, *time.Time, *int64) {
	t.Helper()
	store, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "nodes.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	ring, err := security.NewKeyring("test", map[string]string{"test": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if err != nil {
		t.Fatal(err)
	}
	now, owner := time.Now().UTC(), int64(11)
	s := Reports{Store: store, Keys: ring, Bindings: map[string]Binding{"controller-a": {InstanceID: "controller-a", NodeID: "node-a", EngineID: "engine-a"}, "controller-b": {InstanceID: "controller-b", NodeID: "node-b", EngineID: "engine-b"}}, Now: func(context.Context) (time.Time, error) { return now, nil }, CheckOwner: func(_ context.Context, key string, actual int64) error {
		if key != LockKey("node-a") || actual != owner {
			return ErrStale
		}
		return nil
	}}
	return s, &now, &owner
}

func observationFixture(now time.Time) Observation {
	return Observation{Info: system.Info{ID: "engine-a", Swarm: swarm.Info{NodeID: "node-a"}}, At: now, Complete: true, Containers: []container.Summary{{ID: "container-one", Labels: map[string]string{"fixture": "confidential-label"}}}}
}

func TestNodeReportsRejectOldBootsAndPreserveEncryptedEvidence(t *testing.T) {
	s, now, owner := reportsFixture(t)
	ctx := context.Background()
	first, err := s.Begin(ctx, "controller-a", "node-a", "engine-a", "boot-one", *owner)
	if err != nil || first.Generation != 1 {
		t.Fatal(first, err)
	}
	replay, err := s.Begin(ctx, "controller-a", "node-a", "engine-a", "boot-one", *owner)
	if err != nil || replay != first {
		t.Fatal("begin retry changed generation", replay, err)
	}
	v := Report{NodeID: "node-a", EngineID: "engine-a", Generation: first.Generation, Sequence: 1, Observation: observationFixture(*now)}
	if err = s.Accept(ctx, "controller-a", v); err != nil {
		t.Fatal(err)
	}
	for _, sequence := range []uint64{0, 1} {
		old := v
		old.Sequence = sequence
		if err = s.Accept(ctx, "controller-a", old); !errors.Is(err, ErrStale) {
			t.Fatal("duplicate/out-of-order report accepted", err)
		}
	}
	read, err := s.Read(ctx, "node-a")
	if err != nil || len(read.Observation.Containers) != 1 {
		t.Fatal("reread lost evidence", err)
	}
	d, err := s.Store.GetControlDocument(ctx, reportKey("node-a"))
	if err != nil || bytes.Contains(d.Payload, []byte("confidential-label")) || bytes.Contains(d.Payload, []byte("container-one")) {
		t.Fatal("sensitive observation persisted in plaintext", err)
	}
	*owner = 12
	if _, err = s.Begin(ctx, "controller-a", "node-a", "engine-a", "boot-one", 11); !errors.Is(err, ErrStale) {
		t.Fatal("released session reclaimed generation", err)
	}
	second, err := s.Begin(ctx, "controller-a", "node-a", "engine-a", "boot-two", *owner)
	if err != nil || second.Generation != 2 {
		t.Fatal(second, err)
	}
	if _, err = s.Read(ctx, "node-a"); !errors.Is(err, ErrStale) {
		t.Fatal("old boot remained current", err)
	}
	v.Sequence = 2
	if err = s.Accept(ctx, "controller-a", v); !errors.Is(err, ErrStale) {
		t.Fatal("old boot report accepted", err)
	}
	v.Generation = second.Generation
	if err = s.Accept(ctx, "controller-a", v); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(31 * time.Second)
	if _, err = s.Read(ctx, "node-a"); !errors.Is(err, ErrStale) {
		t.Fatal("offline node shown current", err)
	}
}

func TestNodeReportIdentityAndIncompleteObservations(t *testing.T) {
	for _, mode := range []string{"foreign controller", "foreign health identity", "foreign engine", "foreign observed node", "future", "expired", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			s, now, owner := reportsFixture(t)
			ctx := context.Background()
			session, err := s.Begin(ctx, "controller-a", "node-a", "engine-a", "boot", *owner)
			if err != nil {
				t.Fatal(err)
			}
			v := Report{NodeID: "node-a", EngineID: "engine-a", Generation: session.Generation, Sequence: 1, Observation: observationFixture(*now)}
			peer := "controller-a"
			want := ErrIdentity
			switch mode {
			case "foreign controller":
				peer = "controller-b"
			case "foreign health identity":
				v.Observation.Health = &hoststats.Health{InstanceID: "controller-b", Controller: "healthy"}
			case "foreign engine":
				v.Observation.Info.ID = "engine-b"
			case "foreign observed node":
				v.Observation.Info.Swarm.NodeID = "node-b"
			case "future":
				v.Observation.At = now.Add(6 * time.Second)
				want = ErrStale
			case "expired":
				v.Observation.At = now.Add(-31 * time.Second)
				want = ErrStale
			case "incomplete":
				v.Observation.Complete = false
				v.Observation.Errors = map[string]string{"inventory": "collection_failed"}
				want = nil
			}
			err = s.Accept(ctx, peer, v)
			if !errors.Is(err, want) {
				t.Fatal(mode, err)
			}
			if mode == "incomplete" {
				read, err := s.Read(ctx, "node-a")
				if !errors.Is(err, ErrIncomplete) || len(read.Observation.Containers) != 1 {
					t.Fatal("incomplete evidence treated as healthy/empty", err)
				}
			} else if _, err = s.Read(ctx, "node-a"); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("rejected report altered persisted state", err)
			}
		})
	}
}

func TestNodeReportHTTPAuthenticatesBeforeAcceptingPayload(t *testing.T) {
	s, now, owner := reportsFixture(t)
	h := s.Handler()
	call := func(path, peer string, verified bool, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		if peer != "" {
			uri, _ := url.Parse(peer)
			cert := &x509.Certificate{URIs: []*url.URL{uri}}
			r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
			if verified {
				r.TLS.VerifiedChains = [][]*x509.Certificate{{cert}}
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	begin := BeginRequest{NodeID: "node-a", EngineID: "engine-a", BootID: "boot", Owner: *owner}
	for _, peer := range []string{"", "spiffe://nsc.local/gateways/gateway-a", "spiffe://nsc.local/controllers/unknown", "spiffe://nsc.local/controllers/controller-b"} {
		if w := call("/internal/v1/node-sessions", peer, true, begin); w.Code != 403 {
			t.Fatal("untrusted identity accepted", peer, w.Code)
		}
	}
	peer := "spiffe://nsc.local/controllers/controller-a"
	if w := call("/internal/v1/node-sessions", peer, false, begin); w.Code != 403 {
		t.Fatal("unverified chain accepted", w.Code)
	}
	w := call("/internal/v1/node-sessions", peer, true, begin)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var session Session
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	v := Report{NodeID: "node-a", EngineID: "engine-a", Generation: session.Generation, Sequence: 1, Observation: observationFixture(*now)}
	if w = call("/internal/v1/node-observations", peer, true, v); w.Code != 204 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code)
	}
	if w = call("/internal/v1/node-observations", peer, true, v); w.Code != 409 || strings.Contains(w.Body.String(), "confidential") {
		t.Fatal(w.Code, w.Body.String())
	}
}
