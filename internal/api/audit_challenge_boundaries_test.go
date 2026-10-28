package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type auditChallengeStore struct {
	storage.Store
	events    []model.AuditEvent
	failure   error
	reads     int
	challenge storage.Challenge
	ctxErr    error
}

func (s *auditChallengeStore) AppendAuditEvent(ctx context.Context, v model.AuditEvent) error {
	s.events = append(s.events, v)
	s.ctxErr = ctx.Err()
	return s.failure
}
func (s *auditChallengeStore) GetChallenge(context.Context, string, time.Time) (storage.Challenge, error) {
	s.reads++
	return s.challenge, s.failure
}
func TestAuditRecordsMethodScopeAndDigestAfterRequestCancellation(t *testing.T) {
	for _, tc := range []struct {
		method, path, typ, id     string
		nilBody, canceled, failed bool
	}{
		{"GET", "/api/v1/routes/id", "", "", false, false, false}, {"HEAD", "/api/v1/routes/id", "", "", false, false, false}, {"OPTIONS", "/api/v1/routes/id", "", "", false, false, false},
		{"POST", "/api/v1/routes", "routes", "", false, false, false}, {"PATCH", "/api/v1/routes/id", "routes", "id", false, true, false}, {"DELETE", "/ui/services/svc", "services", "svc", true, false, false}, {"POST", "/unknown", "unknown", "", false, false, false}, {"POST", "/api/v2/routes", "unknown", "", false, false, false}, {"POST", "/else/v1/routes", "unknown", "", false, false, false}, {"POST", "/ui", "unknown", "", true, false, true},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			s, _ := newTestServer(t)
			f := &auditChallengeStore{Store: s.store}
			s.store = f
			if tc.failed {
				f.failure = errors.New("audit store unavailable")
			}
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader("private payload"))
			if tc.nilBody {
				request.Body = nil
			}
			if tc.canceled {
				ctx, cancel := context.WithCancel(request.Context())
				cancel()
				request = request.WithContext(ctx)
			}
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Body != nil {
					_, _ = io.Copy(io.Discard, r.Body)
				}
				w.WriteHeader(409)
			})
			w := httptest.NewRecorder()
			s.auditMutations("operator", next).ServeHTTP(w, request)
			if w.Code != 409 {
				t.Fatal("audit changed operation outcome")
			}
			if tc.typ == "" {
				if len(f.events) != 0 {
					t.Fatal("read produced mutation audit")
				}
				return
			}
			if len(f.events) != 1 {
				t.Fatal("mutation audit missing")
			}
			event := f.events[0]
			if event.ResourceType != tc.typ || event.ResourceID != tc.id || event.Actor != "operator" || event.Action != tc.method || f.ctxErr != nil {
				t.Fatal("audit scope or cancellation boundary lost", event, f.ctxErr)
			}
			var details map[string]any
			if e := json.Unmarshal([]byte(event.Details), &details); e != nil {
				t.Fatal(e)
			}
			payload := "private payload"
			if tc.nilBody {
				payload = ""
			}
			hash := sha256.Sum256([]byte(payload))
			if details["request_sha256"] != hex.EncodeToString(hash[:]) || details["status"] != float64(409) || strings.Contains(event.Details, "private payload") {
				t.Fatal("audit digest leaked or misrepresented payload", details)
			}
		})
	}
}
func TestACMEChallengeRejectsInvalidTokensAndUnknownStorageResult(t *testing.T) {
	for _, mode := range []string{"empty", "long", "slash", "backslash", "newline", "missing", "storage failure", "exact", "trailing dot", "host port", "foreign host"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := newTestServer(t)
			f := &auditChallengeStore{Store: s.store, challenge: storage.Challenge{Domain: "example.test", KeyAuthorization: "token.authorization"}}
			s.store = f
			token := "token"
			host := "example.test"
			want := 404
			switch mode {
			case "empty":
				token = ""
			case "long":
				token = strings.Repeat("a", 256)
			case "slash":
				token = "a/b"
			case "backslash":
				token = "a\\b"
			case "newline":
				token = "a\nb"
			case "missing":
				f.failure = storage.ErrNotFound
			case "storage failure":
				f.failure = errors.New("private storage location")
				want = 500
			case "exact":
				want = 200
			case "trailing dot":
				host = "EXAMPLE.TEST."
				want = 200
			case "host port":
				host = "EXAMPLE.TEST.:8080"
				want = 200
			case "foreign host":
				host = "foreign.test"
			}
			r := httptest.NewRequest("GET", "/challenge", nil)
			r.Host = host
			route := chi.NewRouteContext()
			route.URLParams.Add("token", token)
			r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, route))
			w := httptest.NewRecorder()
			s.challenge(w, r)
			if w.Code != want || strings.Contains(w.Body.String(), "private storage location") {
				t.Fatal("challenge result misrepresented", w.Code, w.Body)
			}
			if want == 200 {
				if w.Body.String() != "token.authorization" || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("challenge proof corrupted")
				}
			} else if strings.Contains(w.Body.String(), "token.authorization") {
				t.Fatal("challenge exposed for invalid request")
			}
			if (mode == "empty" || mode == "long" || mode == "slash" || mode == "backslash" || mode == "newline") && f.reads != 0 {
				t.Fatal("invalid token reached storage")
			}
		})
	}
}
