package discovery

import (
	"context"
	"errors"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"strings"
	"testing"
	"time"
)

type discoveryPersistenceStore struct {
	storage.Store
	stage                   string
	failure                 error
	changed, swept, applied bool
	seen                    model.RuntimeSnapshot
	message                 string
	sweeps, marks           int
}

func (s *discoveryPersistenceStore) NextObservation(context.Context, string) (int64, error) {
	if s.stage == "epoch" {
		return 0, s.failure
	}
	return 7, nil
}
func (s *discoveryPersistenceStore) ApplyRuntimeSnapshot(_ context.Context, v model.RuntimeSnapshot) (bool, int64, error) {
	s.seen = v
	if s.stage == "snapshot" {
		return false, 0, s.failure
	}
	return s.changed, 8, nil
}
func (s *discoveryPersistenceStore) SweepEndpointLifecycle(context.Context, string, int64, string, time.Time, time.Duration, time.Duration, time.Duration) (bool, int64, error) {
	s.sweeps++
	if s.stage == "sweep" {
		return false, 0, s.failure
	}
	return s.swept, 9, nil
}
func (s *discoveryPersistenceStore) MarkRuntimeOffline(_ context.Context, _ string, epoch int64, message string, _ time.Time) (bool, error) {
	s.marks++
	s.message = message
	if epoch != 7 {
		panic("unfenced offline write")
	}
	if s.stage == "offline" {
		return false, s.failure
	}
	return s.applied, nil
}
func TestDiscoveryPersistenceFailuresDoNotPublishUnconfirmedChanges(t *testing.T) {
	for _, mode := range []string{"epoch", "snapshot", "sweep", "changed", "swept", "both", "unchanged", "without publisher", "without metrics", "invalid config"} {
		t.Run(mode, func(t *testing.T) {
			m, rt, p, _ := discoveryBoundaryFixture(t)
			failure := errors.New("discovery persistence unavailable")
			f := &discoveryPersistenceStore{Store: m.store, stage: mode, failure: failure, changed: mode == "changed" || mode == "both" || mode == "without publisher", swept: mode == "swept" || mode == "both"}
			m.store = f
			changes := make(chan model.StateChange, 2)
			m.publisher = capturePublisher{changes: changes}
			if mode == "without publisher" {
				m.publisher = nil
			}
			if mode == "without metrics" {
				m.metrics = nil
			}
			observed := time.Now().UTC()
			p.snapshot = func(context.Context, int64) (model.RuntimeSnapshot, error) {
				v := model.RuntimeSnapshot{RuntimeID: "untrusted", Observation: 999, ObservedAt: observed}
				if mode == "invalid config" {
					v.Services = []model.DiscoveredService{{ExternalID: "web", ConfigurationError: "invalid supported labels"}}
				}
				return v, nil
			}
			err := m.reconcile(context.Background(), rt, p, "fixture")
			failed := mode == "epoch" || mode == "snapshot" || mode == "sweep"
			if failed {
				if !errors.Is(err, failure) || !isDiscoveryPersistenceFailure(err) || len(changes) != 0 {
					t.Fatal("unconfirmed state published", err, len(changes))
				}
				return
			}
			if err != nil || f.seen.RuntimeID != rt.ID || f.seen.Observation != 7 || !f.seen.ObservedAt.Equal(observed) {
				t.Fatal("observation lost fencing or time", f.seen, err)
			}
			expect := (f.changed || f.swept) && m.publisher != nil
			if expect {
				select {
				case got := <-changes:
					want := int64(8)
					if f.swept {
						want = 9
					}
					if got.PoolID != rt.PoolID || got.Revision != want || got.Kind != "runtime-reconcile" {
						t.Fatal("incorrect change evidence", got)
					}
				default:
					t.Fatal("confirmed change not published")
				}
			} else if len(changes) != 0 {
				t.Fatal("unchanged state published")
			}
		})
	}
}
func TestOfflineRecordingRetainsFencingAndDoesNotPublishFailedLifecycle(t *testing.T) {
	for _, mode := range []string{"epoch", "epoch canceled", "offline", "offline canceled", "stale", "sweep", "sweep canceled", "changed", "unchanged", "without publisher", "without metrics"} {
		t.Run(mode, func(t *testing.T) {
			m, rt, _, _ := discoveryBoundaryFixture(t)
			f := &discoveryPersistenceStore{Store: m.store, stage: strings.Fields(mode)[0], failure: errors.New("offline persistence unavailable"), applied: mode != "stale", swept: mode == "changed" || mode == "without publisher"}
			if strings.HasSuffix(mode, "canceled") {
				f.failure = context.Canceled
			}
			m.store = f
			changes := make(chan model.StateChange, 2)
			m.publisher = capturePublisher{changes: changes}
			if mode == "without publisher" {
				m.publisher = nil
			}
			if mode == "without metrics" {
				m.metrics = nil
			}
			m.recordOffline(context.Background(), rt, "snapshot", errors.New(strings.Repeat("x", 1200)))
			if f.marks > 0 && len(f.message) != 1024 {
				t.Fatal("outage diagnostic was not bounded", len(f.message))
			}
			if strings.HasPrefix(mode, "epoch") && f.marks != 0 {
				t.Fatal("unfenced outage wrote state")
			}
			if (strings.HasPrefix(mode, "offline") || mode == "stale") && f.sweeps != 0 {
				t.Fatal("unconfirmed outage advanced lifecycle")
			}
			if mode == "changed" {
				select {
				case v := <-changes:
					if v.Kind != "runtime-offline-drain" || v.PoolID != rt.PoolID || v.Revision != 9 {
						t.Fatal(v)
					}
				default:
					t.Fatal("confirmed drain not published")
				}
			} else if len(changes) != 0 {
				t.Fatal("unconfirmed drain published")
			}
		})
	}
}
