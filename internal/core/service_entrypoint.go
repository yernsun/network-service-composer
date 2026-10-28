package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"maps"
	"slices"
	"time"
)

func (c *Core) validateServiceEntrypoint(ctx context.Context, env Environment, runtime model.Runtime, entry *model.ServiceEntrypoint, names []string) error {
	if entry == nil {
		return nil
	}
	if !slices.Contains(names, entry.Service) {
		return controlInvalid("entrypoint.service", "entrypoint must target a submitted service")
	}
	gateway, err := c.store.GetGateway(ctx, entry.GatewayID)
	if err != nil {
		return err
	}
	if gateway.PoolID != runtime.PoolID || !gateway.Enabled || gateway.Status != model.GatewayReady || len(env.GatewayIDs) > 0 && !slices.Contains(env.GatewayIDs, gateway.ID) {
		return controlInvalid("entrypoint.gateway_id", "gateway is unavailable or outside the environment's allowed Pool")
	}
	entry.Host, err = NormalizeHost(entry.Host)
	if err != nil {
		return err
	}
	if entry.Port < 1 || entry.Port > 65535 {
		return controlInvalid("entrypoint.port", "a backend port is required")
	}
	_, _, _, err = c.prepareDomainBinding(ctx, model.Service{ID: "svc_review", RuntimeID: runtime.ID, DiscoveredProtocol: "http", DiscoveredPort: entry.Port}, runtime, entrypointBinding(entry), model.RoutePending, c.now())
	return err
}
func entrypointBinding(entry *model.ServiceEntrypoint) CreateDomainBindingInput {
	domain := &DomainConfigInput{Host: entry.Host, EntryPoints: []string{"http"}}
	if entry.CertificateID != "" {
		domain.EntryPoints = []string{"http", "https"}
		domain.RedirectHTTPToHTTPS = true
		domain.TLS = DomainTLSInput{Mode: model.TLSCustom, CertificateID: entry.CertificateID}
	}
	return CreateDomainBindingInput{Domain: domain, Route: RouteConfigInput{Path: "/", Backend: RouteBackendInput{Port: entry.Port, Protocol: "http"}}}
}

// Reconcile the active service intent independently of the operation that first
// submitted it. A failed INSTALL followed by REDEPLOY keeps the same intent.
func (c *Core) serviceEntrypointTick(ctx context.Context, env Environment) {
	if env.ApplicationID == "" || c.config.Controller.RecoveryMode {
		return
	}
	recovery := c.observeReadinessRecovery(ctx, "")
	if recovery.Held || !recovery.Available {
		return
	}
	app, err := c.store.GetApplication(ctx, env.ApplicationID)
	if err != nil || !app.Managed || app.System || app.RuntimeID != env.RuntimeID || app.ActiveRevisionID == "" || app.Status == model.ApplicationStopped || app.Status == model.ApplicationDown || app.Status == model.ApplicationFailed {
		return
	}
	revision, err := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
	if err != nil {
		return
	}
	intents, err := c.revisionEntrypointIntents(ctx, revision)
	if err != nil {
		return
	}
	services, err := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
	if err != nil {
		return
	}
	for _, name := range slices.Sorted(maps.Keys(intents)) {
		intent := intents[name]
		for _, service := range services {
			if applicationServiceNameForPolicy(service, app) != name || service.Status == model.ServiceRemoved || !service.Managed {
				continue
			}
			if err = c.reconcileServiceEntrypoint(ctx, env, app, revision, service, intent, recovery); err != nil {
				return
			}
		}
	}
}

func (c *Core) saveEntrypointReceipt(ctx context.Context, app model.Application, intent model.ServiceEntrypointIntent, status ServiceEntrypointStatus, expected int64) error {
	status.IntentID = intent.ID
	write, err := c.controlWrite(intent.ID, entrypointIntentKind, app.ID, status, expected)
	if err != nil {
		return err
	}
	return c.store.WriteApplicationDocuments(ctx, app.ID, app.Revision, []storage.ControlWrite{write})
}

