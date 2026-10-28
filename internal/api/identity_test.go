package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/core"
)

func TestViewerCannotMutateAndSessionIsRevoked(t *testing.T) {
	s, handler := newTestServer(t)
	user, err := s.core.SaveUser(context.Background(), "", core.UserInput{Username: "reader", Role: "viewer", Enabled: true, Password: "reader-password-123456"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"reader","password":"reader-password-123456"}`))
	login.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, login)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	cookie := w.Result().Cookies()[0]
	var response struct {
		Data struct {
			CSRF string `json:"csrf_token"`
			Role string `json:"role"`
		} `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Role != "viewer" {
		t.Fatal(response)
	}
	for _, path := range []string{"/api/v1/gateway-pools", "/api/v1/applications", "/api/v1/users", "/api/v1/certificates"} {
		res := browserRequest(handler, "POST", path, `{}`, cookie, response.Data.CSRF)
		if res.Code != 403 {
			t.Fatal(path, res.Code, res.Body)
		}
	}
	if res := browserRequest(handler, "GET", "/api/v1/gateway-pools", "", cookie, ""); res.Code != 200 {
		t.Fatal(res.Code, res.Body)
	}
	if res := browserRequest(handler, "GET", "/api/v1/users", "", cookie, ""); res.Code != 403 {
		t.Fatal(res.Code, res.Body)
	}
	token, err := s.core.RotateUserToken(context.Background(), user.ID, user.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if res := browserRequest(handler, "GET", "/api/v1/gateway-pools", "", cookie, ""); res.Code != 401 {
		t.Fatal("stale session accepted", res.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/gateway-pools", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("viewer token accepted write", w.Code, w.Body)
	}
}

func TestUserChangesRequireReauthentication(t *testing.T) {
	_, handler := newTestServer(t)
	for _, password := range []string{"", "wrong", "test-admin-token"} {
		r := httptest.NewRequest("POST", "/api/v1/users", strings.NewReader(`{"username":"reader","role":"viewer","enabled":true,"password":"reader-password-123456"}`))
		r.Header.Set("Authorization", "Bearer test-admin-token")
		r.Header.Set("X-NSC-Reauthenticate", password)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := 403
		if password == "test-admin-token" {
			want = 201
		}
		if w.Code != want || strings.Contains(w.Body.String(), "reader-password") || strings.Contains(w.Body.String(), "password_hash") {
			t.Fatal(w.Code, w.Body)
		}
	}
}
