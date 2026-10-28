package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestIssueHTTPFiltersUsePersistedOwnershipAndKeepViewerReadOnly(t *testing.T) {
	s, h := newTestServer(t)
	now := time.Now().UTC()
	for _, row := range []struct {
		id, project, environment string
		active                   bool
	}{
		{"issue_a", "team-a", "env-a", true}, {"issue_b", "team-b", "env-b", true}, {"issue_c", "team-a", "env-a", false},
	} {
		v := core.Issue{Code: "runtime_unavailable", ResourceType: "runtime", ResourceID: "shared", Active: row.active, Occurrences: 1, ObservationState: "UNKNOWN", Scopes: []core.AlertScope{{ProjectID: row.project, EnvironmentID: row.environment}}, FirstSeenAt: now, LastSeenAt: now}
		payload, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: storage.ControlDocument{ID: row.id, Kind: "issue", Payload: payload}}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"state=active&project_id=team-a&environment_id=env-a&resource_type=runtime&resource_id=shared&code=runtime_unavailable", 1},
		{"state=resolved&environment_id=env-a", 1}, {"state=all&environment_id=env-a", 2},
		{"project_id=team-a&environment_id=env-b", 0}, {"resource_id=foreign", 0},
	} {
		response := managementRequest(t, h, "GET", "/issues?"+tc.query, nil, nil)
		var result struct {
			Data []core.Issue `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 || len(result.Data) != tc.want || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(tc.query, response.Code, response.Body.String(), err)
		}
	}
	if response := managementRequest(t, h, "GET", "/issues?state=forged", nil, nil); response.Code != 400 {
		t.Fatal(response.Code, response.Body.String())
	}
	// Actor identity comes from the authenticated principal, never the body.
	response := managementRequest(t, h, "POST", "/issues/issue_a/acknowledge", map[string]string{"actor": "forged-user"}, map[string]string{"If-Match": `"rev-1"`})
	acknowledged := responseData[core.Issue](t, response, 200)
	if !acknowledged.Active || acknowledged.ResolvedAt != nil || acknowledged.AcknowledgedBy != "user_bootstrap" {
		t.Fatal(acknowledged)
	}
	viewer, err := s.core.SaveUser(t.Context(), "", core.UserInput{Username: "issues-viewer", Role: "viewer", Enabled: true, Password: "isolated-issues-viewer-password"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.core.RotateUserToken(t.Context(), viewer.ID, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		want         int
	}{{"GET", "/issues?environment_id=env-b", 200}, {"POST", "/issues/issue_b/acknowledge", 403}, {"PUT", "/issues/issue_b/responsibility", 403}, {"GET", "/notifications", 403}} {
		req := httptest.NewRequest(tc.method, "/api/v1"+tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		result := httptest.NewRecorder()
		h.ServeHTTP(result, req)
		if result.Code != tc.want {
			t.Fatal(tc.method, tc.path, result.Code, result.Body.String())
		}
	}
}
