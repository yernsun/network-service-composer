package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type entrypointBoundaryFixture struct {
	core    *Core
	sql     *storage.SQLStore
	env     Environment
	op      model.DeploymentOperation
	service model.Service
	entry   model.ServiceEntrypoint
	gateway model.Gateway
}

func newEntrypointBoundaryFixture(t *testing.T) entrypointBoundaryFixture {
	t.Helper()
	c, runtime, _ := controlFixture(t)
	ctx := t.Context()
	sql := c.store.(*storage.SQLStore)
	runtime.Type = model.RuntimeSwarm
	c.store = networkRuntimeStore{Store: sql, runtime: runtime}
	app, op, _, err := c.InstallApplication(ctx, InstallApplicationInput{ApplicationPreflightInput: ApplicationPreflightInput{RuntimeID: runtime.ID, ComposeYAML: "services:\n  web:\n    image: example.test/web:fixed\n    expose: [8080]"}, Name: "Website", ProjectName: "website", IdempotencyKey: "install"})
	if err != nil {
		t.Fatal(err)
	}
	services, err := c.store.ListServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("installed service: %v %v", services, err)
	}
	service := services[0]
	now := c.now()
	gateway := model.Gateway{ID: "entrypoint-gateway", PoolID: runtime.PoolID, Name: "edge", Enabled: true, Status: model.GatewayReady, RendererVersion: nginxx.RendererVersion, LastSeenAt: &now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := c.store.UpsertGateway(ctx, &gateway); err != nil {
		t.Fatal(err)
	}
	entry := model.ServiceEntrypoint{Host: "entrypoint.example.test", Service: "web", Port: 8080, GatewayID: gateway.ID}
	rev, err := c.store.GetApplicationRevision(ctx, op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	rev.ConfigurationSource.Entrypoint = &entry
	source, err := json.Marshal(rev.ConfigurationSource)
	if err != nil {
		t.Fatal(err)
	}
	// Seed the accepted execution outcome and immutable intent. No Docker
	// command runs in these reconciliation tests.
	if _, err := sql.DB().ExecContext(ctx, "UPDATE application_revisions SET configuration_source=? WHERE id=?", string(source), rev.ID); err != nil {
		t.Fatal(err)
	}
	if err := sql.ActivateApplicationRevision(ctx, runtime.ID, app.ID, rev.ID, rev.ID, []string{service.ServiceKey}, nil, "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?", op.ID); err != nil {
		t.Fatal(err)
	}
	service.RuntimeReadyInstances = 1
	service.DesiredInstances, service.RunningInstances = 1, 1
	service.LastSeenAt, service.RuntimeObjectID = &now, "entrypoint-service-object"
	if err := c.store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.store.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: runtime.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{readinessDiscoveredService(service)}}); err != nil {
		t.Fatal(err)
	}
	service, err = c.store.GetService(ctx, service.ID)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = sql.GetRuntime(ctx, runtime.ID)
	if err != nil {
		t.Fatal(err)
	}
	runtime.Type = model.RuntimeSwarm
	c.store = networkRuntimeStore{Store: sql, runtime: runtime}
	return entrypointBoundaryFixture{core: c, sql: sql, env: Environment{ApplicationID: app.ID, RuntimeID: runtime.ID, ProjectName: app.ProjectName}, op: op, service: service, entry: entry, gateway: gateway}
}

