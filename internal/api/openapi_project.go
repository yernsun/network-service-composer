package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// Project keys need only the environment ID and ETag to address a future
// deployment. Runtime, gateway and credential references require other grants.
type projectEnvironmentDiscovery struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

func (s *Server) getOpenAPIEnvironment(w http.ResponseWriter, r *http.Request) {
	key, _ := requestOpenAPIKey(r)
	if !key.ProjectScoped() {
		s.getEnvironment(w, r)
		return
	}
	env, err := s.core.GetEnvironment(r.Context(), chi.URLParam(r, "id"))
	s.controlResponse(w, r, projectEnvironmentDiscovery{ID: env.ID, Revision: env.Revision}, env.Revision, err, http.StatusOK)
}

func (s *Server) projectKey(w http.ResponseWriter, r *http.Request, scope string) (core.OpenAPIKey, bool) {
	key, ok := requestOpenAPIKey(r)
	if !ok || !key.ProjectScoped() || (scope != "" && !key.HasScope(scope)) {
		s.writeProblem(w, r, http.StatusNotFound, "resource.not_found", nil, "")
		return core.OpenAPIKey{}, false
	}
	project, err := s.core.GetProject(r.Context(), key.ProjectID)
	if err != nil || project.System {
		s.writeProblem(w, r, http.StatusNotFound, "resource.not_found", nil, "")
		return core.OpenAPIKey{}, false
	}
	return key, true
}

func (s *Server) projectRoute(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := s.projectKey(w, r, scope)
		if !ok {
			return
		}
		if chi.URLParam(r, "id") != key.ProjectID {
			s.writeProblem(w, r, 404, "resource.not_found", nil, "")
			return
		}
		next(w, r)
	}
}

func (s *Server) projectEnvironmentRoute(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := s.projectKey(w, r, scope)
		if !ok {
			return
		}
		env, err := s.core.GetEnvironment(r.Context(), chi.URLParam(r, "id"))
		if err != nil || env.System || env.ProjectID != key.ProjectID {
			s.writeProblem(w, r, 404, "resource.not_found", nil, "")
			return
		}
		next(w, r)
	}
}

func (s *Server) projectServiceRoute(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := s.projectKey(w, r, scope)
		if !ok {
			return
		}
		service, err := s.store.GetService(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		refs, err := s.store.ServiceEnvironments(r.Context(), []string{service.ID})
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		ref, exists := refs[service.ID]
		if !exists || ref.System || ref.ProjectID != key.ProjectID {
			s.writeProblem(w, r, 404, "resource.not_found", nil, "")
			return
		}
		next(w, r)
	}
}

func (s *Server) projectDraftRoute(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := s.projectKey(w, r, scope)
		if !ok {
			return
		}
		draft, err := s.core.GetDomainDraft(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		env, err := s.core.GetEnvironment(r.Context(), draft.EnvironmentID)
		if err != nil || env.System || env.ProjectID != key.ProjectID || draft.ProjectID != key.ProjectID {
			s.writeProblem(w, r, 404, "resource.not_found", nil, "")
			return
		}
		next(w, r)
	}
}

func (s *Server) projectPublishRoute(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.projectKey(w, r, "domains.publish"); !ok {
			return
		}
		next(w, r)
	}
}

