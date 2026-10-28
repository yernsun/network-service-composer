package api

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) registerSystemHA(router chi.Router) {
	router.Get("/system/ha", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		user, ok := requestUser(r)
		if !ok || user.Role != "administrator" {
			s.writeProblem(w, r, 403, "auth.read_only", nil, "")
			return
		}
		if len(r.URL.Query()) != 0 {
			s.writeProblem(w, r, 400, "validation.filter_invalid", nil, "")
			return
		}
		value, err := s.core.SystemHA(r.Context())
		s.controlResponse(w, r, value, 0, err, http.StatusOK)
	})
}
