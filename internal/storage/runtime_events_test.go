package storage

import (
	"context"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestTimelineCombinesScopedOperationsAndAllowlistedRuntimeEvidence(t *testing.T) {
	store := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, store)
	appID, projectID, environmentID := "app-demoapp", "project-demoapp", "environment-prod"
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO applications(id,runtime_id,name,project_name,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
		appID, runtime.ID, "demoapp", "demoapp", model.ApplicationRunning, now, now); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, kind, parent, payload string }{
		{projectID, "project", "", `{}`},
		{environmentID, "environment", projectID, `{"application_id":"app-demoapp"}`},
		{"application-environment:" + appID, "application-environment", appID, `{"environment_id":"environment-prod"}`},
	} {
		if _, err := store.DB().ExecContext(ctx, `INSERT INTO control_documents(id,kind,parent_id,payload,revision,created_at,updated_at) VALUES(?,?,?,?,1,?,?)`,
			row.id, row.kind, row.parent, row.payload, now, now); err != nil {
			t.Fatal(err)
		}
	}
	service := model.Service{ID: id.New(id.Service), RuntimeID: runtime.ID, ServiceKey: "demoapp/api", ExternalID: "demoapp/api",
		ApplicationID: appID, Name: "api", Namespace: "demoapp", DiscoveredProtocol: "http", DiscoveredPort: 8080,
		DiscoveredEnabled: true, Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	instance := model.Endpoint{ID: id.New(id.Endpoint), ServiceID: service.ID, ExternalID: "container-1", Address: "10.0.0.2", Port: 8080,
		RuntimeState: model.EndpointRunning, HealthState: model.HealthHealthy, ReadyState: model.Ready, ReadinessSource: model.ReadinessDocker,
		Weight: 1, FirstSeenAt: now, LastSeenAt: now, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertEndpoint(ctx, &instance); err != nil {
		t.Fatal(err)
	}
	old := model.DeploymentOperation{ID: "op-old", ApplicationID: appID, RuntimeID: runtime.ID, ServiceID: service.ID,
		Type: "SCALE", Status: model.OperationSucceeded, Metadata: model.Metadata{Revision: 1, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}}
	if _, err := store.DB().ExecContext(ctx, store.bind(`INSERT INTO deployment_operations (`+operationColumns+`) VALUES (`+columnPlaceholders(operationColumns)+`)`), operationArgs(&old)...); err != nil {
		t.Fatal(err)
	}
	fresh := model.DeploymentOperation{ID: "op-new", ApplicationID: appID, RuntimeID: runtime.ID, ServiceID: service.ID,
		Type: "SCALE", Status: model.OperationSucceeded, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if _, err := store.CreateDeploymentOperation(ctx, &fresh); err != nil {
		t.Fatal(err)
	}
	var snapshot string
	if err := store.DB().QueryRowContext(ctx, `SELECT project_id FROM deployment_operations WHERE id=?`, fresh.ID).Scan(&snapshot); err != nil || snapshot != projectID {
		t.Fatalf("new operation scope was not saved: %q %v", snapshot, err)
	}
	source := model.RuntimeEventSource{ID: "runtime:" + runtime.ID, RuntimeID: runtime.ID}
	events := []model.DiscoverySignal{
		{ActorType: "container", ExternalID: "container-1", ServiceKey: "demoapp/api", Kind: "oom", At: now.Add(time.Second)},
		{ActorType: "container", ExternalID: "container-1", ServiceKey: "demoapp/api", Kind: "health_status: unhealthy", At: now.Add(2 * time.Second)},
		{ActorType: "container", ExternalID: "container-1", ServiceKey: "demoapp/api", Kind: "name=secret", At: now.Add(3 * time.Second)},
		{ActorType: "container", ExternalID: "other", ServiceKey: "unrecognized", Kind: "die", At: now.Add(4 * time.Second)},
	}
	if err := store.RecordRuntimeEvents(ctx, source, events, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRuntimeEvents(ctx, source, events, now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	filter := TimelineFilter{ProjectID: projectID, EnvironmentID: environmentID}
	rows, err := store.ListTimeline(ctx, filter, Page{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("timeline has %d rows, want old/new operations and two events: %#v", len(rows), rows)
	}
	if rows[0].Action != "health_status: unhealthy" || rows[1].Action != "oom" || !rows[1].OOMKilled || rows[1].InstanceID != instance.ID || rows[3].OperationID != old.ID {
		t.Fatalf("unexpected evidence order or identity: %#v", rows)
	}
	page, err := store.ListTimeline(ctx, filter, Page{Limit: 2})
	if err != nil || len(page) != 2 {
		t.Fatalf("first page: %#v %v", page, err)
	}
	filter.BeforeAt, filter.BeforeID = page[1].OccurredAt, page[1].ID
	page, err = store.ListTimeline(ctx, filter, Page{Limit: 2})
	if err != nil || len(page) != 2 || page[1].OperationID != old.ID {
		t.Fatalf("second page: %#v %v", page, err)
	}
	if err := store.MarkRuntimeEventSource(ctx, source, "stream_disconnected", now.Add(7*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRuntimeEvents(ctx, source, nil, now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	gaps, err := store.ListTimeline(ctx, TimelineFilter{Category: "gap"}, Page{Limit: 50})
	if err != nil || len(gaps) != 1 || gaps[0].GapEndAt == nil {
		t.Fatalf("closed gap: %#v %v", gaps, err)
	}
	scopedGaps, err := store.ListTimeline(ctx, TimelineFilter{ProjectID: projectID, EnvironmentID: environmentID,
		RuntimeID: runtime.ID, GapRuntimeIDs: []string{runtime.ID}, Category: "gap"}, Page{Limit: 50})
	if err != nil || len(scopedGaps) != 1 || scopedGaps[0].Source != "gap" {
		t.Fatalf("runtime coverage gap missing from scoped timeline: %#v %v", scopedGaps, err)
	}
	if err := store.PruneRuntimeEvents(ctx, now.Add(11*time.Second)); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.ListTimeline(ctx, TimelineFilter{}, Page{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range remaining {
		if row.Source == "docker" {
			t.Fatalf("expired Docker evidence survived retention: %#v", row)
		}
	}
	if len(remaining) != 2 {
		t.Fatalf("operation retention changed: %#v", remaining)
	}
	if _, err := store.DB().ExecContext(ctx, `DELETE FROM control_documents WHERE id=?`, environmentID); err != nil {
		t.Fatal(err)
	}
	remaining, err = store.ListTimeline(ctx, TimelineFilter{}, Page{Limit: 50})
	if err != nil || len(remaining) != 2 {
		t.Fatalf("lost environment link removed operation history: %#v %v", remaining, err)
	}
	for _, row := range remaining {
		if row.OperationID == old.ID && (row.ProjectID != "" || row.EnvironmentID != "") {
			t.Fatalf("legacy scope remained after its link disappeared: %#v", row)
		}
		if row.OperationID == fresh.ID && (row.ProjectID != projectID || row.EnvironmentID != environmentID) {
			t.Fatalf("new operation scope snapshot disappeared: %#v", row)
		}
	}
}

func TestTimelineIncludesNodeGapForItsSwarmRuntime(t *testing.T) {
	store := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, store)
	if _, err := store.DB().ExecContext(ctx, `UPDATE runtimes SET cluster_id=? WHERE id=?`, "cluster-a", runtime.ID); err != nil {
		t.Fatal(err)
	}
	source := model.RuntimeEventSource{ID: "node:worker-a", ClusterID: "cluster-a", NodeID: "worker-a"}
	if err := store.RecordRuntimeEvents(ctx, source, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkRuntimeEventSource(ctx, source, "stream_disconnected", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRuntimeEvents(ctx, source, nil, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListTimeline(ctx, TimelineFilter{RuntimeID: runtime.ID, Category: "gap"}, Page{Limit: 10})
	if err != nil || len(rows) != 1 || rows[0].NodeID != "worker-a" {
		t.Fatalf("Swarm node gap was excluded by Runtime filter: %#v %v", rows, err)
	}
}

func TestRuntimeTaskStatesAtSameTimestampRemainDistinct(t *testing.T) {
	store := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, store)
	service := model.Service{ID: id.New(id.Service), RuntimeID: runtime.ID, ServiceKey: "demoapp_api",
		ExternalID: "demoapp_api", RuntimeObjectID: "docker-service-a", Name: "api", Namespace: "demoapp",
		DiscoveredProtocol: "http", DiscoveredPort: 8080, DiscoveredEnabled: true, Status: model.ServiceAvailable,
		Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	signals := []model.DiscoverySignal{
		{ActorType: "task", ExternalID: "task-a", ServiceObjectID: "docker-service-a", Kind: "update", Status: "running", At: now},
		{ActorType: "task", ExternalID: "task-a", ServiceObjectID: "docker-service-a", Kind: "update", Status: "failed", At: now},
	}
	if err := store.RecordRuntimeEvents(ctx, model.RuntimeEventSource{ID: "runtime:" + runtime.ID, RuntimeID: runtime.ID}, signals, now); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListTimeline(ctx, TimelineFilter{RuntimeID: runtime.ID, Category: "lifecycle"}, Page{Limit: 10})
	if err != nil || len(rows) != 2 || rows[0].Status == rows[1].Status {
		t.Fatalf("distinct task state evidence collapsed: %#v %v", rows, err)
	}
}

func TestRuntimeEventStorageFailureDoesNotAcknowledgeBatch(t *testing.T) {
	store := newSQLiteStore(t)
	ctx := context.Background()
	source := model.RuntimeEventSource{ID: "runtime:broken", RuntimeID: "broken"}
	now := time.Now().UTC()
	if err := store.RecordRuntimeEvents(ctx, source, nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `DROP TABLE runtime_events`); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRuntimeEvents(ctx, source, nil, now.Add(time.Minute)); err == nil {
		t.Fatal("storage failure was acknowledged")
	}
	sources, err := store.ListRuntimeEventSources(ctx)
	if err != nil || len(sources) != 1 || !sources[0].LastHeartbeatAt.Equal(now) {
		t.Fatalf("failed batch changed collection status: %#v %v", sources, err)
	}
}

func TestRuntimeEventCoveragePersistsMissedHeartbeatAndOverflow(t *testing.T) {
	store := newSQLiteStore(t)
	ctx := context.Background()
	source := model.RuntimeEventSource{ID: "runtime:covered", RuntimeID: "covered"}
	now := time.Now().UTC()
	if err := store.RecordRuntimeEvents(ctx, source, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRuntimeEvents(ctx, source, nil, now.Add(31*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkRuntimeEventSource(ctx, source, "overflow", now.Add(32*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRuntimeEvents(ctx, source, nil, now.Add(35*time.Second)); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListTimeline(ctx, TimelineFilter{Category: "gap"}, Page{Limit: 10})
	if err != nil || len(rows) != 2 || rows[0].Status != "overflow" || rows[1].Status != "heartbeat_missing" ||
		rows[0].GapEndAt == nil || rows[1].GapEndAt == nil {
		t.Fatalf("missing coverage gaps: %#v %v", rows, err)
	}
	sources, err := store.ListRuntimeEventSources(ctx)
	if err != nil || len(sources) != 1 || sources[0].State != "collecting" || !sources[0].FirstSeenAt.Equal(now) {
		t.Fatalf("source did not resume with its original collection start: %#v %v", sources, err)
	}
}
