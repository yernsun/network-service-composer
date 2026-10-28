package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
)

func (s *Server) registerDomainDrafts(r chi.Router) {
	r.Get("/environments/{id}/domain-drafts", func(w http.ResponseWriter, r *http.Request) {
		page := parsePage(r)
		values, err := s.core.ListDomainDrafts(r.Context(), chi.URLParam(r, "id"), page)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		writeCollection(w, values, page, func(value core.DomainDraft) string { return value.ID })
	})
	r.Post("/environments/{id}/domain-drafts", func(w http.ResponseWriter, r *http.Request) {
		var input core.DomainDraftInput
		if !s.decodeJSON(w, r, &input, 16384) {
			return
		}
		value, err := s.core.CreateDomainDraft(r.Context(), chi.URLParam(r, "id"), input)
		s.controlResponse(w, r, value, value.Revision, err, http.StatusCreated)
	})
	r.Get("/domain-drafts/{id}", func(w http.ResponseWriter, r *http.Request) {
		value, err := s.core.GetDomainDraft(r.Context(), chi.URLParam(r, "id"))
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
	r.Put("/domain-drafts/{id}", func(w http.ResponseWriter, r *http.Request) {
		expected, err := parseIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		var input core.DomainDraftInput
		if !s.decodeJSON(w, r, &input, 16384) {
			return
		}
		value, err := s.core.UpdateDomainDraft(r.Context(), chi.URLParam(r, "id"), input, expected)
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
	r.Delete("/domain-drafts/{id}", func(w http.ResponseWriter, r *http.Request) {
		expected, err := parseIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		if err = s.core.DeleteDomainDraft(r.Context(), chi.URLParam(r, "id"), expected); err != nil {
			s.handleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	r.Post("/domain-drafts/{id}/publish", func(w http.ResponseWriter, r *http.Request) {
		expected, err := parseIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		value, err := s.core.PublishDomainDraft(r.Context(), chi.URLParam(r, "id"), expected)
		s.controlResponse(w, r, value, value.Revision, err, http.StatusAccepted)
	})
}
