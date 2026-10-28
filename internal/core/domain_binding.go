package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type CreateDomainBindingInput struct {
	publication    *entrypointPublication
	DomainID       string             `json:"domain_id,omitempty"`
	Domain         *DomainConfigInput `json:"domain,omitempty"`
	Route          RouteConfigInput   `json:"route"`
	IdempotencyKey string             `json:"-"`
}

type UpdateDomainBindingInput struct {
	DomainRevision int64              `json:"domain_revision,omitempty"`
	Domain         *DomainConfigPatch `json:"domain,omitempty"`
	Route          *RouteConfigPatch  `json:"route,omitempty"`
}

func (c *Core) CreateDomainBinding(ctx context.Context, serviceID string, input CreateDomainBindingInput) (model.DomainBinding, *model.DeploymentOperation, error) {
	return c.createDomainBinding(ctx, serviceID, input, nil)
}

func (c *Core) createDomainBinding(ctx context.Context, serviceID string, input CreateDomainBindingInput, draft *DomainDraft) (model.DomainBinding, *model.DeploymentOperation, error) {
	service, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return model.DomainBinding{}, nil, err
	}
	runtime, err := c.store.GetRuntime(ctx, service.RuntimeID)
	if err != nil {
		return model.DomainBinding{}, nil, err
	}
	requestSHA256, err := operationRequestSHA256("DOMAIN_ATTACH", struct {
		ServiceID string                   `json:"service_id"`
		Input     CreateDomainBindingInput `json:"input"`
	}{ServiceID: service.ID, Input: input})
	if err != nil {
		return model.DomainBinding{}, nil, err
	}
	if existing, getErr := c.store.GetDeploymentOperationByIdempotency(ctx, runtime.ID, input.IdempotencyKey); getErr == nil {
		if matchErr := validateIdempotentOperation(existing, "DOMAIN_ATTACH", service.ApplicationID, service.ID, requestSHA256); matchErr != nil {
			return model.DomainBinding{}, nil, matchErr
		}
		binding, bindingErr := c.domainBindingFromOperation(ctx, service, existing)
		return binding, &existing, bindingErr
	} else if !errors.Is(getErr, storage.ErrNotFound) {
		return model.DomainBinding{}, nil, getErr
	}
	if !service.Managed && service.DNSStatus != model.DNSReady {
		reason := service.DNSLastError
		if reason == "" {
			reason = strings.Join(service.RoutingIssues, "; ")
		}
		return model.DomainBinding{}, nil, invalid("validation.external_service_dns_not_ready", "service_id", fmt.Sprintf("external service DNS is %s: %s; attach it to the pool network with its unique project-qualified alias", service.DNSStatus, reason))
	}
	var servicePatch *model.Service
	serviceExpected := int64(0)
	if !service.Managed && !service.RoutingEligible {
		enabledAllowed := service.EnabledOverride == nil || *service.EnabledOverride
		canResolvePort := input.Route.Backend.Port > 0 && enabledAllowed && routingIssuesAreOnlyMissingPort(service.RoutingIssues)
		if !canResolvePort {
			return model.DomainBinding{}, nil, invalid("validation.external_service_not_route_ready", "service_id", fmt.Sprintf("external service is not route-ready: %s; attach it to the pool backend network before binding", strings.Join(service.RoutingIssues, "; ")))
		}
		// An explicit binding port resolves discovery's ambiguous-port block for
		// this Service. Persist it so subsequent endpoint reconciliation and
		// readiness probes use the same operator-selected TCP port.
		serviceExpected = service.Revision
		service.TargetPortOverride = &input.Route.Backend.Port
		enabled := true
		service.EnabledOverride = &enabled
		service.UpdatedAt = c.now()
		servicePatch = &service
	}
	now := c.now()
	routeStatus := model.RouteActive
	if service.Managed {
		if _, err = c.requireManagement(ctx, runtime.ID); err != nil {
			return model.DomainBinding{}, nil, err
		}
		routeStatus = model.RoutePending
	}
	domain, certificate, route, err := c.prepareDomainBinding(ctx, service, runtime, input, routeStatus, now)
	if err != nil {
		return model.DomainBinding{}, nil, err
	}
	var operation *model.DeploymentOperation
	if service.Managed {
		value, operationErr := c.domainAttachOperation(ctx, service, route.ID, input.IdempotencyKey, input.publication)
		if operationErr != nil {
			return model.DomainBinding{}, nil, operationErr
		}
		value.RequestSHA256 = requestSHA256
		operation = &value
	}
	if input.publication != nil {
		write, writeErr := c.entrypointSubmissionWrite(*input.publication, service.ID, route.ID, operation.ID)
		if writeErr != nil {
			return model.DomainBinding{}, nil, writeErr
		}
		err = c.store.CreateServiceDomainBindingWithIntent(ctx, &domain, certificate, &route, operation, write, input.publication.App.Revision)
	} else if draft != nil {
		published := *draft
		published.Status = "published"
		published.PublishedDomainID = domain.ID
		published.PublishedRouteID = route.ID
		if operation != nil {
			published.OperationID = operation.ID
		}
		write, writeErr := c.controlWrite(draft.ID, "domain-draft", draft.EnvironmentID, published, draft.Revision)
		if writeErr != nil {
			return model.DomainBinding{}, nil, writeErr
		}
		err = c.store.CreateServiceDomainBindingWithDraft(ctx, &domain, certificate, &route, operation, servicePatch, serviceExpected, write)
	} else {
		err = c.store.CreateServiceDomainBinding(ctx, &domain, certificate, &route, operation, servicePatch, serviceExpected)
	}
	if err != nil {
		if operation != nil && input.IdempotencyKey != "" && errors.Is(err, storage.ErrConflict) {
			if existing, getErr := c.store.GetDeploymentOperationByIdempotency(ctx, runtime.ID, input.IdempotencyKey); getErr == nil {
				if matchErr := validateIdempotentOperation(existing, "DOMAIN_ATTACH", service.ApplicationID, service.ID, requestSHA256); matchErr != nil {
					return model.DomainBinding{}, nil, matchErr
				}
				binding, bindingErr := c.domainBindingFromOperation(ctx, service, existing)
				return binding, &existing, bindingErr
			}
		}
		return model.DomainBinding{}, nil, err
	}
	if servicePatch != nil {
		service, err = c.store.GetService(ctx, service.ID)
		if err != nil {
			return model.DomainBinding{}, nil, err
		}
	}
	binding := model.DomainBinding{ID: route.ID, Service: service, Domain: domain, Route: route,
		Source: route.Source, Status: route.Status, Editable: true}
	return c.withDNSTargets(ctx, binding), operation, nil
}

