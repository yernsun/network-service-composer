package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

func (s *Server) registerInstallation(router chi.Router) {
	router.Route("/system/installation", func(routes chi.Router) {
		routes.Use(s.installationAdministrator)
		routes.Get("/", s.systemInstallation)
		routes.Post("/plans", s.planInstallation)
		routes.Post("/operations", s.startInstallation)
		routes.Get("/operations/{operationID}", s.installationOperation)
	})
}
func (s *Server) installationAdministrator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := requestUser(r)
		if !ok || user.Role != "administrator" {
			s.writeProblem(w, r, http.StatusForbidden, "auth.read_only", nil, "")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
func (s *Server) installationError(w http.ResponseWriter, r *http.Request, err error) {
	var blocked *bootstrap.MaintenanceError
	if errors.As(err, &blocked) {
		s.writeProblem(w, r, http.StatusConflict, "installation.blocked", map[string]any{"reason": blocked.Code}, "")
		return
	}
	s.handleError(w, r, err)
}
func (s *Server) systemInstallation(w http.ResponseWriter, r *http.Request) {
	value, err := s.core.SystemInstallation(r.Context())
	if err != nil {
		s.installationError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}
func (s *Server) planInstallation(w http.ResponseWriter, r *http.Request) {
	var input bootstrap.PlanRequest
	if !s.decodeJSON(w, r, &input, 64<<10) {
		return
	}
	value, err := s.core.PlanInstallation(r.Context(), input)
	if err != nil {
		s.installationError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, value)
}
func (s *Server) startInstallation(w http.ResponseWriter, r *http.Request) {
	var input bootstrap.ExecuteRequest
	if !s.decodeJSON(w, r, &input, 16<<10) {
		return
	}
	input.IdempotencyKey = r.Header.Get("Idempotency-Key")
	input.Actor = requestActor(r)
	value, err := s.core.StartInstallation(r.Context(), input)
	if err != nil {
		s.installationError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/system/installation/operations/"+value.ID)
	writeData(w, http.StatusAccepted, value)
}
func (s *Server) installationOperation(w http.ResponseWriter, r *http.Request) {
	value, err := s.core.InstallationOperation(r.Context(), chi.URLParam(r, "operationID"))
	if err != nil {
		s.installationError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}
