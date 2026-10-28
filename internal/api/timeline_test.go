package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestTimelinePaginatesExistingOperationsAndRequiresAuthentication(t *testing.T) {
	server, handler, runtime, _ := managedRuntimeFixture(t)
	now := time.Now().UTC()
	for index, name := range []string{"op-a", "op-b", "op-c"} {
		at := now.Add(time.Duration(index) * time.Second)
		operation := model.DeploymentOperation{ID: name, RuntimeID: runtime.ID, Type: "SCALE", Status: model.OperationSucceeded,
			Metadata: model.Metadata{Revision: 1, CreatedAt: at, UpdatedAt: at}}
		if _, err := server.store.CreateDeploymentOperation(t.Context(), &operation); err != nil {
			t.Fatal(err)
		}
	}
	decode := func(response *httptest.ResponseRecorder) (rows []model.TimelineRecord, next string) {
		t.Helper()
		if response.Code != 200 {
			t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
		}
		var body struct {
			Data       []model.TimelineRecord `json:"data"`
			Pagination struct {
				NextCursor string `json:"next_cursor"`
			} `json:"pagination"`
			Coverage struct {
				State string `json:"state"`
			} `json:"coverage"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Coverage.State != "unavailable" {
			t.Fatalf("unstarted collection presented as healthy: %#v", body.Coverage)
		}
		return body.Data, body.Pagination.NextCursor
	}
	rows, next := decode(managementRequest(t, handler, http.MethodGet, "/timeline?limit=2", nil, nil))
	if len(rows) != 2 || rows[0].OperationID != "op-c" || rows[1].OperationID != "op-b" || next == "" {
		t.Fatalf("first page: %#v %q", rows, next)
	}
	rows, next = decode(managementRequest(t, handler, http.MethodGet, "/timeline?limit=2&cursor="+url.QueryEscape(next), nil, nil))
	if len(rows) != 1 || rows[0].OperationID != "op-a" || next != "" {
		t.Fatalf("second page: %#v %q", rows, next)
	}
	rows, _ = decode(managementRequest(t, handler, http.MethodGet, "/timeline?project_id=other", nil, nil))
	if len(rows) != 0 {
		t.Fatalf("unknown-scope records leaked into a project filter: %#v", rows)
	}
	if got := managementRequest(t, handler, http.MethodGet, "/timeline?cursor=bad", nil, nil); got.Code != 400 {
		t.Fatalf("invalid cursor returned %d", got.Code)
	}
	unauthorized := httptest.NewRequest(http.MethodGet, "/api/v1/timeline", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, unauthorized)
	if w.Code != 401 {
		t.Fatalf("unauthenticated timeline returned %d", w.Code)
	}
}

func TestTimelineCoverageIncludesUnstartedRuntimeInGlobalView(t *testing.T) {
	server, handler, runtime, pool := managedRuntimeFixture(t)
	ctx := t.Context()
	if err := server.store.RecordRuntimeEvents(ctx, model.RuntimeEventSource{ID: "runtime:" + runtime.ID, RuntimeID: runtime.ID}, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	second := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "unstarted", Type: model.RuntimeDocker,
		Endpoint: "unix:///nonexistent/unstarted.sock", Enabled: true, Status: model.RuntimeOnline,
		Metadata: model.Metadata{Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}}
	if err := server.store.CreateRuntime(ctx, &second); err != nil {
		t.Fatal(err)
	}
	coverage := func(path string) struct {
		State   string                     `json:"state"`
		Sources []model.RuntimeEventSource `json:"sources"`
	} {
		t.Helper()
		response := managementRequest(t, handler, http.MethodGet, path, nil, nil)
		if response.Code != 200 {
			t.Fatalf("timeline returned HTTP %d: %s", response.Code, response.Body.String())
		}
		var body struct {
			Coverage struct {
				State   string                     `json:"state"`
				Sources []model.RuntimeEventSource `json:"sources"`
			} `json:"coverage"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Coverage
	}
	if selected := coverage("/timeline?runtime_id=" + runtime.ID); selected.State != "collecting" || len(selected.Sources) != 1 {
		t.Fatalf("active runtime coverage: %#v", selected)
	}
	global := coverage("/timeline")
	if global.State != "partial" || len(global.Sources) != 2 {
		t.Fatalf("global coverage omitted the unstarted runtime: %#v", global)
	}
	third := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "other-project", Type: model.RuntimeDocker,
		Endpoint: "unix:///nonexistent/other.sock", Enabled: true, Status: model.RuntimeOnline,
		Metadata: model.Metadata{Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}}
	if err := server.store.CreateRuntime(ctx, &third); err != nil {
		t.Fatal(err)
	}
	projectID := "project-with-two-runtimes"
	now := time.Now().UTC()
	for _, document := range []struct{ id, kind, parent, payload string }{
		{projectID, "project", "", `{}`},
		{"environment-one", "environment", projectID, `{"runtime_id":"` + runtime.ID + `"}`},
		{"environment-two", "environment", projectID, `{"runtime_id":"` + second.ID + `"}`},
	} {
		if _, err := server.store.(*storage.SQLStore).DB().ExecContext(ctx,
			`INSERT INTO control_documents(id,kind,parent_id,payload,revision,created_at,updated_at) VALUES(?,?,?,?,1,?,?)`,
			document.id, document.kind, document.parent, document.payload, now, now); err != nil {
			t.Fatal(err)
		}
	}
	project := coverage("/timeline?project_id=" + projectID)
	if project.State != "partial" || len(project.Sources) != 2 {
		t.Fatalf("project coverage included unrelated runtime: %#v", project)
	}
}

