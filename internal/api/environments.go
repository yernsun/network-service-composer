package api

import (
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// An absent control-plane draft has revision zero; existing resource APIs
// retain their positive-revision requirement.
func parseControlIfMatch(r *http.Request) (int64, error) {
	if strings.Trim(r.Header.Get("If-Match"), `"`) == "rev-0" {
		return 0, nil
	}
	return parseIfMatch(r)
}

func (s *Server) registerEnvironments(r chi.Router) {
	r.Get("/projects", func(w http.ResponseWriter, r *http.Request) {
		page := parsePage(r)
		v, e := s.core.ListProjects(r.Context(), page)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		writeCollection(w, v, page, func(v core.Project) string { return v.ID })
	})
	r.Post("/projects", func(w http.ResponseWriter, r *http.Request) {
		var in core.ProjectInput
		if !s.decodeJSON(w, r, &in, 0) {
			return
		}
		v, e := s.core.CreateProjectWithEnvironment(r.Context(), in, r.Header.Get("Idempotency-Key"))
		s.controlResponse(w, r, v, v.Revision, e, http.StatusCreated)
	})
	r.Get("/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.core.GetProject(r.Context(), chi.URLParam(r, "id"))
		s.controlResponse(w, r, v, v.Revision, e, 200)
	})
	r.Patch("/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		expected, e := parseIfMatch(r)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		var in core.ProjectSettings
		if !s.decodeJSON(w, r, &in, 8192) {
			return
		}
		v, e := s.core.UpdateProject(r.Context(), chi.URLParam(r, "id"), in, expected)
		s.controlResponse(w, r, v, v.Revision, e, 200)
	})
	r.Post("/projects/{id}/deletion-previews", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Force bool `json:"force"`
		}
		if !s.decodeJSON(w, r, &in, 8192) {
			return
		}
		v, e := s.core.PreviewProjectDeletion(r.Context(), chi.URLParam(r, "id"), in.Force)
		s.controlResponse(w, r, v, v.Revision, e, 200)
	})
	r.Delete("/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		expected, e := parseIfMatch(r)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		var in core.ProjectDeletionInput
		if !s.decodeJSON(w, r, &in, 8192) {
			return
		}
		v, e := s.core.DeleteProject(r.Context(), chi.URLParam(r, "id"), in, expected, r.Header.Get("Idempotency-Key"), requestActor(r))
		if e == nil {
			w.Header().Set("Location", "/api/v1/project-deletions/"+v.ID)
		}
		s.controlResponse(w, r, v, v.Revision, e, 202)
	})
	r.Get("/project-deletions/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.core.GetProjectDeletion(r.Context(), chi.URLParam(r, "id"))
		s.controlResponse(w, r, v, v.Revision, e, 200)
	})
	r.Post("/project-deletions/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		expected, e := parseIfMatch(r)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		var in core.ProjectDeletionInput
		if !s.decodeJSON(w, r, &in, 8192) {
			return
		}
		v, e := s.core.ResumeProjectDeletion(r.Context(), chi.URLParam(r, "id"), in, expected, requestActor(r))
		s.controlResponse(w, r, v, v.Revision, e, 202)
	})
	r.Get("/projects/{id}/environments", func(w http.ResponseWriter, r *http.Request) {
		key := chi.URLParam(r, "id")
		if _, e := s.core.GetProject(r.Context(), key); e != nil {
			s.handleError(w, r, e)
			return
		}
		page := parsePage(r)
		v, e := s.core.ListEnvironments(r.Context(), key, page)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		writeCollection(w, v, page, func(v core.Environment) string { return v.ID })
	})
	r.Post("/projects/{id}/environments", func(w http.ResponseWriter, r *http.Request) {
		var expected int64
		if r.Header.Get("If-Match") != "" {
			var e error
			expected, e = parseIfMatch(r)
			if e != nil {
				s.handleError(w, r, e)
				return
			}
		}
		var in core.EnvironmentInput
		if !s.decodeJSON(w, r, &in, 0) {
			return
		}
		v, e := s.core.CreateEnvironmentChecked(r.Context(), chi.URLParam(r, "id"), in, expected)
		s.controlResponse(w, r, v, v.Revision, e, 201)
	})
	r.Get("/environments/{id}", s.getEnvironment)
	r.Patch("/environments/{id}", func(w http.ResponseWriter, r *http.Request) {
		expected, e := parseIfMatch(r)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		var in core.EnvironmentSettings
		if !s.decodeJSON(w, r, &in, 8192) {
			return
		}
		v, e := s.core.UpdateEnvironment(r.Context(), chi.URLParam(r, "id"), in, expected)
		s.controlResponse(w, r, v, v.Revision, e, 200)
	})
	r.Delete("/environments/{id}", func(w http.ResponseWriter, r *http.Request) {
		expected, e := parseIfMatch(r)
		if e == nil {
			e = s.core.DeleteEmptyEnvironment(r.Context(), chi.URLParam(r, "id"), expected)
		}
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		w.WriteHeader(204)
	})
	for _, path := range []string{"/projects/{id}/variables", "/environments/{id}/variables"} {
		r.Get(path, func(w http.ResponseWriter, r *http.Request) {
			v, e := s.core.GetVariables(r.Context(), chi.URLParam(r, "id"))
			s.controlResponse(w, r, v, v.Revision, e, 200)
		})
		r.Put(path, func(w http.ResponseWriter, r *http.Request) {
			expected, e := parseControlIfMatch(r)
			if e != nil {
				s.handleError(w, r, e)
				return
			}
			var in core.VariableInput
			if !s.decodeJSON(w, r, &in, 1<<20) {
				return
			}
			v, e := s.core.SaveVariables(r.Context(), chi.URLParam(r, "id"), in, expected)
			s.controlResponse(w, r, v, v.Revision, e, 200)
		})
	}
	r.Get("/environments/{id}/effective-variables", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.core.EffectiveVariables(r.Context(), chi.URLParam(r, "id"))
		s.controlResponse(w, r, v, 0, e, 200)
	})
	r.Post("/environments/{id}/compose-validation", func(w http.ResponseWriter, r *http.Request) {
		var in core.ComposeValidationInput
		if !s.decodeJSON(w, r, &in, 1<<20) {
			return
		}
		result, e := s.core.ValidateEnvironmentCompose(r.Context(), chi.URLParam(r, "id"), in)
		s.controlResponse(w, r, result, 0, e, 200)
	})
	r.Post("/environments/{id}/compose-preview", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeApplicationSource(w, r) {
			return
		}
		var in core.ComposeValidationInput
		if !s.decodeJSON(w, r, &in, 1<<20) {
			return
		}
		result, e := s.core.PreviewEnvironmentCompose(r.Context(), chi.URLParam(r, "id"), in)
		s.controlResponse(w, r, result, 0, e, 200)
	})
	r.Post("/environments/{id}/preflight", s.reviewEnvironment)

	r.Post("/environments/{id}/deployments", s.deployEnvironment)
	r.Post("/environments/{id}/compose-merge", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeApplicationSource(w, r) {
			return
		}
		var in struct {
			Files []string `json:"compose_files"`
		}
		if !s.decodeJSON(w, r, &in, 3<<20) {
			return
		}
		value, err := s.core.MergeComposeFiles(r.Context(), chi.URLParam(r, "id"), in.Files)
		s.controlResponse(w, r, map[string]string{"compose_yaml": value}, 0, err, 200)
	})
	r.Post("/environments/{id}/service-preflight", s.reviewEnvironmentServices)

	r.Post("/environments/{id}/service-deployments", s.deployEnvironmentServices)
	r.Get("/environments/{id}/capabilities", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		v, e := s.core.GetEnvironmentCapabilities(r.Context(), chi.URLParam(r, "id"))
		s.controlResponse(w, r, v, 0, e, 200)
	})
	r.Get("/environments/{id}/update-policy", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.core.GetImagePolicy(r.Context(), chi.URLParam(r, "id"))
		s.controlResponse(w, r, v, v.Revision, e, 200)
	})
	r.Put("/environments/{id}/update-policy", func(w http.ResponseWriter, r *http.Request) {
		expected, e := parseControlIfMatch(r)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		var in core.ImagePolicy
		if !s.decodeJSON(w, r, &in, 0) {
			return
		}
		v, e := s.core.SaveImagePolicy(r.Context(), chi.URLParam(r, "id"), in, expected)
		s.controlResponse(w, r, v, v.Revision, e, 200)
	})
	r.Get("/environments/{id}/image-checks", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.core.GetImageCheck(r.Context(), chi.URLParam(r, "id"))
		s.controlResponse(w, r, v, v.Revision, e, 200)
	})
	r.Post("/environments/{id}/image-checks", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.core.RequestImageCheck(r.Context(), chi.URLParam(r, "id"))
		s.controlResponse(w, r, v, v.Revision, e, 202)
	})
	r.Get("/environments/{id}/image-updates", func(w http.ResponseWriter, r *http.Request) {
		page := parsePage(r)
		v, e := s.core.ListImageUpdates(r.Context(), chi.URLParam(r, "id"), page)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		writeCollection(w, v, page, func(v core.ImageUpdate) string { return v.ID })
	})
	r.Post("/environments/{id}/image-updates/{updateID}", func(w http.ResponseWriter, r *http.Request) {
		expected, e := parseControlIfMatch(r)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		var in struct {
			Action string `json:"action"`
		}
		if !s.decodeJSON(w, r, &in, 0) {
			return
		}
		v, e := s.core.ImageUpdateAction(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "updateID"), in.Action, requestActor(r), expected)
		s.controlResponse(w, r, v, v.Revision, e, 202)
	})
	r.Get("/environments/{id}/webhook", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.core.GetHook(r.Context(), chi.URLParam(r, "id"))
		s.controlResponse(w, r, v, v.Revision, e, 200)
	})
	r.Post("/environments/{id}/webhook", func(w http.ResponseWriter, r *http.Request) {
		expected, e := parseControlIfMatch(r)
		if e != nil {
			s.handleError(w, r, e)
			return
		}
		v, secret, e := s.core.RotateHook(r.Context(), chi.URLParam(r, "id"), expected)
		s.controlResponse(w, r, map[string]any{"hook": v, "secret": secret}, v.Revision, e, 200)
	})
}

