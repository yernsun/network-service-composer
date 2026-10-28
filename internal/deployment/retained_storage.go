package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/projectlife"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type storageRetention struct {
	ExpectedNode    string                       `json:"expected_node,omitempty"`
	ExpectedVolumes []string                     `json:"expected_volumes,omitempty"`
	ApplicationID   string                       `json:"application_id"`
	RuntimeID       string                       `json:"runtime_id"`
	Volumes         []projectlife.RetainedVolume `json:"volumes"`
	RecoveryPending bool                         `json:"recovery_pending,omitempty"`
}
type retentionVerifier interface {
	VerifyRetention(context.Context, model.Runtime, model.Application, []projectlife.RetainedVolume) error
}

// Docker's service command may finish before a worker's mount and health
// evidence reaches the Controller. Stateful lifecycle success requires both.
func (w *Worker) runSwarmStorageOperation(ctx context.Context, rt model.Runtime, value material, args []string) (string, error) {
	if value.revision.ConfigurationSource.DeploymentPlan.DataNodeID != "" {
		if err := w.markStorageRecoveryPending(ctx, value.application, true); err != nil {
			return "", err
		}
	}
	output, err := w.runner.Run(ctx, ".", nil, args)
	if err == nil && value.revision.ConfigurationSource.DeploymentPlan.DataNodeID != "" {
		err = w.captureRetainedStorage(ctx, rt, value.application)
	}
	return output, err
}

func retentionKey(app model.Application) string { return "application-retention:" + app.ID }
func (w *Worker) retainedStorage(ctx context.Context, app model.Application) (storageRetention, storage.ControlDocument, error) {
	doc, err := w.store.GetControlDocument(ctx, retentionKey(app))
	if err != nil {
		return storageRetention{}, doc, err
	}
	var value storageRetention
	if doc.Kind != "application-retention" || doc.ParentID != app.ID || json.Unmarshal(doc.Payload, &value) != nil || value.ApplicationID != app.ID || value.RuntimeID != app.RuntimeID || (len(value.Volumes) == 0 && !value.RecoveryPending) {
		return value, doc, projectlife.ErrDataRetention
	}
	return value, doc, nil
}
func (w *Worker) verifyRetainedStorage(ctx context.Context, app model.Application, node string) error {
	if node != "" && app.Status == model.ApplicationFailed {
		return projectlife.ErrDataRetention
	}
	value, _, err := w.retainedStorage(ctx, app)
	if errors.Is(err, storage.ErrNotFound) {
		if node != "" && app.Status == model.ApplicationFailed {
			return projectlife.ErrDataRetention
		}
		if app.ActiveRevisionID != "" {
			previous, readErr := w.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
			if readErr != nil {
				return readErr
			}
			if previous.ConfigurationSource.DeploymentPlan.DataNodeID != "" {
				return projectlife.ErrDataRetention
			}
		}
		return nil
	}
	if err != nil {
		return err
	}
	if value.RecoveryPending {
		return projectlife.ErrDataRetention
	}
	for _, v := range value.Volumes {
		if node == "" || v.NodeID != node || v.Owner != app.ProjectName {
			return projectlife.ErrDataRetention
		}
	}
	verifier, ok := w.lifecycle.(retentionVerifier)
	if !ok {
		return projectlife.ErrDataRetention
	}
	rt, err := w.store.GetRuntime(ctx, app.RuntimeID)
	if err != nil {
		return err
	}
	if err := verifier.VerifyRetention(ctx, rt, app, value.Volumes); err != nil {
		return err
	}
	services, err := w.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
	if err != nil {
		return err
	}
	observed, err := w.lifecycle.Observe(ctx, rt, app, services)
	if err != nil {
		return err
	}
	if len(observed.Retained) == 0 {
		if len(observed.Objects) > 0 {
			return projectlife.ErrDataRetention
		}
		return nil
	}
	if !sameRetainedVolumes(observed.Retained, value.Volumes) {
		return projectlife.ErrDataRetention
	}
	return nil
}
func (w *Worker) recordRetainedStorage(ctx context.Context, app model.Application, o *projectlife.Observation) error {
	previous, doc, err := w.retainedStorage(ctx, app)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if err == nil && len(previous.Volumes) > 0 {
		verifier, ok := w.lifecycle.(retentionVerifier)
		if !ok {
			return projectlife.ErrDataRetention
		}
		rt, err := w.store.GetRuntime(ctx, app.RuntimeID)
		if err != nil {
			return err
		}
		if err := verifier.VerifyRetention(ctx, rt, app, previous.Volumes); err != nil {
			return err
		}
		if len(o.Retained) == 0 {
			o.Retained = previous.Volumes
		}
		if len(o.Retained) != len(previous.Volumes) {
			return projectlife.ErrDataRetention
		}
		for _, retained := range o.Retained {
			found := false
			for _, old := range previous.Volumes {
				if retained.NodeID == old.NodeID && retained.Name == old.Name && retained.CreatedAt.Equal(old.CreatedAt) && retained.Owner == old.Owner && (old.Target == "" || retained.Target == old.Target) {
					found = true
					break
				}
			}
			if !found {
				return projectlife.ErrDataRetention
			}
		}
	}

	if len(previous.Volumes) == 0 && previous.RecoveryPending {
		if len(o.Retained) != len(previous.ExpectedVolumes) {
			return projectlife.ErrDataRetention
		}
		for _, v := range o.Retained {
			found := false
			for _, name := range previous.ExpectedVolumes {
				if name == v.Name && v.NodeID == previous.ExpectedNode {
					found = true
					break
				}
			}
			if !found {
				return projectlife.ErrDataRetention
			}
		}
	}
	if len(o.Retained) == 0 {
		return nil
	}
	kept := map[string]bool{}
	for _, v := range o.Retained {
		if v.Owner != app.ProjectName || v.NodeID == "" || v.Name == "" || v.CreatedAt.IsZero() {
			return projectlife.ErrDataRetention
		}
		kept["volume:"+v.Name] = true
	}
	for _, mount := range o.Preserved {
		if !kept[mount] {
			return projectlife.ErrDataRetention
		}
	}
	raw, err := json.Marshal(storageRetention{ApplicationID: app.ID, RuntimeID: app.RuntimeID, Volumes: o.Retained, RecoveryPending: previous.RecoveryPending})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return w.store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: retentionKey(app), Kind: "application-retention", ParentID: app.ID, Payload: raw, CreatedAt: now, UpdatedAt: now}, Expected: doc.Revision}})
}

