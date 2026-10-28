package core

import (
	"context"
	"errors"
	"testing"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func externalBindingFixture(t *testing.T) (*Core, model.Service, CreateDomainBindingInput) {
	t.Helper()
	c, runtime, _ := controlFixture(t)
	now := c.now()
	service := model.Service{ID: id.New(id.Service), RuntimeID: runtime.ID, ExternalID: "project/web", Name: "web", DNSName: "project_web", DNSStatus: model.DNSReady, RoutingEligible: true, DiscoveredEnabled: true, DiscoveredPort: 8080, DiscoveredProtocol: "http", Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := c.store.UpsertService(context.Background(), &service); err != nil {
		t.Fatal(err)
	}
	return c, service, CreateDomainBindingInput{Domain: &DomainConfigInput{Host: "app.example.test", EntryPoints: []string{EntryPointHTTP}, TLS: DomainTLSInput{Mode: model.TLSDisabled}}, Route: RouteConfigInput{Path: "/"}}
}

func TestExternalDomainBindingValidatesOwnershipAndTargetBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*model.Service, *CreateDomainBindingInput)
	}{
		{"missing DNS", func(s *model.Service, _ *CreateDomainBindingInput) {
			s.DNSStatus = model.DNSMissing
			s.DNSLastError = "not attached"
		}},
		{"conflicting DNS", func(s *model.Service, _ *CreateDomainBindingInput) {
			s.DNSStatus = model.DNSConflict
			s.RoutingIssues = []string{"alias conflict"}
		}},
		{"not eligible", func(s *model.Service, _ *CreateDomainBindingInput) { s.RoutingEligible = false }},
		{"missing port without override", func(s *model.Service, _ *CreateDomainBindingInput) {
			s.RoutingEligible = false
			s.RoutingIssues = []string{"exactly one port required"}
		}},
		{"disabled externally", func(s *model.Service, in *CreateDomainBindingInput) {
			disabled := false
			s.EnabledOverride = &disabled
			s.RoutingEligible = false
			s.RoutingIssues = []string{"port required"}
			in.Route.Backend.Port = 8080
		}},
		{"non port issue", func(s *model.Service, in *CreateDomainBindingInput) {
			s.RoutingEligible = false
			s.RoutingIssues = []string{"network required"}
			in.Route.Backend.Port = 8080
		}},
		{"ambiguous issue", func(s *model.Service, in *CreateDomainBindingInput) {
			s.RoutingEligible = false
			s.RoutingIssues = []string{"port blocked"}
			in.Route.Backend.Port = 8080
		}},
		{"no domain", func(_ *model.Service, in *CreateDomainBindingInput) { in.Domain = nil }},
		{"both domains", func(_ *model.Service, in *CreateDomainBindingInput) { in.DomainID = "other" }},
		{"wrong service", func(_ *model.Service, in *CreateDomainBindingInput) { in.Route.Backend.ServiceID = "other" }},
		{"missing effective port", func(s *model.Service, _ *CreateDomainBindingInput) { s.DiscoveredPort = 0 }},
		{"negative port", func(_ *model.Service, in *CreateDomainBindingInput) { in.Route.Backend.Port = -1 }},
		{"overflow port", func(_ *model.Service, in *CreateDomainBindingInput) { in.Route.Backend.Port = 65536 }},
		{"invalid path", func(_ *model.Service, in *CreateDomainBindingInput) { in.Route.Path = "/bad?query" }},
		{"unsupported protocol", func(_ *model.Service, in *CreateDomainBindingInput) { in.Route.Backend.Protocol = "grpc" }},
		{"invalid SNI", func(_ *model.Service, in *CreateDomainBindingInput) { in.Route.Backend.ServerName = "bad/name" }},
		{"invalid host", func(_ *model.Service, in *CreateDomainBindingInput) { in.Domain.Host = "bad/name" }},
		{"unknown domain", func(_ *model.Service, in *CreateDomainBindingInput) { in.Domain = nil; in.DomainID = "missing" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, service, input := externalBindingFixture(t)
			tc.mutate(&service, &input)
			ctx := context.Background()
			enabledOverride := service.EnabledOverride
			if err := c.store.UpsertService(ctx, &service); err != nil {
				t.Fatal(err)
			}
			if enabledOverride != nil {
				service.EnabledOverride = enabledOverride
				if err := c.store.UpdateService(ctx, &service, service.Revision); err != nil {
					t.Fatal(err)
				}
			}
			binding, op, err := c.CreateDomainBinding(ctx, service.ID, input)
			if err == nil || op != nil || binding.ID != "" {
				t.Fatalf("invalid binding accepted: %#v %#v %v", binding, op, err)
			}
			bindings, err := c.store.ListServiceDomainBindings(ctx, service.ID)
			if err != nil || len(bindings) != 0 {
				t.Fatal("failed validation left a binding")
			}
			domains, err := c.store.ListDomains(ctx, storage.Page{Limit: 100})
			if err != nil || len(domains) != 0 {
				t.Fatal("failed validation left a domain")
			}
		})
	}
}

