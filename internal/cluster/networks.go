package cluster

import (
	"context"
	"fmt"
	"time"
)

func (m *Monitor) ValidateOverlayNetworks(ctx context.Context, runtimeID string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	rt, err := m.store.GetRuntime(ctx, runtimeID)
	if err != nil {
		return err
	}
	cli, err := m.client(rt)
	if err != nil {
		return err
	}
	defer cli.Close()
	call, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	networks, err := cli.NetworkList(call)
	if err != nil {
		return err
	}
	for _, name := range names {
		found := false
		for _, network := range networks {
			if network.Name == name && network.Driver == "overlay" && network.Scope == "swarm" {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("external network %q must be an existing Swarm overlay network", name)
		}
	}
	return nil
}
