package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/yernsun/network-service-composer/internal/cluster"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/runtime/engine/enginetest"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestClusterHTTPPaginationAuthAndLabels(t *testing.T) {
	server, handler := newTestServer(t)
	store, cli := enginetest.Fixture()
	cfg := server.config
	cfg.Management.Enabled = true
	server.core.Cluster = cluster.NewWithFactory(store, cfg, func(model.Runtime, engine.TLSFiles) (engine.MonitoringClient, error) { return cli, nil })
	for i := 0; i < 205; i++ {
		cli.Containers = append(cli.Containers, container.Summary{ID: fmt.Sprintf("extra-%03d", i), Names: []string{fmt.Sprintf("/extra-%03d", i)}, State: container.StateExited})
	}
	if err := server.core.Cluster.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	cookie, csrf := browserLogin(t, handler)
	prefix := "/api/v1/runtimes/rt-cluster/cluster"
	get := func(path string) *httptest.ResponseRecorder {
		return browserRequest(handler, "GET", path, "", cookie, "")
	}
	if res := get(prefix); res.Code != 200 || !strings.Contains(res.Body.String(), `"host_metrics_available":false`) {
		t.Fatal(res.Code, res.Body)
	}
	if res := get(prefix + "/nodes?label=zone%3Da"); res.Code != 200 || strings.Contains(res.Body.String(), `"id":"node-b"`) {
		t.Fatal(res.Code, res.Body)
	}
	var body struct {
		Data       []cluster.Instance `json:"data"`
		Pagination struct {
			Next string `json:"next_cursor"`
		} `json:"pagination"`
	}
	cursor := ""
	seen := map[string]bool{}
	firstCursor := ""
	for page := 0; page < 10; page++ {
		res := get(prefix + "/nodes/node-a/instances?workload_id=standalone&limit=50&cursor=" + url.QueryEscape(cursor))
		if res.Code != 200 {
			t.Fatal(res.Body)
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		for _, i := range body.Data {
			if seen[i.ID] {
				t.Fatal("duplicate page item")
			}
			seen[i.ID] = true
		}
		cursor = body.Pagination.Next
		if firstCursor == "" {
			firstCursor = cursor
		}
		if cursor == "" {
			break
		}
	}
	if len(seen) != 206 {
		t.Fatalf("only %d of 206 instances", len(seen))
	}
	for _, path := range []string{prefix + "/nodes?cursor=" + url.QueryEscape(firstCursor), prefix + "/nodes/node-a/instances?cursor=" + url.QueryEscape(firstCursor), prefix + "/nodes?limit=0", prefix + "/nodes?limit=201", prefix + "/nodes?history=nonsense"} {
		if res := get(path); res.Code != 400 {
			t.Fatal(path, res.Code, res.Body)
		}
	}
	if res := get(prefix + "/nodes/missing"); res.Code != 404 {
		t.Fatal(res.Code, res.Body)
	}
	if res := get(prefix + "/metrics?node_id=node-b&container_id=container-b"); res.Code != 200 || !strings.Contains(res.Body.String(), "not_connected") {
		t.Fatal(res.Code, res.Body)
	}
	if res := get(prefix + "/metrics?node_id=node-b&container_id=container-a"); res.Code != 404 {
		t.Fatal("cross-node metrics leaked", res.Code)
	}
	node := get(prefix + "/nodes/node-a")
	if node.Header().Get("ETag") != `"rev-7"` {
		t.Fatal("missing Docker version ETag")
	}
	put := func(body, etag, auth, token string, withCookie bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PUT", prefix+"/nodes/node-a/labels", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if etag != "" {
			req.Header.Set("If-Match", etag)
		}
		if withCookie {
			req.AddCookie(cookie)
		}
		if token != "" {
			req.Header.Set("X-CSRF-Token", token)
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	input := `{"labels":[{"key":"zone","value":"b"},{"key":"empty","value":""}]}`
	if res := put(input, `"rev-7"`, "", "", true); res.Code != 403 {
		t.Fatal("CSRF allowed", res.Code)
	}
	if res := put(input, `"rev-7"`, "Bearer invalid", csrf, true); res.Code != 401 {
		t.Fatal("bearer fallback", res.Code)
	}
	if res := put(input, "", "", csrf, true); res.Code != 428 {
		t.Fatal("missing precondition", res.Code)
	}
	if res := put(`{"labels":[{"key":"x","value":""},{"key":"x","value":""}]}`, `"rev-7"`, "", csrf, true); res.Code != 400 || !strings.Contains(res.Body.String(), `/labels/1/key`) {
		t.Fatal("duplicates", res.Code, res.Body)
	}
	if res := put(input, `"rev-7"`, "", csrf, true); res.Code != 200 || res.Header().Get("ETag") != `"rev-8"` {
		t.Fatal("write", res.Code, res.Body)
	}
	checkActor := func(want string) {
		t.Helper()
		var actor string
		if err := server.store.(*storage.SQLStore).DB().QueryRow(`SELECT actor FROM audit_events ORDER BY created_at DESC LIMIT 1`).Scan(&actor); err != nil || actor != want {
			t.Fatal("audit", actor, err)
		}
	}
	checkActor("user_bootstrap")
	if res := put(input, `"rev-7"`, "", csrf, true); res.Code != 412 {
		t.Fatal("stale version allowed", res.Code)
	}
	if res := put(`{"labels":[]}`, `"rev-8"`, "Bearer test-admin-token", "", false); res.Code != 200 {
		t.Fatal("bearer write", res.Code, res.Body)
	}
	checkActor("user_bootstrap")
	store.Runtimes[0].ManagementEnabled = false
	if res := put(input, `"rev-9"`, "", csrf, true); res.Code != 403 {
		t.Fatal("management boundary", res.Code, res.Body)
	}
	expired, _ := server.signSession(session{Expires: time.Now().Add(-time.Second).Unix(), CSRF: csrf})
	if res := browserRequest(handler, "GET", prefix, "", &http.Cookie{Name: "nsc_session", Value: expired}, ""); res.Code != 401 {
		t.Fatal("expired session", res.Code)
	}
	if res := get("/infrastructure/nodes"); res.Code != 200 || !strings.Contains(res.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatal("SPA route or CSP", res.Code)
	}
}