// prepareDomainBinding validates and builds the immutable part of a binding.
// Persistence is deliberately left to the caller so an application install can
// commit its definition, placeholder Services, Domain, Route and operation in
// one database transaction.
func (c *Core) prepareDomainBinding(ctx context.Context, service model.Service, runtime model.Runtime, input CreateDomainBindingInput, routeStatus model.RouteStatus, now time.Time) (model.Domain, *model.Certificate, model.Route, error) {
	if (input.DomainID == "") == (input.Domain == nil) {
		return model.Domain{}, nil, model.Route{}, invalid("validation.binding_domain_choice", "domain", "provide exactly one of domain_id or domain")
	}
	if input.Route.Backend.ServiceID != "" && input.Route.Backend.ServiceID != service.ID {
		return model.Domain{}, nil, model.Route{}, invalid("validation.binding_service_mismatch", "route.backend.service_id", "route backend service_id must match the Service binding target")
	}
	targetPort := input.Route.Backend.Port
	if targetPort == 0 {
		targetPort = service.EffectivePort()
	}
	if targetPort < 1 || targetPort > 65535 {
		return model.Domain{}, nil, model.Route{}, invalid("validation.binding_target_port_required", "route.backend.port", "a TCP backend port is required when it cannot be inferred uniquely")
	}
	path, err := NormalizePath(input.Route.Path)
	if err != nil {
		return model.Domain{}, nil, model.Route{}, err
	}
	protocol := strings.ToLower(strings.TrimSpace(input.Route.Backend.Protocol))
	if protocol == "" {
		protocol = service.EffectiveProtocol()
	}
	if !validProtocol(protocol) {
		return model.Domain{}, nil, model.Route{}, invalid("validation.upstream_protocol_invalid", "route.backend.protocol", "backend protocol must be http or https")
	}
	var domain model.Domain
	var certificate *model.Certificate
	if input.DomainID != "" {
		domain, err = c.store.GetDomain(ctx, input.DomainID)
		if err != nil {
			return model.Domain{}, nil, model.Route{}, err
		}
		if domain.PoolID != runtime.PoolID {
			return model.Domain{}, nil, model.Route{}, invalid("validation.binding_domain_pool_mismatch", "domain_id", "an existing domain can only be reused within the same gateway pool")
		}
	} else {
		domain, certificate, err = c.buildDomain(ctx, runtime.PoolID, *input.Domain, now)
		if err != nil {
			return model.Domain{}, nil, model.Route{}, err
		}
	}
	route := model.Route{ID: id.New(id.Route), DomainID: domain.ID, ServiceID: service.ID, Path: path,
		Kind: "proxy", Match: "prefer", ReadTimeoutSeconds: 60, ConnectTimeoutSeconds: 5,
		Priority: input.Route.Priority, StripPath: input.Route.StripPath, TargetPort: targetPort, UpstreamProtocol: protocol,
		UpstreamServerName: strings.TrimSpace(input.Route.Backend.ServerName),
		WebsocketEnabled:   input.Route.WebsocketEnabled, Enabled: true, Status: routeStatus, Source: model.SourceAPI,
		Revision: 1, CreatedAt: now, UpdatedAt: now}
	if route.UpstreamServerName != "" {
		route.UpstreamServerName, err = NormalizeHost(route.UpstreamServerName)
		if err != nil {
			return model.Domain{}, nil, model.Route{}, invalid("validation.upstream_server_name_invalid", "route.backend.server_name", "server_name must be a valid DNS name")
		}
	}
	return domain, certificate, route, nil
}

