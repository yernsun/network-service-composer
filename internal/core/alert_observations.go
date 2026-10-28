package core

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func notificationIssue(event string) (string, string) {
	switch {
	case strings.HasPrefix(event, "deployment.target_"):
		return "deployment_target_lost", "application"
	case strings.HasPrefix(event, "certificate."):
		return "certificate_risk", "certificate"
	case strings.HasPrefix(event, "gateway."):
		return "gateway_offline", "gateway"
	case strings.HasPrefix(event, "runtime."):
		return "runtime_unavailable", "runtime"
	}
	return "", ""
}

func alertLink(kind, resource string) string {
	return "/operations/activity?resource_type=" + url.QueryEscape(kind) + "&resource_id=" + url.QueryEscape(resource)
}

func certificateCondition(cert model.Certificate, now time.Time) (bool, bool) {
	bad := cert.NotBefore != nil && cert.NotBefore.After(now) || cert.LastError != "" || cert.Status == model.CertificateError || cert.Status == model.CertificateExpired || cert.Status == model.CertificateRevoked || cert.NotAfter != nil && cert.NotAfter.Before(now.Add(30*24*time.Hour))
	return bad, bad || cert.NotBefore != nil && cert.NotAfter != nil && (cert.Status == model.CertificateActive || cert.Status == model.CertificateIssued)
}

func (c *Core) gatewayCondition(g model.Gateway) (*bool, string) {
	bad := g.Status == model.GatewayOffline
	if !g.Enabled {
		return nil, "UNMONITORED"
	}
	if bad {
		return &bad, "OFFLINE"
	}
	if g.Status == model.GatewayReady && readinessFresh(c.now(), g.LastSeenAt, c.config.Controller.Gateway.OfflineTimeout.Std()) {
		return &bad, "CONFIRMED"
	}
	return nil, "UNKNOWN"
}

func (c *Core) runtimeCondition(runtime model.Runtime) (string, bool, bool) {
	staleAfter := max(3*c.config.Controller.Discovery.FullReconcile.Interval.Std(), c.config.Controller.Discovery.StaleEndpointTimeout.Std())
	if !runtime.Enabled || runtime.Status == model.RuntimeInitializing && runtime.LastError == "" && c.now().Sub(runtime.CreatedAt) < staleAfter {
		return "UNMONITORED", false, false
	}
	if runtime.Status == model.RuntimeOffline {
		return "OFFLINE", true, true
	}
	if runtime.LastError != "" {
		return "ERROR", true, true
	}
	if runtime.Status == model.RuntimeDegraded {
		return "DEGRADED", true, true
	}
	if runtime.Status == model.RuntimeOnline && runtime.LastSeenAt != nil && !runtime.LastSeenAt.After(c.now()) {
		if c.now().Sub(*runtime.LastSeenAt) < staleAfter {
			return "ONLINE", false, true
		}
		return "STALE", true, true
	}
	return "UNKNOWN", true, true
}

// Runtime issues are collected even when outbound notifications are disabled.
func (c *Core) refreshRuntimeIssues(ctx context.Context) error {
	for page := (storage.Page{Limit: 200}); ; {
		runtimes, err := c.store.ListRuntimes(ctx, page)
		if err != nil {
			return err
		}
		for _, rt := range runtimes {
			state, bad, monitored := c.runtimeCondition(rt)
			condition := &bad
			if !monitored {
				condition = nil
			}
			if err := c.observeIssueState(ctx, "runtime_unavailable", "runtime", rt.ID, alertLink("runtime", rt.ID), condition, state, ""); err != nil {
				return err
			}
		}
		if len(runtimes) < page.Limit {
			return nil
		}
		page.After = runtimes[len(runtimes)-1].ID
	}
}

