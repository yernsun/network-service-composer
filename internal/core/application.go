package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/composecheck"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type ApplicationPreflightInput struct {
	CatalogAnalysis     bool                          `json:"-"`
	DeploymentPlan      *composecheck.Plan            `json:"deployment_plan,omitempty"`
	NetworkName         string                        `json:"network_name,omitempty"`
	ProjectName         string                        `json:"project_name,omitempty"`
	ReplaceEnvironment  bool                          `json:"-"`
	ConfigurationSource model.DeploymentSource        `json:"-"`
	RuntimeID           string                        `json:"runtime_id"`
	CatalogAppVersionID string                        `json:"catalog_app_version_id,omitempty"`
	ComposeYAML         string                        `json:"compose_yaml,omitempty"`
	EnvExample          string                        `json:"env_example,omitempty"`
	Manifest            string                        `json:"manifest,omitempty"`
	Environment         applicationx.EnvironmentInput `json:"environment"`
}

type ApplicationPreflight struct {
	Validation  composecheck.Result `json:"validation"`
	NetworkName string              `json:"network_name,omitempty"`
	ProjectName string              `json:"project_name,omitempty"`
	applicationx.PreflightResult
	Environment map[string]string `json:"environment"`
}

type ApplicationDiff struct {
	FromRevisionID       string   `json:"from_revision_id,omitempty"`
	ComposeChanged       bool     `json:"compose_changed"`
	EnvironmentAdded     []string `json:"environment_added,omitempty"`
	EnvironmentRemoved   []string `json:"environment_removed,omitempty"`
	EnvironmentChanged   []string `json:"environment_changed,omitempty"`
	ServicesAdded        []string `json:"services_added,omitempty"`
	ServicesRemoved      []string `json:"services_removed,omitempty"`
	ServiceConfigChanged bool     `json:"service_config_changed"`
	NetworksChanged      bool     `json:"networks_changed"`
	VolumesChanged       bool     `json:"volumes_changed"`
	DeployPolicyChanged  bool     `json:"deploy_policy_changed"`
}

type ApplicationRevisionPreview struct {
	ApplicationPreflight
	Diff ApplicationDiff `json:"diff"`
}

func (c *Core) PreflightApplication(ctx context.Context, input ApplicationPreflightInput) (ApplicationPreflight, error) {
	runtime, err := c.store.GetRuntime(ctx, input.RuntimeID)
	if err != nil {
		return ApplicationPreflight{}, err
	}
	pool, err := c.store.GetPool(ctx, runtime.PoolID)
	if err != nil {
		return ApplicationPreflight{}, err
	}
	input, err = c.resolveCatalogInput(ctx, input)
	if err != nil {
		return ApplicationPreflight{}, err
	}
	if err = prepareApplicationNetwork(&input, runtime, input.ProjectName, pool.BackendNetwork); err != nil {
		return ApplicationPreflight{}, err
	}
	allowedSecrets, allowedConfigs, err := c.allowedDockerResourceNames(ctx, runtime)
	if err != nil {
		return ApplicationPreflight{}, err
	}
	result, err := applicationx.Preflight(applicationx.PreflightInput{RestrictedHostAccess: input.ConfigurationSource.RestrictedHostAccess, ComposeYAML: input.ComposeYAML, EnvExample: input.EnvExample,
		ManifestYAML: input.Manifest, Environment: input.Environment, Backend: runtime.Type,
		AllowedSecrets: allowedSecrets, AllowedConfigs: allowedConfigs,
		ProtectedNetworks: []string{pool.BackendNetwork}, DataNodeID: effectiveDeploymentPlan(input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan).DataNodeID, RuntimeNodeID: applicationx.RuntimeNodeID(runtime.Endpoint)})
	if err != nil {
		return ApplicationPreflight{}, invalid("validation.application_preflight_failed", "compose_yaml", err.Error())
	}
	validation, policyErr := c.validateDeploymentCompose(ctx, runtime, result.ResolvedYAML, input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan)
	if policyErr != nil {
		err = policyErr
		return ApplicationPreflight{}, err
	}
	input.ConfigurationSource.DeploymentPlan = effectiveDeploymentPlan(input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan)
	redactDeploymentValidation(&validation, result.Environment, result.SecretKeys)

	result.Environment = applicationx.RedactEnvironment(result.Environment, result.SecretKeys)
	result.ResolvedYAML = applicationx.RedactResolvedYAML(result.ComposeYAML, result.Environment, result.SecretKeys)
	return ApplicationPreflight{Validation: validation, NetworkName: input.NetworkName, ProjectName: input.ProjectName, PreflightResult: result, Environment: result.Environment}, nil
}

