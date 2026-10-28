package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
)

// Mounted inside the administrator-only, no-store notifications group. Every
// mutation additionally requires reauthentication and a revision receipt.
func (s *Server) registerNotificationRouting(r chi.Router) {
	r.Get("/notification-receivers", func(w http.ResponseWriter, r *http.Request) {
		values, err := s.core.ListNotificationReceivers(r.Context())
		s.controlResponse(w, r, values, 0, err, http.StatusOK)
	})
	r.Get("/notification-routes", func(w http.ResponseWriter, r *http.Request) {
		values, err := s.core.ListNotificationRoutes(r.Context())
		s.controlResponse(w, r, values, 0, err, http.StatusOK)
	})
	r.Get("/notification-silences", func(w http.ResponseWriter, r *http.Request) {
		values, err := s.core.ListNotificationSilences(r.Context())
		s.controlResponse(w, r, values, 0, err, http.StatusOK)
	})
	r.Get("/notification-health", func(w http.ResponseWriter, r *http.Request) {
		value, err := s.core.NotificationHealth(r.Context())
		s.controlResponse(w, r, value, 0, err, http.StatusOK)
	})
	r.Post("/notification-routing/preview", func(w http.ResponseWriter, r *http.Request) {
		var input core.NotificationPreviewInput
		if !s.decodeJSON(w, r, &input, 2048) {
			return
		}
		value, err := s.core.PreviewNotificationRouting(r.Context(), input)
		s.controlResponse(w, r, value, 0, err, http.StatusOK)
	})
	r.Put("/notification-receivers/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !s.reauthenticate(w, r) {
			return
		}
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		var input core.NotificationReceiverInput
		if !s.decodeJSON(w, r, &input, 8192) {
			return
		}
		value, err := s.core.SaveNotificationReceiver(r.Context(), chi.URLParam(r, "id"), input, expected)
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
	r.Post("/notification-receivers/{id}/test", func(w http.ResponseWriter, r *http.Request) {
		if !s.reauthenticate(w, r) {
			return
		}
		expected, err := parseControlIfMatch(r)
		if err == nil {
			err = s.core.TestNotificationReceiver(r.Context(), chi.URLParam(r, "id"), expected)
		}
		s.controlResponse(w, r, map[string]string{"status": "QUEUED"}, 0, err, http.StatusAccepted)
	})
	r.Put("/notification-routes", func(w http.ResponseWriter, r *http.Request) {
		if !s.reauthenticate(w, r) {
			return
		}
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		var input core.NotificationRouteInput
		if !s.decodeJSON(w, r, &input, 8192) {
			return
		}
		value, err := s.core.SaveNotificationRoute(r.Context(), input, expected)
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
	r.Put("/notification-silences/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !s.reauthenticate(w, r) {
			return
		}
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		var input core.NotificationSilenceInput
		if !s.decodeJSON(w, r, &input, 4096) {
			return
		}
		value, err := s.core.SaveNotificationSilence(r.Context(), chi.URLParam(r, "id"), input, expected, requestActor(r))
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
	r.Post("/notification-silences/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if !s.reauthenticate(w, r) {
			return
		}
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		value, err := s.core.CancelNotificationSilence(r.Context(), chi.URLParam(r, "id"), expected, requestActor(r))
		s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
	})
}
