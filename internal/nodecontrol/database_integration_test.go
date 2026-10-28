package nodecontrol

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestExternalFourNodeSessionsFenceReleasedOwnerAndConcurrentReplay(t *testing.T) {
	for _, fixture := range []struct{ driver, variable string }{{"postgres", "NSC_TEST_POSTGRES_DSN"}, {"mysql", "NSC_TEST_MYSQL_DSN"}} {
		t.Run(fixture.driver, func(t *testing.T) {
			dsn := os.Getenv(fixture.variable)
			if dsn == "" {
				t.Skip("isolated database fixture required: " + fixture.variable)
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
			keys, err := security.NewKeyring("test", map[string]string{"test": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
			if err != nil {
				t.Fatal(err)
			}
			state := controlplane.SQLState{Store: store}
			locker := lease.New(store.DB(), store.Dialect())
			reports := Reports{Store: store, Keys: keys, Bindings: map[string]Binding{}, Now: state.DatabaseTime, CheckOwner: func(ctx context.Context, key string, owner int64) error {
				return lease.CheckOwner(ctx, store.DB(), store.Dialect(), key, owner)
			}}
			held := []lease.Lease{}
			defer func() {
				for _, l := range held {
					_ = l.Release(context.Background())
				}
			}()
			for i := 0; i < 4; i++ {
				id := fmt.Sprintf("node-report-fixture-%d", i)
				b := Binding{InstanceID: id, NodeID: id, EngineID: "engine-" + id}
				reports.Bindings[id] = b
				l, err := locker.TryLock(ctx, LockKey(id))
				if err != nil {
					t.Fatal(err)
				}
				held = append(held, l)
				owner, err := l.(lease.SessionLease).SessionID(ctx)
				if err != nil {
					t.Fatal(err)
				}
				session, err := reports.Begin(ctx, id, id, b.EngineID, "boot-first", owner)
				if err != nil {
					t.Fatal(err)
				}
				now, err := state.DatabaseTime(ctx)
				if err != nil {
					t.Fatal(err)
				}
				o := observationFixture(now)
				o.Info.ID, o.Info.Swarm.NodeID = b.EngineID, id
				r := Report{NodeID: id, EngineID: b.EngineID, Generation: session.Generation, Sequence: 1, Observation: o}
				if err := reports.Accept(ctx, id, r); err != nil {
					t.Fatal(err)
				}
				if _, err := locker.TryLock(ctx, LockKey(id)); !errors.Is(err, lease.ErrBusy) {
					t.Fatal("second collector acquired the same node", err)
				}
				r.Sequence = 2
				var concurrent sync.WaitGroup
				results := make(chan error, 2)
				for n := 0; n < 2; n++ {
					concurrent.Add(1)
					go func() { defer concurrent.Done(); results <- reports.Accept(ctx, id, r) }()
				}
				concurrent.Wait()
				close(results)
				accepted := 0
				for err := range results {
					if err == nil {
						accepted++
					} else if !errors.Is(err, ErrStale) && !errors.Is(err, storage.ErrPrecondition) && !errors.Is(err, storage.ErrConflict) {
						t.Fatal(err)
					}
				}
				if accepted != 1 {
					t.Fatal("duplicate concurrent report accepted", accepted)
				}
				if err := l.Release(ctx); err != nil {
					t.Fatal(err)
				}
				r.Sequence++
				if err := reports.Accept(ctx, id, r); !errors.Is(err, lease.ErrLost) {
					t.Fatal("released database session authorized report", err)
				}
				if _, err := reports.Begin(ctx, id, id, b.EngineID, "late-boot", owner); !errors.Is(err, lease.ErrLost) {
					t.Fatal("released owner replaced boot generation", err)
				}
				read, err := reports.Read(ctx, id)
				if err != nil || read.Sequence != 2 {
					t.Fatal("rejected report changed retained observation", err)
				}
			}
		})
	}
}
