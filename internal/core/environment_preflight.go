package core

import (
	"context"
	"encoding/json"
	"errors"
	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"slices"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type environmentReview struct {
	EnvironmentRevision int64                     `json:"environment_revision"`
	Input               ApplicationPreflightInput `json:"input"`
	Source              model.DeploymentSource    `json:"source"`
	ExpiresAt           time.Time                 `json:"expires_at"`
	ScopeChecked        bool                      `json:"scope_checked,omitempty"`
	RequiredScopes      []string                  `json:"required_scopes,omitempty"`
}

// The review stores an encrypted input, not a browser-controlled digest map.
func (c *Core) ReviewEnvironment(ctx context.Context, key, compose string, networkName ...string) (map[string]any, error) {
	in := ComposeValidationInput{Compose: compose}
	if len(networkName) > 0 {
		in.NetworkName = networkName[0]
	}
	return c.ReviewEnvironmentInput(ctx, key, in)
}

func (c *Core) ReviewEnvironmentInput(ctx context.Context, key string, in ComposeValidationInput, restrictHostAccess ...bool) (map[string]any, error) {
	return c.reviewEnvironmentInput(ctx, key, in, nil, len(restrictHostAccess) > 0 && restrictHostAccess[0])
}

// ReviewEnvironmentInputScoped freezes the server-derived mutation permissions
// alongside the candidate. A later deployment must recheck this exact claim.
func (c *Core) ReviewEnvironmentInputScoped(ctx context.Context, key string, in ComposeValidationInput, scopes []string) (map[string]any, error) {
	if scopes == nil {
		scopes = []string{}
	}
	return c.reviewEnvironmentInput(ctx, key, in, scopes, true)
}

func (c *Core) reviewEnvironmentInput(ctx context.Context, key string, in ComposeValidationInput, scopes []string, restrictHostAccess bool) (map[string]any, error) {
	var catalogInput ApplicationPreflightInput
	if in.CatalogAppVersionID != "" {
		if in.Compose != "" || in.ServiceMode != "" {
			return nil, controlInvalid("catalog_app_version_id", "catalog content cannot be overridden in the same review")
		}
		env, e := c.GetEnvironment(ctx, key)
		if e != nil {
			return nil, e
		}
		catalogInput, e = c.resolveCatalogInput(ctx, ApplicationPreflightInput{RuntimeID: env.RuntimeID, CatalogAppVersionID: in.CatalogAppVersionID, DeploymentPlan: in.DeploymentPlan})
		if e != nil {
			return nil, e
		}
		in.Compose = catalogInput.ComposeYAML
	}

	if in.ServiceMode != "" {
		return c.reviewEnvironmentServicesScoped(ctx, key, in, scopes, restrictHostAccess)
	}
	compose := in.Compose
	v, input, e := c.EnvironmentPreflightInput(ctx, key, compose)
	if e != nil {
		return nil, e
	}
	candidateCompose := input.ComposeYAML
	input.ConfigurationSource.RestrictedHostAccess = restrictHostAccess
	if in.BaseRevisionID != "" && v.ApplicationID != "" {
		app, err := c.store.GetApplication(ctx, v.ApplicationID)
		if err != nil {
			return nil, err
		}
		if app.ActiveRevisionID != in.BaseRevisionID {
			return nil, storage.ErrPrecondition
		}
	}
	if in.RuntimeEnv != "" {
		values, err := applicationx.ParseDotEnv(in.RuntimeEnv)
		if err != nil {
			return nil, controlInvalid("runtime_env", "invalid environment file")
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
	input.NetworkName = in.NetworkName
	input.DeploymentPlan = in.DeploymentPlan
	input.ConfigurationSource.DeploymentPlan = effectiveDeploymentPlan(in.DeploymentPlan, input.ConfigurationSource.DeploymentPlan)
	if e = c.lockEnvironmentImages(ctx, v, &input); e != nil {
		return nil, e
	}
	if in.CatalogAppVersionID != "" {
		input.CatalogAppVersionID = in.CatalogAppVersionID
		input.ComposeYAML = ""
	}
	var preview ApplicationPreflight
	var revisionPreview *ApplicationRevisionPreview
	if v.ApplicationID != "" {
		var value ApplicationRevisionPreview
		value, e = c.PreflightApplicationRevision(ctx, v.ApplicationID, input)
		preview, revisionPreview = value.ApplicationPreflight, &value
	} else {
		preview, e = c.PreflightApplication(ctx, input)
	}
	if e != nil {
		return nil, e
	}
	required := []string{}
	if scopes != nil {
		if v.ApplicationID == "" || revisionPreview == nil || revisionPreview.Diff.FromRevisionID == "" {
			required = append(required, "services.create")
		} else {
			if len(revisionPreview.Diff.ServicesRemoved) > 0 {
				return nil, controlInvalid("compose_yaml", "project API keys cannot remove existing services")
			}
			base, err := c.ApplicationSource(ctx, v.ApplicationID, revisionPreview.Diff.FromRevisionID)
			if err != nil {
				return nil, err
			}
			_, changes, _, err := applicationx.ServiceDelta(base.ComposeYAML, candidateCompose, "update")
			if err != nil {
				return nil, controlInvalid("compose_yaml", err.Error())
			}
			for _, change := range changes {
				switch change.Kind {
				case "retained":
					return nil, controlInvalid("compose_yaml", "project API keys cannot remove existing services")
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
			if len(revisionPreview.Diff.EnvironmentAdded)+len(revisionPreview.Diff.EnvironmentRemoved)+len(revisionPreview.Diff.EnvironmentChanged) > 0 || revisionPreview.Diff.NetworksChanged || revisionPreview.Diff.VolumesChanged || revisionPreview.Diff.DeployPolicyChanged {
				if !slices.Contains(required, "services.edit") {
					required = append(required, "services.edit")
				}
			}
		}
		if err := requireReviewScopes(scopes, required); err != nil {
			return nil, err
		}
	}
	// Freeze the effective network in the review so a later revision cannot
	// silently change the network approved for this deployment.
	input.NetworkName = preview.NetworkName
	input.ConfigurationSource.NetworkName = preview.NetworkName
	reviewID := id.New("envreview")
	input.ConfigurationSource.PreflightID = reviewID
	value := environmentReview{EnvironmentRevision: v.Revision, Input: input, Source: input.ConfigurationSource, ExpiresAt: c.now().Add(15 * time.Minute), ScopeChecked: scopes != nil, RequiredScopes: required}
	w, e := c.encryptedWrite(reviewID, "environment-review", key, value, 0)
	if e != nil {
		return nil, e
	}
	if e = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{w}); e != nil {
		return nil, e
	}
	var publicPreview any = preview
	if revisionPreview != nil {
		publicPreview = revisionPreview
	}
	raw, e := json.Marshal(publicPreview)
	if e != nil {
		return nil, e
	}
	result := map[string]any{}
	if e = json.Unmarshal(raw, &result); e != nil {
		return nil, e
	}
	if resolved, ok := result["resolved_yaml"].(string); ok {
		issues, err := c.environmentIsolation(ctx, v, resolved)
		if err != nil {
			return nil, err
		}
		result["isolation_resources"] = issues
	}
	if rendered, ok := result["resolved_yaml"].(string); ok {
		result["resolved_yaml"] = applicationx.RedactComposeDisplay(rendered)
	}
	result["preflight_id"] = reviewID
	result["configuration_source"] = input.ConfigurationSource
	return result, nil
}

func requireReviewScopes(granted, required []string) error {
	for _, scope := range required {
		if !slices.Contains(granted, scope) {
			return storage.ErrNotFound
		}
	}
	return nil
}

func (c *Core) DeployReviewedEnvironmentScoped(ctx context.Context, environmentID, reviewID, actor, idem string, expected int64, scopes []string) (model.DeploymentOperation, error) {
	d, err := c.store.GetControlDocument(ctx, reviewID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	if d.Kind != "environment-review" || d.ParentID != environmentID {
		return model.DeploymentOperation{}, storage.ErrNotFound
	}
	var review environmentReview
	if _, err = c.readEncrypted(ctx, reviewID, "environment-review", &review); err != nil {
		return model.DeploymentOperation{}, err
	}
	if !review.ScopeChecked || !review.Source.RestrictedHostAccess || !slices.Contains(scopes, "services.deploy") || requireReviewScopes(scopes, review.RequiredScopes) != nil {
		return model.DeploymentOperation{}, storage.ErrNotFound
	}
	return c.DeployReviewedEnvironment(ctx, environmentID, reviewID, actor, idem, expected)
}

func (c *Core) DeployReviewedEnvironment(ctx context.Context, key, reviewID, actor, idem string, expected int64, restrictHostAccess ...bool) (model.DeploymentOperation, error) {
	v, e := c.GetEnvironment(ctx, key)
	if e != nil {
		return model.DeploymentOperation{}, e
	}
	if _, e = c.requireManagement(ctx, v.RuntimeID); e != nil {
		return model.DeploymentOperation{}, e
	}
	if reviewID == "" || idem == "" {
		return model.DeploymentOperation{}, controlInvalid("preflight_id", "review the configuration before deployment")
	}
	if op, e := c.store.GetDeploymentOperationByIdempotency(ctx, v.RuntimeID, idem); e == nil {
		revision, e := c.store.GetApplicationRevision(ctx, op.RevisionID)
		if e != nil {
			return op, e
		}
		if revision.ConfigurationSource.PreflightID != reviewID || revision.ConfigurationSource.EnvironmentID != key {
			return op, storage.ErrConflict
		}
		return op, nil
	} else if !errors.Is(e, storage.ErrNotFound) {
		return model.DeploymentOperation{}, e
	}
	d, e := c.store.GetControlDocument(ctx, reviewID)
	if e != nil {
		return model.DeploymentOperation{}, e
	}
	if d.Kind != "environment-review" || d.ParentID != key {
		return model.DeploymentOperation{}, storage.ErrNotFound
	}
	var review environmentReview
	if _, e = c.readEncrypted(ctx, reviewID, "environment-review", &review); e != nil {
		return model.DeploymentOperation{}, e
	}
	if !review.ExpiresAt.After(c.now()) || review.EnvironmentRevision != expected || v.Revision != expected {
		return model.DeploymentOperation{}, storage.ErrPrecondition
	}
	if e = c.validateServiceReviewBase(ctx, v, review.Source); e != nil {
		return model.DeploymentOperation{}, e
	}
	input := review.Input
	input.ReplaceEnvironment = true
	input.ConfigurationSource = review.Source
	if len(restrictHostAccess) > 0 && restrictHostAccess[0] && !review.Source.RestrictedHostAccess {
		return model.DeploymentOperation{}, storage.ErrNotFound
	}
	input.ConfigurationSource.EnvironmentRevision = expected
	return c.deployPreparedEnvironment(ctx, v, input, actor, idem)
}
