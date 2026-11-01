package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/i18n"
	"github.com/yernsun/network-service-composer/internal/model"
)

func (s *Server) registerAPI(router chi.Router) {
	s.registerSystemHA(router)
	s.registerInstallation(router)
	s.registerCommunity(router)
	s.registerCluster(router)
	s.registerEnvironments(router)
	s.registerNginx(router)
	s.registerDNSProfiles(router)
	s.registerNotifications(router)
	s.registerDomainDrafts(router)
	router.Get("/gateway-pools", s.listPools)
	router.Post("/gateway-pools", s.createPool)
	router.Get("/gateway-pools/{id}", s.getPool)
	router.Patch("/gateway-pools/{id}", s.updatePool)
	router.Delete("/gateway-pools/{id}", s.deletePool)

	router.Get("/runtimes", s.listRuntimes)
	router.Post("/runtimes", s.createRuntime)
	router.Get("/runtimes/{id}", s.getRuntime)
	router.Patch("/runtimes/{id}", s.updateRuntime)
	router.Delete("/runtimes/{id}", s.deleteRuntime)
	router.Get("/runtimes/{id}/docker-resources", s.listDockerResources)
	router.Post("/runtimes/{id}/docker-resources", s.createDockerResource)
	router.Delete("/runtimes/{id}/docker-resources/{resourceID}", s.deleteDockerResource)
	router.Get("/runtimes/{id}/volumes", s.listVolumes)
	router.Post("/runtimes/{id}/volumes", s.createVolume)
	router.Get("/runtimes/{id}/volumes/{name}", s.getVolume)
	router.Delete("/runtimes/{id}/volumes/{name}", s.deleteVolume)

	router.Get("/service-groups", s.listServiceGroups)
	router.Get("/services", s.listServices)
	router.Get("/services/{id}", s.getService)
	router.Patch("/services/{id}", s.updateService)
	router.Get("/services/{id}/instances", s.listServiceInstances)
	router.Get("/services/{id}/logs", s.serviceLogs)
	router.Get("/services/{id}/domain-bindings", s.listServiceDomainBindings)
	router.Post("/services/{id}/domain-bindings", s.createServiceDomainBinding)
	router.Patch("/services/{id}/domain-bindings/{bindingID}", s.updateServiceDomainBinding)
	router.Delete("/services/{id}/domain-bindings/{bindingID}", s.deleteServiceDomainBinding)
	router.Post("/services/{id}/restart", s.restartService)
	router.Post("/services/{id}/scale", s.scaleService)
	router.Post("/services/{id}/operations", s.createServiceOperation)

	router.Get("/catalog/sources", s.listCatalogSources)
	router.Post("/catalog/sources", s.createCatalogSource)
	router.Get("/catalog/sources/{id}", s.getCatalogSource)
	router.Patch("/catalog/sources/{id}", s.updateCatalogSource)
	router.Post("/catalog/sources/{id}/sync", s.syncCatalogSource)
	router.Get("/catalog/apps", s.listCatalogApps)
	router.Get("/catalog/apps/{appKey}/versions", s.listCatalogAppVersions)
	router.Get("/catalog/versions/{id}", s.getCatalogAppVersion)

	router.Post("/applications/preflight", s.preflightApplication)
	router.Get("/applications", s.listApplications)
	router.Post("/applications", s.installApplication)
	router.Get("/applications/{id}", s.getApplication)
	s.registerApplicationSources(router)
	s.registerServiceEntrypoints(router)
	router.Get("/applications/{id}/upgrades", s.listApplicationUpgrades)
	router.Get("/applications/{id}/revisions", s.listApplicationRevisions)
	router.Post("/applications/{id}/revisions/preflight", s.preflightApplicationRevision)
	router.Get("/applications/{id}/revisions/{revisionID}", s.getApplicationRevision)
	router.Post("/applications/{id}/revisions", s.createApplicationRevision)
	router.Get("/applications/{id}/operations", s.listApplicationOperations)
	router.Post("/applications/{id}/operations", s.createApplicationOperation)
	router.Get("/operations", s.listOperations)
	router.Get("/timeline", s.listTimeline)
	router.Get("/operations/{id}", s.getOperation)

	router.Get("/domains", s.listDomains)
	router.Post("/domains", s.createDomain)
	router.Get("/domains/{id}", s.getDomain)
	router.Patch("/domains/{id}", s.updateDomain)
	router.Delete("/domains/{id}", s.deleteDomain)

	router.Get("/routes", s.listRoutes)
	router.Post("/routes", s.createRoute)
	router.Get("/routes/{id}", s.getRoute)
	router.Patch("/routes/{id}", s.updateRoute)
	router.Delete("/routes/{id}", s.deleteRoute)

	router.Get("/certificates", s.listCertificates)
	router.Post("/certificates", s.createCertificate)
	router.Post("/certificates/self-signed", s.createSelfSignedCertificate)
	router.Get("/certificates/{id}", s.getCertificate)
	router.Post("/certificates/{id}/renew", s.renewCertificate)
	router.Post("/certificates/{id}/revoke", s.revokeCertificate)

	router.Get("/gateways", s.listGateways)
	router.Get("/gateways/{id}", s.getGateway)
	router.Patch("/gateways/{id}", s.updateGateway)
	router.Get("/gateway-pools/{id}/config-preview", s.gatewayConfigPreview)
	router.Get("/gateway-pools/{id}/route-trace", s.gatewayRouteTrace)
	router.Get("/settings", s.settings)
}

