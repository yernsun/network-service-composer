package core

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestIPAccessDraftPersistenceAndPreviewBoundaries(t *testing.T) {
	c, service, input := externalBindingFixture(t)
	ctx := context.Background()
	binding, _, err := c.CreateDomainBinding(ctx, service.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	pool := binding.Domain.PoolID
	raw := " 192.168.1.10-192.168.1.20 "
	controls := []model.IPAccessControl{{Scope: "server", ResourceID: binding.Domain.ID, Access: []string{raw}, Forbidden: []string{"2001:db8::/32"}, ClientIP: &model.ClientIPSource{Header: "X-Forwarded-For", TrustedProxies: []string{" 10.0.0.1-10.0.0.9 ", "2001:db8::/32"}}}, {Scope: "location", ResourceID: binding.Route.ID, Access: []string{"192.168.1.11"}, Forbidden: []string{}}}
	saved, err := c.SaveNginxDraft(ctx, pool, NginxConfiguration{AccessControls: controls}, "editor", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved.AccessControls, controls) {
		t.Fatal(saved.AccessControls)
	}
	active, err := c.BuildGatewaySnapshot(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(active.AccessControls) != 0 {
		t.Fatal("draft leaked into active snapshot")
	}
	candidate, err := c.NginxDraftSnapshot(ctx, pool)
	if err != nil || !reflect.DeepEqual(candidate.AccessControls, controls) {
		t.Fatal(candidate, err)
	}
	if _, err = c.SaveNginxDraft(ctx, pool, NginxConfiguration{}, "stale", saved.Revision+1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal(err)
	}
	seen := []model.Snapshot{}
	_, err = c.PreviewNginx(ctx, pool, NginxPreviewInput{DomainID: binding.Domain.ID, AccessControls: controls}, func(s model.Snapshot) (nginxx.PreviewDocument, error) {
		seen = append(seen, s)
		return nginxx.PreviewDocument{}, nil
	})
	if err != nil || len(seen) != 2 || len(seen[0].AccessControls) != 0 || !reflect.DeepEqual(seen[1].AccessControls, controls) {
		t.Fatal(seen, err)
	}
	versions, err := c.ListNginxDraftVersions(ctx, pool, storage.Page{Limit: 100})
	if err != nil || len(versions) != 1 || !reflect.DeepEqual(versions[0].AccessControls, controls) {
		t.Fatal(versions, err)
	}
	before := saved.Revision
	for _, tc := range []struct {
		name  string
		in    NginxConfiguration
		field string
	}{
		{"invalid range", NginxConfiguration{AccessControls: []model.IPAccessControl{{Scope: "server", ResourceID: binding.Domain.ID, Forbidden: []string{"10.0.0.2-10.0.0.1"}}}}, "access_controls.0.forbidden.0"},
		{"invalid scope", NginxConfiguration{AccessControls: []model.IPAccessControl{{Scope: "http", ResourceID: binding.Domain.ID}}}, "access_controls.0"},
		{"duplicate", NginxConfiguration{AccessControls: append(controls, controls[0])}, "access_controls.2"},
		{"manual rules", NginxConfiguration{AccessControls: controls, Snippets: []model.ConfigSnippet{{Scope: "server", ResourceID: binding.Domain.ID, Content: "deny all;"}}}, "access_controls.0"},
		{"header injection", NginxConfiguration{AccessControls: []model.IPAccessControl{{Scope: "server", ResourceID: binding.Domain.ID, ClientIP: &model.ClientIPSource{Header: "X-Forwarded-For; deny all;", TrustedProxies: []string{"10.0.0.1"}}}}}, "access_controls.0.client_ip.header"},
		{"no trust", NginxConfiguration{AccessControls: []model.IPAccessControl{{Scope: "server", ResourceID: binding.Domain.ID, ClientIP: &model.ClientIPSource{Header: "X-Real-IP"}}}}, "access_controls.0.client_ip.trusted_proxies"},
		{"invalid trust", NginxConfiguration{AccessControls: []model.IPAccessControl{{Scope: "server", ResourceID: binding.Domain.ID, ClientIP: &model.ClientIPSource{Header: "X-Client-IP", TrustedProxies: []string{"10.0.0.2-10.0.0.1"}}}}}, "access_controls.0.client_ip.trusted_proxies.0"},
		{"route override", NginxConfiguration{AccessControls: []model.IPAccessControl{{Scope: "location", ResourceID: binding.Route.ID, ClientIP: &model.ClientIPSource{Header: "X-Real-IP", TrustedProxies: []string{"10.0.0.1"}}}}}, "access_controls.0.client_ip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.SaveNginxDraft(ctx, pool, tc.in, "editor", before)
			var validation ValidationError
			if !errors.As(err, &validation) || !strings.Contains(validation.Field, tc.field) {
				t.Fatal(err)
			}
			current, _ := c.NginxConfiguration(ctx, pool, "nginx-draft")
			if current.Revision != before || !reflect.DeepEqual(current.AccessControls, controls) {
				t.Fatal("failed write changed draft")
			}
		})
	}
	other, err := c.CreatePool(ctx, CreatePoolInput{Name: "foreign", BackendNetwork: "foreign", ChallengeUpstream: "http://controller:8080"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.SaveNginxDraft(ctx, other.ID, NginxConfiguration{AccessControls: controls}, "editor", 0); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("foreign pool accepted", err)
	}
}

func TestIPAccessPublicationAndRestoreKeepRawRules(t *testing.T) {
	c, pool, g, previous := nginxValidationFixture(t, false)
	ctx := context.Background()
	domain, err := c.CreateDomain(ctx, CreateDomainInput{PoolID: pool.ID, DomainConfigInput: DomainConfigInput{Host: "raw.acl.test", EntryPoints: []string{EntryPointHTTP}}})
	if err != nil {
		t.Fatal(err)
	}
	controls := []model.IPAccessControl{{Scope: "server", ResourceID: domain.ID, Access: []string{"192.168.1.10-192.168.1.20"}, Forbidden: []string{"192.168.1.15"}, ClientIP: &model.ClientIPSource{Header: "X-Client-IP", TrustedProxies: []string{" 192.168.2.10-192.168.2.20 "}}}}
	draft, err := c.SaveNginxDraft(ctx, pool.ID, NginxConfiguration{AccessControls: controls}, "editor", previous.DraftRevision)
	if err != nil {
		t.Fatal(err)
	}
	validation, err := c.CreateNginxValidation(ctx, pool.ID, draft.Revision)
	if err != nil {
		t.Fatal(err)
	}
	finishValidationFixture(t, c, pool.ID, g, validation)
	release, err := c.PublishNginx(ctx, pool.ID, validation.ID, "editor", "acl-publication", draft.Revision)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := c.BuildGatewaySnapshot(ctx, pool.ID)
	if err != nil || !reflect.DeepEqual(snapshot.AccessControls, controls) {
		t.Fatal(snapshot, err)
	}
	if release.Status == "SUCCEEDED" {
		t.Fatal("publication claimed loaded evidence before Gateway confirmation")
	}
	current, err := c.NginxConfiguration(ctx, pool.ID, "nginx-draft")
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := c.SaveNginxDraft(ctx, pool.ID, NginxConfiguration{}, "editor", current.Revision)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := c.RestoreNginxVersion(ctx, pool.ID, release.VersionID, "editor", cleared.Revision)
	if err != nil || !reflect.DeepEqual(restored.AccessControls, controls) {
		t.Fatal(restored, err)
	}
	// Older renderer cannot validate or receive the newly structured policy.
	g.RendererVersion = "dns-fragments-ip-access-v8"
	if err = c.store.UpsertGateway(ctx, &g); err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateNginxValidation(ctx, pool.ID, restored.Revision); err == nil {
		t.Fatal("old renderer accepted IP access configuration")
	}
}

func TestIPAccessNewRoutePreviewAndDeletion(t *testing.T) {
	c, service, input := externalBindingFixture(t)
	ctx := context.Background()
	binding, _, err := c.CreateDomainBinding(ctx, service.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	route := model.Route{ID: id.New(id.Route), DomainID: binding.Domain.ID, Path: "/restricted", Match: "exact", Kind: "return", ResponseCode: 200, ResponseValue: "hello", Enabled: true}
	controls := []model.IPAccessControl{{Scope: "location", ResourceID: route.ID, Access: []string{"127.0.0.1"}, Forbidden: []string{}}}
	in := NginxConfiguration{Routes: []storage.RouteWrite{{Route: route}}, AccessControls: controls}
	seen := []model.Snapshot{}
	_, err = c.PreviewNginx(ctx, binding.Domain.PoolID, NginxPreviewInput{DomainID: binding.Domain.ID, Routes: in.Routes, AccessControls: controls}, func(s model.Snapshot) (nginxx.PreviewDocument, error) {
		seen = append(seen, s)
		return nginxx.PreviewDocument{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || len(seen[1].Routes) != len(seen[0].Routes)+1 || len(seen[0].AccessControls) != 0 || len(seen[1].AccessControls) != 1 {
		t.Fatal(seen)
	}
	draft, err := c.SaveNginxDraft(ctx, binding.Domain.PoolID, in, "editor", 0)
	if err != nil {
		t.Fatal(err)
	}
	invalid := NginxConfiguration{Routes: []storage.RouteWrite{{Route: binding.Route, ExpectedRevision: binding.Route.Revision, Delete: true}}, AccessControls: []model.IPAccessControl{{Scope: "location", ResourceID: binding.Route.ID}}}
	if _, err = c.SaveNginxDraft(ctx, binding.Domain.PoolID, invalid, "editor", draft.Revision); err == nil {
		t.Fatal("ACL retained a deleted route")
	}
	cleared, err := c.SaveNginxDraft(ctx, binding.Domain.PoolID, NginxConfiguration{}, "editor", draft.Revision)
	if err != nil || len(cleared.AccessControls) != 0 {
		t.Fatal(cleared, err)
	}
}
