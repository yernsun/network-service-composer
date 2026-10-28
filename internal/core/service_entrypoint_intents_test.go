package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/cluster"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/runtime/engine/enginetest"
	"github.com/yernsun/network-service-composer/internal/storage"
	"sync"
	"testing"
	"time"
)

func TestEntrypointRecoversFailedInstallThroughSuccessfulRedeploy(t *testing.T) {
	f := newEntrypointBoundaryFixture(t)
	ctx := t.Context()
	if _, err := f.sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='FAILED' WHERE id=?", f.op.ID); err != nil {
		t.Fatal(err)
	}
	app, err := f.core.store.GetApplication(ctx, f.op.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	app.ActiveRevisionID, app.Status = "", model.ApplicationFailed
	if err = f.core.store.UpdateApplication(ctx, &app, app.Revision); err != nil {
		t.Fatal(err)
	}
	f.core.serviceEntrypointTick(ctx, f.env)
	if status := f.status(t); status.Status != "PENDING" || status.OperationID != "" {
		t.Fatal(status)
	}
	retry, err := f.core.QueueApplicationOperation(ctx, app.ID, "REDEPLOY", "recover-install")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.sql.ActivateApplicationRevision(ctx, app.RuntimeID, app.ID, retry.RevisionID, retry.RevisionID, []string{f.service.ServiceKey}, nil, "", f.core.now()); err != nil {
		t.Fatal(err)
	}
	// Activation alone does not turn an unknown executor result into success.
	f.core.serviceEntrypointTick(ctx, f.env)
	if status := f.status(t); status.OperationID != "" {
		t.Fatal("unknown execution published ingress", status)
	}
	if _, err = f.sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?", retry.ID); err != nil {
		t.Fatal(err)
	}
	f.core.serviceEntrypointTick(ctx, f.env)
	first := f.status(t)
	recovered, err := f.core.ServiceEntrypointStatus(ctx, retry.ID)
	if err != nil || first.OperationID == "" || first.OperationID != recovered.OperationID || first.IntentID != recovered.IntentID {
		t.Fatal(first, recovered, err)
	}
	old, err := f.core.store.GetDeploymentOperation(ctx, f.op.ID)
	if err != nil || old.Status != model.OperationFailed {
		t.Fatal("recovery rewrote failed history", old, err)
	}
}

func TestEntrypointPersistentScaleRetainsExistingBindingAndIntent(t *testing.T) {
	f := newEntrypointBoundaryFixture(t)
	ctx := t.Context()
	client := &enginetest.Client{System: system.Info{Swarm: swarm.Info{ControlAvailable: true}}}
	f.core.Cluster = cluster.NewWithFactory(f.core.store, f.core.config, func(model.Runtime, engine.TLSFiles) (engine.MonitoringClient, error) { return client, nil })
	f.core.serviceEntrypointTick(ctx, f.env)
	before := f.status(t)
	if _, err := f.sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?", before.OperationID); err != nil {
		t.Fatal(err)
	}
	op, err := f.core.QueueServiceOperation(ctx, f.service.ID, "SCALE", 2, "persist-scale", ServiceOperationOptions{Persist: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.sql.ActivateServiceRevision(ctx, f.env.RuntimeID, op.ApplicationID, op.RevisionID, op.RevisionID, []string{f.service.ServiceKey}, nil, "", f.core.now()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?", op.ID); err != nil {
		t.Fatal(err)
	}
	f.core.serviceEntrypointTick(ctx, f.env)
	after, err := f.core.ServiceEntrypointStatus(ctx, op.ID)
	if err != nil || before.IntentID != after.IntentID || before.BindingID != after.BindingID || before.OperationID != after.OperationID || after.Status != "SUCCEEDED" {
		t.Fatal(before, after, err)
	}
	bindings, err := f.core.store.ListServiceDomainBindings(ctx, f.service.ID)
	if err != nil || len(bindings) != 1 {
		t.Fatal(bindings, err)
	}
	operations, err := f.core.store.ListDeploymentOperations(ctx, f.env.ApplicationID, storage.Page{Limit: 20})
	if err != nil || len(operations) != 3 {
		t.Fatal("scale submitted an extra attach", operations, err)
	}
}

func TestEntrypointReusesOnlyMatchingOwnedBinding(t *testing.T) {
	for _, matching := range []bool{true, false} {
		t.Run(fmt.Sprint(matching), func(t *testing.T) {
			f := newEntrypointBoundaryFixture(t)
			ctx := t.Context()
			input := entrypointBinding(&f.entry)
			input.IdempotencyKey = "manual-binding"
			if !matching {
				input.Route.Backend.Port = 9090
			}
			binding, task, err := f.core.CreateDomainBinding(ctx, f.service.ID, input)
			if err != nil {
				t.Fatal(err)
			}
			route := binding.Route
			route.Status = model.RouteActive
			if err = f.core.store.UpdateRoute(ctx, &route, route.Revision); err != nil {
				t.Fatal(err)
			}
			if _, err = f.sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?", task.ID); err != nil {
				t.Fatal(err)
			}
			f.core.serviceEntrypointTick(ctx, f.env)
			status := f.status(t)
			if matching && (status.Status != "SUCCEEDED" || status.BindingID != binding.ID || status.OperationID != task.ID) {
				t.Fatal(status)
			}
			if !matching && (status.Status != "FAILED" || status.ErrorCode != "entrypoint_binding_conflict") {
				t.Fatal(status)
			}
			bindings, err := f.core.store.ListServiceDomainBindings(ctx, f.service.ID)
			if err != nil || len(bindings) != 1 || bindings[0].Route.TargetPort != input.Route.Backend.Port {
				t.Fatal("reconciliation overwrote binding", bindings, err)
			}
		})
	}
}