// Absence is actionable only after a complete, current inventory epoch. Stable
// managed claims survive absence and must never be confused with a stopped app.
func (c *Core) refreshTargetIssues(ctx context.Context) error {
	for page := (storage.Page{Limit: 200}); ; {
		apps, err := c.store.ListApplications(ctx, "", page)
		if err != nil {
			return err
		}
		for _, app := range apps {
			if !app.Managed || app.System {
				continue
			}
			var condition *bool
			state := "UNKNOWN"
			operationID := ""
			if app.Status == model.ApplicationRunning && app.ActiveRevisionID != "" && app.ActiveRevisionID == app.DesiredRevisionID {
				rt, err := c.store.GetRuntime(ctx, app.RuntimeID)
				if err != nil {
					return err
				}
				presence, _, err := readControl[storage.RuntimePresence](ctx, c.store, storage.RuntimePresenceID(rt.ID), "runtime-presence")
				if err != nil && !errors.Is(err, storage.ErrNotFound) {
					return err
				}
				if err == nil && rt.Enabled && rt.Status == model.RuntimeOnline && rt.ObservationEpoch == rt.AppliedObservation && presence.RuntimeID == rt.ID && presence.Observation > 0 && presence.Observation == rt.AppliedObservation && readinessFresh(c.now(), rt.LastSeenAt, c.config.Controller.Discovery.StaleEndpointTimeout.Std()) && presence.ObservedAt.Equal(*rt.LastSeenAt) {
					busy, relatedOperation, err := c.alertApplicationOperation(ctx, app)
					if err != nil {
						return err
					}
					operationID = relatedOperation
					if !busy {
						services, err := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
						if err != nil {
							return err
						}
						condition, err = c.alertMissingTarget(ctx, app, rt, services, presence)
						if err != nil {
							return err
						}
						if condition != nil {
							state = "CONFIRMED"
						}
					}
				}
			} else if app.Status == model.ApplicationStopped || app.Status == model.ApplicationDown {
				state = "UNMONITORED"
			}
			if err := c.observeIssueState(ctx, "deployment_target_lost", "application", app.ID, alertLink("application", app.ID), condition, state, operationID); err != nil {
				return err
			}
		}
		if len(apps) < page.Limit {
			return nil
		}
		page.After = apps[len(apps)-1].ID
	}
}

func (c *Core) alertMissingTarget(ctx context.Context, app model.Application, runtime model.Runtime, services []model.Service, presence storage.RuntimePresence) (*bool, error) {
	revision, err := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
	if err != nil {
		return nil, err
	}
	// Reuse the readiness intent parser for every managed service. Native
	// inventory counters cannot prove an absent object's configured replica count.
	runtime.Type = model.RuntimeDocker
	targets := c.readinessReplicaTargets(model.DeploymentOperation{Type: "OBSERVE"}, app, revision, runtime, services)
	missing, required := false, false
	for _, service := range services {
		if !service.Managed {
			continue
		}
		name := applicationServiceNameForPolicy(service, app)
		if revision.ConfigurationSource.ServiceRevisions != nil && readinessServiceHead(revision, name) == "" {
			continue
		}
		count := targets[service.ID]
		if count == nil {
			return nil, nil
		}
		required = true
		if *count > 0 && !slices.Contains(presence.ServiceIDs, service.ID) {
			missing = true
		}
	}
	if !required {
		return nil, nil
	}
	return &missing, nil
}

func (c *Core) alertApplicationOperation(ctx context.Context, application model.Application) (bool, string, error) {
	var latest model.DeploymentOperation
	for page := (storage.Page{Limit: 200}); ; {
		operations, err := c.store.ListDeploymentOperations(ctx, application.ID, page)
		if err != nil {
			return false, "", err
		}
		for _, operation := range operations {
			if operation.Status == model.OperationQueued || operation.Status == model.OperationRunning {
				return true, operation.ID, nil
			}
			if operation.Status == model.OperationSucceeded && operation.RevisionID == application.ActiveRevisionID && (latest.ID == "" || operation.CreatedAt.After(latest.CreatedAt)) {
				latest = operation
			}
		}
		if len(operations) < page.Limit {
			return false, latest.ID, nil
		}
		page.After = operations[len(operations)-1].ID
	}
}
