package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"sort"
	"strings"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
	"github.com/yernsun/network-service-composer/internal/upstream"
)

func (c *Core) validateNginxRoutes(ctx context.Context, pool string, changes []storage.RouteWrite) error {
	seen := map[string]bool{}
	for i, change := range changes {
		route := change.Route
		field := fmt.Sprintf("routes/%d", i)
		if id.Validate(route.ID, id.Route) != nil || seen[route.ID] {
			return controlInvalid(field+"/id", "invalid or duplicate route ID")
		}
		seen[route.ID] = true
		if e := c.store.CheckProjectResource(ctx, route.DomainID); e != nil {
			return e
		}
		domain, err := c.store.GetDomain(ctx, route.DomainID)
		if err != nil {
			return err
		}
		if domain.PoolID != pool {
			return storage.ErrNotFound
		}
		current, err := c.store.GetRoute(ctx, route.ID)
		if change.ExpectedRevision > 0 {
			if err != nil {
				return err
			}
			if current.Revision != change.ExpectedRevision || current.DomainID != route.DomainID || current.Source != model.SourceAPI {
				return storage.ErrPrecondition
			}
		} else if err == nil {
			return storage.ErrPrecondition
		} else if !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		if change.Delete {
			continue
		}
		clean := path.Clean(route.Path)
		if route.Path != "/" && strings.HasSuffix(route.Path, "/") {
			clean += "/"
		}
		if !strings.HasPrefix(route.Path, "/") || !safePathText(route.Path) || strings.Contains(route.Path, "$") || clean != route.Path || strings.HasPrefix(route.Path, "/.well-known/acme-challenge/") {
			return controlInvalid(field+"/path", "invalid route path")
		}
		if route.Match != "exact" && route.Match != "prefix" && route.Match != "prefer" {
			return controlInvalid(field+"/match", "invalid route match")
		}
		if route.Kind != "proxy" && route.Kind != "return" && route.Kind != "redirect" {
			return controlInvalid(field+"/kind", "invalid route kind")
		}
		if route.ReadTimeoutSeconds < 0 || route.ReadTimeoutSeconds > 3600 || route.ConnectTimeoutSeconds < 0 || route.ConnectTimeoutSeconds > 300 {
			return controlInvalid(field, "invalid proxy timeout")
		}
		if route.Kind != "proxy" {
			if route.ServiceID != "" || route.ExternalURL != "" {
				return controlInvalid(field, "response route cannot have an upstream")
			}
			if err := nginxx.ValidateRouteResponse(route.Kind, route.ResponseCode, route.ResponseValue); err != nil {
				return controlInvalid(field+"/response_value", err.Error())
			}
			continue
		}
		if route.ExternalURL != "" {
			if route.ServiceID != "" {
				return controlInvalid(field, "proxy target must be either a Service or URL")
			}
			if err := nginxx.ValidateExternalProxyURL(route.ExternalURL); err != nil {
				return controlInvalid(field+"/external_url", err.Error())
			}
		} else {
			if route.ServiceID == "" {
				return controlInvalid(field+"/service_id", "proxy Service is required")
			}
			service, err := c.store.GetService(ctx, route.ServiceID)
			if err != nil {
				return err
			}
			runtime, err := c.store.GetRuntime(ctx, service.RuntimeID)
			if err != nil {
				return err
			}
			if runtime.PoolID != pool || service.DNSStatus != model.DNSReady {
				return controlInvalid(field+"/service_id", "Service DNS is unavailable")
			}
		}
		if route.StripPath && (route.Match == "exact" || !strings.HasSuffix(route.Path, "/")) {
			return controlInvalid(field+"/strip_path", "strip path needs a directory prefix")
		}
	}
	return nil
}

func (c *Core) nginxCandidateSnapshot(ctx context.Context, pool string, draft NginxConfiguration) (model.Snapshot, error) {
	snapshot, err := c.BuildGatewaySnapshot(ctx, pool)
	if err != nil {
		return snapshot, err
	}
	return c.overlayNginxRoutes(ctx, pool, snapshot, draft)
}

