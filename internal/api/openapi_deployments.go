package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type openAPIKeyContextKey struct{}

func requestOpenAPIKey(r *http.Request) (core.OpenAPIKey, bool) {
	key, ok := r.Context().Value(openAPIKeyContextKey{}).(core.OpenAPIKey)
	return key, ok
}

// The deployment API uses a separate credential and route tree. Neither a
// console session nor a user Bearer token can grant access to these routes.
func (s *Server) openAPIKeyAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		values := r.Header.Values("X-NSC-API-Key")
		_, sessionErr := r.Cookie("nsc_session")
		_, hasAuthorization := r.Header["Authorization"]
		if len(values) != 1 || hasAuthorization || sessionErr == nil {
			s.writeProblem(w, r, http.StatusUnauthorized, "auth.required", nil, "")
			return
		}
		key, err := s.core.AuthenticateOpenAPIKey(r.Context(), values[0])
		if err != nil {
			s.writeProblem(w, r, http.StatusUnauthorized, "auth.required", nil, "")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), openAPIKeyContextKey{}, key)))
	})
}

func (s *Server) openAPIEnvironmentScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := requestOpenAPIKey(r)
		if !ok {
			s.writeProblem(w, r, http.StatusNotFound, "resource.not_found", nil, "")
			return
		}
		if key.ProjectScoped() {
			env, err := s.core.GetEnvironment(r.Context(), chi.URLParam(r, "id"))
			if err != nil || env.ProjectID != key.ProjectID || env.System {
				s.writeProblem(w, r, http.StatusNotFound, "resource.not_found", nil, "")
				return
			}
		} else if key.EnvironmentID != chi.URLParam(r, "id") {
			s.writeProblem(w, r, http.StatusNotFound, "resource.not_found", nil, "")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) registerOpenAPIDeployments(r chi.Router) {
	r.Use(s.rateLimit, s.openAPIKeyAuth, s.auditAPI)
	r.With(s.openAPIEnvironmentScope).Get("/environments/{id}", s.getOpenAPIEnvironment)
	r.With(s.openAPIEnvironmentScope).Post("/environments/{id}/preflight", s.openAPIPreflight)
	r.With(s.openAPIEnvironmentScope).Post("/environments/{id}/deployments", s.openAPIDeploy)
	r.With(s.openAPIEnvironmentScope).Post("/environments/{id}/service-preflight", s.openAPIServicePreflight)
	r.With(s.openAPIEnvironmentScope).Post("/environments/{id}/service-deployments", s.openAPIDeploy)
	r.Get("/operations/{id}", s.getOpenAPIOperation)
	s.registerProjectOpenAPI(r)
}

func (s *Server) getOpenAPIOperation(w http.ResponseWriter, r *http.Request) {
	key, _ := requestOpenAPIKey(r)
	operation, err := s.store.GetDeploymentOperation(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	if key.ProjectScoped() {
		projectID, environmentID, snapshot, scopeErr := s.store.GetDeploymentOperationScope(r.Context(), operation.ID)
		if scopeErr != nil {
			s.handleError(w, r, scopeErr)
			return
		}
		needed := "services.read"
		if operation.Type == "DOMAIN_ATTACH" || operation.Type == "DOMAIN_DETACH" {
			needed = "domains.read"
		}
		if !snapshot || projectID == "" || environmentID == "" || projectID != key.ProjectID || !key.HasScope(needed) {
			s.handleError(w, r, storage.ErrNotFound)
			return
		}
		setETag(w, operation.Revision)
		writeData(w, http.StatusOK, s.operationResponse(r.Context(), operation))
		return
	}
	// Revision source is the durable environment claim for this operation.
	// An Application can be moved or removed after submission, so the current
	// Application binding is insufficient for authorizing historical evidence.
	if operation.RevisionID == "" {
		s.handleError(w, r, storage.ErrNotFound)
		return
	}
	revision, err := s.store.GetApplicationRevision(r.Context(), operation.RevisionID)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	if revision.ApplicationID != operation.ApplicationID || revision.ConfigurationSource.EnvironmentID != key.EnvironmentID {
		s.handleError(w, r, storage.ErrNotFound)
		return
	}
	setETag(w, operation.Revision)
	writeData(w, http.StatusOK, s.operationResponse(r.Context(), operation))
}

func (s *Server) registerOpenAPIKeyManagement(r chi.Router) {
	r.Get("/openapi-keys", func(w http.ResponseWriter, r *http.Request) {
		keys, err := s.core.ListLegacyOpenAPIKeys(r.Context())
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		writeData(w, http.StatusOK, keys)
	})
	r.Post("/openapi-keys", func(w http.ResponseWriter, r *http.Request) {
		if !s.reauthenticate(w, r) {
			return
		}
		var input struct {
			Name          string `json:"name"`
			EnvironmentID string `json:"environment_id"`
		}
		if !s.decodeJSON(w, r, &input, 8192) {
			return
		}
		key, token, err := s.core.IssueOpenAPIKey(r.Context(), input.Name, input.EnvironmentID, requestActor(r))
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
	r.Delete("/openapi-keys/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !s.reauthenticate(w, r) {
			return
		}
		expected, err := parseIfMatch(r)
		if err == nil {
			err = s.core.RevokeOpenAPIKey(r.Context(), chi.URLParam(r, "id"), expected)
		}
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