func TestExternalBindingLifecyclePreservesWorkloadAndChecksRevisions(t *testing.T) {
	c, service, input := externalBindingFixture(t)
	ctx := context.Background()
	service.RoutingEligible = false
	service.DiscoveredPort = 0
	service.RoutingIssues = []string{"exactly one port required"}
	if err := c.store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	input.Route.Backend = RouteBackendInput{ServiceID: service.ID, Port: 8443, Protocol: " HTTPS ", ServerName: " BACKEND.EXAMPLE.TEST "}
	created, op, err := c.CreateDomainBinding(ctx, service.ID, input)
	if err != nil || op != nil || created.Route.Status != model.RouteActive || created.Route.UpstreamProtocol != "https" || created.Route.UpstreamServerName != "backend.example.test" {
		t.Fatalf("create %#v %#v %v", created, op, err)
	}
	stored, err := c.store.GetService(ctx, service.ID)
	if err != nil || stored.Managed || stored.TargetPortOverride == nil || *stored.TargetPortOverride != 8443 || stored.EnabledOverride == nil || !*stored.EnabledOverride {
		t.Fatalf("explicit port or ownership lost: %#v %v", stored, err)
	}
	if _, err := c.UpdateDomainBinding(ctx, service.ID, created.ID, UpdateDomainBindingInput{}, created.Route.Revision+1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale route update: %v", err)
	}
	priority := 42
	enabled := false
	yes := true
	port := 8081
	updated, err := c.UpdateDomainBinding(ctx, service.ID, created.ID, UpdateDomainBindingInput{Route: &RouteConfigPatch{Path: pointer("/api"), Priority: &priority, StripPath: &yes, WebsocketEnabled: &yes, Enabled: &enabled, Backend: &RouteBackendPatch{ServiceID: &service.ID, Port: &port, Protocol: pointer("HTTP"), ServerName: pointer("")}}}, created.Route.Revision)
	if err != nil || updated.Status != model.RoutePaused || updated.Route.Enabled || updated.Route.Path != "/api/" || updated.Route.Priority != 42 || !updated.Route.StripPath || !updated.Route.WebsocketEnabled || updated.Route.TargetPort != 8081 || updated.Route.UpstreamServerName != "" {
		t.Fatalf("update %#v %v", updated, err)
	}
	updated, err = c.UpdateDomainBinding(ctx, service.ID, created.ID, UpdateDomainBindingInput{Route: &RouteConfigPatch{Enabled: &yes, Backend: &RouteBackendPatch{ServerName: pointer("SNI.EXAMPLE.TEST")}}}, updated.Route.Revision)
	if err != nil || updated.Status != model.RouteActive || updated.Route.UpstreamServerName != "sni.example.test" {
		t.Fatalf("resume %#v %v", updated, err)
	}
	if _, err = c.DeleteDomainBinding(ctx, service.ID, created.ID, updated.Route.Revision+1, ""); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale delete: %v", err)
	}
	op, err = c.DeleteDomainBinding(ctx, service.ID, created.ID, updated.Route.Revision, "")
	if err != nil || op != nil {
		t.Fatalf("external detach queued workload operation: %#v %v", op, err)
	}
	route, err := c.store.GetRoute(ctx, created.ID)
	if err != nil || route.Enabled {
		t.Fatalf("detach did not disable route: %#v %v", route, err)
	}
	if _, err = c.store.GetService(ctx, service.ID); err != nil {
		t.Fatal("detach deleted external workload")
	}
}

