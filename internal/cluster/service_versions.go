package cluster

import (
	"context"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

// ServiceVersions is fresh manager evidence, never cached discovery data. It
// includes every service name so an unmanaged same-name object is a collision.
func (m *Monitor) ServiceVersions(ctx context.Context, runtimeID string) (map[string]model.ServiceVersion, error) {
	rt, err := m.store.GetRuntime(ctx, runtimeID)
	if err != nil {
		return nil, err
	}
	if !rt.Enabled || rt.Type != model.RuntimeSwarm {
		return nil, ErrUnavailable
	}
	cli, err := m.client(rt)
	if err != nil {
		return nil, err
	}
	defer cli.Close()
	bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	info, err := cli.Info(bounded)
	if err != nil {
		return nil, err
	}
	if !info.Swarm.ControlAvailable {
		return nil, ErrUnavailable
	}
	services, err := cli.ServiceList(bounded)
	if err != nil {
		return nil, err
	}
	result := map[string]model.ServiceVersion{}
	for _, service := range services {
		result[service.Spec.Name] = model.ServiceVersion{ID: service.ID, Version: service.Version.Index, Stack: service.Spec.Labels["com.docker.stack.namespace"]}
	}
	return result, nil
}