func (s *Server) getEnvironment(w http.ResponseWriter, r *http.Request) {
	v, err := s.core.GetEnvironment(r.Context(), chi.URLParam(r, "id"))
	s.controlResponse(w, r, v, v.Revision, err, http.StatusOK)
}

func (s *Server) reviewEnvironment(w http.ResponseWriter, r *http.Request) {
	var input core.ComposeValidationInput
	if !s.decodeJSON(w, r, &input, 1<<20) {
		return
	}
	_, keyAuth := requestOpenAPIKey(r)
	preview, err := s.core.ReviewEnvironmentInput(r.Context(), chi.URLParam(r, "id"), input, keyAuth)
	s.controlResponse(w, r, preview, 0, err, http.StatusOK)
}

func (s *Server) reviewEnvironmentServices(w http.ResponseWriter, r *http.Request) {
	var input core.ComposeValidationInput
	if !s.decodeJSON(w, r, &input, 3<<20) {
		return
	}
	if input.ServiceMode != "create" && input.ServiceMode != "update" {
		s.handleError(w, r, core.ValidationError{Code: "validation.application_preflight_failed", Field: "service_mode", Message: "service_mode must be create or update"})
		return
	}
	_, keyAuth := requestOpenAPIKey(r)
	preview, err := s.core.ReviewEnvironmentInput(r.Context(), chi.URLParam(r, "id"), input, keyAuth)
	s.controlResponse(w, r, preview, 0, err, http.StatusOK)
}

