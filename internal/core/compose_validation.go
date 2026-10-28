package core

import (
	"context"
	"strings"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/composecheck"
	"github.com/yernsun/network-service-composer/internal/model"
)

type ComposeValidationInput struct {
	Entrypoint          *model.ServiceEntrypoint `json:"entrypoint,omitempty"`
	CatalogAppVersionID string                   `json:"catalog_app_version_id,omitempty"`

	ServiceMode    string `json:"service_mode,omitempty"`
	BaseRevisionID string `json:"base_revision_id,omitempty"`
	RuntimeEnv     string `json:"runtime_env,omitempty"`

	Compose        string             `json:"compose_yaml"`
	NetworkName    string             `json:"network_name,omitempty"`
	DeploymentPlan *composecheck.Plan `json:"deployment_plan,omitempty"`
}

type ComposePreview struct {
	Validation   composecheck.Result `json:"validation"`
	RenderedYAML *string             `json:"rendered_yaml"`
}

func effectiveDeploymentPlan(input *composecheck.Plan, fallback composecheck.Plan) composecheck.Plan {
	if input != nil {
		return *input
	}
	return fallback
}

func (c *Core) analyzeDeploymentCompose(ctx context.Context, rt model.Runtime, raw string, plan composecheck.Plan, includeNodes bool, overrides ...map[string]int) composecheck.Result {
	options := composecheck.Options{Swarm: rt.Type == model.RuntimeSwarm, Plan: plan}
	if len(overrides) > 0 {
		options.ReplicasOverride = overrides[0]
	}
	if options.Swarm && (includeNodes || composecheck.NeedsNodes(raw)) {
		if nodes, err := c.Cluster.PlacementNodes(ctx, rt.ID); err == nil {
			options.Nodes = nodes
			options.ClusterChecked = true
		}
	}
	return composecheck.Analyze(raw, options)
}

func (c *Core) validateDeploymentCompose(ctx context.Context, rt model.Runtime, raw string, plan *composecheck.Plan, fallback composecheck.Plan, overrides ...map[string]int) (composecheck.Result, error) {
	if err := applicationx.ValidateDataNodePolicy(raw, effectiveDeploymentPlan(plan, fallback).DataNodeID, firstReplicaOverrides(overrides)); err != nil {
		return composecheck.Result{}, invalid("validation.application_preflight_failed", "compose_yaml", err.Error())
	}
	if rt.Type == model.RuntimeSwarm && effectiveDeploymentPlan(plan, fallback).DataNodeID != "" {
		names, err := applicationx.ExternalNetworkNames(raw)
		if err != nil {
			return composecheck.Result{}, err
		}
		if err := c.Cluster.ValidateOverlayNetworks(ctx, rt.ID, names); err != nil {
			return composecheck.Result{}, invalid("validation.application_preflight_failed", "networks", err.Error())
		}
	}
	if _, err := c.freezeDockerResources(ctx, rt, raw); err != nil {
		return composecheck.Result{}, invalid("validation.application_preflight_failed", "compose_yaml", err.Error())
	}
	result := c.analyzeDeploymentCompose(ctx, rt, raw, effectiveDeploymentPlan(plan, fallback), false, overrides...)
	if err := result.Error(); err != nil {
		return result, invalid("validation.application_preflight_failed", "compose_yaml", err.Error())
	}
	return result, nil
}

// ValidateEnvironmentCompose is read-only. In particular it does not resolve
// image tags, create a review, enqueue an operation, or change node labels.
func (c *Core) ValidateEnvironmentCompose(ctx context.Context, key string, in ComposeValidationInput) (composecheck.Result, error) {
	result, _, err := c.validateEnvironmentCompose(ctx, key, in, false)
	return result, err
}

// PreviewEnvironmentCompose shares the read-only analysis path, but returns a
// display render built without sending effective secret values to the browser.
func (c *Core) PreviewEnvironmentCompose(ctx context.Context, key string, in ComposeValidationInput) (ComposePreview, error) {
	result, rendered, err := c.validateEnvironmentCompose(ctx, key, in, true)
	return ComposePreview{Validation: result, RenderedYAML: rendered}, err
}

