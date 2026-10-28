package core

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"testing"
	"time"
)

// Fail at dependency boundaries, retaining real persistence for the resource
// state assertions. These are not Docker or issuer acceptance fixtures.
type ingressUnavailableStore struct {
	storage.Store
	boundary string
}

var errIngressUnavailable = errors.New("ingress fixture dependency unavailable")

func (s ingressUnavailableStore) GetApplication(ctx context.Context, key string) (model.Application, error) {
	if s.boundary == "application" {
		return model.Application{}, errIngressUnavailable
	}
	return s.Store.GetApplication(ctx, key)
}
func (s ingressUnavailableStore) GetApplicationRevision(ctx context.Context, key string) (model.ApplicationRevision, error) {
	if s.boundary == "revision" {
		return model.ApplicationRevision{}, errIngressUnavailable
	}
	return s.Store.GetApplicationRevision(ctx, key)
}
func (s ingressUnavailableStore) GetRuntime(ctx context.Context, key string) (model.Runtime, error) {
	if s.boundary == "runtime" {
		return model.Runtime{}, errIngressUnavailable
	}
	return s.Store.GetRuntime(ctx, key)
}
func (s ingressUnavailableStore) GetService(ctx context.Context, key string) (model.Service, error) {
	if s.boundary == "service" {
		return model.Service{}, errIngressUnavailable
	}
	return s.Store.GetService(ctx, key)
}
func (s ingressUnavailableStore) GetDeploymentOperation(ctx context.Context, key string) (model.DeploymentOperation, error) {
	if s.boundary == "operation" {
		return model.DeploymentOperation{}, errIngressUnavailable
	}
	return s.Store.GetDeploymentOperation(ctx, key)
}
func (s ingressUnavailableStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if s.boundary == "receipt" {
		return storage.ControlDocument{}, errIngressUnavailable
	}
	return s.Store.GetControlDocument(ctx, key)
}
func (s ingressUnavailableStore) ListDeploymentOperations(ctx context.Context, key string, p storage.Page) ([]model.DeploymentOperation, error) {
	if s.boundary == "history" {
		return nil, errIngressUnavailable
	}
	return s.Store.ListDeploymentOperations(ctx, key, p)
}
func (s ingressUnavailableStore) ListAllServicesFiltered(ctx context.Context, f storage.ServiceFilter) ([]model.Service, error) {
	if s.boundary == "inventory" {
		return nil, errIngressUnavailable
	}
	return s.Store.ListAllServicesFiltered(ctx, f)
}
func (s ingressUnavailableStore) ListServiceDomainBindings(ctx context.Context, key string) ([]model.DomainBinding, error) {
	if s.boundary == "bindings" {
		return nil, errIngressUnavailable
	}
	return s.Store.ListServiceDomainBindings(ctx, key)
}
func (s ingressUnavailableStore) WriteApplicationDocuments(ctx context.Context, key string, version int64, w []storage.ControlWrite) error {
	if s.boundary == "write" {
		return errIngressUnavailable
	}
	return s.Store.WriteApplicationDocuments(ctx, key, version, w)
}

func TestEntrypointDependencyFailurePreservesApplicationAndBindings(t *testing.T) {
	for _, boundary := range []string{"application", "revision", "runtime", "service", "operation", "receipt", "history", "inventory", "bindings", "write"} {
		t.Run(boundary, func(t *testing.T) {
			f := newEntrypointBoundaryFixture(t)
			ctx := t.Context()
			before, err := f.core.store.GetApplication(ctx, f.env.ApplicationID)
			if err != nil {
				t.Fatal(err)
			}
			original := f.core.store
			// Reconciliation must not infer success or publish through unavailable data.
			f.core.store = ingressUnavailableStore{Store: original, boundary: boundary}
			f.core.serviceEntrypointTick(ctx, f.env)
			_, _ = f.core.ServiceEntrypointStatus(ctx, f.op.ID)
			_, _ = f.core.RetryServiceEntrypoint(ctx, f.op.ID, 0)
			f.core.store = original
			after, err := original.GetApplication(ctx, before.ID)
			if err != nil || before.ActiveRevisionID != after.ActiveRevisionID || before.DesiredRevisionID != after.DesiredRevisionID {
				t.Fatal("dependency failure changed application", after, err)
			}
			bindings, err := original.ListServiceDomainBindings(ctx, f.service.ID)
			if err != nil {
				t.Fatal(err)
			}
			// Operation lookup and service lookup are read-only API dependencies; the
			// coordinator can safely work using its independent complete inventory.
			if boundary != "operation" && boundary != "service" && len(bindings) != 0 {
				t.Fatal("unavailable dependency published", bindings)
			}
		})
	}
}

