package core

import (
	"context"
	"errors"
	"slices"

	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// These independent prerequisites survive missing workload baselines. Neither a
// configured provider nor historical delivery proves a current external service.
func (c *Core) environmentDependencies(ctx context.Context, env Environment, result *EnvironmentCapabilities) error {
	result.Certificates = Capability{State: "UNKNOWN", Reason: "certificate_binding_required"}
	result.ACME = Capability{State: "BLOCKED", Reason: "acme_configuration_required"}
	if c.config.ACME.AcceptTerms && c.config.ACME.Email != "" && c.config.ACME.DirectoryURL != "" {
		result.ACME = Capability{State: "CONFIGURED", Reason: "acme_external_validation_required"}
	}
	result.Notifications = Capability{State: "BLOCKED", Reason: "notification_route_required"}
	result.HA = Capability{State: "UNKNOWN", Reason: "ha_evidence_required"}
	rt, err := c.store.GetRuntime(ctx, env.RuntimeID)
	if err != nil {
		return err
	}
	if c.HAController != nil && c.HAReports != nil && c.HAController(ctx).State == "READY" {
		v := execution.Verifier{Store: c.store, Config: c.config, Reports: c.HAReports, CheckOwner: c.HAReports.CheckOwner}
		if selected, err := v.Select(ctx, rt); err == nil {
			result.HA = Capability{State: "CONFIGURED", Reason: "ha_external_dependencies_unverified", ObservedAt: &selected.ObservedAt}
		}
	}
	domains, err := c.environmentCertificateDomains(ctx, env)
	if err != nil {
		return err
	}
	if len(domains) > 0 {
		now := c.now()
		result.Certificates = Capability{State: "AVAILABLE", Reason: "certificate_current", ObservedAt: &now}
		for _, domain := range domains {
			if domain.TLSMode == model.TLSManaged && domain.ACMEChallenge == model.ACMEDNS01 {
				if _, err := c.resolveDNSProfile(ctx, domain.DNSProviderRef); err != nil {
					result.ACME = Capability{State: "BLOCKED", Reason: "acme_provider_unavailable"}
				}
			}
			if domain.CertificateID == "" {
				result.Certificates.State, result.Certificates.Reason = "BLOCKED", "certificate_pending"
				continue
			}
			cert, err := c.store.GetCertificate(ctx, domain.CertificateID)
			if errors.Is(err, storage.ErrNotFound) {
				result.Certificates.State, result.Certificates.Reason = "UNKNOWN", "certificate_evidence_missing"
				continue
			}
			if err != nil {
				return err
			}
			bad, known := certificateCondition(cert, now)
			if !known {
				result.Certificates.State, result.Certificates.Reason = "UNKNOWN", "certificate_evidence_missing"
			} else if bad {
				result.Certificates.State, result.Certificates.Reason = "BLOCKED", "certificate_attention_required"
			}
			if cert.NotBefore != nil && !cert.NotBefore.After(now) && (cert.Status == model.CertificateActive || cert.Status == model.CertificateIssued) {
				if result.Certificates.LastSucceededAt == nil || cert.NotBefore.After(*result.Certificates.LastSucceededAt) {
					at := *cert.NotBefore
					result.Certificates.LastSucceededAt = &at
				}
			}
		}
	}
	// Evaluate the same persisted route resolver used by delivery. Return only an
	// aggregate; receiver names, destinations and credentials remain admin-only.
routes:
	for _, kind := range []string{"image.available", "deployment.failed", "certificate.problem", "gateway.offline", "runtime.problem"} {
		for _, severity := range []string{"info", "warning", "critical"} {
			matches, err := c.matchNotificationRoutes(ctx, NotificationEvent{Type: kind, Severity: severity, Scopes: []AlertScope{{ProjectID: env.ProjectID, EnvironmentID: env.ID}}})
			if err != nil {
				return err
			}
			if len(matches) > 0 {
				result.Notifications = Capability{State: "CONFIGURED", Reason: "notification_delivery_unverified"}
				break routes
			}
		}
	}
	for page := (storage.Page{Limit: 200}); ; {
		deliveries, err := c.ListNotificationsFiltered(ctx, page, NotificationFilter{EnvironmentID: env.ID, Status: "DELIVERED"})
		if err != nil {
			return err
		}
		for _, delivery := range deliveries {
			if delivery.DeliveredAt != nil && (result.Notifications.LastSucceededAt == nil || delivery.DeliveredAt.After(*result.Notifications.LastSucceededAt)) {
				at := *delivery.DeliveredAt
				result.Notifications.LastSucceededAt = &at
			}
		}
		if len(deliveries) < page.Limit {
			break
		}
		page.After = deliveries[len(deliveries)-1].ID
	}
	if result.Notifications.State == "BLOCKED" && result.Notifications.LastSucceededAt != nil {
		result.Notifications.State = "HISTORICAL"
	}
	return nil
}

func (c *Core) environmentCertificateDomains(ctx context.Context, env Environment) ([]model.Domain, error) {
	result := []model.Domain{}
	if env.ApplicationID == "" {
		return result, nil
	}
	seen := map[string]bool{}
	for page := (storage.Page{Limit: 200}); ; {
		rows, err := c.store.ListRoutes(ctx, page)
		if err != nil {
			return nil, err
		}
		for _, route := range rows {
			if route.ServiceID == "" || seen[route.DomainID] {
				continue
			}
			service, err := c.store.GetService(ctx, route.ServiceID)
			if err != nil {
				return nil, err
			}
			if service.ApplicationID != env.ApplicationID {
				continue
			}
			domain, err := c.store.GetDomain(ctx, route.DomainID)
			if err != nil {
				return nil, err
			}
			seen[domain.ID] = true
			if slices.Contains([]model.TLSMode{model.TLSManaged, model.TLSCustom}, domain.TLSMode) {
				result = append(result, domain)
			}
		}
		if len(rows) < page.Limit {
			return result, nil
		}
		page.After = rows[len(rows)-1].ID
	}
}