func (s *Server) listPools(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	values, err := s.store.ListPools(r.Context(), page)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeCollection(w, values, page, func(value model.GatewayPool) string { return value.ID })
}
func (s *Server) createPool(w http.ResponseWriter, r *http.Request) {
	var input core.CreatePoolInput
	if !s.decodeJSON(w, r, &input, 0) {
		return
	}
	value, err := s.core.CreatePool(r.Context(), input)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusCreated, value)
}
func (s *Server) getPool(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GetPool(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}
func (s *Server) updatePool(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	value, err := s.store.GetPool(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	var patch struct {
		Name              *string `json:"name"`
		BackendNetwork    *string `json:"backend_network"`
		ChallengeUpstream *string `json:"challenge_upstream"`
		Enabled           *bool   `json:"enabled"`
	}
	if !s.decodeJSON(w, r, &patch, 0) {
		return
	}
	if patch.Name != nil {
		value.Name = *patch.Name
	}
	if patch.BackendNetwork != nil {
		value.BackendNetwork = *patch.BackendNetwork
	}
	if patch.ChallengeUpstream != nil {
		value.ChallengeUpstream = *patch.ChallengeUpstream
	}
	if patch.Enabled != nil {
		value.Enabled = *patch.Enabled
	}
	value, err = s.core.UpdatePool(r.Context(), value, expected)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}
func (s *Server) deletePool(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	if err = s.store.DeletePool(r.Context(), chi.URLParam(r, "id"), expected); err != nil {
		s.handleError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRuntimes(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	values, err := s.store.ListRuntimes(r.Context(), page)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeCollection(w, values, page, func(value model.Runtime) string { return value.ID })
}
func (s *Server) createRuntime(w http.ResponseWriter, r *http.Request) {
	var input core.CreateRuntimeInput
	if !s.decodeJSON(w, r, &input, 0) {
		return
	}
	value, err := s.core.CreateRuntime(r.Context(), input)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusCreated, value)
}
func (s *Server) getRuntime(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GetRuntime(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}
func (s *Server) updateRuntime(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	value, err := s.store.GetRuntime(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	var patch struct {
		PoolID            *string `json:"pool_id"`
		Name              *string `json:"name"`
		Endpoint          *string `json:"endpoint"`
		CredentialRef     *string `json:"credential_ref"`
		Enabled           *bool   `json:"enabled"`
		ManagementEnabled *bool   `json:"management_enabled"`
	}
	if !s.decodeJSON(w, r, &patch, 0) {
		return
	}
	if patch.PoolID != nil {
		value.PoolID = *patch.PoolID
	}
	if patch.Name != nil {
		value.Name = *patch.Name
	}
	if patch.Endpoint != nil {
		value.Endpoint = *patch.Endpoint
	}
	if patch.CredentialRef != nil {
		value.CredentialRef = *patch.CredentialRef
	}
	if patch.Enabled != nil {
		value.Enabled = *patch.Enabled
	}
	if patch.ManagementEnabled != nil {
		value.ManagementEnabled = *patch.ManagementEnabled
	}
	value, err = s.core.UpdateRuntime(r.Context(), value, expected)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}
func (s *Server) deleteRuntime(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	if err = s.store.DeleteRuntime(r.Context(), chi.URLParam(r, "id"), expected); err != nil {
		s.handleError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	f, ok := s.consoleFilter(w, r)
	if !ok {
		return
	}
	filter := f.ServiceFilter
	values, err := s.store.ListServicesFiltered(r.Context(), filter, page)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	ids := make([]string, 0, len(values))
	for _, v := range values {
		ids = append(ids, v.ID)
	}
	refs, err := s.store.ServiceEnvironments(r.Context(), ids)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	for i := range values {
		if ref, ok := refs[values[i].ID]; ok {
			values[i].Environment = &ref
			values[i].System = ref.System
			if ref.System {
				values[i].LifecycleOperations = []string{}
			}
		}
	}
	if err := s.core.ServiceManagement(r.Context(), values); err != nil {
		s.handleError(w, r, err)
		return
	}
	writeCollection(w, values, page, func(value model.Service) string { return value.ID })
}
func (s *Server) getService(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GetService(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	refs, err := s.store.ServiceEnvironments(r.Context(), []string{value.ID})
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	if ref, ok := refs[value.ID]; ok {
		value.Environment = &ref
		value.System = ref.System
		if ref.System {
			value.LifecycleOperations = []string{}
		}
	}
	values := []model.Service{value}
	if err = s.core.ServiceManagement(r.Context(), values); err != nil {
		s.handleError(w, r, err)
		return
	}
	value = values[0]
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}
func (s *Server) updateService(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	var patch core.ServicePatch
	if !s.decodeJSON(w, r, &patch, 0) {
		return
	}
	value, err := s.core.UpdateService(r.Context(), chi.URLParam(r, "id"), patch, expected)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}
func (s *Server) listServiceInstances(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.GetService(r.Context(), id); err != nil {
		s.handleError(w, r, err)
		return
	}
	if r.URL.Query().Has("limit") || r.URL.Query().Has("cursor") {
		page := parsePage(r)
		values, err := s.store.ListEndpointsPage(r.Context(), id, page)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		writeCollection(w, values, page, func(v model.Endpoint) string { return v.ID })
		return
	}
	values, err := s.store.ListEndpoints(r.Context(), id)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, values)
}

func (s *Server) listDomains(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	values, err := s.store.ListDomains(r.Context(), page)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeCollection(w, values, page, func(value model.Domain) string { return value.ID })
}
func (s *Server) createDomain(w http.ResponseWriter, r *http.Request) {
	var input core.CreateDomainInput
	if !s.decodeJSON(w, r, &input, 0) {
		return
	}
	value, err := s.core.CreateDomain(r.Context(), input)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusCreated, value)
}
func (s *Server) getDomain(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GetDomain(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}
func (s *Server) updateDomain(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	value, err := s.store.GetDomain(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	var patch core.DomainConfigPatch
	if !s.decodeJSON(w, r, &patch, 0) {
		return
	}
	if patch.Host != nil {
		value.Host = *patch.Host
	}
	if patch.EntryPoints != nil {
		value.HTTPEnabled, value.HTTPSEnabled, err = core.NormalizeEntryPoints(*patch.EntryPoints)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
	}
	if patch.HTTP3Enabled != nil {
		value.HTTP3Enabled = *patch.HTTP3Enabled
	}
	if patch.RedirectHTTPToHTTPS != nil {
		value.HTTPSRedirect = *patch.RedirectHTTPToHTTPS
	}
	if patch.TLS != nil {
		value.TLSMode = patch.TLS.Mode
		value.ACMEChallenge = patch.TLS.ACMEChallenge
		value.DNSProviderRef = patch.TLS.DNSProviderRef
		value.CertificateID = patch.TLS.CertificateID
	}
	if patch.Enabled != nil {
		value.Enabled = *patch.Enabled
	}
	value, err = s.core.UpdateDomain(r.Context(), value, expected)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}
func (s *Server) deleteDomain(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	if err = s.store.DeleteDomain(r.Context(), chi.URLParam(r, "id"), expected); err != nil {
		s.handleError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRoutes(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	f, ok := s.consoleFilter(w, r)
	if !ok {
		return
	}
	values, err := s.store.ListRoutesFiltered(r.Context(), f, page)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	s.writeRoutes(w, r, values, page)
}

func (s *Server) createRoute(w http.ResponseWriter, r *http.Request) {
	var input core.CreateRouteInput
	if !s.decodeJSON(w, r, &input, 0) {
		return
	}
	value, err := s.core.CreateRoute(r.Context(), input)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusCreated, value)
}
func (s *Server) getRoute(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GetRoute(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}
func (s *Server) updateRoute(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	value, err := s.store.GetRoute(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	var patch struct {
		DomainID *string `json:"domain_id,omitempty"`
		core.RouteConfigPatch
	}
	if !s.decodeJSON(w, r, &patch, 0) {
		return
	}
	if patch.DomainID != nil {
		value.DomainID = *patch.DomainID
	}
	if patch.Path != nil {
		value.Path = *patch.Path
	}
	if patch.Priority != nil {
		value.Priority = *patch.Priority
	}
	if patch.StripPath != nil {
		value.StripPath = *patch.StripPath
	}
	if patch.Backend != nil {
		if patch.Backend.ServiceID != nil {
			value.ServiceID = *patch.Backend.ServiceID
		}
		if patch.Backend.Port != nil {
			value.TargetPort = *patch.Backend.Port
		}
		if patch.Backend.Protocol != nil {
			value.UpstreamProtocol = *patch.Backend.Protocol
		}
		if patch.Backend.ServerName != nil {
			value.UpstreamServerName = *patch.Backend.ServerName
		}
	}
	if patch.WebsocketEnabled != nil {
		value.WebsocketEnabled = *patch.WebsocketEnabled
	}
	if patch.Enabled != nil {
		value.Enabled = *patch.Enabled
	}
	value, err = s.core.UpdateRoute(r.Context(), value, expected)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}
func (s *Server) deleteRoute(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	if err = s.store.DeleteRoute(r.Context(), chi.URLParam(r, "id"), expected); err != nil {
		s.handleError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listCertificates(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	values, err := s.store.ListCertificates(r.Context(), page)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeCollection(w, values, page, func(value model.Certificate) string { return value.ID })
}
func (s *Server) createCertificate(w http.ResponseWriter, r *http.Request) {
	var input core.CustomCertificateInput
	if !s.decodeJSON(w, r, &input, 4<<20) {
		return
	}
	value, err := s.core.CreateCustomCertificate(r.Context(), input)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusCreated, value)
}
func (s *Server) createSelfSignedCertificate(w http.ResponseWriter, r *http.Request) {
	var input core.SelfSignedCertificateInput
	if !s.decodeJSON(w, r, &input, 64<<10) {
		return
	}
	value, err := s.core.CreateSelfSignedCertificate(r.Context(), input)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusCreated, value)
}
func (s *Server) getCertificate(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GetCertificate(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}
func (s *Server) renewCertificate(w http.ResponseWriter, r *http.Request) {
	value, err := s.core.MarkCertificateForRenewal(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeData(w, http.StatusAccepted, value)
}
func (s *Server) revokeCertificate(w http.ResponseWriter, r *http.Request) {
	value, err := s.core.RevokeCertificate(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	writeData(w, http.StatusAccepted, value)
}

func (s *Server) listGateways(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	values, err := s.store.ListGateways(r.Context(), page)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	for i := range values {
		e, err := s.core.GatewayEvidence(r.Context(), values[i].ID)
		if err != nil {
			s.handleError(w, r, err)
			return
		}
		values[i].Evidence = &e
		applyEvidenceStatus(&values[i])
	}
	writeCollection(w, values, page, func(value model.Gateway) string { return value.ID })
}
func (s *Server) getGateway(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GetGateway(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	e, err := s.core.GatewayEvidence(r.Context(), value.ID)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	value.Evidence = &e
	applyEvidenceStatus(&value)
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}
func (s *Server) updateGateway(w http.ResponseWriter, r *http.Request) {
	expected, err := parseIfMatch(r)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	value, err := s.store.GetGateway(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	var patch struct {
		Name    *string `json:"name"`
		Enabled *bool   `json:"enabled"`
	}
	if !s.decodeJSON(w, r, &patch, 0) {
		return
	}
	if patch.Name != nil {
		value.Name = *patch.Name
	}
	if patch.Enabled != nil {
		value.Enabled = *patch.Enabled
		if !value.Enabled {
			value.Status = model.GatewayOffline
		}
	}
	value.UpdatedAt = time.Now().UTC()
	if err = s.store.UpdateGateway(r.Context(), &value, expected); err != nil {
		s.handleError(w, r, err)
		return
	}
	value, err = s.store.GetGateway(r.Context(), value.ID)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	setETag(w, value.Revision)
	writeData(w, http.StatusOK, value)
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.core.ListDNSProfiles(r.Context())
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	dnsProviders := make(map[string]any, len(profiles))
	for _, profile := range profiles {
		dnsProviders[profile.Reference] = map[string]any{"provider": profile.Provider, "configured": profile.Configured, "source": profile.Source}
	}
	writeData(w, http.StatusOK, map[string]any{"storage": map[string]any{"driver": s.config.Storage.Driver, "ha": s.config.Storage.HA}, "server": map[string]any{"http_listen": s.config.Server.HTTPListen, "grpc_listen": s.config.Server.GRPCListen, "public_url": s.config.Server.PublicURL}, "management": map[string]any{"enabled": s.config.Management.Enabled, "external_secrets": s.config.Management.ExternalSecrets, "external_configs": s.config.Management.ExternalConfigs, "poll_interval": s.config.Management.PollInterval, "heartbeat_interval": s.config.Management.HeartbeatInterval, "stale_after": s.config.Management.StaleAfter, "deploy_timeout": s.config.Management.DeployTimeout}, "controller": s.config.Controller, "nginx": s.config.NGINX, "acme": map[string]any{"directory_url": s.config.ACME.DirectoryURL, "email": s.config.ACME.Email, "dns_providers": dnsProviders}, "i18n": map[string]any{"default_language": s.config.I18N.DefaultLanguage, "supported_languages": i18n.SupportedCodes()}, "security": map[string]any{"admin_username": s.config.Security.AdminUsername, "admin_token": "[redacted]", "primary_key_id": s.config.Security.PrimaryKeyID, "mtls_required": true}})
}