func (s *Server) deployEnvironment(w http.ResponseWriter, r *http.Request) {
	expected, err := parseControlIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	var input struct {
		Compose     string `json:"compose_yaml"`
		PreflightID string `json:"preflight_id"`
	}
	if !s.decodeJSON(w, r, &input, 1<<20) {
		return
	}
	_, keyAuth := requestOpenAPIKey(r)
	operation, err := s.core.DeployReviewedEnvironment(r.Context(), chi.URLParam(r, "id"), input.PreflightID, requestActor(r), r.Header.Get("Idempotency-Key"), expected, keyAuth)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/operations/"+operation.ID)
	if _, keyAuth := requestOpenAPIKey(r); keyAuth {
		w.Header().Set("Location", "/openapi/v1/operations/"+operation.ID)
	}
	s.controlResponse(w, r, operation, operation.Revision, nil, http.StatusAccepted)
}

func (s *Server) deployEnvironmentServices(w http.ResponseWriter, r *http.Request) {
	s.deployEnvironment(w, r)
}

func (s *Server) controlResponse(w http.ResponseWriter, r *http.Request, v any, revision int64, err error, status int) {
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, revision)
	writeData(w, status, s.operationResponseValue(r.Context(), v))
}
func (s *Server) imageWebhook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if e != nil {
		s.writeProblem(w, r, 413, "request.invalid_json", nil, "")
		return
	}
	v, e := s.core.ReceiveImageHook(r.Context(), chi.URLParam(r, "hookId"), r.Header.Get("X-NSC-Timestamp"), r.Header.Get("X-NSC-Event-ID"), r.Header.Get("X-NSC-Signature"), body)
	if e == storage.ErrConflict {
		s.writeProblem(w, r, 409, "conflict.resource", nil, "")
		return
	}
	s.controlResponse(w, r, v, v.Revision, e, 202)
}
