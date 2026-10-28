package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
)

func (s *Server) registerCommunity(r chi.Router) {
	r.Post("/services/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		var in struct {
			AcknowledgeDataBoundary bool `json:"acknowledge_data_boundary"`
		}
		if !s.decodeJSON(w, r, &in, 8192) {
			return
		}
		v, err := s.core.QueueDeleteService(r.Context(), chi.URLParam(r, "id"), requestActor(r), r.Header.Get("Idempotency-Key"), expected, in.AcknowledgeDataBoundary)
		s.controlResponse(w, r, v, v.Revision, err, 202)
	})
	r.Post("/services/{id}/revisions/{revisionID}/restore-preview", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.core.ReviewServiceRestore(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "revisionID"))
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		writeData(w, 200, v)
	})
	r.Post("/services/{id}/restore", func(w http.ResponseWriter, r *http.Request) {
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		var in struct {
			ReviewID                string `json:"review_id"`
			AcknowledgeDataBoundary bool   `json:"acknowledge_data_boundary"`
		}
		if !s.decodeJSON(w, r, &in, 8192) {
			return
		}
		v, err := s.core.RestoreService(r.Context(), chi.URLParam(r, "id"), in.ReviewID, requestActor(r), r.Header.Get("Idempotency-Key"), expected, in.AcknowledgeDataBoundary)
		s.controlResponse(w, r, v, v.Revision, err, 202)
	})
	r.Post("/applications/{id}/revisions/{revisionID}/restore-preview", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.core.ReviewApplicationRestore(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "revisionID"))
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		writeData(w, 200, v)
	})
	r.Post("/applications/{id}/restore", func(w http.ResponseWriter, r *http.Request) {
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		var in struct {
			ReviewID                string `json:"review_id"`
			AcknowledgeDataBoundary bool   `json:"acknowledge_data_boundary"`
		}
		if !s.decodeJSON(w, r, &in, 8192) {
			return
		}
		v, err := s.core.RestoreApplication(r.Context(), chi.URLParam(r, "id"), in.ReviewID, requestActor(r), r.Header.Get("Idempotency-Key"), expected, in.AcknowledgeDataBoundary)
		s.controlResponse(w, r, v, v.Revision, err, 202)
	})
	r.Get("/activity", func(w http.ResponseWriter, r *http.Request) {
		page := parsePage(r)
		values, err := s.store.ListAuditEvents(r.Context(), page)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		writeCollection(w, values, page, func(v model.AuditEvent) string { return v.ID })
	})
	r.Get("/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		page := parsePage(r)
		q := r.URL.Query()
		values, err := s.core.IssuesFiltered(r.Context(), page, core.IssueFilter{State: q.Get("state"), ResourceType: q.Get("resource_type"), ResourceID: q.Get("resource_id"), EnvironmentID: q.Get("environment_id"), ProjectID: q.Get("project_id"), Code: q.Get("code")})
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		writeCollection(w, values, page, func(v core.Issue) string { return v.ID })
	})
	r.Post("/issues/{issueID}/acknowledge", func(w http.ResponseWriter, r *http.Request) {
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		v, err := s.core.AcknowledgeIssue(r.Context(), chi.URLParam(r, "issueID"), expected, requestActor(r))
		s.controlResponse(w, r, v, v.Revision, err, 200)
	})
	r.Put("/issues/{issueID}/responsibility", func(w http.ResponseWriter, r *http.Request) {
		expected, err := parseControlIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		var input struct {
			UserID string `json:"user_id"`
		}
		if !s.decodeJSON(w, r, &input, 1024) {
			return
		}
		v, err := s.core.AssignIssue(r.Context(), chi.URLParam(r, "issueID"), input.UserID, expected)
		s.controlResponse(w, r, v, v.Revision, err, 200)
	})
	r.Get("/gateways/{id}/evidence", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.core.GatewayEvidence(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		writeData(w, 200, v)
	})
}

func applyEvidenceStatus(g *model.Gateway) {
	if g.Evidence == nil || !g.Enabled || g.Status == model.GatewayIncompatible || g.Status == model.GatewayApplyFailed {
		return
	}
	if g.Evidence.StartupState == "RISK" || g.Evidence.ConfigurationState == "EXTERNAL_CHANGE" {
		g.Status = model.GatewayDegraded
	} else if g.Evidence.ConfigurationState == "UNKNOWN" && g.Status == model.GatewayReady {
		g.Status = model.GatewayStale
	}
}