func (c *Core) PreflightApplicationRevision(ctx context.Context, applicationID string, input ApplicationPreflightInput) (ApplicationRevisionPreview, error) {
	managedApplication, err := c.store.GetApplication(ctx, applicationID)
	if err != nil {
		return ApplicationRevisionPreview{}, err
	}
	runtime, err := c.store.GetRuntime(ctx, managedApplication.RuntimeID)
	if err != nil {
		return ApplicationRevisionPreview{}, err
	}
	pool, err := c.store.GetPool(ctx, runtime.PoolID)
	if err != nil {
		return ApplicationRevisionPreview{}, err
	}
	previousEnvironment := map[string]string{}
	previousSecretKeys := []string(nil)
	previousCompose := ""
	previousResolved := ""
	previousComposeSHA := ""
	fromRevisionID := managedApplication.DesiredRevisionID
	if input.ConfigurationSource.ServiceScope != nil {
		fromRevisionID = managedApplication.ActiveRevisionID
	}
	if fromRevisionID == "" {
		fromRevisionID = managedApplication.ActiveRevisionID
	}
	input.RuntimeID = runtime.ID
	if input.DeploymentPlan == nil && fromRevisionID != "" {
		base, e := c.store.GetApplicationRevision(ctx, fromRevisionID)
		if e != nil {
			return ApplicationRevisionPreview{}, e
		}
		input.ConfigurationSource.DeploymentPlan = base.ConfigurationSource.DeploymentPlan
	}

	input, err = c.resolveCatalogInput(ctx, input)
	if err != nil {
		return ApplicationRevisionPreview{}, err
	}
	if fromRevisionID != "" {
		current, getErr := c.store.GetApplicationRevision(ctx, fromRevisionID)
		if getErr != nil {
			return ApplicationRevisionPreview{}, getErr
		}
		previousComposeSHA = current.ComposeSHA256
		previousSecretKeys = append(previousSecretKeys, current.SecretKeys...)
		compose, _, decryptErr := c.keyring.Decrypt(current.EncryptedCompose, revisionAAD(current.ID, "compose"))
		if decryptErr != nil {
			return ApplicationRevisionPreview{}, fmt.Errorf("decrypt base application Compose: %w", decryptErr)
		}
		previousCompose = string(compose)
		if digestString(previousCompose) != current.ComposeSHA256 {
			return ApplicationRevisionPreview{}, fmt.Errorf("base application Compose digest verification failed")
		}
		environmentText, _, decryptErr := c.keyring.Decrypt(current.EncryptedEnvironment, revisionAAD(current.ID, "environment"))
		if decryptErr != nil {
			return ApplicationRevisionPreview{}, fmt.Errorf("decrypt active application environment: %w", decryptErr)
		}
		previousEnvironment, decryptErr = applicationx.ParseDotEnv(string(environmentText))
		if decryptErr != nil {
			return ApplicationRevisionPreview{}, fmt.Errorf("decode active application environment: %w", decryptErr)
		}
		resolved, _, decryptErr := c.keyring.Decrypt(current.EncryptedResolved, revisionAAD(current.ID, "resolved"))
		if decryptErr != nil {
			return ApplicationRevisionPreview{}, fmt.Errorf("decrypt active application Compose: %w", decryptErr)
		}
		previousResolved = string(resolved)
		if _, inheritErr := c.inheritRevisionContent(ctx, current, &input); inheritErr != nil {
			return ApplicationRevisionPreview{}, inheritErr
		}
	}
	if input.ReplaceEnvironment {
		previousEnvironment = nil
		previousSecretKeys = nil
	}
	if err = prepareApplicationNetwork(&input, runtime, managedApplication.ProjectName, pool.BackendNetwork); err != nil {
		return ApplicationRevisionPreview{}, err
	}
	allowedSecrets, allowedConfigs, err := c.allowedDockerResourceNames(ctx, runtime)
	if err != nil {
		return ApplicationRevisionPreview{}, err
	}
	result, err := applicationx.Preflight(applicationx.PreflightInput{RestrictedHostAccess: input.ConfigurationSource.RestrictedHostAccess, ComposeYAML: input.ComposeYAML, EnvExample: input.EnvExample,
		ManifestYAML: input.Manifest, Environment: input.Environment, Backend: runtime.Type, Previous: previousEnvironment,
		PreviousSecretKeys: previousSecretKeys, AllowedSecrets: allowedSecrets,
		AllowedConfigs: allowedConfigs, ProtectedNetworks: []string{pool.BackendNetwork}, DataNodeID: effectiveDeploymentPlan(input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan).DataNodeID, RuntimeNodeID: applicationx.RuntimeNodeID(runtime.Endpoint)})
	if err != nil {
		return ApplicationRevisionPreview{}, invalid("validation.application_preflight_failed", "compose_yaml", err.Error())
	}
	if fromRevisionID != "" {
		base, e := c.store.GetApplicationRevision(ctx, fromRevisionID)
		if e != nil {
			return ApplicationRevisionPreview{}, e
		}
		if e = applicationx.ValidateDataNodeChange(previousResolved, result.ResolvedYAML, base.ConfigurationSource.DeploymentPlan.DataNodeID, effectiveDeploymentPlan(input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan).DataNodeID); e != nil {
			return ApplicationRevisionPreview{}, invalid("validation.application_preflight_failed", "compose_yaml", e.Error())
		}
	}

	if err = c.validateUnscopedSwarmRevision(ctx, managedApplication, runtime, input.ConfigurationSource, result.ResolvedYAML); err != nil {
		return ApplicationRevisionPreview{}, err
	}
	validation, policyErr := c.validateDeploymentCompose(ctx, runtime, result.ResolvedYAML, input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan, serviceScopeOverrides(input.ConfigurationSource, managedApplication.ReplicaOverrides))
	if policyErr != nil {
		err = policyErr
		return ApplicationRevisionPreview{}, err
	}
	input.ConfigurationSource.DeploymentPlan = effectiveDeploymentPlan(input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan)
	redactDeploymentValidation(&validation, result.Environment, result.SecretKeys)

	if err = pinPreflight(&result, input.ConfigurationSource); err != nil {
		return ApplicationRevisionPreview{}, err
	}
	diff := buildApplicationDiff(fromRevisionID, previousComposeSHA, previousCompose, previousResolved, previousEnvironment, result)
	if fromRevisionID != "" {
		base, readErr := c.store.GetApplicationRevision(ctx, fromRevisionID)
		if readErr != nil {
			return ApplicationRevisionPreview{}, readErr
		}
		diff.NetworksChanged = diff.NetworksChanged || base.ConfigurationSource.NetworkName != input.NetworkName
	}
	redacted := result
	redacted.Environment = applicationx.RedactEnvironment(result.Environment, result.SecretKeys)
	redacted.ResolvedYAML = applicationx.RedactResolvedYAML(result.ComposeYAML, result.Environment, result.SecretKeys)
	return ApplicationRevisionPreview{ApplicationPreflight: ApplicationPreflight{Validation: validation, NetworkName: input.NetworkName, ProjectName: input.ProjectName, PreflightResult: redacted, Environment: redacted.Environment}, Diff: diff}, nil
}

func buildApplicationDiff(fromRevisionID, previousComposeSHA, previousCompose, previousResolved string, previousEnvironment map[string]string, candidate applicationx.PreflightResult) ApplicationDiff {
	diff := ApplicationDiff{FromRevisionID: fromRevisionID, ComposeChanged: previousComposeSHA != candidate.ComposeSHA256}
	for key, value := range candidate.Environment {
		previous, exists := previousEnvironment[key]
		if !exists {
			diff.EnvironmentAdded = append(diff.EnvironmentAdded, key)
		} else if previous != value {
			diff.EnvironmentChanged = append(diff.EnvironmentChanged, key)
		}
	}
	for key := range previousEnvironment {
		if _, exists := candidate.Environment[key]; !exists {
			diff.EnvironmentRemoved = append(diff.EnvironmentRemoved, key)
		}
	}
	previousServices := map[string]struct{}{}
	if previousResolved != "" {
		if value, err := applicationx.Preflight(applicationx.PreflightInput{ComposeYAML: previousResolved, Backend: model.RuntimeDocker, CatalogAnalysis: true}); err == nil {
			for _, service := range value.Services {
				previousServices[service.Name] = struct{}{}
			}
		}
	}
	candidateServices := make(map[string]struct{}, len(candidate.Services))
	for _, service := range candidate.Services {
		candidateServices[service.Name] = struct{}{}
		if _, exists := previousServices[service.Name]; !exists {
			diff.ServicesAdded = append(diff.ServicesAdded, service.Name)
		}
	}
	for service := range previousServices {
		if _, exists := candidateServices[service]; !exists {
			diff.ServicesRemoved = append(diff.ServicesRemoved, service)
		}
	}
	before, _ := applicationx.ComposeSectionDigests(previousCompose)
	after, _ := applicationx.ComposeSectionDigests(candidate.ComposeYAML)
	diff.ServiceConfigChanged = before["services"] != after["services"]
	diff.NetworksChanged = before["networks"] != after["networks"]
	diff.VolumesChanged = before["volumes"] != after["volumes"]
	diff.DeployPolicyChanged = before["deploy"] != after["deploy"]
	for _, values := range [][]string{diff.EnvironmentAdded, diff.EnvironmentRemoved, diff.EnvironmentChanged, diff.ServicesAdded, diff.ServicesRemoved} {
		sort.Strings(values)
	}
	return diff
}

type InstallApplicationInput struct {
	ApplicationPreflightInput
	Name                  string                     `json:"name"`
	ProjectName           string                     `json:"project_name"`
	RegistryCredentialRef string                     `json:"registry_credential_ref,omitempty"`
	Adopt                 bool                       `json:"adopt,omitempty"`
	DomainBinding         *InstallDomainBindingInput `json:"domain_binding,omitempty"`
	IdempotencyKey        string                     `json:"-"`
	Actor                 string                     `json:"-"`
}