func (c *Core) validateEnvironmentCompose(ctx context.Context, key string, in ComposeValidationInput, preview bool) (composecheck.Result, *string, error) {
	var catalogInput ApplicationPreflightInput
	if in.CatalogAppVersionID != "" {
		if in.Compose != "" || in.ServiceMode != "" {
			return composecheck.Result{}, nil, controlInvalid("catalog_app_version_id", "catalog content cannot be overridden")
		}
		env, e := c.GetEnvironment(ctx, key)
		if e != nil {
			return composecheck.Result{}, nil, e
		}
		catalogInput, e = c.resolveCatalogInput(ctx, ApplicationPreflightInput{CatalogAnalysis: true, RuntimeID: env.RuntimeID, CatalogAppVersionID: in.CatalogAppVersionID, DeploymentPlan: in.DeploymentPlan})
		if e != nil {
			return composecheck.Result{}, nil, e
		}
		in.Compose = catalogInput.ComposeYAML
	}
	originalCompose := in.Compose
	env, input, err := c.EnvironmentPreflightInput(ctx, key, in.Compose)
	if err != nil {
		return composecheck.Result{}, nil, err
	}
	if in.ServiceMode != "" && env.ApplicationID != "" {
		base, e := c.ApplicationSource(ctx, env.ApplicationID, "")
		if e != nil {
			return composecheck.Result{}, nil, e
		}
		delta, _, names, e := applicationx.ServiceDelta(base.ComposeYAML, in.Compose, in.ServiceMode, in.ServiceMode == "update" && in.RuntimeEnv != base.RuntimeEnv)
		if e != nil {
			return composecheck.Result{}, nil, controlInvalid("compose_yaml", e.Error())
		}
		if len(names) > 0 {
			in.Compose = delta
			input.ComposeYAML = delta
		}
	}
	if in.RuntimeEnv != "" {
		values, e := applicationx.ParseDotEnv(in.RuntimeEnv)
		if e != nil {
			return composecheck.Result{}, nil, controlInvalid("runtime_env", "invalid environment file")
		}
		for key, value := range values {
			found := false
			for i := range *input.Environment.Values {
				if (*input.Environment.Values)[i].Key == key {
					(*input.Environment.Values)[i].Value = &value
					found = true
					break
				}
			}
			if !found {
				*input.Environment.Values = append(*input.Environment.Values, applicationx.EnvironmentVariable{Key: key, Value: &value})
			}
		}
	}
	rt, err := c.store.GetRuntime(ctx, env.RuntimeID)
	if err != nil {
		return composecheck.Result{}, nil, err
	}
	if _, ds := composecheck.Parse(in.Compose); len(ds) > 0 {
		return composecheck.Analyze(in.Compose, composecheck.Options{}), nil, nil
	}
	values, err := applicationx.ParseDotEnv(catalogInput.EnvExample)
	if err != nil {
		return composecheck.Result{}, nil, err
	}
	if catalogInput.Manifest != "" {
		manifest, e := applicationx.ParseManifest(catalogInput.Manifest)
		if e != nil {
			return composecheck.Result{}, nil, e
		}
		for _, v := range manifest.Variables {
			if v.Default != "" {
				values[v.Key] = v.Default
			}
		}
	}

	if input.Environment.Values != nil {
		for _, v := range *input.Environment.Values {
			if v.Value != nil {
				values[v.Key] = *v.Value
			}
		}
	}
	var secretKeys []string
	if input.Environment.Values != nil {
		for _, v := range *input.Environment.Values {
			if v.Secret || applicationx.IsCredentialKey(v.Key) {
				secretKeys = append(secretKeys, v.Key)
			}
		}
	}
	resolved, err := applicationx.Interpolate(in.Compose, values)
	if err != nil {
		r := composecheck.Analyze(in.Compose, composecheck.Options{})
		r.Deployable = false
		r.Diagnostics = append(r.Diagnostics, composecheck.Diagnostic{ID: "COMPOSE_ENVIRONMENT_UNRESOLVED", Code: "COMPOSE_ENVIRONMENT_UNRESOLVED", Severity: "error", Path: []string{}, Actions: []string{"LOCATE"}})
		return r, nil, nil
	}
	var rendered *string
	if preview {
		if value, e := applicationx.PreviewResolvedYAML(originalCompose, values, secretKeys); e == nil {
			rendered = &value
		}
	}
	result := c.analyzeDeploymentCompose(ctx, rt, resolved, effectiveDeploymentPlan(in.DeploymentPlan, composecheck.Plan{}), true)
	if !result.SyntaxValid || !result.SchemaValid {
		rendered = nil
	}
	pool, err := c.store.GetPool(ctx, rt.PoolID)
	if err != nil {
		return result, nil, err
	}
	input.NetworkName = in.NetworkName
	if err = prepareApplicationNetwork(&input, rt, env.ProjectName, pool.BackendNetwork); err != nil {
		result.Deployable = false
		result.Diagnostics = append(result.Diagnostics, composecheck.Diagnostic{ID: "NSC_NETWORK_POLICY", Code: "NSC_EXECUTION_POLICY", Severity: "error", Path: []string{"networks"}, Pointer: "/networks", Actions: []string{"LOCATE"}})
	}
	allowedSecrets, allowedConfigs, err := c.allowedDockerResourceNames(ctx, rt)
	if err != nil {
		return composecheck.Result{}, nil, err
	}
	_, err = applicationx.Preflight(applicationx.PreflightInput{ComposeYAML: in.Compose, EnvExample: catalogInput.EnvExample, ManifestYAML: catalogInput.Manifest, Environment: input.Environment, Backend: rt.Type, AllowedSecrets: allowedSecrets, AllowedConfigs: allowedConfigs, ProtectedNetworks: []string{pool.BackendNetwork}, DataNodeID: effectiveDeploymentPlan(in.DeploymentPlan, composecheck.Plan{}).DataNodeID, RuntimeNodeID: applicationx.RuntimeNodeID(rt.Endpoint)})
	if err == nil {
		err = applicationx.ValidateDataNodePolicy(resolved, effectiveDeploymentPlan(in.DeploymentPlan, composecheck.Plan{}).DataNodeID, nil)
	}
	if err != nil && result.SyntaxValid && result.SchemaValid {
		result.Deployable = false
		result.Diagnostics = append(result.Diagnostics, composecheck.Diagnostic{ID: "NSC_EXECUTION_POLICY", Code: "NSC_EXECUTION_POLICY", Severity: "error", Path: []string{}, Actions: []string{"LOCATE"}})
	} else if err == nil {
		if _, resourceErr := c.freezeDockerResources(ctx, rt, resolved); resourceErr != nil {
			result.Deployable = false
			result.Diagnostics = append(result.Diagnostics, composecheck.Diagnostic{ID: "NSC_DOCKER_RESOURCE_UNAVAILABLE", Code: "NSC_EXECUTION_POLICY", Severity: "error", Path: []string{}, Actions: []string{"LOCATE"}})
		}
	}
	redactDeploymentValidation(&result, values, secretKeys)
	return result, rendered, nil
}

