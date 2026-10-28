package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/lease"
	metricsx "github.com/yernsun/network-service-composer/internal/metrics"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type Publisher interface {
	Publish(model.StateChange)
}

type Manager struct {
	store     storage.Store
	factory   runtime.Factory
	locker    lease.Locker
	config    config.ControllerConfig
	metrics   *metricsx.Metrics
	logger    *slog.Logger
	publisher Publisher

	mu      sync.Mutex
	workers map[string]*worker
	tasks   map[string]map[string]observedTask
}

type worker struct {
	spec   workerSpec
	cancel context.CancelFunc
	done   chan struct{}
}

// workerSpec contains only values that change the Docker provider connection
// or discovery semantics. Runtime revision also advances for observations and
// status changes, so using it here would tear down a healthy event watch after
// every reconcile.
type workerSpec struct {
	PoolID         string
	BackendNetwork string
	RuntimeType    model.RuntimeType
	Endpoint       string
	CredentialRef  string
}

type desiredWorker struct {
	runtime model.Runtime
	spec    workerSpec
}

// persistenceFailure separates Controller storage contention from evidence
// that the Docker Runtime itself is unreachable. The last successful Runtime
// observation remains valid while a failed transaction is retried by the
// worker; reporting it as a source outage makes healthy Docker services appear
// disconnected.
type persistenceFailure struct {
	operation string
	cause     error
}

func (e persistenceFailure) Error() string { return fmt.Sprintf("%s: %v", e.operation, e.cause) }
func (e persistenceFailure) Unwrap() error { return e.cause }

func discoveryPersistenceFailure(operation string, cause error) error {
	return persistenceFailure{operation: operation, cause: cause}
}

func isDiscoveryPersistenceFailure(err error) bool {
	var failure persistenceFailure
	return errors.As(err, &failure)
}

func New(store storage.Store, factory runtime.Factory, locker lease.Locker, cfg config.ControllerConfig, metrics *metricsx.Metrics, logger *slog.Logger, publisher Publisher) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{store: store, factory: factory, locker: locker, config: cfg, metrics: metrics, logger: logger, publisher: publisher,
		workers: map[string]*worker{}, tasks: map[string]map[string]observedTask{}}
}

func (m *Manager) Run(ctx context.Context) error {
	if err := m.syncWorkers(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.stopWorkers()
			return ctx.Err()
		case <-ticker.C:
			if err := m.syncWorkers(ctx); err != nil {
				m.logger.Error("failed to refresh runtime workers", "error", err)
			}
		}
	}
}

func (m *Manager) syncWorkers(ctx context.Context) error {
	runtimes, err := listAllRuntimes(ctx, m.store)
	if err != nil {
		return err
	}
	pools, err := listAllPools(ctx, m.store)
	if err != nil {
		return err
	}
	poolsByID := make(map[string]model.GatewayPool, len(pools))
	for _, pool := range pools {
		poolsByID[pool.ID] = pool
	}
	desired := make(map[string]desiredWorker, len(runtimes))
	for _, value := range runtimes {
		if !value.Enabled {
			continue
		}
		pool, exists := poolsByID[value.PoolID]
		if !exists {
			continue
		}
		spec := workerSpec{PoolID: value.PoolID, BackendNetwork: pool.BackendNetwork, RuntimeType: value.Type,
			Endpoint: value.Endpoint, CredentialRef: value.CredentialRef}
		desired[value.ID] = desiredWorker{runtime: value, spec: spec}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for runtimeID, current := range m.workers {
		value, wanted := desired[runtimeID]
		finished := isClosed(current.done)
		if !wanted || finished || value.spec != current.spec {
			current.cancel()
			delete(m.workers, runtimeID)
		}
	}
	for runtimeID, value := range desired {
		if _, exists := m.workers[runtimeID]; exists {
			continue
		}
		workerCtx, cancel := context.WithCancel(ctx)
		current := &worker{spec: value.spec, cancel: cancel, done: make(chan struct{})}
		m.workers[runtimeID] = current
		go func(rt model.Runtime, spec workerSpec, state *worker) {
			defer close(state.done)
			if err := m.runRuntime(workerCtx, rt, spec.BackendNetwork); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, lease.ErrBusy) {
				m.logger.Error("runtime worker stopped", "runtime_id", rt.ID, "runtime_type", rt.Type, "error", err)
			}
		}(value.runtime, value.spec, current)
	}
	return nil
}