// InstallDomainBindingInput selects a Compose service by its definition name
// and then uses the same binding contract as the Service API.
type InstallDomainBindingInput struct {
	Service string `json:"service"`
	CreateDomainBindingInput
}

func (c *Core) InstallApplication(ctx context.Context, input InstallApplicationInput) (model.Application, model.DeploymentOperation, ApplicationPreflight, error) {
	runtime, err := c.requireManagement(ctx, input.RuntimeID)
	if err != nil {
		return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	typeName := "INSTALL"
	if input.Adopt {
		typeName = "ADOPT"
	}
	requestSHA256, err := operationRequestSHA256(typeName, input)
	if err != nil {
		return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	if existing, getErr := c.store.GetDeploymentOperationByIdempotency(ctx, runtime.ID, input.IdempotencyKey); getErr == nil {
		if matchErr := validateIdempotentOperation(existing, typeName, "", "", requestSHA256); matchErr != nil {
			return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, matchErr
		}
		managedApplication, appErr := c.store.GetApplication(ctx, existing.ApplicationID)
		return managedApplication, existing, ApplicationPreflight{}, appErr
	} else if !errors.Is(getErr, storage.ErrNotFound) {
		return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, getErr
	}
	input.ApplicationPreflightInput, err = c.resolveCatalogInput(ctx, input.ApplicationPreflightInput)
	if err != nil {
		return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	input.Name = strings.TrimSpace(input.Name)
	input.ProjectName = strings.TrimSpace(input.ProjectName)
	if input.Name == "" {
		input.Name = input.ProjectName
	}
	if input.Name == "" || !validProjectName(input.ProjectName) {
		return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, invalid("validation.application_identity_invalid", "project_name", "name and a lowercase project_name beginning with a letter or number are required")
	}
	input.RegistryCredentialRef = strings.TrimSpace(input.RegistryCredentialRef)
	if input.RegistryCredentialRef != "" {
		if _, ok := c.config.Registries[input.RegistryCredentialRef]; !ok {
			return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, invalid("validation.registry_credential_unconfigured", "registry_credential_ref", "registry credential reference is not configured")
		}
	}
	pool, err := c.store.GetPool(ctx, runtime.PoolID)
	if err != nil {
		return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	if err = prepareApplicationNetwork(&input.ApplicationPreflightInput, runtime, input.ProjectName, pool.BackendNetwork); err != nil {
		return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	allowedSecrets, allowedConfigs, err := c.allowedDockerResourceNames(ctx, runtime)
	if err != nil {
		return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	result, err := applicationx.Preflight(applicationx.PreflightInput{RestrictedHostAccess: input.ConfigurationSource.RestrictedHostAccess, ComposeYAML: input.ComposeYAML, EnvExample: input.EnvExample,
		ManifestYAML: input.Manifest, Environment: input.Environment, Backend: runtime.Type,
		AllowedSecrets: allowedSecrets, AllowedConfigs: allowedConfigs,
		ProtectedNetworks: []string{pool.BackendNetwork}, DataNodeID: effectiveDeploymentPlan(input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan).DataNodeID, RuntimeNodeID: applicationx.RuntimeNodeID(runtime.Endpoint)})
	if err != nil {
		return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, invalid("validation.application_preflight_failed", "compose_yaml", err.Error())
	}
	validation, policyErr := c.validateDeploymentCompose(ctx, runtime, result.ResolvedYAML, input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan)
	if policyErr != nil {
		err = policyErr
		return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	input.ConfigurationSource.DeploymentPlan = effectiveDeploymentPlan(input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan)
	redactDeploymentValidation(&validation, result.Environment, result.SecretKeys)

	if err = c.validateApplicationProjectClaim(ctx, runtime, input.ProjectName, result.Services, input.Adopt); err != nil {
		return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	now := c.now()
	applicationID := id.New(id.Application)
	revisionID := id.New(id.ApplicationRevision)
	operationID := id.New(id.Operation)
	if err = pinPreflight(&result, input.ConfigurationSource); err != nil {
		return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	if err = c.mergeServiceRevision(ctx, &input.ConfigurationSource, &result, revisionID); err != nil {
		return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	if err = c.mergeEntrypointIntents(ctx, &input.ConfigurationSource, "", result.ComposeYAML); err != nil {
		return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	input.ConfigurationSource.DockerResources, err = c.freezeDockerResources(ctx, runtime, result.ResolvedYAML)
	if err != nil {
		return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	revision, err := c.encryptRevision(applicationID, revisionID, 1, "", input.CatalogAppVersionID, input.Actor, result, now)
	if err != nil {
		return model.Application{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	revision.ConfigurationSource = input.ConfigurationSource
	value := model.Application{ID: applicationID, RuntimeID: runtime.ID, Name: input.Name, ProjectName: input.ProjectName,
		RegistryCredentialRef: input.RegistryCredentialRef,
		CatalogAppVersionID:   input.CatalogAppVersionID, DesiredRevisionID: revision.ID, Status: model.ApplicationPending,
		Managed: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	services, servicesByName, err := c.applicationInstallServices(ctx, runtime, value, result.Services, input.Adopt, now)
	if err != nil {
		return value, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	var domain *model.Domain
	var certificate *model.Certificate
	var route *model.Route
	if input.DomainBinding != nil {
		serviceName := strings.TrimSpace(input.DomainBinding.Service)
		service, exists := servicesByName[serviceName]
		if !exists {
			return value, model.DeploymentOperation{}, ApplicationPreflight{}, invalid("validation.binding_service_unknown", "domain_binding.service", "domain binding service must name a service from the Compose definition")
		}
		if input.Adopt && !service.RoutingEligible {
			return value, model.DeploymentOperation{}, ApplicationPreflight{}, invalid("validation.external_service_not_route_ready", "domain_binding.service", fmt.Sprintf("adoption target is not route-ready: %s; attach it to the pool backend network before installing a binding", strings.Join(service.RoutingIssues, "; ")))
		}
		bindingDomain, bindingCertificate, bindingRoute, bindingErr := c.prepareDomainBinding(ctx, service, runtime,
			input.DomainBinding.CreateDomainBindingInput, model.RoutePending, now)
		if bindingErr != nil {
			return value, model.DeploymentOperation{}, ApplicationPreflight{}, bindingErr
		}
		domain = &bindingDomain
		certificate = bindingCertificate
		route = &bindingRoute
	}
	operationInput := map[string]any{"service_names": serviceNames(result.Services), "incremental": input.ConfigurationSource.ServiceScope != nil}
	if route != nil {
		operationInput["route_id"] = route.ID
	}
	operation, err := c.newOperation(operationID, value.ID, runtime.ID, "", revision.ID, typeName, input.IdempotencyKey,
		operationInput, now)
	if err != nil {
		return value, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	operation.RequestSHA256 = requestSHA256
	if err = c.store.CreateApplicationInstall(ctx, &value, &revision, services, domain, certificate, route, &operation); err != nil {
		if input.IdempotencyKey != "" && errors.Is(err, storage.ErrConflict) {
			if existing, getErr := c.store.GetDeploymentOperationByIdempotency(ctx, runtime.ID, input.IdempotencyKey); getErr == nil {
				if matchErr := validateIdempotentOperation(existing, typeName, "", "", requestSHA256); matchErr != nil {
					return value, operation, ApplicationPreflight{}, matchErr
				}
				managedApplication, appErr := c.store.GetApplication(ctx, existing.ApplicationID)
				return managedApplication, existing, ApplicationPreflight{}, appErr
			}
		}
		return value, operation, ApplicationPreflight{}, err
	}
	redacted := result
	redacted.Environment = applicationx.RedactEnvironment(result.Environment, result.SecretKeys)
	redacted.ResolvedYAML = applicationx.RedactResolvedYAML(result.ComposeYAML, result.Environment, result.SecretKeys)
	return value, operation, ApplicationPreflight{Validation: validation, NetworkName: input.NetworkName, ProjectName: input.ProjectName, PreflightResult: redacted, Environment: redacted.Environment}, nil
}

func (c *Core) applicationInstallServices(ctx context.Context, runtime model.Runtime, application model.Application, specs []applicationx.ServiceSpec, adopt bool, now time.Time) ([]model.Service, map[string]model.Service, error) {
	valuesByName := make(map[string]model.Service, len(specs))
	if adopt {
		observed, err := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{RuntimeID: runtime.ID})
		if err != nil {
			return nil, nil, err
		}
		byKey := make(map[string]model.Service, len(observed))
		for _, service := range observed {
			byKey[service.ServiceKey] = service
		}
		for _, spec := range specs {
			stableKey := application.ProjectName + "/" + spec.Name
			if runtime.Type == model.RuntimeSwarm {
				stableKey = application.ProjectName + "_" + spec.Name
			}
			service, exists := byKey[stableKey]
			if !exists {
				return nil, nil, invalid("validation.application_adoption_mismatch", "compose_yaml", "every saved Compose service must already exist in the discovered project or stack")
			}
			valuesByName[spec.Name] = service
			valuesByName[stableKey] = service
		}
		return nil, valuesByName, nil
	}
	services := make([]model.Service, 0, len(specs))
	for _, spec := range specs {
		stableKey := application.ProjectName + "/" + spec.Name
		if runtime.Type == model.RuntimeSwarm {
			stableKey = application.ProjectName + "_" + spec.Name
		}
		qualifiedName := application.ProjectName + "_" + spec.Name
		service := model.Service{ID: id.New(id.Service), RuntimeID: runtime.ID, ServiceKey: stableKey, ExternalID: stableKey,
			ApplicationID: application.ID, Managed: true, RoutingEligible: false,
			RoutingIssues: []string{"awaiting initial deployment and runtime discovery"}, Name: spec.Name,
			Namespace: application.ProjectName, QualifiedName: qualifiedName, DNSName: qualifiedName,
			DNSStatus: model.DNSMissing, DiscoveredProtocol: "http", DiscoveredPort: spec.TargetPort,
			Readiness: c.config.Controller.Endpoint.Readiness,
			Status:    model.ServiceUnavailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
		services = append(services, service)
		valuesByName[spec.Name] = service
		valuesByName[stableKey] = service
	}
	return services, valuesByName, nil
}

type CreateRevisionInput struct {
	ApplicationPreflightInput
	IdempotencyKey string                  `json:"-"`
	Actor          string                  `json:"-"`
	ServiceID      string                  `json:"-"`
	DeferredStatus model.ApplicationStatus `json:"-"`
}

func (c *Core) CreateApplicationRevision(ctx context.Context, applicationID string, input CreateRevisionInput, expected int64) (model.ApplicationRevision, model.DeploymentOperation, ApplicationPreflight, error) {
	application, err := c.store.GetApplication(ctx, applicationID)
	if err != nil {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	if application.System {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, storage.ErrSystemProtected
	}
	if !application.Managed {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, invalid("validation.service_adoption_required", "", "external applications must be explicitly adopted before updates")
	}
	runtime, err := c.requireManagement(ctx, application.RuntimeID)
	if err != nil {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	requestSHA256, err := operationRequestSHA256("UPGRADE", struct {
		ApplicationID string              `json:"application_id"`
		Input         CreateRevisionInput `json:"input"`
	}{ApplicationID: applicationID, Input: input})
	if err != nil {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	if existing, getErr := c.store.GetDeploymentOperationByIdempotency(ctx, runtime.ID, input.IdempotencyKey); getErr == nil {
		if matchErr := validateIdempotentOperation(existing, "UPGRADE", applicationID, "", requestSHA256); matchErr != nil {
			return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, matchErr
		}
		revision, revisionErr := c.store.GetApplicationRevision(ctx, existing.RevisionID)
		return revision, existing, ApplicationPreflight{}, revisionErr
	} else if !errors.Is(getErr, storage.ErrNotFound) {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, getErr
	}
	// Once an application has service revisions, all updates must carry a
	// frozen service scope. A legacy full-list activation would otherwise mark
	// omitted services removed even though Docker retains them.
	if input.ConfigurationSource.ServiceScope == nil && application.ActiveRevisionID != "" {
		active, readErr := c.store.GetApplicationRevision(ctx, application.ActiveRevisionID)
		if readErr != nil {
			return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, readErr
		}
		if active.ConfigurationSource.ServiceScope != nil {
			return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, controlInvalid("preflight_id", "review service changes before updating this Stack")
		}
	}
	if scope := input.ConfigurationSource.ServiceScope; scope != nil {
		if application.ActiveRevisionID != scope.BaseRevisionID {
			return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, fmt.Errorf("service revision base changed: %w", storage.ErrPrecondition)
		}
		if c.serviceRevisionInFlight(ctx, application) {
			return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, fmt.Errorf("service revision is in flight: %w", storage.ErrPrecondition)
		}
		if !applicationx.ReplicaOverridesMatch(scope.Names, application.ReplicaOverrides, scope.ObservedReplicaOverrides) {
			return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, fmt.Errorf("service replica overrides changed: %w", storage.ErrPrecondition)
		}
	}
	input.RuntimeID = runtime.ID
	if input.DeploymentPlan == nil && application.ActiveRevisionID != "" {
		base, e := c.store.GetApplicationRevision(ctx, application.ActiveRevisionID)
		if e != nil {
			return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, e
		}
		input.ConfigurationSource.DeploymentPlan = base.ConfigurationSource.DeploymentPlan
	}

	input.ApplicationPreflightInput, err = c.resolveCatalogInput(ctx, input.ApplicationPreflightInput)
	if err != nil {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	previous := make(map[string]string)
	previousSecretKeys := []string(nil)
	baseRevisionID := application.DesiredRevisionID
	if input.ConfigurationSource.ServiceScope != nil {
		baseRevisionID = application.ActiveRevisionID
	}
	if baseRevisionID == "" {
		baseRevisionID = application.ActiveRevisionID
	}
	revisionCatalogVersionID := input.CatalogAppVersionID
	if baseRevisionID != "" {
		current, getErr := c.store.GetApplicationRevision(ctx, baseRevisionID)
		if getErr != nil {
			return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, getErr
		}
		plaintext, _, decryptErr := c.keyring.Decrypt(current.EncryptedEnvironment, revisionAAD(current.ID, "environment"))
		if decryptErr != nil {
			return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, fmt.Errorf("decrypt active application environment: %w", decryptErr)
		}
		previous, decryptErr = applicationx.ParseDotEnv(string(plaintext))
		if decryptErr != nil {
			return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, fmt.Errorf("decode active application environment: %w", decryptErr)
		}
		previousSecretKeys = append(previousSecretKeys, current.SecretKeys...)
		inheritedCatalogVersionID, inheritErr := c.inheritRevisionContent(ctx, current, &input.ApplicationPreflightInput)
		if inheritErr != nil {
			return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, inheritErr
		}
		if revisionCatalogVersionID == "" {
			revisionCatalogVersionID = inheritedCatalogVersionID
		}
	}
	pool, err := c.store.GetPool(ctx, runtime.PoolID)
	if err != nil {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	if input.ReplaceEnvironment {
		previous = nil
		previousSecretKeys = nil
	}
	if err = prepareApplicationNetwork(&input.ApplicationPreflightInput, runtime, application.ProjectName, pool.BackendNetwork); err != nil {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	allowedSecrets, allowedConfigs, err := c.allowedDockerResourceNames(ctx, runtime)
	if err != nil {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	result, err := applicationx.Preflight(applicationx.PreflightInput{RestrictedHostAccess: input.ConfigurationSource.RestrictedHostAccess, ComposeYAML: input.ComposeYAML, EnvExample: input.EnvExample,
		ManifestYAML: input.Manifest, Environment: input.Environment, Backend: runtime.Type, Previous: previous,
		PreviousSecretKeys: previousSecretKeys, AllowedSecrets: allowedSecrets,
		AllowedConfigs: allowedConfigs, ProtectedNetworks: []string{pool.BackendNetwork}, DataNodeID: effectiveDeploymentPlan(input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan).DataNodeID, RuntimeNodeID: applicationx.RuntimeNodeID(runtime.Endpoint)})
	if err != nil {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, invalid("validation.application_preflight_failed", "compose_yaml", err.Error())
	}
	if baseRevisionID != "" {
		base, e := c.store.GetApplicationRevision(ctx, baseRevisionID)
		if e != nil {
			return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, e
		}
		raw, _, e := c.keyring.Decrypt(base.EncryptedResolved, revisionAAD(base.ID, "resolved"))
		if e != nil {
			return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, e
		}
		if e = applicationx.ValidateDataNodeChange(string(raw), result.ResolvedYAML, base.ConfigurationSource.DeploymentPlan.DataNodeID, effectiveDeploymentPlan(input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan).DataNodeID); e != nil {
			return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, invalid("validation.application_preflight_failed", "compose_yaml", e.Error())
		}
	}

	if err = c.validateUnscopedSwarmRevision(ctx, application, runtime, input.ConfigurationSource, result.ResolvedYAML); err != nil {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	validation, policyErr := c.validateDeploymentCompose(ctx, runtime, result.ResolvedYAML, input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan, serviceScopeOverrides(input.ConfigurationSource, application.ReplicaOverrides))
	if policyErr != nil {
		err = policyErr
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	input.ConfigurationSource.DeploymentPlan = effectiveDeploymentPlan(input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan)
	redactDeploymentValidation(&validation, result.Environment, result.SecretKeys)

	sequence, err := c.nextRevisionSequence(ctx, application.ID)
	if err != nil {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	now := c.now()
	revisionID := id.New(id.ApplicationRevision)
	if err = pinPreflight(&result, input.ConfigurationSource); err != nil {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	if err = c.mergeServiceRevision(ctx, &input.ConfigurationSource, &result, revisionID); err != nil {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	if err = c.mergeEntrypointIntents(ctx, &input.ConfigurationSource, application.ActiveRevisionID, result.ComposeYAML); err != nil {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	input.ConfigurationSource.DockerResources, err = c.freezeDockerResources(ctx, runtime, result.ResolvedYAML)
	if err != nil {
		return model.ApplicationRevision{}, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	revision, err := c.encryptRevision(application.ID, revisionID, sequence, baseRevisionID,
		revisionCatalogVersionID, input.Actor, result, now)
	if err != nil {
		return revision, model.DeploymentOperation{}, ApplicationPreflight{}, err
	}
	revision.ConfigurationSource = input.ConfigurationSource
	operationInput := map[string]any{"service_names": serviceNames(result.Services), "incremental": input.ConfigurationSource.ServiceScope != nil}
	if input.DeferredStatus != "" {
		operationInput["deferred_status"] = input.DeferredStatus
	}
	operation, err := c.newOperation(id.New(id.Operation), application.ID, runtime.ID, input.ServiceID, revision.ID, "UPGRADE",
		input.IdempotencyKey, operationInput, now)
	if err != nil {
		return revision, operation, ApplicationPreflight{}, err
	}
	operation.RequestSHA256 = requestSHA256
	if err = c.store.CreateApplicationRevision(ctx, &revision, &operation, expected, now); err != nil {
		if input.IdempotencyKey != "" && errors.Is(err, storage.ErrConflict) {
			if existing, getErr := c.store.GetDeploymentOperationByIdempotency(ctx, runtime.ID, input.IdempotencyKey); getErr == nil {
				if matchErr := validateIdempotentOperation(existing, "UPGRADE", applicationID, "", requestSHA256); matchErr != nil {
					return revision, operation, ApplicationPreflight{}, matchErr
				}
				storedRevision, revisionErr := c.store.GetApplicationRevision(ctx, existing.RevisionID)
				return storedRevision, existing, ApplicationPreflight{}, revisionErr
			}
		}
		return revision, operation, ApplicationPreflight{}, fmt.Errorf("store application revision with expected revision %d: %w", expected, err)
	}
	redacted := result
	redacted.Environment = applicationx.RedactEnvironment(result.Environment, result.SecretKeys)
	redacted.ResolvedYAML = applicationx.RedactResolvedYAML(result.ComposeYAML, result.Environment, result.SecretKeys)
	return revision, operation, ApplicationPreflight{Validation: validation, NetworkName: input.NetworkName, ProjectName: input.ProjectName, PreflightResult: redacted, Environment: redacted.Environment}, nil
}

func (c *Core) QueueApplicationOperation(ctx context.Context, applicationID, action, idempotencyKey string) (model.DeploymentOperation, error) {
	application, err := c.store.GetApplication(ctx, applicationID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	if application.System {
		return model.DeploymentOperation{}, storage.ErrSystemProtected
	}
	if !application.Managed {
		return model.DeploymentOperation{}, invalid("validation.service_adoption_required", "", "external applications must be explicitly adopted before lifecycle operations")
	}
	if _, err = c.requireManagement(ctx, application.RuntimeID); err != nil {
		return model.DeploymentOperation{}, err
	}
	action = strings.ToUpper(strings.TrimSpace(action))
	switch action {
	case "START", "STOP", "DOWN", "REDEPLOY":
	default:
		return model.DeploymentOperation{}, invalid("validation.application_operation_invalid", "action", "action must be start, stop, down, or redeploy")
	}
	revisionID := application.ActiveRevisionID
	if action == "START" || action == "REDEPLOY" {
		if application.DesiredRevisionID != "" {
			revisionID = application.DesiredRevisionID
		}
		if revisionID == "" {
			return model.DeploymentOperation{}, invalid("validation.application_revision_missing", "", "application has no deployable revision")
		}
		rt, readErr := c.store.GetRuntime(ctx, application.RuntimeID)
		if readErr != nil {
			return model.DeploymentOperation{}, readErr
		}
		rev, readErr := c.store.GetApplicationRevision(ctx, revisionID)
		if readErr != nil {
			return model.DeploymentOperation{}, readErr
		}
		if scope := rev.ConfigurationSource.ServiceScope; scope != nil && revisionID != application.ActiveRevisionID && !(scope.DeferredUntilStart && (application.Status == model.ApplicationStopped || application.Status == model.ApplicationDown)) {
			return model.DeploymentOperation{}, controlInvalid("revision_id", "review the failed service changes against current Docker state before retrying")
		}
		raw, _, readErr := c.keyring.Decrypt(rev.EncryptedResolved, revisionAAD(rev.ID, "resolved"))
		if readErr != nil {
			return model.DeploymentOperation{}, readErr
		}
		if _, readErr = c.validateDeploymentCompose(ctx, rt, string(raw), nil, rev.ConfigurationSource.DeploymentPlan, application.ReplicaOverrides); readErr != nil {
			return model.DeploymentOperation{}, readErr
		}
	}
	now := c.now()
	operation, err := c.newOperation(id.New(id.Operation), application.ID, application.RuntimeID, "", revisionID, action, idempotencyKey, nil, now)
	if err != nil {
		return operation, err
	}
	operation.RequestSHA256, err = operationRequestSHA256(action, struct {
		ApplicationID string `json:"application_id"`
	}{ApplicationID: application.ID})
	if err != nil {
		return operation, err
	}
	return c.store.CreateDeploymentOperation(ctx, &operation)
}

type ServiceOperationOptions struct {
	Persist          bool
	ExpectedRevision int64
	Actor            string
}

func (c *Core) QueueServiceOperation(ctx context.Context, serviceID, action string, replicas int, idempotencyKey string, options ...ServiceOperationOptions) (model.DeploymentOperation, error) {
	service, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	if err = c.store.CheckProjectResource(ctx, service.ID); err != nil {
		return model.DeploymentOperation{}, err
	}
	if !service.Managed || service.ApplicationID == "" {
		return model.DeploymentOperation{}, invalid("validation.service_adoption_required", "", "external services must be explicitly adopted before lifecycle operations")
	}
	var option ServiceOperationOptions
	if len(options) > 0 {
		option = options[0]
	}
	action = strings.ToUpper(strings.TrimSpace(action))
	if service.System || service.Status == model.ServiceRemoved && action != "REMOVE_RUNTIME" {
		return model.DeploymentOperation{}, storage.ErrSystemProtected
	}
	if _, err = c.requireManagement(ctx, service.RuntimeID); err != nil {
		return model.DeploymentOperation{}, err
	}
	if option.Persist && action != "SCALE" {
		return model.DeploymentOperation{}, invalid("validation.service_operation_invalid", "persist", "only scale can persist")
	}
	if option.Persist && action == "SCALE" || action == "REMOVE_RUNTIME" {
		if existing, getErr := c.store.GetDeploymentOperationByIdempotency(ctx, service.RuntimeID, idempotencyKey); getErr == nil {
			if existing.ServiceID != service.ID || existing.ApplicationID != service.ApplicationID ||
				(option.Persist && existing.Type != "UPGRADE") || (!option.Persist && existing.Type != action) {
				return model.DeploymentOperation{}, storage.ErrConflict
			}
			if option.Persist {
				revision, readErr := c.store.GetApplicationRevision(ctx, existing.RevisionID)
				if readErr != nil {
					return model.DeploymentOperation{}, readErr
				}
				scope := revision.ConfigurationSource.ServiceScope
				if scope == nil || scope.RequestedReplicas == nil || *scope.RequestedReplicas != replicas || len(scope.Names) != 1 ||
					strings.TrimPrefix(service.ServiceKey, service.Namespace+"/") != scope.Names[0] && strings.TrimPrefix(service.ServiceKey, service.Namespace+"_") != scope.Names[0] {
					return model.DeploymentOperation{}, storage.ErrConflict
				}
			}
			return existing, nil
		} else if !errors.Is(getErr, storage.ErrNotFound) {
			return model.DeploymentOperation{}, getErr
		}
	}
	if option.ExpectedRevision > 0 && service.Revision != option.ExpectedRevision {
		return model.DeploymentOperation{}, storage.ErrPrecondition
	}
	input := map[string]any{"service_name": service.Name, "service_key": service.ServiceKey,
		"runtime_object_id": service.RuntimeObjectID}
	switch action {
	case "RESTART":
	case "SCALE":
		if replicas < 0 || replicas > 10000 {
			return model.DeploymentOperation{}, invalid("validation.replicas_invalid", "replicas", "replicas must be between 0 and 10000")
		}
		input["replicas"] = replicas
	case "REMOVE_RUNTIME":
		if option.Persist {
			return model.DeploymentOperation{}, invalid("validation.service_operation_invalid", "persist", "runtime removal cannot persist")
		}
	default:
		return model.DeploymentOperation{}, invalid("validation.service_operation_invalid", "action", "action must be restart, scale or remove_runtime")
	}
	app, err := c.store.GetApplication(ctx, service.ApplicationID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	if app.System {
		return model.DeploymentOperation{}, storage.ErrSystemProtected
	}
	if app.ActiveRevisionID != "" && action == "SCALE" {
		rev, e := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
		if e != nil {
			return model.DeploymentOperation{}, e
		}
		if rev.ConfigurationSource.DeploymentPlan.DataNodeID != "" {
			raw, _, e := c.keyring.Decrypt(rev.EncryptedResolved, revisionAAD(rev.ID, "resolved"))
			if e != nil {
				return model.DeploymentOperation{}, e
			}
			name := applicationServiceNameForPolicy(service, app)
			if e := applicationx.ValidateDataNodePolicy(string(raw), rev.ConfigurationSource.DeploymentPlan.DataNodeID, map[string]int{name: replicas}); e != nil {
				return model.DeploymentOperation{}, invalid("validation.replicas_invalid", "replicas", e.Error())
			}
		}
	}

	if option.Persist {
		if action != "SCALE" {
			return model.DeploymentOperation{}, invalid("validation.service_operation_invalid", "persist", "only scale can persist")
		}
		return c.queuePersistentServiceScale(ctx, service, app, replicas, idempotencyKey, option.Actor)
	}
	if action == "SCALE" && app.Status != model.ApplicationRunning {
		return model.DeploymentOperation{}, invalid("validation.service_operation_invalid", "replicas", "temporary scale requires a running application")
	}
	if action == "SCALE" && service.RemovedAt != nil {
		return model.DeploymentOperation{}, storage.ErrPrecondition
	}
	rev, err := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	raw, _, err := c.keyring.Decrypt(rev.EncryptedResolved, revisionAAD(rev.ID, "resolved"))
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	rt, err := c.store.GetRuntime(ctx, service.RuntimeID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	overrides := map[string]int{}
	for name, count := range app.ReplicaOverrides {
		overrides[name] = count
	}
	if action == "SCALE" {
		name := service.Name
		if rt.Type == model.RuntimeSwarm {
			name = strings.TrimPrefix(name, app.ProjectName+"_")
		}
		overrides[name] = replicas
	}
	name := strings.TrimPrefix(service.ServiceKey, app.ProjectName+"_")
	if rt.Type != model.RuntimeSwarm {
		name = strings.TrimPrefix(service.ServiceKey, app.ProjectName+"/")
	}
	selected, selectErr := applicationx.SelectServices(string(raw), []string{name})
	if selectErr != nil {
		return model.DeploymentOperation{}, selectErr
	}
	if action == "SCALE" {
		if scaleErr := applicationx.ValidateServiceScaling(selected, name, replicas); scaleErr != nil {
			return model.DeploymentOperation{}, invalid("validation.replicas_invalid", "replicas", scaleErr.Error())
		}
	}
	if action != "REMOVE_RUNTIME" && (action != "SCALE" || replicas != 0) {
		if err = c.analyzeDeploymentCompose(ctx, rt, selected, rev.ConfigurationSource.DeploymentPlan, false, overrides).Error(); err != nil {
			return model.DeploymentOperation{}, invalid("validation.application_preflight_failed", "compose_yaml", err.Error())
		}
	}
	now := c.now()
	operation, err := c.newOperation(id.New(id.Operation), service.ApplicationID, service.RuntimeID, service.ID, app.ActiveRevisionID, action, idempotencyKey, input, now)
	if err != nil {
		return operation, err
	}
	operation.RequestSHA256, err = operationRequestSHA256(action, struct {
		ServiceID string         `json:"service_id"`
		Input     map[string]any `json:"input"`
	}{ServiceID: service.ID, Input: input})
	if err != nil {
		return operation, err
	}
	return c.store.CreateDeploymentOperation(ctx, &operation)
}

func (c *Core) requireManagement(ctx context.Context, runtimeID string) (model.Runtime, error) {
	runtime, err := c.store.GetRuntime(ctx, runtimeID)
	if err != nil {
		return runtime, err
	}
	if !c.config.Management.Enabled || !runtime.ManagementEnabled {
		return runtime, invalid("management.disabled", "", "Docker management requires management.enabled and runtime management_enabled")
	}
	if !runtime.Enabled {
		return runtime, invalid("management.runtime_disabled", "runtime_id", "runtime is disabled")
	}
	if runtime.Type == model.RuntimeSwarm && c.store.Dialect() == storage.SQLite {
		return runtime, invalid("management.swarm_sqlite_unsupported", "runtime_id", "Swarm management requires PostgreSQL or MySQL")
	}
	return runtime, nil
}

func (c *Core) resolveCatalogInput(ctx context.Context, input ApplicationPreflightInput) (ApplicationPreflightInput, error) {
	if input.CatalogAppVersionID == "" {
		return input, nil
	}
	if strings.TrimSpace(input.ComposeYAML) != "" || strings.TrimSpace(input.EnvExample) != "" || strings.TrimSpace(input.Manifest) != "" {
		return input, invalid("validation.catalog_content_override", "catalog_app_version_id", "catalog content is immutable; omit compose_yaml, env_example, and manifest when selecting a catalog version")
	}
	version, err := c.store.GetCatalogAppVersion(ctx, input.CatalogAppVersionID)
	if err != nil {
		return input, err
	}
	if version.Withdrawn {
		return input, invalid("validation.catalog_source_disabled", "catalog_app_version_id", "catalog version is withdrawn from new installation")
	}
	input.ComposeYAML = version.ComposeYAML
	input.EnvExample = version.EnvExample
	input.Manifest = version.ManifestJSON
	artifactDigest := catalogArtifactDigest(version.ComposeYAML, version.EnvExample, version.ManifestJSON, version.README, version.SwarmYAML)
	if digestString(version.ComposeYAML) != version.ComposeSHA256 || optionalDigest(version.SwarmYAML) != version.SwarmSHA256 || normalizeDigest(version.ArtifactDigest) != artifactDigest {
		return input, invalid("catalog.digest_mismatch", "catalog_app_version_id", "catalog application digest verification failed")
	}
	if input.RuntimeID != "" {
		rt, err := c.store.GetRuntime(ctx, input.RuntimeID)
		if err != nil {
			return input, err
		}
		if rt.Type == model.RuntimeSwarm && version.SwarmYAML != "" {
			plan := effectiveDeploymentPlan(input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan)
			if input.CatalogAnalysis && plan.DataNodeID == "" {
				input.ComposeYAML = version.SwarmYAML
			} else {
				input.ComposeYAML, err = applicationx.PinDataNode(version.SwarmYAML, plan.DataNodeID)
			}
			if err != nil {
				return input, invalid("validation.application_preflight_failed", "deployment_plan.data_node_id", err.Error())
			}
			input.ConfigurationSource.DeploymentPlan = plan
		}
	}
	return input, nil
}

// inheritRevisionContent makes environment-only revisions first-class. The
// original Compose document remains encrypted at rest and is decrypted only
// inside the Controller immediately before preflight. If the base revision is
// a catalog version, its immutable manifest/defaults are inherited as a unit
// so an environment-only change does not silently sever upgrade tracking.
func (c *Core) inheritRevisionContent(ctx context.Context, base model.ApplicationRevision, input *ApplicationPreflightInput) (string, error) {
	if input != nil && input.NetworkName == "" && input.ConfigurationSource.RestoredFrom == "" && input.ConfigurationSource.ServiceScope == nil {
		input.NetworkName = base.ConfigurationSource.NetworkName
	}
	if input != nil && base.ConfigurationSource.DeploymentPlan.DataNodeID != "" {
		plan := effectiveDeploymentPlan(input.DeploymentPlan, base.ConfigurationSource.DeploymentPlan)
		if plan.DataNodeID != base.ConfigurationSource.DeploymentPlan.DataNodeID {
			return "", invalid("validation.application_preflight_failed", "deployment_plan.data_node_id", "data node cannot change")
		}
		input.ConfigurationSource.DeploymentPlan.DataNodeID = plan.DataNodeID
	}
	if input != nil && input.DeploymentPlan == nil && input.ConfigurationSource.RestoredFrom == "" {
		input.ConfigurationSource.DeploymentPlan = base.ConfigurationSource.DeploymentPlan
	}
	if input == nil || strings.TrimSpace(input.ComposeYAML) != "" {
		return "", nil
	}
	compose, _, err := c.keyring.Decrypt(base.EncryptedCompose, revisionAAD(base.ID, "compose"))
	if err != nil {
		return "", fmt.Errorf("decrypt base application Compose: %w", err)
	}
	if strings.TrimSpace(string(compose)) == "" || digestString(string(compose)) != base.ComposeSHA256 {
		return "", fmt.Errorf("base application Compose digest verification failed")
	}
	input.ComposeYAML = string(compose)
	if base.CatalogAppVersionID == "" {
		return "", nil
	}
	metadataUntouched := strings.TrimSpace(input.EnvExample) == "" && strings.TrimSpace(input.Manifest) == ""
	version, err := c.store.GetCatalogAppVersion(ctx, base.CatalogAppVersionID)
	if err != nil {
		return "", fmt.Errorf("load base catalog application version: %w", err)
	}
	artifactDigest := catalogArtifactDigest(version.ComposeYAML, version.EnvExample, version.ManifestJSON, version.README, version.SwarmYAML)
	if digestString(version.ComposeYAML) != version.ComposeSHA256 || optionalDigest(version.SwarmYAML) != version.SwarmSHA256 || normalizeDigest(version.ArtifactDigest) != artifactDigest ||
		!catalogBaseMatches(version, base) {
		return "", fmt.Errorf("base catalog application digest verification failed")
	}
	if strings.TrimSpace(input.EnvExample) == "" {
		input.EnvExample = version.EnvExample
	}
	if strings.TrimSpace(input.Manifest) == "" {
		input.Manifest = version.ManifestJSON
	}
	if metadataUntouched {
		return base.CatalogAppVersionID, nil
	}
	return "", nil
}

func (c *Core) validateApplicationProjectClaim(ctx context.Context, runtime model.Runtime, projectName string, specs []applicationx.ServiceSpec, adopt bool) error {
	services, err := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{RuntimeID: runtime.ID})
	if err != nil {
		return err
	}
	expected := make(map[string]struct{}, len(specs))
	for _, service := range specs {
		key := projectName + "/" + service.Name
		if runtime.Type == model.RuntimeSwarm {
			key = projectName + "_" + service.Name
		}
		expected[key] = struct{}{}
	}
	found := make(map[string]struct{}, len(expected))
	for _, service := range services {
		if service.Status == model.ServiceRemoved || !serviceBelongsToProject(service, runtime.Type, projectName) {
			continue
		}
		if err := c.store.CheckProjectResource(ctx, service.ID); err != nil {
			return err
		}
		if !adopt {
			return invalid("validation.application_project_exists", "project_name", "a live Compose project or Swarm stack already uses this project_name; use explicit adoption")
		}
		if service.Managed {
			return invalid("validation.application_project_exists", "project_name", "the project already contains a managed service")
		}
		if _, exists := expected[service.ServiceKey]; !exists {
			return invalid("validation.application_adoption_mismatch", "compose_yaml", "the saved Compose services do not exactly match the discovered project or stack")
		}
		found[service.ServiceKey] = struct{}{}
	}
	if adopt && len(found) != len(expected) {
		return invalid("validation.application_adoption_mismatch", "compose_yaml", "every saved Compose service must already exist in the discovered project or stack")
	}
	return nil
}

func serviceBelongsToProject(service model.Service, runtimeType model.RuntimeType, projectName string) bool {
	if runtimeType == model.RuntimeSwarm {
		return strings.HasPrefix(service.ServiceKey, projectName+"_")
	}
	return service.Namespace == projectName || strings.HasPrefix(service.ServiceKey, projectName+"/")
}

func (c *Core) encryptRevision(applicationID, revisionID string, sequence int64, parentID, catalogID, actor string, result applicationx.PreflightResult, now time.Time) (model.ApplicationRevision, error) {
	compose, err := c.keyring.Encrypt([]byte(result.ComposeYAML), revisionAAD(revisionID, "compose"))
	if err != nil {
		return model.ApplicationRevision{}, err
	}
	environment, err := c.keyring.Encrypt([]byte(applicationx.RenderDotEnv(result.Environment)), revisionAAD(revisionID, "environment"))
	if err != nil {
		return model.ApplicationRevision{}, err
	}
	resolved, err := c.keyring.Encrypt([]byte(result.ResolvedYAML), revisionAAD(revisionID, "resolved"))
	if err != nil {
		return model.ApplicationRevision{}, err
	}
	return model.ApplicationRevision{ID: revisionID, ApplicationID: applicationID, Sequence: sequence,
		ParentRevisionID: parentID, CatalogAppVersionID: catalogID, ComposeSHA256: result.ComposeSHA256,
		EnvironmentSHA256: result.EnvironmentSHA256, ResolvedSHA256: result.ResolvedSHA256,
		EnvironmentKeys: result.EnvironmentKeys, SecretKeys: result.SecretKeys, EncryptedCompose: compose,
		EncryptedEnvironment: environment, EncryptedResolved: resolved, EncryptionKeyID: c.keyring.PrimaryID(),
		CreatedBy: actor, CreatedAt: now}, nil
}

func (c *Core) newOperation(operationID, applicationID, runtimeID, serviceID, revisionID, operationType, idempotencyKey string, input any, now time.Time) (model.DeploymentOperation, error) {
	value := model.DeploymentOperation{ID: operationID, ApplicationID: applicationID, RuntimeID: runtimeID,
		ServiceID: serviceID, RevisionID: revisionID, Type: operationType, Status: model.OperationQueued,
		IdempotencyKey: strings.TrimSpace(idempotencyKey), EncryptionKeyID: c.keyring.PrimaryID(), Revision: 1,
		CreatedAt: now, UpdatedAt: now}
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return value, err
		}
		encrypted, err := c.keyring.Encrypt(encoded, operationAAD(value.ID))
		if err != nil {
			return value, err
		}
		value.EncryptedInput = encrypted
	}
	return value, nil
}

func operationRequestSHA256(operationType string, payload any) (string, error) {
	encoded, err := json.Marshal(struct {
		Type    string `json:"type"`
		Payload any    `json:"payload"`
	}{Type: operationType, Payload: payload})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func validateIdempotentOperation(existing model.DeploymentOperation, operationType, applicationID, serviceID, requestSHA256 string) error {
	if existing.Type != operationType || existing.RequestSHA256 == "" || existing.RequestSHA256 != requestSHA256 ||
		(applicationID != "" && existing.ApplicationID != applicationID) || (serviceID != "" && existing.ServiceID != serviceID) {
		return fmt.Errorf("%w: Idempotency-Key was already used for a different request", storage.ErrConflict)
	}
	return nil
}

func (c *Core) nextRevisionSequence(ctx context.Context, applicationID string) (int64, error) {
	var sequence int64 = 1
	after := ""
	for {
		values, err := c.store.ListApplicationRevisions(ctx, applicationID, storage.Page{After: after, Limit: 200})
		if err != nil {
			return 0, err
		}
		for _, value := range values {
			if value.Sequence >= sequence {
				sequence = value.Sequence + 1
			}
		}
		if len(values) < 200 {
			return sequence, nil
		}
		after = values[len(values)-1].ID
	}
}

func revisionAAD(revisionID, part string) []byte {
	return []byte("application-revision:" + revisionID + ":" + part)
}
func operationAAD(operationID string) []byte { return []byte("deployment-operation:" + operationID) }

func validProjectName(value string) bool {
	if value == "" || len(value) > 128 || !((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= '0' && value[0] <= '9')) {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func serviceNames(values []applicationx.ServiceSpec) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.Name)
	}
	sort.Strings(result)
	return result
}

func digestString(value string) string {
	sum := sha256.Sum256([]byte(strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")))
	return hex.EncodeToString(sum[:])
}

func serviceScopeOverrides(source model.DeploymentSource, fallback map[string]int) map[string]int {
	if source.ServiceScope != nil && source.ServiceScope.ReplicaOverrides != nil {
		return source.ServiceScope.ReplicaOverrides
	}
	return fallback
}

func catalogBaseMatches(v model.CatalogAppVersion, base model.ApplicationRevision) bool {
	if v.ComposeSHA256 == base.ComposeSHA256 {
		return true
	}
	if v.SwarmYAML == "" {
		return false
	}
	raw, err := applicationx.PinDataNode(v.SwarmYAML, base.ConfigurationSource.DeploymentPlan.DataNodeID)
	return err == nil && digestString(raw) == base.ComposeSHA256
}

func applicationServiceNameForPolicy(s model.Service, a model.Application) string {
	return strings.TrimPrefix(strings.TrimPrefix(s.ServiceKey, a.ProjectName+"_"), a.ProjectName+"/")
}