func (c *Core) reconcileServiceEntrypoint(ctx context.Context, env Environment, app model.Application, revision model.ApplicationRevision, service model.Service, intent model.ServiceEntrypointIntent, recovery readinessRecovery) error {
	saved, expected, err := c.readEntrypointReceipt(ctx, app.ID, intent)
	if err != nil {
		return err
	}
	if expected == 0 {
		if saved.Status == "" {
			saved.Status = "PENDING"
		}
		if err = c.saveEntrypointReceipt(ctx, app, intent, saved, 0); err != nil {
			return err
		}
		expected = 1
	}
	if saved.Status == "SUBMITTED" || saved.Status == "SUPERSEDED" || saved.Attempts >= 3 || saved.NextAttempt.After(c.now()) || service.RuntimeReadyInstances == 0 {
		return nil
	}
	runtime, err := c.store.GetRuntime(ctx, env.RuntimeID)
	if err != nil {
		return err
	}
	entry := intent.Entry
	fail := func(code string) error {
		return c.saveEntrypointReceipt(ctx, app, intent, ServiceEntrypointStatus{Status: "FAILED", Attempts: saved.Attempts + 1, ErrorCode: code, NextAttempt: c.now().Add(time.Minute)}, expected)
	}
	if err = c.validateServiceEntrypoint(ctx, env, runtime, &entry, []string{entry.Service}); err != nil {
		return fail("entrypoint_validation_failed")
	}
	if !c.entrypointWorkloadFresh(ctx, runtime, service, recovery) {
		return nil
	}
	confirmed, err := c.entrypointExecutionConfirmed(ctx, app.ID, revision.ID, serviceRevisionHead(revision, entry.Service))
	if err != nil {
		return err
	}
	if !confirmed {
		return nil
	}
	bindings, err := c.store.ListServiceDomainBindings(ctx, service.ID)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if binding.Domain.Host != entry.Host {
			continue
		}
		if !entrypointBindingMatches(binding, runtime.PoolID, intent.Entry) {
			return fail("entrypoint_binding_conflict")
		}
		// Existing active routes need no network mutation. PENDING/ERROR routes must
		// retain their original child operation, not become a fabricated success.
		previous, taskErr := c.entrypointBindingOperation(ctx, app.ID, binding.ID)
		if taskErr != nil {
			return taskErr
		}
		if binding.Route.Status != model.RouteActive && previous == "" {
			return fail("entrypoint_binding_conflict")
		}
		return c.saveEntrypointReceipt(ctx, app, intent, ServiceEntrypointStatus{Status: "SUBMITTED", Attempts: saved.Attempts + 1, BindingID: binding.ID, ServiceID: service.ID, OperationID: previous}, expected)
	}
	input := entrypointBinding(&entry)
	input.IdempotencyKey = "entrypoint:" + intent.ID
	input.publication = &entrypointPublication{App: app, Intent: intent, Expected: expected, Attempts: saved.Attempts + 1}
	_, _, err = c.CreateDomainBinding(ctx, service.ID, input)
	if errors.Is(err, storage.ErrPrecondition) {
		return err
	}
	if err != nil {
		return fail("entrypoint_binding_failed")
	}
	return nil
}

func (c *Core) entrypointWorkloadFresh(ctx context.Context, runtime model.Runtime, service model.Service, recovery readinessRecovery) bool {
	now := c.now()
	ttl := c.config.Controller.Discovery.StaleEndpointTimeout.Std()
	if !runtime.Enabled || runtime.Status != model.RuntimeOnline || runtime.ObservationEpoch != runtime.AppliedObservation || !readinessFresh(now, runtime.LastSeenAt, ttl) || !readinessFresh(now, service.LastSeenAt, ttl) || !readinessAfterCutover(recovery.Cutover, runtime.LastSeenAt) || !readinessAfterCutover(recovery.Cutover, service.LastSeenAt) || service.RuntimeObjectID == "" {
		return false
	}
	presence, _, err := readControl[storage.RuntimePresence](ctx, c.store, storage.RuntimePresenceID(runtime.ID), "runtime-presence")
	return err == nil && presence.RuntimeID == runtime.ID && presence.Observation == runtime.AppliedObservation && presence.ObservedAt.Equal(*runtime.LastSeenAt) && slices.Contains(presence.ServiceIDs, service.ID)
}

