package core

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type entrypointIdentityStore struct {
	storage.Store
	revisions  map[string]model.ApplicationRevision
	routeError error
	taskID     string
	taskError  error
	writeError error
}

func (s entrypointIdentityStore) GetApplicationRevision(ctx context.Context, key string) (model.ApplicationRevision, error) {
	if v, ok := s.revisions[key]; ok {
		return v, nil
	}
	return s.Store.GetApplicationRevision(ctx, key)
}
func (s entrypointIdentityStore) GetRoute(ctx context.Context, key string) (model.Route, error) {
	if s.routeError != nil {
		return model.Route{}, s.routeError
	}
	return s.Store.GetRoute(ctx, key)
}
func (s entrypointIdentityStore) GetDeploymentOperation(ctx context.Context, key string) (model.DeploymentOperation, error) {
	if key == s.taskID {
		return model.DeploymentOperation{}, s.taskError
	}
	return s.Store.GetDeploymentOperation(ctx, key)
}
func (s entrypointIdentityStore) WriteApplicationDocuments(ctx context.Context, key string, expected int64, writes []storage.ControlWrite) error {
	if s.writeError != nil {
		return s.writeError
	}
	return s.Store.WriteApplicationDocuments(ctx, key, expected, writes)
}

func TestEntrypointLegacyServiceHeadsAndIntentReplacement(t *testing.T) {
	f := newEntrypointBoundaryFixture(t)
	ctx := t.Context()
	base, err := f.core.store.GetApplicationRevision(ctx, f.op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	currentHead := base
	currentHead.ConfigurationSource.ServiceRevisions = map[string]string{"web": base.ID}
	for _, entry := range []*model.ServiceEntrypoint{&f.entry, nil, {Service: "api"}} {
		currentHead.ConfigurationSource.Entrypoint = entry
		intents, err := f.core.revisionEntrypointIntents(ctx, currentHead)
		if err != nil || (len(intents) == 1) != (entry != nil && entry.Service == "web") {
			t.Fatal("legacy current head confused service intent", intents, err)
		}
	}
	previous := base
	previous.ID = "previous-service-head"
	previous.ConfigurationSource.Entrypoint = &f.entry
	original := f.core.store
	f.core.store = entrypointIdentityStore{Store: original, revisions: map[string]model.ApplicationRevision{previous.ID: previous}}
	base.ConfigurationSource.Entrypoint = nil
	base.ConfigurationSource.ServiceRevisions = map[string]string{"web": previous.ID, "api": base.ID}
	intents, err := f.core.revisionEntrypointIntents(ctx, base)
	if err != nil || len(intents) != 1 || intents["web"].Entry != f.entry {
		t.Fatal(intents, err)
	}
	previous.ApplicationID = "foreign-application"
	f.core.store = entrypointIdentityStore{Store: original, revisions: map[string]model.ApplicationRevision{previous.ID: previous}}
	if _, err = f.core.revisionEntrypointIntents(ctx, base); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("foreign service head accepted", err)
	}
	previous.ApplicationID, previous.ConfigurationSource.Entrypoint = base.ApplicationID, nil
	f.core.store = entrypointIdentityStore{Store: original, revisions: map[string]model.ApplicationRevision{previous.ID: previous}}
	if intents, err = f.core.revisionEntrypointIntents(ctx, base); err != nil || len(intents) != 0 {
		t.Fatal(intents, err)
	}
	f.core.store = original
	first := model.DeploymentSource{Entrypoint: &f.entry}
	if err = f.core.mergeEntrypointIntents(ctx, &first, "", "services:\n  web:\n    image: example.test/web:fixed\n"); err != nil {
		t.Fatal(err)
	}
	base.ConfigurationSource.EntrypointIntents = first.EntrypointIntents
	f.core.store = entrypointIdentityStore{Store: original, revisions: map[string]model.ApplicationRevision{base.ID: base}}
	same := model.DeploymentSource{Entrypoint: &f.entry}
	if err = f.core.mergeEntrypointIntents(ctx, &same, base.ID, "services:\n  web:\n    image: example.test/web:fixed\n"); err != nil || same.EntrypointIntents["web"].ID != first.EntrypointIntents["web"].ID {
		t.Fatal(same, err)
	}
	changed := f.entry
	changed.Port++
	updated := model.DeploymentSource{Entrypoint: &changed}
	if err = f.core.mergeEntrypointIntents(ctx, &updated, base.ID, "services:\n  web:\n    image: example.test/web:fixed\n"); err != nil || updated.EntrypointIntents["web"].ID == first.EntrypointIntents["web"].ID {
		t.Fatal(updated, err)
	}
	if err = f.core.mergeEntrypointIntents(ctx, &same, base.ID, "services:\n  api:\n    image: example.test/api:fixed\n"); err != nil || len(same.EntrypointIntents) != 0 {
		t.Fatal("deleted service retains ingress", same, err)
	}
	if err = f.core.mergeEntrypointIntents(ctx, &same, base.ID, "invalid: ["); err == nil {
		t.Fatal("invalid configuration accepted")
	}
	if err = f.core.mergeEntrypointIntents(ctx, &same, "missing-base", "services:\n  web:\n    image: example.test/web:fixed\n"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
	base.ConfigurationSource.EntrypointIntents = nil
	base.ConfigurationSource.ServiceRevisions["web"] = "missing-head"
	f.core.store = entrypointIdentityStore{Store: original, revisions: map[string]model.ApplicationRevision{base.ID: base}}
	if err = f.core.mergeEntrypointIntents(ctx, &same, base.ID, "services:\n  web:\n    image: example.test/web:fixed\n"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestEntrypointOperationSelectionRejectsForeignOwnersAndAmbiguousServices(t *testing.T) {
	f := newEntrypointBoundaryFixture(t)
	ctx := t.Context()
	revision, err := f.core.store.GetApplicationRevision(ctx, f.op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	intent := legacyEntrypointIntent(revision.ApplicationID, f.entry)
	revision.ConfigurationSource.EntrypointIntents = map[string]model.ServiceEntrypointIntent{"web": intent}
	original := f.core.store
	set := func() {
		f.core.store = entrypointIdentityStore{Store: original, revisions: map[string]model.ApplicationRevision{revision.ID: revision}}
	}
	set()
	if got, err := f.core.operationEntrypointIntent(ctx, f.op); err != nil || got.ID != intent.ID {
		t.Fatal(got, err)
	}
	revision.ConfigurationSource.Entrypoint = nil
	set()
	if got, err := f.core.operationEntrypointIntent(ctx, f.op); err != nil || got.ID != intent.ID {
		t.Fatal("single intent cannot be selected", got, err)
	}
	op := f.op
	op.ServiceID = f.service.ID
	if got, err := f.core.operationEntrypointIntent(ctx, op); err != nil || got.ID != intent.ID {
		t.Fatal("service lifecycle lost intent", got, err)
	}
	revision.ConfigurationSource.EntrypointIntents["api"] = model.ServiceEntrypointIntent{ID: "other-intent"}
	set()
	if _, err = f.core.operationEntrypointIntent(ctx, f.op); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("ambiguous operation selected an ingress", err)
	}
	op.ServiceID = "missing-service"
	if _, err = f.core.operationEntrypointIntent(ctx, op); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
	op.ServiceID, op.RuntimeID = f.service.ID, "foreign-runtime"
	if _, err = f.core.operationEntrypointIntent(ctx, op); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("foreign runtime accepted", err)
	}
	op.RuntimeID, revision.ApplicationID = f.op.RuntimeID, "foreign-application"
	set()
	if _, err = f.core.operationEntrypointIntent(ctx, op); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("foreign revision accepted", err)
	}
}

func TestEntrypointStatusRequiresOwnedRouteAndChildEvidence(t *testing.T) {
	for _, scenario := range []string{"missing route", "route owner", "missing task", "task application", "task service", "task kind", "active without task", "pending without task"} {
		t.Run(scenario, func(t *testing.T) {
			f := newEntrypointBoundaryFixture(t)
			ctx := t.Context()
			input := entrypointBinding(&f.entry)
			binding, task, err := f.core.CreateDomainBinding(ctx, f.service.ID, input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.sql.DB().ExecContext(ctx, "UPDATE routes SET route_status='ACTIVE' WHERE id=?", binding.ID); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "active without task", "pending without task":
				if _, err = f.sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET operation_type='RESTART' WHERE id=?", task.ID); err != nil {
					t.Fatal(err)
				}
				if scenario == "pending without task" {
					if _, err = f.sql.DB().ExecContext(ctx, "UPDATE routes SET route_status='PENDING' WHERE id=?", binding.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			f.core.serviceEntrypointTick(ctx, f.env)
			before := f.status(t)
			if scenario == "active without task" {
				if before.Status != "SUCCEEDED" || before.OperationID != "" {
					t.Fatal(before)
				}
				return
			}
			if scenario == "pending without task" {
				if before.Status != "FAILED" || before.ErrorCode != "entrypoint_binding_conflict" {
					t.Fatal(before)
				}
				return
			}
			original := f.core.store
			switch scenario {
			case "missing route":
				f.core.store = entrypointIdentityStore{Store: original, routeError: storage.ErrNotFound}
			case "route owner":
				if _, err = f.sql.DB().ExecContext(ctx, "UPDATE routes SET service_id=NULL WHERE id=?", binding.ID); err != nil {
					t.Fatal(err)
				}
			case "missing task":
				f.core.store = entrypointIdentityStore{Store: original, taskID: task.ID, taskError: storage.ErrNotFound}
			default:
				// Foreign-key enforcement is retained: model a corrupt response at the read boundary.
				doc, err := original.GetControlDocument(ctx, before.IntentID)
				if err != nil {
					t.Fatal(err)
				}
				corrupt := *task
				if scenario == "task application" {
					corrupt.ApplicationID = "foreign"
				}
				if scenario == "task service" {
					corrupt.ServiceID = ""
				}
				if scenario == "task kind" {
					corrupt.Type = "RESTART"
				}
				f.core.store = entrypointTaskStore{Store: original, task: corrupt}
				if doc.Revision != before.Revision {
					t.Fatal("receipt unexpectedly changed")
				}
			}
			if _, err = f.core.ServiceEntrypointStatus(ctx, f.op.ID); err == nil {
				t.Fatal("unknown or foreign evidence reported as success")
			}
			if _, err = f.core.RetryServiceEntrypoint(ctx, f.op.ID, before.Revision); err == nil {
				t.Fatal("retry accepted corrupt evidence")
			}
		})
	}
}

type entrypointTaskStore struct {
	storage.Store
	task model.DeploymentOperation
}

func (s entrypointTaskStore) GetDeploymentOperation(ctx context.Context, key string) (model.DeploymentOperation, error) {
	if key == s.task.ID {
		return s.task, nil
	}
	return s.Store.GetDeploymentOperation(ctx, key)
}

func TestEntrypointBindingHistoryTraversesAllPagesAndChoosesLatestRetry(t *testing.T) {
	f := newEntrypointBoundaryFixture(t)
	ctx := t.Context()
	binding, original, err := f.core.CreateDomainBinding(ctx, f.service.ID, entrypointBinding(&f.entry))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 201; i++ {
		task := f.op
		task.ID = fmt.Sprintf("op_000-%03d", i)
		task.RevisionID = ""
		task.Type = "RESTART"
		if _, err = f.core.store.CreateDeploymentOperation(ctx, &task); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := f.core.domainAttachOperation(ctx, f.service, binding.ID, "latest-retry", nil)
	if err != nil {
		t.Fatal(err)
	}
	latest.CreatedAt = original.CreatedAt.Add(time.Second)
	if _, err = f.core.store.CreateDeploymentOperation(ctx, &latest); err != nil {
		t.Fatal(err)
	}
	if got, err := f.core.entrypointBindingOperation(ctx, f.env.ApplicationID, binding.ID); err != nil || got != latest.ID {
		t.Fatal("earlier failed task hid the latest retry", got, err)
	}
	if got, err := f.core.entrypointBindingOperation(ctx, f.env.ApplicationID, "unrelated-route"); err != nil || got != "" {
		t.Fatal(got, err)
	}
	if confirmed, err := f.core.entrypointExecutionConfirmed(ctx, f.env.ApplicationID, f.op.RevisionID, f.op.RevisionID); err != nil || !confirmed {
		t.Fatal("history pagination lost completed workload", confirmed, err)
	}
	if confirmed, err := f.core.entrypointExecutionConfirmed(ctx, f.env.ApplicationID, "unknown-head", "unknown-head"); err != nil || confirmed {
		t.Fatal("unknown workload confirmed", confirmed, err)
	}
	// Unreadable frozen inputs keep network history unknown.
	if _, err = f.sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET encrypted_input=? WHERE id=?", []byte("corrupt"), original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.core.entrypointBindingOperation(ctx, f.env.ApplicationID, binding.ID); err == nil {
		t.Fatal("unreadable input ignored")
	}
	raw, err := f.core.keyring.Encrypt([]byte("{"), operationAAD(original.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET encrypted_input=? WHERE id=?", raw, original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.core.entrypointBindingOperation(ctx, f.env.ApplicationID, binding.ID); err == nil {
		t.Fatal("invalid operation input ignored")
	}
}

func TestEntrypointCoordinatorRequiresManagedRunningOwnership(t *testing.T) {
	for _, scenario := range []string{"no application", "recovery mode", "external application", "system application", "wrong runtime", "no active revision", "stopped", "down", "failed", "removed service", "external service", "unrelated service"} {
		t.Run(scenario, func(t *testing.T) {
			f := newEntrypointBoundaryFixture(t)
			ctx := t.Context()
			app, err := f.core.store.GetApplication(ctx, f.env.ApplicationID)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "no application":
				f.env.ApplicationID = ""
			case "recovery mode":
				f.core.config.Controller.RecoveryMode = true
			case "external application":
				app.Managed = false
			case "system application":
				app.System = true
			case "wrong runtime":
				f.env.RuntimeID = "other-runtime"
			case "no active revision":
				app.ActiveRevisionID = ""
			case "stopped":
				app.Status = model.ApplicationStopped
			case "down":
				app.Status = model.ApplicationDown
			case "failed":
				app.Status = model.ApplicationFailed
			case "removed service":
				f.service.Status = model.ServiceRemoved
			case "external service":
				f.service.Managed = false
			case "unrelated service":
				f.service.ServiceKey = "unrelated/web"
			}
			original := f.core.store
			f.core.store = entrypointInventoryStore{Store: original, app: app, services: []model.Service{f.service}}
			f.core.serviceEntrypointTick(ctx, f.env)
			bindings, err := original.ListServiceDomainBindings(ctx, f.service.ID)
			if err != nil || len(bindings) != 0 {
				t.Fatal("unmanaged or stopped workload published", bindings, err)
			}
		})
	}
}

type entrypointInventoryStore struct {
	storage.Store
	app      model.Application
	services []model.Service
}

func (s entrypointInventoryStore) GetApplication(context.Context, string) (model.Application, error) {
	return s.app, nil
}
func (s entrypointInventoryStore) ListAllServicesFiltered(context.Context, storage.ServiceFilter) ([]model.Service, error) {
	return s.services, nil
}

func TestEntrypointRetryCannotPublishThroughRecoveryOrStaleReceipt(t *testing.T) {
	for _, scenario := range []string{"recovery mode", "held", "write unavailable", "write conflict"} {
		t.Run(scenario, func(t *testing.T) {
			f := newEntrypointBoundaryFixture(t)
			ctx := t.Context()
			if _, err := f.sql.DB().ExecContext(ctx, "UPDATE gateways SET enabled=0 WHERE id=?", f.gateway.ID); err != nil {
				t.Fatal(err)
			}
			f.core.serviceEntrypointTick(ctx, f.env)
			failed := f.status(t)
			if failed.Status != "FAILED" {
				t.Fatal(failed)
			}
			original := f.core.store
			switch scenario {
			case "recovery mode":
				f.core.config.Controller.RecoveryMode = true
			case "held":
				if err := f.core.saveControl(ctx, storage.RecoveryBarrierID, "recovery", "", map[string]string{"status": "held"}, 0); err != nil {
					t.Fatal(err)
				}
			case "write unavailable":
				f.core.store = entrypointIdentityStore{Store: original, writeError: errIngressUnavailable}
			case "write conflict":
				f.core.store = entrypointIdentityStore{Store: original, writeError: storage.ErrPrecondition}
			}
			if _, err := f.core.RetryServiceEntrypoint(ctx, f.op.ID, failed.Revision); err == nil {
				t.Fatal("unsafe retry accepted")
			}
			f.core.store = original
			if after := f.status(t); after.Status != "FAILED" || after.Revision != failed.Revision {
				t.Fatal("rejected retry changed the receipt", after)
			}
		})
	}
}
