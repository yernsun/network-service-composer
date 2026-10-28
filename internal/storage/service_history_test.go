package storage

import (
	"context"
	"github.com/yernsun/network-service-composer/internal/model"
	"testing"
	"time"
)

func TestDeletedServiceSurvivesHealthSweepsAndRequiresFreshRediscovery(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteStore(t)
	now := time.Now().UTC()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	pool := model.GatewayPool{ID: "pool_history", Name: "history", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	must(s.CreatePool(ctx, &pool))
	rt := model.Runtime{ID: "rt_history", PoolID: pool.ID, Name: "history", Type: model.RuntimeDocker, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	must(s.CreateRuntime(ctx, &rt))
	observed := []model.DiscoveredService{}
	runtimeCreatedAt := now.Add(-48 * time.Hour)
	runtimeUpdatedAt := now.Add(-time.Hour)
	for _, name := range []string{"deleted", "managed", "stopped", "zero"} {
		v := model.DiscoveredService{ServiceKey: name, ExternalID: name, Name: name, DNSName: name, DNSStatus: model.DNSReady, Protocol: "http", TargetPort: 8080, Enabled: true}
		if name == "deleted" {
			v.RuntimeCreatedAt = &runtimeCreatedAt
			v.RuntimeUpdatedAt = &runtimeUpdatedAt
		}
		if name == "stopped" {
			v.DesiredInstances = 1
			v.Endpoints = []model.DiscoveredEndpoint{{ExternalID: "stopped-container", RuntimeState: model.EndpointDead}}
		}
		observed = append(observed, v)
	}
	apply := func(services []model.DiscoveredService) int64 {
		t.Helper()
		epoch, err := s.NextObservation(ctx, rt.ID)
		must(err)
		_, _, err = s.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: rt.ID, Observation: epoch, ObservedAt: now, Services: services})
		must(err)
		return epoch
	}
	managedSeed := model.Service{ID: "svc_managed", RuntimeID: rt.ID, ServiceKey: "managed", ExternalID: "managed", Name: "managed", Managed: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	must(s.UpsertService(ctx, &managedSeed))
	initial := apply(observed)
	rows, err := s.ListServices(ctx, Page{})
	must(err)
	ids := map[string]string{}
	for _, v := range rows {
		ids[v.Name] = v.ID
	}
	assertStatus := func(name string, removed bool) {
		t.Helper()
		v, err := s.GetService(ctx, ids[name])
		must(err)
		if (v.Status == model.ServiceRemoved) != removed {
			t.Fatalf("%s: %s, removed expected %v", name, v.Status, removed)
		}
	}
	epoch, err := s.NextObservation(ctx, rt.ID)
	must(err)
	_, err = s.MarkRuntimeOffline(ctx, rt.ID, epoch, "collector disconnected", now)
	must(err)
	for name := range ids {
		assertStatus(name, false)
	}
	// An obsolete empty response cannot turn a disconnect into deletion.
	_, _, err = s.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: rt.ID, Observation: initial, ObservedAt: now})
	must(err)
	for name := range ids {
		assertStatus(name, false)
	}
	epoch = apply(observed[2:])
	removed, err := s.GetService(ctx, ids["deleted"])
	must(err)
	if removed.RemovedAt == nil || !removed.RemovedAt.Equal(now) || removed.RuntimeCreatedAt == nil || !removed.RuntimeCreatedAt.Equal(runtimeCreatedAt) || removed.RuntimeUpdatedAt == nil || !removed.RuntimeUpdatedAt.Equal(runtimeUpdatedAt) {
		t.Fatalf("removal or Docker Service lifecycle evidence was not preserved: %#v", removed)
	}
	for i := 0; i < 4; i++ {
		_, _, err = s.SweepEndpointLifecycle(ctx, rt.ID, epoch, "fail-closed", now.Add(time.Duration(i+1)*time.Minute), time.Second, time.Second, time.Second)
		must(err)
		assertStatus("deleted", true)
		current, getErr := s.GetService(ctx, ids["deleted"])
		must(getErr)
		if current.RemovedAt == nil || !current.RemovedAt.Equal(*removed.RemovedAt) {
			t.Fatalf("repeated lifecycle sweep rewrote removal time: %#v", current.RemovedAt)
		}
		for _, name := range []string{"managed", "stopped", "zero"} {
			assertStatus(name, false)
		}
	}
	managed, err := s.GetService(ctx, ids["managed"])
	must(err)
	if managed.Status != model.ServiceUnavailable {
		t.Fatalf("missing managed service: %s", managed.Status)
	}
	// A stale positive response must not revive a confirmed deletion.
	_, _, err = s.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: rt.ID, Observation: initial, ObservedAt: now, Services: observed})
	must(err)
	assertStatus("deleted", true)
	apply(observed)
	assertStatus("deleted", false)
	revived, err := s.GetService(ctx, ids["deleted"])
	must(err)
	if revived.RemovedAt != nil || revived.RuntimeCreatedAt == nil || !revived.RuntimeCreatedAt.Equal(runtimeCreatedAt) || revived.RuntimeUpdatedAt == nil || !revived.RuntimeUpdatedAt.Equal(runtimeUpdatedAt) {
		t.Fatalf("fresh rediscovery did not clear removal evidence or preserve Docker lifecycle: %#v", revived)
	}
}

