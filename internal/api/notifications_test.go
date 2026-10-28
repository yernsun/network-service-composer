package api

import (
	"context"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/storage"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
)

func TestNotificationsHTTPAuthorizationReauthenticationAndRedaction(t *testing.T) {
	s, h := newTestServer(t)
	body := map[string]any{"enabled": true, "events": []string{"images", "certificates", "gateways"}, "endpoint": "https://receiver.example.test/hook?token=private-endpoint", "secret": "private-notification-secret"}
	reauth := map[string]string{"X-NSC-Reauthenticate": "test-admin-token", "If-Match": `"rev-0"`}
	initial := managementRequest(t, h, "GET", "/notification-settings", nil, nil)
	if initial.Code != 200 || initial.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(initial.Code, initial.Body)
	}
	if res := managementRequest(t, h, "PUT", "/notification-settings", body, nil); res.Code != 403 {
		t.Fatal(res.Code, res.Body)
	}
	if res := managementRequest(t, h, "PUT", "/notification-settings", body, map[string]string{"X-NSC-Reauthenticate": "test-admin-token"}); res.Code != 428 {
		t.Fatal(res.Code, res.Body)
	}
	if res := managementRequest(t, h, "PUT", "/notification-settings", "{", reauth); res.Code != 400 {
		t.Fatal(res.Code, res.Body)
	}
	value := responseData[core.NotificationSettings](t, managementRequest(t, h, "PUT", "/notification-settings", body, reauth), 200)
	if value.Revision != 1 || !value.Enabled {
		t.Fatal(value)
	}
	if res := managementRequest(t, h, "PUT", "/notification-settings", body, reauth); res.Code != 412 {
		t.Fatal("stale save accepted", res.Code)
	}
	for _, path := range []string{"/notification-settings", "/notifications"} {
		res := managementRequest(t, h, "GET", path, nil, nil)
		if res.Code != 200 || strings.Contains(res.Body.String(), "private-") || strings.Contains(res.Body.String(), "token=") {
			t.Fatal("settings leaked", res.Code, res.Body)
		}
	}
	reauth["If-Match"] = `"rev-1"`
	if res := managementRequest(t, h, "POST", "/notifications/test", nil, reauth); res.Code != 202 {
		t.Fatal(res.Code, res.Body)
	}
	rows := responseData[[]core.Notification](t, managementRequest(t, h, "GET", "/notifications", nil, nil), 200)
	if len(rows) != 1 || rows[0].Status != "PENDING" {
		t.Fatal(rows)
	}
	if res := managementRequest(t, h, "POST", "/notifications/"+rows[0].ID+"/retry", nil, reauth); res.Code != 409 {
		t.Fatal("pending delivery retried", res.Code, res.Body)
	}
	if res := managementRequest(t, h, "POST", "/notifications/missing/retry", nil, reauth); res.Code != 404 {
		t.Fatal(res.Code, res.Body)
	}
	if res := managementRequest(t, h, "POST", "/notifications/missing/retry", nil, map[string]string{"X-NSC-Reauthenticate": "test-admin-token"}); res.Code != 428 {
		t.Fatal(res.Code, res.Body)
	}
	viewer, err := s.core.SaveUser(context.Background(), "", core.UserInput{Username: "notification-reader", Role: "viewer", Enabled: true, Password: "viewer-password-for-tests"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.core.RotateUserToken(context.Background(), viewer.ID, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/notification-settings", "/api/v1/notifications"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != 403 {
			t.Fatal("viewer read global notification data", res.Code)
		}
		req = httptest.NewRequest(http.MethodGet, path, nil)
		res = httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != 401 {
			t.Fatal("anonymous notification read accepted", res.Code)
		}
	}
	// No API accepts a request body as a substitute for administrator credentials.
	if res := managementRequest(t, h, "POST", "/notifications/test", nil, nil); res.Code != 403 {
		t.Fatal(res.Code)
	}
	raw, _ := json.Marshal(value)
	if strings.Contains(string(raw), "private-") {
		t.Fatal("credentials returned")
	}
}
func TestNotificationsHTTPRecoveryModeRefusesDeliveryRequests(t *testing.T) {
	s, _ := newTestServer(t)
	s.config.Controller.RecoveryMode = true
	h := s.Handler()
	for _, path := range []string{"/api/v1/notification-settings", "/api/v1/notifications/test", "/api/v1/notifications/id/retry"} {
		req := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer test-admin-token")
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != 503 || !strings.Contains(res.Body.String(), "recovery.read_only") {
			t.Fatal("recovery allowed outgoing notification task", res.Code, res.Body)
		}
	}
}

func TestNotificationHTTPUnreadableHistoryAndMissingWriteProof(t *testing.T) {
	s, h := newTestServer(t)
	headers := map[string]string{"X-NSC-Reauthenticate": "test-admin-token"}
	for _, path := range []string{"/notifications/test", "/notifications/missing/retry"} {
		if res := managementRequest(t, h, "POST", path, nil, headers); res.Code != 428 {
			t.Fatal(path, res.Code, res.Body)
		}
	}
	headers["If-Match"] = `"rev-1"`
	if res := managementRequest(t, h, "POST", "/notifications/missing/retry", nil, nil); res.Code != 403 {
		t.Fatal(res.Code)
	}
	now := time.Now().UTC()
	bad := storage.ControlDocument{ID: "notice-corrupt", Kind: "notification-delivery", Payload: json.RawMessage(`{"status":"FAILED"}`), CreatedAt: now, UpdatedAt: now}
	if err := s.store.WriteControlDocuments(context.Background(), []storage.ControlWrite{{Document: bad}}); err != nil {
		t.Fatal(err)
	}
	// Simulate existing on-disk corruption after passing transactional admission.
	if _, err := s.store.(*storage.SQLStore).DB().ExecContext(context.Background(), `UPDATE control_documents SET payload='{' WHERE id='notice-corrupt'`); err != nil {
		t.Fatal(err)
	}
	if res := managementRequest(t, h, "GET", "/notifications", nil, nil); res.Code != 500 {
		t.Fatal("unreadable history was hidden", res.Code, res.Body)
	}
	// Registering handlers directly must still refuse requests without identity.
	router := chi.NewRouter()
	s.registerNotifications(router)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest("GET", "/notification-settings", nil))
	if res.Code != 403 {
		t.Fatal("notification route trusted missing user context", res.Code)
	}
}
