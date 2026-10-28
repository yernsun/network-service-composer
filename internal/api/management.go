package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	applicationx "github.com/yernsun/network-service-composer/internal/application"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func (s *Server) listServiceDomainBindings(w http.ResponseWriter, r *http.Request) {
	values, err := s.store.ListServiceDomainBindings(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, values)
}

func (s *Server) createServiceDomainBinding(w http.ResponseWriter, r *http.Request) {
	var input core.CreateDomainBindingInput
	if !s.decodeJSON(w, r, &input, 0) {
		return
	}
	input.IdempotencyKey = r.Header.Get("Idempotency-Key")
	binding, operation, err := s.core.CreateDomainBinding(r.Context(), chi.URLParam(r, "id"), input)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	if operation != nil {
		location := "/api/v1/operations/" + operation.ID
		if _, apiKey := requestOpenAPIKey(r); apiKey {
			location = "/openapi/v1/operations/" + operation.ID
		}
		w.Header().Set("Location", location)
		writeData(w, http.StatusAccepted, map[string]any{"binding": binding, "operation": s.operationResponse(r.Context(), *operation)})
		return
	}
	setETag(w, binding.Route.Revision)
	writeData(w, http.StatusCreated, binding)
}

func (s *Server) updateServiceDomainBinding(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	var patch core.UpdateDomainBindingInput
	if !s.decodeJSON(w, r, &patch, 0) {
		return
	}
	value, err := s.core.UpdateDomainBinding(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "bindingID"), patch, expected)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Route.Revision)
	w.Header().Set("X-Domain-ETag", `"rev-`+strconv.FormatInt(value.Domain.Revision, 10)+`"`)
	writeData(w, http.StatusOK, value)
}

func (s *Server) deleteServiceDomainBinding(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	operation, err := s.core.DeleteDomainBinding(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "bindingID"), expected, r.Header.Get("Idempotency-Key"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	if operation != nil {
		location := "/api/v1/operations/" + operation.ID
		if _, apiKey := requestOpenAPIKey(r); apiKey {
			location = "/openapi/v1/operations/" + operation.ID
		}
		w.Header().Set("Location", location)
		writeData(w, http.StatusAccepted, s.operationResponse(r.Context(), *operation))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) restartService(w http.ResponseWriter, r *http.Request) {
	s.queueServiceOperation(w, r, "restart", 0, false)
}

func (s *Server) scaleService(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Replicas int  `json:"replicas"`
		Persist  bool `json:"persist"`
	}
	if !s.decodeJSON(w, r, &input, 0) {
		return
	}
	s.queueServiceOperation(w, r, "scale", input.Replicas, input.Persist)
}

func (s *Server) createServiceOperation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action   string `json:"action"`
		Replicas int    `json:"replicas,omitempty"`
		Persist  bool   `json:"persist,omitempty"`
	}
	if !s.decodeJSON(w, r, &input, 0) {
		return
	}
	s.queueServiceOperation(w, r, input.Action, input.Replicas, input.Persist)
}

func (s *Server) queueServiceOperation(w http.ResponseWriter, r *http.Request, action string, replicas int, persist bool) {
	expected := int64(0)
	if r.Header.Get("If-Match") != "" {
		var err error
		expected, err = parseIfMatch(r)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
	}
	if persist || strings.EqualFold(action, "remove_runtime") {
		if expected == 0 {
			s.writeProblem(w, r, http.StatusPreconditionRequired, "precondition.if_match_required", nil, "")
			return
		}
	}
	operation, err := s.core.QueueServiceOperation(r.Context(), chi.URLParam(r, "id"), action, replicas, r.Header.Get("Idempotency-Key"),
		core.ServiceOperationOptions{Persist: persist, ExpectedRevision: expected, Actor: requestActor(r)})
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	location := "/api/v1/operations/" + operation.ID
	if _, apiKey := requestOpenAPIKey(r); apiKey {
		location = "/openapi/v1/operations/" + operation.ID
	}
	w.Header().Set("Location", location)
	writeData(w, http.StatusAccepted, s.operationResponse(r.Context(), operation))
}

func (s *Server) listCatalogSources(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	values, err := s.store.ListCatalogSources(r.Context(), page)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeCollection(w, values, page, func(value model.CatalogSource) string { return value.ID })
}

func (s *Server) createCatalogSource(w http.ResponseWriter, r *http.Request) {
	var input core.CreateCatalogSourceInput
	if !s.decodeJSON(w, r, &input, 0) {
		return
	}
	value, err := s.core.CreateCatalogSource(r.Context(), input)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusCreated, value)
}

