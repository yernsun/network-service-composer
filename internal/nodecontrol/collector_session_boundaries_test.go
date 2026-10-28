package nodecontrol

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/hoststats"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/pki"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/runtime/engine/enginetest"
)

type collectingSessionLease struct {
	checks, lostAt, releases int
	sessionErr               error
}

func (l *collectingSessionLease) Check(context.Context) error {
	l.checks++
	if l.lostAt > 0 && l.checks >= l.lostAt {
		return lease.ErrLost
	}
	return nil
}
func (l *collectingSessionLease) Release(context.Context) error            { l.releases++; return nil }
func (l *collectingSessionLease) SessionID(context.Context) (int64, error) { return 11, l.sessionErr }

type collectingLocker struct {
	held  *collectingSessionLease
	busy  bool
	calls int
}

func (l *collectingLocker) TryLock(_ context.Context, key string) (lease.Lease, error) {
	l.calls++
	if key != LockKey("node-a") {
		return nil, ErrIdentity
	}
	if l.busy {
		return nil, lease.ErrBusy
	}
	return l.held, nil
}

func collectionTLS(t *testing.T) *tls.Config {
	t.Helper()
	dir := t.TempDir()
	ca, key := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	if err := pki.InitCA(ca, key, "isolated collector session CA", time.Hour); err != nil {
		t.Fatal(err)
	}
	uri, err := controlplane.IdentityURI("controller-a")
	if err != nil {
		t.Fatal(err)
	}
	cert, private := filepath.Join(dir, "controller.pem"), filepath.Join(dir, "controller.key")
	if err = pki.Issue(ca, key, cert, private, pki.CertificateOptions{CommonName: "controller-a", IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, URIs: []*url.URL{uri}, Server: true, Client: true, Duration: time.Hour}); err != nil {
		t.Fatal(err)
	}
	cfg, err := controlplane.InternalTLS(ca, cert, private, "controller-a")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestCollectorSessionPreservesLastInventoryButMarksFailedAttemptUnavailable(t *testing.T) {
	cfg := collectionTLS(t)
	var mu sync.Mutex
	var received []Report
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/v1/node-sessions" {
			var begin BeginRequest
			if err := json.NewDecoder(r.Body).Decode(&begin); err != nil || begin.Owner != 11 || begin.NodeID != "node-a" || begin.BootID == "" {
				t.Error("invalid node session begin", err)
			}
			_ = json.NewEncoder(w).Encode(Session{Generation: 3, Owner: 11})
			return
		}
		var report Report
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			t.Error(err)
		}
		mu.Lock()
		received = append(received, report)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewUnstartedServer(handler)
	server.TLS = cfg
	server.StartTLS()
	defer server.Close()
	_, base := enginetest.Fixture()
	attempt := 0
	held := &collectingSessionLease{lostAt: 4}
	collector := Collector{Binding: Binding{InstanceID: "controller-a", NodeID: "node-a", EngineID: "engine-local", URL: server.URL}, TLS: cfg, Interval: time.Millisecond, Health: func(context.Context) hoststats.Health { return hoststats.Health{Controller: "healthy"} }, Open: func() (engine.MonitoringClient, error) {
		attempt++
		if attempt == 2 {
			return nil, errors.New("engine temporarily unavailable")
		}
		return base, nil
	}}
	if err := collector.collectSession(t.Context(), held); !errors.Is(err, lease.ErrLost) {
		t.Fatal("collector did not stop after lease loss", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 {
		t.Fatal("missing report attempts", len(received))
	}
	for i, r := range received {
		if r.Generation != 3 || r.Sequence != uint64(i+1) || r.Observation.At.IsZero() || r.Observation.Health.InstanceID != "controller-a" {
			t.Fatal("missing monotonic reporting identity", i)
		}
		if len(r.Observation.Containers) != 3 {
			t.Fatal("partial attempt lost retained inventory", i)
		}
	}
	if received[1].Observation.Complete || received[1].Observation.Errors["inventory"] != "collection_failed" || received[1].Observation.Health.Collector != "degraded" {
		t.Fatal("failed attempt masqueraded as current")
	}
	if !received[0].Observation.Complete || !received[2].Observation.Complete || received[2].Observation.Health.Collector != "healthy" {
		t.Fatal("collection did not recover")
	}
}

func TestCollectorRunReleasesSessionAndStopsOnEngineIdentityChange(t *testing.T) {
	cfg := collectionTLS(t)
	var posts atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		_ = json.NewEncoder(w).Encode(Session{Generation: 1, Owner: 11})
	}))
	server.TLS = cfg
	server.StartTLS()
	defer server.Close()
	_, base := enginetest.Fixture()
	base.System.ID = "foreign-engine"
	held := &collectingSessionLease{}
	locker := &collectingLocker{held: held}
	collector := Collector{Binding: Binding{InstanceID: "controller-a", NodeID: "node-a", EngineID: "engine-local", URL: server.URL}, Locker: locker, TLS: cfg, Interval: time.Millisecond, Open: func() (engine.MonitoringClient, error) { return base, nil }}
	if err := collector.Run(t.Context()); !errors.Is(err, ErrIdentity) || held.releases != 1 || locker.calls != 1 || posts.Load() != 1 {
		t.Fatal("identity changed without safe session exit", err, held.releases, locker.calls, posts.Load())
	}
}

func TestCollectorAdmissionRequiresSessionOwnershipAndValidTransport(t *testing.T) {
	cfg := collectionTLS(t)
	_, base := enginetest.Fixture()
	baseCollector := Collector{Binding: Binding{InstanceID: "controller-a", NodeID: "node-a", EngineID: "engine-local", URL: "https://controller.example"}, TLS: cfg, Locker: &collectingLocker{held: &collectingSessionLease{}}, Open: func() (engine.MonitoringClient, error) { return base, nil }, Interval: time.Millisecond}
	for _, mode := range []string{"negative interval", "missing locker", "missing tls", "missing opener", "http endpoint"} {
		t.Run(mode, func(t *testing.T) {
			c := baseCollector
			switch mode {
			case "negative interval":
				c.Interval = -time.Second
			case "missing locker":
				c.Locker = nil
			case "missing tls":
				c.TLS = nil
			case "missing opener":
				c.Open = nil
			case "http endpoint":
				c.Binding.URL = "http://controller.example"
			}
			if err := c.Run(t.Context()); err == nil {
				t.Fatal("invalid collector configuration accepted")
			}
		})
	}
	if err := baseCollector.collectSession(t.Context(), &collectingSessionLease{sessionErr: lease.ErrLost}); !errors.Is(err, lease.ErrLost) {
		t.Fatal("lost database session began reporting", err)
	}
	ordinary := lease.New(nil, "sqlite")
	held, err := ordinary.TryLock(t.Context(), "collector-local")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release(t.Context())
	if err := baseCollector.collectSession(t.Context(), held); err == nil {
		t.Fatal("local lease impersonated shared database session")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	baseCollector.Interval = 0
	if err := baseCollector.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled collector did not stop", err)
	}
	busy := &collectingLocker{busy: true}
	baseCollector.Locker = busy
	baseCollector.Interval = time.Millisecond
	ctx, cancel = context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := baseCollector.Run(ctx); !errors.Is(err, context.DeadlineExceeded) || busy.calls == 0 {
		t.Fatal("busy ownership neither retried nor stopped", err)
	}
}
