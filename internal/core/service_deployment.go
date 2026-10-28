package core

import (
	"context"
	"encoding/json"
	"fmt"
	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"slices"
	"time"
)

// Reviews contain the original candidate and server-selected service scope.
// Callers cannot supply an execution scope directly to a deployment operation.
func (c *Core) validateUnscopedSwarmRevision(ctx context.Context, app model.Application, runtime model.Runtime, source model.DeploymentSource, candidate string) error {
	if runtime.Type != model.RuntimeSwarm || source.ServiceScope != nil || app.ActiveRevisionID == "" {
		return nil
	}
	active, err := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
	if err != nil {
		return err
	}
	if active.ConfigurationSource.ServiceScope != nil {
		return controlInvalid("preflight_id", "review service changes before updating this Stack")
	}
	raw, _, err := c.keyring.Decrypt(active.EncryptedResolved, revisionAAD(active.ID, "resolved"))
	if err != nil {
		return err
	}
	_, changes, _, err := applicationx.ServiceDelta(string(raw), candidate, "update")
	if err != nil {
		return err
	}
	for _, change := range changes {
		if change.Kind == "retained" {
			return controlInvalid("compose_yaml", "partial Stack updates require a service review; omitted services are retained, never deleted")
		}
	}
	return nil
}

func (c *Core) reviewEnvironmentServices(ctx context.Context, key string, in ComposeValidationInput) (map[string]any, error) {
	return c.reviewEnvironmentServicesScoped(ctx, key, in, nil, false)
}