func (s *Server) registerProjectOpenAPI(r chi.Router) {
	r.Get("/projects", func(w http.ResponseWriter, r *http.Request) {
		key, ok := s.projectKey(w, r, "")
		if !ok {
			return
		}
		project, err := s.core.GetProject(r.Context(), key.ProjectID)
		if err != nil || project.System {
			s.writeProblem(w, r, 404, "resource.not_found", nil, "")
			return
		}
		page := parsePage(r)
		if page.After != "" && page.After >= project.ID {
			writeCollection(w, []core.Project{}, page, func(v core.Project) string { return v.ID })
			return
		}
		writeCollection(w, []core.Project{project}, page, func(v core.Project) string { return v.ID })
	})
	r.Get("/projects/{id}", s.projectRoute("", func(w http.ResponseWriter, r *http.Request) {
		project, err := s.core.GetProject(r.Context(), chi.URLParam(r, "id"))
		s.controlResponse(w, r, project, project.Revision, err, 200)
	}))
	r.Get("/projects/{id}/environments", s.projectRoute("", func(w http.ResponseWriter, r *http.Request) {
		page := parsePage(r)
		envs, err := s.core.ListEnvironments(r.Context(), chi.URLParam(r, "id"), page)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		visible := make([]projectEnvironmentDiscovery, 0, len(envs))
		for _, env := range envs {
			if !env.System {
				visible = append(visible, projectEnvironmentDiscovery{ID: env.ID, Revision: env.Revision})
			}
		}
		writeCollection(w, visible, page, func(v projectEnvironmentDiscovery) string { return v.ID })
	}))
	r.Get("/projects/{id}/variables", s.projectRoute("project_variables.read", s.getProjectAPIVariables))
	r.Put("/projects/{id}/variables", s.projectRoute("project_variables.edit", s.putProjectAPIVariables))
	r.Get("/environments/{id}/variables", s.projectEnvironmentRoute("environment_variables.read", s.getProjectAPIVariables))
	r.Put("/environments/{id}/variables", s.projectEnvironmentRoute("environment_variables.edit", s.putProjectAPIVariables))
	r.Get("/environments/{id}/effective-variables", s.projectEnvironmentRoute("environment_variables.read", func(w http.ResponseWriter, r *http.Request) {
		value, err := s.core.EffectiveVariables(r.Context(), chi.URLParam(r, "id"))
		s.controlResponse(w, r, value, 0, err, 200)
	}))
	r.Get("/services", s.listProjectAPIServices)
	r.Get("/services/{id}", s.projectServiceRoute("services.read", s.getService))
	r.Patch("/services/{id}", s.projectServiceRoute("services.edit", s.updateService))
	r.Get("/services/{id}/instances", s.projectServiceRoute("services.read", s.listServiceInstances))
	r.Post("/services/{id}/restart", s.projectServiceRoute("services.operate", s.restartService))
	r.Post("/services/{id}/scale", s.projectServiceRoute("services.operate", s.projectScaleService))
	r.Get("/services/{id}/domain-bindings", s.projectServiceRoute("domains.read", s.listServiceDomainBindings))
	r.Post("/services/{id}/domain-bindings", s.projectServiceRoute("domains.create", s.projectPublishRoute(s.projectCreateDomainBinding)))
	r.Patch("/services/{id}/domain-bindings/{bindingID}", s.projectServiceRoute("domains.edit", s.projectPublishRoute(s.projectUpdateDomainBinding)))
	r.Delete("/services/{id}/domain-bindings/{bindingID}", s.projectServiceRoute("domains.delete", s.projectPublishRoute(s.deleteServiceDomainBinding)))
	r.Get("/environments/{id}/domain-drafts", s.projectEnvironmentRoute("domains.edit", s.listProjectDomainDrafts))
	r.Post("/environments/{id}/domain-drafts", s.projectEnvironmentRoute("domains.create", s.createProjectDomainDraft))
	r.Get("/domain-drafts/{id}", s.projectDraftRoute("domains.edit", s.getProjectDomainDraft))
	r.Put("/domain-drafts/{id}", s.projectDraftRoute("domains.edit", s.updateProjectDomainDraft))
	r.Delete("/domain-drafts/{id}", s.projectDraftRoute("domains.delete", s.deleteProjectDomainDraft))
	r.Post("/domain-drafts/{id}/publish", s.projectDraftRoute("domains.publish", s.publishProjectDomainDraft))
}

