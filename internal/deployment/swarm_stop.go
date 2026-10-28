package deployment

import (
	"context"
	"fmt"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/projectlife"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// STOP preserves infrastructure. A missing stack listing or a stored STOPPED
// state is not exit evidence: inspect owned services, tasks and containers.
func (w *Worker) stopSwarmApplication(ctx context.Context, runtime model.Runtime, application model.Application) (string, error) {
	services, err := w.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: application.ID})
	if err != nil {
		return "", err
	}
	observed, err := w.lifecycle.Observe(ctx, runtime, application, services)
	if err != nil {
		return "", err
	}
	// Named volumes/bind mounts can survive deletion but be replaced by empty
	// data when Swarm schedules START elsewhere. Until placement and mount reuse
	// are proven, reject before removal. Anonymous volumes are rejected by Observe.
	if len(observed.Preserved) != 0 && len(observed.Retained) == 0 {
		return "", projectlife.ErrDataRetention
	}
	if len(observed.Retained) > 0 {
		revisionID := application.ActiveRevisionID
		if revisionID == "" {
			revisionID = application.DesiredRevisionID
		}
		revision, err := w.store.GetApplicationRevision(ctx, revisionID)
		if err != nil {
			return "", err
		}
		node := revision.ConfigurationSource.DeploymentPlan.DataNodeID
		if node == "" {
			return "", projectlife.ErrDataRetention
		}
		for _, v := range observed.Retained {
			if v.NodeID != node {
				return "", projectlife.ErrDataRetention
			}
		}
	}
	if err := w.recordRetainedStorage(ctx, application, &observed); err != nil {
		return "", err
	}
	if err := w.markStorageRecoveryPending(ctx, application, true); err != nil {
		return "", err
	}
	output := fmt.Sprintf("verified %d owned runtime objects before service removal\n", len(observed.Objects))
	if err := w.lifecycle.Remove(ctx, runtime, application, services, observed, true); err != nil {
		return output, err
	}
	if err := w.markStorageRecoveryPending(ctx, application, false); err != nil {
		return output, err
	}
	return output + "verified owned services, tasks and containers removed; shared infrastructure retained\n", nil
}
