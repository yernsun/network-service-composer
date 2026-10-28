package core

import (
	"context"
	"errors"
	"time"

	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// ExecutionEvidence is a display projection. Session owners, input digests,
// internal URLs and frozen commands never enter the public operation response.
type ExecutionEvidence struct {
	RuntimeID          string     `json:"runtime_id"`
	ConfiguredNodeID   string     `json:"configured_node_id,omitempty"`
	ConfiguredEngineID string     `json:"configured_engine_id,omitempty"`
	ClusterID          string     `json:"cluster_id,omitempty"`
	ActualInstanceID   string     `json:"actual_instance_id,omitempty"`
	ActualNodeID       string     `json:"actual_node_id,omitempty"`
	ActualEngineID     string     `json:"actual_engine_id,omitempty"`
	Attempt            int        `json:"attempt"`
	State              string     `json:"state"`
	Reason             string     `json:"reason,omitempty"`
	ObservedAt         *time.Time `json:"observed_at,omitempty"`
	ReconciledAt       *time.Time `json:"reconciled_at,omitempty"`
}

func (c *Core) operationExecutionEvidence(ctx context.Context, op model.DeploymentOperation) *ExecutionEvidence {
	evidence := &ExecutionEvidence{RuntimeID: op.RuntimeID, ConfiguredNodeID: op.ExecutionIdentity.NodeID, ConfiguredEngineID: op.ExecutionIdentity.EngineID, ClusterID: op.ExecutionIdentity.ClusterID, Attempt: op.Attempt, State: "QUEUED"}
	if op.Attempt == 0 {
		if execution.NodeID(op.ExecutionIdentity.Endpoint) == "" {
			return nil
		}
		return evidence
	}
	record, err := execution.Load(ctx, c.store, op.ID, op.Attempt)
	if errors.Is(err, storage.ErrNotFound) {
		if execution.NodeID(op.ExecutionIdentity.Endpoint) == "" {
			return nil
		}
		evidence.State, evidence.Reason = "UNKNOWN", "execution_evidence_unavailable"
		return evidence
	}
	if err != nil {
		evidence.State, evidence.Reason = "UNKNOWN", "execution_evidence_unavailable"
		return evidence
	}
	evidence.State, evidence.Reason = record.State, record.Reason
	evidence.ActualInstanceID, evidence.ActualNodeID, evidence.ActualEngineID = record.Reference.Target.InstanceID, record.Reference.Target.NodeID, record.Reference.Target.EngineID
	if !record.Reference.Target.ObservedAt.IsZero() {
		evidence.ObservedAt = &record.Reference.Target.ObservedAt
	}
	evidence.ReconciledAt = record.ReconciledAt
	return evidence
}
