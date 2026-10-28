package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestControlHTTPPreconditionsAndMalformedBodiesPreserveDrafts(t *testing.T) {
	s, h, rt, pool := managedRuntimeFixture(t)
	ctx := context.Background()
	project, e := s.core.CreateProject(ctx, "Control", "control")
	if e != nil {
		t.Fatal(e)
	}
	env, e := s.core.CreateEnvironment(ctx, project.ID, core.EnvironmentInput{Name: "dev", RuntimeID: rt.ID})
	if e != nil {
		t.Fatal(e)
	}
	base := "/gateway-pools/" + pool.ID + "/nginx"
	environment := "/environments/" + env.ID
	for _, tc := range []struct {
		method, path string
		body         bool
	}{
		{"PUT", "/projects/" + project.ID + "/variables", true}, {"PUT", environment + "/variables", true}, {"POST", environment + "/deployments", true}, {"PUT", environment + "/update-policy", true}, {"POST", environment + "/image-updates/missing", true}, {"POST", environment + "/webhook", false},
		{"PUT", base + "/draft", true}, {"PUT", base + "/sandbox", true}, {"POST", base + "/validations", false}, {"POST", base + "/validations/missing/cancel", false}, {"POST", base + "/releases", true}, {"POST", base + "/releases/missing/verify", false}, {"POST", base + "/releases/missing/stop", false}, {"POST", base + "/versions/missing/restore", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := managementRequest(t, h, tc.method, tc.path, map[string]any{}, nil)
			if w.Code != 428 {
				t.Fatalf("missing precondition=%d %s", w.Code, w.Body)
			}
			if tc.body {
				req := httptest.NewRequest(tc.method, "/api/v1"+tc.path, strings.NewReader("{"))
				req.Header.Set("Authorization", "Bearer test-admin-token")
				req.Header.Set("If-Match", `"rev-0"`)
				req.Header.Set("Content-Type", "application/json")
				w = httptest.NewRecorder()
				h.ServeHTTP(w, req)
				if w.Code != 400 {
					t.Fatalf("invalid body=%d %s", w.Code, w.Body)
				}
			}
		})
	}
	for _, path := range []string{"/projects", "/projects/" + project.ID + "/environments", environment + "/preflight", base + "/preview"} {
		req := httptest.NewRequest("POST", "/api/v1"+path, strings.NewReader("{"))
		req.Header.Set("Authorization", "Bearer test-admin-token")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatalf("invalid JSON %s=%d %s", path, w.Code, w.Body)
		}
	}
	current, e := s.store.GetPool(ctx, pool.ID)
	if e != nil || current.DesiredRevision != pool.DesiredRevision {
		t.Fatal("rejected control request changed production target", e)
	}
	draft, e := s.core.NginxConfiguration(ctx, pool.ID, "nginx-draft")
	if e != nil || draft.Revision != 0 {
		t.Fatal("rejected request created draft", e)
	}
}

func TestControlHTTPResourcesAndImageQueueKeepManagementExplicit(t *testing.T) {
	s, h, rt, pool := managedRuntimeFixture(t)
	project := responseData[core.Project](t, managementRequest(t, h, "POST", "/projects", map[string]string{"name": "HTTP project", "slug": "http-project"}, nil), 201)
	env := responseData[core.Environment](t, managementRequest(t, h, "POST", "/projects/"+project.ID+"/environments", core.EnvironmentInput{Name: "dev", RuntimeID: rt.ID}, nil), 201)
	base := "/environments/" + env.ID
	nginx := "/gateway-pools/" + pool.ID + "/nginx"
	for _, path := range []string{"/projects", "/projects/" + project.ID, "/projects/" + project.ID + "/environments", base, base + "/variables", base + "/effective-variables", base + "/update-policy", base + "/image-checks", base + "/image-updates", base + "/webhook", nginx, nginx + "/draft", nginx + "/draft-versions", nginx + "/sandbox", nginx + "/versions", nginx + "/preview", nginx + "/preview?source=draft"} {
		w := managementRequest(t, h, "GET", path, nil, nil)
		if w.Code != 200 {
			t.Fatalf("GET %s=%d %s", path, w.Code, w.Body)
		}
	}
	match := map[string]string{"If-Match": `"rev-0"`}
	p := responseData[core.ImagePolicy](t, managementRequest(t, h, "PUT", base+"/update-policy", core.ImagePolicy{Enabled: false, IntervalSeconds: 60}, match), 200)
	if p.Enabled || p.IntervalSeconds != 60 {
		t.Fatal("disabled policy changed")
	}
	check := responseData[core.ImageCheck](t, managementRequest(t, h, "POST", base+"/image-checks", nil, nil), 202)
	if !check.Requested || check.LastSuccess != nil {
		t.Fatal("queued check reported as verified")
	}
	hook := responseData[struct {
		Hook   core.Hook `json:"hook"`
		Secret string    `json:"secret"`
	}](t, managementRequest(t, h, "POST", base+"/webhook", nil, match), 200)
	if hook.Secret == "" || hook.Hook.HookID == "" {
		t.Fatal("webhook rotation omitted one-time secret")
	}
	w := managementRequest(t, h, "GET", base+"/webhook", nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), hook.Secret) {
		t.Fatal("webhook read exposed secret")
	}
	for _, path := range []string{"/projects/missing/environments", "/environments/missing/image-updates", nginx + "/versions/missing/restore", nginx + "/releases/missing/verify", nginx + "/releases/missing/stop", nginx + "/validations/missing/cancel"} {
		method := "GET"
		if strings.HasSuffix(path, "restore") || strings.HasSuffix(path, "verify") || strings.HasSuffix(path, "stop") || strings.HasSuffix(path, "cancel") {
			method = "POST"
		}
		w := managementRequest(t, h, method, path, nil, map[string]string{"If-Match": `"rev-1"`})
		if w.Code != 404 {
			t.Fatalf("missing resource %s=%d %s", path, w.Code, w.Body)
		}
	}
	queued, e := s.store.ListDeploymentOperations(context.Background(), env.ApplicationID, storage.Page{Limit: 10})
	if e != nil || len(queued) != 0 {
		t.Fatal("policy configuration deployed application", e)
	}
}

