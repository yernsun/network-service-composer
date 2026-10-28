package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type loginNonceBoundaryStore struct{ storage.Store }

func (s *loginNonceBoundaryStore) AppendAuditEvent(ctx context.Context, event model.AuditEvent) error {
	e := s.Store.AppendAuditEvent(ctx, event)
	if event.Action == "LOGIN_SUCCEEDED" {
		rand.Reader = sessionNonceFailure{}
	}
	return e
}
func TestLoginDoesNotIssueCookiesWhenNonceGenerationFails(t *testing.T) {
	for _, mode := range []string{"form", "json"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := newTestServer(t)
			s.store = &loginNonceBoundaryStore{Store: s.store}
			path, body, contentType := "/login", "username=admin&password=test-admin-token", "application/x-www-form-urlencoded"
			if mode == "json" {
				path, body, contentType = "/api/v1/auth/login", `{"username":"admin","password":"test-admin-token"}`, "application/json"
			}
			r := httptest.NewRequest("POST", path, strings.NewReader(body))
			r.Header.Set("Content-Type", contentType)
			w := httptest.NewRecorder()
			original := rand.Reader
			func() {
				defer func() { rand.Reader = original }()
				if mode == "json" {
					s.loginJSON(w, r)
				} else {
					s.login(w, r)
				}
			}()
			if w.Code != 500 || len(w.Result().Cookies()) != 0 || !strings.Contains(w.Body.String(), "session.create_failed") || w.Header().Get("Location") != "" {
				t.Fatalf("failed session was accepted: %d %s", w.Code, w.Body)
			}
		})
	}
}
func TestSignedSessionRequiresValidPayloadAndEnabledUser(t *testing.T) {
	s, _ := newTestServer(t)
	for _, raw := range []string{"{", `{"exp":"not a timestamp"}`, `null`} {
		mac := hmac.New(sha256.New, []byte(s.config.Security.SessionKey))
		io.WriteString(mac, raw)
		cookie := base64.RawURLEncoding.EncodeToString([]byte(raw)) + "." + hex.EncodeToString(mac.Sum(nil))
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Cookie", "nsc_session="+cookie)
		if _, ok := s.readSession(r); ok {
			t.Fatal("signed malformed/empty identity accepted")
		}
	}
	u, e := s.core.SaveUser(context.Background(), "", core.UserInput{Username: "disabled", Role: "viewer", Password: "disabled-password-123456", Enabled: false}, 0)
	if e != nil {
		t.Fatal(e)
	}
	cookie, e := s.signSession(session{UserID: u.ID, UserRevision: u.Revision, Expires: time.Now().Add(time.Hour).Unix()})
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Cookie", "nsc_session="+cookie)
	if _, ok := s.readSession(r); ok {
		t.Fatal("disabled identity accepted with matching revision")
	}
}

func TestJSONLoginRejectsInvalidCredentialsWithoutAuthenticatedState(t *testing.T) {
	s, _ := newTestServer(t)
	r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"incorrect-password"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.loginJSON(w, r)
	if w.Code != 401 || len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "incorrect-password") {
		t.Fatalf("failed login leaked credentials or created session: %d %s", w.Code, w.Body)
	}
	if actor := requestActor(r); actor != "unauthenticated" {
		t.Fatal("unauthenticated request invented an actor", actor)
	}
}