func (c *Core) overlayNginxRoutes(ctx context.Context, pool string, snapshot model.Snapshot, draft NginxConfiguration) (model.Snapshot, error) {
	snapshot.Services = maps.Clone(snapshot.Services)
	if len(draft.Routes) == 0 {
		snapshot.Snippets = draft.Snippets
		snapshot.AccessControls = draft.AccessControls
		return snapshot, nil
	}
	changed := map[string]storage.RouteWrite{}
	for _, change := range draft.Routes {
		changed[change.Route.ID] = change
	}
	routes := make([]model.SnapshotRoute, 0, len(snapshot.Routes)+len(changed))
	for _, route := range snapshot.Routes {
		if _, ok := changed[route.ID]; !ok {
			routes = append(routes, route)
		}
	}
	domains := map[string]model.SnapshotDomain{}
	for _, domain := range snapshot.Domains {
		domains[domain.ID] = domain
	}
	for _, change := range draft.Routes {
		if change.Delete || !change.Route.Enabled {
			continue
		}
		route := change.Route
		domain, ok := domains[route.DomainID]
		if !ok {
			return snapshot, storage.ErrNotFound
		}
		candidate := model.SnapshotRoute{ID: route.ID, Host: domain.Host, Path: route.Path, Priority: route.Priority, StripPath: route.StripPath,
			Kind: route.Kind, Match: route.Match, ServiceID: route.ServiceID, ExternalURL: route.ExternalURL, ResponseCode: route.ResponseCode,
			ResponseValue: route.ResponseValue, ReadTimeoutSeconds: route.ReadTimeoutSeconds, ConnectTimeoutSeconds: route.ConnectTimeoutSeconds,
			TargetPort: route.TargetPort, UpstreamProtocol: route.UpstreamProtocol, UpstreamServerName: route.UpstreamServerName,
			WebsocketEnabled: route.WebsocketEnabled, TLSMode: domain.TLSMode, HTTPSRedirect: domain.HTTPSRedirect, CertificateID: domain.CertificateID}
		if route.Kind == "proxy" && route.ExternalURL == "" {
			service, e := c.store.GetService(ctx, route.ServiceID)
			if e != nil {
				return snapshot, e
			}
			if candidate.TargetPort == 0 {
				candidate.TargetPort = service.EffectivePort()
			}
			if candidate.UpstreamProtocol == "" {
				candidate.UpstreamProtocol = service.EffectiveProtocol()
			}
			qualified := service.EffectiveQualifiedName()
			candidate.UpstreamName, e = upstream.Name(service.RuntimeID, service.ServiceKey, qualified, candidate.TargetPort, candidate.UpstreamProtocol, candidate.UpstreamServerName)
			if e != nil {
				return snapshot, e
			}
			snapshot.Services[service.ID] = model.SnapshotService{ID: service.ID, QualifiedName: qualified, DNSName: service.EffectiveDNSName(), Protocol: service.EffectiveProtocol()}
		} else if route.Kind == "proxy" {
			if strings.HasPrefix(route.ExternalURL, "https://") {
				candidate.UpstreamProtocol = "https"
			} else {
				candidate.UpstreamProtocol = "http"
			}
		}
		routes = append(routes, candidate)
	}
	seen := map[string]bool{}
	for _, route := range routes {
		key := route.Host + "\x00" + route.Path + "\x00" + route.Match
		if seen[key] {
			return snapshot, controlInvalid("routes", "duplicate route match")
		}
		seen[key] = true
	}
	sort.SliceStable(routes, func(i, j int) bool {
		if routes[i].Host != routes[j].Host {
			return routes[i].Host < routes[j].Host
		}
		if len(routes[i].Path) != len(routes[j].Path) {
			return len(routes[i].Path) > len(routes[j].Path)
		}
		return routes[i].ID < routes[j].ID
	})
	snapshot.Routes = routes
	snapshot.Snippets = draft.Snippets
	snapshot.AccessControls = draft.AccessControls
	return snapshot, nil
}

// NginxDraftSnapshot returns the same candidate used for gateway validation.
func (c *Core) NginxDraftSnapshot(ctx context.Context, pool string) (model.Snapshot, error) {
	draft, err := c.NginxConfiguration(ctx, pool, "nginx-draft")
	if err != nil {
		return model.Snapshot{}, err
	}
	return c.nginxCandidateSnapshot(ctx, pool, draft)
}