func (c *Core) UpdateDomainBinding(ctx context.Context, serviceID, routeID string, input UpdateDomainBindingInput, routeExpected int64) (model.DomainBinding, error) {
	service, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return model.DomainBinding{}, err
	}
	route, err := c.store.GetRoute(ctx, routeID)
	if err != nil {
		return model.DomainBinding{}, err
	}
	if route.ServiceID != service.ID {
		return model.DomainBinding{}, storageNotFound("domain binding")
	}
	if route.Source != model.SourceAPI {
		return model.DomainBinding{}, invalid("validation.label_binding_read_only", "", "label-sourced domain bindings are read-only")
	}
	domain, err := c.store.GetDomain(ctx, route.DomainID)
	if err != nil {
		return model.DomainBinding{}, err
	}
	if input.Route != nil {
		if input.Route.Path != nil {
			route.Path, err = NormalizePath(*input.Route.Path)
			if err != nil {
				return model.DomainBinding{}, err
			}
		}
		if input.Route.Priority != nil {
			route.Priority = *input.Route.Priority
		}
		if input.Route.StripPath != nil {
			route.StripPath = *input.Route.StripPath
		}
		if input.Route.WebsocketEnabled != nil {
			route.WebsocketEnabled = *input.Route.WebsocketEnabled
		}
		if input.Route.Enabled != nil {
			route.Enabled = *input.Route.Enabled
			if route.Enabled {
				route.Status = model.RouteActive
			} else {
				route.Status = model.RoutePaused
			}
		}
		if input.Route.Backend != nil {
			backend := input.Route.Backend
			if backend.ServiceID != nil && *backend.ServiceID != service.ID {
				return model.DomainBinding{}, invalid("validation.binding_service_mismatch", "route.backend.service_id", "route backend service_id must match the Service binding target")
			}
			if backend.Port != nil {
				if *backend.Port < 1 || *backend.Port > 65535 {
					return model.DomainBinding{}, invalid("validation.binding_target_port_required", "route.backend.port", "port must be in 1..65535")
				}
				route.TargetPort = *backend.Port
			}
			if backend.Protocol != nil {
				protocol := strings.ToLower(strings.TrimSpace(*backend.Protocol))
				if !validProtocol(protocol) {
					return model.DomainBinding{}, invalid("validation.upstream_protocol_invalid", "route.backend.protocol", "protocol must be http or https")
				}
				route.UpstreamProtocol = protocol
			}
			if backend.ServerName != nil {
				serverName := strings.TrimSpace(*backend.ServerName)
				if serverName != "" {
					serverName, err = NormalizeHost(serverName)
					if err != nil {
						return model.DomainBinding{}, invalid("validation.upstream_server_name_invalid", "route.backend.server_name", "server_name must be a valid DNS name")
					}
				}
				route.UpstreamServerName = serverName
			}
		}
	}
	now := c.now()
	route.UpdatedAt = now
	domainChanged := input.Domain != nil
	if !domainChanged {
		if err = c.store.UpdateRoute(ctx, &route, routeExpected); err != nil {
			return model.DomainBinding{}, err
		}
	} else {
		if input.DomainRevision < 1 {
			return model.DomainBinding{}, invalid("validation.binding_domain_revision_required", "domain_revision", "domain_revision is required when editing shared TLS settings")
		}
		if input.Domain.Host != nil {
			domain.Host, err = NormalizeHost(*input.Domain.Host)
			if err != nil {
				return model.DomainBinding{}, err
			}
		}
		if input.Domain.EntryPoints != nil {
			domain.HTTPEnabled, domain.HTTPSEnabled, err = NormalizeEntryPoints(*input.Domain.EntryPoints)
			if err != nil {
				return model.DomainBinding{}, err
			}
		}
		if input.Domain.HTTP3Enabled != nil {
			domain.HTTP3Enabled = *input.Domain.HTTP3Enabled
		}
		if input.Domain.RedirectHTTPToHTTPS != nil {
			domain.HTTPSRedirect = *input.Domain.RedirectHTTPToHTTPS
		}
		if input.Domain.Enabled != nil {
			domain.Enabled = *input.Domain.Enabled
		}
		if input.Domain.TLS != nil {
			domain.TLSMode = input.Domain.TLS.Mode
			domain.ACMEChallenge = input.Domain.TLS.ACMEChallenge
			domain.DNSProviderRef = input.Domain.TLS.DNSProviderRef
			// Managed certificates are selected by the Controller. Repeating TLS
			// settings without a certificate ID must retain that selection; changed
			// hosts or challenge settings are handled by managedNeedsProvision below.
			if certificateID := strings.TrimSpace(input.Domain.TLS.CertificateID); domain.TLSMode != model.TLSManaged || certificateID != "" {
				domain.CertificateID = certificateID
			}
		}
		if domain.TLSMode != model.TLSDisabled && domain.TLSMode != model.TLSManaged && domain.TLSMode != model.TLSCustom {
			return model.DomainBinding{}, invalid("validation.tls_mode_invalid", "domain.tls.mode", "tls mode must be disabled, managed, or custom")
		}
		domain.ACMEChallenge, domain.DNSProviderRef, err = c.validateACMESelection(ctx, domain.TLSMode, domain.ACMEChallenge, domain.DNSProviderRef)
		if err != nil {
			return model.DomainBinding{}, err
		}
		var certificate *model.Certificate
		// Compare against the persisted values before any generated certificate
		// is selected.
		persisted, getErr := c.store.GetDomain(ctx, domain.ID)
		if getErr != nil {
			return model.DomainBinding{}, getErr
		}
		managedNeedsProvision := domain.TLSMode == model.TLSManaged && (persisted.TLSMode != model.TLSManaged || persisted.Host != domain.Host || persisted.ACMEChallenge != domain.ACMEChallenge || persisted.DNSProviderRef != domain.DNSProviderRef || domain.CertificateID == "")
		if !domain.HTTPEnabled && !domain.HTTPSEnabled {
			return model.DomainBinding{}, invalid("validation.entrypoints_required", "domain.entrypoints", "at least one of http or https is required")
		}
		if domain.HTTPSEnabled && domain.TLSMode == model.TLSDisabled {
			return model.DomainBinding{}, invalid("validation.https_requires_tls", "domain.entrypoints", "the https entrypoint requires managed or custom TLS")
		}
		if !domain.HTTPSEnabled && domain.TLSMode != model.TLSDisabled {
			return model.DomainBinding{}, invalid("validation.tls_requires_https", "domain.tls.mode", "managed or custom TLS requires the https entrypoint")
		}
		if domain.HTTPSRedirect && (!domain.HTTPEnabled || !domain.HTTPSEnabled) {
			return model.DomainBinding{}, invalid("validation.redirect_requires_both_entrypoints", "domain.redirect_http_to_https", "HTTP to HTTPS redirect requires both entrypoints")
		}
		if err = c.validateHTTP3(ctx, domain.PoolID, domain.HTTP3Enabled, domain.HTTPSEnabled); err != nil {
			return model.DomainBinding{}, err
		}
		switch domain.TLSMode {
		case model.TLSDisabled:
			domain.ACMEChallenge = ""
			domain.DNSProviderRef = ""
			domain.CertificateID = ""
			domain.HTTPSRedirect = false
			domain.HTTPSEnabled = false
			domain.Status = model.DomainReady
		case model.TLSCustom:
			if domain.CertificateID == "" {
				return model.DomainBinding{}, invalid("validation.custom_tls_certificate_required", "certificate_id", "custom TLS requires a certificate_id")
			}
			if _, err = c.validateDomainCertificate(ctx, domain.PoolID, domain.Host, domain.CertificateID, model.TLSCustom); err != nil {
				return model.DomainBinding{}, err
			}
			domain.Status = model.DomainReady
		case model.TLSManaged:
			if managedNeedsProvision {
				value := model.Certificate{ID: id.New(id.Certificate), PoolID: domain.PoolID, Provider: "acme",
					ACMEChallenge: domain.ACMEChallenge, DNSProviderRef: domain.DNSProviderRef, Status: model.CertificatePending,
					PrimaryDomain: domain.Host, Domains: []string{domain.Host}, Revision: 1, CreatedAt: now, UpdatedAt: now}
				certificate = &value
				domain.CertificateID = value.ID
				domain.Status = model.DomainCertPending
			} else if _, err = c.validateDomainCertificate(ctx, domain.PoolID, domain.Host, domain.CertificateID, model.TLSManaged); err != nil {
				return model.DomainBinding{}, err
			}
		}
		domain.UpdatedAt = now
		if err = c.store.UpdateServiceDomainBinding(ctx, service.ID, &domain, certificate, &route, input.DomainRevision, routeExpected); err != nil {
			return model.DomainBinding{}, err
		}
	}
	domain, err = c.store.GetDomain(ctx, domain.ID)
	if err != nil {
		return model.DomainBinding{}, err
	}
	route, err = c.store.GetRoute(ctx, route.ID)
	if err != nil {
		return model.DomainBinding{}, err
	}
	binding := model.DomainBinding{ID: route.ID, Service: service, Domain: domain, Route: route,
		Source: route.Source, Status: route.Status, Editable: true}
	return c.withDNSTargets(ctx, binding), nil
}