func (w *Worker) captureRetainedStorage(ctx context.Context, rt model.Runtime, app model.Application) error {
	services, err := w.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
	if err != nil {
		return err
	}
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	recorded := false
	for {
		observed, err := w.lifecycle.Observe(ctx, rt, app, services)
		if err == nil && len(observed.Retained) > 0 {
			if !recorded {
				if err := w.recordRetainedStorage(ctx, app, &observed); err != nil {
					if !errors.Is(err, projectlife.ErrDataRetention) {
						return err
					}
				} else {
					recorded = true
				}
			}
			if recorded && observed.Unhealthy {
				return projectlife.ErrReadiness
			}
			if recorded && observed.Ready {
				return w.markStorageRecoveryPending(ctx, app, false)
			}
		}
		if err != nil && !errors.Is(err, projectlife.ErrDataRetention) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return projectlife.ErrDataRetention
		case <-ticker.C:
		}
	}
}

func (w *Worker) verifyNewStorage(ctx context.Context, rt model.Runtime, value material) error {
	node := value.revision.ConfigurationSource.DeploymentPlan.DataNodeID
	if node == "" {
		return nil
	}
	if _, _, err := w.retainedStorage(ctx, value.application); !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	verifier, ok := w.lifecycle.(interface {
		VerifyNewStorage(context.Context, model.Runtime, model.Application, string, []string) error
	})
	if !ok {
		return projectlife.ErrDataRetention
	}
	names, err := applicationx.ManagedVolumeNames(value.resolved, value.application.ProjectName)
	if err != nil {
		return err
	}
	return verifier.VerifyNewStorage(ctx, rt, value.application, node, names)
}

func (w *Worker) markStorageRecoveryPending(ctx context.Context, app model.Application, pending bool) error {
	value, doc, err := w.retainedStorage(ctx, app)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !pending && len(value.Volumes) == 0 {
		return projectlife.ErrDataRetention
	}
	value.RecoveryPending = pending
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	doc.Payload = raw
	doc.UpdatedAt = time.Now().UTC()
	return w.store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: doc, Expected: doc.Revision}})
}

// Persist uncertainty before the first Docker mutation, even when no volume
// identity can be recorded yet. A new revision cannot bypass this marker.
func (w *Worker) markNewStorageMutation(ctx context.Context, value material) error {
	if value.revision.ConfigurationSource.DeploymentPlan.DataNodeID == "" {
		return nil
	}
	_, _, err := w.retainedStorage(ctx, value.application)
	if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	app := value.application
	names, err := applicationx.ManagedVolumeNames(value.resolved, app.ProjectName)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return projectlife.ErrDataRetention
	}
	raw, err := json.Marshal(storageRetention{ApplicationID: app.ID, RuntimeID: app.RuntimeID, RecoveryPending: true, ExpectedNode: value.revision.ConfigurationSource.DeploymentPlan.DataNodeID, ExpectedVolumes: names})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return w.store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: retentionKey(app), Kind: "application-retention", ParentID: app.ID, Payload: raw, CreatedAt: now, UpdatedAt: now}}})
}

func sameRetainedVolumes(actual, frozen []projectlife.RetainedVolume) bool {
	if len(actual) != len(frozen) {
		return false
	}
	for _, v := range actual {
		found := false
		for _, old := range frozen {
			if v.NodeID == old.NodeID && v.Name == old.Name && v.CreatedAt.Equal(old.CreatedAt) && v.Owner == old.Owner && (old.Target == "" || v.Target == old.Target) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
