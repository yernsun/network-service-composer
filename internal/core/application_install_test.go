package core

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestInstallApplicationCommitsInitialDomainIntent(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "install.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "install", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtime := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "docker", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, ManagementEnabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateRuntime(ctx, &runtime); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Management.Enabled = true
	cfg.Storage.Driver = "sqlite"
	ring, err := security.NewKeyring("v1", map[string]string{"v1": base64.StdEncoding.EncodeToString(make([]byte, 32))})
	if err != nil {
		t.Fatal(err)
	}
	serviceCore := New(store, cfg, ring)
	input := InstallApplicationInput{ApplicationPreflightInput: ApplicationPreflightInput{RuntimeID: runtime.ID,
		ComposeYAML: "services:\n  web:\n    image: nginx:1.29-alpine\n    expose:\n      - '8080'\n"},
		Name: "Website", ProjectName: "website", IdempotencyKey: "install-with-domain", Actor: "test",
		DomainBinding: &InstallDomainBindingInput{Service: "web", CreateDomainBindingInput: CreateDomainBindingInput{
			Domain: &DomainConfigInput{Host: "website.example.com", EntryPoints: []string{EntryPointHTTP}, TLS: DomainTLSInput{Mode: model.TLSDisabled}},
			Route:  RouteConfigInput{Path: "/app", Backend: RouteBackendInput{Port: 8080, Protocol: "http"}}}}}
	application, operation, _, err := serviceCore.InstallApplication(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != model.OperationQueued || operation.ApplicationID != application.ID {
		t.Fatalf("unexpected install operation: %#v", operation)
	}
	services, err := store.ListServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: application.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("initial placeholder Services: %#v, %v", services, err)
	}
	bindings, err := store.ListServiceDomainBindings(ctx, services[0].ID)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("initial bindings: %#v, %v", bindings, err)
	}
	if bindings[0].Route.Status != model.RoutePending || bindings[0].Route.TargetPort != 8080 || bindings[0].Domain.Host != "website.example.com" {
		t.Fatalf("unexpected initial binding: %#v", bindings[0])
	}
	retriedApplication, retriedOperation, _, err := serviceCore.InstallApplication(ctx, input)
	if err != nil || retriedApplication.ID != application.ID || retriedOperation.ID != operation.ID {
		t.Fatalf("idempotent install did not recover aggregate: app=%s operation=%s err=%v", retriedApplication.ID, retriedOperation.ID, err)
	}
}

func TestValidProjectNameMatchesDockerComposeConstraints(t *testing.T) {
	for _, value := range []string{"demo", "demo_2", "2-demo"} {
		if !validProjectName(value) {
			t.Errorf("valid project name %q was rejected", value)
		}
	}
	for _, value := range []string{"", "Uppercase", "_leading", "contains.dot", "contains/slash"} {
		if validProjectName(value) {
			t.Errorf("invalid project name %q was accepted", value)
		}
	}
}