func (m *Manager) stopWorkers() {
	m.mu.Lock()
	workers := make([]*worker, 0, len(m.workers))
	for _, value := range m.workers {
		value.cancel()
		workers = append(workers, value)
	}
	m.mu.Unlock()
	for _, value := range workers {
		<-value.done
	}
}

func (m *Manager) runRuntime(ctx context.Context, rt model.Runtime, backendNetwork string) error {
	ownership, err := m.locker.TryLock(ctx, "runtime/"+rt.ID)
	if err != nil {
		return err
	}
	m.logger.Info("runtime ownership acquired", "runtime_id", rt.ID, "runtime_type", rt.Type, "pool_id", rt.PoolID)
	defer func() {
		if err := ownership.Release(context.Background()); err != nil {
			m.logger.Warn("runtime ownership release failed", "runtime_id", rt.ID, "error", err)
			return
		}
		m.logger.Info("runtime ownership released", "runtime_id", rt.ID, "runtime_type", rt.Type, "pool_id", rt.PoolID)
	}()
	backoff := time.Second
	for ctx.Err() == nil {
		if err = ownership.Check(ctx); err != nil {
			return err
		}
		provider, err := m.factory.New(ctx, rt, backendNetwork)
		if err != nil {
			if lockErr := ownership.Check(ctx); lockErr != nil {
				return lockErr
			}
			m.recordOffline(ctx, rt, "connect", err)
			if m.config.RuntimeEvents.Enabled && len(m.config.Nodes) == 0 {
				_ = m.store.MarkRuntimeEventSource(ctx, model.RuntimeEventSource{ID: "runtime:" + rt.ID, RuntimeID: rt.ID}, "stream_disconnected", time.Now().UTC())
			}
			if err = sleepContext(ctx, backoff); err != nil {
				return err
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		err = m.runProvider(ctx, rt, provider, ownership)
		_, nodeReported := provider.(interface{ UsesNodeReports() bool })
		_ = provider.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, lease.ErrLost) {
			return err
		}
		if lockErr := ownership.Check(ctx); lockErr != nil {
			return lockErr
		}
		if isDiscoveryPersistenceFailure(err) {
			m.logger.Warn("runtime reconciliation persistence failed; retaining last runtime status", "runtime_id", rt.ID, "error", err)
		} else {
			m.recordOffline(ctx, rt, "watch", err)
			if m.config.RuntimeEvents.Enabled && !nodeReported {
				if markErr := m.store.MarkRuntimeEventSource(ctx, model.RuntimeEventSource{ID: "runtime:" + rt.ID, RuntimeID: rt.ID}, "stream_disconnected", time.Now().UTC()); markErr != nil {
					m.logger.Warn("record runtime event gap", "runtime_id", rt.ID, "error", markErr)
				}
			}
		}
		if err = sleepContext(ctx, backoff); err != nil {
			return err
		}
		backoff = min(backoff*2, 30*time.Second)
	}
	return ctx.Err()
}

func (m *Manager) runProvider(ctx context.Context, rt model.Runtime, provider runtime.Provider, ownership lease.Lease) error {
	if err := ownership.Check(ctx); err != nil {
		return err
	}
	if err := m.reconcile(ctx, rt, provider, "initial"); err != nil {
		return err
	}
	var signals <-chan model.DiscoverySignal
	var watchErrors <-chan error
	if m.config.Discovery.Events.Enabled || m.config.RuntimeEvents.Enabled {
		signals, watchErrors = provider.Watch(ctx)
	}
	_, nodeReported := provider.(interface{ UsesNodeReports() bool })
	eventSource := model.RuntimeEventSource{ID: "runtime:" + rt.ID, RuntimeID: rt.ID}
	if m.config.RuntimeEvents.Enabled && !nodeReported {
		// A new watch cannot certify the interval since the previous worker's
		// last acknowledgement, even when that worker stopped recently.
		if err := m.store.MarkRuntimeEventSource(ctx, eventSource, "stream_disconnected", time.Now().UTC()); err != nil {
			return discoveryPersistenceFailure("mark runtime event stream restart", err)
		}
		if err := m.store.RecordRuntimeEvents(ctx, eventSource, nil, time.Now().UTC()); err != nil {
			return discoveryPersistenceFailure("start runtime event collection", err)
		}
	}
	reconcileEvery := m.config.Discovery.Reconcile.Interval.Std()
	if reconcileEvery <= 0 {
		reconcileEvery = 15 * time.Second
	}
	fullEvery := m.config.Discovery.FullReconcile.Interval.Std()
	if fullEvery <= 0 {
		fullEvery = 5 * time.Minute
	}
	periodic := time.NewTicker(reconcileEvery)
	full := time.NewTicker(fullEvery)
	heartbeat := time.NewTicker(10 * time.Second)
	defer periodic.Stop()
	defer full.Stop()
	defer heartbeat.Stop()

	debounce := m.config.Discovery.Events.Debounce.Std()
	if debounce <= 0 {
		debounce = 500 * time.Millisecond
	}
	maxDelay := m.config.Discovery.Events.MaxDelay.Std()
	if maxDelay < debounce {
		maxDelay = debounce
	}
	var debounceTimer, maxTimer *time.Timer
	var debounceC, maxC <-chan time.Time
	stopTimers := func() {
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
		if maxTimer != nil {
			maxTimer.Stop()
		}
		debounceTimer, maxTimer = nil, nil
		debounceC, maxC = nil, nil
	}
	defer stopTimers()
	reconcile := func(reason string) error {
		stopTimers()
		if err := ownership.Check(ctx); err != nil {
			return err
		}
		return m.reconcile(ctx, rt, provider, reason)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case signal, ok := <-signals:
			if !ok {
				return errors.New("Docker event stream closed")
			}
			if m.config.RuntimeEvents.Enabled && !nodeReported && signal.ActorType != "" {
				if err := ownership.Check(ctx); err != nil {
					return err
				}
				if err := m.store.RecordRuntimeEvents(ctx, eventSource, []model.DiscoverySignal{signal}, time.Now().UTC()); err != nil {
					return discoveryPersistenceFailure("record runtime event", err)
				}
			}
			if !m.config.Discovery.Events.Enabled {
				continue
			}
			if debounceTimer == nil {
				debounceTimer = time.NewTimer(debounce)
				maxTimer = time.NewTimer(maxDelay)
				debounceC, maxC = debounceTimer.C, maxTimer.C
			} else {
				if !debounceTimer.Stop() {
					select {
					case <-debounceTimer.C:
					default:
					}
				}
				debounceTimer.Reset(debounce)
				debounceC = debounceTimer.C
			}
		case err, ok := <-watchErrors:
			if !ok {
				return errors.New("Docker event error stream closed")
			}
			if err != nil {
				return err
			}
		case <-debounceC:
			if err := reconcile("event"); err != nil {
				return err
			}
		case <-maxC:
			if err := reconcile("event-max-delay"); err != nil {
				return err
			}
		case <-periodic.C:
			if err := reconcile("periodic"); err != nil {
				return err
			}
		case <-full.C:
			if err := reconcile("full"); err != nil {
				return err
			}
		case <-heartbeat.C:
			if m.config.RuntimeEvents.Enabled && !nodeReported {
				if err := ownership.Check(ctx); err != nil {
					return err
				}
				if err := m.store.RecordRuntimeEvents(ctx, eventSource, nil, time.Now().UTC()); err != nil {
					return discoveryPersistenceFailure("heartbeat runtime event collection", err)
				}
			}
		}
	}
}

