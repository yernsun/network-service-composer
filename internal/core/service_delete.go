package core

import (
	"context"
	"errors"
	"maps"
	"strings"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// QueueDeleteService publishes a revision without one service. The worker
// removes only that runtime object, then atomically marks the service and its
// routes unavailable while preserving volumes and history.
func (c *Core) QueueDeleteService(ctx context.Context, serviceID, actor, idem string, expected int64, acknowledge bool) (model.DeploymentOperation, error) {
	if !acknowledge || strings.TrimSpace(idem) == "" {
		return model.DeploymentOperation{}, controlInvalid("acknowledge_data_boundary", "acknowledge service and route unavailability and supply an idempotency key")
	}
	service, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	if err = c.store.CheckProjectResource(ctx, service.ID); err != nil {
		return model.DeploymentOperation{}, err
	}
	if existing, readErr := c.store.GetDeploymentOperationByIdempotency(ctx, service.RuntimeID, idem); readErr == nil {
		if existing.ServiceID != service.ID || existing.Type != "UPGRADE" {
			return model.DeploymentOperation{}, storage.ErrConflict
		}
		revision, e := c.store.GetApplicationRevision(ctx, existing.RevisionID)
		if e != nil {
			return model.DeploymentOperation{}, e
		}
		scope := revision.ConfigurationSource.ServiceScope
		if scope == nil || scope.Mode != "delete" || len(scope.Names) != 1 || scope.TargetServiceRevision != expected {
			return model.DeploymentOperation{}, storage.ErrConflict
		}
		return existing, nil
	} else if !errors.Is(readErr, storage.ErrNotFound) {
		return model.DeploymentOperation{}, readErr
	}
	if service.Revision != expected {
		return model.DeploymentOperation{}, storage.ErrPrecondition
	}
	if service.System || !service.Managed || service.ApplicationID == "" || service.RemovedAt != nil {
		return model.DeploymentOperation{}, storage.ErrSystemProtected
	}
	runtime, err := c.requireManagement(ctx, service.RuntimeID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	app, err := c.store.GetApplication(ctx, service.ApplicationID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	if app.System || !app.Managed || app.Status != model.ApplicationRunning || app.ActiveRevisionID == "" || app.RuntimeID != service.RuntimeID || c.serviceRevisionInFlight(ctx, app) {
		return model.DeploymentOperation{}, storage.ErrPrecondition
	}
	name := strings.TrimPrefix(service.ServiceKey, app.ProjectName+"/")
	if runtime.Type == model.RuntimeSwarm {
		name = strings.TrimPrefix(service.ServiceKey, app.ProjectName+"_")
	}
	if name == service.ServiceKey || name == "" {
		return model.DeploymentOperation{}, storage.ErrPrecondition
	}
	source, err := c.ApplicationSource(ctx, app.ID, app.ActiveRevisionID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	candidate, err := applicationx.RemoveService(source.ComposeYAML, name)
	if err != nil {
		return model.DeploymentOperation{}, controlInvalid("service_id", err.Error())
	}
	base, err := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	scope := &model.ServiceDeploymentScope{Mode: "delete", Names: []string{name}, BaseRevisionID: app.ActiveRevisionID, TargetServiceRevision: service.Revision, TargetRuntimeObjectID: service.RuntimeObjectID,
		ObservedReplicaOverrides: map[string]int{}, ReplicaOverrides: map[string]int{}}
	for key, count := range app.ReplicaOverrides {
		if key == name {
			scope.ObservedReplicaOverrides[name] = count
		} else {
			scope.ReplicaOverrides[key] = count
		}
	}
	configuration := base.ConfigurationSource
	configuration.RestoredFrom = ""
	configuration.Images = maps.Clone(configuration.Images)
	configuration.TaggedImages = maps.Clone(configuration.TaggedImages)
	configuration.ServiceRevisions = maps.Clone(configuration.ServiceRevisions)
	delete(configuration.Images, name)
	delete(configuration.TaggedImages, name)
	delete(configuration.ServiceRevisions, name)
	if configuration.Entrypoint != nil && configuration.Entrypoint.Service == name {
		configuration.Entrypoint = nil
	}
	configuration.ServiceScope = scope
	configuration.PreflightID = ""
	if configuration.EnvironmentID != "" {
		env, e := c.GetEnvironment(ctx, configuration.EnvironmentID)
		if e != nil {
			return model.DeploymentOperation{}, e
		}
		if env.ApplicationID != app.ID || env.RuntimeID != app.RuntimeID || env.ProjectName != app.ProjectName {
			return model.DeploymentOperation{}, storage.ErrPrecondition
		}
		scope.ProjectID = env.ProjectID
		public, e := c.variables(ctx, env.ProjectID)
		if e != nil {
			return model.DeploymentOperation{}, e
		}
		local, e := c.variables(ctx, env.ID)
		if e != nil {
			return model.DeploymentOperation{}, e
		}
		if public.Revision != configuration.PublicVariablesVersion || local.Revision != configuration.EnvironmentVariablesVersion {
			return model.DeploymentOperation{}, storage.ErrPrecondition
		}
		configuration.EnvironmentRevision = env.Revision
		if runtime.Type == model.RuntimeSwarm {
			if e = c.freezeServiceVersions(ctx, env, scope); e != nil {
				return model.DeploymentOperation{}, e
			}
		}
	}
	rawEnv := source.RuntimeEnv
	input := CreateRevisionInput{ApplicationPreflightInput: ApplicationPreflightInput{RuntimeID: app.RuntimeID, ComposeYAML: candidate,
		Environment: applicationx.EnvironmentInput{Raw: &rawEnv}, ConfigurationSource: configuration}, Actor: actor, IdempotencyKey: idem, ServiceID: service.ID}
	_, operation, _, err := c.CreateApplicationRevision(ctx, app.ID, input, app.Revision)
	return operation, err
}
