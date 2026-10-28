package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type entrypointHistoryStore struct {
	storage.Store
	history []model.DeploymentOperation
	err     error
}

func (s entrypointHistoryStore) ListDeploymentOperations(context.Context, string, storage.Page) ([]model.DeploymentOperation, error) {
	return s.history, s.err
}

func TestEntrypointPublicationRequiresSuccessfulWorkloadExecution(t *testing.T) {
	for _, kind := range []string{"INSTALL", "UPGRADE", "START", "REDEPLOY", "ADOPT", "STOP", "DOWN", "DOMAIN_ATTACH", "FAILED", "UNAVAILABLE"} {
		t.Run(kind, func(t *testing.T) {
			f := newEntrypointBoundaryFixture(t)
			ctx := t.Context()
			original := f.core.store
			revision, err := original.GetApplicationRevision(ctx, f.op.RevisionID)
			if err != nil {
				t.Fatal(err)
			}
			intent := legacyEntrypointIntent(f.op.ApplicationID, f.entry)
			intent.ID = "entryintent_current"
			revision.ConfigurationSource.EntrypointIntents = map[string]model.ServiceEntrypointIntent{"web": intent}
			op := f.op
			op.Type = kind
			op.Status = model.OperationSucceeded
			if kind == "FAILED" {
				op.Type, op.Status = "INSTALL", model.OperationFailed
			}
			history := entrypointHistoryStore{Store: original, history: []model.DeploymentOperation{op}}
			if kind == "UNAVAILABLE" {
				history.err = errIngressUnavailable
			}
			f.core.store = entrypointIdentityStore{Store: history, revisions: map[string]model.ApplicationRevision{revision.ID: revision}}
			f.core.serviceEntrypointTick(ctx, f.env)
			bindings, err := original.ListServiceDomainBindings(ctx, f.service.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := kind == "INSTALL" || kind == "UPGRADE" || kind == "START" || kind == "REDEPLOY" || kind == "ADOPT"
			if (len(bindings) == 1) != want {
				receipt, version, readErr := f.core.readEntrypointReceipt(ctx, f.op.ApplicationID, intent)
				t.Fatal("network publication violated execution evidence", kind, bindings, receipt, version, readErr)
			}
		})
	}
}

func TestEntrypointLegacyReceiptPreservesFailureAndUnknownHistory(t *testing.T) {
	f := newEntrypointBoundaryFixture(t)
	ctx := t.Context()
	intent := legacyEntrypointIntent(f.op.ApplicationID, f.entry)
	previous := f.op
	previous.ID = "previous-install"
	newer := f.op
	newer.ID = "newer-start"
	for _, saved := range []struct {
		op       model.DeploymentOperation
		attempts int
	}{{previous, 1}, {newer, 2}} {
		if err := f.core.saveControl(ctx, saved.op.ID+":entrypoint", "service-entrypoint", saved.op.ID, ServiceEntrypointStatus{Status: "FAILED", Attempts: saved.attempts, ErrorCode: "entrypoint_validation_failed"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	original := f.core.store
	unrelatedRevision, err := original.GetApplicationRevision(ctx, f.op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	unrelatedRevision.ID = "unrelated-revision"
	unrelatedRevision.ConfigurationSource.Entrypoint = nil
	unrelated := f.op
	unrelated.RevisionID = unrelatedRevision.ID
	history := entrypointHistoryStore{Store: original, history: []model.DeploymentOperation{unrelated, previous, newer, previous}}
	f.core.store = entrypointIdentityStore{Store: history, revisions: map[string]model.ApplicationRevision{unrelatedRevision.ID: unrelatedRevision}}
	status, version, err := f.core.readEntrypointReceipt(ctx, f.op.ApplicationID, intent)
	if err != nil || version != 0 || status.Status != "FAILED" || status.Attempts != 2 {
		t.Fatal("legacy failure was lost", status, version, err)
	}
	unrelatedRevision.ConfigurationSource.Entrypoint = &model.ServiceEntrypoint{Service: "api"}
	f.core.store = entrypointIdentityStore{Store: history, revisions: map[string]model.ApplicationRevision{unrelatedRevision.ID: unrelatedRevision}}
	if status, _, err = f.core.readEntrypointReceipt(ctx, f.op.ApplicationID, intent); err != nil || status.Attempts != 2 {
		t.Fatal(status, err)
	}
	history.history = []model.DeploymentOperation{{RevisionID: "missing-history-revision"}}
	f.core.store = history
	if _, _, err = f.core.readEntrypointReceipt(ctx, f.op.ApplicationID, intent); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("missing history reported as success", err)
	}
	history.history = []model.DeploymentOperation{previous}
	doc, err := original.GetControlDocument(ctx, previous.ID+":entrypoint")
	if err != nil {
		t.Fatal(err)
	}
	doc.Payload = json.RawMessage("{")
	if _, err = f.sql.DB().ExecContext(ctx, "UPDATE control_documents SET payload=? WHERE id=?", string(doc.Payload), doc.ID); err != nil {
		t.Fatal(err)
	}
	f.core.store = history
	if _, _, err = f.core.readEntrypointReceipt(ctx, f.op.ApplicationID, intent); err == nil {
		t.Fatal("corrupt historical receipt ignored")
	}
}