func (f entrypointBoundaryFixture) status(t *testing.T) ServiceEntrypointStatus {
	t.Helper()
	s, err := f.core.ServiceEntrypointStatus(t.Context(), f.op.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEntrypointReconciliationWaitsForReadinessAndQueuesOnlyBinding(t *testing.T) {
	f := newEntrypointBoundaryFixture(t)
	ctx := t.Context()
	f.service.RuntimeReadyInstances = 0
	if err := f.core.store.UpsertService(ctx, &f.service); err != nil {
		t.Fatal(err)
	}
	f.core.serviceEntrypointTick(ctx, f.env)
	if s := f.status(t); s.Status != "PENDING" {
		t.Fatalf("unready service attached: %+v", s)
	}
	f.service.RuntimeReadyInstances = 1
	if err := f.core.store.UpsertService(ctx, &f.service); err != nil {
		t.Fatal(err)
	}
	f.core.serviceEntrypointTick(ctx, f.env)
	s := f.status(t)
	if s.Status != string(model.OperationQueued) || s.Attempts != 1 || s.BindingID == "" || s.ServiceID != f.service.ID || s.OperationID == "" {
		t.Fatalf("binding intent not submitted: %+v", s)
	}
	firstTask := s.OperationID
	f.core.serviceEntrypointTick(ctx, f.env)
	if again := f.status(t); again.OperationID != firstTask || again.Revision != s.Revision {
		t.Fatalf("duplicate reconciliation changed receipt: %+v", again)
	}
	operations, err := f.core.store.ListDeploymentOperations(ctx, f.env.ApplicationID, storage.Page{Limit: 20})
	if err != nil || len(operations) != 2 {
		t.Fatalf("duplicate or missing operation: %d %v", len(operations), err)
	}
	for _, op := range operations {
		if op.ID != f.op.ID && op.Type != "DOMAIN_ATTACH" {
			t.Fatalf("reconciliation redeployed application: %s", op.Type)
		}
	}
	app, err := f.core.store.GetApplication(ctx, f.env.ApplicationID)
	if err != nil || app.ActiveRevisionID != f.op.RevisionID || app.DesiredRevisionID != f.op.RevisionID {
		t.Fatalf("entrypoint changed accepted deployment: %+v %v", app, err)
	}
	route, err := f.core.store.GetRoute(ctx, s.BindingID)
	if err != nil || route.ServiceID != f.service.ID || route.Status != model.RoutePending {
		t.Fatalf("incorrect pending route: %+v %v", route, err)
	}
}

func TestEntrypointValidationFailureRequiresCurrentReceiptForExplicitRetry(t *testing.T) {
	f := newEntrypointBoundaryFixture(t)
	ctx := t.Context()
	f.gateway.Enabled = false
	if _, err := f.sql.DB().ExecContext(ctx, "UPDATE gateways SET enabled=? WHERE id=?", f.gateway.Enabled, f.gateway.ID); err != nil {
		t.Fatal(err)
	}
	f.core.serviceEntrypointTick(ctx, f.env)
	s := f.status(t)
	if s.Status != "FAILED" || s.Attempts != 1 || s.ErrorCode != "entrypoint_validation_failed" || !s.NextAttempt.After(f.core.now()) {
		t.Fatalf("missing bounded failure receipt: %+v", s)
	}
	f.core.serviceEntrypointTick(ctx, f.env)
	if again := f.status(t); again.Revision != s.Revision {
		t.Fatal("backoff was ignored")
	}
	if _, err := f.core.RetryServiceEntrypoint(ctx, f.op.ID, s.Revision-1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale retry accepted: %v", err)
	}
	reset, err := f.core.RetryServiceEntrypoint(ctx, f.op.ID, s.Revision)
	if err != nil || reset.Status != "PENDING" || reset.Attempts != 0 || reset.ErrorCode != "" || !reset.NextAttempt.IsZero() {
		t.Fatalf("explicit retry retained terminal state: %+v %v", reset, err)
	}
	if _, err := f.core.RetryServiceEntrypoint(ctx, f.op.ID, reset.Revision); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("pending retry accepted: %v", err)
	}
	f.gateway.Enabled = true
	if _, err := f.sql.DB().ExecContext(ctx, "UPDATE gateways SET enabled=? WHERE id=?", f.gateway.Enabled, f.gateway.ID); err != nil {
		t.Fatal(err)
	}
	f.core.serviceEntrypointTick(ctx, f.env)
	if s := f.status(t); s.OperationID == "" || s.Attempts != 1 {
		t.Fatalf("recovered gateway did not permit binding: %+v", s)
	}
}

func TestEntrypointNetworkRetryPreservesSuccessfulServiceDeployment(t *testing.T) {
	f := newEntrypointBoundaryFixture(t)
	ctx := t.Context()
	f.core.serviceEntrypointTick(ctx, f.env)
	s := f.status(t)
	sql := f.sql
	if _, err := sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='FAILED',error_code='network_attach_failed' WHERE id=?", s.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := sql.DB().ExecContext(ctx, "UPDATE routes SET route_status='ERROR' WHERE id=?", s.BindingID); err != nil {
		t.Fatal(err)
	}
	failed := f.status(t)
	if failed.Status != "FAILED" || failed.ErrorCode != "network_attach_failed" {
		t.Fatalf("task failure missing from entrypoint status: %+v", failed)
	}
	retried, err := f.core.RetryServiceEntrypoint(ctx, f.op.ID, failed.Revision)
	if err != nil || retried.Status != "SUBMITTED" || retried.OperationID == s.OperationID || retried.BindingID != s.BindingID {
		t.Fatalf("network retry replaced deployment or route: %+v %v", retried, err)
	}
	op, err := f.core.store.GetDeploymentOperation(ctx, retried.OperationID)
	if err != nil || op.Type != "DOMAIN_ATTACH" || op.ServiceID != f.service.ID || op.ApplicationID != f.env.ApplicationID {
		t.Fatalf("incorrect retry operation: %+v %v", op, err)
	}
	raw, _, err := f.core.keyring.Decrypt(op.EncryptedInput, operationAAD(op.ID))
	var frozen struct {
		ServiceRevisionID string `json:"service_revision_id"`
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &frozen); err != nil || frozen.ServiceRevisionID != f.op.RevisionID {
		t.Fatal("retry lost its frozen service revision", frozen, err)
	}
	prior, err := f.core.store.GetDeploymentOperation(ctx, s.OperationID)
	if err != nil || prior.Status != model.OperationFailed {
		t.Fatalf("retry erased failure history: %+v %v", prior, err)
	}
	if _, err := f.core.RetryServiceEntrypoint(ctx, f.op.ID, failed.Revision); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale concurrent retry accepted: %v", err)
	}
}