func (s *Server) getCatalogSource(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GetCatalogSource(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, struct {
		model.CatalogSource
		Market json.RawMessage `json:"market,omitempty"`
	}{value, marketJSON(value.MarketJSON)})
}

func (s *Server) updateCatalogSource(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	value, err := s.store.GetCatalogSource(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	if value.Type == model.CatalogBuiltin {
		s.handleError(w, r, core.ValidationError{Code: "validation.builtin_catalog_read_only", Message: "built-in catalog source is read-only"})
		return
	}
	var patch struct {
		ApprovedEndpoint *string `json:"approved_endpoint"`
		Name             *string `json:"name"`
		URL              *string `json:"url"`
		PinnedRef        *string `json:"pinned_ref"`
		Trusted          *bool   `json:"trusted"`
		Enabled          *bool   `json:"enabled"`
	}
	if !s.decodeJSON(w, r, &patch, 0) {
		return
	}
	if patch.Name != nil {
		value.Name = strings.TrimSpace(*patch.Name)
		if value.Name == "" {
			s.handleError(w, r, core.ValidationError{Code: "validation.catalog_name_required", Field: "name", Message: "catalog source name is required"})
			return
		}
	}
	if patch.URL != nil {
		value.URL = strings.TrimSpace(*patch.URL)
	}
	if patch.PinnedRef != nil {
		value.PinnedRef = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(*patch.PinnedRef)), "sha256:")
	}
	if patch.Trusted != nil {
		value.Trusted = *patch.Trusted
	}
	if patch.ApprovedEndpoint != nil {
		value.ApprovedEndpoint = strings.TrimSpace(*patch.ApprovedEndpoint)
	}
	if patch.URL != nil || patch.PinnedRef != nil || patch.ApprovedEndpoint != nil {
		// Reuse the complete source validator so updates cannot weaken the SSRF
		// and immutable-reference guarantees enforced at creation time.
		validationErr := s.core.ValidateCatalogSourceUpdate(value)
		if validationErr != nil {
			s.handleError(w, r, validationErr)
			return
		}
	}
	if patch.Enabled != nil {
		value.Enabled = *patch.Enabled
	}
	value.UpdatedAt = time.Now().UTC()
	if err = s.store.UpdateCatalogSource(r.Context(), &value, expected); err != nil {
		s.handleError(w, r, err)
		return
	}
	value, err = s.store.GetCatalogSource(r.Context(), value.ID)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}

func (s *Server) syncCatalogSource(w http.ResponseWriter, r *http.Request) {
	value, count, err := s.core.SyncCatalogSource(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"source": value, "versions_synced": count})
}

func (s *Server) listCatalogApps(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	values, err := s.store.ListCatalogAppVersions(r.Context(), r.URL.Query().Get("source_id"), r.URL.Query().Get("app_key"), page)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeCollection(w, values, page, func(value model.CatalogAppVersion) string { return value.ID })
}

func (s *Server) listCatalogAppVersions(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	values, err := s.store.ListCatalogAppVersions(r.Context(), r.URL.Query().Get("source_id"), chi.URLParam(r, "appKey"), page)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeCollection(w, values, page, func(value model.CatalogAppVersion) string { return value.ID })
}

func (s *Server) getCatalogAppVersion(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GetCatalogAppVersion(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	manifest, err := applicationx.ParseManifest(value.ManifestJSON)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, struct {
		model.CatalogAppVersion
		Variables []applicationx.ManifestVariable `json:"variables"`
		Market    json.RawMessage                 `json:"market,omitempty"`
	}{value, manifest.Variables, marketJSON(value.MarketJSON)})
}

func (s *Server) preflightApplication(w http.ResponseWriter, r *http.Request) {
	var input core.ApplicationPreflightInput
	if !s.decodeJSON(w, r, &input, 4<<20) {
		return
	}
	value, err := s.core.PreflightApplication(r.Context(), input)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, value)
}

func (s *Server) listApplications(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	values, err := s.store.ListApplications(r.Context(), r.URL.Query().Get("runtime_id"), page)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	for i := range values {
		s.applicationCapabilities(r, &values[i])
	}
	writeCollection(w, values, page, func(value model.Application) string { return value.ID })
}

