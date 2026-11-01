package core

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/yernsun/network-service-composer/internal/storage"
)

// AlertScope contains only durable ownership identities, never user supplied
// names, URLs, diagnostics or configuration. Shared resources may have many.
type AlertScope struct {
	ProjectID     string `json:"project_id"`
	EnvironmentID string `json:"environment_id"`
}

func (c *Core) alertScopes(ctx context.Context, kind, resource string) ([]AlertScope, error) {
	gatewayPool := ""
	if kind == "gateway" {
		gateway, err := c.store.GetGateway(ctx, resource)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return nil, err
		}
		gatewayPool = gateway.PoolID
	}
	runtimePools := map[string]string{}
	applications := map[string]bool{}
	if kind == "application" {
		applications[resource] = true
	}
	if kind == "service" {
		service, err := c.store.GetService(ctx, resource)
		if err != nil {
			return nil, err
		}
		applications[service.ApplicationID] = true
	}
	if kind == "certificate" {
		for page := (storage.Page{Limit: 200}); ; {
			routes, err := c.store.ListRoutes(ctx, page)
			if err != nil {
				return nil, err
			}
			for _, route := range routes {
				domain, err := c.store.GetDomain(ctx, route.DomainID)
				if err != nil {
					return nil, err
				}
				if domain.CertificateID != resource || route.ServiceID == "" {
					continue
				}
				service, err := c.store.GetService(ctx, route.ServiceID)
				if err != nil {
					return nil, err
				}
				applications[service.ApplicationID] = true
			}
			if len(routes) < page.Limit {
				break
			}
			page.After = routes[len(routes)-1].ID
		}
	}
	result := []AlertScope{}
	for page := (storage.Page{Limit: 200}); ; {
		environments, err := c.ListEnvironments(ctx, "", page)
		if err != nil {
			return nil, err
		}
		for _, env := range environments {
			match := env.ApplicationID != "" && applications[env.ApplicationID]
			if kind == "environment" {
				match = env.ID == resource
			}
			if kind == "runtime" {
				match = env.RuntimeID == resource
			}
			if kind == "gateway" {
				match = slices.Contains(env.GatewayIDs, resource)
				if len(env.GatewayIDs) == 0 && gatewayPool != "" {
					pool, ok := runtimePools[env.RuntimeID]
					if !ok {
						runtime, err := c.store.GetRuntime(ctx, env.RuntimeID)
						if err != nil {
							return nil, err
						}
						pool = runtime.PoolID
						runtimePools[env.RuntimeID] = pool
					}
					match = pool == gatewayPool
				}
			}
			if match {
				result = append(result, AlertScope{env.ProjectID, env.ID})
			}
		}
		if len(environments) < page.Limit {
			return result, nil
		}
		page.After = environments[len(environments)-1].ID
	}
}

func (c *Core) notificationScopes(ctx context.Context, event *NotificationEvent) error {
	kind := ""
	switch {
	case strings.HasPrefix(event.Type, "deployment.system_"):
		event.Scopes = []AlertScope{}
		event.EnvironmentID = ""
		return nil
	case event.Type == "notification.test":
		return nil
	case strings.HasPrefix(event.Type, "image."):
		kind = "environment"
	case strings.HasPrefix(event.Type, "deployment."):
		kind = "application"
	case strings.HasPrefix(event.Type, "certificate."):
		kind = "certificate"
	case strings.HasPrefix(event.Type, "gateway."):
		kind = "gateway"
	case strings.HasPrefix(event.Type, "runtime."):
		kind = "runtime"
	}
	key := event.ResourceID
	if kind == "environment" {
		key = event.EnvironmentID
	}
	var err error
	event.Scopes, err = c.alertScopes(ctx, kind, key)
	if err != nil {
		return err
	}
	event.EnvironmentID = ""
	if len(event.Scopes) == 1 {
		event.EnvironmentID = event.Scopes[0].EnvironmentID
	}
	return nil
}
