package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// ReconcileGatewayApplications consumes authenticated, fresh observations keyed
// by runtime and actual service/container ID. Names never establish ownership.
func (c *Core) ReconcileGatewayApplications(ctx context.Context, project Project, cluster string, definitions []config.SystemApplicationConfig, evidence map[string]map[string]bool) error {
	return c.ReconcileGatewayApplicationsWithRetirements(ctx, project, cluster, definitions, evidence, nil)
}

// Stopped evidence is supplied only by the authenticated node collector, never
// by an API request or by the absence of running instances in inventory.
func (c *Core) ReconcileGatewayApplicationsWithRetirements(ctx context.Context, project Project, cluster string, definitions []config.SystemApplicationConfig, evidence, stopped map[string]map[string]bool) error {
	if !project.System || cluster == "" {
		return storage.ErrConflict
	}
	marker, err := c.store.GetControlDocument(ctx, "system-project:gateway")
	if err != nil {
		return err
	}
	var owner struct {
		ProjectID string `json:"project_id"`
		ClusterID string `json:"cluster_id"`
	}
	if err = json.Unmarshal(marker.Payload, &owner); err != nil {
		return err
	}
	if owner.ProjectID != project.ID || owner.ClusterID != cluster {
		return storage.ErrConflict
	}
	envs, err := c.allProjectEnvironments(ctx, project.ID)
	if err != nil {
		return err
	}
	byName := map[string]Environment{}
	for _, e := range envs {
		byName[e.Name] = e
	}
	writes := []storage.ControlWrite{}
	add := func(key, kind, parent string, value any) error {
		expected := int64(0)
		old, e := c.store.GetControlDocument(ctx, key)
		if e == nil {
			if old.Kind != kind || old.ParentID != parent {
				return storage.ErrConflict
			}
			expected = old.Revision
			encoded, e := json.Marshal(value)
			if e != nil {
				return e
			}
			if string(old.Payload) == string(encoded) {
				return nil
			}
		} else if !errors.Is(e, storage.ErrNotFound) {
			return e
		}
		w, e := c.controlWrite(key, kind, parent, value, expected)
		if e == nil {
			writes = append(writes, w)
		}
		return e
	}
	plans := []storage.SystemApplicationRegistration{}
	keys := map[string]bool{}
	claimed := map[string]bool{}
	for _, d := range definitions {
		names := map[string]string{"controller": "Controller", "postgresql": "PostgreSQL", "nginx": "NGINX", "nginx-sandbox": "NGINX Sandbox"}
		if names[d.Key] == "" || d.Name != names[d.Key] || keys[d.Key] || len(d.ObjectIDs) == 0 || (d.Environment != "prod" && d.Environment != "sandbox") || (d.Key == "nginx-sandbox") != (d.Environment == "sandbox") {
			return fmt.Errorf("invalid system application registration")
		}
		keys[d.Key] = true
		rt, e := c.store.GetRuntime(ctx, d.RuntimeID)
		if e != nil {
			return e
		}
		if !rt.Enabled || (rt.Type != model.RuntimeSwarm && rt.Type != model.RuntimeDocker) {
			return storage.ErrConflict
		}
		env, found := byName[d.Environment]
		if !found {
			if d.Environment != "sandbox" {
				return storage.ErrConflict
			}
			env = Environment{ProjectID: project.ID, Name: "sandbox", DisplayName: "sandbox", Kind: "sandbox", RuntimeID: d.RuntimeID, ProjectName: "gateway-sandbox", System: true, GatewayIDs: []string{}}
			env.ID = "env_" + digestString(project.ID + ":sandbox")[:32]
			if e = add(env.ID, "environment", project.ID, env); e != nil {
				return e
			}
			if e = add("environment-name:"+project.ID+":sandbox", "environment-name", project.ID, map[string]string{"environment_id": env.ID}); e != nil {
				return e
			}
			byName[env.Name] = env
		}
		appID := "app_" + digestString(project.ID + ":" + d.Key)[:32]
		ref := model.EnvironmentReference{System: true, ID: env.ID, Name: env.Name, ProjectID: project.ID, ProjectName: env.ProjectName}
		a := model.Application{ID: appID, Name: d.Name, RuntimeID: d.RuntimeID, ProjectName: "gateway-" + d.Key, System: true, SystemKey: d.Key, Environment: &ref, Status: model.ApplicationRunning, Metadata: model.Metadata{Revision: 1, CreatedAt: c.now(), UpdatedAt: c.now()}}
		services, e := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{RuntimeID: d.RuntimeID})
		if e != nil {
			return e
		}
		ids := []string{}
		revisions := map[string]int64{}
		selectedObjects := map[string]bool{}
		for _, object := range d.ObjectIDs {
			selectedObjects[object] = true
		}
		selectedServices := map[string]bool{}
		running := false
		unknown := false
		for _, object := range d.ObjectIDs {
			if object == "" || claimed[d.RuntimeID+":"+object] || !evidence[d.RuntimeID][object] {
				return fmt.Errorf("missing or duplicate system runtime object")
			}
			claimed[d.RuntimeID+":"+object] = true
			matches := []model.Service{}
			for _, s := range services {
				match := rt.Type == model.RuntimeSwarm && s.RuntimeObjectID == object
				if rt.Type == model.RuntimeDocker {
					endpoints, e := c.store.ListEndpoints(ctx, s.ID)
					if e != nil {
						return e
					}
					for _, ep := range endpoints {
						if ep.ExternalID == object {
							match = true
						}
					}
				}
				if match {
					matches = append(matches, s)
				}
			}
			if len(matches) != 1 {
				return fmt.Errorf("system object must resolve to exactly one observed service")
			}
			s := matches[0]
			if s.Managed || (s.ApplicationID != "" && s.ApplicationID != appID) {
				return storage.ErrConflict
			}
			if rt.Type == model.RuntimeDocker {
				endpoints, e := c.store.ListEndpoints(ctx, s.ID)
				if e != nil {
					return e
				}
				for _, ep := range endpoints {
					if ep.ReadyState != model.Removed && !selectedObjects[ep.ExternalID] {
						return fmt.Errorf("selected system service contains an unregistered container")
					}
				}
			}
			if selectedServices[s.ID] {
				continue
			}
			selectedServices[s.ID] = true
			if claimed["service:"+s.ID] {
				return fmt.Errorf("system service registered more than once")
			}
			claimed["service:"+s.ID] = true
			ids = append(ids, s.ID)
			revisions[s.ID] = s.Revision
			running = running || s.RunningInstances > 0
			unknown = unknown || s.LastSeenAt == nil || c.now().Sub(*s.LastSeenAt) > time.Minute
		}
		if unknown {
			a.Status = model.ApplicationUnknown
		} else if !running {
			a.Status = model.ApplicationUnknown
		} // Absence of running instances is not stop evidence.
		if e = add(a.ID, "system-application", env.ID, map[string]any{"system": true, "key": d.Key, "cluster_id": cluster, "runtime_id": d.RuntimeID, "environment": ref, "object_ids": d.ObjectIDs, "observed_at": c.now().Truncate(30 * time.Second)}); e != nil {
			return e
		}
		if e = add("application-environment:"+a.ID, "application-environment", a.ID, map[string]string{"environment_id": env.ID}); e != nil {
			return e
		}
		replacement, e := c.systemApplicationReplacement(ctx, a, d, stopped)
		if e != nil {
			return e
		}
		if replacement != nil {
			for _, object := range d.ReplacesObjectIDs {
				if e = add(object, "system-service", replacement.PreviousRuntimeID, storage.SystemService{EnvironmentID: env.ID, ProjectID: project.ID, ClusterID: cluster, System: true}); e != nil {
					return e
				}
			}
			if e = add("system-application-retirement:"+a.ID+":"+replacement.PreviousRuntimeID, "system-application-retirement", a.ID, map[string]any{"previous_runtime_id": replacement.PreviousRuntimeID, "previous_object_ids": d.ReplacesObjectIDs, "previous_services": replacement.ServiceRevisions, "runtime_id": a.RuntimeID, "object_ids": d.ObjectIDs, "retired_at": c.now()}); e != nil {
				return e
			}
		}
		sort.Strings(ids)
		plans = append(plans, storage.SystemApplicationRegistration{Application: a, ServiceIDs: ids, ServiceRevisions: revisions, Replacement: replacement})
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].Application.ID < plans[j].Application.ID })
	return c.store.ReconcileSystemApplications(storage.WithSystemBootstrap(ctx), project.ID, plans, writes)
}
