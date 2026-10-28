package core

import (
	"context"
	"sort"
	"strings"
	"time"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/images"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type ApplicationRestoreReview struct {
	ID               string                     `json:"id"`
	ApplicationID    string                     `json:"application_id"`
	SourceRevisionID string                     `json:"source_revision_id"`
	BaseRevision     int64                      `json:"base_revision"`
	ExpiresAt        time.Time                  `json:"expires_at"`
	Preview          ApplicationRevisionPreview `json:"preview"`
	DataRestored     bool                       `json:"data_restored"`
}
type savedApplicationRestore struct {
	Review          ApplicationRestoreReview  `json:"review"`
	Input           ApplicationPreflightInput `json:"input"`
	Source          *model.DeploymentSource   `json:"source,omitempty"`
	ServiceID       string                    `json:"service_id,omitempty"`
	ServiceRevision int64                     `json:"service_revision,omitempty"`
}

func (c *Core) ReviewApplicationRestore(ctx context.Context, application, revision string, serviceTarget ...string) (ApplicationRestoreReview, error) {
	if len(serviceTarget) != 0 && len(serviceTarget) != 2 {
		return ApplicationRestoreReview{}, storage.ErrPrecondition
	}
	app, err := c.store.GetApplication(ctx, application)
	if err != nil {
		return ApplicationRestoreReview{}, err
	}
	if app.System {
		return ApplicationRestoreReview{}, storage.ErrSystemProtected
	}
	if !app.Managed {
		return ApplicationRestoreReview{}, invalid("validation.service_adoption_required", "", "application is externally managed")
	}
	old, err := c.store.GetApplicationRevision(ctx, revision)
	if err != nil {
		return ApplicationRestoreReview{}, err
	}
	if old.ApplicationID != app.ID {
		return ApplicationRestoreReview{}, storage.ErrNotFound
	}
	serviceName, serviceID := "", ""
	var serviceRevision int64
	var serviceReplicas int
	if len(serviceTarget) == 2 {
		serviceName, serviceID = serviceTarget[0], serviceTarget[1]
		service, err := c.store.GetService(ctx, serviceID)
		if err != nil {
			return ApplicationRestoreReview{}, err
		}
		if service.ApplicationID != app.ID || service.System || !service.Managed || app.ActiveRevisionID == "" {
			return ApplicationRestoreReview{}, storage.ErrPrecondition
		}
		serviceRevision = service.Revision
		serviceReplicas = service.DesiredInstances
	}
	compose, _, err := c.keyring.Decrypt(old.EncryptedCompose, revisionAAD(old.ID, "compose"))
	if err != nil {
		return ApplicationRestoreReview{}, err
	}
	environmentRevision := old
	if serviceID != "" {
		active, e := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
		if e != nil {
			return ApplicationRestoreReview{}, e
		}
		target, e := applicationx.SelectServices(string(compose), []string{serviceName})
		if e != nil {
			return ApplicationRestoreReview{}, e
		}
		current, _, e := c.keyring.Decrypt(active.EncryptedCompose, revisionAAD(active.ID, "compose"))
		if e != nil {
			return ApplicationRestoreReview{}, e
		}
		merged, e := applicationx.MergeServices(string(current), target)
		if e != nil {
			return ApplicationRestoreReview{}, e
		}
		compose = []byte(merged)
		environmentRevision = active
	}
	env, _, err := c.keyring.Decrypt(environmentRevision.EncryptedEnvironment, revisionAAD(environmentRevision.ID, "environment"))
	if err != nil {
		return ApplicationRestoreReview{}, err
	}
	values, err := applicationx.ParseDotEnv(string(env))
	if err != nil {
		return ApplicationRestoreReview{}, err
	}
	secret := map[string]bool{}
	for _, key := range environmentRevision.SecretKeys {
		secret[key] = true
	}
	vars := []applicationx.EnvironmentVariable{}
	for key, value := range values {
		v := value
		vars = append(vars, applicationx.EnvironmentVariable{Key: key, Value: &v, Secret: secret[key]})
	}
	sort.Slice(vars, func(i, j int) bool { return vars[i].Key < vars[j].Key })
	in := ApplicationPreflightInput{RuntimeID: app.RuntimeID, ComposeYAML: string(compose), ReplaceEnvironment: true, Environment: applicationx.EnvironmentInput{Values: &vars}, ConfigurationSource: old.ConfigurationSource}
	in.ConfigurationSource.RestoredFrom = old.ID
	in.ConfigurationSource.Entrypoint = nil
	if serviceID != "" {
		current, e := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
		if e != nil {
			return ApplicationRestoreReview{}, e
		}
		in.ConfigurationSource = current.ConfigurationSource
		in.ConfigurationSource.RestoredFrom = old.ID
		in.ConfigurationSource.Entrypoint = nil
		in.ConfigurationSource.Images = map[string]string{}
		for name, image := range current.ConfigurationSource.Images {
			in.ConfigurationSource.Images[name] = image
		}
		if image := old.ConfigurationSource.Images[serviceName]; image != "" {
			in.ConfigurationSource.Images[serviceName] = image
		} else {
			delete(in.ConfigurationSource.Images, serviceName)
		}
		in.ConfigurationSource.ServiceScope = &model.ServiceDeploymentScope{Mode: "update", Names: []string{serviceName}, BaseRevisionID: app.ActiveRevisionID, RequestedReplicas: &serviceReplicas}
		if current.ConfigurationSource.ServiceScope != nil {
			in.ConfigurationSource.ServiceScope.ProjectID = current.ConfigurationSource.ServiceScope.ProjectID
		}
	}
	if serviceID == "" && in.ConfigurationSource.ServiceScope == nil && app.ActiveRevisionID != "" {
		active, readErr := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
		if readErr != nil {
			return ApplicationRestoreReview{}, readErr
		}
		if active.ConfigurationSource.ServiceScope != nil {
			// Restoring a pre-incremental revision updates its services while
			// retaining services added later. It is never an implicit deletion.
			_, _, names, parseErr := applicationx.ServiceDelta("services: {}\n", in.ComposeYAML, "create", false)
			if parseErr != nil {
				return ApplicationRestoreReview{}, parseErr
			}
			in.ConfigurationSource.ServiceScope = &model.ServiceDeploymentScope{Names: names, ProjectID: active.ConfigurationSource.ServiceScope.ProjectID}
			in.ConfigurationSource.EnvironmentID = active.ConfigurationSource.EnvironmentID
		}
	}
	if in.ConfigurationSource.ServiceScope != nil {
		prior := in.ConfigurationSource.ServiceScope
		if serviceID == "" {
			selected, e := applicationx.SelectServices(in.ComposeYAML, prior.Names)
			if e != nil {
				return ApplicationRestoreReview{}, e
			}
			in.ComposeYAML = selected
		}
		in.ConfigurationSource.ServiceScope = &model.ServiceDeploymentScope{Mode: "update", Names: append([]string(nil), prior.Names...), BaseRevisionID: app.ActiveRevisionID, ProjectID: prior.ProjectID, RequestedReplicas: prior.RequestedReplicas}
		target, err := c.GetEnvironment(ctx, in.ConfigurationSource.EnvironmentID)
		if err != nil {
			return ApplicationRestoreReview{}, err
		}
		if in.ConfigurationSource.ServiceScope.ProjectID == "" {
			in.ConfigurationSource.ServiceScope.ProjectID = target.ProjectID
		}
		runtime, runtimeErr := c.store.GetRuntime(ctx, target.RuntimeID)
		if runtimeErr != nil {
			return ApplicationRestoreReview{}, runtimeErr
		}
		if runtime.Type == model.RuntimeSwarm {
			if err = c.freezeServiceVersions(ctx, target, in.ConfigurationSource.ServiceScope); err != nil {
				return ApplicationRestoreReview{}, err
			}
		}
	}
	// Historical content is immutable; only the reviewed ownership/concurrency
	// tuple is rebound. Do not merge today's variables into yesterday's values.
	if in.ConfigurationSource.EnvironmentID != "" {
		current, err := c.GetEnvironment(ctx, in.ConfigurationSource.EnvironmentID)
		if err != nil {
			return ApplicationRestoreReview{}, err
		}
		if current.ApplicationID != app.ID || current.RuntimeID != app.RuntimeID {
			return ApplicationRestoreReview{}, storage.ErrConflict
		}
		common, err := c.variables(ctx, current.ProjectID)
		if err != nil {
			return ApplicationRestoreReview{}, err
		}
		local, err := c.variables(ctx, current.ID)
		if err != nil {
			return ApplicationRestoreReview{}, err
		}
		in.ConfigurationSource.EnvironmentRevision = current.Revision
		in.ConfigurationSource.PublicVariablesVersion = common.Revision
		in.ConfigurationSource.EnvironmentVariablesVersion = local.Revision
		in.ConfigurationSource.PreflightID = ""
		in.ConfigurationSource.UpdatePolicyRevision = 0
		in.ConfigurationSource.ImageUpdate = false
	}
	// Only previously pinned artifacts are eligible for automatic restoration.
	// A mutable tag cannot prove that it still denotes the historical image.
	preview, err := c.PreflightApplicationRevision(ctx, app.ID, in)
	if err != nil {
		return ApplicationRestoreReview{}, err
	}
	resolved, _, err := c.keyring.Decrypt(old.EncryptedResolved, revisionAAD(old.ID, "resolved"))
	if err != nil {
		return ApplicationRestoreReview{}, err
	}
	_, imageRefs, err := images.Pin(string(resolved), nil)
	if err != nil {
		return ApplicationRestoreReview{}, err
	}
	for name, image := range imageRefs {
		if serviceID != "" && name != serviceName {
			continue
		}
		if !strings.Contains(image, "@sha256:") && old.ConfigurationSource.Images[name] == "" {
			return ApplicationRestoreReview{}, controlInvalid("revision_id", "historical images are not pinned; prepare an explicit update instead")
		}
	}
	imagesToPin := old.ConfigurationSource.Images
	if serviceID != "" {
		imagesToPin = map[string]string{}
		if value := old.ConfigurationSource.Images[serviceName]; value != "" {
			imagesToPin[serviceName] = value
		}
	}
	preview.ResolvedYAML, _, err = images.Pin(preview.ResolvedYAML, imagesToPin)
	if err != nil {
		return ApplicationRestoreReview{}, err
	}
	r := ApplicationRestoreReview{ID: id.New("restore"), ApplicationID: app.ID, SourceRevisionID: old.ID, BaseRevision: app.Revision, ExpiresAt: c.now().Add(5 * time.Minute), Preview: preview}
	w, err := c.encryptedWrite(r.ID, "application-restore", app.ID, savedApplicationRestore{Review: r, Input: in, Source: &in.ConfigurationSource, ServiceID: serviceID, ServiceRevision: serviceRevision}, 0)
	if err != nil {
		return r, err
	}
	err = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{w})
	return r, err
}

