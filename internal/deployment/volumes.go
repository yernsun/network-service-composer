package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"github.com/yernsun/network-service-composer/internal/volumemanager"
)

func (w *Worker) runVolumeOperation(ctx context.Context, op *model.DeploymentOperation, raw []byte) error {
	var request volumemanager.Request
	if json.Unmarshal(raw, &request) != nil {
		return w.fail(ctx, op, "volume_input_invalid", errors.New("volume input invalid"))
	}
	request.OperationID = op.ID
	manager := volumemanager.New(w.store, w.config, w.keyring)
	manager.Open = w.openEngine
	result, err := manager.Do(ctx, request)
	if err != nil {
		err = w.nodeResourceError(ctx, err)
	}
	if err != nil {
		if errors.Is(err, volumemanager.ErrIdentity) || errors.Is(err, volumemanager.ErrBlocked) || errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrPrecondition) {
			return w.fail(ctx, op, "volume_precondition_failed", errors.New("volume identity or consumer check failed"))
		}
		return err // retain encrypted input until the actual Docker result is reconciled
	}
	if request.Action == "create" {
		payload, err := json.Marshal(struct {
			RuntimeID string `json:"runtime_id"`
			NodeID    string `json:"node_id"`
			EngineID  string `json:"engine_id"`
			Name      string `json:"name"`
			Version   string `json:"version"`
		}{request.RuntimeID, result.NodeID, result.EngineID, result.Name, result.Version})
		if err != nil {
			return err
		}
		key := "volume-operation:" + op.ID
		doc, err := w.store.GetControlDocument(ctx, key)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		now := time.Now().UTC()
		err = w.store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: key, Kind: "volume-operation", ParentID: op.ID, Payload: payload, CreatedAt: now, UpdatedAt: now}, Expected: doc.Revision}})
		if err != nil {
			return err
		}
	}
	op.Status = model.OperationSucceeded
	op.RedactedLog = "Volume operation completed: " + result.Name
	return w.finish(ctx, op)
}
