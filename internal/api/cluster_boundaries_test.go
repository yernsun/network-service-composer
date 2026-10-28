package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/moby/moby/api/types/container"
	"github.com/yernsun/network-service-composer/internal/cluster"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/runtime/engine/enginetest"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func clusterBoundaryHTTP(t *testing.T) (*Server, http.Handler, *enginetest.Store, *enginetest.Client) {
	t.Helper()
	s, h := newTestServer(t)
	store, client := enginetest.Fixture()
	cfg := s.config
	cfg.Management.Enabled = true
	s.core.Cluster = cluster.NewWithFactory(store, cfg, func(model.Runtime, engine.TLSFiles) (engine.MonitoringClient, error) { return client, nil })
	if e := s.core.Cluster.Refresh(context.Background()); e != nil {
		t.Fatal(e)
	}
	return s, h, store, client
}
func clusterBoundaryRequest(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/v1/runtimes/rt-cluster/cluster"+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer test-admin-token")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("If-Match", `"rev-7"`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestClusterHTTPFiltersAndInvalidEvidence(t *testing.T) {
	s, h, store, client := clusterBoundaryHTTP(t)
	for _, item := range []struct {
		query string
		count int
		id    string
	}{
		{"", 3, ""}, {"?node_id=node-a", 1, "node-a"}, {"?node_id=missing", 0, ""}, {"?search=NODE-B", 1, "node-b"}, {"?search=absent", 0, ""}, {"?state=ready", 2, ""}, {"?state=down", 0, ""}, {"?label=zone", 1, "node-a"}, {"?label=missing", 0, ""}, {"?label=zone%3Dx", 0, ""}, {"?label=zone%3Da", 1, "node-a"}, {"?history=true", 3, ""},
	} {
		t.Run(item.query, func(t *testing.T) {
			w := clusterBoundaryRequest(h, "GET", "/nodes"+item.query, "")
			v := responseData[[]cluster.Node](t, w, 200)
			if len(v) != item.count || item.id != "" && v[0].ID != item.id {
				t.Fatal("filter leaked excluded nodes", v)
			}
		})
	}
	for _, path := range []string{"/nodes?limit=bad", "/nodes?cursor=%25", "/nodes?cursor=" + base64.RawURLEncoding.EncodeToString([]byte("{")), "/nodes?cursor=" + base64.RawURLEncoding.EncodeToString([]byte(`[]`)), "/nodes?cursor=" + base64.RawURLEncoding.EncodeToString([]byte(`["wrong","id"]`)), "/metrics?range=24h&node_id=node-a"} {
		if w := clusterBoundaryRequest(h, "GET", path, ""); w.Code != 400 {
			t.Fatal(path, w.Code, w.Body)
		}
	}
	for _, path := range []string{"/nodes/missing/workloads", "/nodes/missing/instances", "/metrics?node_id=missing", "/metrics?node_id=node-a&container_id=missing"} {
		if w := clusterBoundaryRequest(h, "GET", path, ""); w.Code != 404 {
			t.Fatal(path, w.Code, w.Body)
		}
	}
	w := clusterBoundaryRequest(h, "GET", "/nodes/node-b", "")
	if w.Code != 200 || w.Header().Get("ETag") != "" {
		t.Fatal("unknown version produced an edit precondition", w.Code, w.Header())
	}
	metrics := responseData[cluster.Metrics](t, clusterBoundaryRequest(h, "GET", "/metrics?node_id=node-a&range=1h", ""), 200)
	if metrics.Source != "unavailable" || metrics.UnavailableReason != "host_metrics_unavailable" {
		t.Fatal("host metrics invented", metrics)
	}
	instances := responseData[[]cluster.Instance](t, clusterBoundaryRequest(h, "GET", "/nodes/node-a/instances", ""), 200)
	if len(instances) == 0 {
		t.Fatal("fixture inventory empty")
	}
	selected := instances[0]
	for _, q := range []string{"?instance_id=" + url.QueryEscape(selected.ID), "?instance_id=" + url.QueryEscape(selected.ID) + "&workload_id=" + url.QueryEscape(selected.WorkloadID)} {
		v := responseData[[]cluster.Instance](t, clusterBoundaryRequest(h, "GET", "/nodes/node-a/instances"+q, ""), 200)
		if len(v) != 1 || v[0].ID != selected.ID {
			t.Fatal("instance scope ignored", v)
		}
	}
	v := responseData[[]cluster.Instance](t, clusterBoundaryRequest(h, "GET", "/nodes/node-a/instances?workload_id=absent", ""), 200)
	if len(v) != 0 {
		t.Fatal(v)
	}
	client.Containers[0].State = container.StateExited
	client.Tasks[0].Status.State = "shutdown"
	if e := s.core.Cluster.Refresh(context.Background()); e != nil {
		t.Fatal(e)
	}
	metrics = responseData[cluster.Metrics](t, clusterBoundaryRequest(h, "GET", "/metrics?node_id=node-a&container_id=container-a&history=true", ""), 200)
	if metrics.UnavailableReason != "not_running" {
		t.Fatal("stopped container presented as current", metrics)
	}
	store.Runtimes[0].Enabled = false
	for _, path := range []string{"", "/nodes", "/nodes/node-a", "/nodes/node-a/workloads", "/nodes/node-a/instances", "/metrics?node_id=node-a"} {
		if w := clusterBoundaryRequest(h, "GET", path, ""); w.Code != 503 {
			t.Fatal("unavailable inventory accepted", path, w.Code, w.Body)
		}
	}
}

func TestClusterLabelValidationPreservesRuntimeLabels(t *testing.T) {
	_, h, store, client := clusterBoundaryHTTP(t)
	for _, body := range []string{"{", `{}`, `{"labels":null}`, `{"labels":[{"key":" ","value":"x"}]}`, `{"labels":[{"key":"bad\nkey","value":"x"}]}`, `{"labels":[{"key":"duplicate"},{"key":"duplicate"}]}`} {
		w := clusterBoundaryRequest(h, "PUT", "/nodes/node-a/labels", body)
		if w.Code != 400 || client.Updates != 0 || client.Nodes[0].Spec.Labels["zone"] != "a" {
			t.Fatal("invalid labels changed runtime", w.Code, w.Body)
		}
	}
	store.Runtimes[0].ManagementEnabled = false
	w := clusterBoundaryRequest(h, "PUT", "/nodes/node-a/labels", `{"labels":[]}`)
	if w.Code != 403 || client.Updates != 0 {
		t.Fatal("external management overwritten", w.Code, w.Body)
	}
}

type clusterReferencesBoundaryStore struct {
	storage.Store
	failure error
	calls   int
}

func (s *clusterReferencesBoundaryStore) ServiceEnvironments(_ context.Context, ids []string) (map[string]model.EnvironmentReference, error) {
	s.calls++
	if s.failure != nil {
		return nil, s.failure
	}
	result := map[string]model.EnvironmentReference{}
	for _, id := range ids {
		result[id] = model.EnvironmentReference{ID: "environment", Name: "Production"}
	}
	return result, nil
}

func TestClusterWorkloadsRetainEnvironmentReferencesAndReadFailures(t *testing.T) {
	s, _, store, _ := clusterBoundaryHTTP(t)
	store.Services = []model.Service{{ID: "managed-web", RuntimeID: "rt-cluster", RuntimeObjectID: "service-web", ServiceKey: "shop/web", Name: "web", Namespace: "shop", QualifiedName: "shop_web"}}
	if e := s.core.Cluster.Refresh(context.Background()); e != nil {
		t.Fatal(e)
	}
	wrapped := &clusterReferencesBoundaryStore{Store: s.store}
	s.store = wrapped
	router := chi.NewRouter()
	s.registerCluster(router)
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/runtimes/rt-cluster/cluster/nodes/node-a/workloads", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	rows := responseData[[]cluster.Workload](t, request(), 200)
	found := false
	for _, row := range rows {
		if row.ServiceID == "managed-web" {
			found = true
			if row.Environment == nil || row.Environment.ID != "environment" {
				t.Fatal("environment reference lost", row)
			}
		}
	}
	if !found || wrapped.calls != 1 {
		b, _ := json.Marshal(rows)
		t.Fatal("fixture service was not linked", string(b), wrapped.calls)
	}
	wrapped.failure = errors.New("environment references unavailable")
	w := request()
	if w.Code != 500 || strings.Contains(w.Body.String(), "environment references unavailable") {
		t.Fatal("partial workload evidence or raw error leaked", w.Code, w.Body)
	}
}