func TestTimelineShowsRuntimeGapForEnvironmentWithoutRuntimeQuery(t *testing.T) {
	server, handler, runtime, _ := managedRuntimeFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	projectID, environmentID := "project-demoapp", "environment-prod"
	for _, document := range []struct{ id, kind, parent, payload string }{
		{projectID, "project", "", `{}`},
		{environmentID, "environment", projectID, `{"project_id":"project-demoapp","name":"prod","runtime_id":"` + runtime.ID + `"}`},
	} {
		if _, err := server.store.(*storage.SQLStore).DB().ExecContext(ctx,
			`INSERT INTO control_documents(id,kind,parent_id,payload,revision,created_at,updated_at) VALUES(?,?,?,?,1,?,?)`,
			document.id, document.kind, document.parent, document.payload, now, now); err != nil {
			t.Fatal(err)
		}
	}
	source := model.RuntimeEventSource{ID: "runtime:" + runtime.ID, RuntimeID: runtime.ID}
	if err := server.store.RecordRuntimeEvents(ctx, source, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := server.store.MarkRuntimeEventSource(ctx, source, "stream_disconnected", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := server.store.RecordRuntimeEvents(ctx, source, nil, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	response := managementRequest(t, handler, http.MethodGet,
		"/timeline?project_id="+projectID+"&environment_id="+environmentID+"&category=gap", nil, nil)
	if response.Code != 200 {
		t.Fatalf("scoped gap timeline returned %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Data []model.TimelineRecord `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || len(body.Data) != 1 || body.Data[0].Source != "gap" {
		t.Fatalf("environment coverage gap missing: %#v %v", body, err)
	}
	foreign := managementRequest(t, handler, http.MethodGet,
		"/timeline?project_id=another-project&environment_id="+environmentID+"&category=gap", nil, nil)
	if foreign.Code != 200 {
		t.Fatalf("mismatched scope returned %d: %s", foreign.Code, foreign.Body.String())
	}
	if err := json.Unmarshal(foreign.Body.Bytes(), &body); err != nil || len(body.Data) != 0 {
		t.Fatalf("mismatched project leaked a Runtime gap: %#v %v", body, err)
	}
}