func (m *Manager) reconcile(ctx context.Context, rt model.Runtime, provider runtime.Provider, reason string) (result error) {
	// Drivers can report an interrupted transaction instead of wrapping the
	// cancelled context. Preserve ordinary failures, but stop shutdown as cancellation.
	defer func() {
		if result != nil && ctx.Err() != nil {
			result = ctx.Err()
		}
	}()
	started := time.Now()
	epoch, err := m.store.NextObservation(ctx, rt.ID)
	if err != nil {
		m.observeReconcile(rt, started, "error")
		return discoveryPersistenceFailure("issue runtime observation", err)
	}
	snapshot, err := provider.Snapshot(ctx)
	if err != nil {
		m.observeReconcile(rt, started, "error")
		return err
	}
	snapshot.RuntimeID = rt.ID
	snapshot.Observation = epoch
	if snapshot.ObservedAt.IsZero() {
		snapshot.ObservedAt = time.Now().UTC()
	}
	for _, service := range snapshot.Services {
		if service.ConfigurationError != "" {
			m.logger.Warn("discovered service configuration is invalid", "runtime_id", rt.ID,
				"service_external_id", service.ExternalID, "error", service.ConfigurationError)
		}
	}
	changed, revision, err := m.store.ApplyRuntimeSnapshot(ctx, snapshot)
	if err != nil {
		m.observeReconcile(rt, started, "error")
		return discoveryPersistenceFailure("apply runtime snapshot", err)
	}
	if m.config.RuntimeEvents.Enabled && rt.Type == model.RuntimeSwarm {
		if err := m.recordTaskChanges(ctx, rt, snapshot); err != nil {
			m.observeReconcile(rt, started, "error")
			return discoveryPersistenceFailure("record Swarm task changes", err)
		}
	}
	if lifecycleChanged, lifecycleRevision, lifecycleErr := m.store.SweepEndpointLifecycle(ctx, rt.ID, snapshot.Observation, m.config.Discovery.OfflinePolicy, time.Now().UTC(), m.config.Discovery.StaleEndpointTimeout.Std(), m.config.Endpoint.DrainTimeout.Std(), m.config.GC.EndpointRetention.Std()); lifecycleErr != nil {
		m.observeReconcile(rt, started, "error")
		return discoveryPersistenceFailure("sweep endpoint lifecycle", lifecycleErr)
	} else if lifecycleChanged {
		changed, revision = true, lifecycleRevision
	}
	m.observeReconcile(rt, started, "success")
	if changed && m.publisher != nil {
		m.publisher.Publish(model.StateChange{PoolID: rt.PoolID, Revision: revision, Kind: "runtime-reconcile", IDs: []string{rt.ID}})
	}
	m.logger.Debug("runtime reconciled", "runtime_id", rt.ID, "reason", reason, "changed", changed, "pool_revision", revision)
	return nil
}

