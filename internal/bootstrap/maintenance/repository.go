package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type Repository struct{ Store storage.Store }

func write(id, kind, parent string, value any, expected int64) storage.ControlWrite {
	data, _ := json.Marshal(value)
	now := time.Now().UTC()
	return storage.ControlWrite{Document: storage.ControlDocument{ID: id, Kind: kind, ParentID: parent, Payload: data, CreatedAt: now, UpdatedAt: now}, Expected: expected}
}
func (r Repository) Installation(ctx context.Context) (bootstrap.Installation, storage.ControlDocument, error) {
	document, err := r.Store.GetControlDocument(ctx, bootstrap.InstallationDocumentID)
	if err != nil {
		return bootstrap.Installation{}, document, err
	}
	var value bootstrap.Installation
	if json.Unmarshal(document.Payload, &value) != nil || document.Kind != bootstrap.InstallationDocumentKind || document.ParentID != value.ID || bootstrap.ValidateInstallation(value) != nil {
		return value, document, &bootstrap.MaintenanceError{Code: "ownership_mismatch"}
	}
	return value, document, nil
}
func (r Repository) Plan(ctx context.Context, id string) (bootstrap.Plan, error) {
	document, err := r.Store.GetControlDocument(ctx, "installation-plan:"+id)
	if err != nil {
		return bootstrap.Plan{}, err
	}
	var value bootstrap.Plan
	if json.Unmarshal(document.Payload, &value) != nil || document.Kind != "system-installation-plan" || value.Hash != id || document.ParentID != value.InstallationID {
		return value, storage.ErrConflict
	}
	return value, nil
}

type storedJob struct {
	bootstrap.MaintenanceJob
	KeyHash string `json:"key_hash"`
}

func (r Repository) Job(ctx context.Context, id string) (bootstrap.MaintenanceJob, storage.ControlDocument, error) {
	document, err := r.Store.GetControlDocument(ctx, "installation-job:"+id)
	if err != nil {
		return bootstrap.MaintenanceJob{}, document, err
	}
	var value storedJob
	if json.Unmarshal(document.Payload, &value) != nil || document.Kind != "system-installation-job" || value.ID != id || document.ParentID != value.InstallationID {
		return value.MaintenanceJob, document, storage.ErrConflict
	}
	if value.ExecutionMode == "" {
		value.ExecutionMode = "service"
		if strings.HasPrefix(value.ID, "cli-") {
			value.ExecutionMode = "cli"
		}
	}
	value.Key = value.KeyHash
	value.Revision = document.Revision
	return value.MaintenanceJob, document, nil
}
func jobWrite(value bootstrap.MaintenanceJob, expected int64) storage.ControlWrite {
	value.UpdatedAt = time.Now().UTC()
	return write("installation-job:"+value.ID, "system-installation-job", value.InstallationID, storedJob{MaintenanceJob: value, KeyHash: value.Key}, expected)
}
func (r Repository) SaveJob(ctx context.Context, value *bootstrap.MaintenanceJob) error {
	current, old, err := r.Job(ctx, value.ID)
	if err != nil {
		return err
	}
	if value.Revision != old.Revision || current.Plan.Hash != value.Plan.Hash || current.InstallationID != value.InstallationID {
		return storage.ErrConflict
	}
	if (current.State == bootstrap.Succeeded || current.State == bootstrap.Failed || current.State == bootstrap.Blocked) && value.State != current.State {
		return storage.ErrConflict
	}
	value.UpdatedAt = time.Now().UTC()
	if err = r.Store.WriteControlDocuments(storage.WithSystemBootstrap(ctx), []storage.ControlWrite{jobWrite(*value, old.Revision)}); err != nil {
		return err
	}
	value.Revision = old.Revision + 1
	return nil
}
func record(value bootstrap.Installation) *bootstrap.Record {
	result := &bootstrap.Record{ID: value.ID, Revision: value.Revision, ConfigDigest: value.ConfigDigest, MaintenanceAnchor: value.MaintenanceAnchor, ClusterID: value.ClusterID, PoolID: value.PoolID, RuntimeID: value.RuntimeID, CertificateID: value.CertificateID, Nodes: value.Nodes, Topology: value.Topology, Database: value.Database, Release: value.Release, History: value.History, Resources: map[string]bootstrap.Resource{}, Operations: []bootstrap.Operation{}, MetadataLayout: value.MetadataLayout, MaintenanceUID: value.MaintenanceUID, MaintenanceID: value.MaintenanceID, TrustSHA256: value.TrustSHA256, MaintenanceExpiresAt: value.MaintenanceExpiresAt, CheckpointRevision: value.CheckpointRevision}
	for key, resource := range value.Components {
		prefix := "reserve:"
		if key == "postgresql" {
			prefix = "service:"
		}
		result.Resources[prefix+key] = resource
	}
	result.Resources["reserve:executor"], result.Resources["control"], result.Resources["secret:maintenance"] = value.Executor, value.Control, value.CredentialSecret
	return result
}
func project(value bootstrap.Installation, record *bootstrap.Record) bootstrap.Installation {
	value.Revision, value.Release, value.History, value.Topology = record.Revision, record.Release, record.History, record.Topology
	value.CheckpointRevision = record.CheckpointRevision
	value.CertificateID = record.CertificateID
	value.ConsoleFingerprint, value.ConsoleCertificateExpiresAt = record.ConsoleFingerprint, record.ConsoleCertificateExpiresAt
	value.MetadataLayout, value.MaintenanceUID, value.MaintenanceID = record.MetadataLayout, record.MaintenanceUID, record.MaintenanceID
	value.TrustSHA256, value.MaintenanceExpiresAt = record.TrustSHA256, record.MaintenanceExpiresAt
	if receipt := record.Resources["control"]; receipt.ID != "" {
		value.Control = receipt
	}
	if receipt := record.Resources["secret:maintenance"]; receipt.ID != "" {
		value.CredentialSecret = receipt
	}
	if receipt := record.Resources["reserve:executor"]; receipt.ID != "" {
		value.Executor = Latest(record, receipt.ID)
	}
	value.UpdatedAt = time.Now().UTC()
	for key, receipt := range value.Components {
		value.Components[key] = Latest(record, receipt.ID)
	}
	value.Executor = Latest(record, value.Executor.ID)
	return value
}

