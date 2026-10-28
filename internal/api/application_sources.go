package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
)

func (s *Server) authorizeApplicationSource(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := requestUser(r)
	if !ok {
		s.writeProblem(w, r, 401, "auth.required", nil, "")
		return false
	}
	if user.Role != "administrator" {
		s.writeProblem(w, r, 403, "auth.read_only", nil, "")
		return false
	}
	return true
}

func (s *Server) registerApplicationSources(r chi.Router) {
	r.Get("/environments/{id}/deployment-draft", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeApplicationSource(w, r) {
			return
		}
		user, _ := requestUser(r)
		value, err := s.core.ReadDeploymentDraft(r.Context(), chi.URLParam(r, "id"), user.ID)
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
	r.Put("/environments/{id}/deployment-draft", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeApplicationSource(w, r) {
			return
		}
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		var input struct {
			Data json.RawMessage `json:"data"`
		}
		if !s.decodeJSON(w, r, &input, 3<<20) {
			return
		}
		user, _ := requestUser(r)
		value, err := s.core.SaveDeploymentDraft(r.Context(), chi.URLParam(r, "id"), user.ID, input.Data, expected)
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
	r.Delete("/environments/{id}/deployment-draft", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeApplicationSource(w, r) {
			return
		}
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		user, _ := requestUser(r)
		if err = s.core.DeleteDeploymentDraft(r.Context(), chi.URLParam(r, "id"), user.ID, expected); err != nil {
			s.handleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	r.Get("/services/{id}/source", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeApplicationSource(w, r) {
			return
		}
		value, err := s.core.ServiceSource(r.Context(), chi.URLParam(r, "id"))
		s.controlResponse(w, r, value, 0, err, http.StatusOK)
	})
	r.Get("/services/{id}/draft", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeApplicationSource(w, r) {
			return
		}
		value, err := s.core.ServiceDraft(r.Context(), chi.URLParam(r, "id"))
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
	r.Put("/services/{id}/draft", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeApplicationSource(w, r) {
			return
		}
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		var input core.ApplicationDraftInput
		if !s.decodeJSON(w, r, &input, 4<<20) {
			return
		}
		value, err := s.core.SaveServiceDraft(r.Context(), chi.URLParam(r, "id"), input, expected, r.Header.Get("Idempotency-Key"))
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
	read := func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeApplicationSource(w, r) {
			return
		}
		value, err := s.core.ApplicationSource(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "revisionID"))
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		writeData(w, http.StatusOK, value)
	}
	r.Get("/applications/{id}/source", read)
	r.Get("/applications/{id}/revisions/{revisionID}/source", read)
	r.Get("/applications/{id}/draft", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeApplicationSource(w, r) {
			return
		}
		value, err := s.core.ApplicationDraft(r.Context(), chi.URLParam(r, "id"))
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
	r.Put("/applications/{id}/draft", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeApplicationSource(w, r) {
			return
		}
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		var input core.ApplicationDraftInput
		if !s.decodeJSON(w, r, &input, 4<<20) {
			return
		}
		value, err := s.core.SaveApplicationDraft(r.Context(), chi.URLParam(r, "id"), input, expected, r.Header.Get("Idempotency-Key"))
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
}
