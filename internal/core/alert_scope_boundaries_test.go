package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

var errAlertOwnership = errors.New("ownership observation unavailable")

type certificateScopeStore struct {
	storage.Store
	boundary  string
	count     int
	runtimeID string
}

func (s certificateScopeStore) GetGateway(ctx context.Context, key string) (model.Gateway, error) {
	if s.boundary == "gateway" {
		return model.Gateway{}, errAlertOwnership
	}
	return s.Store.GetGateway(ctx, key)
}
func (s certificateScopeStore) GetRuntime(ctx context.Context, key string) (model.Runtime, error) {
	if s.boundary == "runtime" {
		return model.Runtime{}, errAlertOwnership
	}
	return s.Store.GetRuntime(ctx, key)
}

func (s certificateScopeStore) ListRoutes(_ context.Context, p storage.Page) ([]model.Route, error) {
	if s.boundary == "routes" {
		return nil, errAlertOwnership
	}
	rows := []model.Route{}
	for i := 0; i < s.count; i++ {
		id := fmt.Sprintf("route-%04d", i)
		if id > p.After && len(rows) < p.Limit {
			service := "service"
			if s.boundary == "no service" {
				service = ""
			}
			rows = append(rows, model.Route{ID: id, DomainID: "domain", ServiceID: service})
		}
	}
	return rows, nil
}
func (s certificateScopeStore) GetDomain(context.Context, string) (model.Domain, error) {
	if s.boundary == "domain" {
		return model.Domain{}, errAlertOwnership
	}
	return model.Domain{CertificateID: "cert-shared"}, nil
}
func (s certificateScopeStore) GetService(context.Context, string) (model.Service, error) {
	if s.boundary == "service" {
		return model.Service{}, errAlertOwnership
	}
	return model.Service{ApplicationID: "owned-app"}, nil
}
func (s certificateScopeStore) ListControlDocuments(ctx context.Context, kind, parent string, p storage.Page) ([]storage.ControlDocument, error) {
	if kind != "environment" {
		return s.Store.ListControlDocuments(ctx, kind, parent, p)
	}
	if s.boundary == "environment" {
		return nil, errAlertOwnership
	}
	rows := []storage.ControlDocument{}
	for i := 0; i < s.count; i++ {
		id := fmt.Sprintf("env-%04d", i)
		if id > p.After && len(rows) < p.Limit {
			raw, _ := json.Marshal(Environment{ProjectID: "project", ApplicationID: "owned-app", RuntimeID: s.runtimeID})
			rows = append(rows, storage.ControlDocument{ID: id, Kind: kind, Payload: raw})
		}
	}
	return rows, nil
}

func TestSharedCertificateScopesPaginateAndFailClosed(t *testing.T) {
	c, _, _ := controlFixture(t)
	original := c.store
	c.store = certificateScopeStore{Store: original, count: 201}
	scopes, err := c.alertScopes(t.Context(), "certificate", "cert-shared")
	if err != nil || len(scopes) != 201 || scopes[200].EnvironmentID != "env-0200" {
		t.Fatal("shared certificate ownership lost", len(scopes), err)
	}
	for _, kind := range []string{"service", "certificate"} {
		event := NotificationEvent{Type: "certificate.problem", ResourceID: "cert-shared"}
		if kind == "service" {
			if scopes, err := c.alertScopes(t.Context(), kind, "service"); err != nil || len(scopes) != 201 {
				t.Fatal(scopes, err)
			}
		}
		if err := c.notificationScopes(t.Context(), &event); err != nil || len(event.Scopes) != 201 || event.EnvironmentID != "" {
			t.Fatal(event, err)
		}
	}
	for _, boundary := range []string{"routes", "domain", "service", "environment"} {
		c.store = certificateScopeStore{Store: original, count: 1, boundary: boundary}
		if err := c.enqueueNotification(t.Context(), NotificationEvent{ID: "event", Type: "certificate.problem", ResourceID: "cert-shared", OccurredAt: c.now()}, 1); !errors.Is(err, errAlertOwnership) {
			t.Fatal("ownership failure became global delivery", boundary, err)
		}
	}
	c.store = certificateScopeStore{Store: original, count: 1, boundary: "service"}
	if _, err := c.alertScopes(t.Context(), "service", "service"); !errors.Is(err, errAlertOwnership) {
		t.Fatal("unavailable service ownership accepted", err)
	}
	for _, test := range []struct{ boundary, id string }{{"", "another-certificate"}, {"no service", "cert-shared"}} {
		c.store = certificateScopeStore{Store: original, count: 1, boundary: test.boundary}
		if got, err := c.alertScopes(t.Context(), "certificate", test.id); err != nil || len(got) != 0 {
			t.Fatal("unrelated binding became ownership", got, err)
		}
	}
	c.store = original
	if len(notices(t, c)) != 0 {
		t.Fatal("failed ownership resolution inserted notification")
	}
}