func TestEntrypointMatchingRequiresExactIngressConfiguration(t *testing.T) {
	f := newEntrypointBoundaryFixture(t)
	binding, _, err := f.core.CreateDomainBinding(t.Context(), f.service.ID, entrypointBinding(&f.entry))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := f.core.store.GetRuntime(t.Context(), f.env.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	if !entrypointBindingMatches(binding, runtime.PoolID, f.entry) {
		t.Fatal("canonical ingress does not match")
	}
	cases := []struct {
		name   string
		change func(*model.DomainBinding)
	}{
		{"other pool", func(b *model.DomainBinding) { b.Domain.PoolID = "other" }},
		{"readonly domain", func(b *model.DomainBinding) { b.Domain.Source = model.SourceStatic }},
		{"other host", func(b *model.DomainBinding) { b.Domain.Host = "other.example.test" }},
		{"disabled domain", func(b *model.DomainBinding) { b.Domain.Enabled = false }},
		{"HTTP unavailable", func(b *model.DomainBinding) { b.Domain.HTTPEnabled = false }},
		{"TLS entrypoints", func(b *model.DomainBinding) { b.Domain.HTTPSEnabled = true }},
		{"certificate", func(b *model.DomainBinding) { b.Domain.CertificateID = "other" }},
		{"redirect", func(b *model.DomainBinding) { b.Domain.HTTPSRedirect = true }},
		{"readonly route", func(b *model.DomainBinding) { b.Route.Source = model.SourceStatic }},
		{"disabled route", func(b *model.DomainBinding) { b.Route.Enabled = false }},
		{"other path", func(b *model.DomainBinding) { b.Route.Path = "/other" }},
		{"route kind", func(b *model.DomainBinding) { b.Route.Kind = "redirect" }},
		{"priority", func(b *model.DomainBinding) { b.Route.Priority = 3 }},
		{"path rewrite", func(b *model.DomainBinding) { b.Route.StripPath = true }},
		{"WebSocket", func(b *model.DomainBinding) { b.Route.WebsocketEnabled = true }},
		{"backend port", func(b *model.DomainBinding) { b.Route.TargetPort = 9090 }},
		{"backend TLS", func(b *model.DomainBinding) { b.Route.UpstreamProtocol = "https" }},
		{"backend SNI", func(b *model.DomainBinding) { b.Route.UpstreamServerName = "other.example.test" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := binding
			tc.change(&changed)
			if entrypointBindingMatches(changed, runtime.PoolID, f.entry) {
				t.Fatal("operator configuration silently reused")
			}
		})
	}
}

func TestEntrypointFreshnessRequiresAuthoritativeMembershipAndIdentity(t *testing.T) {
	f := newEntrypointBoundaryFixture(t)
	ctx := t.Context()
	runtime, err := f.core.store.GetRuntime(ctx, f.env.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	recovery := f.core.observeReadinessRecovery(ctx, f.op.ID)
	if !f.core.entrypointWorkloadFresh(ctx, runtime, f.service, recovery) {
		t.Fatal("fixture not authoritative")
	}
	cases := []struct {
		name   string
		change func(*model.Runtime, *model.Service, *readinessRecovery)
	}{
		{"disabled", func(r *model.Runtime, _ *model.Service, _ *readinessRecovery) { r.Enabled = false }},
		{"offline", func(r *model.Runtime, _ *model.Service, _ *readinessRecovery) { r.Status = model.RuntimeOffline }},
		{"unapplied", func(r *model.Runtime, _ *model.Service, _ *readinessRecovery) { r.ObservationEpoch++ }},
		{"old service", func(_ *model.Runtime, s *model.Service, _ *readinessRecovery) {
			old := f.core.now().Add(-time.Hour)
			s.LastSeenAt = &old
		}},
		{"post restore runtime", func(_ *model.Runtime, _ *model.Service, r *readinessRecovery) {
			cutover := f.core.now().Add(time.Minute)
			r.Cutover = &cutover
		}},
		{"post restore service", func(r *model.Runtime, s *model.Service, recovery *readinessRecovery) {
			cutover := f.core.now().Add(-250 * time.Millisecond)
			recovery.Cutover = &cutover
			current := f.core.now()
			r.LastSeenAt = &current
			old := current.Add(-500 * time.Millisecond)
			s.LastSeenAt = &old
		}},
		{"unknown object", func(_ *model.Runtime, s *model.Service, _ *readinessRecovery) { s.RuntimeObjectID = "" }},
		{"different observation", func(r *model.Runtime, _ *model.Service, _ *readinessRecovery) {
			r.AppliedObservation++
			r.ObservationEpoch = r.AppliedObservation
		}},
		{"different timestamp", func(r *model.Runtime, _ *model.Service, _ *readinessRecovery) {
			newer := r.LastSeenAt.Add(time.Second)
			r.LastSeenAt = &newer
		}},
		{"unknown member", func(_ *model.Runtime, s *model.Service, _ *readinessRecovery) { s.ID = "other" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, s, cut := runtime, f.service, recovery
			tc.change(&r, &s, &cut)
			if f.core.entrypointWorkloadFresh(ctx, r, s, cut) {
				t.Fatal("unproven member used for ingress")
			}
		})
	}
}

func TestEntrypointScopeInheritanceAndGatewayRestrictions(t *testing.T) {
	f := serviceReviewBoundarySetup(t)
	ctx := t.Context()
	now := f.core.now()
	gateway := model.Gateway{ID: id.New(id.Gateway), PoolID: f.runtime.PoolID, Enabled: true, Status: model.GatewayReady, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	other := gateway
	other.ID = id.New(id.Gateway)
	for _, g := range []*model.Gateway{&gateway, &other} {
		if err := f.core.store.UpsertGateway(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	revision, err := f.core.store.GetApplicationRevision(ctx, f.op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	entry := model.ServiceEntrypoint{Service: "web", Host: "scope.example.test", Port: 8080, GatewayID: gateway.ID}
	revision.ConfigurationSource.EntrypointIntents = map[string]model.ServiceEntrypointIntent{"web": {ID: "scope-intent", Entry: entry}}
	raw, err := json.Marshal(revision.ConfigurationSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.sql.DB().ExecContext(ctx, "UPDATE application_revisions SET configuration_source=? WHERE id=?", string(raw), revision.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.core.UpdateEnvironment(ctx, f.env.ID, EnvironmentSettings{DisplayName: f.env.DisplayName, Kind: f.env.Kind, GatewayIDs: []string{other.ID}}, f.env.Revision); err == nil {
		t.Fatal("pending ingress gateway was removed")
	}
	if _, err = f.core.UpdateEnvironment(ctx, f.env.ID, EnvironmentSettings{DisplayName: f.env.DisplayName, Kind: f.env.Kind, GatewayIDs: []string{gateway.ID, other.ID}}, f.env.Revision); err != nil {
		t.Fatal(err)
	}
	source := model.DeploymentSource{}
	if err = f.core.mergeEntrypointIntents(ctx, &source, revision.ID, "services:\n  web:\n    image: example.test/web:fixed\n  api:\n    image: example.test/api:fixed\n"); err != nil || source.EntrypointIntents["web"].ID != "scope-intent" {
		t.Fatal("unrelated service lost ingress", source, err)
	}
	source.Entrypoint = &model.ServiceEntrypoint{Service: "api", Host: "api.example.test", Port: 8081, GatewayID: other.ID}
	if err = f.core.mergeEntrypointIntents(ctx, &source, revision.ID, "services:\n  web:\n    image: example.test/web:fixed\n  api:\n    image: example.test/api:fixed\n"); err != nil || len(source.EntrypointIntents) != 2 {
		t.Fatal(source, err)
	}
	for _, name := range []string{"web", "api"} {
		op := f.op
		op.Type = "REDEPLOY"
		copied := revision
		copied.ConfigurationSource = source
		copied.ConfigurationSource.Entrypoint = nil
		copied.ConfigurationSource.ServiceScope = &model.ServiceDeploymentScope{Names: []string{name}}
		raw, err = json.Marshal(copied.ConfigurationSource)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.sql.DB().ExecContext(ctx, "UPDATE application_revisions SET configuration_source=? WHERE id=?", string(raw), revision.ID); err != nil {
			t.Fatal(err)
		}
		intent, err := f.core.operationEntrypointIntent(ctx, op)
		if err != nil || intent.Entry.Service != name {
			t.Fatal("operation scope selects different ingress", intent, err)
		}
	}
}
