package execution

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

const RecordKind = "operation-execution"

type LockProof struct {
	Key   string `json:"key"`
	Owner int64  `json:"owner"`
}
type Reference struct {
	OperationID        string      `json:"operation_id"`
	Attempt            int         `json:"attempt"`
	LeaderID           string      `json:"leader_id"`
	LeaderTerm         int64       `json:"leader_term"`
	RuntimeID          string      `json:"runtime_id"`
	RuntimeFingerprint string      `json:"runtime_fingerprint"`
	ApplicationID      string      `json:"application_id,omitempty"`
	Namespace          string      `json:"namespace,omitempty"`
	ServiceID          string      `json:"service_id,omitempty"`
	RevisionID         string      `json:"revision_id,omitempty"`
	Type               string      `json:"type"`
	InputSHA256        string      `json:"input_sha256"`
	Locks              []LockProof `json:"locks"`
	Target             Target      `json:"target"`
}
type Record struct {
	storage.ControlDocument
	Reference        Reference  `json:"reference"`
	State            string     `json:"state"`
	Reason           string     `json:"reason,omitempty"`
	SelectedAt       time.Time  `json:"selected_at"`
	AcceptedAt       *time.Time `json:"accepted_at,omitempty"`
	CommandStartedAt *time.Time `json:"command_started_at,omitempty"`
	ExecutionOwner   int64      `json:"execution_owner,omitempty"`
	ReconciledAt     *time.Time `json:"reconciled_at,omitempty"`
	InventorySHA256  string     `json:"inventory_sha256,omitempty"`
}

func RecordID(operation string, attempt int) string {
	return "operation-execution:" + operation + ":" + strconv.Itoa(attempt)
}
func ExecutionLock(operation string, attempt int) string {
	return "operation-execution-lock:" + operation + ":" + strconv.Itoa(attempt)
}
func StorageLock(rt model.Runtime) string {
	if rt.ClusterID != "" {
		return "volume-runtime:" + rt.ClusterID
	}
	return fmt.Sprintf("volume-endpoint:%x", sha256.Sum256([]byte(rt.Endpoint)))
}
func RuntimeFingerprint(rt model.Runtime) string {
	// Display names, observations and health do not change physical ownership.
	data, _ := json.Marshal([]string{rt.ID, rt.PoolID, string(rt.Type), rt.Endpoint, rt.CredentialRef, rt.ClusterID})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
func (v *Verifier) InputDigest(op model.DeploymentOperation) (string, error) {
	if len(v.Config.Security.SessionKey) < 32 {
		return "", ErrFenced
	}
	var plaintext []byte
	if len(op.EncryptedInput) != 0 {
		if v.Keys == nil {
			return "", ErrFenced
		}
		var err error
		plaintext, _, err = v.Keys.Decrypt(op.EncryptedInput, []byte("deployment-operation:"+op.ID))
		if err != nil {
			return "", ErrFenced
		}
		defer clear(plaintext)
	}
	// A plain hash would allow a database reader to guess a low-entropy secret
	// in a resource request. This domain-separated MAC uses the shared session
	// key and remains stable when the storage encryption key is rotated.
	digest := hmac.New(sha256.New, []byte(v.Config.Security.SessionKey))
	_, _ = digest.Write([]byte("nsc-operation-input:" + op.ID + "\x00"))
	_, _ = digest.Write(plaintext)
	return hex.EncodeToString(digest.Sum(nil)), nil
}
func Load(ctx context.Context, store storage.Store, operation string, attempt int) (Record, error) {
	doc, err := store.GetControlDocument(ctx, RecordID(operation, attempt))
	if err != nil {
		return Record{}, err
	}
	var record Record
	if doc.Kind != RecordKind || doc.ParentID != operation || json.Unmarshal(doc.Payload, &record) != nil || record.Reference.OperationID != operation || record.Reference.Attempt != attempt {
		return Record{}, ErrFenced
	}
	record.ControlDocument = doc
	return record, nil
}
func Save(ctx context.Context, store storage.Store, record Record, at time.Time) error {
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: RecordID(record.Reference.OperationID, record.Reference.Attempt), Kind: RecordKind, ParentID: record.Reference.OperationID, Payload: body, CreatedAt: at, UpdatedAt: at}, Expected: record.Revision}})
}

func resourceOperation(kind string) bool {
	return kind == "VOLUME_CREATE" || kind == "VOLUME_DELETE" || kind == "DOCKER_RESOURCE_CREATE" || kind == "DOCKER_RESOURCE_DELETE"
}

