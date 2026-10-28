package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestCatalogSourceHTTPUpdatesPreserveImmutableReferences(t *testing.T) {
	s, h := newTestServer(t)
	input := map[string]any{"name": " Remote ", "type": "GIT", "url": "https://catalog.example.test/index.json", "pinned_ref": strings.Repeat("a", 64), "trusted": true}
	source := responseData[model.CatalogSource](t, managementRequest(t, h, "POST", "/catalog/sources", input, nil), 201)
	if source.Name != "Remote" || !source.Enabled || !source.Trusted {
		t.Fatalf("source creation lost explicit trust: %#v", source)
	}
	path := "/catalog/sources/" + source.ID
	loaded := responseData[model.CatalogSource](t, managementRequest(t, h, "GET", path, nil, nil), 200)
	if loaded.ID != source.ID {
		t.Fatal("source not findable")
	}
	for _, patch := range []string{`{`, `{"name":" "}`, `{"url":"http://127.0.0.1/index.json"}`, `{"pinned_ref":"mutable-branch"}`} {
		w := managementRequest(t, h, "PATCH", path, patch, map[string]string{"If-Match": `"rev-1"`})
		if w.Code != 400 {
			t.Fatalf("invalid source mutation accepted: %d %s", w.Code, w.Body.String())
		}
		actual, err := s.store.GetCatalogSource(context.Background(), source.ID)
		if err != nil || actual.Revision != 1 || actual.URL != source.URL {
			t.Fatalf("invalid mutation changed source: %#v %v", actual, err)
		}
	}
	patch := map[string]any{"name": " Renamed ", "url": " https://catalog.example.test/v2/index.json ", "pinned_ref": " SHA256:" + strings.Repeat("B", 64) + " ", "trusted": false, "enabled": false, "approved_endpoint": "https://catalog.example.test/"}
	updated := responseData[model.CatalogSource](t, managementRequest(t, h, "PATCH", path, patch, map[string]string{"If-Match": `"rev-1"`}), 200)
	if updated.Name != "Renamed" || updated.PinnedRef != strings.Repeat("b", 64) || updated.Trusted || updated.Enabled || updated.Revision != 2 {
		t.Fatalf("source patch lost values: %#v", updated)
	}
	w := managementRequest(t, h, "PATCH", path, `{"name":"stale"}`, map[string]string{"If-Match": `"rev-1"`})
	if w.Code != 412 {
		t.Fatalf("stale source overwritten: %d %s", w.Code, w.Body.String())
	}
	w = managementRequest(t, h, "POST", path+"/sync", nil, nil)
	if w.Code != 400 {
		t.Fatalf("untrusted source synchronized: %d %s", w.Code, w.Body.String())
	}
	w = managementRequest(t, h, "PATCH", path, `{}`, map[string]string{"If-Match": `"rev-2"`})
	responseData[model.CatalogSource](t, w, 200)
	w = managementRequest(t, h, "GET", "/catalog/sources?limit=1", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), source.ID) {
		t.Fatalf("catalog list missing source: %d %s", w.Code, w.Body.String())
	}
	for _, patch := range []map[string]any{{"url": "https://catalog.example.test/v3/index.json"}, {"pinned_ref": strings.Repeat("c", 64)}, {"approved_endpoint": "https://catalog.example.test/v3/"}} {
		current, err := s.store.GetCatalogSource(context.Background(), source.ID)
		if err != nil {
			t.Fatal(err)
		}
		responseData[model.CatalogSource](t, managementRequest(t, h, "PATCH", path, patch, map[string]string{"If-Match": revisionETag(current.Revision)}), 200)
	}
}

func revisionETag(revision int64) string { return `"rev-` + strconv.FormatInt(revision, 10) + `"` }

