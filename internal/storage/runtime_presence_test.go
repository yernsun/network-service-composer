package storage

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func TestRuntimePresenceTracksAcceptedInventoryAndPreservesOwnershipHistory(t *testing.T) {
	s := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, s)
	created := now.Add(-time.Hour)
	service := model.Service{ID: "presence-service", RuntimeID: runtime.ID, ServiceKey: "fixture/web", ExternalID: "owned-object", RuntimeObjectID: "owned-object", Name: "web", Namespace: "fixture", Managed: true, RuntimeCreatedAt: &created, RuntimeUpdatedAt: &created, CreatedAt: created, UpdatedAt: now, Revision: 1}
	if err := s.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	snapshot := model.RuntimeSnapshot{RuntimeID: runtime.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{{ServiceKey: service.ServiceKey, ExternalID: service.ExternalID, RuntimeObjectID: service.RuntimeObjectID, Name: service.Name, Namespace: service.Namespace, RuntimeCreatedAt: &created, RuntimeUpdatedAt: &created}}}
	if _, _, err := s.ApplyRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	read := func() (ControlDocument, RuntimePresence) {
		t.Helper()
		doc, err := s.GetControlDocument(ctx, RuntimePresenceID(runtime.ID))
		if err != nil {
			t.Fatal(err)
		}
		var presence RuntimePresence
		if err = json.Unmarshal(doc.Payload, &presence); err != nil {
			t.Fatal(err)
		}
		return doc, presence
	}
	first, observed := read()
	if first.Kind != "runtime-presence" || first.ParentID != runtime.ID || observed.RuntimeID != runtime.ID || observed.Observation != 1 || !observed.ObservedAt.Equal(now) || !reflect.DeepEqual(observed.ServiceIDs, []string{service.ID}) {
		t.Fatal("a present zero-replica Service lost its inventory membership", observed)
	}
	snapshot.Observation, snapshot.ObservedAt, snapshot.Services = 2, now.Add(time.Second), nil
	if _, _, err := s.ApplyRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	second, absent := read()
	if second.Revision != first.Revision+1 || !second.CreatedAt.Equal(first.CreatedAt) || absent.Observation != 2 || !absent.ObservedAt.Equal(snapshot.ObservedAt) || len(absent.ServiceIDs) != 0 {
		t.Fatal("authoritative absence retained old membership", absent)
	}
	current, err := s.GetService(ctx, service.ID)
	if err != nil || current.Status != model.ServiceUnavailable || current.RuntimeObjectID != service.RuntimeObjectID || !current.CreatedAt.Equal(service.CreatedAt) || !equalOptionalTime(current.RuntimeCreatedAt, service.RuntimeCreatedAt) || !equalOptionalTime(current.RuntimeUpdatedAt, service.RuntimeUpdatedAt) {
		t.Fatal("absence erased the stable managed claim or runtime creation history", err)
	}
	snapshot.Observation, snapshot.ObservedAt, snapshot.Services = 1, now.Add(2*time.Second), []model.DiscoveredService{{ServiceKey: service.ServiceKey}}
	if _, _, err := s.ApplyRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	after, _ := read()
	if !reflect.DeepEqual(after, second) {
		t.Fatal("rejected old inventory refreshed presence evidence")
	}
}

func TestRuntimePresenceFailureRollsBackAcceptedInventory(t *testing.T) {
	s := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, s)
	before, err := s.GetRuntime(ctx, runtime.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB().ExecContext(ctx, `CREATE TRIGGER reject_presence BEFORE INSERT ON control_documents WHEN NEW.kind='runtime-presence' BEGIN SELECT RAISE(ABORT,'fixture presence write unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: runtime.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{{ServiceKey: "fixture/web", Name: "web"}}})
	if err == nil {
		t.Fatal("inventory committed without its authoritative presence evidence")
	}
	after, err := s.GetRuntime(ctx, runtime.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("presence failure partially advanced the Runtime", err)
	}
	services, err := s.ListAllServicesFiltered(ctx, ServiceFilter{RuntimeID: runtime.ID})
	if err != nil || len(services) != 0 {
		t.Fatal("presence failure committed partial discovered Services", err)
	}
}

func TestSharedRuntimePresenceUsesTheSameMicrosecondTimeAsAuthoritativeInventory(t *testing.T) {
	observed := time.Date(2026, 10, 13, 10, 20, 30, 123456789, time.FixedZone("fixture-zone", 8*60*60))
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		t.Run(string(dialect), func(t *testing.T) {
			got := runtimeObservationTime(dialect, observed)
			want := observed
			if dialect != SQLite {
				want = observed.UTC().Truncate(time.Microsecond)
				if got.Location() != time.UTC || got.Nanosecond()%1000 != 0 {
					t.Fatal("shared observation retained a timezone or unpersistable fraction")
				}
			}
			if !got.Equal(want) || !runtimeObservationTime(dialect, got).Equal(got) {
				t.Fatal("inventory precision changed its time or was not stable")
			}
		})
	}
}