func (c *Core) RestoreApplication(ctx context.Context, application, review, actor, idem string, expected int64, dataBoundary bool, serviceTarget ...string) (model.DeploymentOperation, error) {
	if strings.TrimSpace(idem) == "" {
		return model.DeploymentOperation{}, controlInvalid("idempotency_key", "an idempotency key is required")
	}
	if !dataBoundary {
		return model.DeploymentOperation{}, controlInvalid("acknowledge_data_boundary", "configuration restoration does not restore application data")
	}
	var saved savedApplicationRestore
	d, err := c.readEncrypted(ctx, review, "application-restore", &saved)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	if d.ParentID != application {
		return model.DeploymentOperation{}, storage.ErrNotFound
	}
	if saved.ServiceID != "" {
		if len(serviceTarget) != 1 || serviceTarget[0] != saved.ServiceID {
			return model.DeploymentOperation{}, storage.ErrPrecondition
		}
		service, e := c.store.GetService(ctx, saved.ServiceID)
		if e != nil {
			return model.DeploymentOperation{}, e
		}
		if service.ApplicationID != application || service.Revision != saved.ServiceRevision {
			return model.DeploymentOperation{}, storage.ErrPrecondition
		}
	} else if len(serviceTarget) != 0 {
		return model.DeploymentOperation{}, storage.ErrPrecondition
	}
	if expected != saved.Review.BaseRevision || !c.now().Before(saved.Review.ExpiresAt) {
		return model.DeploymentOperation{}, storage.ErrPrecondition
	}
	// Internal fields are reconstructed explicitly after JSON persistence.
	saved.Input.ReplaceEnvironment = true
	old, err := c.store.GetApplicationRevision(ctx, saved.Review.SourceRevisionID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	if old.ApplicationID != application {
		return model.DeploymentOperation{}, storage.ErrNotFound
	}
	// A review must carry the exact concurrency tuple the user reviewed.
	// Incomplete reviews must be recreated, never reconstructed from history.
	if saved.Source == nil {
		return model.DeploymentOperation{}, storage.ErrPrecondition
	}
	saved.Input.ConfigurationSource = *saved.Source
	saved.Input.ConfigurationSource.RestoredFrom = old.ID
	serviceID := ""
	if len(serviceTarget) != 0 {
		serviceID = serviceTarget[0]
	}
	_, op, _, err := c.CreateApplicationRevision(ctx, application, CreateRevisionInput{ApplicationPreflightInput: saved.Input, IdempotencyKey: idem, Actor: actor, ServiceID: serviceID}, expected)
	return op, err
}
