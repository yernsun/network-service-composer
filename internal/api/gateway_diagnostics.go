package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/gateway"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
	"github.com/yernsun/network-service-composer/internal/upstream"
)

type configFragmentPreview struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Config string `json:"config"`
}

func (s *Server) gatewayConfigPreview(w http.ResponseWriter, r *http.Request) {
	poolID := chi.URLParam(r, "id")
	pool, err := s.store.GetPool(r.Context(), poolID)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	host, err := core.NormalizeHost(r.URL.Query().Get("host"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	snapshot, err := s.store.BuildSnapshot(r.Context(), poolID)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	var found bool
	for _, domain := range snapshot.Domains {
		if domain.Host == host {
			found = true
			break
		}
	}
	if !found {
		s.handleError(w, r, storage.ErrNotFound)
		return
	}
	temporary, err := os.MkdirTemp("", "nsc-config-preview-")
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	defer os.RemoveAll(temporary)
	renderer := nginx.NewRenderer(s.config.NGINX)
	if err = renderer.Render(gateway.SnapshotToProto(snapshot), temporary); err != nil {
		s.handleError(w, r, err)
		return
	}
	domainFile := nginx.DomainFileName(host)
	domainPreview, err := readPreviewFragment(temporary, filepath.Join("domains", domainFile))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	upstreamNames := make(map[string]struct{})
	for _, route := range snapshot.Routes {
		// Paused routes are rendered as an explicit local 503 and intentionally
		// have no upstream fragment. Keep preview semantics identical to the
		// renderer instead of attempting to read a file that must not exist.
		if route.Host == host && !route.Paused {
			upstreamNames[route.UpstreamName] = struct{}{}
		}
	}
	names := make([]string, 0, len(upstreamNames))
	for name := range upstreamNames {
		names = append(names, name)
	}
	sort.Strings(names)
	upstreamPreviews := make([]configFragmentPreview, 0, len(names))
	for _, name := range names {
		preview, readErr := readPreviewFragment(temporary, filepath.Join("upstreams", name+".conf"))
		if readErr != nil {
			s.handleError(w, r, readErr)
			return
		}
		upstreamPreviews = append(upstreamPreviews, preview)
	}
	encoded, err := gateway.EncodeSnapshot(snapshot, gateway.DefaultChunkSize)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	mainConfig := renderer.MainConfig("/run/nginx.pid")
	mainDigest := sha256.Sum256(mainConfig)
	writeData(w, http.StatusOK, map[string]any{
		"pool_id": pool.ID, "host": host, "config_revision": pool.DesiredRevision, "probe_revision": pool.ProbeRevision,
		"renderer_version": nginx.RendererVersion, "model_digest": encoded.Header.ModelDigest,
		"main_config_sha256": hex.EncodeToString(mainDigest[:]), "main_config": string(mainConfig),
		"domain": domainPreview, "upstreams": upstreamPreviews,
	})
}

func readPreviewFragment(root, relative string) (configFragmentPreview, error) {
	content, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil {
		return configFragmentPreview{}, err
	}
	digest := sha256.Sum256(content)
	return configFragmentPreview{File: filepath.ToSlash(relative), SHA256: hex.EncodeToString(digest[:]), Config: string(content)}, nil
}

func (s *Server) gatewayRouteTrace(w http.ResponseWriter, r *http.Request) {
	pool, err := s.store.GetPool(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	host, err := core.NormalizeHost(r.URL.Query().Get("host"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	requestPath, err := core.NormalizePath(r.URL.Query().Get("path"))
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	domains, err := listAllDomains(r.Context(), s.store)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	var domain *model.Domain
	for index := range domains {
		if domains[index].PoolID == pool.ID && domains[index].Host == host {
			domain = &domains[index]
			break
		}
	}
	if domain == nil {
		writeData(w, http.StatusOK, map[string]any{"pool_id": pool.ID, "host": host, "path": requestPath, "matched": false, "reason": "domain_not_found"})
		return
	}
	routes, err := listAllRoutes(r.Context(), s.store)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	var matched *model.Route
	for index := range routes {
		candidate := &routes[index]
		if candidate.DomainID != domain.ID || !routePathMatches(candidate.Path, requestPath) {
			continue
		}
		if matched == nil || traceRoutePrecedes(*candidate, *matched) {
			matched = candidate
		}
	}
	entrypoints := make([]string, 0, 2)
	if domain.HTTPEnabled {
		entrypoints = append(entrypoints, core.EntryPointHTTP)
	}
	if domain.HTTPSEnabled {
		entrypoints = append(entrypoints, core.EntryPointHTTPS)
	}
	base := map[string]any{
		"pool_id": pool.ID, "host": host, "path": requestPath, "config_revision": pool.DesiredRevision,
		"probe_revision": pool.ProbeRevision, "domain": domain, "entrypoints": entrypoints,
	}
	if matched == nil {
		base["matched"] = false
		base["reason"] = "route_not_found"
		writeData(w, http.StatusOK, base)
		return
	}
	service, err := s.store.GetService(r.Context(), matched.ServiceID)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	runtime, err := s.store.GetRuntime(r.Context(), service.RuntimeID)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	targetPort := matched.TargetPort
	if targetPort == 0 {
		targetPort = service.EffectivePort()
	}
	protocol := matched.UpstreamProtocol
	if protocol == "" {
		protocol = service.EffectiveProtocol()
	}
	upstreamName := ""
	backendIdentityValid := service.EffectiveDNSName() != "" && targetPort >= 1 && targetPort <= 65535 && (protocol == "http" || protocol == "https")
	if backendIdentityValid {
		upstreamName, err = upstream.Name(runtime.ID, service.ServiceKey, service.EffectiveQualifiedName(), targetPort, protocol, matched.UpstreamServerName)
		if err != nil {
			backendIdentityValid = false
		}
	}
	gateways, err := listAllGateways(r.Context(), s.store)
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	applied := make([]model.Gateway, 0)
	for _, value := range gateways {
		if value.PoolID == pool.ID {
			applied = append(applied, value)
		}
	}
	base["matched"] = true
	base["route"] = matched
	base["service"] = service
	base["dns_name"] = service.EffectiveDNSName()
	base["dns_status"] = service.DNSStatus
	base["upstream_name"] = upstreamName
	base["backend_health"] = service.Status
	base["gateways"] = applied
	if !domain.Enabled || domain.Status == model.DomainDisabled {
		base["reason"] = "domain_disabled"
	} else if matched.Status == model.RoutePaused {
		base["reason"] = "route_paused"
	} else if !matched.Enabled || matched.Status == model.RouteDisabled {
		base["reason"] = "route_disabled"
	} else if matched.Status != model.RouteActive {
		base["reason"] = strings.ToLower(string(matched.Status))
	} else if !backendIdentityValid {
		base["reason"] = "invalid_backend_identity"
	} else if service.DNSStatus != model.DNSReady {
		base["reason"] = "dns_" + strings.ToLower(string(service.DNSStatus))
	} else {
		base["reason"] = "ready"
	}
	writeData(w, http.StatusOK, base)
}

func traceRoutePrecedes(candidate, current model.Route) bool {
	candidateConfigured := (candidate.Enabled && candidate.Status == model.RouteActive) || candidate.Status == model.RoutePaused
	currentConfigured := (current.Enabled && current.Status == model.RouteActive) || current.Status == model.RoutePaused
	if candidateConfigured != currentConfigured {
		return candidateConfigured
	}
	if len(candidate.Path) != len(current.Path) {
		return len(candidate.Path) > len(current.Path)
	}
	if candidate.Source.Priority() != current.Source.Priority() {
		return candidate.Source.Priority() > current.Source.Priority()
	}
	if candidate.Priority != current.Priority {
		return candidate.Priority > current.Priority
	}
	return candidate.ID < current.ID
}

func routePathMatches(routePath, requestPath string) bool {
	if routePath == "/" {
		return true
	}
	return strings.HasPrefix(requestPath, routePath)
}

func listAllDomains(ctx context.Context, store storage.Store) ([]model.Domain, error) {
	var result []model.Domain
	for after := ""; ; {
		values, err := store.ListDomains(ctx, storage.Page{Limit: 200, After: after})
		if err != nil {
			return nil, err
		}
		result = append(result, values...)
		if len(values) < 200 {
			return result, nil
		}
		after = values[len(values)-1].ID
	}
}

func listAllRoutes(ctx context.Context, store storage.Store) ([]model.Route, error) {
	var result []model.Route
	for after := ""; ; {
		values, err := store.ListRoutes(ctx, storage.Page{Limit: 200, After: after})
		if err != nil {
			return nil, err
		}
		result = append(result, values...)
		if len(values) < 200 {
			return result, nil
		}
		after = values[len(values)-1].ID
	}
}

func listAllGateways(ctx context.Context, store storage.Store) ([]model.Gateway, error) {
	var result []model.Gateway
	for after := ""; ; {
		values, err := store.ListGateways(ctx, storage.Page{Limit: 200, After: after})
		if err != nil {
			return nil, err
		}
		result = append(result, values...)
		if len(values) < 200 {
			return result, nil
		}
		after = values[len(values)-1].ID
	}
}