func TestBindingPatchValidationDoesNotPartiallySaveRoute(t *testing.T) {
	c, service, input := externalBindingFixture(t)
	ctx := context.Background()
	created, _, err := c.CreateDomainBinding(ctx, service.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	overflow := 65536
	yes := true
	empty := []string{}
	https := []string{EntryPointHTTPS}
	for _, patch := range []UpdateDomainBindingInput{
		{Route: &RouteConfigPatch{Path: pointer("/bad?query")}},
		{Route: &RouteConfigPatch{Backend: &RouteBackendPatch{ServiceID: pointer("other")}}},
		{Route: &RouteConfigPatch{Backend: &RouteBackendPatch{Port: &zero}}}, {Route: &RouteConfigPatch{Backend: &RouteBackendPatch{Port: &overflow}}},
		{Route: &RouteConfigPatch{Backend: &RouteBackendPatch{Protocol: pointer("tcp")}}}, {Route: &RouteConfigPatch{Backend: &RouteBackendPatch{ServerName: pointer("bad/name")}}},
		{Domain: &DomainConfigPatch{}},
		{DomainRevision: created.Domain.Revision, Domain: &DomainConfigPatch{Host: pointer("bad/name")}},
		{DomainRevision: created.Domain.Revision, Domain: &DomainConfigPatch{EntryPoints: &empty}},
		{DomainRevision: created.Domain.Revision, Domain: &DomainConfigPatch{EntryPoints: &https}},
		{DomainRevision: created.Domain.Revision, Domain: &DomainConfigPatch{RedirectHTTPToHTTPS: &yes}},
		{DomainRevision: created.Domain.Revision, Domain: &DomainConfigPatch{HTTP3Enabled: &yes}},
		{DomainRevision: created.Domain.Revision, Domain: &DomainConfigPatch{TLS: &DomainTLSInput{Mode: "invalid"}}},
		{DomainRevision: created.Domain.Revision, Domain: &DomainConfigPatch{TLS: &DomainTLSInput{Mode: model.TLSCustom}}},
		{DomainRevision: created.Domain.Revision, Domain: &DomainConfigPatch{EntryPoints: &https, TLS: &DomainTLSInput{Mode: model.TLSCustom}}},
	} {
		if _, err := c.UpdateDomainBinding(ctx, service.ID, created.ID, patch, created.Route.Revision); err == nil {
			t.Fatalf("invalid patch accepted: %#v", patch)
		}
		route, err := c.store.GetRoute(ctx, created.ID)
		if err != nil || route.Revision != created.Route.Revision || route.Path != "/" {
			t.Fatalf("failed patch changed route: %#v %v", route, err)
		}
	}
}

func TestSharedDomainTLSChangesAreAtomicAndPreserveOtherBindings(t *testing.T) {
	c, service, input := externalBindingFixture(t)
	ctx := context.Background()
	first, _, err := c.CreateDomainBinding(ctx, service.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	second, op, err := c.CreateDomainBinding(ctx, service.ID, CreateDomainBindingInput{DomainID: first.Domain.ID, Route: RouteConfigInput{Path: "/second"}})
	if err != nil || op != nil || second.Domain.ID != first.Domain.ID {
		t.Fatalf("domain reuse: %#v %v", second, err)
	}
	both := []string{EntryPointHTTP, EntryPointHTTPS}
	yes := true
	no := false
	patch := UpdateDomainBindingInput{DomainRevision: first.Domain.Revision, Domain: &DomainConfigPatch{EntryPoints: &both, RedirectHTTPToHTTPS: &yes, Enabled: &yes, TLS: &DomainTLSInput{Mode: model.TLSManaged}}}
	managed, err := c.UpdateDomainBinding(ctx, service.ID, first.ID, patch, first.Route.Revision)
	if err != nil || managed.Domain.CertificateID == "" || managed.Domain.Status != model.DomainCertPending {
		t.Fatalf("managed TLS intent: %#v %v", managed, err)
	}
	certificate, err := c.store.GetCertificate(ctx, managed.Domain.CertificateID)
	if err != nil || certificate.Status != model.CertificatePending || certificate.PrimaryDomain != input.Domain.Host {
		t.Fatalf("certificate intent: %#v %v", certificate, err)
	}
	unchanged, err := c.store.GetRoute(ctx, second.ID)
	if err != nil || unchanged.Revision != second.Route.Revision || !unchanged.Enabled {
		t.Fatal("shared TLS edit rewrote sibling route")
	}
	if _, err = c.UpdateDomainBinding(ctx, service.ID, first.ID, patch, managed.Route.Revision); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale shared domain update accepted: %v", err)
	}
	patch.DomainRevision = managed.Domain.Revision
	retained, err := c.UpdateDomainBinding(ctx, service.ID, first.ID, patch, managed.Route.Revision)
	if err != nil || retained.Domain.CertificateID != managed.Domain.CertificateID {
		t.Fatalf("unchanged TLS regenerated certificate: %#v %v", retained, err)
	}
	patch.DomainRevision = retained.Domain.Revision
	patch.Domain.Host = pointer("renamed.example.test")
	renamed, err := c.UpdateDomainBinding(ctx, service.ID, first.ID, patch, retained.Route.Revision)
	if err != nil || renamed.Domain.CertificateID == retained.Domain.CertificateID || renamed.Domain.Host != "renamed.example.test" {
		t.Fatalf("renamed host retained wrong certificate: %#v %v", renamed, err)
	}
	if _, err = c.store.GetCertificate(ctx, retained.Domain.CertificateID); err != nil {
		t.Fatal("old certificate silently deleted")
	}
	http := []string{EntryPointHTTP}
	disabled, err := c.UpdateDomainBinding(ctx, service.ID, first.ID, UpdateDomainBindingInput{DomainRevision: renamed.Domain.Revision, Domain: &DomainConfigPatch{EntryPoints: &http, RedirectHTTPToHTTPS: &no, HTTP3Enabled: &no, Enabled: &no, TLS: &DomainTLSInput{Mode: model.TLSDisabled}}}, renamed.Route.Revision)
	if err != nil || disabled.Domain.CertificateID != "" || disabled.Domain.HTTPSEnabled || disabled.Domain.Enabled || disabled.Domain.Status != model.DomainReady {
		t.Fatalf("TLS disabled: %#v %v", disabled, err)
	}
}

func managedBindingFixture(t *testing.T) (*Core, model.Service, CreateDomainBindingInput) {
	t.Helper()
	c, runtime, _ := controlFixture(t)
	ctx := context.Background()
	app, _, _, err := c.InstallApplication(ctx, InstallApplicationInput{ApplicationPreflightInput: ApplicationPreflightInput{RuntimeID: runtime.ID, ComposeYAML: "services:\n  web:\n    image: example.test/web:fixed\n    expose: [8080]"}, Name: "Website", ProjectName: "website", IdempotencyKey: "install"})
	if err != nil {
		t.Fatal(err)
	}
	services, err := c.store.ListServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("installed placeholders: %#v %v", services, err)
	}
	return c, services[0], CreateDomainBindingInput{Domain: &DomainConfigInput{Host: "managed.example.test", EntryPoints: []string{EntryPointHTTP}}, Route: RouteConfigInput{Path: "/"}, IdempotencyKey: "attach"}
}