// CheckClaim follows only durable task identity and live session ownership.
// Request parameters, reports and recent heartbeats cannot replace these locks.
func (v *Verifier) CheckClaim(ctx context.Context, ref Reference) (model.DeploymentOperation, model.Runtime, error) {
	if v.Config.Controller.RecoveryMode || !v.Config.Management.Enabled || v.Leader == nil || v.Now == nil || v.CheckOwner == nil || ref.LeaderID == "" || ref.LeaderTerm <= 0 || ref.Attempt <= 0 {
		return model.DeploymentOperation{}, model.Runtime{}, ErrFenced
	}
	if held, err := storage.RecoveryHeld(ctx, v.Store, "deployment", ref.OperationID); err != nil || held {
		return model.DeploymentOperation{}, model.Runtime{}, ErrFenced
	}
	if err := v.Leader.Validate(ctx, ref.LeaderID, ref.LeaderTerm); err != nil {
		return model.DeploymentOperation{}, model.Runtime{}, ErrFenced
	}
	op, err := v.Store.GetDeploymentOperation(ctx, ref.OperationID)
	if err != nil {
		return op, model.Runtime{}, ErrFenced
	}
	if op.Status != model.OperationRunning || op.WorkerID != ref.LeaderID || op.Attempt != ref.Attempt || op.RuntimeID != ref.RuntimeID || op.ApplicationID != ref.ApplicationID || op.ServiceID != ref.ServiceID || op.RevisionID != ref.RevisionID || op.Type != ref.Type {
		return op, model.Runtime{}, ErrFenced
	}
	rt, err := v.Store.GetRuntime(ctx, ref.RuntimeID)
	if err != nil || !rt.Enabled || !rt.ManagementEnabled || RuntimeFingerprint(rt) != ref.RuntimeFingerprint {
		return op, rt, ErrFenced
	}
	if _, err := v.FrozenRuntime(op, rt); err != nil {
		return op, rt, ErrFenced
	}
	digest, err := v.InputDigest(op)
	if err != nil || digest != ref.InputSHA256 {
		return op, rt, ErrFenced
	}
	now, err := v.Now(ctx)
	if err != nil || op.HeartbeatAt == nil || now.Sub(*op.HeartbeatAt) > v.Config.Management.StaleAfter.Std() || op.HeartbeatAt.After(now.Add(5*time.Second)) {
		return op, rt, ErrFenced
	}
	keys := []string{StorageLock(rt)}
	if !resourceOperation(op.Type) {
		app, err := v.Store.GetApplication(ctx, ref.ApplicationID)
		if err != nil || !app.Managed || app.System || app.RuntimeID != rt.ID || app.ProjectName != ref.Namespace {
			return op, rt, ErrFenced
		}
		if err := v.Store.CheckProjectResource(ctx, app.ID); err != nil {
			return op, rt, ErrFenced
		}
		keys = append(keys, "deployment:"+rt.ID+":"+app.ProjectName)
	} else if op.Type == "DOCKER_RESOURCE_CREATE" || op.Type == "DOCKER_RESOURCE_DELETE" {
		keys = []string{"docker-resource:" + rt.ClusterID}
	}
	if len(keys) != len(ref.Locks) {
		return op, rt, ErrFenced
	}
	for _, key := range keys {
		owner := int64(0)
		for _, proof := range ref.Locks {
			if proof.Key == key {
				owner = proof.Owner
			}
		}
		if owner <= 0 || v.CheckOwner(ctx, key, owner) != nil {
			return op, rt, ErrFenced
		}
	}
	return op, rt, nil
}

func (v *Verifier) CheckExecutor(ctx context.Context, record Record) error {
	current, err := Load(ctx, v.Store, record.Reference.OperationID, record.Reference.Attempt)
	if err != nil || current.ExecutionOwner != record.ExecutionOwner || !reflect.DeepEqual(current.Reference, record.Reference) {
		return ErrFenced
	}
	record = current
	ref := record.Reference
	_, rt, err := v.CheckClaim(ctx, ref)
	if err != nil {
		return err
	}
	if record.State != "RUNNING" || record.ExecutionOwner <= 0 || ref.Target.InstanceID != v.Config.Controller.InstanceID {
		return ErrFenced
	}
	binding, err := v.binding(ref.Target.NodeID)
	if err != nil || binding.InstanceID != ref.Target.InstanceID || binding.EngineID != ref.Target.EngineID || binding.ClusterID != ref.Target.ClusterID {
		return ErrFenced
	}
	if err = v.CheckOwner(ctx, ExecutionLock(ref.OperationID, ref.Attempt), record.ExecutionOwner); err != nil {
		return ErrFenced
	}
	report, session, err := v.observation(ctx, binding)
	if err != nil || session.Generation != ref.Target.CollectorGeneration || session.Owner != ref.Target.CollectorOwner {
		return ErrFenced
	}
	if rt.Type == model.RuntimeSwarm && rt.ClusterID != binding.ClusterID {
		return ErrFenced
	}
	if rt.Type == model.RuntimeDocker && NodeID(rt.Endpoint) != ref.Target.NodeID {
		return ErrFenced
	}
	if rt.Type == model.RuntimeSwarm {
		if ref.Type == "VOLUME_CREATE" || ref.Type == "VOLUME_DELETE" {
			if _, err := v.Select(ctx, rt); err != nil {
				return ErrUnavailable
			}
		} else if !report.Observation.Info.Swarm.ControlAvailable || report.Observation.Info.Swarm.Cluster == nil || !Quorum(report.Observation.Info, report.Observation.Nodes) {
			return ErrUnavailable
		}
	}
	return nil
}
