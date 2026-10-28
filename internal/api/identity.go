package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type identityContextKey struct{}

func requestUser(r *http.Request) (core.User, bool) {
	if user, ok := r.Context().Value(identityContextKey{}).(core.User); ok {
		return user, true
	}
	if sess, ok := r.Context().Value(sessionKey{}).(session); ok {
		return sess.User, true
	}
	return core.User{}, false
}

func (s *Server) authorizeRole(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := requestUser(r)
		if !ok {
			s.writeProblem(w, r, 401, "auth.required", nil, "")
			return
		}
		// Viewer access is intentionally limited to reads; previews that create
		// jobs and all credential/user endpoints remain administrator-only.
		selfPassword := r.Method == http.MethodPost && r.URL.Path == "/api/v1/me/password"
		userKeys := strings.HasPrefix(r.URL.Path, "/api/v1/users/") && strings.Contains(r.URL.Path, "/api-keys")
		if user.Role != "administrator" && !selfPassword && !userKeys && ((r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS") || strings.HasPrefix(r.URL.Path, "/api/v1/users") || strings.HasPrefix(r.URL.Path, "/api/v1/openapi-keys")) {
			s.writeProblem(w, r, 403, "auth.read_only", nil, "")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// The password is supplied per sensitive request, never persisted in logs or
// accepted from a query parameter. This also covers Bearer clients.
func (s *Server) reauthenticate(w http.ResponseWriter, r *http.Request) bool {
	user, ok := requestUser(r)
	if !ok {
		s.writeProblem(w, r, 401, "auth.required", nil, "")
		return false
	}
	_, err := s.core.Authenticate(r.Context(), user.Username, r.Header.Get("X-NSC-Reauthenticate"), "")
	if err != nil {
		s.writeProblem(w, r, 403, "auth.reauthentication_required", nil, "")
		return false
	}
	return true
}

func (s *Server) registerIdentities(r chi.Router) {
	r.Post("/me/password", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			CurrentPassword string `json:"current_password"`
			NewPassword     string `json:"new_password"`
		}
		if !s.decodeJSON(w, r, &input, 8192) {
			return
		}
		actor, _ := requestUser(r)
		user, err := s.core.ChangePassword(r.Context(), actor.ID, input.CurrentPassword, input.NewPassword)
		if err != nil {
			if err == storage.ErrNotFound {
				s.writeProblem(w, r, http.StatusForbidden, "auth.reauthentication_required", nil, "current_password")
				return
			}
			s.handleError(w, r, err)
			return
		}
		response := map[string]any{"user": user}
		if _, browser := r.Context().Value(sessionKey{}).(session); browser {
			sess, ok := s.issueSession(w, r, user)
			if !ok {
				return
			}
			response["csrf_token"] = sess.CSRF
		}
		writeData(w, http.StatusOK, response)
	})
	r.Get("/users/{userID}/api-keys", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeUserKeys(w, r) {
			return
		}
		keys, err := s.core.ListUserAPIKeys(r.Context(), chi.URLParam(r, "userID"))
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		writeData(w, http.StatusOK, keys)
	})
	r.Post("/users/{userID}/api-keys", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeUserKeys(w, r) || !s.reauthenticate(w, r) {
			return
		}
		var input struct {
			Name      string   `json:"name"`
			ProjectID string   `json:"project_id"`
			Scopes    []string `json:"scopes"`
		}
		if !s.decodeJSON(w, r, &input, 8192) {
			return
		}
		actor, _ := requestUser(r)
		if actor.Role != "administrator" {
			for _, scope := range input.Scopes {
				if !strings.HasSuffix(scope, ".read") {
					s.writeProblem(w, r, http.StatusForbidden, "auth.read_only", nil, "scopes")
					return
				}
			}
		}
		key, token, err := s.core.IssueUserAPIKey(r.Context(), input.Name, input.ProjectID, chi.URLParam(r, "userID"), actor.ID, input.Scopes)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		setETag(w, key.Revision)
		writeData(w, http.StatusCreated, struct {
			core.OpenAPIKey
			Token string `json:"token"`
		}{key, token})
	})
	r.Delete("/users/{userID}/api-keys/{keyID}", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeUserKeys(w, r) || !s.reauthenticate(w, r) {
			return
		}
		expected, err := parseIfMatch(r)
		if err == nil {
			err = s.core.RevokeUserAPIKey(r.Context(), chi.URLParam(r, "userID"), chi.URLParam(r, "keyID"), expected)
		}
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	r.Get("/users", func(w http.ResponseWriter, r *http.Request) {
		users, err := s.core.Users(r.Context())
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		writeData(w, 200, users)
	})
	r.Post("/users", func(w http.ResponseWriter, r *http.Request) {
		if !s.reauthenticate(w, r) {
			return
		}
		var in core.UserInput
		if !s.decodeJSON(w, r, &in, 8192) {
			return
		}
		user, err := s.core.SaveUser(r.Context(), "", in, 0)
		s.controlResponse(w, r, user, user.Revision, err, 201)
	})
	r.Put("/users/{userID}", func(w http.ResponseWriter, r *http.Request) {
		if !s.reauthenticate(w, r) {
			return
		}
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		var in core.UserInput
		if !s.decodeJSON(w, r, &in, 8192) {
			return
		}
		user, err := s.core.SaveUser(r.Context(), chi.URLParam(r, "userID"), in, expected)
		s.controlResponse(w, r, user, user.Revision, err, 200)
	})
	r.Post("/users/{userID}/token", func(w http.ResponseWriter, r *http.Request) {
		if !s.reauthenticate(w, r) {
			return
		}
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		token, err := s.core.RotateUserToken(r.Context(), chi.URLParam(r, "userID"), expected)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		response := map[string]string{"token": token}
		actor, _ := requestUser(r)
		if _, browser := r.Context().Value(sessionKey{}).(session); browser && actor.ID == chi.URLParam(r, "userID") {
			updated, err := s.core.User(r.Context(), actor.ID)
			if err != nil {
				s.handleError(w, r, err)
				return
			}
			sess, ok := s.issueSession(w, r, updated)
			if !ok {
				return
			}
			response["csrf_token"] = sess.CSRF
		}
		writeData(w, 200, response)
	})
}

func (s *Server) authorizeUserKeys(w http.ResponseWriter, r *http.Request) bool {
	actor, ok := requestUser(r)
	if !ok || (actor.Role != "administrator" && actor.ID != chi.URLParam(r, "userID")) {
		s.writeProblem(w, r, http.StatusNotFound, "resource.not_found", nil, "")
		return false
	}
	return true
}

func (s *Server) recordLogin(r *http.Request, user string, success bool) {
	if s.config.Controller.RecoveryMode {
		return
	}
	action := "LOGIN_FAILED"
	if success {
		action = "LOGIN_SUCCEEDED"
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()
	if err := s.store.AppendAuditEvent(ctx, model.AuditEvent{ID: id.New(id.Audit), Actor: user, Action: action, ResourceType: "identity", ResourceID: user, CreatedAt: time.Now().UTC()}); err != nil {
		s.logger.ErrorContext(ctx, "record login event", "error", err)
	}
}