func TestEntrypointSupersededIntentCannotAttachAfterNewServiceRevision(t *testing.T) {
	f := newEntrypointBoundaryFixture(t)
	ctx := t.Context()
	rev, err := f.core.store.GetApplicationRevision(ctx, f.op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	rev.ConfigurationSource.ServiceRevisions = map[string]string{"web": "newer-service-revision"}
	source, err := json.Marshal(rev.ConfigurationSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.sql.DB().ExecContext(ctx, "UPDATE application_revisions SET configuration_source=? WHERE id=?", string(source), rev.ID); err != nil {
		t.Fatal(err)
	}
	f.core.serviceEntrypointTick(ctx, f.env)
	s := f.status(t)
	if s.Status != "SUPERSEDED" || s.OperationID != "" {
		t.Fatalf("old entrypoint overwrote newer intent: %+v", s)
	}
	f.core.serviceEntrypointTick(ctx, f.env)
	if again := f.status(t); again.Revision != s.Revision {
		t.Fatal("superseded receipt was rewritten")
	}
}

func TestEntrypointReconciliationBoundsAutomaticRetryAndContinuesPagination(t *testing.T) {
	f := newEntrypointBoundaryFixture(t)
	ctx := t.Context()
	for i := 0; i < 200; i++ {
		op := model.DeploymentOperation{ID: fmt.Sprintf("op_000-%03d", i), RuntimeID: f.env.RuntimeID, ApplicationID: f.env.ApplicationID, Type: "START", Status: model.OperationSucceeded, Metadata: model.Metadata{Revision: 1, CreatedAt: f.core.now(), UpdatedAt: f.core.now()}}
		if _, err := f.core.store.CreateDeploymentOperation(ctx, &op); err != nil {
			t.Fatal(err)
		}
	}
	f.gateway.Enabled = false
	if _, err := f.sql.DB().ExecContext(ctx, "UPDATE gateways SET enabled=? WHERE id=?", f.gateway.Enabled, f.gateway.ID); err != nil {
		t.Fatal(err)
	}
	clock := f.core.now()
	f.core.now = func() time.Time { return clock }
	for attempt := 1; attempt <= 3; attempt++ {
		f.core.serviceEntrypointTick(ctx, f.env)
		if s := f.status(t); s.Attempts != attempt || s.Status != "FAILED" {
			t.Fatalf("paginated failure was skipped: %+v", s)
		}
		clock = clock.Add(2 * time.Minute)
	}
	s := f.status(t)
	f.core.serviceEntrypointTick(ctx, f.env)
	if again := f.status(t); again.Revision != s.Revision {
		t.Fatal("automatic retry exceeded its budget")
	}
}

func TestEntrypointReviewRejectsUnavailableTargetsBeforeDomainMutation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*entrypointBoundaryFixture, *model.ServiceEntrypoint)
	}{
		{"unsubmitted service", func(_ *entrypointBoundaryFixture, e *model.ServiceEntrypoint) { e.Service = "other" }},
		{"missing gateway", func(_ *entrypointBoundaryFixture, e *model.ServiceEntrypoint) { e.GatewayID = "missing" }},
		{"wrong pool", func(f *entrypointBoundaryFixture, _ *model.ServiceEntrypoint) { f.gateway.PoolID = "other" }},
		{"disabled gateway", func(f *entrypointBoundaryFixture, _ *model.ServiceEntrypoint) { f.gateway.Enabled = false }},
		{"unready gateway", func(f *entrypointBoundaryFixture, _ *model.ServiceEntrypoint) {
			f.gateway.Status = model.GatewayOffline
		}},
		{"outside allowed gateways", func(f *entrypointBoundaryFixture, _ *model.ServiceEntrypoint) { f.env.GatewayIDs = []string{"other"} }},
		{"invalid host", func(_ *entrypointBoundaryFixture, e *model.ServiceEntrypoint) { e.Host = "bad/host" }},
		{"missing port", func(_ *entrypointBoundaryFixture, e *model.ServiceEntrypoint) { e.Port = 0 }},
		{"overflow port", func(_ *entrypointBoundaryFixture, e *model.ServiceEntrypoint) { e.Port = 65536 }},
		{"missing HTTPS certificate", func(_ *entrypointBoundaryFixture, e *model.ServiceEntrypoint) { e.CertificateID = "missing" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEntrypointBoundaryFixture(t)
			e := f.entry
			tc.mutate(&f, &e)
			// The wrong-pool observation is deliberately presented by a store wrapper,
			// preserving the fixture's real foreign-key constraints.
			f.core.store = entrypointGatewayStore{Store: f.core.store, gateway: f.gateway}
			runtime, err := f.core.store.GetRuntime(t.Context(), f.env.RuntimeID)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.core.validateServiceEntrypoint(t.Context(), f.env, runtime, &e, []string{"web"}); err == nil {
				t.Fatal("unsafe entrypoint accepted")
			}
			routes, err := f.core.store.ListRoutes(t.Context(), storage.Page{Limit: 10})
			if err != nil || len(routes) != 0 {
				t.Fatalf("review created routes: %d %v", len(routes), err)
			}
		})
	}
}

type entrypointGatewayStore struct {
	storage.Store
	gateway model.Gateway
}

func (s entrypointGatewayStore) GetGateway(ctx context.Context, id string) (model.Gateway, error) {
	if id == s.gateway.ID {
		return s.gateway, nil
	}
	return s.Store.GetGateway(ctx, id)
}
