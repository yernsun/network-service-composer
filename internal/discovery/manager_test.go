package discovery

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type debounceProvider struct {
	snapshot model.RuntimeSnapshot
	signals  chan model.DiscoverySignal
	errors   chan error
	called   chan int64
	calls    atomic.Int64
	active   atomic.Int64
	maximum  atomic.Int64
	watches  atomic.Int64
}

func (p *debounceProvider) Snapshot(context.Context) (model.RuntimeSnapshot, error) {
	active := p.active.Add(1)
	defer p.active.Add(-1)
	for {
		maximum := p.maximum.Load()
		if active <= maximum || p.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	call := p.calls.Add(1)
	select {
	case p.called <- call:
	default:
	}
	return p.snapshot, nil
}
func (p *debounceProvider) Watch(context.Context) (<-chan model.DiscoverySignal, <-chan error) {
	p.watches.Add(1)
	return p.signals, p.errors
}
func (p *debounceProvider) Close() error { return nil }

type capturePublisher struct{ changes chan model.StateChange }

func (p capturePublisher) Publish(change model.StateChange) {
	select {
	case p.changes <- change:
	default:
	}
}

func TestEventBurstIsDebouncedAndReconcileIsSingleFlight(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "discovery.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "debounce-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "debounce-runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	policy := model.ReadinessPolicy{Mode: model.ReadinessGrace, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: time.Minute}
	provider := &debounceProvider{
		snapshot: model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, ObservedAt: now, Services: []model.DiscoveredService{{ExternalID: "api", Name: "api", Protocol: "http", TargetPort: 8080, Enabled: true, DesiredInstances: 1, Readiness: policy, Endpoints: []model.DiscoveredEndpoint{{ExternalID: "container", Address: "10.90.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, Weight: 1}}, Routes: []model.DiscoveredRoute{{ExternalKey: "public", Host: "debounce.example.com", Path: "/", TLSMode: model.TLSDisabled, UpstreamProtocol: "http"}}}}},
		signals:  make(chan model.DiscoverySignal, 64),
		errors:   make(chan error, 1),
		called:   make(chan int64, 8),
	}
	controllerConfig := config.Defaults().Controller
	controllerConfig.Discovery.Events.Debounce = config.Duration(20 * time.Millisecond)
	controllerConfig.Discovery.Events.MaxDelay = config.Duration(100 * time.Millisecond)
	controllerConfig.Discovery.Reconcile.Interval = config.Duration(time.Hour)
	controllerConfig.Discovery.FullReconcile.Interval = config.Duration(time.Hour)
	publisher := capturePublisher{changes: make(chan model.StateChange, 8)}
	manager := New(store, nil, nil, controllerConfig, nil, nil, publisher)
	runCtx, cancel := context.WithCancel(context.Background())
	ownership, err := lease.New(store.DB(), store.Dialect()).TryLock(runCtx, "runtime/"+runtimeValue.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ownership.Release(context.Background()) })
	done := make(chan error, 1)
	go func() { done <- manager.runProvider(runCtx, runtimeValue, provider, ownership) }()

	waitForCall(t, provider.called, 1)
	for index := 0; index < 40; index++ {
		provider.signals <- model.DiscoverySignal{RuntimeID: runtimeValue.ID, Kind: "update", ExternalID: "container"}
	}
	waitForCall(t, provider.called, 2)
	select {
	case call := <-provider.called:
		t.Fatalf("event burst produced an extra reconcile call %d", call)
	case <-time.After(80 * time.Millisecond):
	}
	if provider.calls.Load() != 2 || provider.maximum.Load() != 1 {
		t.Fatalf("calls=%d maximum_concurrency=%d", provider.calls.Load(), provider.maximum.Load())
	}
	select {
	case change := <-publisher.changes:
		if change.PoolID != pool.ID || change.Revision < 1 {
			t.Fatalf("unexpected post-commit publication: %+v", change)
		}
	default:
		t.Fatal("initial effective state was not published")
	}
	provider.signals <- model.DiscoverySignal{RuntimeID: runtimeValue.ID, ActorType: "container", Kind: "oom",
		ExternalID: "container", ServiceKey: "api", At: time.Now().UTC()}
	deadline := time.Now().Add(time.Second)
	for {
		events, listErr := store.ListTimeline(ctx, storage.TimelineFilter{RuntimeID: runtimeValue.ID, Category: "fault"}, storage.Page{Limit: 10})
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(events) == 1 && events[0].Action == "oom" && events[0].Source == "docker" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Docker signal was not retained: %#v", events)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runProvider returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runProvider did not stop after cancellation")
	}
}

func TestWorkerSurvivesObservationRevisionChanges(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "worker-spec.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "worker-spec-edge", BackendNetwork: "nsc-ingress", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "worker-spec-runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	provider := &debounceProvider{
		snapshot: model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, ObservedAt: now},
		signals:  make(chan model.DiscoverySignal),
		errors:   make(chan error),
		called:   make(chan int64, 4),
	}
	var factoryCalls atomic.Int64
	factory := runtime.ProviderFactory(func(context.Context, model.Runtime, string) (runtime.Provider, error) {
		factoryCalls.Add(1)
		return provider, nil
	})
	manager := New(store, factory, lease.New(store.DB(), store.Dialect()), config.Defaults().Controller, nil, nil, nil)
	if err = manager.syncWorkers(ctx); err != nil {
		t.Fatal(err)
	}
	waitForCall(t, provider.called, 1)
	deadline := time.Now().Add(time.Second)
	for {
		current, getErr := store.GetRuntime(ctx, runtimeValue.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if current.AppliedObservation > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial observation was not persisted")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err = manager.syncWorkers(ctx); err != nil {
		t.Fatal(err)
	}
	if got := factoryCalls.Load(); got != 1 {
		t.Fatalf("observation revision restarted provider: factory calls=%d", got)
	}
	cancel()
	manager.stopWorkers()
}

func TestDisabledEventWatchUsesPeriodicReconcileOnly(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "periodic-only.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "periodic-edge", BackendNetwork: "nsc-ingress", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "periodic-runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	provider := &debounceProvider{snapshot: model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, ObservedAt: now}, signals: make(chan model.DiscoverySignal), errors: make(chan error), called: make(chan int64, 4)}
	controllerConfig := config.Defaults().Controller
	controllerConfig.Discovery.Events.Enabled = false
	controllerConfig.RuntimeEvents.Enabled = false
	controllerConfig.Discovery.Reconcile.Interval = config.Duration(20 * time.Millisecond)
	controllerConfig.Discovery.FullReconcile.Interval = config.Duration(time.Hour)
	manager := New(store, nil, nil, controllerConfig, nil, nil, nil)
	runCtx, cancel := context.WithCancel(context.Background())
	ownership, err := lease.New(store.DB(), store.Dialect()).TryLock(runCtx, "runtime/"+runtimeValue.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer ownership.Release(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.runProvider(runCtx, runtimeValue, provider, ownership) }()
	waitForCall(t, provider.called, 1)
	waitForCall(t, provider.called, 2)
	if got := provider.watches.Load(); got != 0 {
		t.Fatalf("disabled Docker events still opened %d watch streams", got)
	}
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runProvider returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("periodic provider did not stop after cancellation")
	}
}

func waitForCall(t *testing.T, calls <-chan int64, want int64) {
	t.Helper()
	select {
	case got := <-calls:
		if got != want {
			t.Fatalf("snapshot call = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for snapshot call %d", want)
	}
}