func (s *Server) getProjectAPIVariables(w http.ResponseWriter, r *http.Request) {
	value, err := s.core.GetVariables(r.Context(), chi.URLParam(r, "id"))
	s.controlResponse(w, r, value, value.Revision, err, 200)
}

func (s *Server) putProjectAPIVariables(w http.ResponseWriter, r *http.Request) {
	expected, err := parseControlIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	var input core.VariableInput
	if !s.decodeJSON(w, r, &input, 1<<20) {
		return
	}
	value, err := s.core.SaveVariables(r.Context(), chi.URLParam(r, "id"), input, expected)
	s.controlResponse(w, r, value, value.Revision, err, 200)
}

func (s *Server) listProjectAPIServices(w http.ResponseWriter, r *http.Request) {
	key, ok := s.projectKey(w, r, "services.read")
	if !ok {
		return
	}
	page := parsePage(r)
	limit := page.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	values := make([]model.Service, 0, limit)
	after := page.After
	for len(values) < limit {
		batch, err := s.store.ListServicesFiltered(r.Context(), storage.ServiceFilter{ExcludeRemoved: true}, storage.Page{After: after, Limit: 200})
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		if len(batch) == 0 {
			break
		}
		ids := make([]string, len(batch))
		for i, service := range batch {
			ids[i] = service.ID
		}
		refs, err := s.store.ServiceEnvironments(r.Context(), ids)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		for _, service := range batch {
			after = service.ID
			ref, exists := refs[service.ID]
			if !exists || ref.System || ref.ProjectID != key.ProjectID {
				continue
			}
			service.Environment = &ref
			values = append(values, service)
			if len(values) == limit {
				break
			}
		}
		if len(batch) < 200 {
			break
		}
	}
	writeCollection(w, values, page, func(v model.Service) string { return v.ID })
}

func (s *Server) openAPIPreflight(w http.ResponseWriter, r *http.Request) {
	key, _ := requestOpenAPIKey(r)
	if !key.ProjectScoped() {
		s.reviewEnvironment(w, r)
		return
	}
	s.projectReview(w, r, false)
}

func (s *Server) openAPIServicePreflight(w http.ResponseWriter, r *http.Request) {
	key, _ := requestOpenAPIKey(r)
	if !key.ProjectScoped() {
		s.reviewEnvironmentServices(w, r)
		return
	}
	s.projectReview(w, r, true)
}

func (s *Server) projectReview(w http.ResponseWriter, r *http.Request, servicesOnly bool) {
	key, _ := requestOpenAPIKey(r)
	if !key.HasScope("services.create") && !key.HasScope("services.edit") {
		s.writeProblem(w, r, 404, "resource.not_found", nil, "")
		return
	}
	var input core.ComposeValidationInput
	limit := int64(1 << 20)
	if servicesOnly {
		limit = 3 << 20
	}
	if !s.decodeJSON(w, r, &input, limit) {
		return
	}
	if servicesOnly && input.ServiceMode != "create" && input.ServiceMode != "update" {
		s.handleError(w, r, core.ValidationError{Code: "validation.application_preflight_failed", Field: "service_mode", Message: "service_mode must be create or update"})
		return
	}
	result, err := s.core.ReviewEnvironmentInputScoped(r.Context(), chi.URLParam(r, "id"), input, key.Scopes)
	s.controlResponse(w, r, result, 0, err, 200)
}

func (s *Server) openAPIDeploy(w http.ResponseWriter, r *http.Request) {
	key, _ := requestOpenAPIKey(r)
	if !key.ProjectScoped() {
		s.deployEnvironment(w, r)
		return
	}
	if !key.HasScope("services.deploy") {
		s.writeProblem(w, r, 404, "resource.not_found", nil, "")
		return
	}
	expected, err := parseControlIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	var input struct {
		PreflightID string `json:"preflight_id"`
	}
	if !s.decodeJSON(w, r, &input, 1<<20) {
		return
	}
	operation, err := s.core.DeployReviewedEnvironmentScoped(r.Context(), chi.URLParam(r, "id"), input.PreflightID, requestActor(r), r.Header.Get("Idempotency-Key"), expected, key.Scopes)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	w.Header().Set("Location", "/openapi/v1/operations/"+operation.ID)
	s.controlResponse(w, r, operation, operation.Revision, nil, http.StatusAccepted)
}

