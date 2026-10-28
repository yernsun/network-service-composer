package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/core"
)

func TestNotificationRoutingHTTPBoundariesAndStoredPreview(t *testing.T) {
	s, h := newTestServer(t)
	body := map[string]any{"name": "team", "enabled": true, "endpoint": "https://receiver.example.test/hook?private=value", "secret": "private-signing-secret"}
	headers := map[string]string{"If-Match": `"rev-0"`, "X-NSC-Reauthenticate": "test-admin-token"}
	for _, path := range []string{"/notification-receivers/receiver_team", "/notification-routes", "/notification-silences/silence_work"} {
		if res := managementRequest(t, h, "PUT", path, body, nil); res.Code != 403 {
			t.Fatal(path, res.Code)
		}
		if res := managementRequest(t, h, "PUT", path, body, map[string]string{"X-NSC-Reauthenticate": "test-admin-token"}); res.Code != 428 {
			t.Fatal(path, res.Code, res.Body)
		}
		if res := managementRequest(t, h, "PUT", path, "{", headers); res.Code != 400 {
			t.Fatal(path, res.Code, res.Body)
		}
	}
	receiver := responseData[core.NotificationReceiver](t, managementRequest(t, h, "PUT", "/notification-receivers/receiver_team", body, headers), 200)
	if receiver.ID != "receiver_team" || receiver.Revision != 1 {
		t.Fatal(receiver)
	}
	if res := managementRequest(t, h, "PUT", "/notification-receivers/receiver_team", body, headers); res.Code != 412 {
		t.Fatal("stale receiver accepted", res.Code)
	}
	rule := map[string]any{"scope_type": "global", "scope_id": "", "enabled": true, "receiver_ids": []string{receiver.ID}, "events": []string{"runtimes"}, "severities": []string{}, "base_settings_revision": 0}
	responseData[core.NotificationRoute](t, managementRequest(t, h, "PUT", "/notification-routes", rule, headers), 200)
	silence := responseData[core.NotificationSilence](t, managementRequest(t, h, "PUT", "/notification-silences/silence_work", map[string]any{"scope_type": "global", "reason": "maintenance"}, headers), 200)
	if silence.Actor == "" || !silence.Active {
		t.Fatal(silence)
	}
	for _, path := range []string{"/notification-receivers", "/notification-routes", "/notification-silences", "/notification-health"} {
		res := managementRequest(t, h, "GET", path, nil, nil)
		if res.Code != 200 || res.Header().Get("Cache-Control") != "no-store" || strings.Contains(res.Body.String(), "private-") || strings.Contains(res.Body.String(), "?private") {
			t.Fatal(path, res.Code, res.Body)
		}
	}
	for _, path := range []string{"/notification-receivers/receiver_team/test", "/notification-silences/silence_work/cancel"} {
		if res := managementRequest(t, h, "POST", path, nil, nil); res.Code != 403 {
			t.Fatal(path, res.Code)
		}
		if res := managementRequest(t, h, "POST", path, nil, map[string]string{"X-NSC-Reauthenticate": "test-admin-token"}); res.Code != 428 {
			t.Fatal(path, res.Code)
		}
	}
	headers["If-Match"] = `"rev-1"`
	if res := managementRequest(t, h, "POST", "/notification-receivers/receiver_team/test", nil, headers); res.Code != 202 {
		t.Fatal(res.Code, res.Body)
	}
	responseData[core.NotificationSilence](t, managementRequest(t, h, "POST", "/notification-silences/silence_work/cancel", nil, headers), 200)
	if res := managementRequest(t, h, "POST", "/notification-routing/preview", map[string]any{"type": "runtime.problem", "scopes": []string{"forged"}}, nil); res.Code != 422 && res.Code != 400 {
		t.Fatal("preview trusted arbitrary input", res.Code, res.Body)
	}
	if res := managementRequest(t, h, "POST", "/notification-routing/preview", "{", nil); res.Code != 400 {
		t.Fatal(res.Code, res.Body)
	}
	if res := managementRequest(t, h, "POST", "/notification-routing/preview", map[string]string{"issue_id": "missing"}, nil); res.Code != 404 {
		t.Fatal(res.Code, res.Body)
	}
	viewer, err := s.core.SaveUser(context.Background(), "", core.UserInput{Username: "routing-reader", Role: "viewer", Enabled: true, Password: "viewer-password-for-routing"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.core.RotateUserToken(context.Background(), viewer.ID, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/notification-receivers", "/notification-routes", "/notification-silences", "/notification-health", "/notification-routing/preview"} {
		method := http.MethodGet
		if path == "/notification-routing/preview" {
			method = http.MethodPost
		}
		req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != 403 {
			t.Fatal("viewer read routing configuration", path, res.Code)
		}
	}
}