func (c *Core) reviewEnvironmentServicesScoped(ctx context.Context, key string, in ComposeValidationInput, scopes []string, restrictHostAccess bool) (map[string]any, error) {
	if in.ServiceMode != "create" && in.ServiceMode != "update" {
		return nil, controlInvalid("service_mode", "expected create or update")
	}
	env, input, err := c.EnvironmentPreflightInput(ctx, key, in.Compose)
	if err != nil {
		return nil, err
	}
	runtime, err := c.requireManagement(ctx, env.RuntimeID)
	if err != nil {
		return nil, err
	}
	if runtime.Type != model.RuntimeSwarm {
		return nil, controlInvalid("service_mode", "incremental service deployment requires Docker Swarm")
	}
	var app model.Application
	var base ApplicationSource
	if env.ApplicationID != "" {
		app, err = c.store.GetApplication(ctx, env.ApplicationID)
		if err != nil {
			return nil, err
		}
		if !app.Managed || app.System {
			return nil, storage.ErrSystemProtected
		}
		if c.serviceRevisionInFlight(ctx, app) {
			return nil, storage.ErrPrecondition
		}
		if in.BaseRevisionID != "" && app.ActiveRevisionID != in.BaseRevisionID {
			return nil, storage.ErrPrecondition
		}
		if app.ActiveRevisionID != "" {
			base, err = c.ApplicationSource(ctx, app.ID, app.ActiveRevisionID)
			if err != nil {
				return nil, err
			}
		}
	} else if in.ServiceMode == "update" {
		return nil, storage.ErrNotFound
	}
	delta, changes, names, err := applicationx.ServiceDelta(base.ComposeYAML, in.Compose, in.ServiceMode, in.ServiceMode == "update" && in.RuntimeEnv != base.RuntimeEnv)
	if err != nil {
		return nil, controlInvalid("compose_yaml", err.Error())
	}
	if len(names) == 0 {
		return nil, controlInvalid("compose_yaml", "no service configuration changes")
	}
	required := []string{}
	if scopes != nil {
		for _, change := range changes {
			switch change.Kind {
			case "added":
				if !slices.Contains(required, "services.create") {
					required = append(required, "services.create")
				}
			case "updated":
				if !slices.Contains(required, "services.edit") {
					required = append(required, "services.edit")
				}
			}
		}
		if err := requireReviewScopes(scopes, required); err != nil {
			return nil, err
		}
	}
	// Unknown Docker objects may exist before inventory is refreshed. The Worker
	// repeats the collision check against the manager immediately before deploy.
	observed, err := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{RuntimeID: runtime.ID})
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		for _, s := range observed {
			if s.ServiceKey != env.ProjectName+"_"+name || s.Status == model.ServiceRemoved {
				continue
			}
			if in.ServiceMode == "create" || s.ApplicationID != app.ID {
				return nil, controlInvalid("compose_yaml", fmt.Sprintf("service %q already exists or has different ownership", name))
			}
		}
	}
	if err = c.validateServiceEntrypoint(ctx, env, runtime, in.Entrypoint, names); err != nil {
		return nil, err
	}
	input.ConfigurationSource.Entrypoint = in.Entrypoint
	input.ConfigurationSource.RestrictedHostAccess = restrictHostAccess
	input.ComposeYAML = delta
	input.DeploymentPlan = in.DeploymentPlan
	input.ConfigurationSource.DeploymentPlan = effectiveDeploymentPlan(in.DeploymentPlan, input.ConfigurationSource.DeploymentPlan)
	input.ConfigurationSource.ServiceScope = &model.ServiceDeploymentScope{Mode: in.ServiceMode, Names: names, BaseRevisionID: app.ActiveRevisionID, ProjectID: env.ProjectID}
	overrides, overrideErr := applicationx.UpdatedReplicaOverrides(base.ComposeYAML, delta, app.ReplicaOverrides)
	if overrideErr != nil {
		return nil, overrideErr
	}
	input.ConfigurationSource.ServiceScope.ReplicaOverrides = overrides
	if err = c.freezeServiceVersions(ctx, env, input.ConfigurationSource.ServiceScope); err != nil {
		return nil, err
	}
	if !applicationx.ReplicaOverridesMatch(names, app.ReplicaOverrides, input.ConfigurationSource.ServiceScope.ObservedReplicaOverrides) {
		return nil, storage.ErrPrecondition
	}
	input.NetworkName = in.NetworkName
	if input.NetworkName == "" && in.ServiceMode == "create" {
		p, e := c.GetProject(ctx, env.ProjectID)
		if e != nil {
			return nil, e
		}
		input.NetworkName = p.Slug
	}
	if in.RuntimeEnv != "" {
		values, e := applicationx.ParseDotEnv(in.RuntimeEnv)
		if e != nil {
			return nil, controlInvalid("runtime_env", "invalid environment file")
		}
		for k, v := range values {
			found := false
			for i, entry := range *input.Environment.Values {
				if entry.Key == k {
					(*input.Environment.Values)[i].Value = &v
					found = true
					break
				}
			}
			if !found {
				*input.Environment.Values = append(*input.Environment.Values, applicationx.EnvironmentVariable{Key: k, Value: &v})
			}
		}
	}
	if err = c.lockEnvironmentImages(ctx, env, &input); err != nil {
		return nil, err
	}
	var preview ApplicationPreflight
	if app.ID != "" {
		p, e := c.PreflightApplicationRevision(ctx, app.ID, input)
		if e != nil {
			return nil, e
		}
		preview = p.ApplicationPreflight
	} else {
		preview, err = c.PreflightApplication(ctx, input)
		if err != nil {
			return nil, err
		}
	}
	input.NetworkName = preview.NetworkName
	input.ConfigurationSource.NetworkName = preview.NetworkName
	// Render the selected execution source after pinning and network expansion.
	// Public preflight redaction cannot be used as executable input.
	frozen, err := c.serviceExecutionPreview(ctx, input, app.ActiveRevisionID)
	if err != nil {
		return nil, err
	}
	reviewID := id.New("envreview")
	input.ConfigurationSource.PreflightID = reviewID
	value := environmentReview{EnvironmentRevision: env.Revision, Input: input, Source: input.ConfigurationSource, ExpiresAt: c.now().Add(15 * time.Minute), ScopeChecked: scopes != nil, RequiredScopes: required}
	write, err := c.encryptedWrite(reviewID, "environment-review", key, value, 0)
	if err != nil {
		return nil, err
	}
	if err = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{write}); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(preview)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	result["preflight_id"] = reviewID
	result["configuration_source"] = input.ConfigurationSource
	result["service_changes"] = changes
	result["base_revision_id"] = app.ActiveRevisionID
	result["resolved_yaml"] = frozen[1]
	result["before_resolved_yaml"] = frozen[0]
	delete(result, "environment")
	// The raw candidate remains behind the sensitive source-read permission.
	delete(result, "compose_yaml")
	return result, nil
}