func TestNginxDraftHistoryHTTPReturnsSavedSnapshots(t *testing.T) {
	s, h, _, pool := managedRuntimeFixture(t)
	ctx := context.Background()
	first, err := s.core.SaveNginxDraft(ctx, pool.ID, core.NginxConfiguration{Snippets: []model.ConfigSnippet{{Scope: "http", Content: "gzip on;"}}}, "alice", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.core.SaveNginxDraft(ctx, pool.ID, core.NginxConfiguration{Snippets: []model.ConfigSnippet{{Scope: "http", Content: "gzip off;"}}}, "bob", first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	path := "/gateway-pools/" + pool.ID + "/nginx/draft-versions"
	rows := responseData[[]core.NginxConfiguration](t, managementRequest(t, h, "GET", path, nil, nil), 200)
	if len(rows) != 2 || rows[0].Actor != "alice" || rows[1].Actor != "bob" || rows[0].Snippets[0].Content != "gzip on;" || rows[1].Snippets[0].Content != "gzip off;" {
		t.Fatalf("saved revisions not listed: %+v", rows)
	}
	page := managementRequest(t, h, "GET", path+"?limit=1", nil, nil)
	if page.Code != 200 {
		t.Fatalf("paged history=%d %s", page.Code, page.Body)
	}
	var result struct {
		Data       []core.NginxConfiguration `json:"data"`
		Pagination struct {
			NextCursor string `json:"next_cursor"`
		} `json:"pagination"`
	}
	if err = json.Unmarshal(page.Body.Bytes(), &result); err != nil || len(result.Data) != 1 || result.Pagination.NextCursor == "" {
		t.Fatalf("draft history pagination: %+v %v", result, err)
	}
	next := responseData[[]core.NginxConfiguration](t, managementRequest(t, h, "GET", path+"?limit=1&cursor="+result.Pagination.NextCursor, nil, nil), 200)
	if len(next) != 1 || next[0].ID == result.Data[0].ID {
		t.Fatalf("draft history cursor repeated: %+v", next)
	}
}

type controlHTTPReadStore struct {
	storage.Store
	kind, key string
	failure   error
}

func (s *controlHTTPReadStore) ListControlDocuments(ctx context.Context, kind, parent string, p storage.Page) ([]storage.ControlDocument, error) {
	if kind == s.kind {
		return nil, s.failure
	}
	return s.Store.ListControlDocuments(ctx, kind, parent, p)
}
func (s *controlHTTPReadStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if s.key != "" && strings.HasSuffix(key, s.key) {
		return storage.ControlDocument{}, s.failure
	}
	return s.Store.GetControlDocument(ctx, key)
}
func TestControlHTTPReadFailuresNeverLookLikeEmptyState(t *testing.T) {
	s, _, rt, pool := managedRuntimeFixture(t)
	ctx := context.Background()
	project, e := s.core.CreateProject(ctx, "Read failures", "reads")
	if e != nil {
		t.Fatal(e)
	}
	env, e := s.core.CreateEnvironment(ctx, project.ID, core.EnvironmentInput{Name: "dev", RuntimeID: rt.ID})
	if e != nil {
		t.Fatal(e)
	}
	baseStore := s.store
	for _, tc := range []struct{ path, kind, key string }{
		{"/projects", "project", ""}, {"/projects/" + project.ID + "/environments", "environment", ""}, {"/environments/" + env.ID + "/image-updates", "image-update", ""}, {"/gateway-pools/" + pool.ID + "/nginx/versions", "nginx-version", ""}, {"/gateway-pools/" + pool.ID + "/nginx/draft-versions", "nginx-draft-version", ""}, {"/gateway-pools/" + pool.ID + "/nginx", "", ":nginx-active"}, {"/gateway-pools/" + pool.ID + "/nginx", "", ":nginx-draft"}, {"/gateway-pools/" + pool.ID + "/nginx/preview?source=draft", "", ":nginx-draft"},
	} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			wrapped := &controlHTTPReadStore{Store: baseStore, kind: tc.kind, key: tc.key, failure: errors.New("control store unavailable")}
			s.store = wrapped
			s.core = core.New(wrapped, s.config, nil)
			r := chi.NewRouter()
			s.registerEnvironments(r)
			s.registerNginx(r)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != 500 {
				t.Fatalf("unavailable control state=%d %s", w.Code, w.Body)
			}
		})
	}
}
