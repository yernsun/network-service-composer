package projectlife

import (
	"context"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func NewDocker(cfg config.Config, store storage.Store, keys *security.Keyring) Docker {
	d := Docker{Config: cfg}
	if len(cfg.Controller.Nodes) == 0 {
		return d
	}
	bindings := map[string]nodecontrol.Binding{}
	for _, n := range cfg.Controller.Nodes {
		bindings[n.InstanceID] = nodecontrol.Binding{InstanceID: n.InstanceID, NodeID: n.NodeID, EngineID: n.EngineID, ClusterID: n.ClusterID}
	}
	sqlStore, ok := store.(*storage.SQLStore)
	if !ok {
		return d
	}
	reports := nodecontrol.Reports{Store: store, Keys: keys, Bindings: bindings, Now: (controlplane.SQLState{Store: sqlStore}).DatabaseTime}
	d.ReadNode = func(ctx context.Context, node string) (NodeStorage, error) {
		bound := false
		for _, b := range bindings {
			if b.NodeID == node {
				bound = true
				break
			}
		}
		if !bound {
			return NodeStorage{}, ErrDataRetention
		}
		r, err := reports.Read(ctx, node)
		if err != nil {
			return NodeStorage{}, err
		}
		return NodeStorage{NodeID: r.NodeID, Generation: r.Generation, Sequence: r.Sequence, At: r.Observation.At, Complete: r.Observation.Complete && r.Observation.StorageComplete, Volumes: r.Observation.Volumes, VolumeOwners: r.Observation.VolumeOwners, Images: r.Observation.ImageVolumes, Containers: r.Observation.Inspections}, nil
	}
	return d
}