func TestBuiltinCatalogHTTPReadsAndReadOnlyBoundary(t *testing.T) {
	s, h := newTestServer(t)
	ctx := context.Background()
	if err := s.core.EnsureBuiltinCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	sources, err := s.store.ListCatalogSources(ctx, storage.Page{Limit: 200})
	if err != nil || len(sources) != 1 {
		t.Fatalf("builtin source fixture: %#v %v", sources, err)
	}
	source := sources[0]
	path := "/catalog/sources/" + source.ID
	w := managementRequest(t, h, "PATCH", path, `{"name":"overwrite"}`, map[string]string{"If-Match": revisionETag(source.Revision)})
	if w.Code != 400 {
		t.Fatalf("builtin mutation accepted: %d", w.Code)
	}
	syncResult := responseData[struct {
		VersionsSynced int `json:"versions_synced"`
	}](t, managementRequest(t, h, "POST", path+"/sync", nil, nil), 200)
	if syncResult.VersionsSynced != 0 {
		t.Fatal("builtin sync falsely fetched external content")
	}
	versions, err := s.store.ListCatalogAppVersions(ctx, source.ID, "", storage.Page{Limit: 200})
	if err != nil || len(versions) == 0 {
		t.Fatalf("builtin versions unavailable: %v", err)
	}
	version := versions[0]
	for _, path := range []string{"/catalog/apps?source_id=" + source.ID + "&limit=1", "/catalog/apps/" + version.AppKey + "/versions?source_id=" + source.ID + "&limit=1"} {
		w := managementRequest(t, h, "GET", path, nil, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), version.AppKey) {
			t.Fatalf("catalog list=%d %s", w.Code, w.Body.String())
		}
	}
	w = managementRequest(t, h, "GET", "/catalog/versions/"+version.ID, nil, nil)
	value := responseData[map[string]json.RawMessage](t, w, 200)
	if len(value["variables"]) == 0 || w.Header().Get("ETag") == "" {
		t.Fatal("catalog version omitted input schema or revision")
	}
	actual, err := s.store.GetCatalogSource(ctx, source.ID)
	if err != nil || actual.Name != source.Name {
		t.Fatal("read-only source changed")
	}
}

type failingCatalogStore struct {
	storage.Store
	fail string
	gets int
}

func (s *failingCatalogStore) ListCatalogSources(ctx context.Context, p storage.Page) ([]model.CatalogSource, error) {
	return nil, diagnosticFailure
}
func (s *failingCatalogStore) ListCatalogAppVersions(ctx context.Context, source, app string, p storage.Page) ([]model.CatalogAppVersion, error) {
	return nil, diagnosticFailure
}
func (s *failingCatalogStore) GetCatalogSource(ctx context.Context, key string) (model.CatalogSource, error) {
	s.gets++
	if s.fail == "get" || s.fail == "after update" && s.gets > 1 {
		return model.CatalogSource{}, diagnosticFailure
	}
	return s.Store.GetCatalogSource(ctx, key)
}
func (s *failingCatalogStore) UpdateCatalogSource(ctx context.Context, v *model.CatalogSource, expected int64) error {
	if s.fail == "update" {
		return diagnosticFailure
	}
	return s.Store.UpdateCatalogSource(ctx, v, expected)
}
func (s *failingCatalogStore) GetCatalogAppVersion(context.Context, string) (model.CatalogAppVersion, error) {
	if s.fail == "corrupt manifest" {
		return model.CatalogAppVersion{ManifestJSON: "{"}, nil
	}
	return model.CatalogAppVersion{}, diagnosticFailure
}

func TestCatalogHTTPDoesNotTurnStorageFailuresIntoEmptySuccess(t *testing.T) {
	s, h := newTestServer(t)
	base := s.store
	for _, path := range []string{"/catalog/sources", "/catalog/apps", "/catalog/apps/app/versions", "/catalog/sources/missing", "/catalog/versions/missing"} {
		s.store = &failingCatalogStore{Store: base, fail: "get"}
		w := managementRequest(t, h, "GET", path, nil, nil)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("failed read %s=%d", path, w.Code)
		}
	}
	s.store = &failingCatalogStore{Store: base, fail: "corrupt manifest"}
	w := managementRequest(t, h, "GET", "/catalog/versions/corrupt", nil, nil)
	if w.Code != 500 {
		t.Fatalf("corrupt manifest accepted: %d", w.Code)
	}
	s.store = base
	input := map[string]any{"name": "Remote", "type": "STATIC_MANIFEST", "url": "https://catalog.example.test/manifest.json", "trusted": true}
	source := responseData[model.CatalogSource](t, managementRequest(t, h, "POST", "/catalog/sources", input, nil), 201)
	for _, failure := range []string{"get", "update", "after update"} {
		current, err := base.GetCatalogSource(context.Background(), source.ID)
		if err != nil {
			t.Fatal(err)
		}
		s.store = &failingCatalogStore{Store: base, fail: failure}
		w := managementRequest(t, h, "PATCH", "/catalog/sources/"+source.ID, `{"name":"updated"}`, map[string]string{"If-Match": revisionETag(current.Revision)})
		if w.Code != 500 {
			t.Fatalf("%s error became success: %d %s", failure, w.Code, w.Body.String())
		}
	}
}
