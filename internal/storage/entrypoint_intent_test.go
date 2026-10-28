package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEntrypointReceiptTransactionsAcrossDatabases(t *testing.T) {
	for _, fixture := range []struct{ driver, dsn string }{
		{"sqlite", "file:" + filepath.Join(t.TempDir(), "entrypoint.db") + "?_pragma=foreign_keys(1)&_time_format=sqlite"},
		{"postgres", os.Getenv("NSC_TEST_POSTGRES_DSN")}, {"mysql", os.Getenv("NSC_TEST_MYSQL_DSN")},
	} {
		t.Run(fixture.driver, func(t *testing.T) {
			if fixture.dsn == "" {
				t.Skip("isolated external database is not configured")
			}
			store, err := Open(fixture.driver, fixture.dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.Close() })
			if err = store.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			ctx, runtime, now := managedRuntimeFixture(t, store)
			app := model.Application{ID: id.New(id.Application), RuntimeID: runtime.ID, Name: "Entry", ProjectName: id.New("entry"), Managed: true, Status: model.ApplicationRunning, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			revision := model.ApplicationRevision{ID: id.New(id.ApplicationRevision), ApplicationID: app.ID, Sequence: 1, EncryptionKeyID: "fixture", EncryptedCompose: []byte("fixture"), EncryptedEnvironment: []byte("fixture"), EncryptedResolved: []byte("fixture"), CreatedAt: now}
			app.ActiveRevisionID, app.DesiredRevisionID = revision.ID, revision.ID
			service := model.Service{ID: id.New(id.Service), ApplicationID: app.ID, RuntimeID: runtime.ID, ServiceKey: app.ProjectName + "/web", Name: "web", Namespace: app.ProjectName, Managed: true, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			install := model.DeploymentOperation{ID: id.New(id.Operation), ApplicationID: app.ID, RuntimeID: runtime.ID, RevisionID: revision.ID, Type: "INSTALL", Status: model.OperationSucceeded, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			if err = store.CreateApplicationInstall(ctx, &app, &revision, []model.Service{service}, nil, nil, nil, &install); err != nil {
				t.Fatal(err)
			}
			intentID := id.New("entryintent")
			initial := ControlDocument{ID: intentID, Kind: "service-entrypoint-intent", ParentID: app.ID, Payload: json.RawMessage("{}"), CreatedAt: now, UpdatedAt: now}
			if err = store.WriteApplicationDocuments(ctx, app.ID, app.Revision, []ControlWrite{{Document: initial}}); err != nil {
				t.Fatal(err)
			}
			if err = store.SetApplicationStatus(ctx, app.ID, model.ApplicationRunning, "", now); err != nil {
				t.Fatal(err)
			}
			app, err = store.GetApplication(ctx, app.ID)
			if err != nil {
				t.Fatal(err)
			}
			domain := model.Domain{ID: id.New(id.Domain), PoolID: runtime.PoolID, Host: fmt.Sprintf("entry-%d.example.test", time.Now().UnixNano()), Enabled: true, HTTPEnabled: true, TLSMode: model.TLSDisabled, Source: model.SourceAPI, Status: model.DomainReady, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			route := model.Route{ID: id.New(id.Route), DomainID: domain.ID, ServiceID: service.ID, Path: "/", Kind: "proxy", Enabled: true, Source: model.SourceAPI, Status: model.RoutePending, TargetPort: 8080, UpstreamProtocol: "http", Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			child := model.DeploymentOperation{ID: id.New(id.Operation), ApplicationID: app.ID, RuntimeID: runtime.ID, ServiceID: service.ID, RevisionID: revision.ID, Type: "DOMAIN_ATTACH", Status: model.OperationQueued, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			receipt := initial
			receipt.Payload, err = json.Marshal(map[string]string{"status": "SUBMITTED", "operation_id": child.ID, "binding_id": route.ID})
			if err != nil {
				t.Fatal(err)
			}
			write := ControlWrite{Document: receipt, Expected: 1}
			if err = store.CreateServiceDomainBindingWithIntent(ctx, &domain, nil, &route, &child, write, app.Revision-1); !errors.Is(err, ErrPrecondition) {
				t.Fatal("stale application accepted", err)
			}
			for _, resource := range []string{domain.ID, route.ID, child.ID} {
				var count int
				table := "deployment_operations"
				if resource == domain.ID {
					table = "domains"
				}
				if resource == route.ID {
					table = "routes"
				}
				if err = store.DB().QueryRowContext(ctx, store.bind("SELECT COUNT(*) FROM "+table+" WHERE id=:id"), resource).Scan(&count); err != nil || count != 0 {
					t.Fatal("failed publication left a resource", resource, count, err)
				}
			}
			if err = store.CreateServiceDomainBindingWithIntent(ctx, &domain, nil, &route, &child, write, app.Revision); err != nil {
				t.Fatal(err)
			}
			saved, err := store.GetControlDocument(ctx, intentID)
			if err != nil || saved.Revision != 2 {
				t.Fatal(saved, err)
			}
			retry := child
			retry.ID = id.New(id.Operation)
			write.Document.Payload, err = json.Marshal(map[string]string{"status": "SUBMITTED", "operation_id": retry.ID, "binding_id": route.ID})
			if err != nil {
				t.Fatal(err)
			}
			if err = store.CreateDeploymentOperationWithReceipt(ctx, &retry, write, app.Revision); !errors.Is(err, ErrPrecondition) {
				t.Fatal("stale receipt accepted", err)
			}
			if _, err = store.GetDeploymentOperation(ctx, retry.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("failed receipt left an orphan task", err)
			}
			write.Expected = 2
			if err = store.CreateDeploymentOperationWithReceipt(ctx, &retry, write, app.Revision); err != nil {
				t.Fatal(err)
			}
			saved, err = store.GetControlDocument(ctx, intentID)
			if err != nil || saved.Revision != 3 {
				t.Fatal(saved, err)
			}
			for _, opID := range []string{child.ID, retry.ID} {
				if _, err = store.DB().ExecContext(ctx, store.bind("UPDATE deployment_operations SET status=:status WHERE id=:id"), model.OperationSucceeded, opID); err != nil {
					t.Fatal(err)
				}
			}
			wrong := write
			wrong.Document.ParentID = runtime.ID
			if err = store.CreateDeploymentOperationWithReceipt(ctx, &retry, wrong, app.Revision); !errors.Is(err, ErrConflict) {
				t.Fatal("foreign receipt owner accepted", err)
			}
			if err = store.CreateServiceDomainBindingWithIntent(ctx, &domain, nil, &route, nil, write, app.Revision); !errors.Is(err, ErrConflict) {
				t.Fatal("missing child accepted", err)
			}
			for _, invalid := range []ControlWrite{wrong, {Document: ControlDocument{Kind: "domain-draft", ParentID: app.ID}}} {
				if err = store.CreateServiceDomainBindingWithIntent(ctx, &domain, nil, &route, &child, invalid, app.Revision); !errors.Is(err, ErrConflict) {
					t.Fatal("invalid intent accepted", err)
				}
				if err = store.CreateDeploymentOperationWithReceipt(ctx, &retry, invalid, app.Revision); !errors.Is(err, ErrConflict) {
					t.Fatal("invalid receipt accepted", err)
				}
			}
			if err = store.CreateServiceDomainBindingWithIntent(ctx, &domain, nil, &route, &child, write, 0); !errors.Is(err, ErrConflict) {
				t.Fatal("missing application version accepted", err)
			}
			if err = store.CreateDeploymentOperationWithReceipt(ctx, &retry, write, 0); !errors.Is(err, ErrConflict) {
				t.Fatal("missing retry application version accepted", err)
			}
			if err = store.CreateDeploymentOperationWithReceipt(ctx, &retry, write, app.Revision-1); !errors.Is(err, ErrPrecondition) {
				t.Fatal("stale retry application accepted", err)
			}
			write.Expected = 3
			if err = store.CreateDeploymentOperationWithReceipt(ctx, &retry, write, app.Revision); !errors.Is(err, ErrConflict) {
				t.Fatal("duplicate child accepted", err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if err = store.CreateDeploymentOperationWithReceipt(cancelled, &retry, write, app.Revision); !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled retry accepted", err)
			}
		})
	}
}
