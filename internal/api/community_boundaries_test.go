package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestGatewayEvidenceStatusPreservesIndependentFailures(t *testing.T) {
	for _, test := range []struct {
		name         string
		enabled      bool
		status, want model.GatewayStatus
		evidence     *model.GatewayEvidence
	}{
		{"no evidence", true, model.GatewayReady, model.GatewayReady, nil},
		{"disabled", false, model.GatewayReady, model.GatewayReady, &model.GatewayEvidence{StartupState: "RISK"}},
		{"incompatible", true, model.GatewayIncompatible, model.GatewayIncompatible, &model.GatewayEvidence{StartupState: "RISK"}},
		{"apply failed", true, model.GatewayApplyFailed, model.GatewayApplyFailed, &model.GatewayEvidence{ConfigurationState: "UNKNOWN"}},
		{"restart unsafe", true, model.GatewayReady, model.GatewayDegraded, &model.GatewayEvidence{StartupState: "RISK"}},
		{"external drift", true, model.GatewayReady, model.GatewayDegraded, &model.GatewayEvidence{ConfigurationState: "EXTERNAL_CHANGE"}},
		{"stale evidence", true, model.GatewayReady, model.GatewayStale, &model.GatewayEvidence{ConfigurationState: "UNKNOWN"}},
		{"already degraded", true, model.GatewayDegraded, model.GatewayDegraded, &model.GatewayEvidence{ConfigurationState: "UNKNOWN"}},
		{"confirmed", true, model.GatewayReady, model.GatewayReady, &model.GatewayEvidence{ConfigurationState: "CONFIRMED", StartupState: "CONFIRMED"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := model.Gateway{Enabled: test.enabled, Status: test.status, Evidence: test.evidence}
			applyEvidenceStatus(&g)
			if g.Status != test.want || g.Evidence != test.evidence {
				t.Fatalf("evidence classification changed facts: %+v", g)
			}
		})
	}
}

type communityHTTPFailureStore struct {
	storage.Store
	failure error
}

func (s *communityHTTPFailureStore) ListAuditEvents(context.Context, storage.Page) ([]model.AuditEvent, error) {
	return nil, s.failure
}
func (s *communityHTTPFailureStore) ListControlDocuments(context.Context, string, string, storage.Page) ([]storage.ControlDocument, error) {
	return nil, s.failure
}
func (s *communityHTTPFailureStore) GetGateway(context.Context, string) (model.Gateway, error) {
	return model.Gateway{}, s.failure
}

func TestCommunityHTTPUnavailableReadsAreNotEmptySuccess(t *testing.T) {
	s, _ := newTestServer(t)
	boundary := &communityHTTPFailureStore{Store: s.store, failure: errors.New("community storage unavailable")}
	s.store = boundary
	s.core = core.New(boundary, s.config, nil)
	router := chi.NewRouter()
	s.registerCommunity(router)
	for _, path := range []string{"/activity", "/issues", "/gateways/gateway/evidence"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 500 {
			t.Fatalf("%s reported false success: %d %s", path, w.Code, w.Body)
		}
	}
}