func (c *Core) serviceExecutionPreview(ctx context.Context, input ApplicationPreflightInput, base string) ([2]string, error) {
	var output [2]string
	runtime, err := c.store.GetRuntime(ctx, input.RuntimeID)
	if err != nil {
		return output, err
	}
	pool, err := c.store.GetPool(ctx, runtime.PoolID)
	if err != nil {
		return output, err
	}
	allowedSecrets, allowedConfigs, err := c.allowedDockerResourceNames(ctx, runtime)
	if err != nil {
		return output, err
	}
	result, err := applicationx.Preflight(applicationx.PreflightInput{ComposeYAML: input.ComposeYAML, Environment: input.Environment, Backend: runtime.Type, AllowedSecrets: allowedSecrets, AllowedConfigs: allowedConfigs, ProtectedNetworks: []string{pool.BackendNetwork}, DataNodeID: effectiveDeploymentPlan(input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan).DataNodeID, RuntimeNodeID: applicationx.RuntimeNodeID(runtime.Endpoint)})
	if err != nil {
		return output, err
	}
	if err = pinPreflight(&result, input.ConfigurationSource); err != nil {
		return output, err
	}
	source := input.ConfigurationSource
	if err = c.mergeServiceRevision(ctx, &source, &result, "review"); err != nil {
		return output, err
	}
	selected, err := applicationx.SelectServices(result.ResolvedYAML, source.ServiceScope.Names)
	if err != nil {
		return output, err
	}
	selected, err = applicationx.WithReplicaOverrides(selected, source.ServiceScope.ReplicaOverrides)
	if err != nil {
		return output, err
	}
	secretValues := make([]string, 0, len(result.SecretKeys))
	for _, key := range result.SecretKeys {
		secretValues = append(secretValues, result.Environment[key])
	}
	output[1] = applicationx.RedactComposeDisplay(selected, secretValues...)
	if base != "" {
		revision, err := c.store.GetApplicationRevision(ctx, base)
		if err != nil {
			return output, err
		}
		raw, _, err := c.keyring.Decrypt(revision.EncryptedResolved, revisionAAD(base, "resolved"))
		if err != nil {
			return output, err
		}
		before := string(raw)
		app, readErr := c.store.GetApplication(ctx, revision.ApplicationID)
		if readErr != nil {
			return output, readErr
		}
		before, err = applicationx.WithReplicaOverrides(before, app.ReplicaOverrides)
		if err != nil {
			return output, err
		}
		if revision.ConfigurationSource.ServiceScope == nil {
			before, err = applicationx.DeploymentNetworks(before, revision.ConfigurationSource.NetworkName)
			if err != nil {
				return output, err
			}
			before, err = applicationx.NamespaceApplicationNetwork(before, revision.ConfigurationSource.NetworkName)
			if err != nil {
				return output, err
			}
		}
		_, changes, _, err := applicationx.ServiceDelta(before, selected, "update")
		if err != nil {
			return output, err
		}
		names := []string{}
		for _, change := range changes {
			if change.Kind == "updated" || change.Kind == "unchanged" {
				names = append(names, change.Name)
			}
		}
		if len(names) > 0 {
			before, err = applicationx.SelectServices(before, names)
			if err != nil {
				return output, err
			}
			envRaw, _, decryptErr := c.keyring.Decrypt(revision.EncryptedEnvironment, revisionAAD(base, "environment"))
			if decryptErr != nil {
				return output, decryptErr
			}
			values, parseErr := applicationx.ParseDotEnv(string(envRaw))
			if parseErr != nil {
				return output, parseErr
			}
			beforeSecrets := make([]string, 0, len(revision.SecretKeys))
			for _, key := range revision.SecretKeys {
				beforeSecrets = append(beforeSecrets, values[key])
			}
			output[0] = applicationx.RedactComposeDisplay(before, beforeSecrets...)
		}
	}
	return output, nil
}

