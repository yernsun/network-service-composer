package deployment

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yernsun/network-service-composer/internal/dockerresource"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/volumemanager"
)

func (w *Worker) reconciliationTarget(ctx context.Context, rt model.Runtime, op model.DeploymentOperation) (execution.Target, error) {
	if !strings.HasPrefix(op.Type, "VOLUME_") {
		return w.execution.Select(ctx, rt)
	}
	raw, _, err := w.keyring.Decrypt(op.EncryptedInput, []byte("deployment-operation:"+op.ID))
	if err != nil {
		return execution.Target{}, err
	}
	defer clear(raw)
	var request volumemanager.Request
	if json.Unmarshal(raw, &request) != nil {
		return execution.Target{}, execution.ErrUnknown
	}
	// A Swarm manager owns cluster configuration; a volume still belongs to its
	// original Engine. Acknowledgement loss cannot authorize moving its evidence.
	return w.execution.SelectPinned(ctx, rt, request.NodeID, request.EngineID)
}

func (w *Worker) reconciliationInventory(ctx context.Context, rt model.Runtime, op model.DeploymentOperation, report nodecontrol.Report) ([]byte, error) {
	if strings.HasPrefix(op.Type, "DOCKER_RESOURCE_") {
		raw, _, err := w.keyring.Decrypt(op.EncryptedInput, []byte("deployment-operation:"+op.ID))
		if err != nil {
			return nil, err
		}
		defer clear(raw)
		var input dockerResourceInput
		if json.Unmarshal(raw, &input) != nil {
			return nil, execution.ErrUnknown
		}
		defer clear(input.Content)
		manager := dockerresource.Manager{Store: w.store, Config: w.config, Reports: w.execution.Reports, Open: w.openEngine}
		result, err := manager.Do(ctx, dockerresource.Request{RuntimeID: rt.ID, Action: "list", Kind: input.Kind})
		if err != nil {
			return nil, err
		}
		return json.Marshal(result.Items)
	}
	if strings.HasPrefix(op.Type, "VOLUME_") {
		if !report.Observation.VolumeInventoryComplete {
			return nil, execution.ErrUnavailable
		}
		return json.Marshal(report.Observation.VolumeDetails)
	}
	return json.Marshal(struct {
		Services   any
		Containers any
	}{report.Observation.Services, report.Observation.Containers})
}
