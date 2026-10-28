package api

import (
	"context"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/moby/moby/api/types/volume"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/volumemanager"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func volumeHTTPFixture(t *testing.T) (*Server, http.Handler, model.Runtime, *httptest.Server) {
	t.Helper()
	s, h, rt, _ := managedRuntimeFixture(t)
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		if strings.HasPrefix(path, "/v") {
			path = path[strings.Index(path[1:], "/")+1:]
		}
		var data any
		switch path {
		case "/_ping":
			w.Header().Set("API-Version", "1.52")
			w.Write([]byte("OK"))
			return
		case "/info":
			data = map[string]string{"ID": "engine", "Name": "fixture"}
		case "/containers/json":
			data = []any{}
		case "/volumes":
			data = map[string]any{"Volumes": []volume.Volume{{Name: "data", Driver: "local", Scope: "local", CreatedAt: "2026-10-01T00:00:00Z"}, {Name: "other", Driver: "local", Scope: "local", CreatedAt: "2026-10-01T00:00:00Z"}}}
		case "/volumes/data":
			data = volume.Volume{Name: "data", Driver: "local", Scope: "local", CreatedAt: "2026-10-01T00:00:00Z"}
		default:
			w.WriteHeader(404)
			data = map[string]string{"message": "not found"}
		}
		json.NewEncoder(w).Encode(data)
	}))
	t.Cleanup(engine.Close)
	rt.Endpoint = engine.URL
	if err := s.store.UpdateRuntime(t.Context(), &rt, rt.Revision); err != nil {
		t.Fatal(err)
	}
	return s, h, rt, engine
}
func TestVolumeHTTPAsyncCreateReplayAndEncryptedTarget(t *testing.T) {
	s, h, rt, engine := volumeHTTPFixture(t)
	path := "/runtimes/" + rt.ID + "/volumes"
	headers := map[string]string{"Idempotency-Key": "anonymous-create"}
	first := responseData[model.DeploymentOperation](t, managementRequest(t, h, "POST", path, map[string]string{"name": ""}, headers), 202)
	if first.Type != "VOLUME_CREATE" || first.Status != model.OperationQueued {
		t.Fatalf("not async: %+v", first)
	}
	op, err := s.store.GetDeploymentOperation(t.Context(), first.ID)
	if err != nil || len(op.EncryptedInput) == 0 {
		t.Fatalf("not encrypted: %v", err)
	}
	engine.Close()
	again := responseData[model.DeploymentOperation](t, managementRequest(t, h, "POST", path, map[string]string{"name": ""}, headers), 202)
	if again.ID != first.ID {
		t.Fatal("offline retry duplicated operation")
	}
	if got := managementRequest(t, h, "POST", path, map[string]string{"name": "other"}, headers); got.Code != 409 {
		t.Fatalf("changed request: %d %s", got.Code, got.Body.String())
	}
}
func TestVolumeHTTPListDetailAndDeleteContract(t *testing.T) {
	_, h, rt, _ := volumeHTTPFixture(t)
	path := "/runtimes/" + rt.ID + "/volumes"
	list := managementRequest(t, h, "GET", path+"?limit=1", nil, nil)
	var body struct {
		Data       []volumemanager.Item `json:"data"`
		Complete   bool                 `json:"complete"`
		Pagination struct {
			Next string `json:"next_cursor"`
		} `json:"pagination"`
	}
	if json.Unmarshal(list.Body.Bytes(), &body) != nil || list.Code != 200 || len(body.Data) != 1 || !body.Complete || body.Pagination.Next != "data" {
		t.Fatalf("list=%d %s", list.Code, list.Body.String())
	}
	if got := managementRequest(t, h, "GET", path+"?kind=bad", nil, nil); got.Code != 400 {
		t.Fatalf("invalid kind=%d", got.Code)
	}
	detail := managementRequest(t, h, "GET", path+"/data", nil, nil)
	v := responseData[volumemanager.Item](t, detail, 200)
	if detail.Header().Get("ETag") != `"volume-`+v.Version+`"` {
		t.Fatal("missing fingerprint")
	}
	input := map[string]bool{"acknowledge_data_loss": true}
	headers := map[string]string{"Idempotency-Key": "delete"}
	if got := managementRequest(t, h, "DELETE", path+"/data", input, headers); got.Code != 428 {
		t.Fatalf("missing etag=%d", got.Code)
	}
	headers["If-Match"] = `"bad"`
	if got := managementRequest(t, h, "DELETE", path+"/data", input, headers); got.Code != 400 {
		t.Fatalf("bad etag=%d", got.Code)
	}
	headers["If-Match"] = `"volume-` + strings.Repeat("0", 64) + `"`
	if got := managementRequest(t, h, "DELETE", path+"/data", input, headers); got.Code != 412 {
		t.Fatalf("stale etag=%d", got.Code)
	}
	headers["If-Match"] = detail.Header().Get("ETag")
	op := responseData[model.DeploymentOperation](t, managementRequest(t, h, "DELETE", path+"/data", input, headers), 202)
	if op.Type != "VOLUME_DELETE" {
		t.Fatal("incorrect delete operation")
	}
	again := responseData[model.DeploymentOperation](t, managementRequest(t, h, "DELETE", path+"/data", input, headers), 202)
	if op.ID != again.ID {
		t.Fatal("duplicate delete")
	}
	if v.Version != nodecontrol.VolumeMetadata(volume.Volume{Name: "data", Driver: "local", Scope: "local", CreatedAt: "2026-10-01T00:00:00Z"}).Version {
		t.Fatal("wrong metadata version")
	}
}
func TestVolumeHTTPRejectsInvalidMutationInputs(t *testing.T) {
	_, h, rt, _ := volumeHTTPFixture(t)
	path := "/runtimes/" + rt.ID + "/volumes"
	for _, tc := range []struct {
		method, path string
		body         any
		headers      map[string]string
		code         int
	}{
		{"POST", path, map[string]string{"name": ""}, nil, 400},
		{"POST", path, map[string]string{"name": "invalid/name"}, map[string]string{"Idempotency-Key": "key"}, 422},
		{"DELETE", path + "/data", map[string]bool{"acknowledge_data_loss": false}, map[string]string{"Idempotency-Key": "key"}, 428},
		{"GET", path + "/missing", nil, nil, 404},
	} {
		got := managementRequest(t, h, tc.method, tc.path, tc.body, tc.headers)
		if got.Code != tc.code {
			t.Errorf("%s %s got=%d want=%d: %s", tc.method, tc.path, got.Code, tc.code, got.Body.String())
		}
	}
}