func (c *Core) DeleteDomainBinding(ctx context.Context, serviceID, routeID string, expected int64, idempotencyKey string) (*model.DeploymentOperation, error) {
	service, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	requestSHA256, err := operationRequestSHA256("DOMAIN_DETACH", struct {
		ServiceID string `json:"service_id"`
		RouteID   string `json:"route_id"`
	}{ServiceID: service.ID, RouteID: routeID})
	if err != nil {
		return nil, err
	}
	if existing, getErr := c.store.GetDeploymentOperationByIdempotency(ctx, service.RuntimeID, idempotencyKey); getErr == nil {
		if matchErr := validateIdempotentOperation(existing, "DOMAIN_DETACH", service.ApplicationID, service.ID, requestSHA256); matchErr != nil {
			return nil, matchErr
		}
		return &existing, nil
	} else if !errors.Is(getErr, storage.ErrNotFound) {
		return nil, getErr
	}
	route, err := c.store.GetRoute(ctx, routeID)
	if err != nil {
		return nil, err
	}
	if route.ServiceID != service.ID {
		return nil, storageNotFound("domain binding")
	}
	if route.Source != model.SourceAPI {
		return nil, invalid("validation.label_binding_read_only", "", "label-sourced domain bindings are read-only")
	}
	var operation *model.DeploymentOperation
	now := c.now()
	if service.Managed {
		if _, err = c.requireManagement(ctx, service.RuntimeID); err != nil {
			return nil, err
		}
		bindings, listErr := c.store.ListServiceDomainBindings(ctx, service.ID)
		if listErr != nil {
			return nil, listErr
		}
		activeOthers := 0
		for _, binding := range bindings {
			if binding.Route.ID != route.ID && binding.Route.Enabled {
				activeOthers++
			}
		}
		if activeOthers == 0 {
			value, operationErr := c.newOperation(id.New(id.Operation), service.ApplicationID, service.RuntimeID, service.ID, "",
				"DOMAIN_DETACH", idempotencyKey, map[string]any{"service_name": service.Name}, now)
			if operationErr != nil {
				return nil, operationErr
			}
			value.RequestSHA256 = requestSHA256
			operation = &value
		}
	}
	if err = c.store.DisableServiceDomainBinding(ctx, service.ID, route.ID, expected, now, operation); err != nil {
		if operation != nil && idempotencyKey != "" && errors.Is(err, storage.ErrConflict) {
			if existing, getErr := c.store.GetDeploymentOperationByIdempotency(ctx, service.RuntimeID, idempotencyKey); getErr == nil {
				if matchErr := validateIdempotentOperation(existing, "DOMAIN_DETACH", service.ApplicationID, service.ID, requestSHA256); matchErr != nil {
					return nil, matchErr
				}
				return &existing, nil
			}
		}
		return nil, err
	}
	return operation, nil
}