func TestEntrypointConcurrentReconciliationAndExplicitDetach(t *testing.T) {
	f := newEntrypointBoundaryFixture(t)
	ctx := t.Context()
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() { defer workers.Done(); f.core.serviceEntrypointTick(ctx, f.env) }()
	}
	workers.Wait()
	f.core.serviceEntrypointTick(ctx, f.env)
	status := f.status(t)
	if status.OperationID == "" {
		t.Fatal(status)
	}
	operations, err := f.core.store.ListDeploymentOperations(ctx, f.env.ApplicationID, storage.Page{Limit: 20})
	if err != nil || len(operations) != 2 {
		t.Fatal("concurrent publication duplicated work", operations, err)
	}
	if _, err = f.sql.DB().ExecContext(ctx, "UPDATE routes SET route_status='DISABLED',enabled=0 WHERE id=?", status.BindingID); err != nil {
		t.Fatal(err)
	}
	f.core.serviceEntrypointTick(ctx, f.env)
	if after := f.status(t); after.Status != "DISABLED" || after.OperationID != status.OperationID {
		t.Fatal("explicit detach resurrected", after)
	}
	if _, err = f.core.RetryServiceEntrypoint(ctx, f.op.ID, status.Revision); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("detached ingress retried", err)
	}
}

func TestEntrypointStalePresenceAndRecoveryBarrierPreventPublication(t *testing.T) {
	for _, scenario := range []string{"stale", "missing", "recovery"} {
		t.Run(scenario, func(t *testing.T) {
			f := newEntrypointBoundaryFixture(t)
			ctx := t.Context()
			switch scenario {
			case "stale":
				clock := f.core.now().Add(time.Hour)
				f.core.now = func() time.Time { return clock }
			case "missing":
				if _, err := f.sql.DB().ExecContext(ctx, "DELETE FROM control_documents WHERE id=?", storage.RuntimePresenceID(f.env.RuntimeID)); err != nil {
					t.Fatal(err)
				}
			case "recovery":
				if err := f.core.saveControl(ctx, storage.RecoveryBarrierID, "recovery", "", map[string]string{"status": "held"}, 0); err != nil {
					t.Fatal(err)
				}
			}
			f.core.serviceEntrypointTick(ctx, f.env)
			if status := f.status(t); status.OperationID != "" || status.Status != "PENDING" {
				t.Fatal("unverified workload published ingress", status)
			}
			bindings, err := f.core.store.ListServiceDomainBindings(ctx, f.service.ID)
			if err != nil || len(bindings) != 0 {
				t.Fatal(bindings, err)
			}
		})
	}
}

func TestEntrypointLegacyReceiptMigrationIsReadOnlyUntilCoordination(t *testing.T) {
	f := newEntrypointBoundaryFixture(t)
	ctx := t.Context()
	input := entrypointBinding(&f.entry)
	input.IdempotencyKey = "entrypoint:" + f.op.ID
	binding, task, err := f.core.CreateDomainBinding(ctx, f.service.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	legacy := ServiceEntrypointStatus{Status: "SUBMITTED", Attempts: 1, BindingID: binding.ID, ServiceID: f.service.ID, OperationID: task.ID}
	if err = f.core.saveControl(ctx, f.op.ID+":entrypoint", "service-entrypoint", f.op.ID, legacy, 0); err != nil {
		t.Fatal(err)
	}
	status := f.status(t)
	if status.OperationID != task.ID {
		t.Fatal(status)
	}
	if _, err = f.core.store.GetControlDocument(ctx, status.IntentID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("read endpoint migrated receipt", err)
	}
	f.core.serviceEntrypointTick(ctx, f.env)
	doc, err := f.core.store.GetControlDocument(ctx, status.IntentID)
	if err != nil || doc.ParentID != f.env.ApplicationID {
		t.Fatal(doc, err)
	}
	var migrated ServiceEntrypointStatus
	if err = json.Unmarshal(doc.Payload, &migrated); err != nil || migrated.OperationID != task.ID {
		t.Fatal(migrated, err)
	}
	if _, err = f.core.store.GetControlDocument(ctx, f.op.ID+":entrypoint"); err != nil {
		t.Fatal("migration removed historical receipt", err)
	}
}