func TestVolumeHTTPFiltersAndRuntimeAuthorization(t *testing.T) {
	s, h, rt, _ := volumeHTTPFixture(t)
	path := "/runtimes/" + rt.ID + "/volumes"
	for _, tc := range []struct {
		query string
		count int
		code  int
	}{
		{"?search=DATA", 1, 200}, {"?search=missing", 0, 200}, {"?kind=unknown", 2, 200}, {"?kind=named", 0, 200}, {"?kind=anonymous", 0, 200}, {"?usage=unused", 2, 200}, {"?usage=used", 0, 200}, {"?usage=bad", 0, 400}, {"?cursor=data", 1, 200}, {"?limit=201", 2, 200},
	} {
		response := managementRequest(t, h, "GET", path+tc.query, nil, nil)
		if response.Code != tc.code {
			t.Fatalf("filter %s=%d", tc.query, response.Code)
		}
		if tc.code == 200 {
			rows := responseData[[]volumemanager.Item](t, response, 200)
			if len(rows) != tc.count {
				t.Fatalf("filter %s produced %d", tc.query, len(rows))
			}
		}
	}
	if got := managementRequest(t, h, "GET", "/runtimes/missing/volumes", nil, nil); got.Code != 404 {
		t.Fatalf("unknown runtime=%d", got.Code)
	}
	router := chi.NewRouter()
	router.Get("/runtimes/{id}/volumes", s.listVolumes)
	for _, role := range []string{"", "viewer", "operator"} {
		r := httptest.NewRequest("GET", path, nil)
		if role != "" {
			r = r.WithContext(context.WithValue(r.Context(), identityContextKey{}, core.User{Role: role, Enabled: true}))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("role %q read volumes: %d", role, w.Code)
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*model.Runtime)
		query  string
		code   int
	}{
		{"disabled", func(r *model.Runtime) { r.Enabled = false }, "", 422},
		{"unsupported", func(r *model.Runtime) { r.Type = "agent" }, "", 422},
		{"Swarm needs node", func(r *model.Runtime) { r.Type = model.RuntimeSwarm; r.ClusterID = "cluster" }, "", 422},
		{"Swarm rejects wrong node", func(r *model.Runtime) { r.Type = model.RuntimeSwarm; r.ClusterID = "cluster" }, "?node_id=wrong", 503},
	} {
		copy := rt
		tc.change(&copy)
		coverageRuntimeSnapshot(t, s, copy)
		response := managementRequest(t, h, "GET", path+tc.query, nil, nil)
		if response.Code != tc.code {
			t.Fatalf("%s=%d %s", tc.name, response.Code, response.Body.String())
		}
	}
}
func TestVolumeHTTPManagementPreconditionsAndSafeErrors(t *testing.T) {
	s, h, rt, _ := volumeHTTPFixture(t)
	path := "/runtimes/" + rt.ID + "/volumes"
	for _, tc := range []struct {
		method, url, body string
		headers           map[string]string
		code              int
	}{
		{"POST", path, `{"name":"data","driver":"nfs"}`, map[string]string{"Idempotency-Key": "unknown"}, 400},
		{"POST", path, `{`, map[string]string{"Idempotency-Key": "bad-json"}, 400},
		{"DELETE", path + "/data", `{`, nil, 400},
		{"DELETE", path + "/data", `{"acknowledge_data_loss":true}`, map[string]string{"Idempotency-Key": "bad-fingerprint", "If-Match": `"volume-` + strings.Repeat("x", 64) + `"`}, 400},
	} {
		if response := managementRequest(t, h, tc.method, tc.url, tc.body, tc.headers); response.Code != tc.code {
			t.Fatalf("bad input=%d %s", response.Code, response.Body.String())
		}
	}
	s.config.Management.Enabled = false
	if got := managementRequest(t, h, "POST", path, map[string]string{"name": "data"}, map[string]string{"Idempotency-Key": "disabled"}); got.Code != 403 {
		t.Fatalf("global management=%d", got.Code)
	}
	s.config.Management.Enabled = true
	rt.ManagementEnabled = false
	coverageRuntimeSnapshot(t, s, rt)
	if got := managementRequest(t, h, "DELETE", path+"/data", map[string]bool{"acknowledge_data_loss": true}, nil); got.Code != 403 {
		t.Fatalf("runtime management=%d", got.Code)
	}
	for _, tc := range []struct {
		err     error
		code    int
		problem string
	}{{volumemanager.ErrBlocked, 409, "volume.in_use"}, {volumemanager.ErrIdentity, 503, "system.not_ready"}} {
		w := httptest.NewRecorder()
		s.volumeError(w, httptest.NewRequest("GET", path, nil), tc.err)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.problem) {
			t.Fatalf("unsafe problem mapping=%d %s", w.Code, w.Body.String())
		}
	}
}