func (c *Core) domainBindingFromOperation(ctx context.Context, service model.Service, operation model.DeploymentOperation) (model.DomainBinding, error) {
	if len(operation.EncryptedInput) == 0 {
		return model.DomainBinding{}, fmt.Errorf("idempotent domain operation has no binding input")
	}
	plaintext, _, err := c.keyring.Decrypt(operation.EncryptedInput, operationAAD(operation.ID))
	if err != nil {
		return model.DomainBinding{}, err
	}
	var input struct {
		RouteID string `json:"route_id"`
	}
	if err = json.Unmarshal(plaintext, &input); err != nil {
		return model.DomainBinding{}, err
	}
	if input.RouteID == "" {
		return model.DomainBinding{}, fmt.Errorf("idempotent domain operation has no route id")
	}
	route, err := c.store.GetRoute(ctx, input.RouteID)
	if err != nil {
		return model.DomainBinding{}, err
	}
	if route.ServiceID != service.ID {
		return model.DomainBinding{}, storage.ErrConflict
	}
	domain, err := c.store.GetDomain(ctx, route.DomainID)
	if err != nil {
		return model.DomainBinding{}, err
	}
	binding := model.DomainBinding{ID: route.ID, Service: service, Domain: domain, Route: route,
		Source: route.Source, Status: route.Status, Editable: route.Source == model.SourceAPI}
	return c.withDNSTargets(ctx, binding), nil
}

