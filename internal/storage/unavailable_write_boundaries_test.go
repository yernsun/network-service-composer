package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func TestUnavailableDatabaseNeverReportsBusinessMutationSuccess(t *testing.T) {
	s := newSQLiteStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	pool := model.GatewayPool{ID: "pool", Name: "pool", Revision: 1, CreatedAt: now, UpdatedAt: now}
	runtime := model.Runtime{ID: "runtime", PoolID: pool.ID, Name: "runtime", Type: model.RuntimeDocker, Revision: 1}
	service := model.Service{ID: "service", RuntimeID: runtime.ID, ExternalID: "project/service", ServiceKey: "project/service", Revision: 1}
	domain := model.Domain{ID: "domain", PoolID: pool.ID, Host: "service.example.test", TLSMode: model.TLSDisabled, Revision: 1}
	route := model.Route{ID: "route", DomainID: domain.ID, ServiceID: service.ID, Path: "/", Revision: 1}
	certificate := model.Certificate{ID: "certificate", PoolID: pool.ID, PrimaryDomain: domain.Host, Domains: []string{domain.Host}, Provider: "custom", Revision: 1}
	app := model.Application{ID: "application", RuntimeID: runtime.ID, ProjectName: "project", Managed: true, Revision: 1}
	revision := model.ApplicationRevision{ID: "revision", ApplicationID: app.ID}
	op := model.DeploymentOperation{ID: "operation", ApplicationID: app.ID, RuntimeID: runtime.ID, WorkerID: "worker", Status: model.OperationQueued, Type: "INSTALL", Revision: 1}
	source := model.CatalogSource{ID: "source", Name: "source", Type: model.CatalogManifest, Revision: 1}
	version := model.CatalogAppVersion{ID: "version", SourceID: source.ID, AppKey: "web", Version: "1"}
	gateway := model.Gateway{ID: "gateway", PoolID: pool.ID, Revision: 1}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"create pool", func() error { return s.CreatePool(ctx, &pool) }}, {"update pool", func() error { return s.UpdatePool(ctx, &pool, 1) }}, {"delete pool", func() error { return s.DeletePool(ctx, pool.ID, 1) }},
		{"bump pool", func() error { _, err := s.BumpPoolRevision(ctx, pool.ID); return err }},
		{"create runtime", func() error { return s.CreateRuntime(ctx, &runtime) }}, {"update runtime", func() error { return s.UpdateRuntime(ctx, &runtime, 1) }}, {"delete runtime", func() error { return s.DeleteRuntime(ctx, runtime.ID, 1) }},
		{"observation", func() error { _, err := s.NextObservation(ctx, runtime.ID); return err }},
		{"apply discovery", func() error {
			_, _, err := s.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: runtime.ID, Observation: 1})
			return err
		}},
		{"sweep endpoints", func() error {
			_, _, err := s.SweepEndpointLifecycle(ctx, runtime.ID, 1, "retain", now, time.Second, time.Second, time.Hour)
			return err
		}},
		{"upsert service", func() error { return s.UpsertService(ctx, &service) }}, {"update service", func() error { return s.UpdateService(ctx, &service, 1) }},
		{"upsert endpoint", func() error { return s.UpsertEndpoint(ctx, &model.Endpoint{ID: "endpoint", ServiceID: service.ID}) }},
		{"probe result", func() error {
			_, err := s.UpdateServiceProbe(ctx, model.ProbeResult{PoolID: pool.ID, ServiceID: service.ID, Revision: 1})
			return err
		}},
		{"activate application", func() error {
			return s.ActivateApplicationRevision(ctx, runtime.ID, app.ID, revision.ID, revision.ID, []string{service.ServiceKey}, map[string]int{"service": 1}, "", now)
		}},
		{"create domain", func() error { return s.CreateDomain(ctx, &domain) }}, {"create managed domain", func() error {
			managedDomain, managedCertificate := domain, certificate
			managedDomain.TLSMode, managedCertificate.Provider = model.TLSManaged, "acme"
			return s.CreateManagedDomain(ctx, &managedDomain, &managedCertificate)
		}},
		{"update domain", func() error { return s.UpdateDomain(ctx, &domain, 1) }}, {"delete domain", func() error { return s.DeleteDomain(ctx, domain.ID, 1) }},
		{"create route", func() error { return s.CreateRoute(ctx, &route) }}, {"update route", func() error { return s.UpdateRoute(ctx, &route, 1) }}, {"delete route", func() error { return s.DeleteRoute(ctx, route.ID, 1) }},
		{"create binding", func() error { return s.CreateServiceDomainBinding(ctx, &domain, nil, &route, nil, nil, 0) }},
		{"update binding", func() error { return s.UpdateServiceDomainBinding(ctx, service.ID, &domain, nil, &route, 1, 1) }},
		{"disable binding", func() error { return s.DisableServiceDomainBinding(ctx, service.ID, route.ID, 1, now, nil) }},
		{"application routes", func() error { return s.SetApplicationRouteStatus(ctx, app.ID, model.RouteError, "failed", now) }},
		{"application route", func() error {
			return s.SetApplicationRouteStatusByID(ctx, app.ID, route.ID, model.RouteActive, "", now)
		}},
		{"complete detach", func() error { return s.CompleteApplicationDomainDetach(ctx, app.ID, service.ID, now) }},
		{"create catalog source", func() error { return s.CreateCatalogSource(ctx, &source) }}, {"update catalog source", func() error { return s.UpdateCatalogSource(ctx, &source, 1) }},
		{"upsert catalog version", func() error { return s.UpsertCatalogAppVersion(ctx, &version) }}, {"sync catalog", func() error { return s.SyncCatalogAppVersions(ctx, &source, []model.CatalogAppVersion{version}, 1) }},
		{"create application", func() error { return s.CreateApplication(ctx, &app, &revision, &op) }},
		{"install application", func() error {
			return s.CreateApplicationInstall(ctx, &app, &revision, []model.Service{service}, &domain, &certificate, &route, &op)
		}},
		{"update application", func() error { return s.UpdateApplication(ctx, &app, 1) }}, {"application status", func() error { return s.SetApplicationStatus(ctx, app.ID, model.ApplicationRunning, "", now) }},
		{"replica override", func() error { return s.SetApplicationReplicaOverride(ctx, app.ID, "service", 1, now) }},
		{"application revision", func() error { return s.CreateApplicationRevision(ctx, &revision, &op, 1, now) }},
		{"create deployment", func() error { _, err := s.CreateDeploymentOperation(ctx, &op); return err }},
		{"claim deployment", func() error { _, err := s.ClaimDeploymentOperation(ctx, "worker", now, now); return err }},
		{"deployment heartbeat", func() error { return s.HeartbeatDeploymentOperation(ctx, op.ID, "worker", now) }}, {"finish deployment", func() error { return s.FinishDeploymentOperation(ctx, &op, now) }},
		{"create certificate", func() error { return s.CreateCertificate(ctx, &certificate) }},
		{"provision managed certificate", func() error { _, err := s.ProvisionManagedCertificate(ctx, &certificate, domain.ID, now); return err }},
		{"install custom certificate", func() error { return s.InstallCustomCertificate(ctx, &certificate, []string{domain.ID}, now) }},
		{"update certificate", func() error { return s.UpdateCertificate(ctx, &certificate, 1) }}, {"activate certificate", func() error { return s.ActivateCertificate(ctx, &certificate, 1, now) }},
		{"distribute certificate", func() error { return s.SetCertificateDistributionRevision(ctx, certificate.ID, 1) }}, {"link certificate", func() error { return s.LinkCertificateDomains(ctx, certificate.ID, []string{domain.ID}) }},
		{"certificate domain status", func() error { return s.UpdateCertificateDomains(ctx, certificate.ID, model.DomainError, "failed", now) }},
		{"upsert gateway", func() error { return s.UpsertGateway(ctx, &gateway) }}, {"update gateway", func() error { return s.UpdateGateway(ctx, &gateway, 1) }},
		{"gateway heartbeat", func() error { return s.HeartbeatGateway(ctx, gateway.ID, 1, 1, now) }}, {"gateway ACK", func() error { return s.AckGateway(ctx, gateway.ID, 1, "model", "renderer", "rendered", "") }},
		{"select probe leader", func() error { _, err := s.SelectProbeLeader(ctx, pool.ID, time.Second); return err }},
		{"put challenge", func() error {
			return s.PutChallenge(ctx, Challenge{ID: "challenge", CertificateID: certificate.ID, Token: "token"})
		}},
		{"delete challenges", func() error { return s.DeleteChallenges(ctx, certificate.ID) }}, {"delete challenge token", func() error { return s.DeleteChallengeToken(ctx, certificate.ID, "token") }},
		{"ACME account", func() error {
			return s.UpsertACMEAccount(ctx, &model.ACMEAccount{ID: "account", DirectoryURL: "https://acme.example.test", Email: "admin@example.test"})
		}},
		{"record snapshot", func() error { return s.RecordSnapshot(ctx, pool.ID, 1, "digest", []byte("snapshot"), now) }}, {"audit event", func() error { return s.AppendAuditEvent(ctx, model.AuditEvent{ID: "event"}) }},
		{"garbage collection", func() error { _, err := s.GarbageCollect(ctx, now, time.Hour); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err == nil || !strings.Contains(err.Error(), "closed") {
				t.Fatalf("database outage was hidden: %v", err)
			}
		})
	}
}
