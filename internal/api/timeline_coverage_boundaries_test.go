package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/cluster"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/runtime/engine/enginetest"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestCoverageTimelineRejectsInvalidPaginationWithoutScopeLeak(t *testing.T) {
	_, handler, _, _ := managedRuntimeFixture(t)
	encode := func(text string) string { return base64.RawURLEncoding.EncodeToString([]byte(text)) }
	for _, query := range []string{
		"limit=bad", "limit=0", "limit=200", "category=password",
		"cursor=" + strings.Repeat("x", 1025),
		"cursor=" + encode("2026-10-04T00:00:00Z"),
		"cursor=" + encode("2026-10-04T00:00:00Z\n"),
		"cursor=" + encode("2026-10-04T00:00:00Z\n"+strings.Repeat("x", 256)),
		"cursor=" + encode("invalid-timestamp\nid"),
	} {
		response := managementRequest(t, handler, http.MethodGet, "/timeline?"+query, nil, nil)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "validation.filter_invalid") {
			t.Fatalf("timeline invalid pagination accepted: %s %d", query, response.Code)
		}
	}
}

func coverageTimelineEnvelope(t *testing.T, response *httptest.ResponseRecorder) ([]model.TimelineRecord, string, timelineCoverage) {
	t.Helper()
	var envelope struct {
		Data       []model.TimelineRecord
		Pagination struct {
			Next string `json:"next_cursor"`
		}
		Coverage timelineCoverage
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &envelope) != nil {
		t.Fatalf("timeline query failed: %d %s", response.Code, response.Body)
	}
	return envelope.Data, envelope.Pagination.Next, envelope.Coverage
}

func coverageTimelineDocument(t *testing.T, server *Server, id, kind, parent string, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := server.store.(*storage.SQLStore).DB().ExecContext(t.Context(),
		`INSERT INTO control_documents(id,kind,parent_id,payload,revision,created_at,updated_at) VALUES(?,?,?,?,1,?,?)`,
		id, kind, parent, string(raw), now, now); err != nil {
		t.Fatal(err)
	}
}