func (c *Core) withDNSTargets(ctx context.Context, binding model.DomainBinding) model.DomainBinding {
	// DNS hints are supplementary. A transient read failure must not make a
	// successfully committed binding look like the mutation itself failed.
	binding.DNSTargets, _ = c.store.ListGatewayDNSTargets(ctx, binding.Domain.PoolID)
	return binding
}

func storageNotFound(resource string) error {
	return fmt.Errorf("%w: %s", storage.ErrNotFound, resource)
}

func routingIssuesAreOnlyMissingPort(issues []string) bool {
	if len(issues) == 0 {
		return false
	}
	for _, issue := range issues {
		value := strings.ToLower(issue)
		if !strings.Contains(value, "port") || (!strings.Contains(value, "required") && !strings.Contains(value, "exactly one")) {
			return false
		}
	}
	return true
}

// RetryDomainBinding changes only this service's desired network attachment.
// It never queues a whole-application redeployment.
func (c *Core) RetryDomainBinding(ctx context.Context, serviceID, routeID, idem string, expected int64) (model.DeploymentOperation, error) {
	return c.retryDomainBinding(ctx, serviceID, routeID, idem, expected, nil)
}

func (c *Core) retryDomainBinding(ctx context.Context, serviceID, routeID, idem string, expected int64, publication *entrypointPublication) (model.DeploymentOperation, error) {
	service, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	if !service.Managed || service.ApplicationID == "" {
		return model.DeploymentOperation{}, storage.ErrConflict
	}
	if err = c.store.CheckProjectResource(ctx, service.ID); err != nil {
		return model.DeploymentOperation{}, err
	}
	if _, err = c.requireManagement(ctx, service.RuntimeID); err != nil {
		return model.DeploymentOperation{}, err
	}
	route, err := c.store.GetRoute(ctx, routeID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	if route.ServiceID != service.ID {
		return model.DeploymentOperation{}, storage.ErrNotFound
	}
	digest, err := operationRequestSHA256("DOMAIN_ATTACH", struct {
		Service, Route string
		Revision       int64
	}{serviceID, routeID, expected})
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	if existing, lookupErr := c.store.GetDeploymentOperationByIdempotency(ctx, service.RuntimeID, idem); lookupErr == nil {
		if err = validateIdempotentOperation(existing, "DOMAIN_ATTACH", service.ApplicationID, service.ID, digest); err != nil {
			return model.DeploymentOperation{}, err
		}
		return existing, nil
	} else if !errors.Is(lookupErr, storage.ErrNotFound) {
		return model.DeploymentOperation{}, lookupErr
	}
	if route.Revision != expected || route.Status != model.RouteError {
		return model.DeploymentOperation{}, storage.ErrPrecondition
	}
	op, err := c.domainAttachOperation(ctx, service, route.ID, idem, publication)
	if err != nil {
		return op, err
	}
	op.RequestSHA256 = digest
	if publication != nil {
		write, writeErr := c.entrypointSubmissionWrite(*publication, service.ID, route.ID, op.ID)
		if writeErr != nil {
			return op, writeErr
		}
		if err = c.store.CreateDeploymentOperationWithReceipt(ctx, &op, write, publication.App.Revision); err != nil {
			return model.DeploymentOperation{}, err
		}
		return op, nil
	}
	return c.store.CreateDeploymentOperation(ctx, &op)
}

func (c *Core) domainAttachOperation(ctx context.Context, service model.Service, routeID, idem string, publication *entrypointPublication) (model.DeploymentOperation, error) {
	app, err := c.store.GetApplication(ctx, service.ApplicationID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	input := map[string]any{"service_name": service.ServiceKey, "service_key": service.ServiceKey, "runtime_object_id": service.RuntimeObjectID, "route_id": routeID}
	if app.ActiveRevisionID != "" {
		revision, readErr := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
		if readErr != nil {
			return model.DeploymentOperation{}, readErr
		}
		input["service_revision_id"] = serviceRevisionHead(revision, applicationServiceNameForPolicy(service, app))
	}
	if publication != nil {
		if app.Revision != publication.App.Revision {
			return model.DeploymentOperation{}, storage.ErrPrecondition
		}
		input["entrypoint_intent_id"], input["entrypoint"] = publication.Intent.ID, publication.Intent.Entry
	}
	return c.newOperation(id.New(id.Operation), service.ApplicationID, service.RuntimeID, service.ID, app.ActiveRevisionID, "DOMAIN_ATTACH", idem, input, c.now())
}