func entrypointBindingMatches(binding model.DomainBinding, pool string, entry model.ServiceEntrypoint) bool {
	domain, route := binding.Domain, binding.Route
	return domain.PoolID == pool && domain.Source == model.SourceAPI && domain.Host == entry.Host && domain.Enabled && domain.HTTPEnabled && domain.HTTPSEnabled == (entry.CertificateID != "") && domain.CertificateID == entry.CertificateID && domain.HTTPSRedirect == (entry.CertificateID != "") && route.Source == model.SourceAPI && route.Enabled && route.Path == "/" && route.Kind == "proxy" && route.Priority == 0 && !route.StripPath && !route.WebsocketEnabled && route.TargetPort == entry.Port && route.UpstreamProtocol == "http" && route.UpstreamServerName == ""
}

func (c *Core) entrypointExecutionConfirmed(ctx context.Context, appID, activeID, serviceHead string) (bool, error) {
	for page := (storage.Page{Limit: 200}); ; {
		operations, err := c.store.ListDeploymentOperations(ctx, appID, page)
		if err != nil {
			return false, err
		}
		for _, op := range operations {
			if op.Status == model.OperationSucceeded && (op.RevisionID == activeID || op.RevisionID == serviceHead) && (op.Type == "INSTALL" || op.Type == "UPGRADE" || op.Type == "START" || op.Type == "REDEPLOY" || op.Type == "ADOPT") {
				return true, nil
			}
		}
		if len(operations) < page.Limit {
			return false, nil
		}
		page.After = operations[len(operations)-1].ID
	}
}

func (c *Core) entrypointBindingOperation(ctx context.Context, appID, routeID string) (string, error) {
	var latest model.DeploymentOperation
	for page := (storage.Page{Limit: 200}); ; {
		ops, err := c.store.ListDeploymentOperations(ctx, appID, page)
		if err != nil {
			return "", err
		}
		for _, op := range ops {
			if op.Type != "DOMAIN_ATTACH" {
				continue
			}
			raw, _, err := c.keyring.Decrypt(op.EncryptedInput, operationAAD(op.ID))
			if err != nil {
				return "", err
			}
			var input struct {
				RouteID string `json:"route_id"`
			}
			if err = json.Unmarshal(raw, &input); err != nil {
				return "", err
			}
			if input.RouteID == routeID && (latest.ID == "" || op.CreatedAt.After(latest.CreatedAt)) {
				latest = op
			}
		}
		if len(ops) < page.Limit {
			return latest.ID, nil
		}
		page.After = ops[len(ops)-1].ID
	}
}

func serviceRevisionHead(revision model.ApplicationRevision, name string) string {
	if head := revision.ConfigurationSource.ServiceRevisions[name]; head != "" {
		return head
	}
	return revision.ID
}

type entrypointPublication struct {
	App      model.Application
	Intent   model.ServiceEntrypointIntent
	Expected int64
	Attempts int
}

func (c *Core) entrypointSubmissionWrite(publication entrypointPublication, serviceID, routeID, operationID string) (storage.ControlWrite, error) {
	return c.controlWrite(publication.Intent.ID, entrypointIntentKind, publication.App.ID, ServiceEntrypointStatus{IntentID: publication.Intent.ID, Status: "SUBMITTED", Attempts: publication.Attempts, ServiceID: serviceID, BindingID: routeID, OperationID: operationID}, publication.Expected)
}

type ServiceEntrypointStatus struct {
	IntentID    string    `json:"intent_id,omitempty"`
	Status      string    `json:"status"`
	Attempts    int       `json:"attempts"`
	Revision    int64     `json:"revision"`
	ErrorCode   string    `json:"error_code,omitempty"`
	NextAttempt time.Time `json:"next_attempt,omitempty"`
	BindingID   string    `json:"binding_id,omitempty"`
	ServiceID   string    `json:"service_id,omitempty"`
	OperationID string    `json:"operation_id,omitempty"`
}

