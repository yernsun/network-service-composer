package controlplane

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestExternalLeaderSessionFencing(t *testing.T) {
	for _, fixture := range []struct{ driver, variable string }{{"postgres", "NSC_TEST_POSTGRES_DSN"}, {"mysql", "NSC_TEST_MYSQL_DSN"}} {
		t.Run(fixture.driver, func(t *testing.T) {
			dsn := os.Getenv(fixture.variable)
			if dsn == "" {
				t.Skip("isolated database fixture is required: " + fixture.variable)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			store, err := storage.Open(fixture.driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			locker := lease.New(store.DB(), store.Dialect())
			held, err := locker.TryLock(ctx, leaderKey)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Release(context.Background())
			owner, err := held.(lease.SessionLease).SessionID(ctx)
			if err != nil {
				t.Fatal(err)
			}
			state := SQLState{Store: store}
			first, err := state.Claim(ctx, Identity{ID: "first", LockOwner: owner}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.Validate(ctx, first.ID, first.Term); err != nil {
				t.Fatal(err)
			}
			if _, err := locker.TryLock(ctx, leaderKey); !errors.Is(err, lease.ErrBusy) {
				t.Fatal("second database session acquired leader lock", err)
			}
			if err := held.Release(ctx); err != nil {
				t.Fatal(err)
			}
			if err := state.Validate(ctx, first.ID, first.Term); !errors.Is(err, ErrNotLeader) {
				t.Fatal("unexpired heartbeat authorized a released database lock", err)
			}
			next, err := locker.TryLock(ctx, leaderKey)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Release(context.Background())
			owner, err = next.(lease.SessionLease).SessionID(ctx)
			if err != nil {
				t.Fatal(err)
			}
			second, err := state.Claim(ctx, Identity{ID: "second", LockOwner: owner}, time.Minute)
			if err != nil || second.Term <= first.Term {
				t.Fatal("durable term not advanced", err)
			}
			if err := state.Validate(ctx, first.ID, first.Term); !errors.Is(err, ErrNotLeader) {
				t.Fatal("stale command not fenced", err)
			}
			if err := state.Validate(ctx, second.ID, second.Term); err != nil {
				t.Fatal(err)
			}
		})
	}
}
