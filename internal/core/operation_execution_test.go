package core

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestOperationExecutionEvidenceSeparatesConfigurationAndActualTarget(t *testing.T) {
	c, _, _ := controlFixture(t)
	op := model.DeploymentOperation{ID: "operation-evidence", RuntimeID: "runtime", ExecutionIdentity: model.RuntimeExecutionIdentity{Endpoint: "node://original", NodeID: "original", EngineID: "original-engine", ClusterID: "cluster"}}
	if v := c.operationExecutionEvidence(t.Context(), op); v == nil || v.State != "QUEUED" || v.ConfiguredNodeID != "original" || v.ActualNodeID != "" {
		t.Fatal(v)
	}
	op.Attempt = 2
	if v := c.operationExecutionEvidence(t.Context(), op); v == nil || v.State != "UNKNOWN" || v.Reason != "execution_evidence_unavailable" {
		t.Fatal(v)
	}
	now := c.now()
	record := execution.Record{Reference: execution.Reference{OperationID: op.ID, Attempt: op.Attempt, InputSHA256: "private-input-digest", Locks: []execution.LockProof{{Key: "private-lock", Owner: 99}}, Target: execution.Target{InstanceID: "alternate-controller", NodeID: "alternate-manager", EngineID: "alternate-engine", ObservedAt: now}}, State: "WAITING", Reason: "verified_executor_unavailable", SelectedAt: now, ReconciledAt: &now}
	if err := execution.Save(t.Context(), c.store, record, now); err != nil {
		t.Fatal(err)
	}
	v := c.operationExecutionEvidence(t.Context(), op)
	if v.ConfiguredNodeID != "original" || v.ActualNodeID != "alternate-manager" || v.ActualInstanceID != "alternate-controller" || v.Attempt != 2 || v.ObservedAt == nil || v.ReconciledAt == nil {
		t.Fatal(v)
	}
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-input", "private-lock", "execution_owner", "leader_term", "internal_url"} {
		if strings.Contains(string(body), secret) {
			t.Fatal("execution response disclosed private authority", string(body))
		}
	}
	c.store = routingBoundaryStore{Store: c.store, get: execution.RecordID(op.ID, op.Attempt)}
	if v = c.operationExecutionEvidence(t.Context(), op); v.State != "UNKNOWN" || v.ActualNodeID != "" {
		t.Fatal("unavailable evidence retained an actual executor", v)
	}
}

func TestOperationExecutionEvidenceHandlesLegacyTasksAndMalformedRecords(t *testing.T) {
	c, _, _ := controlFixture(t)
	op := model.DeploymentOperation{ID: "legacy-operation", RuntimeID: "runtime"}
	if c.operationExecutionEvidence(t.Context(), op) != nil {
		t.Fatal("ordinary legacy task acquired a node executor")
	}
	op.Attempt = 1
	if c.operationExecutionEvidence(t.Context(), op) != nil {
		t.Fatal("missing legacy identity was synthesized")
	}
	key := execution.RecordID(op.ID, op.Attempt)
	if err := c.saveControl(t.Context(), key, execution.RecordKind, op.ID, map[string]any{"reference": 12}, 0); err != nil {
		t.Fatal(err)
	}
	if v := c.operationExecutionEvidence(t.Context(), op); v == nil || v.State != "UNKNOWN" {
		t.Fatal(v)
	}
	doc, err := c.store.GetControlDocument(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: doc, Expected: doc.Revision, Remove: true}}); err != nil {
		t.Fatal(err)
	}
	if err = execution.Save(t.Context(), c.store, execution.Record{Reference: execution.Reference{OperationID: op.ID, Attempt: 1}, State: "WAITING", Reason: "frozen_target_requires_review"}, c.now()); err != nil {
		t.Fatal(err)
	}
	if v := c.operationExecutionEvidence(t.Context(), op); v.State != "WAITING" || v.ConfiguredNodeID != "" || v.ObservedAt != nil {
		t.Fatal("legacy target was fabricated", v)
	}
}