func TestDefaultGatewayOwnershipUsesRuntimePool(t *testing.T) {
	c, rt, pool := controlFixture(t)
	ctx := t.Context()
	p, err := c.CreateProject(ctx, "Gateways", "gateways")
	if err != nil {
		t.Fatal(err)
	}
	env, err := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "dev", RuntimeID: rt.ID})
	if err != nil {
		t.Fatal(err)
	}
	other, err := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "prod", RuntimeID: rt.ID})
	if err != nil {
		t.Fatal(err)
	}
	gateway := model.Gateway{ID: "gateway-default", PoolID: pool.ID, Name: "default", Enabled: true, Metadata: model.Metadata{Revision: 1, CreatedAt: c.now(), UpdatedAt: c.now()}}
	if err := c.store.UpsertGateway(ctx, &gateway); err != nil {
		t.Fatal(err)
	}
	got, err := c.alertScopes(ctx, "gateway", gateway.ID)
	ids := map[string]bool{}
	for _, scope := range got {
		ids[scope.EnvironmentID] = true
	}
	if err != nil || len(got) != 2 || !ids[env.ID] || !ids[other.ID] {
		t.Fatal("default Gateway not routed to its environment", got, err)
	}
	if got, err := c.alertScopes(ctx, "gateway", "missing-gateway"); err != nil || len(got) != 0 {
		t.Fatal("absent Gateway inferred pool ownership", got, err)
	}
	base := c.store
	for _, boundary := range []string{"gateway", "runtime"} {
		c.store = certificateScopeStore{Store: base, count: 1, runtimeID: rt.ID, boundary: boundary}
		if _, err := c.alertScopes(ctx, "gateway", gateway.ID); !errors.Is(err, errAlertOwnership) {
			t.Fatal("unavailable Gateway ownership accepted", boundary, err)
		}
	}
}

func TestIssueResponsibilityUsesExistingEnabledUsersAndCAS(t *testing.T) {
	c, _, _ := controlFixture(t)
	ctx := t.Context()
	directory := identityDirectory{Users: []identityCredential{{User: User{ID: "enabled", Enabled: true}}, {User: User{ID: "disabled", Enabled: false}}}}
	if err := c.saveControl(ctx, identityKey, "identities", "", directory, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.observeIssue(ctx, "runtime_unavailable", "runtime", "runtime", "/", true); err != nil {
		t.Fatal(err)
	}
	rows, err := c.Issues(ctx, storage.Page{})
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	issue := rows[0]
	for _, user := range []string{"unknown", "disabled"} {
		if _, err := c.AssignIssue(ctx, issue.ID, user, issue.Revision); err == nil {
			t.Fatal("invalid responsible user accepted")
		}
	}
	if _, err := c.AssignIssue(ctx, issue.ID, "enabled", issue.Revision+1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal(err)
	}
	assigned, err := c.AssignIssue(ctx, issue.ID, "enabled", issue.Revision)
	if err != nil || assigned.ResponsibleUserID != "enabled" || !assigned.Active {
		t.Fatal(assigned, err)
	}
	cleared, err := c.AssignIssue(ctx, issue.ID, "", assigned.Revision)
	if err != nil || cleared.ResponsibleUserID != "" {
		t.Fatal(cleared, err)
	}
	if _, err := c.AssignIssue(ctx, "missing", "", 0); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
}