func (s *Server) installApplication(w http.ResponseWriter, r *http.Request) {
	var input core.InstallApplicationInput
	if !s.decodeJSON(w, r, &input, 4<<20) {
		return
	}
	input.IdempotencyKey = r.Header.Get("Idempotency-Key")
	input.Actor = requestActor(r)
	application, operation, preflight, err := s.core.InstallApplication(r.Context(), input)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/operations/"+operation.ID)
	writeData(w, http.StatusAccepted, map[string]any{"application": application, "operation": s.operationResponse(r.Context(), operation), "preflight": preflight})
}

func (s *Server) getApplication(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GetApplication(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	s.applicationCapabilities(r, &value)
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}

func (s *Server) listApplicationUpgrades(w http.ResponseWriter, r *http.Request) {
	managedApplication, err := s.store.GetApplication(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	if managedApplication.CatalogAppVersionID == "" {
		writeData(w, http.StatusOK, []model.CatalogAppVersion{})
		return
	}
	current, err := s.store.GetCatalogAppVersion(r.Context(), managedApplication.CatalogAppVersionID)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	versions, err := s.store.ListCatalogAppVersions(r.Context(), current.SourceID, current.AppKey, storage.Page{Limit: 200})
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	available := make([]model.CatalogAppVersion, 0, len(versions))
	for _, version := range versions {
		if version.ID != current.ID {
			available = append(available, version)
		}
	}
	writeData(w, http.StatusOK, available)
}

func (s *Server) listApplicationRevisions(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	values, err := s.store.ListApplicationRevisions(r.Context(), chi.URLParam(r, "id"), page)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeCollection(w, values, page, func(value model.ApplicationRevision) string { return value.ID })
}

func (s *Server) preflightApplicationRevision(w http.ResponseWriter, r *http.Request) {
	var input core.ApplicationPreflightInput
	if !s.decodeJSON(w, r, &input, 4<<20) {
		return
	}
	value, err := s.core.PreflightApplicationRevision(r.Context(), chi.URLParam(r, "id"), input)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, value)
}

func (s *Server) getApplicationRevision(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GetApplicationRevision(r.Context(), chi.URLParam(r, "revisionID"))
	if err == nil && value.ApplicationID != chi.URLParam(r, "id") {
		err = responseError{status: http.StatusNotFound, code: "resource.not_found"}
	}
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, value)
}

func (s *Server) createApplicationRevision(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	var input core.CreateRevisionInput
	if !s.decodeJSON(w, r, &input, 4<<20) {
		return
	}
	input.IdempotencyKey = r.Header.Get("Idempotency-Key")
	input.Actor = requestActor(r)
	revision, operation, preview, err := s.core.CreateApplicationRevision(r.Context(), chi.URLParam(r, "id"), input, expected)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/operations/"+operation.ID)
	writeData(w, http.StatusAccepted, map[string]any{"revision": revision, "operation": s.operationResponse(r.Context(), operation), "preview": preview})
}

func (s *Server) listApplicationOperations(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	values, err := s.store.ListDeploymentOperations(r.Context(), chi.URLParam(r, "id"), page)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeCollection(w, s.operationResponses(r.Context(), values), page, func(value model.DeploymentOperation) string { return value.ID })
}

func (s *Server) createApplicationOperation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action string `json:"action"`
	}
	if !s.decodeJSON(w, r, &input, 0) {
		return
	}
	value, err := s.core.QueueApplicationOperation(r.Context(), chi.URLParam(r, "id"), input.Action, r.Header.Get("Idempotency-Key"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/operations/"+value.ID)
	writeData(w, http.StatusAccepted, s.operationResponse(r.Context(), value))
}

func (s *Server) listOperations(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	values, err := s.store.ListDeploymentOperations(r.Context(), r.URL.Query().Get("application_id"), page)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeCollection(w, s.operationResponses(r.Context(), values), page, func(value model.DeploymentOperation) string { return value.ID })
}

func (s *Server) getOperation(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GetDeploymentOperation(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, s.operationResponse(r.Context(), value))
}

func marketJSON(value string) json.RawMessage {
	if value == "" {
		return nil
	}
	return json.RawMessage(value)
}

// Resource-level capabilities. Request authorization remains enforced by the API.
func (s *Server) applicationCapabilities(r *http.Request, a *model.Application) {
	a.LifecycleOperations = []string{}
	if a.System || !a.Managed || !s.config.Management.Enabled {
		return
	}
	rt, err := s.store.GetRuntime(r.Context(), a.RuntimeID)
	if err == nil && rt.Enabled && rt.ManagementEnabled {
		a.LifecycleOperations = []string{"START", "STOP", "DOWN", "REDEPLOY"}
	}
}