func TestManagedBindingOperationsAreIdempotentAndDoNotClaimExecution(t *testing.T) {
	c, service, input := managedBindingFixture(t)
	ctx := context.Background()
	binding, op, err := c.CreateDomainBinding(ctx, service.ID, input)
	if err != nil || op == nil || op.Status != model.OperationQueued || binding.Status != model.RoutePending {
		t.Fatalf("attach must queue: %#v %#v %v", binding, op, err)
	}
	retry, retryOp, err := c.CreateDomainBinding(ctx, service.ID, input)
	if err != nil || retryOp == nil || retryOp.ID != op.ID || retry.ID != binding.ID {
		t.Fatalf("duplicate attach: %#v %#v %v", retry, retryOp, err)
	}
	changed := input
	changed.Route.Path = "/different"
	if _, _, err = c.CreateDomainBinding(ctx, service.ID, changed); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("idempotency key reused with changed input: %v", err)
	}
	second, _, err := c.CreateDomainBinding(ctx, service.ID, CreateDomainBindingInput{DomainID: binding.Domain.ID, Route: RouteConfigInput{Path: "/second"}, IdempotencyKey: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if detach, err := c.DeleteDomainBinding(ctx, service.ID, binding.ID, binding.Route.Revision, "detach-first"); err != nil || detach != nil {
		t.Fatalf("non-final detach queued workload network removal: %#v %v", detach, err)
	}
	detach, err := c.DeleteDomainBinding(ctx, service.ID, second.ID, second.Route.Revision, "detach-last")
	if err != nil || detach == nil || detach.Status != model.OperationQueued {
		t.Fatalf("last detach not queued: %#v %v", detach, err)
	}
	again, err := c.DeleteDomainBinding(ctx, service.ID, second.ID, second.Route.Revision, "detach-last")
	if err != nil || again == nil || again.ID != detach.ID {
		t.Fatalf("duplicate detach: %#v %v", again, err)
	}
	if _, err = c.DeleteDomainBinding(ctx, service.ID, binding.ID, binding.Route.Revision, "detach-last"); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("detach key reused for another route: %v", err)
	}
	stored, err := c.store.GetService(ctx, service.ID)
	if err != nil || !stored.Managed {
		t.Fatal("queued detach changed workload ownership")
	}
}

