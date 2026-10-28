package discovery

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/lease"
	metricsx "github.com/yernsun/network-service-composer/internal/metrics"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type discoveryBoundaryLease struct {
	check            func(int64) error
	checks, releases atomic.Int64
	releaseError     error
}

func (l *discoveryBoundaryLease) Check(context.Context) error {
	n := l.checks.Add(1)
	if l.check != nil {
		return l.check(n)
	}
	return nil
}
func (l *discoveryBoundaryLease) Release(context.Context) error {
	l.releases.Add(1)
	return l.releaseError
}

type discoveryBoundaryLocker struct {
	value   lease.Lease
	failure error
}

func (l discoveryBoundaryLocker) TryLock(context.Context, string) (lease.Lease, error) {
	return l.value, l.failure
}

type discoveryBoundaryProvider struct {
	snapshot      func(context.Context, int64) (model.RuntimeSnapshot, error)
	signals       chan model.DiscoverySignal
	errors        chan error
	calls, closed atomic.Int64
}

func (p *discoveryBoundaryProvider) Snapshot(ctx context.Context) (model.RuntimeSnapshot, error) {
	n := p.calls.Add(1)
	if p.snapshot != nil {
		return p.snapshot(ctx, n)
	}
	return model.RuntimeSnapshot{}, nil
}
func (p *discoveryBoundaryProvider) Watch(context.Context) (<-chan model.DiscoverySignal, <-chan error) {
	return p.signals, p.errors
}
func (p *discoveryBoundaryProvider) Close() error { p.closed.Add(1); return nil }

