package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestIdentityRoleBoundaryMethodsAndContextSources(t *testing.T) {
	s, _ := newTestServer(t)
	for _, role := range []string{"", "viewer", "administrator"} {
		for _, method := range []string{"GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE"} {
			for _, path := range []string{"/api/v1/gateway-pools", "/api/v1/users"} {
				t.Run(role+"/"+method+path, func(t *testing.T) {
					r := httptest.NewRequest(method, path, nil)
					if role != "" {
						r = r.WithContext(context.WithValue(r.Context(), identityContextKey{}, core.User{ID: "user", Role: role, Enabled: true}))
					}
					called := false
					h := s.authorizeRole(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(204) }))
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					want := 204
					if role == "" {
						want = 401
					} else if role == "viewer" && (path == "/api/v1/users" || method != "GET" && method != "HEAD" && method != "OPTIONS") {
						want = 403
					}
					if w.Code != want || called != (want == 204) {
						t.Fatalf("role boundary = %d invoked=%t want=%d", w.Code, called, want)
					}
				})
			}
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	browser := core.User{ID: "browser"}
	bearer := core.User{ID: "bearer"}
	r = r.WithContext(context.WithValue(r.Context(), sessionKey{}, session{User: browser}))
	got, ok := requestUser(r)
	if !ok || got.ID != browser.ID {
		t.Fatal("browser identity not resolved")
	}
	r = r.WithContext(context.WithValue(r.Context(), identityContextKey{}, bearer))
	got, ok = requestUser(r)
	if !ok || got.ID != bearer.ID {
		t.Fatal("authenticated request identity did not take precedence")
	}
}

