package core

import (
	"context"
	"fmt"
	"strings"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// queuePersistentServiceScale publishes an immutable revision for only the
// selected service. The old override is removed when that revision activates.
func (c *Core) queuePersistentServiceScale(ctx context.Context, service model.Service, app model.Application, replicas int, key, actor string) (model.DeploymentOperation, error) {
	if app.ActiveRevisionID == "" || c.serviceRevisionInFlight(ctx, app) {
		return model.DeploymentOperation{}, fmt.Errorf("persistent scale requires a stable active revision: %w", storage.ErrPrecondition)
	}
	runtime, err := c.store.GetRuntime(ctx, app.RuntimeID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	name := strings.TrimPrefix(service.ServiceKey, app.ProjectName+"/")
	if runtime.Type == model.RuntimeSwarm {
		name = strings.TrimPrefix(service.ServiceKey, app.ProjectName+"_")
	}
	if name == service.ServiceKey || name == "" {
		return model.DeploymentOperation{}, storage.ErrPrecondition
	}
	base, err := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	source, err := c.ApplicationSource(ctx, app.ID, app.ActiveRevisionID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	updated, err := applicationx.SetServiceReplicas(source.ComposeYAML, name, replicas)
	if err != nil {
		return model.DeploymentOperation{}, invalid("validation.application_preflight_failed", "replicas", err.Error())
	}
	selected, err := applicationx.SelectServices(updated, []string{name})
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	overrides := make(map[string]int, len(app.ReplicaOverrides))
	for serviceName, count := range app.ReplicaOverrides {
		if serviceName != name {
			overrides[serviceName] = count
		}
	}
	scope := &model.ServiceDeploymentScope{Mode: "update", Names: []string{name}, BaseRevisionID: app.ActiveRevisionID,
		ReplicaOverrides: overrides, RequestedReplicas: &replicas, ObservedReplicaOverrides: map[string]int{}, Observed: map[string]model.ServiceVersion{}}
	if app.Environment != nil {
		scope.ProjectID = app.Environment.ProjectID
	} else if base.ConfigurationSource.ServiceScope != nil {
		scope.ProjectID = base.ConfigurationSource.ServiceScope.ProjectID
	}
	if count, exists := app.ReplicaOverrides[name]; exists {
		scope.ObservedReplicaOverrides[name] = count
	}
	if runtime.Type == model.RuntimeSwarm {
		versions, readErr := c.Cluster.ServiceVersions(ctx, runtime.ID)
		if readErr != nil {
			return model.DeploymentOperation{}, readErr
		}
		version := versions[app.ProjectName+"_"+name]
		if version.ID != "" && version.Stack != app.ProjectName {
			return model.DeploymentOperation{}, storage.ErrConflict
		}
		scope.Observed[name] = version
	}
	configuration := base.ConfigurationSource
	configuration.RestoredFrom = ""
	configuration.ServiceScope = scope
	configuration.PreflightID = ""
	// This revision submits only the selected service. Image pins for siblings
	// belong to the active base and are merged back after the partial review.
	selectedImage := func(all map[string]string) map[string]string {
		if image, ok := all[name]; ok {
			return map[string]string{name: image}
		}
		return nil
	}
	configuration.Images = selectedImage(configuration.Images)
	configuration.TaggedImages = selectedImage(configuration.TaggedImages)
	if configuration.EnvironmentID != "" {
		env, readErr := c.GetEnvironment(ctx, configuration.EnvironmentID)
		if readErr != nil {
			return model.DeploymentOperation{}, readErr
		}
		if env.ApplicationID != app.ID || env.RuntimeID != app.RuntimeID || env.ProjectName != app.ProjectName {
			return model.DeploymentOperation{}, storage.ErrPrecondition
		}
		publicVariables, readErr := c.variables(ctx, env.ProjectID)
		if readErr != nil {
			return model.DeploymentOperation{}, readErr
		}
		environmentVariables, readErr := c.variables(ctx, env.ID)
		if readErr != nil {
			return model.DeploymentOperation{}, readErr
		}
		// The binding transaction advances the environment revision on every
		// deployment. Refresh only that lease; changing variable layers needs
		// its own review and must not silently enter a replica-only revision.
		if publicVariables.Revision != configuration.PublicVariablesVersion || environmentVariables.Revision != configuration.EnvironmentVariablesVersion {
			return model.DeploymentOperation{}, storage.ErrPrecondition
		}
		configuration.EnvironmentRevision = env.Revision
	}
	rawEnv := source.RuntimeEnv
	input := CreateRevisionInput{ApplicationPreflightInput: ApplicationPreflightInput{
		RuntimeID: app.RuntimeID, ComposeYAML: selected,
		Environment: applicationx.EnvironmentInput{Raw: &rawEnv}, ConfigurationSource: configuration,
	}, IdempotencyKey: key, Actor: actor, ServiceID: service.ID}
	if app.Status == model.ApplicationStopped || app.Status == model.ApplicationDown {
		input.DeferredStatus = app.Status
		scope.DeferredUntilStart = true
	} else if app.Status != model.ApplicationRunning {
		return model.DeploymentOperation{}, storage.ErrPrecondition
	}
	_, operation, _, err := c.CreateApplicationRevision(ctx, app.ID, input, app.Revision)
	if err != nil {
		return operation, fmt.Errorf("create persistent service scale revision: %w", err)
	}
	return operation, nil
}
