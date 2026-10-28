package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type Item struct {
	Kind           string          `json:"kind"`
	ID             string          `json:"id"`
	Revision       int64           `json:"revision"`
	State          string          `json:"state"`
	InputDigest    string          `json:"input_digest"`
	Decision       string          `json:"decision"`
	Reconciliation *Reconciliation `json:"reconciliation,omitempty"`
}

type Plan struct {
	Format             int       `json:"format"`
	Fingerprint        string    `json:"fingerprint"`
	BarrierRevision    int64     `json:"barrier_revision"`
	CreatedAt          time.Time `json:"created_at"`
	ActivationReviewed bool      `json:"activation_reviewed"`
	Items              []Item    `json:"items"`
}

func inputDigest(raw []byte) string {
	value := sha256.Sum256(raw)
	return hex.EncodeToString(value[:])
}

func Prepare(ctx context.Context, store *storage.SQLStore) (Plan, error) {
	plan := Plan{Format: 1, BarrierRevision: 1, CreatedAt: time.Now().UTC(), Items: []Item{}}
	prepared, err := store.RecoveryPrepared(ctx)
	if err != nil {
		return plan, err
	}
	plan.Fingerprint, err = storage.RecoveryFingerprint(ctx, store.DB())
	if err != nil {
		return plan, err
	}
	if prepared {
		doc, err := store.GetControlDocument(ctx, storage.RecoveryBarrierID)
		if err != nil {
			return plan, err
		}
		var existing Plan
		if err = json.Unmarshal(doc.Payload, &existing); err != nil {
			return plan, err
		}
		if existing.Format != 1 || existing.Fingerprint != plan.Fingerprint {
			return plan, storage.ErrPrecondition
		}
		existing.BarrierRevision = doc.Revision
		return existing, nil
	}
	operations, err := pages(ctx, func(ctx context.Context, page storage.Page) ([]model.DeploymentOperation, error) {
		return store.ListDeploymentOperations(ctx, "", page)
	}, func(v model.DeploymentOperation) string { return v.ID })
	if err != nil {
		return plan, err
	}
	for _, operation := range operations {
		if operation.Status != model.OperationQueued && operation.Status != model.OperationRunning {
			continue
		}
		plan.Items = append(plan.Items, Item{Kind: "deployment", ID: operation.ID, Revision: operation.Revision, State: string(operation.Status), InputDigest: inputDigest(operation.EncryptedInput), Decision: "hold"})
	}
	certificates, err := pages(ctx, store.ListCertificates, func(v model.Certificate) string { return v.ID })
	if err != nil {
		return plan, err
	}
	for _, certificate := range certificates {
		if certificate.Provider != "acme" {
			continue
		}
		plan.Items = append(plan.Items, Item{Kind: "certificate", ID: certificate.ID, Revision: certificate.Revision, State: string(certificate.Status), InputDigest: inputDigest(certificate.EncryptedPrivateKey), Decision: "hold"})
	}
	domains, err := pages(ctx, store.ListDomains, func(v model.Domain) string { return v.ID })
	if err != nil {
		return plan, err
	}
	for _, domain := range domains {
		if !domain.Enabled || domain.TLSMode != model.TLSManaged || domain.CertificateID != "" {
			continue
		}
		plan.Items = append(plan.Items, Item{Kind: "domain-certificate", ID: domain.ID, Revision: domain.Revision, State: "MISSING", InputDigest: inputDigest([]byte(domain.DNSProviderRef)), Decision: "hold"})
	}
	documents, err := pages(ctx, func(ctx context.Context, page storage.Page) ([]storage.ControlDocument, error) {
		return store.ListControlDocuments(ctx, "project-deletion", "", page)
	}, func(v storage.ControlDocument) string { return v.ID })
	if err != nil {
		return plan, err
	}
	for _, document := range documents {
		var task struct {
			Status string `json:"status"`
		}
		if err = json.Unmarshal(document.Payload, &task); err != nil {
			return plan, fmt.Errorf("invalid restored deletion task %s", document.ID)
		}
		if task.Status != "QUEUED" && task.Status != "RUNNING" {
			continue
		}
		plan.Items = append(plan.Items, Item{Kind: "project-deletion", ID: document.ID, Revision: document.Revision, State: task.Status, InputDigest: inputDigest(document.Payload), Decision: "hold"})
	}
	for _, kind := range []string{"notification-event", "notification-delivery", "notification-suppression"} {
		docs, err := pages(ctx, func(ctx context.Context, page storage.Page) ([]storage.ControlDocument, error) {
			return store.ListControlDocuments(ctx, kind, "", page)
		}, func(v storage.ControlDocument) string { return v.ID })
		if err != nil {
			return plan, err
		}
		for _, document := range docs {
			var value struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(document.Payload, &value); err != nil {
				return plan, fmt.Errorf("invalid restored notification %s", document.ID)
			}
			if value.Status == "PENDING" || value.Status == "SENDING" {
				plan.Items = append(plan.Items, Item{Kind: "notification", ID: document.ID, Revision: document.Revision, State: value.Status, InputDigest: inputDigest(document.Payload), Decision: "hold"})
			}
		}
	}
	payload, err := json.Marshal(plan)
	if err != nil {
		return plan, err
	}
	writes := []storage.ControlWrite{{Document: storage.ControlDocument{ID: storage.RecoveryBarrierID, Kind: "recovery-barrier", Payload: payload, CreatedAt: plan.CreatedAt, UpdatedAt: plan.CreatedAt}}}
	for _, item := range plan.Items {
		key := storage.RecoveryHoldID(item.Kind, item.ID)
		if _, err = store.GetControlDocument(ctx, key); err == nil {
			continue
		} else if !errors.Is(err, storage.ErrNotFound) {
			return plan, err
		}
		raw, err := json.Marshal(item)
		if err != nil {
			return plan, err
		}
		writes = append(writes, storage.ControlWrite{Document: storage.ControlDocument{ID: key, Kind: "recovery-hold", ParentID: item.ID, Payload: raw, CreatedAt: plan.CreatedAt, UpdatedAt: plan.CreatedAt}})
	}
	// The CLI operates with all Controllers stopped. Recheck before persisting
	// so a changed restore cannot be mistaken for the reviewed inventory.
	current, err := storage.RecoveryFingerprint(ctx, store.DB())
	if err != nil {
		return plan, err
	}
	if current != plan.Fingerprint {
		return plan, storage.ErrPrecondition
	}
	return plan, store.WriteControlDocuments(ctx, writes)
}

