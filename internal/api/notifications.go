package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
)

func (s *Server) registerNotifications(router chi.Router) {
	router.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				user, ok := requestUser(r)
				if !ok || user.Role != "administrator" {
					s.writeProblem(w, r, http.StatusForbidden, "auth.read_only", nil, "")
					return
				}
				w.Header().Set("Cache-Control", "no-store")
				next.ServeHTTP(w, r)
			})
		})
		r.Get("/notification-settings", func(w http.ResponseWriter, r *http.Request) {
			value, err := s.core.GetNotificationSettings(r.Context())
			s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
		})
		r.Put("/notification-settings", func(w http.ResponseWriter, r *http.Request) {
			if !s.reauthenticate(w, r) {
				return
			}
			expected, err := parseControlIfMatch(r)
			if err != nil {
				s.handleError(w, r, err)
				return
			}
			var input core.NotificationSettingsInput
			if !s.decodeJSON(w, r, &input, 8192) {
				return
			}
			value, err := s.core.SaveNotificationSettings(r.Context(), input, expected)
			s.controlResponse(w, r, value, value.Revision, err, http.StatusOK)
		})
		r.Get("/notifications", func(w http.ResponseWriter, r *http.Request) {
			page := parsePage(r)
			q := r.URL.Query()
			values, err := s.core.ListNotificationsFiltered(r.Context(), page, core.NotificationFilter{ReceiverID: q.Get("receiver_id"), IssueID: q.Get("issue_id"), EnvironmentID: q.Get("environment_id"), ProjectID: q.Get("project_id"), Status: q.Get("status"), EventID: q.Get("event_id"), ResourceID: q.Get("resource_id")})
			if err != nil {
				s.handleError(w, r, err)
				return
			}
			writeCollection(w, values, page, func(v core.Notification) string { return v.ID })
		})
		s.registerNotificationRouting(r)
		r.Post("/notifications/test", func(w http.ResponseWriter, r *http.Request) {
			if !s.reauthenticate(w, r) {
				return
			}
			expected, err := parseControlIfMatch(r)
			if err == nil {
				err = s.core.TestNotification(r.Context(), expected)
			}
			s.controlResponse(w, r, map[string]string{"status": "QUEUED"}, 0, err, http.StatusAccepted)
		})
		r.Post("/notifications/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
			if !s.reauthenticate(w, r) {
				return
			}
			expected, err := parseControlIfMatch(r)
			if err != nil {
				s.handleError(w, r, err)
				return
			}
			value, err := s.core.RetryNotification(r.Context(), chi.URLParam(r, "id"), expected)
			s.controlResponse(w, r, value, value.Revision, err, http.StatusAccepted)
		})
	})
}