func identityBoundaryRequest(h http.Handler, method, path, body, reauth, match string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer test-admin-token")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-NSC-Reauthenticate", reauth)
	r.Header.Set("If-Match", match)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestUserHTTPEditAndTokenPreconditions(t *testing.T) {
	s, h := newTestServer(t)
	ctx := context.Background()
	user, err := s.core.SaveUser(ctx, "", core.UserInput{Username: "reader", Role: "viewer", Enabled: true, Password: "reader-password-123456"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"username":"reader","role":"viewer","enabled":true}`
	for _, test := range []struct {
		name, method, path, body, reauth, match string
		want                                    int
	}{
		{"create malformed", "POST", "/api/v1/users", "{", "test-admin-token", "", 400},
		{"update reauthenticate", "PUT", "/api/v1/users/" + user.ID, body, "", "\"rev-1\"", 403},
		{"update missing revision", "PUT", "/api/v1/users/" + user.ID, body, "test-admin-token", "", 428},
		{"update malformed revision", "PUT", "/api/v1/users/" + user.ID, body, "test-admin-token", "bad", 428},
		{"update malformed body", "PUT", "/api/v1/users/" + user.ID, "{", "test-admin-token", "\"rev-1\"", 400},
		{"token reauthenticate", "POST", "/api/v1/users/" + user.ID + "/token", "", "", "\"rev-1\"", 403},
		{"token missing revision", "POST", "/api/v1/users/" + user.ID + "/token", "", "test-admin-token", "", 428},
		{"token stale revision", "POST", "/api/v1/users/" + user.ID + "/token", "", "test-admin-token", "\"rev-8\"", 412},
		{"token unknown user", "POST", "/api/v1/users/missing/token", "", "test-admin-token", "\"rev-1\"", 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := identityBoundaryRequest(h, test.method, test.path, test.body, test.reauth, test.match)
			if w.Code != test.want {
				t.Fatalf("HTTP %d want %d: %s", w.Code, test.want, w.Body)
			}
			if strings.Contains(w.Body.String(), "reader-password") || strings.Contains(w.Body.String(), "password_hash") || strings.Contains(w.Body.String(), "token_hash") {
				t.Fatal("credential material leaked")
			}
		})
	}
	updated := identityBoundaryRequest(h, "PUT", "/api/v1/users/"+user.ID, body, "test-admin-token", "\"rev-1\"")
	if updated.Code != 200 {
		t.Fatalf("valid update: %d %s", updated.Code, updated.Body)
	}
	user, err = s.core.User(ctx, user.ID)
	if err != nil || user.Revision != 2 {
		t.Fatalf("update not persisted: %+v %v", user, err)
	}
	tokenResponse := identityBoundaryRequest(h, "POST", "/api/v1/users/"+user.ID+"/token", "", "test-admin-token", fmt.Sprintf("\"rev-%d\"", user.Revision))
	if tokenResponse.Code != 200 {
		t.Fatalf("token response: %d %s", tokenResponse.Code, tokenResponse.Body)
	}
	var response struct {
		Data map[string]string `json:"data"`
	}
	if err = json.Unmarshal(tokenResponse.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data["token"] == "" || response.Data["csrf_token"] != "" {
		t.Fatal("bearer request received browser session state")
	}
	if got, e := s.core.Authenticate(ctx, "", "", response.Data["token"]); e != nil || got.ID != user.ID {
		t.Fatalf("returned token is unusable: %+v %v", got, e)
	}
	list := identityBoundaryRequest(h, "GET", "/api/v1/users", "", "", "")
	if list.Code != 200 || strings.Contains(list.Body.String(), response.Data["token"]) {
		t.Fatalf("user list leaked token or failed: %d", list.Code)
	}
}

func TestBrowserSelfTokenRotationRefreshesSessionAndCSRF(t *testing.T) {
	s, h := newTestServer(t)
	cookie, csrf := browserLogin(t, h)
	owner, err := s.core.Authenticate(context.Background(), "admin", "test-admin-token", "")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/v1/users/"+owner.ID+"/token", nil)
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("X-NSC-Reauthenticate", "test-admin-token")
	r.Header.Set("If-Match", fmt.Sprintf("\"rev-%d\"", owner.Revision))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("self rotation: %d %s", w.Code, w.Body)
	}
	var response struct {
		Data map[string]string `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data["csrf_token"] == "" || response.Data["csrf_token"] == csrf || len(w.Result().Cookies()) != 1 {
		t.Fatal("rotation did not refresh browser session")
	}
	if old := browserRequest(h, "GET", "/api/v1/gateway-pools", "", cookie, ""); old.Code != 401 {
		t.Fatalf("stale session survived rotation: %d", old.Code)
	}
	if fresh := browserRequest(h, "GET", "/api/v1/gateway-pools", "", w.Result().Cookies()[0], ""); fresh.Code != 200 {
		t.Fatalf("new session not usable: %d %s", fresh.Code, fresh.Body)
	}
}

type identityHTTPFailureStore struct {
	storage.Store
	failure       error
	auditContext  context.Context
	audit         model.AuditEvent
	auditCanceled bool
}

func (s *identityHTTPFailureStore) GetControlDocument(context.Context, string) (storage.ControlDocument, error) {
	return storage.ControlDocument{}, s.failure
}
func (s *identityHTTPFailureStore) AppendAuditEvent(ctx context.Context, v model.AuditEvent) error {
	s.auditContext = ctx
	s.audit = v
	s.auditCanceled = ctx.Err() != nil
	return s.failure
}

func TestIdentityHTTPReadFailureAndReauthenticationSources(t *testing.T) {
	s, _ := newTestServer(t)
	owner, err := s.core.Authenticate(context.Background(), "admin", "test-admin-token", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"anonymous", "query password"} {
		r := httptest.NewRequest("POST", "/api/v1/users?password=test-admin-token", nil)
		if kind != "anonymous" {
			r = r.WithContext(context.WithValue(r.Context(), identityContextKey{}, owner))
		}
		w := httptest.NewRecorder()
		if s.reauthenticate(w, r) {
			t.Fatal("missing per-request password accepted")
		}
		want := 403
		if kind == "anonymous" {
			want = 401
		}
		if w.Code != want {
			t.Fatalf("reauth status=%d want=%d", w.Code, want)
		}
	}
	failure := errors.New("identity store unavailable")
	boundary := &identityHTTPFailureStore{Store: s.store, failure: failure}
	s.core = core.New(boundary, s.config, nil)
	router := chi.NewRouter()
	s.registerIdentities(router)
	r := httptest.NewRequest("GET", "/users", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 500 {
		t.Fatalf("failed identity list reported success: %d %s", w.Code, w.Body)
	}
	// Logging outlives a canceled login request, but retains a bounded context.
	s.store = boundary
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = httptest.NewRequest("POST", "/login", nil).WithContext(ctx)
	s.recordLogin(r, "owner", false)
	if boundary.audit.Action != "LOGIN_FAILED" || boundary.auditContext == nil || boundary.auditCanceled {
		t.Fatal("failed login record missing")
	}
	if _, ok := boundary.auditContext.Deadline(); !ok {
		t.Fatal("audit write had no deadline")
	}
}
