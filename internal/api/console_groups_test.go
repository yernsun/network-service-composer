package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/yernsun/network-service-composer/internal/storage"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestConsoleGroupAndInstanceHTTPContract(t *testing.T) {
	server, handler := newTestServer(t)
	ctx := context.Background()
	pool, err := server.core.CreatePool(ctx, core.CreatePoolInput{Name: "group-api"})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := server.core.CreateRuntime(ctx, core.CreateRuntimeInput{PoolID: pool.ID, Name: "group-runtime", RuntimeType: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	v := model.Service{ID: "svc_group", RuntimeID: runtime.ID, ServiceKey: "project/web", ExternalID: "project/web", Name: "web", Namespace: "project", QualifiedName: "project_web", DNSName: "project_web", Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = server.store.UpsertService(ctx, &v); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ep_a", "ep_b"} {
		ep := model.Endpoint{ID: id, ServiceID: v.ID, ExternalID: id, FirstSeenAt: now, LastSeenAt: now, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err = server.store.UpsertEndpoint(ctx, &ep); err != nil {
			t.Fatal(err)
		}
	}
	cookie, _ := browserLogin(t, handler)
	get := func(path string) *httptest.ResponseRecorder {
		return browserRequest(handler, "GET", path, "", cookie, "")
	}
	for _, path := range []string{"/api/v1/service-groups?resource=bad", "/api/v1/service-groups?cursor=!", "/api/v1/services?group_key=!", "/api/v1/routes?group_key=!"} {
		if r := get(path); r.Code != 400 {
			t.Fatalf("%s: %d %s", path, r.Code, r.Body)
		}
	}
	r := get("/api/v1/service-groups?resource=services")
	if r.Code != 200 {
		t.Fatal(r.Body)
	}
	var groups struct {
		Data []struct {
			Key   string `json:"key"`
			Count int    `json:"count"`
		}
	}
	if err = json.Unmarshal(r.Body.Bytes(), &groups); err != nil {
		t.Fatal(err)
	}
	if len(groups.Data) != 1 || groups.Data[0].Count != 1 {
		t.Fatal(r.Body)
	}
	if r = get("/api/v1/services?group_key=" + groups.Data[0].Key); r.Code != 200 {
		t.Fatal(r.Body)
	}
	for _, tc := range []struct {
		query     string
		count     int
		paginated bool
	}{{"", 2, false}, {"?limit=1", 1, true}, {"?limit=1&cursor=ep_a", 1, true}} {
		r = get("/api/v1/services/svc_group/instances" + tc.query)
		var body struct {
			Data       []model.Endpoint
			Pagination *json.RawMessage
		}
		if err = json.Unmarshal(r.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if r.Code != 200 || len(body.Data) != tc.count || (body.Pagination != nil) != tc.paginated {
			t.Fatal(r.Body)
		}
	}
	if r = get("/api/v1/services/missing/instances?limit=1"); r.Code != 404 {
		t.Fatal(r.Body)
	}
}

func TestServiceHistoryFilteringBeforePagination(t *testing.T) {
	server, handler := newTestServer(t)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	pool, err := server.core.CreatePool(ctx, core.CreatePoolInput{Name: "history"})
	must(err)
	rt, err := server.core.CreateRuntime(ctx, core.CreateRuntimeInput{PoolID: pool.ID, Name: "history", RuntimeType: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock"})
	must(err)
	now := time.Now().UTC()
	for i := 0; i < 211; i++ {
		status := model.ServiceRemoved
		if i%2 == 0 {
			status = model.ServiceUnavailable
		}
		key := fmt.Sprintf("svc_history_%03d", i)
		v := model.Service{ID: key, RuntimeID: rt.ID, ServiceKey: key, ExternalID: key, Name: key, Namespace: "history", Status: status, Revision: 1, CreatedAt: now, UpdatedAt: now}
		must(server.store.UpsertService(ctx, &v))
	}
	cookie, _ := browserLogin(t, handler)
	for _, tc := range []struct {
		query string
		count int
	}{{"", 106}, {"&include_removed=false", 106}, {"&include_removed=true", 211}, {"&status=REMOVED", 105}, {"&status=REMOVED&include_removed=false", 105}} {
		cursor := ""
		seen := map[string]bool{}
		for {
			r := browserRequest(handler, "GET", "/api/v1/services?limit=50&cursor="+cursor+tc.query, "", cookie, "")
			if r.Code != 200 {
				t.Fatal(r.Body)
			}
			var body struct{ Data []model.Service }
			must(json.Unmarshal(r.Body.Bytes(), &body))
			for _, v := range body.Data {
				if seen[v.ID] {
					t.Fatal("duplicate", v.ID)
				}
				seen[v.ID] = true
			}
			if len(body.Data) < 50 {
				break
			}
			cursor = body.Data[len(body.Data)-1].ID
		}
		if len(seen) != tc.count {
			t.Fatalf("%s: got %d want %d", tc.query, len(seen), tc.count)
		}
	}
	for _, query := range []string{"", "&include_removed=true"} {
		r := browserRequest(handler, "GET", "/api/v1/service-groups?resource=services"+query, "", cookie, "")
		var body struct{ Data []struct{ Count int } }
		must(json.Unmarshal(r.Body.Bytes(), &body))
		if r.Code != 200 || len(body.Data) != 1 || body.Data[0].Count != 106 {
			t.Fatal(r.Body)
		}
	}
	r := browserRequest(handler, "GET", "/api/v1/services?include_removed=maybe", "", cookie, "")
	if r.Code != 400 {
		t.Fatal(r.Body)
	}
	// Internal ownership and recovery queries still see the complete inventory.
	all, err := server.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{RuntimeID: rt.ID})
	must(err)
	if len(all) != 211 {
		t.Fatal(len(all))
	}
}