func (m *Manager) recordOffline(ctx context.Context, rt model.Runtime, operation string, cause error) {
	if m.metrics != nil {
		m.metrics.RuntimeUp.WithLabelValues(string(rt.Type)).Set(0)
		m.metrics.DiscoveryErrors.WithLabelValues(string(rt.Type), operation).Inc()
	}
	message := cause.Error()
	if len(message) > 1024 {
		message = message[:1024]
	}
	observation, err := m.store.NextObservation(ctx, rt.ID)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			m.logger.Error("failed to fence runtime outage", "runtime_id", rt.ID, "error", err)
		}
		return
	}
	applied, err := m.store.MarkRuntimeOffline(ctx, rt.ID, observation, message, time.Now().UTC())
	if err != nil && !errors.Is(err, context.Canceled) {
		m.logger.Error("failed to record runtime outage", "runtime_id", rt.ID, "error", err)
		return
	}
	if !applied {
		return
	}
	changed, revision, err := m.store.SweepEndpointLifecycle(ctx, rt.ID, observation, m.config.Discovery.OfflinePolicy, time.Now().UTC(),
		m.config.Discovery.StaleEndpointTimeout.Std(), m.config.Endpoint.DrainTimeout.Std(), m.config.GC.EndpointRetention.Std())
	if err != nil && !errors.Is(err, context.Canceled) {
		m.logger.Error("failed to advance offline endpoint lifecycle", "runtime_id", rt.ID, "error", err)
		return
	}
	if changed && m.publisher != nil {
		m.publisher.Publish(model.StateChange{PoolID: rt.PoolID, Revision: revision, Kind: "runtime-offline-drain", IDs: []string{rt.ID}})
	}
}

func (m *Manager) observeReconcile(rt model.Runtime, started time.Time, result string) {
	if m.metrics == nil {
		return
	}
	m.metrics.DiscoveryReconcile.WithLabelValues(string(rt.Type), result).Inc()
	m.metrics.DiscoveryDuration.WithLabelValues(string(rt.Type)).Observe(time.Since(started).Seconds())
	if result == "success" {
		m.metrics.RuntimeUp.WithLabelValues(string(rt.Type)).Set(1)
	}
}

func listAllRuntimes(ctx context.Context, store storage.Store) ([]model.Runtime, error) {
	var result []model.Runtime
	after := ""
	for {
		page, err := store.ListRuntimes(ctx, storage.Page{After: after, Limit: 200})
		if err != nil {
			return nil, err
		}
		result = append(result, page...)
		if len(page) < 200 {
			return result, nil
		}
		after = page[len(page)-1].ID
	}
}

func listAllPools(ctx context.Context, store storage.Store) ([]model.GatewayPool, error) {
	var result []model.GatewayPool
	after := ""
	for {
		page, err := store.ListPools(ctx, storage.Page{After: after, Limit: 200})
		if err != nil {
			return nil, err
		}
		result = append(result, page...)
		if len(page) < 200 {
			return result, nil
		}
		after = page[len(page)-1].ID
	}
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isClosed(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}