func TestApplicationRestoreHTTPReviewAndQueue(t *testing.T) {
	s, h, rt, _ := managedRuntimeFixture(t)
	ctx := context.Background()
	in := core.ApplicationPreflightInput{RuntimeID: rt.ID, ComposeYAML: "services:\n  web:\n    image: example.test/app@sha256:" + strings.Repeat("a", 64) + "\n"}
	a, op, _, err := s.core.InstallApplication(ctx, core.InstallApplicationInput{ApplicationPreflightInput: in, Name: "restore", ProjectName: "restore", IdempotencyKey: "install", Actor: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.(*storage.SQLStore).DB().ExecContext(ctx, `UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?`, op.ID); err != nil {
		t.Fatal(err)
	}
	w := managementRequest(t, h, "POST", "/applications/"+a.ID+"/revisions/"+op.RevisionID+"/restore-preview", nil, nil)
	review := responseData[core.ApplicationRestoreReview](t, w, 200)
	if review.DataRestored || review.SourceRevisionID != op.RevisionID || review.ApplicationID != a.ID {
		t.Fatalf("restore preview overstated scope: %+v", review)
	}
	headers := map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, review.BaseRevision), "Idempotency-Key": "restore"}
	body := map[string]any{"review_id": review.ID, "acknowledge_data_boundary": false}
	w = managementRequest(t, h, "POST", "/applications/"+a.ID+"/restore", body, headers)
	if w.Code != 400 {
		t.Fatalf("missing data boundary accepted: %d %s", w.Code, w.Body)
	}
	body["acknowledge_data_boundary"] = true
	w = managementRequest(t, h, "POST", "/applications/"+a.ID+"/restore", body, headers)
	queued := responseData[model.DeploymentOperation](t, w, 202)
	if queued.ID == "" || queued.RevisionID == op.RevisionID || queued.Status != model.OperationQueued {
		t.Fatalf("restore did not create independent queued operation: %+v", queued)
	}
	w = managementRequest(t, h, "POST", "/applications/"+a.ID+"/restore", body, headers)
	repeated := responseData[model.DeploymentOperation](t, w, 202)
	if repeated.ID != queued.ID {
		t.Fatal("retry queued duplicate restoration")
	}
	r, err := s.store.GetApplicationRevision(ctx, queued.RevisionID)
	if err != nil || r.ConfigurationSource.RestoredFrom != op.RevisionID {
		t.Fatal("restoration provenance lost", err)
	}
}

func TestCommunityHTTPPreconditionsAndPersistentAcknowledgement(t *testing.T) {
	s, h := newTestServer(t)
	ctx := context.Background()
	v := core.Issue{Code: "gateway_unknown", Active: true, Occurrences: 1}
	payload, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: "issue_fixture", Kind: "issue", Payload: payload}}}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path, body, match string
		want              int
	}{
		{"/applications/missing/revisions/missing/restore-preview", "", "", 404},
		{"/applications/missing/restore", "{}", "", 428},
		{"/applications/missing/restore", "{", `"rev-1"`, 400},
		{"/applications/missing/restore", `{"review_id":"missing"}`, `"rev-1"`, 400},
		{"/issues/issue_fixture/acknowledge", "", "", 428},
		{"/issues/issue_fixture/acknowledge", "", `"rev-9"`, 412},
		{"/issues/missing/acknowledge", "", `"rev-1"`, 404},
	} {
		t.Run(fmt.Sprintf("%s/%d", test.path, test.want), func(t *testing.T) {
			w := managementRequest(t, h, "POST", test.path, test.body, map[string]string{"If-Match": test.match})
			if w.Code != test.want {
				t.Fatalf("HTTP %d want %d: %s", w.Code, test.want, w.Body)
			}
		})
	}
	w := managementRequest(t, h, "POST", "/issues/issue_fixture/acknowledge", nil, map[string]string{"If-Match": `"rev-1"`})
	ack := responseData[core.Issue](t, w, 200)
	if !ack.Active || !ack.Acknowledged || ack.Revision != 2 {
		t.Fatalf("acknowledgement erased ongoing issue: %+v", ack)
	}
	w = managementRequest(t, h, "GET", "/issues", nil, nil)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	var collection struct {
		Data []core.Issue `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &collection); err != nil || len(collection.Data) != 1 || !collection.Data[0].Acknowledged || !collection.Data[0].Active {
		t.Fatalf("issue not persisted: %s %v", w.Body, err)
	}
	for _, path := range []string{"/activity", "/gateways/missing/evidence"} {
		w := managementRequest(t, h, "GET", path, nil, nil)
		want := 200
		if path != "/activity" {
			want = 404
		}
		if w.Code != want {
			t.Fatalf("%s HTTP %d: %s", path, w.Code, w.Body)
		}
	}
}
