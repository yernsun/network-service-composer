package core

import (
	"context"
	"fmt"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// EnsureGatewayConsoleDomain is called only by the configured system reconciler
// after authenticated cluster/application reconciliation. It creates the explicit
// operator-selected console route without granting API writes to system resources.
// Existing routes are checked, never overwritten or removed. An explicitly
// configured certificate may update only the matching console domain's TLS.
func (c *Core) EnsureGatewayConsoleDomain(ctx context.Context, project Project, host string) error {
	if host == "" {
		return nil
	}
	host, err := NormalizeHost(host)
	if err != nil {
		return err
	}
	if !project.System {
		return storage.ErrSystemProtected
	}
	appID := "app_" + digestString(project.ID + ":controller")[:32]
	app, err := c.store.GetApplication(ctx, appID)
	if err != nil {
		return err
	}
	if !app.System || app.SystemKey != "controller" || app.Managed || app.Environment == nil || app.Environment.ProjectID != project.ID {
		return storage.ErrSystemProtected
	}
	services, err := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{RuntimeID: app.RuntimeID})
	if err != nil {
		return err
	}
	selected := []model.Service{}
	for _, svc := range services {
		if svc.ApplicationID == app.ID {
			selected = append(selected, svc)
		}
	}
	if len(selected) != 1 {
		return fmt.Errorf("console domain requires exactly one registered Controller service")
	}
	svc := selected[0]
	if svc.Managed || svc.DNSStatus != model.DNSReady || !svc.RoutingEligible || svc.EffectivePort() != 8080 || svc.EffectiveProtocol() != "http" {
		return fmt.Errorf("registered Controller is not ready for console routing")
	}
	certificateID := c.config.Controller.SystemProject.ConsoleCertificateID
	bindings, err := c.store.ListServiceDomainBindings(ctx, svc.ID)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if binding.Domain.Host != host {
			continue
		}
		if binding.Domain.Enabled && binding.Domain.HTTPEnabled && binding.Route.Enabled && binding.Route.Status == model.RouteActive && binding.Route.Path == "/" && binding.Route.TargetPort == 8080 && binding.Route.UpstreamProtocol == "http" && !binding.Route.StripPath && binding.Route.WebsocketEnabled {
			if certificateID != "" {
				if _, err := c.validateDomainCertificate(ctx, binding.Domain.PoolID, host, certificateID, model.TLSCustom); err != nil {
					return err
				}
				if binding.Domain.CertificateID != certificateID || !binding.Domain.HTTPSEnabled || binding.Domain.TLSMode != model.TLSCustom {
					domain := binding.Domain
					domain.CertificateID = certificateID
					domain.TLSMode = model.TLSCustom
					domain.HTTPSEnabled = true
					domain.ACMEChallenge = ""
					domain.DNSProviderRef = ""
					_, err := c.UpdateDomain(storage.WithSystemBootstrap(ctx), domain, domain.Revision)
					return err
				}
			}
			return nil
		}
		return fmt.Errorf("existing console domain binding differs from operator configuration")
	}
	domain := DomainConfigInput{Host: host, EntryPoints: []string{EntryPointHTTP}, TLS: DomainTLSInput{Mode: model.TLSDisabled}}
	if certificateID != "" {
		domain.EntryPoints = append(domain.EntryPoints, EntryPointHTTPS)
		domain.TLS = DomainTLSInput{Mode: model.TLSCustom, CertificateID: certificateID}
	}
	_, operation, err := c.CreateDomainBinding(storage.WithSystemBootstrap(ctx), svc.ID, CreateDomainBindingInput{Domain: &domain, Route: RouteConfigInput{Path: "/", WebsocketEnabled: true, Backend: RouteBackendInput{Port: 8080, Protocol: "http"}}})
	if err != nil {
		return err
	}
	if operation != nil {
		return fmt.Errorf("console routing unexpectedly required workload deployment")
	}
	return nil
}
