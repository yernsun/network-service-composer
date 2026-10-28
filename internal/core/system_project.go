package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// EnsureGatewayProject consumes an explicit deployment registration and a fresh,
// authenticated Manager observation. Discovery names and labels grant no ownership.
// It deliberately does not require Docker management to be enabled.
func (c *Core) EnsureGatewayProject(ctx context.Context, registration config.SystemProjectConfig, clusterID string, objects map[string]bool) (Project, error) {
	if registration.ClusterID == "" || registration.ClusterID != clusterID || registration.RuntimeID == "" || len(registration.Services) == 0 {
		return Project{}, fmt.Errorf("invalid Gateway deployment registration or cluster identity")
	}
	rt, err := c.store.GetRuntime(ctx, registration.RuntimeID)
	if err != nil {
		return Project{}, err
	}
	if rt.Type != model.RuntimeSwarm || !rt.Enabled {
		return Project{}, fmt.Errorf("Gateway registration requires an enabled Swarm runtime")
	}
	seen := map[string]bool{}
	for _, resource := range registration.Services {
		if resource.ObjectID == "" || seen[resource.ObjectID] || !objects[resource.ObjectID] || (resource.Environment != "prod" && resource.Environment != "sandbox") {
			return Project{}, fmt.Errorf("Gateway registration contains a duplicate, missing or invalid runtime object")
		}
		seen[resource.ObjectID] = true
	}
	// Refuse to reclassify an application-owned workload, even if its object ID
	// was accidentally placed in the deployment manifest.
	services, err := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{RuntimeID: rt.ID})
	if err != nil {
		return Project{}, err
	}
	for _, service := range services {
		if seen[service.RuntimeObjectID] && (service.Managed || service.ApplicationID != "") {
			app, e := c.store.GetApplication(ctx, service.ApplicationID)
			if e != nil || !app.System {
				return Project{}, fmt.Errorf("Gateway registration overlaps application ownership: %w", storage.ErrConflict)
			}
		}
	}
	// The globally unique system marker and slug claim are committed together with
	// initial environment. A racing initializer either owns the whole set or none.
	marker := "system-project:gateway"
	var saved struct {
		ProjectID string `json:"project_id"`
		ClusterID string `json:"cluster_id"`
		RuntimeID string `json:"runtime_id"`
	}
	d, err := c.store.GetControlDocument(ctx, marker)
	var p Project
	if err == nil {
		if err = json.Unmarshal(d.Payload, &saved); err != nil {
			return p, err
		}
		if d.Kind != "system-project" || saved.ClusterID != clusterID || saved.RuntimeID != rt.ID {
			return p, storage.ErrConflict
		}
		p, err = c.GetProject(ctx, saved.ProjectID)
		if err != nil {
			return p, err
		}
		if !p.System {
			return p, storage.ErrConflict
		}
	} else if errors.Is(err, storage.ErrNotFound) {
		for page := (storage.Page{Limit: 200}); ; {
			projects, e := c.ListProjects(ctx, page)
			if e != nil {
				return p, e
			}
			for _, existing := range projects {
				if strings.EqualFold(existing.Name, "Gateway") {
					return p, fmt.Errorf("Gateway name is already owned by an ordinary project: %w", storage.ErrConflict)
				}
			}
			if len(projects) < page.Limit {
				break
			}
			page.After = projects[len(projects)-1].ID
		}
		p = Project{Name: "Gateway", Slug: "gateway", Description: "NSC cluster infrastructure", DefaultRuntimeID: rt.ID, System: true}
		p.ID = id.New("prj")
		prod := Environment{ProjectID: p.ID, Name: "prod", DisplayName: "prod", Kind: "production", RuntimeID: rt.ID, ProjectName: "gateway-prod", System: true, GatewayIDs: []string{}}
		prod.ID = id.New("env")
		p.InitialEnvironmentID = prod.ID
		writes := []storage.ControlWrite{}
		add := func(key, kind, parent string, value any) error {
			w, e := c.controlWrite(key, kind, parent, value, 0)
			writes = append(writes, w)
			return e
		}
		if err = add(p.ID, "project", "", p); err != nil {
			return p, err
		}
		if err = add(prod.ID, "environment", p.ID, prod); err != nil {
			return p, err
		}
		if err = add("environment-name:"+p.ID+":prod", "environment-name", p.ID, map[string]string{"environment_id": prod.ID}); err != nil {
			return p, err
		}
		if err = add("project-slug:gateway", "project-slug", "", map[string]string{"project_id": p.ID}); err != nil {
			return p, err
		}
		if err = add(marker, "system-project", "", map[string]string{"project_id": p.ID, "cluster_id": clusterID, "runtime_id": rt.ID}); err != nil {
			return p, err
		}
		if err = c.store.WriteControlDocuments(storage.WithSystemBootstrap(ctx), writes); err != nil {
			if errors.Is(err, storage.ErrConflict) { // Only a winning system initializer may resolve this collision.
				if _, e := c.store.GetControlDocument(ctx, marker); e == nil {
					return c.EnsureGatewayProject(ctx, registration, clusterID, objects)
				}
			}
			return Project{}, fmt.Errorf("initialize Gateway project (reserved slug may already exist): %w", err)
		}
		p, err = c.GetProject(ctx, p.ID)
		if err != nil {
			return p, err
		}
	} else {
		return p, err
	}
	envs, err := c.allProjectEnvironments(ctx, p.ID)
	if err != nil {
		return p, err
	}
	byName := map[string]Environment{}
	for _, e := range envs {
		byName[e.Name] = e
	}
	writes := []storage.ControlWrite{}
	for _, resource := range registration.Services {
		e, found := byName[resource.Environment]
		if !found {
			if resource.Environment != "sandbox" {
				return p, storage.ErrConflict
			}
			e = Environment{ProjectID: p.ID, Name: "sandbox", DisplayName: "sandbox", Kind: "sandbox", RuntimeID: rt.ID, ProjectName: "gateway-sandbox", System: true, GatewayIDs: []string{}}
			e.ID = "env_" + digestString(p.ID + ":sandbox")[:32]
			w, err := c.controlWrite(e.ID, "environment", p.ID, e, 0)
			if err != nil {
				return p, err
			}
			writes = append(writes, w)
			w, err = c.controlWrite("environment-name:"+p.ID+":sandbox", "environment-name", p.ID, map[string]string{"environment_id": e.ID}, 0)
			if err != nil {
				return p, err
			}
			writes = append(writes, w)
			byName[e.Name] = e
		}
		claim := storage.SystemService{EnvironmentID: e.ID, ProjectID: p.ID, ClusterID: clusterID, System: true}
		// Object IDs are used as document keys; parent runtime additionally fences
		// cross-runtime reuse. Retired registrations remain protected historical evidence.
		old, err := c.store.GetControlDocument(ctx, resource.ObjectID)
		if err == nil {
			var previous storage.SystemService
			if err = json.Unmarshal(old.Payload, &previous); err != nil {
				return p, err
			}
			if old.Kind != "system-service" || old.ParentID != rt.ID || previous != claim {
				return p, storage.ErrConflict
			}
			continue
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return p, err
		}
		w, err := c.controlWrite(resource.ObjectID, "system-service", rt.ID, claim, 0)
		if err != nil {
			return p, err
		}
		writes = append(writes, w)
	}
	if len(writes) > 0 {
		err = c.store.WriteControlDocuments(storage.WithSystemBootstrap(ctx), writes)
	}
	return p, err
}