func discoveryBoundaryFixture(t *testing.T) (*Manager, model.Runtime, *discoveryBoundaryProvider, *discoveryBoundaryLease) {
	t.Helper()
	ctx := context.Background()
	s, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "discovery.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "boundary", Enabled: true, BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = s.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	rt := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "boundary", Type: model.RuntimeDocker, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = s.CreateRuntime(ctx, &rt); err != nil {
		t.Fatal(err)
	}
	p := &discoveryBoundaryProvider{signals: make(chan model.DiscoverySignal, 4), errors: make(chan error, 4)}
	l := &discoveryBoundaryLease{}
	cfg := config.Defaults().Controller
	cfg.Discovery.Reconcile.Interval = config.Duration(time.Hour)
	cfg.Discovery.FullReconcile.Interval = config.Duration(time.Hour)
	cfg.Discovery.Events.Debounce = config.Duration(time.Millisecond)
	cfg.Discovery.Events.MaxDelay = config.Duration(time.Millisecond)
	factory := runtime.ProviderFactory(func(context.Context, model.Runtime, string) (runtime.Provider, error) { return p, nil })
	m := New(s, factory, discoveryBoundaryLocker{value: l}, cfg, metricsx.New(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	return m, rt, p, l
}

func TestDiscoveryProviderFailureAndLeaseBoundaries(t *testing.T) {
	failure := errors.New("discovery interrupted")
	for _, kind := range []string{"initial lease", "initial snapshot", "closed events", "closed errors", "watch failure", "nil watch error", "event failure", "periodic failure", "full failure", "event lease", "periodic lease", "full lease", "default timers"} {
		t.Run(kind, func(t *testing.T) {
			m, rt, p, l := discoveryBoundaryFixture(t)
			want := failure
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			switch kind {
			case "initial lease":
				l.check = func(int64) error { return failure }
			case "initial snapshot":
				p.snapshot = func(context.Context, int64) (model.RuntimeSnapshot, error) { return model.RuntimeSnapshot{}, failure }
			case "closed events":
				close(p.signals)
				want = nil
			case "closed errors":
				close(p.errors)
				want = nil
			case "watch failure":
				p.errors <- failure
			case "nil watch error":
				p.errors <- nil
				p.errors <- failure
			case "default timers":
				m.config.Discovery.Reconcile.Interval = 0
				m.config.Discovery.FullReconcile.Interval = 0
				m.config.Discovery.Events.Debounce = 0
				m.config.Discovery.Events.MaxDelay = 0
				p.errors <- failure
			default:
				if strings.Contains(kind, "lease") {
					l.check = func(n int64) error {
						if n > 1 {
							return lease.ErrLost
						}
						return nil
					}
					want = lease.ErrLost
				} else {
					p.snapshot = func(_ context.Context, n int64) (model.RuntimeSnapshot, error) {
						if n > 1 {
							return model.RuntimeSnapshot{}, failure
						}
						return model.RuntimeSnapshot{}, nil
					}
				}
				if strings.HasPrefix(kind, "event") {
					p.signals <- model.DiscoverySignal{Kind: "changed"}
				}
				if strings.HasPrefix(kind, "periodic") {
					m.config.Discovery.Reconcile.Interval = config.Duration(time.Millisecond)
				}
				if strings.HasPrefix(kind, "full") {
					m.config.Discovery.FullReconcile.Interval = config.Duration(time.Millisecond)
				}
			}
			err := m.runProvider(ctx, rt, p, l)
			if want != nil {
				if !errors.Is(err, want) {
					t.Fatalf("provider failure hidden: %v want %v", err, want)
				}
			} else if err == nil || !strings.Contains(err.Error(), "stream closed") {
				t.Fatalf("closed event stream became healthy: %v", err)
			}
			if kind == "initial lease" && p.calls.Load() != 0 {
				t.Fatal("snapshot read after losing ownership")
			}
		})
	}
}

func TestDiscoveryRuntimeOwnershipAndReconnect(t *testing.T) {
	failure := errors.New("provider connection lost")
	for _, kind := range []string{"busy", "lease before connect", "lease after connect failure", "connect retry", "lease after watch", "watch retry", "cancel during provider", "release error"} {
		t.Run(kind, func(t *testing.T) {
			m, rt, p, l := discoveryBoundaryFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			want := context.Canceled
			var factoryCalls atomic.Int64
			m.factory = runtime.ProviderFactory(func(context.Context, model.Runtime, string) (runtime.Provider, error) {
				n := factoryCalls.Add(1)
				if n > 1 {
					cancel()
					return nil, failure
				}
				if kind == "connect retry" || kind == "lease after connect failure" {
					return nil, failure
				}
				if kind == "cancel during provider" || kind == "release error" {
					p.snapshot = func(context.Context, int64) (model.RuntimeSnapshot, error) {
						cancel()
						return model.RuntimeSnapshot{}, context.Canceled
					}
				}
				return p, nil
			})
			switch kind {
			case "busy":
				m.locker = discoveryBoundaryLocker{failure: lease.ErrBusy}
				want = lease.ErrBusy
			case "lease before connect":
				l.check = func(int64) error { return lease.ErrLost }
				want = lease.ErrLost
			case "lease after connect failure":
				l.check = func(n int64) error {
					if n == 2 {
						return lease.ErrLost
					}
					return nil
				}
				want = lease.ErrLost
			case "lease after watch":
				p.errors <- failure
				l.check = func(n int64) error {
					if n == 3 {
						return lease.ErrLost
					}
					return nil
				}
				want = lease.ErrLost
			case "watch retry":
				p.errors <- failure
			case "release error":
				l.releaseError = failure
			}
			if err := m.runRuntime(ctx, rt, "nsc-ingress"); !errors.Is(err, want) {
				t.Fatalf("runtime worker outcome: %v want %v", err, want)
			}
			if kind == "busy" {
				if l.releases.Load() != 0 || factoryCalls.Load() != 0 {
					t.Fatal("busy worker acquired resources")
				}
				return
			}
			if l.releases.Load() != 1 {
				t.Fatalf("ownership leaked: %d", l.releases.Load())
			}
			if kind == "watch retry" || kind == "connect retry" {
				current, err := m.store.GetRuntime(context.Background(), rt.ID)
				if err != nil || current.Status != model.RuntimeOffline || current.LastError != failure.Error() || factoryCalls.Load() != 2 {
					t.Fatalf("outage was not retained across reconnect: %+v %v calls=%d", current, err, factoryCalls.Load())
				}
			}
			if kind == "watch retry" || kind == "lease after watch" || kind == "cancel during provider" || kind == "release error" {
				if p.closed.Load() != 1 {
					t.Fatalf("provider leaked: %d", p.closed.Load())
				}
			}
		})
	}
}

func TestDiscoveryPersistenceFailureDoesNotReportRuntimeOffline(t *testing.T) {
	m, rt, p, l := discoveryBoundaryFixture(t)
	failure := errors.New("deadlock detected (SQLSTATE 40P01)")
	store := &discoveryPersistenceStore{Store: m.store, stage: "snapshot", failure: failure}
	m.store = store
	ctx, cancel := context.WithCancel(context.Background())
	l.check = func(n int64) error {
		// Cancel after runProvider returns its persistence failure and before the
		// reconnect delay, while still allowing the classification branch to run.
		if n == 3 {
			cancel()
		}
		return nil
	}
	err := m.runRuntime(ctx, rt, "nsc-ingress")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runtime worker outcome: %v", err)
	}
	if store.marks != 0 {
		t.Fatalf("database contention was recorded as a Docker outage: marks=%d", store.marks)
	}
	current, getErr := store.Store.GetRuntime(context.Background(), rt.ID)
	if getErr != nil || current.Status == model.RuntimeOffline || current.LastError != "" {
		t.Fatalf("last successful runtime evidence was replaced: %+v %v", current, getErr)
	}
	if p.closed.Load() != 1 || l.releases.Load() != 1 {
		t.Fatalf("runtime resources leaked: provider=%d lease=%d", p.closed.Load(), l.releases.Load())
	}
}

func TestDiscoveryCanceledBeforeRuntimeLoopReleasesOwnership(t *testing.T) {
	m, rt, p, l := discoveryBoundaryFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.runRuntime(ctx, rt, "nsc-ingress"); !errors.Is(err, context.Canceled) {
		t.Fatalf("runtime worker outcome: %v", err)
	}
	if p.calls.Load() != 0 || p.closed.Load() != 0 || l.releases.Load() != 1 {
		t.Fatalf("canceled worker acquired resources or leaked ownership: calls=%d closed=%d releases=%d", p.calls.Load(), p.closed.Load(), l.releases.Load())
	}
}

func TestDiscoveryOutageRecordingBoundsAndDatabaseFailure(t *testing.T) {
	m, rt, _, _ := discoveryBoundaryFixture(t)
	ctx := context.Background()
	m.recordOffline(ctx, rt, "connect", errors.New(strings.Repeat("x", 2000)))
	got, err := m.store.GetRuntime(ctx, rt.ID)
	if err != nil || got.Status != model.RuntimeOffline || len(got.LastError) != 1024 || got.AppliedObservation == 0 {
		t.Fatalf("outage evidence missing: %+v %v", got, err)
	}
	if err = m.store.Close(); err != nil {
		t.Fatal(err)
	}
	m.recordOffline(ctx, rt, "connect", errors.New("another failure"))
	if err = m.Run(ctx); err == nil {
		t.Fatal("manager reported success with unavailable database")
	}
}
