package nodecontrol

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestNodeEventBatchRequiresCurrentOwnershipAndDeduplicatesRetry(t *testing.T) {
	reports, now, owner := reportsFixture(t)
	reports.EventsEnabled = true
	binding := reports.Bindings["controller-a"]
	binding.ClusterID = "cluster-a"
	reports.Bindings["controller-a"] = binding
	store := reports.Store.(*storage.SQLStore)
	ctx := context.Background()
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "node-events", BackendNetwork: "nsc-test", ChallengeUpstream: "http://controller:8080", Enabled: true,
		Revision: 1, CreatedAt: *now, UpdatedAt: *now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtime := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "node-events", Type: model.RuntimeDocker,
		Endpoint: "unix:///nonexistent", ClusterID: binding.ClusterID, Enabled: true, Status: model.RuntimeOnline,
		Revision: 1, CreatedAt: *now, UpdatedAt: *now}
	if err := store.CreateRuntime(ctx, &runtime); err != nil {
		t.Fatal(err)
	}
	service := model.Service{ID: id.New(id.Service), RuntimeID: runtime.ID, ServiceKey: "demoapp/api", ExternalID: "demoapp/api",
		Name: "api", Namespace: "demoapp", DiscoveredProtocol: "http", DiscoveredPort: 8080, DiscoveredEnabled: true,
		Status: model.ServiceAvailable, Revision: 1, CreatedAt: *now, UpdatedAt: *now}
	if err := store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	session, err := reports.Begin(ctx, "controller-a", binding.NodeID, binding.EngineID, "boot-a", *owner)
	if err != nil {
		t.Fatal(err)
	}
	batch := EventBatch{NodeID: binding.NodeID, EngineID: binding.EngineID, Generation: session.Generation, At: *now,
		Events: []model.DiscoverySignal{{ActorType: "container", ExternalID: "container-1", ServiceKey: service.ServiceKey, Kind: "oom", At: now.Add(-time.Second)}}}
	for range 2 {
		if err := reports.AcceptEvents(ctx, "controller-a", batch); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.ListTimeline(ctx, storage.TimelineFilter{RuntimeID: runtime.ID}, storage.Page{Limit: 10})
	if err != nil || len(rows) != 1 || !rows[0].OOMKilled || rows[0].NodeID != binding.NodeID {
		t.Fatalf("duplicate event was not associated once: %#v %v", rows, err)
	}
	malicious := batch
	malicious.Events = []model.DiscoverySignal{{ActorType: "container", ExternalID: "container-2", ServiceKey: service.ServiceKey, Kind: "die", Status: "secret-token", At: *now}}
	if err := reports.AcceptEvents(ctx, "controller-a", malicious); !errors.Is(err, ErrIdentity) {
		t.Fatalf("unallowlisted status accepted: %v", err)
	}
	*owner = 12
	if err := reports.AcceptEvents(ctx, "controller-a", batch); !errors.Is(err, ErrStale) {
		t.Fatalf("lost owner still wrote events: %v", err)
	}
	*now = now.Add(31 * time.Second)
	stale := batch
	stale.At = *now
	if err := reports.AcceptEvents(ctx, "controller-a", stale); !errors.Is(err, ErrStale) {
		t.Fatalf("stale generation accepted: %v", err)
	}
}
