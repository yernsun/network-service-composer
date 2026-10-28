package discovery

import (
	"context"
	"errors"
	"fmt"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"testing"
	"time"
)

type discoveryPageStore struct {
	storage.Store
	failure   error
	calls     int
	badCursor bool
}

func (s *discoveryPageStore) ListRuntimes(_ context.Context, p storage.Page) ([]model.Runtime, error) {
	s.calls++
	if p.Limit != 200 {
		s.badCursor = true
	}
	if p.After != "" {
		if p.After != "199" {
			s.badCursor = true
		}
		if s.failure != nil {
			return nil, s.failure
		}
		return []model.Runtime{{ID: "200"}}, nil
	}
	values := make([]model.Runtime, 200)
	for i := range values {
		values[i].ID = fmt.Sprint(i)
	}
	return values, nil
}
func (s *discoveryPageStore) ListPools(ctx context.Context, p storage.Page) ([]model.GatewayPool, error) {
	rows, e := s.ListRuntimes(ctx, p)
	if e != nil {
		return nil, e
	}
	values := make([]model.GatewayPool, len(rows))
	for i, v := range rows {
		values[i].ID = v.ID
	}
	return values, nil
}
func TestDiscoveryInventoryPaginationDoesNotReturnPartialSuccess(t *testing.T) {
	failure := errors.New("second inventory page unavailable")
	for _, kind := range []string{"runtime", "pool"} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/failure=%t", kind, fail), func(t *testing.T) {
				s := &discoveryPageStore{}
				if fail {
					s.failure = failure
				}
				count := 0
				var e error
				if kind == "runtime" {
					var rows []model.Runtime
					rows, e = listAllRuntimes(context.Background(), s)
					count = len(rows)
				} else {
					var rows []model.GatewayPool
					rows, e = listAllPools(context.Background(), s)
					count = len(rows)
				}
				if s.calls != 2 || s.badCursor {
					t.Fatal("inventory skipped or repeated page")
				}
				if fail {
					if !errors.Is(e, failure) || count != 0 {
						t.Fatal("partial inventory accepted", e)
					}
				} else if e != nil || count != 201 {
					t.Fatal("later-page runtime omitted", count, e)
				}
			})
		}
	}
}
func TestDiscoveryRestartsFinishedWorkersAndRemovesDisabledRuntimes(t *testing.T) {
	m, rt, _, _ := discoveryBoundaryFixture(t)
	m.locker = discoveryBoundaryLocker{failure: lease.ErrBusy}
	ctx := context.Background()
	if e := m.syncWorkers(ctx); e != nil {
		t.Fatal(e)
	}
	m.mu.Lock()
	first := m.workers[rt.ID]
	m.mu.Unlock()
	if first == nil {
		t.Fatal("enabled runtime did not start worker")
	}
	select {
	case <-first.done:
	case <-time.After(time.Second):
		t.Fatal("busy worker did not exit")
	}
	if e := m.syncWorkers(ctx); e != nil {
		t.Fatal(e)
	}
	m.mu.Lock()
	second := m.workers[rt.ID]
	m.mu.Unlock()
	if second == nil || second == first {
		t.Fatal("finished worker prevented a new ownership attempt")
	}
	select {
	case <-second.done:
	case <-time.After(time.Second):
		t.Fatal("second busy worker did not exit")
	}
	current, e := m.store.GetRuntime(ctx, rt.ID)
	if e != nil {
		t.Fatal(e)
	}
	current.Enabled = false
	if e = m.store.UpdateRuntime(ctx, &current, current.Revision); e != nil {
		t.Fatal(e)
	}
	if e = m.syncWorkers(ctx); e != nil {
		t.Fatal(e)
	}
	m.mu.Lock()
	count := len(m.workers)
	m.mu.Unlock()
	if count != 0 {
		t.Fatal("disabled runtime retained worker")
	}
	m.stopWorkers()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if e = m.Run(canceled); !errors.Is(e, context.Canceled) {
		t.Fatal("canceled manager continued", e)
	}
	m.store.Close()
	if e = m.syncWorkers(ctx); e == nil {
		t.Fatal("unavailable inventory accepted")
	}
}
