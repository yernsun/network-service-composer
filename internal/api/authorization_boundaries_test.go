package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
)

// Walk the real router so newly added mutation routes inherit this protection
// check, including routes that are not reachable from the current UI. The
// self-service exceptions have dedicated owner and reauthentication tests.
func TestResourceMutationAuthorizationBoundary(t *testing.T) {
	s, handler := newTestServerWithConfig(t, func(c *config.Config) {
		c.Server.RateLimitRPS = 100000
		c.Server.RateLimitBurst = 100000
	})
	user, err := s.core.SaveUser(context.Background(), "", core.UserInput{
		Username: "boundary-reader", Role: "viewer", Enabled: true, Password: "reader-password-123456",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.core.RotateUserToken(context.Background(), user.ID, user.Revision)
	if err != nil {
		t.Fatal(err)
	}
	parameters := regexp.MustCompile(`\{[^}]+\}`)
	checked := 0
	err = chi.Walk(handler.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/v1/") || strings.HasPrefix(route, "/api/v1/auth/") || strings.HasPrefix(route, "/api/v1/hooks/") || route == "/api/v1/locale" {
			return nil
		}
		if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch && method != http.MethodDelete {
			return nil
		}
		checked++
		path := parameters.ReplaceAllString(route, "nonexistent-resource")
		selfService := route == "/api/v1/me/password" || strings.HasPrefix(route, "/api/v1/users/{userID}/api-keys")
		for _, actor := range []struct {
			name, token string
			status      int
		}{{"anonymous", "", http.StatusUnauthorized}, {"viewer", token, http.StatusForbidden}} {
			if actor.name == "viewer" && selfService {
				continue
			}
			t.Run(actor.name+"/"+method+"/"+route, func(t *testing.T) {
				// Invalid JSON and unknown IDs must not bypass authentication or
				// expose handler validation results to an unauthorized caller.
				r := httptest.NewRequest(method, path, strings.NewReader(`{"invalid":`))
				r.Header.Set("Content-Type", "application/json")
				if actor.token != "" {
					r.Header.Set("Authorization", "Bearer "+actor.token)
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != actor.status {
					t.Fatalf("want %d, got %d: %s", actor.status, w.Code, w.Body.String())
				}
				if strings.Contains(w.Body.String(), actor.token) && actor.token != "" {
					t.Fatal("response leaked the authentication token")
				}
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 20 {
		t.Fatalf("router walk did not discover expected mutation surface: %d routes", checked)
	}
	t.Logf("checked anonymous and viewer authorization on %d mutation routes", checked)
}
