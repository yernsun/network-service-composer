package core

import (
	"context"
	"errors"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func (c *Core) systemApplicationReplacement(ctx context.Context, next model.Application, definition config.SystemApplicationConfig, stopped map[string]map[string]bool) (*storage.SystemApplicationReplacement, error) {
	old, err := c.store.GetApplication(ctx, next.ID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if old.RuntimeID == next.RuntimeID {
		return nil, nil
	}
	if !old.System || old.RuntimeID != definition.ReplacesRuntimeID || len(definition.ReplacesObjectIDs) == 0 {
		return nil, storage.ErrConflict
	}
	previous, err := c.store.GetRuntime(ctx, old.RuntimeID)
	if err != nil {
		return nil, err
	}
	// The supported handoff is an explicitly stopped standalone deployment.
	if previous.Type != model.RuntimeDocker {
		return nil, storage.ErrConflict
	}
	selected := map[string]bool{}
	for _, object := range definition.ReplacesObjectIDs {
		if object == "" || selected[object] || !stopped[old.RuntimeID][object] {
			return nil, storage.ErrConflict
		}
		selected[object] = true
	}
	services, err := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{RuntimeID: old.RuntimeID})
	if err != nil {
		return nil, err
	}
	result := &storage.SystemApplicationReplacement{PreviousRuntimeID: old.RuntimeID, ExpectedRevision: old.Revision, ServiceRevisions: map[string]int64{}}
	seen := map[string]bool{}
	for _, service := range services {
		if service.ApplicationID != old.ID {
			continue
		}
		if service.Managed || service.RunningInstances != 0 {
			return nil, storage.ErrConflict
		}
		endpoints, e := c.store.ListEndpoints(ctx, service.ID)
		if e != nil {
			return nil, e
		}
		matched := false
		for _, endpoint := range endpoints {
			if selected[endpoint.ExternalID] {
				if endpoint.RuntimeState == model.EndpointRunning {
					return nil, storage.ErrConflict
				}
				seen[endpoint.ExternalID] = true
				matched = true
			} else if endpoint.ReadyState != model.Removed {
				return nil, storage.ErrConflict
			}
		}
		if !matched {
			return nil, storage.ErrConflict
		}
		result.ServiceRevisions[service.ID] = service.Revision
	}
	if len(result.ServiceRevisions) == 0 || len(seen) != len(selected) {
		return nil, storage.ErrConflict
	}
	return result, nil
}