func (s *Server) projectScaleService(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Replicas int  `json:"replicas"`
		Persist  bool `json:"persist"`
	}
	if !s.decodeJSON(w, r, &input, 8192) {
		return
	}
	key, _ := requestOpenAPIKey(r)
	if input.Persist && (!key.HasScope("services.edit") || !key.HasScope("services.deploy")) {
		s.writeProblem(w, r, 404, "resource.not_found", nil, "")
		return
	}
	s.queueServiceOperation(w, r, "scale", input.Replicas, input.Persist)
}

func (s *Server) projectCreateDomainBinding(w http.ResponseWriter, r *http.Request) {
	var input core.CreateDomainBindingInput
	if !s.decodeJSON(w, r, &input, 16384) {
		return
	}
	// An existing Domain belongs to the shared pool, not the project.
	if input.DomainID != "" || input.Domain == nil {
		s.writeProblem(w, r, 404, "resource.not_found", nil, "domain_id")
		return
	}
	input.IdempotencyKey = r.Header.Get("Idempotency-Key")
	binding, operation, err := s.core.CreateDomainBinding(r.Context(), chi.URLParam(r, "id"), input)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	if operation != nil {
		w.Header().Set("Location", "/openapi/v1/operations/"+operation.ID)
		writeData(w, http.StatusAccepted, map[string]any{"binding": binding, "operation": s.operationResponse(r.Context(), *operation)})
		return
	}
	setETag(w, binding.Route.Revision)
	writeData(w, http.StatusCreated, binding)
}

func (s *Server) projectUpdateDomainBinding(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	var patch core.UpdateDomainBindingInput
	if !s.decodeJSON(w, r, &patch, 16384) {
		return
	}
	if patch.Domain != nil {
		s.writeProblem(w, r, 404, "resource.not_found", nil, "domain")
		return
	}
	value, err := s.core.UpdateDomainBinding(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "bindingID"), patch, expected)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Route.Revision)
	writeData(w, 200, value)
}

func (s *Server) listProjectDomainDrafts(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	values, err := s.core.ListDomainDrafts(r.Context(), chi.URLParam(r, "id"), page)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeCollection(w, values, page, func(v core.DomainDraft) string { return v.ID })
}
func (s *Server) createProjectDomainDraft(w http.ResponseWriter, r *http.Request) {
	var input core.DomainDraftInput
	if !s.decodeJSON(w, r, &input, 16384) {
		return
	}
	value, err := s.core.CreateDomainDraft(r.Context(), chi.URLParam(r, "id"), input)
	s.controlResponse(w, r, value, value.Revision, err, 201)
}
func (s *Server) getProjectDomainDraft(w http.ResponseWriter, r *http.Request) {
	value, err := s.core.GetDomainDraft(r.Context(), chi.URLParam(r, "id"))
	s.controlResponse(w, r, value, value.Revision, err, 200)
}
func (s *Server) updateProjectDomainDraft(w http.ResponseWriter, r *http.Request) {
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
	s.controlResponse(w, r, value, value.Revision, err, 200)
}
func (s *Server) deleteProjectDomainDraft(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err == nil {
		err = s.core.DeleteDomainDraft(r.Context(), chi.URLParam(r, "id"), expected)
	}
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) publishProjectDomainDraft(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	value, err := s.core.PublishDomainDraft(r.Context(), chi.URLParam(r, "id"), expected)
	if err == nil && value.OperationID != "" {
		w.Header().Set("Location", "/openapi/v1/operations/"+value.OperationID)
	}
	s.controlResponse(w, r, value, value.Revision, err, 202)
}