func TestOptionalRuntimeTimeComparisonCoversMissingAndPresentEvidence(t *testing.T) {
	one := time.Date(2026, 9, 22, 1, 2, 3, 0, time.UTC)
	two := one.Add(time.Second)
	for _, test := range []struct {
		name        string
		left, right *time.Time
		want        bool
	}{
		{name: "both missing", want: true},
		{name: "left missing", right: &one},
		{name: "right missing", left: &one},
		{name: "equal", left: &one, right: &one, want: true},
		{name: "different", left: &one, right: &two},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := equalOptionalTime(test.left, test.right); got != test.want {
				t.Fatalf("equalOptionalTime() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestUpsertServicePreservesAndAcceptsLifecycleEvidence(t *testing.T) {
	store := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, store)
	created := now.Add(-48 * time.Hour)
	updated := now.Add(-time.Hour)
	removed := now.Add(-time.Minute)
	service := model.Service{
		ID: "service_lifecycle_merge", RuntimeID: runtime.ID, ServiceKey: "demo_web", ExternalID: "object-1",
		Name: "web", QualifiedName: "demo_web", DNSName: "demo_web", DNSStatus: model.DNSReady,
		DiscoveredProtocol: "http", DiscoveredPort: 8080, DiscoveredEnabled: true, RoutingEligible: true,
		Status: model.ServiceRemoved, RuntimeCreatedAt: &created, RuntimeUpdatedAt: &updated, RemovedAt: &removed,
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}

	withoutEvidence := service
	withoutEvidence.ID = "replacement-id"
	withoutEvidence.RuntimeCreatedAt = nil
	withoutEvidence.RuntimeUpdatedAt = nil
	withoutEvidence.RemovedAt = nil
	withoutEvidence.UpdatedAt = now.Add(time.Second)
	if err := store.UpsertService(ctx, &withoutEvidence); err != nil {
		t.Fatal(err)
	}
	if withoutEvidence.RuntimeCreatedAt == nil || !withoutEvidence.RuntimeCreatedAt.Equal(created) || withoutEvidence.RuntimeUpdatedAt == nil || !withoutEvidence.RuntimeUpdatedAt.Equal(updated) || withoutEvidence.RemovedAt == nil || !withoutEvidence.RemovedAt.Equal(removed) {
		t.Fatalf("missing update evidence overwrote stored lifecycle: %#v", withoutEvidence)
	}

	created2 := created.Add(time.Hour)
	updated2 := updated.Add(time.Minute)
	removed2 := removed.Add(time.Second)
	withEvidence := withoutEvidence
	withEvidence.RuntimeCreatedAt = &created2
	withEvidence.RuntimeUpdatedAt = &updated2
	withEvidence.RemovedAt = &removed2
	withEvidence.UpdatedAt = now.Add(2 * time.Second)
	if err := store.UpsertService(ctx, &withEvidence); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetService(ctx, service.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RuntimeCreatedAt == nil || !stored.RuntimeCreatedAt.Equal(created2) || stored.RuntimeUpdatedAt == nil || !stored.RuntimeUpdatedAt.Equal(updated2) || stored.RemovedAt == nil || !stored.RemovedAt.Equal(removed2) {
		t.Fatalf("explicit lifecycle evidence was not persisted: %#v", stored)
	}
}
