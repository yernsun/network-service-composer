package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
	"sort"
)

type NginxPreviewInput struct {
	Routes         []storage.RouteWrite    `json:"routes,omitempty"`
	AccessControls []model.IPAccessControl `json:"access_controls,omitempty"`
	Snippets       []model.ConfigSnippet   `json:"snippets"`
	DomainID       string                  `json:"domain_id,omitempty"`
	FocusIndex     *int                    `json:"focus_index,omitempty"`
}
type NginxPreview struct {
	Candidate       nginxx.PreviewDocument `json:"candidate"`
	Active          nginxx.PreviewDocument `json:"active"`
	Domains         []model.SnapshotDomain `json:"domains"`
	DomainID        string                 `json:"domain_id"`
	ConfigRevision  int64                  `json:"config_revision"`
	RendererVersion string                 `json:"renderer_version"`
}

func (c *Core) PreviewNginx(ctx context.Context, pool string, in NginxPreviewInput, renderSnapshot func(model.Snapshot) (nginxx.PreviewDocument, error)) (NginxPreview, error) {
	result := NginxPreview{}
	// Validate full input before filtering; callers cannot hide invalid references
	// in another domain. This operation never saves a draft or changes revision.
	for i, s := range in.Snippets {
		if _, e := nginxx.ParseSnippet(s.Scope, s.Content); e != nil {
			var se *nginxx.SnippetError
			line := 1
			if errors.As(e, &se) {
				line = se.Line
			}
			return result, invalidParams("validation.control_invalid", fmt.Sprintf("snippets.%d.content", i), e.Error(), map[string]any{"reason": e.Error(), "line": line, "snippet_index": i})
		}
	}
	if e := c.validateNginxConfiguration(ctx, pool, NginxConfiguration{Snippets: in.Snippets, AccessControls: in.AccessControls, Routes: in.Routes}); e != nil {
		return result, e
	}
	snap, e := c.store.BuildSnapshot(ctx, pool)
	if e != nil {
		return result, e
	}
	candidate, e := c.overlayNginxRoutes(ctx, pool, snap, NginxConfiguration{Snippets: in.Snippets, AccessControls: in.AccessControls, Routes: in.Routes})
	if e != nil {
		return result, e
	}
	result.ConfigRevision = snap.Revision
	result.RendererVersion = nginxx.RendererVersion
	result.Domains = append([]model.SnapshotDomain{}, snap.Domains...)
	sort.Slice(result.Domains, func(i, j int) bool { return result.Domains[i].Host < result.Domains[j].Host })
	target := in.DomainID
	if in.FocusIndex != nil {
		if *in.FocusIndex < 0 || *in.FocusIndex >= len(in.Snippets) {
			return result, controlInvalid("focus_index", "invalid snippet index")
		}
		if target == "" {
			snippet := in.Snippets[*in.FocusIndex]
			if snippet.Scope == "server" {
				target = snippet.ResourceID
			}
			if snippet.Scope == "location" {
				route, err := c.store.GetRoute(ctx, snippet.ResourceID)
				if err != nil {
					return result, err
				}
				target = route.DomainID
			}
		}
	}
	if target == "" && len(result.Domains) > 0 {
		target = result.Domains[0].ID
	}
	result.DomainID = target
	if target != "" {
		var selected *model.SnapshotDomain
		for _, d := range snap.Domains {
			if d.ID == target {
				copy := d
				selected = &copy
			}
		}
		if selected == nil {
			return result, storage.ErrNotFound
		}
		snap.Domains = []model.SnapshotDomain{*selected}
		routes := []model.SnapshotRoute{}
		for _, route := range snap.Routes {
			if route.Host == selected.Host {
				routes = append(routes, route)
			}
		}
		snap.Routes = routes
		candidate.Domains = snap.Domains
		candidateRoutes := []model.SnapshotRoute{}
		for _, route := range candidate.Routes {
			if route.Host == selected.Host {
				candidateRoutes = append(candidateRoutes, route)
			}
		}
		candidate.Routes = candidateRoutes
	}
	render := func(snippets []model.ConfigSnippet, accessControls []model.IPAccessControl) (nginxx.PreviewDocument, error) {
		snap.Snippets = snippets
		snap.AccessControls = accessControls
		return renderSnapshot(snap)
	}
	result.Active, e = render(snap.Snippets, snap.AccessControls)
	if e != nil {
		return result, e
	}
	result.Candidate, e = renderSnapshot(candidate)
	return result, e
}