func redactDeploymentValidation(result *composecheck.Result, values map[string]string, keys []string) {
	redact := func(s string) string {
		for _, key := range keys {
			if values[key] != "" {
				s = strings.ReplaceAll(s, values[key], "[redacted]")
			}
		}
		return s
	}
	for i := range result.Diagnostics {
		d := &result.Diagnostics[i]
		d.ID, d.Pointer, d.Service, d.Resource, d.Reason = redact(d.ID), redact(d.Pointer), redact(d.Service), redact(d.Resource), redact(d.Reason)
		for j := range d.Path {
			d.Path[j] = redact(d.Path[j])
		}
	}
	for i := range result.Services {
		result.Services[i].Name = redact(result.Services[i].Name)
		for j := range result.Services[i].Mounts {
			m := &result.Services[i].Mounts[j]
			m.Key = redact(m.Key)
			m.Source = redact(m.Source)
			m.Target = redact(m.Target)
		}
		for j, c := range result.Services[i].Constraints {
			result.Services[i].Constraints[j] = redact(c)
		}
		for j := range result.Services[i].MatchedNodes {
			redactComposeNode(&result.Services[i].MatchedNodes[j], redact)
		}
	}
	for i := range result.Nodes {
		redactComposeNode(&result.Nodes[i], redact)
	}
}

func redactComposeNode(node *composecheck.Node, redact func(string) string) {
	node.ID, node.Name, node.Role, node.State = redact(node.ID), redact(node.Name), redact(node.Role), redact(node.State)
	node.Availability, node.OS, node.Arch, node.IP = redact(node.Availability), redact(node.OS), redact(node.Arch), redact(node.IP)
	if node.Labels != nil {
		labels := make(map[string]string, len(node.Labels))
		for key, value := range node.Labels {
			labels[redact(key)] = redact(value)
		}
		node.Labels = labels
	}
	if node.EngineLabels != nil {
		labels := make(map[string]string, len(node.EngineLabels))
		for key, value := range node.EngineLabels {
			labels[redact(key)] = redact(value)
		}
		node.EngineLabels = labels
	}
}

func firstReplicaOverrides(values []map[string]int) map[string]int {
	if len(values) > 0 {
		return values[0]
	}
	return nil
}