func (c *Core) ServiceEntrypointStatus(ctx context.Context, operation string) (ServiceEntrypointStatus, error) {
	op, err := c.store.GetDeploymentOperation(ctx, operation)
	if err != nil {
		return ServiceEntrypointStatus{}, err
	}
	intent, err := c.operationEntrypointIntent(ctx, op)
	if err != nil {
		return ServiceEntrypointStatus{}, err
	}
	app, err := c.store.GetApplication(ctx, op.ApplicationID)
	if err != nil {
		return ServiceEntrypointStatus{}, err
	}
	active, err := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
	if errors.Is(err, storage.ErrNotFound) && app.ActiveRevisionID == "" {
		return ServiceEntrypointStatus{Status: "PENDING", IntentID: intent.ID}, nil
	}
	if err != nil {
		return ServiceEntrypointStatus{}, err
	}
	current, err := c.revisionEntrypointIntents(ctx, active)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return ServiceEntrypointStatus{}, err
	}
	if current[intent.Entry.Service].ID != intent.ID {
		return ServiceEntrypointStatus{Status: "SUPERSEDED", IntentID: intent.ID}, nil
	}
	status, expected, err := c.readEntrypointReceipt(ctx, app.ID, intent)
	if err != nil {
		return status, err
	}
	status.IntentID, status.Revision = intent.ID, expected
	if status.Status == "" {
		status.Status = "PENDING"
	}
	if status.BindingID != "" {
		route, readErr := c.store.GetRoute(ctx, status.BindingID)
		if readErr != nil {
			return status, readErr
		}
		if route.ServiceID != status.ServiceID {
			return status, storage.ErrConflict
		}
		if route.Status == model.RouteDisabled {
			status.Status = "DISABLED"
			return status, nil
		}
	}
	if status.OperationID != "" {
		task, readErr := c.store.GetDeploymentOperation(ctx, status.OperationID)
		if readErr != nil {
			return status, readErr
		}
		if task.ApplicationID != app.ID || task.ServiceID != status.ServiceID || task.Type != "DOMAIN_ATTACH" {
			return status, storage.ErrConflict
		}
		status.Status, status.ErrorCode = string(task.Status), task.ErrorCode
	} else if status.Status == "SUBMITTED" {
		status.Status = "SUCCEEDED"
	}
	return status, nil
}

func (c *Core) RetryServiceEntrypoint(ctx context.Context, operation string, expected int64) (ServiceEntrypointStatus, error) {
	status, err := c.ServiceEntrypointStatus(ctx, operation)
	if err != nil {
		return status, err
	}
	if status.Revision != expected || status.Status != "FAILED" {
		return status, storage.ErrPrecondition
	}
	op, err := c.store.GetDeploymentOperation(ctx, operation)
	if err != nil {
		return status, err
	}
	intent, err := c.operationEntrypointIntent(ctx, op)
	if err != nil {
		return status, err
	}
	app, err := c.store.GetApplication(ctx, op.ApplicationID)
	if err != nil {
		return status, err
	}
	if err = c.store.CheckProjectResource(ctx, app.ID); err != nil {
		return status, err
	}
	if held, readErr := storage.RecoveryHeld(ctx, c.store, "deployment", op.ID); readErr != nil || held || c.config.Controller.RecoveryMode {
		return status, storage.ErrPrecondition
	}
	if status.OperationID != "" {
		route, readErr := c.store.GetRoute(ctx, status.BindingID)
		if readErr != nil {
			return status, readErr
		}
		task, retryErr := c.retryDomainBinding(ctx, status.ServiceID, status.BindingID, intent.ID+":retry:"+fmt.Sprint(expected), route.Revision, &entrypointPublication{App: app, Intent: intent, Expected: expected, Attempts: status.Attempts})
		if retryErr != nil {
			return status, retryErr
		}
		status.OperationID, status.Status = task.ID, "SUBMITTED"
		status.Revision = expected + 1
		return status, nil
	} else {
		status.Status, status.Attempts, status.ErrorCode, status.NextAttempt = "PENDING", 0, "", time.Time{}
	}
	if err = c.saveEntrypointReceipt(ctx, app, intent, status, expected); err != nil {
		return status, err
	}
	status.Revision = expected + 1
	return status, nil
}
