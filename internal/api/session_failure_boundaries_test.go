package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/storage"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type sessionNonceFailure struct{}

func (sessionNonceFailure) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type rotatedSessionStore struct {
	storage.Store
	written      bool
	failRead     bool
	nonceFailure bool
}

func (s *rotatedSessionStore) WriteControlDocuments(ctx context.Context, w []storage.ControlWrite) error {
	e := s.Store.WriteControlDocuments(ctx, w)
	if e == nil {
		s.written = true
	}
	return e
}
func (s *rotatedSessionStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if s.written && s.failRead {
		return storage.ControlDocument{}, errors.New("private identity storage failure")
	}
	d, e := s.Store.GetControlDocument(ctx, key)
	if s.written && s.nonceFailure {
		rand.Reader = sessionNonceFailure{}
	}
	return d, e
}
func TestRotatedBrowserIdentityDoesNotIssueAFalseSessionOnFailure(t *testing.T) {
	for _, stage := range []string{"read failure", "nonce failure", "other user"} {
		t.Run(stage, func(t *testing.T) {
			s, _ := newTestServer(t)
			ctx := context.Background()
			owner, e := s.core.Authenticate(ctx, "admin", "test-admin-token", "")
			if e != nil {
				t.Fatal(e)
			}
			target := owner
			if stage == "other user" {
				target, e = s.core.SaveUser(ctx, "", core.UserInput{Username: "other", Role: "viewer", Enabled: true, Password: "other-password-123456"}, 0)
				if e != nil {
					t.Fatal(e)
				}
			}
			wrapper := &rotatedSessionStore{Store: s.store, failRead: stage == "read failure", nonceFailure: stage == "nonce failure"}
			s.core = core.New(wrapper, s.config, nil)
			router := chi.NewRouter()
			s.registerIdentities(router)
			req := httptest.NewRequest("POST", "/users/"+target.ID+"/token", nil)
			req.Header.Set("X-NSC-Reauthenticate", "test-admin-token")
			req.Header.Set("If-Match", fmt.Sprintf("\"rev-%d\"", target.Revision))
			req = req.WithContext(context.WithValue(req.Context(), sessionKey{}, session{User: owner}))
			response := httptest.NewRecorder()
			original := rand.Reader
			func() { defer func() { rand.Reader = original }(); router.ServeHTTP(response, req) }()
			want := 500
			if stage == "other user" {
				want = 200
			}
			if response.Code != want || len(response.Result().Cookies()) != 0 || strings.Contains(response.Body.String(), "private identity") {
				t.Fatalf("unsafe session result: %d %s", response.Code, response.Body)
			}
			if stage != "other user" && strings.Contains(response.Body.String(), "\"token\"") {
				t.Fatal("failed session returned a token as success")
			}
			plain := core.New(wrapper.Store, s.config, nil)
			updated, e := plain.User(ctx, target.ID)
			if e != nil || updated.Revision != target.Revision+1 {
				t.Fatalf("accepted token rotation history lost: %+v %v", updated, e)
			}
		})
	}
}
func TestSessionRejectsStaleAndMissingIdentity(t *testing.T) {
	s, _ := newTestServer(t)
	u, e := s.core.Authenticate(context.Background(), "admin", "test-admin-token", "")
	if e != nil {
		t.Fatal(e)
	}
	for _, value := range []session{{UserID: u.ID, UserRevision: u.Revision + 1, Expires: time.Now().Add(time.Hour).Unix()}, {UserID: "missing", UserRevision: 1, Expires: time.Now().Add(time.Hour).Unix()}} {
		cookie, e := s.signSession(value)
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Cookie", "nsc_session="+cookie)
		if _, ok := s.readSession(r); ok {
			t.Fatal("stale or missing identity session accepted")
		}
	}
}
