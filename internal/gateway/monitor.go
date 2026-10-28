package gateway

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type Monitor struct {
	store   *storage.SQLStore
	timeout time.Duration
	broker  *Broker
	logger  *slog.Logger
}

func NewMonitor(store *storage.SQLStore, timeout time.Duration, broker *Broker, logger *slog.Logger) *Monitor {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Monitor{store: store, timeout: timeout, broker: broker, logger: logger}
}

func (m *Monitor) Run(ctx context.Context) error {
	interval := min(m.timeout/2, 5*time.Second)
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			count, err := m.store.MarkGatewaysOffline(ctx, now.UTC().Add(-m.timeout), now.UTC())
			if err != nil && !errors.Is(err, context.Canceled) {
				m.logger.ErrorContext(ctx, "mark offline gateways", "error", err)
			} else if count > 0 {
				m.logger.WarnContext(ctx, "gateways marked offline", "count", count)
			}
			if err := m.reconcileProbeLeaders(ctx); err != nil && !errors.Is(err, context.Canceled) {
				m.logger.ErrorContext(ctx, "reconcile gateway probe leaders", "error", err)
			}
		}
	}
}

func (m *Monitor) reconcileProbeLeaders(ctx context.Context) error {
	after := ""
	for {
		pools, err := m.store.ListPools(ctx, storage.Page{After: after, Limit: 200})
		if err != nil {
			return err
		}
		for _, previous := range pools {
			current, selectErr := m.store.SelectProbeLeader(ctx, previous.ID, m.timeout)
			if selectErr != nil {
				return selectErr
			}
			if m.broker != nil && (current.ProbeEpoch != previous.ProbeEpoch || current.DesiredRevision != previous.DesiredRevision) {
				m.broker.Publish(model.StateChange{PoolID: current.ID, Revision: current.DesiredRevision, Kind: "probe-leader"})
			}
		}
		if len(pools) < 200 {
			return nil
		}
		after = pools[len(pools)-1].ID
	}
}
