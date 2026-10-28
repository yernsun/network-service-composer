package controlplane

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/yernsun/network-service-composer/internal/lease"
)

// Election owns the scheduling lifetime. A losing candidate never starts Run's
// callback. The callback must finish cancellation before this process willingly
// releases its lock; in-flight external effects still need task-level evidence.
type Election struct {
	Identity Identity
	Locker   lease.Locker
	Store    StateStore
	Logger   *slog.Logger
	TTL      time.Duration
	Interval time.Duration
	Timeout  time.Duration
	mu       sync.RWMutex
	active   *Term
}

type Term struct {
	State   State
	Context context.Context
	store   StateStore
	held    lease.Lease
}

// TryOfflineLock excludes the active scheduler while an operator prepares or
// releases a restored database. Offline commands must still stop all peers.
func TryOfflineLock(ctx context.Context, locker lease.Locker) (lease.Lease, error) {
	return locker.TryLock(ctx, leaderKey)
}

func (t *Term) Check(ctx context.Context) error {
	if t == nil || t.Context.Err() != nil {
		return ErrNotLeader
	}
	if err := t.held.Check(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrNotLeader, err)
	}
	return t.store.Validate(ctx, t.State.ID, t.State.Term)
}

func (e *Election) Active() *Term {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.active
}

func (e *Election) setActive(t *Term) {
	e.mu.Lock()
	e.active = t
	e.mu.Unlock()
}

func (e *Election) Run(ctx context.Context, run func(context.Context, *Term) error) error {
	if e.TTL == 0 {
		e.TTL = 15 * time.Second
	}
	if e.Interval == 0 {
		e.Interval = time.Second
	}
	if e.Timeout == 0 {
		e.Timeout = 3 * time.Second
	}
	if e.Identity.ID == "" || e.Locker == nil || e.Store == nil || run == nil || e.Interval <= 0 || e.Timeout <= 0 || e.TTL < time.Second || e.Interval+e.Timeout >= e.TTL {
		return errors.New("invalid controller election configuration")
	}
	logger := e.Logger
	if logger == nil {
		logger = slog.Default()
	}
	for ctx.Err() == nil {
		attempt, cancel := context.WithTimeout(ctx, e.Timeout)
		held, err := e.Locker.TryLock(attempt, leaderKey)
		cancel()
		if err == nil {
			err = e.lead(ctx, held, run, logger)
			if err != nil && !errors.Is(err, ErrNotLeader) && !errors.Is(err, context.Canceled) {
				return err
			}
		} else if !errors.Is(err, lease.ErrBusy) && ctx.Err() == nil {
			logger.WarnContext(ctx, "controller election unavailable", "error", err)
		}
		timer := time.NewTimer(e.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return ctx.Err()
}

func (e *Election) lead(ctx context.Context, held lease.Lease, run func(context.Context, *Term) error, logger *slog.Logger) error {
	defer held.Release(context.Background())
	attempt, cancel := context.WithTimeout(ctx, e.Timeout)
	who := e.Identity
	if session, ok := held.(lease.SessionLease); ok {
		owner, err := session.SessionID(attempt)
		if err != nil {
			cancel()
			return fmt.Errorf("%w: %v", ErrNotLeader, err)
		}
		who.LockOwner = owner
	}
	state, err := e.Store.Claim(attempt, who, e.TTL)
	if err == nil {
		err = held.Check(attempt)
	}
	if err == nil {
		err = e.Store.Validate(attempt, state.ID, state.Term)
	}
	cancel()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotLeader, err)
	}
	termCtx, stop := context.WithCancel(ctx)
	term := &Term{State: state, Context: termCtx, store: e.Store, held: held}
	e.setActive(term)
	defer e.setActive(nil)
	defer stop()
	logger.InfoContext(ctx, "controller leadership acquired", "instance_id", state.ID, "term", state.Term)
	finished := make(chan error, 1)
	go func() { finished <- run(termCtx, term) }()
	ticker := time.NewTicker(e.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-termCtx.Done():
			stop()
			<-finished
			e.revoke(state)
			return termCtx.Err()
		case err := <-finished:
			stop()
			e.revoke(state)
			if err == nil && ctx.Err() == nil {
				return errors.New("controller scheduling stopped unexpectedly")
			}
			return err
		case <-ticker.C:
			attempt, cancel := context.WithTimeout(termCtx, e.Timeout)
			err = held.Check(attempt)
			if err == nil {
				state, err = e.Store.Renew(attempt, state, e.TTL)
			}
			cancel()
			if err != nil {
				stop()
				e.setActive(nil)
				logger.WarnContext(ctx, "controller leadership lost", "instance_id", term.State.ID, "term", term.State.Term, "error", err)
				<-finished
				// Do not revoke after loss: a successor may already own the row.
				return ErrNotLeader
			}
		}
	}
}

func (e *Election) revoke(v State) {
	ctx, cancel := context.WithTimeout(context.Background(), e.Timeout)
	defer cancel()
	_ = e.Store.Revoke(ctx, v)
}