func TestCoverageTimelineIntersectsProjectEnvironmentApplicationAndServiceScopes(t *testing.T) {
	server, handler, runtime, pool := managedRuntimeFixture(t)
	now := time.Now().UTC()
	other := runtime
	other.ID, other.Name = "runtime-other", "other"
	other.PoolID = pool.ID
	if err := server.store.CreateRuntime(t.Context(), &other); err != nil {
		t.Fatal(err)
	}
	for _, app := range []struct{ id, runtime string }{{"app-a", runtime.ID}, {"app-b", other.ID}} {
		if _, err := server.store.(*storage.SQLStore).DB().ExecContext(t.Context(),
			`INSERT INTO applications(id,runtime_id,name,project_name,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, app.id, app.runtime, app.id, app.id, model.ApplicationRunning, now, now); err != nil {
			t.Fatal(err)
		}
	}
	coverageTimelineDocument(t, server, "project-a", "project", "", map[string]any{"name": "A"})
	coverageTimelineDocument(t, server, "project-b", "project", "", map[string]any{"name": "B"})
	coverageTimelineDocument(t, server, "env-a", "environment", "project-a", map[string]string{"project_id": "project-a", "runtime_id": runtime.ID, "application_id": "app-a"})
	coverageTimelineDocument(t, server, "env-b", "environment", "project-b", map[string]string{"project_id": "project-b", "runtime_id": other.ID, "application_id": "app-b"})
	coverageTimelineDocument(t, server, "application-environment:app-a", "application-environment", "app-a", map[string]string{"environment_id": "env-a"})
	coverageTimelineDocument(t, server, "application-environment:app-b", "application-environment", "app-b", map[string]string{"environment_id": "env-b"})
	serviceA := logCoverageService(t, server, runtime, "service-a")
	serviceA.ApplicationID = "app-a"
	if _, err := server.store.(*storage.SQLStore).DB().ExecContext(t.Context(), `UPDATE services SET application_id=? WHERE id=?`, serviceA.ApplicationID, serviceA.ID); err != nil {
		t.Fatal(err)
	}
	serviceB := logCoverageService(t, server, other, "service-b")
	serviceB.ApplicationID = "app-b"
	if _, err := server.store.(*storage.SQLStore).DB().ExecContext(t.Context(), `UPDATE services SET application_id=? WHERE id=?`, serviceB.ApplicationID, serviceB.ID); err != nil {
		t.Fatal(err)
	}
	sourceA := model.RuntimeEventSource{ID: "runtime:" + runtime.ID, RuntimeID: runtime.ID}
	sourceB := model.RuntimeEventSource{ID: "runtime:" + other.ID, RuntimeID: other.ID}
	events := []model.DiscoverySignal{
		{ActorType: "container", ExternalID: "container-a", ServiceKey: serviceA.ServiceKey, Kind: "oom", At: now},
		{ActorType: "container", ExternalID: "container-a", ServiceKey: serviceA.ServiceKey, Kind: "health_status: unhealthy", At: now},
	}
	for index := 0; index < 2; index++ {
		if err := server.store.RecordRuntimeEvents(t.Context(), sourceA, events, now); err != nil {
			t.Fatal(err)
		}
	}
	for category, count := range map[string]int{"fault": 1, "health": 1, "lifecycle": 0, "gap": 0, "restart": 0, "completed": 0, "planned": 0} {
		rows, _, _ := coverageTimelineEnvelope(t, managementRequest(t, handler, http.MethodGet, "/timeline?application_id=app-a&category="+category, nil, nil))
		if len(rows) != count {
			t.Fatalf("category %s returned %d records, want %d", category, len(rows), count)
		}
	}
	if err := server.store.RecordRuntimeEvents(t.Context(), sourceB, nil, now); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"project_id=project-a", "environment_id=env-a", "application_id=app-a", "service_id=service-a",
		"project_id=project-a&environment_id=env-a&application_id=app-a&service_id=service-a&runtime_id=" + runtime.ID,
	} {
		rows, _, coverage := coverageTimelineEnvelope(t, managementRequest(t, handler, http.MethodGet, "/timeline?"+query, nil, nil))
		if len(rows) != 2 || coverage.State != "collecting" || len(coverage.Sources) != 1 || coverage.Sources[0].RuntimeID != runtime.ID {
			t.Fatalf("scoped evidence lost or duplicated: %s %#v %#v", query, rows, coverage)
		}
		for _, row := range rows {
			if row.ApplicationID != "app-a" || row.ServiceID != serviceA.ID {
				t.Fatal("timeline leaked another application's event")
			}
		}
	}
	for _, query := range []string{
		"environment_id=missing", "application_id=missing", "service_id=missing",
		"project_id=project-a&environment_id=env-b", "project_id=project-a&application_id=app-b",
		"project_id=project-a&service_id=service-b", "environment_id=env-a&application_id=app-b",
		"environment_id=env-a&service_id=service-b", "application_id=app-a&service_id=service-b",
		"project_id=project-a&runtime_id=" + other.ID,
	} {
		rows, _, coverage := coverageTimelineEnvelope(t, managementRequest(t, handler, http.MethodGet, "/timeline?"+query, nil, nil))
		if len(rows) != 0 || coverage.State != "unavailable" || len(coverage.Sources) != 0 {
			t.Fatalf("contradictory scope exposed collector evidence: %s %#v %#v", query, rows, coverage)
		}
	}
	rows, cursor, _ := coverageTimelineEnvelope(t, managementRequest(t, handler, http.MethodGet, "/timeline?application_id=app-a&limit=1", nil, nil))
	if len(rows) != 1 || cursor == "" {
		t.Fatal("equal-timestamp events were not paginated")
	}
	next, nextCursor, _ := coverageTimelineEnvelope(t, managementRequest(t, handler, http.MethodGet, "/timeline?application_id=app-a&limit=1&cursor="+url.QueryEscape(cursor), nil, nil))
	if len(next) != 1 || next[0].ID == rows[0].ID || nextCursor != "" {
		t.Fatal("equal-timestamp pagination duplicated or skipped event")
	}
}

func TestCoverageTimelineProjectPaginationIncludesLaterEnvironmentRuntime(t *testing.T) {
	server, handler, first, _ := managedRuntimeFixture(t)
	second := first
	second.ID, second.Name = "runtime-last", "last environment"
	if err := server.store.CreateRuntime(t.Context(), &second); err != nil {
		t.Fatal(err)
	}
	coverageTimelineDocument(t, server, "many-environments", "project", "", map[string]string{"name": "large project"})
	tx, err := server.store.(*storage.SQLStore).DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	for index := 0; index < 201; index++ {
		runtimeID := first.ID
		if index == 200 {
			runtimeID = second.ID
		}
		payload, _ := json.Marshal(map[string]string{"project_id": "many-environments", "runtime_id": runtimeID})
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO control_documents(id,kind,parent_id,payload,revision,created_at,updated_at) VALUES(?,'environment','many-environments',?,1,?,?)`, fmt.Sprintf("env-%03d", index), string(payload), now, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, runtime := range []model.Runtime{first, second} {
		if err := server.store.RecordRuntimeEvents(t.Context(), model.RuntimeEventSource{ID: "runtime:" + runtime.ID, RuntimeID: runtime.ID}, nil, now); err != nil {
			t.Fatal(err)
		}
	}
	_, _, coverage := coverageTimelineEnvelope(t, managementRequest(t, handler, http.MethodGet, "/timeline?project_id=many-environments", nil, nil))
	if coverage.State != "collecting" || len(coverage.Sources) != 2 {
		t.Fatalf("project collection omitted environment after first page: %#v", coverage)
	}
}

func TestCoverageTimelineSwarmSourcesDeduplicateAndExposeStaleOrUnconfiguredNodes(t *testing.T) {
	server, handler, runtime, _ := managedRuntimeFixture(t)
	runtime.Type, runtime.ClusterID = model.RuntimeSwarm, "cluster-a"
	coverageRuntimeSnapshot(t, server, runtime)
	second := runtime
	second.ID, second.Name, second.Type = "runtime-second", "same cluster second runtime", model.RuntimeDocker
	if err := server.store.CreateRuntime(t.Context(), &second); err != nil {
		t.Fatal(err)
	}
	second.Type = model.RuntimeSwarm
	coverageRuntimeSnapshot(t, server, second)
	_, client := enginetest.Fixture()
	server.core.Cluster = cluster.NewWithFactory(server.store, server.config, func(model.Runtime, engine.TLSFiles) (engine.MonitoringClient, error) { return client, nil })
	server.config.Controller.Nodes = []config.ControllerNodeConfig{
		{NodeID: "node-a", ClusterID: "cluster-a", EngineID: "engine-local"},
		{NodeID: "node-b", ClusterID: "cluster-a", EngineID: "engine-b"},
		{NodeID: "unrelated-node", ClusterID: "cluster-b"},
	}
	if err := server.core.Cluster.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := server.store.RecordRuntimeEvents(t.Context(), model.RuntimeEventSource{ID: "node:node-a", ClusterID: runtime.ClusterID, NodeID: "node-a", EngineID: "engine-local"}, nil, now); err != nil {
		t.Fatal(err)
	}
	coverageTimelineDocument(t, server, "shared-cluster", "project", "", map[string]string{"name": "shared cluster"})
	for _, selected := range []model.Runtime{runtime, second} {
		coverageTimelineDocument(t, server, "env:"+selected.ID, "environment", "shared-cluster", map[string]string{"runtime_id": selected.ID, "project_id": "shared-cluster"})
	}
	_, _, coverage := coverageTimelineEnvelope(t, managementRequest(t, handler, http.MethodGet, "/timeline?project_id=shared-cluster", nil, nil))
	counts := map[string]int{}
	for _, source := range coverage.Sources {
		counts[source.ID]++
		if source.NodeID == "unrelated-node" {
			t.Fatal("another cluster's configured collector leaked")
		}
	}
	if coverage.State != "partial" || counts["node:node-a"] != 1 || counts["node:node-b"] != 1 {
		t.Fatalf("shared node evidence duplicated or absent: %#v", coverage)
	}
	if err := server.store.RecordRuntimeEvents(t.Context(), model.RuntimeEventSource{ID: "node:node-a", ClusterID: runtime.ClusterID, NodeID: "node-a"}, nil, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, _, coverage = coverageTimelineEnvelope(t, managementRequest(t, handler, http.MethodGet, "/timeline?runtime_id="+runtime.ID, nil, nil))
	foundStale := false
	for _, source := range coverage.Sources {
		if source.NodeID == "node-a" {
			foundStale = source.State == "unavailable" && source.LastError == "heartbeat_missing"
		}
	}
	if coverage.State != "unavailable" || !foundStale {
		t.Fatalf("stale collector still presented as live: %#v", coverage)
	}
	server.config.Controller.RuntimeEvents.Enabled = false
	_, _, coverage = coverageTimelineEnvelope(t, managementRequest(t, handler, http.MethodGet, "/timeline", nil, nil))
	if coverage.State != "unavailable" || len(coverage.Sources) != 0 {
		t.Fatal("disabled events reported collector coverage")
	}
}

func TestCoverageTimelineGlobalCoverageIncludesAllRuntimePagesAndRejectsUnknownClusterIdentity(t *testing.T) {
	server, handler, runtime, _ := managedRuntimeFixture(t)
	db := server.store.(*storage.SQLStore).DB()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for index := 0; index < 200; index++ {
		if _, err := tx.ExecContext(t.Context(),
			`INSERT INTO runtimes(id,pool_id,name,runtime_type,endpoint,enabled,status,created_at,updated_at) SELECT ?,pool_id,?,'docker',endpoint,?,status,created_at,updated_at FROM runtimes WHERE id=?`,
			fmt.Sprintf("rt-page-%03d", index), fmt.Sprintf("runtime-page-%03d", index), index != 0, runtime.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_, _, coverage := coverageTimelineEnvelope(t, managementRequest(t, handler, http.MethodGet, "/timeline", nil, nil))
	foundLast := false
	for _, source := range coverage.Sources {
		if source.RuntimeID == "rt-page-000" {
			t.Fatal("disabled runtime was presented as an expected collector")
		}
		if source.RuntimeID == "rt-page-199" {
			foundLast = true
		}
		if source.State != "unavailable" || source.LastError != "collector_not_started" {
			t.Fatal("unstarted runtime invented current collection evidence")
		}
	}
	if coverage.State != "unavailable" || len(coverage.Sources) != 200 || !foundLast {
		t.Fatalf("global coverage omitted runtime page: %d %#v", len(coverage.Sources), coverage.State)
	}
	runtime.Type, runtime.ClusterID = model.RuntimeSwarm, ""
	coverageRuntimeSnapshot(t, server, runtime)
	_, _, coverage = coverageTimelineEnvelope(t, managementRequest(t, handler, http.MethodGet, "/timeline?runtime_id="+runtime.ID, nil, nil))
	if len(coverage.Sources) != 1 || coverage.Sources[0].LastError != "cluster_identity_missing" || coverage.State != "unavailable" {
		t.Fatal("Swarm without cluster identity was presented as observable")
	}
	runtime.ClusterID = "unconfigured-cluster"
	coverageRuntimeSnapshot(t, server, runtime)
	_, _, coverage = coverageTimelineEnvelope(t, managementRequest(t, handler, http.MethodGet, "/timeline?runtime_id="+runtime.ID, nil, nil))
	foundInventory := false
	for _, source := range coverage.Sources {
		if source.LastError == "node_inventory_unavailable" {
			foundInventory = true
		}
	}
	if coverage.State != "unavailable" || !foundInventory {
		t.Fatal("unknown node inventory hid unavailable evidence")
	}
}
