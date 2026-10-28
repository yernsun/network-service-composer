package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestConfiguredConsoleDomainPreservesSystemProtection(t *testing.T) {
	c, reg, objects := systemFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	svc := model.Service{ID: "console-service", RuntimeID: reg.RuntimeID, RuntimeObjectID: "controller-object", ServiceKey: "controller", ExternalID: "controller", Name: "Controller", DNSName: "nsc_controller", DNSStatus: model.DNSReady, RoutingEligible: true, DiscoveredPort: 8080, DiscoveredProtocol: "http", RunningInstances: 4, LastSeenAt: &now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := c.store.UpsertService(ctx, &svc); err != nil {
		t.Fatal(err)
	}
	project, err := c.EnsureGatewayProject(ctx, reg, reg.ClusterID, objects)
	if err != nil {
		t.Fatal(err)
	}
	defs := []config.SystemApplicationConfig{{Key: "controller", Name: "Controller", Environment: "prod", RuntimeID: reg.RuntimeID, ObjectIDs: []string{"controller-object"}}}
	if err = c.ReconcileGatewayApplications(ctx, project, reg.ClusterID, defs, map[string]map[string]bool{reg.RuntimeID: objects}); err != nil {
		t.Fatal(err)
	}
	if err = c.EnsureGatewayConsoleDomain(ctx, project, ""); err != nil {
		t.Fatal(err)
	}
	before, err := c.store.GetService(ctx, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = c.EnsureGatewayConsoleDomain(ctx, project, "console.example.test"); err != nil {
			t.Fatal(err)
		}
	}
	bindings, err := c.store.ListServiceDomainBindings(ctx, svc.ID)
	if err != nil || len(bindings) != 1 {
		t.Fatal(bindings, err)
	}
	b := bindings[0]
	if b.Route.ServiceID != svc.ID || b.Route.TargetPort != 8080 || !b.Route.WebsocketEnabled || b.Domain.Host != "console.example.test" {
		t.Fatal(b)
	}
	if _, _, err = c.CreateDomainBinding(ctx, svc.ID, CreateDomainBindingInput{DomainID: b.Domain.ID, Route: RouteConfigInput{Path: "/denied"}}); !errors.Is(err, storage.ErrSystemProtected) {
		t.Fatalf("API binding protection: %v", err)
	}
	if err = c.store.CheckProjectResource(ctx, svc.ID); !errors.Is(err, storage.ErrSystemProtected) {
		t.Fatal(err)
	}
	after, err := c.store.GetService(ctx, svc.ID)
	if err != nil || before.Revision != after.Revision || before.ApplicationID != after.ApplicationID {
		t.Fatal(after, err)
	}
	if err = c.EnsureGatewayConsoleDomain(ctx, project, "bad host"); err == nil {
		t.Fatal("invalid host accepted")
	}
	untrusted := project
	untrusted.System = false
	if err = c.EnsureGatewayConsoleDomain(ctx, untrusted, "console.example.test"); !errors.Is(err, storage.ErrSystemProtected) {
		t.Fatal(err)
	}
	input := customCertificateInputFixture(t, c, b.Domain)
	input.DomainIDs = nil
	cert, err := c.CreateCustomCertificate(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	c.config.Controller.SystemProject.ConsoleCertificateID = "missing-certificate"
	if err = c.EnsureGatewayConsoleDomain(ctx, project, b.Domain.Host); err == nil {
		t.Fatal("missing console certificate accepted")
	}
	unchanged, err := c.store.GetDomain(ctx, b.Domain.ID)
	if err != nil || unchanged.Revision != b.Domain.Revision || unchanged.HTTPSEnabled {
		t.Fatal("failed certificate selection changed domain", unchanged, err)
	}
	c.config.Controller.SystemProject.ConsoleCertificateID = cert.ID
	if err = c.EnsureGatewayConsoleDomain(ctx, project, b.Domain.Host); err != nil {
		t.Fatal(err)
	}
	tlsDomain, err := c.store.GetDomain(ctx, b.Domain.ID)
	if err != nil || !tlsDomain.HTTPSEnabled || !tlsDomain.HTTPEnabled || tlsDomain.TLSMode != model.TLSCustom || tlsDomain.CertificateID != cert.ID {
		t.Fatal(tlsDomain, err)
	}
	if err = c.EnsureGatewayConsoleDomain(ctx, project, b.Domain.Host); err != nil {
		t.Fatal(err)
	}
	again, err := c.store.GetDomain(ctx, b.Domain.ID)
	if err != nil || again.Revision != tlsDomain.Revision {
		t.Fatal("TLS reconciliation must be idempotent", again, err)
	}
	if _, err = c.UpdateDomain(ctx, tlsDomain, tlsDomain.Revision); !errors.Is(err, storage.ErrSystemProtected) {
		t.Fatalf("domain API protection: %v", err)
	}
	if err = c.EnsureGatewayConsoleDomain(ctx, project, "uncovered.example.test"); err == nil {
		t.Fatal("certificate for a different host accepted")
	}
	c.config.Controller.SystemProject.ConsoleCertificateID = ""
	if err = c.EnsureGatewayConsoleDomain(ctx, project, b.Domain.Host); err != nil {
		t.Fatal(err)
	}
	retained, err := c.store.GetDomain(ctx, b.Domain.ID)
	if err != nil || !retained.HTTPSEnabled || retained.Revision != tlsDomain.Revision {
		t.Fatal("omitting certificate must not disable HTTPS", retained, err)
	}
	b.Route.WebsocketEnabled = false
	if err = c.store.UpdateRoute(storage.WithSystemBootstrap(ctx), &b.Route, b.Route.Revision); err != nil {
		t.Fatal(err)
	}
	if err = c.EnsureGatewayConsoleDomain(ctx, project, "console.example.test"); err == nil {
		t.Fatal("existing route silently overwritten")
	}
	afterRoute, err := c.store.GetRoute(ctx, b.Route.ID)
	if err != nil || afterRoute.WebsocketEnabled {
		t.Fatal(afterRoute, err)
	}
	svc = after
	svc.DNSStatus = model.DNSConflict
	if err = c.store.UpsertService(storage.WithSystemBootstrap(ctx), &svc); err != nil {
		t.Fatal(err)
	}
	if err = c.EnsureGatewayConsoleDomain(ctx, project, "other.example.test"); err == nil {
		t.Fatal("unready service accepted")
	}
}
