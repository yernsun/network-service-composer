package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func TestSystemServiceProbePreservesProtectionAndDeletionFence(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	_, runtime, now := managedRuntimeFixture(t, store)
	policy := model.ReadinessPolicy{Mode: model.ReadinessHTTP, Path: "/ready", Scheme: "http", Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1}
	_, _, err := store.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: runtime.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{{ServiceKey: "gateway", ExternalID: "gateway", RuntimeObjectID: "gateway-object", Name: "gateway", QualifiedName: "nsc_gateway", DNSName: "nsc_gateway", DNSStatus: model.DNSReady, RoutingEligible: true, Protocol: "http", TargetPort: 80, Enabled: true, Readiness: policy}}})
	if err != nil {
		t.Fatal(err)
	}
	services, err := store.ListServices(ctx, Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("services: %v %v", services, err)
	}
	service := services[0]
	claim, _ := json.Marshal(SystemService{ProjectID: "system-project", System: true})
	for _, doc := range []ControlDocument{{ID: "system-project", Kind: "project", Payload: json.RawMessage(`{"system":true}`)}, {ID: "gateway-object", Kind: "system-service", ParentID: runtime.ID, Payload: claim}} {
		doc.CreatedAt = now
		doc.UpdatedAt = now
		if err = store.WriteControlDocuments(WithSystemBootstrap(ctx), []ControlWrite{{Document: doc}}); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.CheckProjectResource(ctx, service.ID); !errors.Is(err, ErrSystemProtected) {
		t.Fatalf("missing mutation protection: %v", err)
	}
	target := currentProbeTarget(t, store, runtime.PoolID, service.ID)
	pool, err := store.GetPool(ctx, runtime.PoolID)
	if err != nil {
		t.Fatal(err)
	}
	result := model.ProbeResult{PoolID: pool.ID, ServiceID: service.ID, Epoch: pool.ProbeEpoch, Revision: pool.ProbeRevision, TargetToken: target.TargetToken, Sequence: 1, Success: false, Error: "unreachable", ObservedAt: now}
	changed, err := store.UpdateServiceProbe(ctx, result)
	if err != nil || !changed {
		t.Fatalf("system observation rejected: changed=%t err=%v", changed, err)
	}
	current, err := store.GetService(ctx, service.ID)
	if err != nil || current.DNSStatus != model.DNSUnreachable {
		t.Fatalf("probe did not persist: %v %v", current.DNSStatus, err)
	}
	if err = store.CheckProjectResource(ctx, service.ID); !errors.Is(err, ErrSystemProtected) {
		t.Fatalf("observation enabled mutation: %v", err)
	}
	guard := ControlDocument{ID: "project-guard:" + service.ID, Kind: "deleted-project-guard", Payload: json.RawMessage(`{"project_id":"system-project","operation_id":"deleted"}`), CreatedAt: now, UpdatedAt: now}
	if err = store.WriteControlDocuments(WithSystemBootstrap(ctx), []ControlWrite{{Document: guard}}); err != nil {
		t.Fatal(err)
	}
	result.Sequence++
	result.Success = true
	changed, err = store.UpdateServiceProbe(ctx, result)
	if err != nil || changed {
		t.Fatalf("late probe crossed deletion fence: %t %v", changed, err)
	}
	after, err := store.GetService(ctx, service.ID)
	if err != nil || after.DNSStatus != current.DNSStatus || after.Revision != current.Revision {
		t.Fatalf("late probe changed retained service: %v", err)
	}
}
