package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type apiEvidenceReadStore struct {
	storage.Store
	failure error
}

func (s apiEvidenceReadStore) GetGateway(context.Context, string) (model.Gateway, error) {
	return model.Gateway{}, s.failure
}
func TestGatewayHTTPDoesNotReturnSuccessWhenEvidenceReadFails(t *testing.T) {
	s, _, _, _, _, gateway := resourcePatchFixture(t)
	s.core = core.New(apiEvidenceReadStore{Store: s.store, failure: errors.New("private gateway evidence unavailable")}, s.config, nil)
	router := chi.NewRouter()
	router.Get("/gateways", s.listGateways)
	router.Get("/gateways/{id}", s.getGateway)
	for _, path := range []string{"/gateways", "/gateways/" + gateway.ID} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 500 || strings.Contains(w.Body.String(), "private gateway evidence") || strings.Contains(w.Body.String(), `"data"`) {
			t.Fatal("partial gateway evidence reported as success", w.Code, w.Body)
		}
	}
}

func TestCommunityHistoryPaginationRetainsRecoverableRecords(t *testing.T) {
	s, h := newTestServer(t)
	ctx := context.Background()
	now := time.Now()
	for _, id := range []string{"history-a", "history-b"} {
		if e := s.store.AppendAuditEvent(ctx, model.AuditEvent{ID: id, Actor: "fixture", Action: "test recovery", CreatedAt: now}); e != nil {
			t.Fatal(e)
		}
		payload, e := json.Marshal(core.Issue{Code: "startup_risk", Active: true})
		if e != nil {
			t.Fatal(e)
		}
		if e = s.store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: id, Kind: "issue", ParentID: "gateway", Payload: payload}}}); e != nil {
			t.Fatal(e)
		}
	}
	for _, path := range []string{"/activity", "/issues"} {
		w := managementRequest(t, h, "GET", path+"?limit=1", nil, nil)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		var body struct {
			Data       []json.RawMessage `json:"data"`
			Pagination struct {
				Next string `json:"next_cursor"`
			} `json:"pagination"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
			t.Fatal(e)
		}
		if len(body.Data) != 1 {
			t.Fatal("unbounded history page", w.Body)
		}
		// Collection pagination preserves the ID cursor rather than deleting older evidence.
		first, second := "history-a", "history-b"
		if path == "/activity" {
			first, second = second, first
		}
		if body.Pagination.Next != first || !strings.Contains(string(body.Data[0]), first) {
			t.Fatal("history cursor lost stable identity", w.Body)
		}
		next := managementRequest(t, h, "GET", path+"?limit=1&cursor="+body.Pagination.Next, nil, nil)
		if next.Code != 200 || !strings.Contains(next.Body.String(), second) {
			t.Fatal("history continuation lost older evidence", next.Code, next.Body)
		}
	}
}

func TestMissingMarketMetadataSerializesAsNull(t *testing.T) {
	for _, raw := range []string{"", `{"name":"Market"}`} {
		encoded, e := json.Marshal(struct {
			Metadata json.RawMessage `json:"metadata"`
		}{Metadata: marketJSON(raw)})
		if e != nil {
			t.Fatal("missing market metadata broke API encoding", e)
		}
		want := `{"metadata":null}`
		if raw != "" {
			want = `{"metadata":` + raw + `}`
		}
		if string(encoded) != want {
			t.Fatal("market metadata wire representation changed", string(encoded))
		}
	}
}