func TestBindingsRejectForeignAndLabelOwnedRoutes(t *testing.T) {
	c, service, input := externalBindingFixture(t)
	ctx := context.Background()
	binding, _, err := c.CreateDomainBinding(ctx, service.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	other := service
	other.ID = id.New(id.Service)
	other.ExternalID = "project/other"
	other.ServiceKey = "project/other"
	if err = c.store.UpsertService(ctx, &other); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"missing", other.ID} {
		if _, err = c.UpdateDomainBinding(ctx, target, binding.ID, UpdateDomainBindingInput{}, 1); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("foreign update: %v", err)
		}
		if _, err = c.DeleteDomainBinding(ctx, target, binding.ID, 1, ""); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("foreign delete: %v", err)
		}
	}
	store := c.store.(*storage.SQLStore)
	if _, err = store.DB().ExecContext(ctx, "UPDATE routes SET source=? WHERE id=?", model.SourceDiscovery, binding.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.UpdateDomainBinding(ctx, service.ID, binding.ID, UpdateDomainBindingInput{}, 1); err == nil {
		t.Fatal("label route edited")
	}
	if _, err = c.DeleteDomainBinding(ctx, service.ID, binding.ID, 1, ""); err == nil {
		t.Fatal("label route deleted")
	}
	route, err := c.store.GetRoute(ctx, binding.ID)
	if err != nil || !route.Enabled || route.Revision != binding.Route.Revision {
		t.Fatal("ownership rejection mutated route")
	}
}
