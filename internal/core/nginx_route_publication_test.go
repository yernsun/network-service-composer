package core

import (
	"context"
	"errors"
	"testing"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestFixedResponseRouteStagesThenPublishesWithGatewayEvidence(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	domain, err := c.CreateDomain(ctx, CreateDomainInput{PoolID: pool.ID, DomainConfigInput: DomainConfigInput{Host: "hello.example.test", EntryPoints: []string{"http"}}})
	if err != nil {
		t.Fatal(err)
	}
	now := c.now()
	gw := model.Gateway{ID: id.New(id.Gateway), PoolID: pool.ID, Name: "edge", Enabled: true, RendererVersion: nginxx.RendererVersion, LastSeenAt: &now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := c.store.UpsertGateway(ctx, &gw); err != nil {
		t.Fatal(err)
	}
	route := model.Route{ID: id.New(id.Route), DomainID: domain.ID, Kind: "return", Match: "exact", Path: "/hello", ResponseCode: 200, ResponseValue: "hello", Enabled: true}
	redirect := model.Route{ID: id.New(id.Route), DomainID: domain.ID, Kind: "redirect", Match: "exact", Path: "/old", ResponseCode: 308, ResponseValue: "https://hello.example.test/hello", Enabled: true}
	external := model.Route{ID: id.New(id.Route), DomainID: domain.ID, Kind: "proxy", Match: "prefix", Path: "/external/", ExternalURL: "https://upstream.example.test/", Enabled: true}
	draft, err := c.SaveNginxDraft(ctx, pool.ID, NginxConfiguration{Snippets: []model.ConfigSnippet{}, Routes: []storage.RouteWrite{{Route: route}, {Route: redirect}, {Route: external}}}, "operator", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.GetRoute(ctx, route.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("draft touched production: %v", err)
	}
	candidate, err := c.NginxDraftSnapshot(ctx, pool.ID)
	if err != nil || len(candidate.Routes) != 3 {
		t.Fatalf("candidate: %+v %v", candidate.Routes, err)
	}
	validation, err := c.CreateNginxValidation(ctx, pool.ID, draft.Revision)
	if err != nil {
		t.Fatal(err)
	}
	finishValidationFixture(t, c, pool.ID, gw, validation)
	if _, err := c.PublishNginx(ctx, pool.ID, validation.ID, "operator", "fixed-response", draft.Revision); err != nil {
		t.Fatal(err)
	}
	stored, err := c.store.GetRoute(ctx, route.ID)
	if err != nil || stored.Kind != "return" || stored.ServiceID != "" {
		t.Fatalf("published route: %+v %v", stored, err)
	}
	for _, expected := range []model.Route{redirect, external} {
		stored, err := c.store.GetRoute(ctx, expected.ID)
		if err != nil || stored.Kind != expected.Kind || stored.ServiceID != "" {
			t.Fatalf("published service-free route: %+v %v", stored, err)
		}
	}
	visible, err := c.store.ListRoutesFiltered(ctx, storage.ConsoleFilter{DomainID: domain.ID}, storage.Page{})
	if err != nil || len(visible) != 3 {
		t.Fatalf("Service-free route missing from API list: %+v %v", visible, err)
	}
}

func TestExternalProxyRejectsInjectedAndCredentialTargets(t *testing.T) {
	for _, target := range []string{"http://user:pass@internal/", "http://target/;return 200", "https://target/\nadd_header X 1;"} {
		if nginxx.ValidateExternalProxyURL(target) == nil {
			t.Fatalf("accepted unsafe target %q", target)
		}
	}
	for _, target := range []string{"http://127.0.0.1:8080/", "https://internal.example.test/base/"} {
		if err := nginxx.ValidateExternalProxyURL(target); err != nil {
			t.Fatalf("valid target %q: %v", target, err)
		}
	}
}

func TestPoolRoutePublicationRollsBackOnRouteStorageFailure(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	before, err := c.store.GetPool(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	route := model.Route{ID: id.New(id.Route), DomainID: id.New(id.Domain), Kind: "return", Match: "exact", Path: "/hello", ResponseCode: 200, ResponseValue: "hello", Source: model.SourceAPI, Enabled: true, Status: model.RouteActive}
	if err := c.store.PublishNginxChanges(ctx, pool.ID, before.DesiredRevision, nil, []storage.RouteWrite{{Route: route}}); err == nil {
		t.Fatal("missing domain route committed")
	}
	after, err := c.store.GetPool(ctx, pool.ID)
	if err != nil || after.DesiredRevision != before.DesiredRevision {
		t.Fatalf("failed transaction advanced pool: %+v %v", after, err)
	}
	if _, err := c.store.GetRoute(ctx, route.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("failed transaction exposed route: %v", err)
	}
}
