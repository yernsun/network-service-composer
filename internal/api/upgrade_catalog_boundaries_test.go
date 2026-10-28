package api

import (
	"context"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"net/http"
	"strings"
	"testing"
)

type upgradeCatalogStore struct {
	storage.Store
	mode           string
	source, appKey string
}

func (s *upgradeCatalogStore) GetApplication(context.Context, string) (model.Application, error) {
	if s.mode == "application failure" {
		return model.Application{}, errors.New("private application store")
	}
	v := model.Application{ID: "application", CatalogAppVersionID: "current"}
	if s.mode == "not catalog" {
		v.CatalogAppVersionID = ""
	}
	return v, nil
}
func (s *upgradeCatalogStore) GetCatalogAppVersion(context.Context, string) (model.CatalogAppVersion, error) {
	if s.mode == "current failure" {
		return model.CatalogAppVersion{}, errors.New("private catalog store")
	}
	return model.CatalogAppVersion{ID: "current", SourceID: "official", AppKey: "web"}, nil
}
func (s *upgradeCatalogStore) ListCatalogAppVersions(_ context.Context, source, key string, _ storage.Page) ([]model.CatalogAppVersion, error) {
	s.source, s.appKey = source, key
	if s.mode == "list failure" {
		return nil, errors.New("private list store")
	}
	if s.mode == "empty" {
		return nil, nil
	}
	return []model.CatalogAppVersion{{ID: "current", SourceID: source, AppKey: key}, {ID: "candidate", SourceID: source, AppKey: key}}, nil
}
func TestApplicationUpgradeListPreservesCatalogScopeAndErrors(t *testing.T) {
	for _, mode := range []string{"valid", "application failure", "current failure", "list failure", "not catalog", "empty"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := newTestServer(t)
			f := &upgradeCatalogStore{Store: s.store, mode: mode}
			s.store = f
			router := chi.NewRouter()
			router.Route("/api/v1", func(r chi.Router) { s.registerAPI(r) })
			w := managementRequest(t, router, http.MethodGet, "/applications/application/upgrades", nil, nil)
			if strings.HasSuffix(mode, "failure") {
				if w.Code != 500 || strings.Contains(w.Body.String(), "private") {
					t.Fatal("unknown catalog result misrepresented", w.Code, w.Body)
				}
				return
			}
			versions := responseData[[]model.CatalogAppVersion](t, w, 200)
			want := 0
			if mode == "valid" {
				want = 1
			}
			if len(versions) != want {
				t.Fatal("current version offered as upgrade", versions)
			}
			if want == 1 && (versions[0].ID != "candidate" || f.source != "official" || f.appKey != "web") {
				t.Fatal("upgrade crossed source identity", versions)
			}
		})
	}
}
