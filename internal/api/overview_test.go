package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/yernsun/network-service-composer/internal/cluster"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/runtime/engine/enginetest"
	"net/url"
	"testing"
)

func TestOverviewPaginationAndAuthentication(t *testing.T) {
	server, handler := newTestServer(t)
	store, cli := enginetest.Fixture()
	rt := store.Runtimes[0]
	for i := 0; i < 205; i++ {
		copy := rt
		copy.ID = fmt.Sprintf("rt-%03d", i)
		store.Runtimes = append(store.Runtimes, copy)
	}
	server.core.Cluster = cluster.NewWithFactory(store, server.config, func(model.Runtime, engine.TLSFiles) (engine.MonitoringClient, error) { return cli, nil })
	if err := server.core.Cluster.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	cookie, _ := browserLogin(t, handler)
	res := browserRequest(handler, "GET", "/api/v1/overview", "", cookie, "")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body)
	}
	var body struct {
		Data struct {
			Cluster cluster.NodeSummary `json:"cluster"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Cluster.Nodes != 2 {
		t.Fatal("duplicate runtime nodes counted", res.Body)
	}
	seen := map[string]bool{}
	cursor := ""
	for i := 0; i < 10; i++ {
		res = browserRequest(handler, "GET", "/api/v1/overview/runtimes?limit=50&cursor="+url.QueryEscape(cursor), "", cookie, "")
		if res.Code != 200 {
			t.Fatal(res.Code, res.Body)
		}
		var page struct {
			Data       []cluster.RuntimeSummary `json:"data"`
			Pagination struct {
				Next string `json:"next_cursor"`
			} `json:"pagination"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Data) > 50 {
			t.Fatal("page limit exceeded")
		}
		for _, row := range page.Data {
			if seen[row.ID] {
				t.Fatal("duplicate page row")
			}
			seen[row.ID] = true
		}
		cursor = page.Pagination.Next
		if cursor == "" {
			break
		}
	}
	if len(seen) != 206 {
		t.Fatal("incomplete pagination", len(seen))
	}
	res = browserRequest(handler, "GET", "/api/v1/overview/runtimes?cursor=invalid", "", cookie, "")
	if res.Code != 400 {
		t.Fatal(res.Code)
	}
	res = browserRequest(handler, "GET", "/api/v1/overview", "", nil, "")
	if res.Code != 401 {
		t.Fatal("anonymous overview", res.Code)
	}
}
