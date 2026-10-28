package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/model"
)

// Reconciliation uses a fresh, complete inventory of the same physical target.
// It records inspection independently from command acceptance. A matching
// replica count alone cannot prove which rollout or restart was accepted.
func (w *Worker) reconcileNodeOperation(ctx context.Context, rt model.Runtime, op model.DeploymentOperation, previous execution.Record) error {
	target, err := w.reconciliationTarget(ctx, rt, op)
	if err != nil {
		now, clockErr := w.execution.Now(ctx)
		if clockErr != nil {
			return clockErr
		}
		return execution.Save(ctx, w.store, execution.Record{Reference: execution.Reference{OperationID: op.ID, Attempt: op.Attempt}, State: "UNKNOWN", Reason: "reconciliation_executor_unavailable", SelectedAt: now, CommandStartedAt: previous.CommandStartedAt}, now)
	}
	report, err := w.execution.Reports.ReadLive(ctx, target.NodeID)
	if err != nil {
		return err
	}
	now, err := w.execution.Now(ctx)
	if err != nil {
		return err
	}
	inventory, err := w.reconciliationInventory(ctx, rt, op, report)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(inventory)
	record := execution.Record{Reference: execution.Reference{OperationID: op.ID, Attempt: op.Attempt, Target: target}, State: "UNKNOWN", Reason: "accepted_effects_require_review", SelectedAt: now, CommandStartedAt: previous.CommandStartedAt, ReconciledAt: &target.ObservedAt, InventorySHA256: hex.EncodeToString(digest[:])}
	return execution.Save(ctx, w.store, record, now)
}
