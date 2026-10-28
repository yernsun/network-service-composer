package api

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) registerServiceEntrypoints(r chi.Router) {
	r.Get("/operations/{id}/readiness", func(w http.ResponseWriter, r *http.Request) {
		value, err := s.core.DeploymentReadiness(r.Context(), chi.URLParam(r, "id"))
		w.Header().Set("Cache-Control", "no-store")
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
	r.Post("/services/{id}/domain-bindings/{bindingID}/retry", func(w http.ResponseWriter, r *http.Request) {
		expected, err := parseIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		value, err := s.core.RetryDomainBinding(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "bindingID"), r.Header.Get("Idempotency-Key"), expected)
		s.controlResponse(w, r, value, value.Revision, err, http.StatusAccepted)
	})
	r.Get("/operations/{id}/entrypoint", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		value, err := s.core.ServiceEntrypointStatus(r.Context(), chi.URLParam(r, "id"))
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
	r.Post("/operations/{id}/entrypoint/retry", func(w http.ResponseWriter, r *http.Request) {
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		value, err := s.core.RetryServiceEntrypoint(r.Context(), chi.URLParam(r, "id"), expected)
		s.controlResponse(w, r, value, value.Revision, err, http.StatusAccepted)
	})
}