// Checkpoint mirrors only typed non-secret receipts. The local intent is saved
// first; failure to mirror stops before the next external mutation.
type Checkpointer struct {
	Journal    interface{ Save(*bootstrap.Record) error }
	Repository Repository
	Engine     Engine
	Job        *bootstrap.MaintenanceJob
}

func (c *Checkpointer) Save(value *bootstrap.Record) error {
	value.CheckpointRevision++
	if err := c.Journal.Save(value); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	current, document, err := c.Repository.Installation(ctx)
	if err != nil {
		return err
	}
	if current.ID != value.ID || current.MaintenanceAnchor != value.MaintenanceAnchor || current.ClusterID != value.ClusterID {
		return storage.ErrConflict
	}
	if c.Job == nil && current.OperationID != "" {
		return &bootstrap.MaintenanceError{Code: "executor_claim_changed"}
	}
	if c.Job != nil && current.OperationID != c.Job.ID {
		return &bootstrap.MaintenanceError{Code: "executor_claim_changed"}
	}
	next := project(current, value)
	writes := []storage.ControlWrite{}
	var jobRevision int64
	if c.Job != nil {
		_, jobDocument, err := c.Repository.Job(ctx, c.Job.ID)
		if err != nil {
			return err
		}
		jobRevision = jobDocument.Revision + 1
		for n := range value.Operations {
			if value.Operations[n].Key == c.Job.Key {
				op := value.Operations[n]
				c.Job.Operation = &op
				c.Job.State = op.State
				c.Job.Reason = ""
				break
			}
		}
		if c.Job.Operation != nil && (c.Job.State == bootstrap.Succeeded || c.Job.State == bootstrap.Failed) {
			next.OperationID = ""
			next.State = c.Job.State
			if c.Job.State == bootstrap.Succeeded {
				next.Health = "VERIFIED"
				next.Drift = []string{}
			}
		}
		writes = append(writes, jobWrite(*c.Job, jobDocument.Revision))
	}
	writes = append(writes, write(bootstrap.InstallationDocumentID, bootstrap.InstallationDocumentKind, value.ID, next, document.Revision))
	if err = c.Repository.Store.WriteControlDocuments(storage.WithSystemBootstrap(ctx), writes); err != nil {
		return err
	}
	if c.Job != nil {
		c.Job.Revision = jobRevision
		details, _ := json.Marshal(map[string]any{"state": c.Job.State, "action": c.Job.Plan.Action, "plan_hash": c.Job.Plan.Hash})
		event := model.AuditEvent{ID: "aud_" + bootstrap.Digest([]string{c.Job.ID, string(c.Job.State)}), Actor: c.Job.Actor, Action: "SYSTEM_" + string(c.Job.Plan.Action) + "_" + string(c.Job.State), ResourceType: "installation", ResourceID: c.Job.ID, Details: string(details), CreatedAt: time.Now().UTC()}
		if err = c.Repository.Store.AppendAuditEvent(ctx, event); err != nil && !errors.Is(err, storage.ErrConflict) {
			return err
		}
	}
	if next.Control.ID == "" {
		return nil
	}
	return mirrorControl(ctx, c.Engine, next, value)
}
func mirrorControl(ctx context.Context, engine Engine, installation bootstrap.Installation, value *bootstrap.Record) error {
	var actual struct {
		ID      string
		Version struct{ Index uint64 }
		Spec    struct {
			Name   string
			Labels map[string]string
			Data   []byte
		}
	}
	path := "/configs/" + installation.Control.ID
	if err := engine.Engine(ctx, "GET", path, nil, &actual); err != nil {
		return err
	}
	if actual.ID != installation.Control.ID || actual.Spec.Labels["io.nsc.installation"] != value.ID || bootstrap.Digest(actual.Spec.Data) != installation.Control.SpecDigest {
		return &bootstrap.MaintenanceError{Code: "checkpoint_conflict"}
	}
	if actual.Spec.Labels == nil {
		actual.Spec.Labels = map[string]string{}
	}
	actual.Spec.Labels["io.nsc.checkpoint"] = fmt.Sprint(value.CheckpointRevision)
	actual.Spec.Labels["io.nsc.revision"] = fmt.Sprint(value.Revision)
	actual.Spec.Labels["io.nsc.state"] = string(installation.State)
	if len(value.Operations) > 0 {
		actual.Spec.Labels["io.nsc.operation"] = value.Operations[len(value.Operations)-1].ID
		actual.Spec.Labels["io.nsc.state"] = string(value.Operations[len(value.Operations)-1].State)
	}
	return engine.Engine(ctx, "POST", fmt.Sprintf("%s/update?version=%d", path, actual.Version.Index), actual.Spec, nil)
}