type ReconciliationValidator func(context.Context, Item, *Reconciliation) error

func Release(ctx context.Context, store *storage.SQLStore, review Plan, validators ...ReconciliationValidator) error {
	doc, err := store.GetControlDocument(ctx, storage.RecoveryBarrierID)
	if err != nil {
		return err
	}
	var prepared Plan
	if err = json.Unmarshal(doc.Payload, &prepared); err != nil {
		return err
	}
	if review.Format != 1 || prepared.Format != 1 || !review.ActivationReviewed || review.Fingerprint != prepared.Fingerprint || review.BarrierRevision != doc.Revision || !review.CreatedAt.Equal(prepared.CreatedAt) || len(review.Items) != len(prepared.Items) {
		return storage.ErrPrecondition
	}
	approved := []string{}
	writes := []storage.ControlWrite{}
	identities := []storage.ReviewedExecutionIdentity{}
	for i, item := range review.Items {
		original := prepared.Items[i]
		decision := item.Decision
		proof := item.Reconciliation
		item.Decision = original.Decision
		item.Reconciliation = original.Reconciliation
		if item != original {
			return storage.ErrPrecondition
		}
		switch decision {
		case "hold":
		case "resume":
			if original.State == "RUNNING" || original.State == "AUTHORIZING" || original.State == "VALIDATING" || original.State == "RENEWING" {
				return fmt.Errorf("interrupted %s %s requires runtime reconciliation; retained on hold", original.Kind, original.ID)
			}
			approved = append(approved, storage.RecoveryHoldID(item.Kind, item.ID))
		case "reconcile":
			if original.Kind != "deployment" || original.State != "RUNNING" || len(validators) != 1 {
				return errors.New("running Application release requires live read-only runtime reconciliation")
			}
			if err := validators[0](ctx, original, proof); err != nil {
				return err
			}
			op, err := store.GetDeploymentOperation(ctx, original.ID)
			if err != nil {
				return err
			}
			if op.ExecutionIdentity.Endpoint == "" {
				if proof == nil || proof.Evidence.FrozenTarget.Endpoint == "" {
					return errors.New("legacy task requires a reviewed physical execution identity")
				}
				identities = append(identities, storage.ReviewedExecutionIdentity{OperationID: op.ID, ExpectedRevision: op.Revision, Identity: proof.Evidence.FrozenTarget})
			}
			record, recordErr := execution.Load(ctx, store, op.ID, op.Attempt)
			if recordErr != nil && !errors.Is(recordErr, storage.ErrNotFound) {
				return recordErr
			}
			if recordErr == nil {
				if proof == nil {
					return errors.New("runtime evidence is required to reconcile an execution attempt")
				}
				record.State, record.Reason, record.ReconciledAt = "RECONCILED", "runtime_effects_reviewed", &proof.Evidence.ObservedAt
				payload, err := json.Marshal(record)
				if err != nil {
					return err
				}
				record.Payload = payload
				writes = append(writes, storage.ControlWrite{Document: record.ControlDocument, Expected: record.Revision})
			}
			approved = append(approved, storage.RecoveryHoldID(item.Kind, item.ID))
		case "defer":
			if original.Kind != "project-deletion" || original.State != "RUNNING" {
				return errors.New("defer is reserved for interrupted project deletion")
			}
			deletion, err := store.GetControlDocument(ctx, original.ID)
			if err != nil {
				return err
			}
			if deletion.Revision != original.Revision || inputDigest(deletion.Payload) != original.InputDigest {
				return storage.ErrPrecondition
			}
			var task core.ProjectDeletion
			if err := json.Unmarshal(deletion.Payload, &task); err != nil {
				return err
			}
			task.Status, task.ErrorCode = "UNKNOWN", "project.deletion.interrupted"
			if len(task.Attempts) > 0 {
				now := time.Now().UTC()
				attempt := &task.Attempts[len(task.Attempts)-1]
				attempt.Status, attempt.ErrorCode, attempt.FinishedAt = "UNKNOWN", task.ErrorCode, &now
			}
			before := deletion
			before.ID = "recovery:before:" + original.ID + ":" + strconv.FormatInt(original.Revision, 10)
			before.Kind = "recovery-before"
			deletion.Payload, err = json.Marshal(task)
			if err != nil {
				return err
			}
			deletion.UpdatedAt = time.Now().UTC()
			writes = append(writes, storage.ControlWrite{Document: before}, storage.ControlWrite{Document: deletion, Expected: original.Revision})
			approved = append(approved, storage.RecoveryHoldID(item.Kind, item.ID))
		default:
			return errors.New("recovery decision must be hold, resume, reconcile or defer")
		}
	}
	payload, err := json.Marshal(review)
	if err != nil {
		return err
	}
	return store.ApplyRecoveryReviewWithIdentities(ctx, doc.Revision, review.Fingerprint, approved, payload, identities, writes...)
}

func pages[T any](ctx context.Context, fetch func(context.Context, storage.Page) ([]T, error), id func(T) string) ([]T, error) {
	result := []T{}
	page := storage.Page{Limit: 200}
	for {
		values, err := fetch(ctx, page)
		if err != nil {
			return nil, err
		}
		result = append(result, values...)
		if len(values) < page.Limit {
			return result, nil
		}
		page.After = id(values[len(values)-1])
	}
}
