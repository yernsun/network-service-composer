package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type routeReferenceBoundaryStore struct {
	storage.Store
	kind    string
	failure error
	refs    map[string]storage.ConsoleReference
}

func (s *routeReferenceBoundaryStore) ConsoleReferences(ctx context.Context, kind string, ids []string) (map[string]storage.ConsoleReference, error) {
	if kind == s.kind {
		return nil, s.failure
	}
	if s.refs != nil {
		return s.refs, nil
	}
	return s.Store.ConsoleReferences(ctx, kind, ids)
}
func TestRouteReferencesNeverExposePartialOrUnserializableRows(t *testing.T) {
	s, _, _, domain, route, _ := resourcePatchFixture(t)
	base := s.store
	for _, mode := range []string{"complete", "service read failed", "domain read failed", "missing references", "invalid timestamp", "empty collection"} {
		t.Run(mode, func(t *testing.T) {
			store := &routeReferenceBoundaryStore{Store: base, failure: errors.New("private reference database failure")}
			values := []model.Route{route}
			switch mode {
			case "service read failed":
				store.kind = "services"
			case "domain read failed":
				store.kind = "domains"
			case "missing references":
				store.refs = map[string]storage.ConsoleReference{}
			case "invalid timestamp":
				values[0].CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
			case "empty collection":
				values = nil
			}
			s.store = store
			req := httptest.NewRequest("GET", "/api/v1/routes?include=references", nil)
			w := httptest.NewRecorder()
			s.writeRoutes(w, req, values, storage.Page{Limit: 1})
			failed := strings.Contains(mode, "failed") || mode == "invalid timestamp"
			if failed {
				if w.Code != 500 || strings.Contains(w.Body.String(), "private reference") || strings.Contains(w.Body.String(), `"data"`) {
					t.Fatal("reference failure exposed partial rows or internal detail", w.Code, w.Body)
				}
				return
			}
			data := responseData[[]map[string]any](t, w, 200)
			if mode == "empty collection" {
				if len(data) != 0 {
					t.Fatal("empty collection invented rows")
				}
				return
			}
			if len(data) != 1 || data[0]["id"] != route.ID {
				t.Fatal("enrichment changed Route identity", data)
			}
			service := data[0]["service"].(map[string]any)
			d := data[0]["domain"].(map[string]any)
			if mode == "complete" && (service["id"] != route.ServiceID || d["id"] != domain.ID || d["host"] != domain.Host) {
				t.Fatal("references misattributed", data)
			}
			if mode == "missing references" && (service["id"] != "" || d["id"] != "") {
				t.Fatal("missing references were guessed")
			}
			var envelope map[string]json.RawMessage
			if e := json.Unmarshal(w.Body.Bytes(), &envelope); e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(w.Body.String(), route.ID) {
				t.Fatal("reference pagination omitted row identity")
			}
		})
	}
}
func TestExternalServiceBindingHTTPChangesOnlyOwnedEntrance(t *testing.T) {
	s, _, _, _, route, _ := resourcePatchFixture(t)
	ctx := context.Background()
	router := chi.NewRouter()
	router.Route("/api/v1", func(r chi.Router) { s.registerAPI(r) })
	original, e := s.store.GetService(ctx, route.ServiceID)
	if e != nil {
		t.Fatal(e)
	}
	path := "/services/" + route.ServiceID + "/domain-bindings"
	bindings := responseData[[]model.DomainBinding](t, managementRequest(t, router, http.MethodGet, path, nil, nil), 200)
	if len(bindings) != 1 || bindings[0].Route.ID != route.ID {
		t.Fatal("existing entrance missing from service")
	}
	created := responseData[model.DomainBinding](t, managementRequest(t, router, "POST", path, map[string]any{"domain": map[string]any{"host": "binding.example.test", "entrypoints": []string{"http"}, "tls": map[string]any{"mode": "disabled"}}, "route": map[string]any{"path": "/", "backend": map[string]any{"port": 8080, "protocol": "http"}}}, nil), 201)
	headers := map[string]string{"If-Match": `"rev-1"`}
	updated := responseData[model.DomainBinding](t, managementRequest(t, router, "PATCH", path+"/"+created.Route.ID, map[string]any{"route": map[string]any{"path": "/api/"}}, headers), 200)
	if updated.Route.Path != "/api/" || updated.Domain.ID != created.Domain.ID {
		t.Fatal("binding patch changed domain identity")
	}
	headers["If-Match"] = `"rev-` + strconv.FormatInt(updated.Route.Revision, 10) + `"`
	if w := managementRequest(t, router, "DELETE", path+"/"+created.Route.ID, nil, headers); w.Code != 204 {
		t.Fatal("external binding deletion unexpectedly deployed workload", w.Code, w.Body)
	}
	current, e := s.store.GetService(ctx, original.ID)
	if e != nil || current.Managed || current.ApplicationID != original.ApplicationID || current.RuntimeObjectID != original.RuntimeObjectID {
		t.Fatal("entrance changes adopted external workload", current, e)
	}
	if _, e = s.store.GetRoute(ctx, route.ID); e != nil {
		t.Fatal("unrelated entrance removed", e)
	}
}