func (c *Core) validateServiceReviewBase(ctx context.Context, env Environment, source model.DeploymentSource) error {
	if source.ServiceScope == nil {
		return nil
	}
	if env.ApplicationID == "" {
		if source.ServiceScope.BaseRevisionID != "" {
			return storage.ErrPrecondition
		}
	} else {
		a, err := c.store.GetApplication(ctx, env.ApplicationID)
		if err != nil {
			return err
		}
		if a.ActiveRevisionID != source.ServiceScope.BaseRevisionID || c.serviceRevisionInFlight(ctx, a) || !applicationx.ReplicaOverridesMatch(source.ServiceScope.Names, a.ReplicaOverrides, source.ServiceScope.ObservedReplicaOverrides) {
			return storage.ErrPrecondition
		}
	}
	common, err := c.variables(ctx, env.ProjectID)
	if err != nil {
		return err
	}
	local, err := c.variables(ctx, env.ID)
	if err != nil {
		return err
	}
	if common.Revision != source.PublicVariablesVersion || local.Revision != source.EnvironmentVariablesVersion {
		return storage.ErrPrecondition
	}
	current, err := c.Cluster.ServiceVersions(ctx, env.RuntimeID)
	if err != nil {
		return controlInvalid("runtime_id", "cannot verify current Docker service versions")
	}
	for _, name := range source.ServiceScope.Names {
		if current[env.ProjectName+"_"+name] != source.ServiceScope.Observed[name] {
			return storage.ErrPrecondition
		}
	}
	return nil
}

func (c *Core) serviceRevisionInFlight(ctx context.Context, app model.Application) bool {
	if app.DesiredRevisionID == "" || app.DesiredRevisionID == app.ActiveRevisionID {
		return false
	}
	operations, err := c.store.ListDeploymentOperations(ctx, app.ID, storage.Page{Limit: 200})
	if err != nil {
		return true
	}
	for _, op := range operations {
		if op.RevisionID == app.DesiredRevisionID {
			return op.Status == model.OperationQueued || op.Status == model.OperationRunning
		}
	}
	return true
}

func (c *Core) freezeServiceVersions(ctx context.Context, env Environment, scope *model.ServiceDeploymentScope) error {
	observed, err := c.Cluster.ServiceVersions(ctx, env.RuntimeID)
	if err != nil {
		return controlInvalid("runtime_id", "cannot verify current Docker services")
	}
	scope.ObservedReplicaOverrides = map[string]int{}
	if env.ApplicationID != "" {
		app, readErr := c.store.GetApplication(ctx, env.ApplicationID)
		if readErr != nil {
			return readErr
		}
		for _, name := range scope.Names {
			if count, exists := app.ReplicaOverrides[name]; exists {
				scope.ObservedReplicaOverrides[name] = count
			}
		}
	}
	scope.Observed = map[string]model.ServiceVersion{}
	for _, name := range scope.Names {
		state, exists := observed[env.ProjectName+"_"+name]
		if exists && (scope.Mode == "create" || state.Stack != env.ProjectName) {
			return controlInvalid("compose_yaml", fmt.Sprintf("service %q already exists or belongs to another Stack", name))
		}
		scope.Observed[name] = state
	}
	return nil
}

