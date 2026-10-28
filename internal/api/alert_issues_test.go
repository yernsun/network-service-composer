package api

import (
	"encoding/json"
	"fmt"
	"github.com/yernsun/network-service-composer/internal/storage"
	"testing"

	"github.com/yernsun/network-service-composer/internal/core"
)

func TestIssueHTTPResponsibilityAndAcknowledgementPreserveActiveFault(t *testing.T) {
	s, h, _, _ := managedRuntimeFixture(t)
	if err := s.core.RefreshCommunityIssues(t.Context()); err != nil {
		t.Fatal(err)
	}
	res := managementRequest(t, h, "GET", "/issues", nil, nil)
	var collection struct {
		Data []core.Issue `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &collection); err != nil || res.Code != 200 || res.Header().Get("Cache-Control") != "no-store" || len(collection.Data) == 0 {
		t.Fatal(res.Code, res.Body.String(), err)
	}
	issue := collection.Data[0]
	path := "/issues/" + issue.ID
	if res := managementRequest(t, h, "PUT", path+"/responsibility", map[string]string{"user_id": "user_bootstrap"}, nil); res.Code != 428 {
		t.Fatal(res.Code, res.Body.String())
	}
	if res := managementRequest(t, h, "PUT", path+"/responsibility", map[string]string{"user_id": "user_bootstrap"}, map[string]string{"If-Match": `"rev-999"`}); res.Code != 412 {
		t.Fatal(res.Code, res.Body.String())
	}
	// The collection is not a single-resource receipt; use its item revision.
	rows, err := s.core.Issues(t.Context(), storage.Page{})
	if err != nil || len(rows) == 0 {
		t.Fatal(err)
	}
	headers := map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, rows[0].Revision)}
	res = managementRequest(t, h, "PUT", path+"/responsibility", map[string]string{"user_id": "user_bootstrap"}, headers)
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	var assigned struct {
		Data core.Issue `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &assigned); err != nil || assigned.Data.ResponsibleUserID != "user_bootstrap" {
		t.Fatal(res.Body.String(), err)
	}
	res = managementRequest(t, h, "POST", path+"/acknowledge", nil, map[string]string{"If-Match": res.Header().Get("ETag")})
	var acknowledged struct {
		Data core.Issue `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &acknowledged); err != nil || res.Code != 200 || !acknowledged.Data.Active || !acknowledged.Data.Acknowledged || acknowledged.Data.AcknowledgedBy != "user_bootstrap" || acknowledged.Data.AcknowledgedAt == nil || acknowledged.Data.ResolvedAt != nil {
		t.Fatal(res.Code, res.Body.String(), err)
	}
}