// ClaimCLI shares the database claim with Web maintenance; the caller already
// holds the original node-local journal lock. It never launches another runner.
func (r Repository) ClaimCLI(ctx context.Context, plan bootstrap.Plan, key, actor string) (bootstrap.MaintenanceJob, error) {
	if !bootstrap.ValidIdempotencyKey(key) {
		return bootstrap.MaintenanceJob{}, &bootstrap.MaintenanceError{Code: "idempotency_required"}
	}
	installation, document, err := r.Installation(ctx)
	if err != nil {
		return bootstrap.MaintenanceJob{}, err
	}
	keyHash := bootstrap.Digest([]string{installation.ID, key})
	id := "cli-" + keyHash[:32]
	if existing, _, err := r.Job(ctx, id); err == nil {
		if existing.Plan.Hash != plan.Hash {
			return existing, &bootstrap.MaintenanceError{Code: "idempotency_conflict"}
		}
		return existing, nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return bootstrap.MaintenanceJob{}, err
	}
	if installation.OperationID != "" {
		return bootstrap.MaintenanceJob{}, &bootstrap.MaintenanceError{Code: "operation_active"}
	}
	if installation.Revision != plan.Revision {
		return bootstrap.MaintenanceJob{}, &bootstrap.MaintenanceError{Code: "stale_plan"}
	}
	job := bootstrap.MaintenanceJob{ExecutionMode: "cli", ID: id, InstallationID: installation.ID, State: bootstrap.Pending, Plan: plan, Key: key, Actor: actor, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	// The public document stores a digest, while CLI Executor keeps the operator's
	// idempotency key in the private journal. Normalize to one non-secret key.
	job.Key = keyHash
	installation.OperationID = id
	installation.State = bootstrap.Pending
	err = r.Store.WriteControlDocuments(storage.WithSystemBootstrap(ctx), []storage.ControlWrite{write(bootstrap.InstallationDocumentID, bootstrap.InstallationDocumentKind, installation.ID, installation, document.Revision), jobWrite(job, 0)})
	if err == nil {
		job.Revision = 1
	}
	return job, err
}
