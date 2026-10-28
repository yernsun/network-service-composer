package controlplane

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func electionStore(t *testing.T) SQLState {
	t.Helper()
	s, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "election.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return SQLState{Store: s}
}

func TestOfflineRecoveryUsesTheSameSchedulerLock(t *testing.T) {
	ctx := context.Background()
	locker := lease.New(nil, storage.SQLite)
	leader, err := locker.TryLock(ctx, leaderKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = TryOfflineLock(ctx, locker); !errors.Is(err, lease.ErrBusy) {
		t.Fatal("offline work bypassed active scheduler", err)
	}
	if err = leader.Release(ctx); err != nil {
		t.Fatal(err)
	}
	offline, err := TryOfflineLock(ctx, locker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = locker.TryLock(ctx, leaderKey); !errors.Is(err, lease.ErrBusy) {
		t.Fatal("scheduler bypassed offline recovery", err)
	}
	if err = offline.Release(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDurableTermsRejectStaleRenewalAndRevoke(t *testing.T) {
	s := electionStore(t)
	ctx := context.Background()
	first, err := s.Claim(ctx, Identity{ID: "first"}, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := s.Renew(ctx, first, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Renew(ctx, first, 15*time.Second); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale heartbeat accepted: %v", err)
	}
	if err = s.Validate(ctx, "other", first.Term); !errors.Is(err, ErrNotLeader) {
		t.Fatal("foreign owner accepted", err)
	}
	if err = s.Revoke(ctx, renewed); err != nil {
		t.Fatal(err)
	}
	if err = s.Validate(ctx, first.ID, first.Term); !errors.Is(err, ErrNotLeader) {
		t.Fatal("revoked leader accepted", err)
	}
	second, err := s.Claim(ctx, Identity{ID: "second"}, 15*time.Second)
	if err != nil || second.Term != first.Term+1 {
		t.Fatal("term did not advance", second, err)
	}
	if err = s.Revoke(ctx, renewed); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("old owner revoked successor", err)
	}
	if err = s.Validate(ctx, second.ID, second.Term); err != nil {
		t.Fatal("successor lost ownership", err)
	}
	if err = s.Validate(ctx, first.ID, first.Term); !errors.Is(err, ErrNotLeader) {
		t.Fatal("delayed old command accepted", err)
	}
	second.ExpiresAt = time.Unix(1, 0)
	if _, err = s.save(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Renew(ctx, second, 15*time.Second); !errors.Is(err, ErrNotLeader) {
		t.Fatal("expired term revived", err)
	}
}

type observedLocker struct {
	lease.Locker
	busy chan struct{}
}

func (l observedLocker) TryLock(ctx context.Context, key string) (lease.Lease, error) {
	v, err := l.Locker.TryLock(ctx, key)
	if errors.Is(err, lease.ErrBusy) {
		select {
		case l.busy <- struct{}{}:
		default:
		}
	}
	return v, err
}

func await[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("coordination did not complete")
		var zero T
		return zero
	}
}

func TestOnlyLockOwnerSchedulesAndSuccessorFencesOldTerm(t *testing.T) {
	s := electionStore(t)
	busy := make(chan struct{}, 1)
	locker := observedLocker{Locker: lease.New(nil, storage.SQLite), busy: busy}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	first := &Election{Identity: Identity{ID: "first"}, Store: s, Locker: locker, Logger: logger, Interval: 20 * time.Millisecond, Timeout: 100 * time.Millisecond, TTL: 5 * time.Second}
	second := &Election{Identity: Identity{ID: "second"}, Store: s, Locker: locker, Logger: logger, Interval: 20 * time.Millisecond, Timeout: 100 * time.Millisecond, TTL: 5 * time.Second}
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	started1, started2 := make(chan *Term, 1), make(chan *Term, 1)
	done1, done2 := make(chan error, 1), make(chan error, 1)
	run := func(started chan *Term) func(context.Context, *Term) error {
		return func(ctx context.Context, v *Term) error { started <- v; <-ctx.Done(); return ctx.Err() }
	}
	go func() { done1 <- first.Run(ctx1, run(started1)) }()
	old := await(t, started1)
	if err := old.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	go func() { done2 <- second.Run(ctx2, run(started2)) }()
	await(t, busy)
	if second.Active() != nil {
		t.Fatal("follower started scheduling")
	}
	cancel1()
	await(t, done1)
	next := await(t, started2)
	if next.State.Term <= old.State.Term {
		t.Fatal("takeover reused term")
	}
	if err := old.Check(context.Background()); !errors.Is(err, ErrNotLeader) {
		t.Fatal("old scheduling context usable", err)
	}
	if err := next.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancel2()
	await(t, done2)
	if first.Active() != nil || second.Active() != nil {
		t.Fatal("stopped election retained authority")
	}
}

func TestDatabaseFailureCancelsScheduling(t *testing.T) {
	s := electionStore(t)
	e := &Election{Identity: Identity{ID: "first"}, Store: s, Locker: lease.New(nil, storage.SQLite), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Interval: 20 * time.Millisecond, Timeout: 100 * time.Millisecond, TTL: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan *Term, 1)
	stopped := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- e.Run(ctx, func(ctx context.Context, v *Term) error {
			started <- v
			<-ctx.Done()
			stopped <- struct{}{}
			return ctx.Err()
		})
	}()
	old := await(t, started)
	if err := s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	await(t, stopped)
	if err := old.Check(context.Background()); !errors.Is(err, ErrNotLeader) {
		t.Fatal("database failure retained authority", err)
	}
	cancel()
	await(t, done)
}
