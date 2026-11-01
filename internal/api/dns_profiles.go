package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
)

func (s *Server) registerDNSProfiles(r chi.Router) {
	r.Get("/dns-provider-profiles", func(w http.ResponseWriter, r *http.Request) {
		values, err := s.core.ListDNSProfiles(r.Context())
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		writeData(w, http.StatusOK, values)
	})
	r.Get("/dns-provider-profiles/{reference}", func(w http.ResponseWriter, r *http.Request) {
		if user, ok := requestUser(r); ok && user.Role == "administrator" {
			value, err := s.core.DNSProfileEditDetail(r.Context(), chi.URLParam(r, "reference"))
			s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
			return
		}
		value, err := s.core.GetDNSProfile(r.Context(), chi.URLParam(r, "reference"))
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
	r.Post("/dns-provider-profiles", func(w http.ResponseWriter, r *http.Request) {
		if !s.reauthenticate(w, r) {
			return
		}
		var input core.DNSProfileInput
		if !s.decodeJSON(w, r, &input, 16384) {
			return
		}
		value, err := s.core.SaveDNSProfile(r.Context(), input.Reference, input, 0)
		if err == nil {
			w.Header().Set("Location", "/api/v1/dns-provider-profiles/"+value.Reference)
		}
		s.controlResponse(w, r, value, value.Revision, err, http.StatusCreated)
	})
	r.Put("/dns-provider-profiles/{reference}", func(w http.ResponseWriter, r *http.Request) {
		if !s.reauthenticate(w, r) {
			return
		}
		expected, err := parseIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		var input core.DNSProfileInput
		if !s.decodeJSON(w, r, &input, 16384) {
			return
		}
		value, err := s.core.SaveDNSProfile(r.Context(), chi.URLParam(r, "reference"), input, expected)
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
	r.Patch("/dns-provider-profiles/{reference}", func(w http.ResponseWriter, r *http.Request) {
		if !s.reauthenticate(w, r) {
			return
		}
		expected, err := parseIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		var input core.DNSProfilePatch
		if !s.decodeJSON(w, r, &input, 16384) {
			return
		}
		value, err := s.core.PatchDNSProfile(r.Context(), chi.URLParam(r, "reference"), input, expected)
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
	r.Delete("/dns-provider-profiles/{reference}", func(w http.ResponseWriter, r *http.Request) {
		if !s.reauthenticate(w, r) {
			return
		}
		expected, err := parseIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		if err = s.core.DeleteDNSProfile(r.Context(), chi.URLParam(r, "reference"), expected); err != nil {
			s.handleError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
