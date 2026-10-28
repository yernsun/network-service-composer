package core

import (
	"context"
	"errors"
	"fmt"

	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func (c *Core) validateIPAccessControls(ctx context.Context, pool string, in NginxConfiguration) error {
	if len(in.AccessControls) > 1000 {
		return controlInvalid("access_controls", "too many access controls")
	}
	seen := map[string]bool{}
	for i, acl := range in.AccessControls {
		field := fmt.Sprintf("access_controls.%d", i)
		key := acl.Scope + ":" + acl.ResourceID
		if acl.ResourceID == "" || (acl.Scope != "server" && acl.Scope != "location") || seen[key] {
			return controlInvalid(field, "invalid or duplicate access control target")
		}
		seen[key] = true
		if err := c.store.CheckProjectResource(ctx, acl.ResourceID); err != nil {
			return err
		}
		domainID := acl.ResourceID
		if acl.Scope == "location" {
			route, err := c.store.GetRoute(ctx, acl.ResourceID)
			if errors.Is(err, storage.ErrNotFound) {
				for _, change := range in.Routes {
					if change.Route.ID == acl.ResourceID && !change.Delete {
						route = change.Route
						err = nil
						break
					}
				}
			}
			if err != nil {
				return err
			}
			for _, change := range in.Routes {
				if change.Route.ID == acl.ResourceID && change.Delete {
					return controlInvalid(field, "access control targets a deleted route")
				}
			}
			domainID = route.DomainID
		}
		domain, err := c.store.GetDomain(ctx, domainID)
		if err != nil {
			return err
		}
		if domain.PoolID != pool {
			return storage.ErrNotFound
		}
		if acl.ClientIP != nil {
			if acl.Scope != "server" {
				return controlInvalid(field+".client_ip", "client IP source must be configured on the Domain")
			}
			if !nginxx.ValidClientIPHeader(acl.ClientIP.Header) {
				return controlInvalid(field+".client_ip.header", "invalid client IP header name")
			}
			if len(acl.ClientIP.TrustedProxies) == 0 || len(acl.ClientIP.TrustedProxies) > 1000 {
				return controlInvalid(field+".client_ip.trusted_proxies", "configure between 1 and 1000 trusted proxy IP entries")
			}
			for j, raw := range acl.ClientIP.TrustedProxies {
				if _, err := nginxx.ParseIPRange(raw); err != nil {
					return controlInvalid(fmt.Sprintf("%s.client_ip.trusted_proxies.%d", field, j), err.Error())
				}
			}
		}
		if len(acl.Access)+len(acl.Forbidden) > 1000 {
			return controlInvalid(field, "too many IP entries")
		}
		for _, list := range []struct {
			name   string
			values []string
		}{{"access", acl.Access}, {"forbidden", acl.Forbidden}} {
			for j, raw := range list.values {
				if _, err := nginxx.ParseIPRange(raw); err != nil {
					return controlInvalid(fmt.Sprintf("%s.%s.%d", field, list.name, j), err.Error())
				}
			}
		}
		if len(acl.Access)+len(acl.Forbidden) == 0 {
			continue
		}
		for _, snippet := range in.Snippets {
			if snippet.Scope != acl.Scope || snippet.ResourceID != acl.ResourceID {
				continue
			}
			directives, err := nginxx.ParseSnippet(snippet.Scope, snippet.Content)
			if err != nil {
				return controlInvalid(field, err.Error())
			}
			for _, d := range directives {
				if d.Name == "allow" || d.Name == "deny" {
					return controlInvalid(field, "remove manual allow/deny before configuring IP access control")
				}
			}
		}
	}
	return nil
}