// Preserve an immutable aggregate revision for whole-stack reads and recovery,
// but record the service scope separately for the incremental execution path.
func (c *Core) mergeServiceRevision(ctx context.Context, source *model.DeploymentSource, result *applicationx.PreflightResult, revisionID string) error {
	scope := source.ServiceScope
	if scope == nil {
		return nil
	}
	if scope.Mode == "delete" {
		// The reviewed candidate already contains every retained service.
		// Merging its source with the base would resurrect the deleted target.
		return nil
	}
	heads := map[string]string{}
	if scope.BaseRevisionID == "" {
		var err error
		result.ResolvedYAML, err = applicationx.DeploymentNetworks(result.ResolvedYAML, source.NetworkName)
		if err != nil {
			return err
		}
		result.ResolvedYAML, err = applicationx.NamespaceApplicationNetwork(result.ResolvedYAML, source.NetworkName)
		if err != nil {
			return err
		}
	}
	if scope.BaseRevisionID != "" {
		base, err := c.store.GetApplicationRevision(ctx, scope.BaseRevisionID)
		if err != nil {
			return err
		}
		compose, _, err := c.keyring.Decrypt(base.EncryptedCompose, revisionAAD(base.ID, "compose"))
		if err != nil {
			return err
		}
		resolved, _, err := c.keyring.Decrypt(base.EncryptedResolved, revisionAAD(base.ID, "resolved"))
		if err != nil {
			return err
		}
		// Freeze the actual network attachment in the effective resolved source.
		previous := string(resolved)
		if base.ConfigurationSource.ServiceScope == nil {
			previous, err = applicationx.DeploymentNetworks(previous, base.ConfigurationSource.NetworkName)
			if err != nil {
				return err
			}
			previous, err = applicationx.NamespaceApplicationNetwork(previous, base.ConfigurationSource.NetworkName)
			if err != nil {
				return err
			}
		}
		candidate, err := applicationx.InheritImplicitDefaultNetwork(previous, result.ResolvedYAML)
		if err != nil {
			return err
		}
		next, err := applicationx.DeploymentNetworks(candidate, source.NetworkName)
		if err != nil {
			return err
		}
		// DeploymentNetworks uses a reserved key; distinct per-service networks must
		// not overwrite the definition used by retained services.
		if source.NetworkName == "" {
			next, err = applicationx.PreserveManagedNetworks(previous, next)
			if err != nil {
				return err
			}
		}
		next, err = applicationx.NamespaceApplicationNetwork(next, source.NetworkName)
		if err != nil {
			return err
		}
		result.ComposeYAML, err = applicationx.MergeServices(string(compose), result.ComposeYAML)
		if err != nil {
			return err
		}
		result.ResolvedYAML, err = applicationx.MergeServices(previous, next)
		if err != nil {
			return err
		}
		envRaw, _, err := c.keyring.Decrypt(base.EncryptedEnvironment, revisionAAD(base.ID, "environment"))
		if err != nil {
			return err
		}
		previousEnv, err := applicationx.ParseDotEnv(string(envRaw))
		if err != nil {
			return err
		}
		for key, value := range previousEnv {
			if _, ok := result.Environment[key]; !ok {
				result.Environment[key] = value
				result.EnvironmentKeys = append(result.EnvironmentKeys, key)
			}
		}
		for _, key := range base.SecretKeys {
			found := false
			for _, existing := range result.SecretKeys {
				if existing == key {
					found = true
				}
			}
			if !found {
				result.SecretKeys = append(result.SecretKeys, key)
			}
		}
		for _, declaration := range base.ConfigurationSource.DeploymentPlan.Storage {
			found := false
			for _, value := range source.DeploymentPlan.Storage {
				if value.Resource == declaration.Resource {
					found = true
				}
			}
			if !found {
				source.DeploymentPlan.Storage = append(source.DeploymentPlan.Storage, declaration)
			}
		}
		for name, head := range base.ConfigurationSource.ServiceRevisions {
			heads[name] = head
		}
		for _, pair := range []struct {
			current  *map[string]string
			previous map[string]string
		}{{&source.Images, base.ConfigurationSource.Images}, {&source.TaggedImages, base.ConfigurationSource.TaggedImages}} {
			merged := map[string]string{}
			for key, value := range pair.previous {
				merged[key] = value
			}
			for key, value := range *pair.current {
				merged[key] = value
			}
			*pair.current = merged
		}
		if len(heads) == 0 {
			_, changes, _, e := applicationx.ServiceDelta("", string(compose), "update")
			if e != nil {
				return e
			}
			for _, change := range changes {
				heads[change.Name] = base.ID
			}
		}
	}
	for _, name := range scope.Names {
		heads[name] = revisionID
	}
	source.ServiceRevisions = heads
	result.EnvironmentSHA256 = digestString(applicationx.RenderDotEnv(result.Environment))
	result.ComposeSHA256 = digestString(result.ComposeYAML)
	result.ResolvedSHA256 = digestString(result.ResolvedYAML)
	return nil
}
