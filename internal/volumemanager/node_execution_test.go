package volumemanager

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/execution/fixtures"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
)

func TestNodeVolumeMutationStaysOnFrozenEngineAndFencesLateChanges(t *testing.T) {
	for _, scenario := range []string{"create", "create-fenced", "delete", "delete-fenced", "different-request-node", "different-request-engine", "foreign-controller", "unbound-context"} {
		t.Run(scenario, func(t *testing.T) {
			docker := newFixture(t)
			request := Request{RuntimeID: "rt", NodeID: "node", EngineID: "engine", OperationID: "op", Action: "create", Name: "data"}
			kind := "VOLUME_CREATE"
			if scenario == "delete" || scenario == "delete-fenced" {
				kind = "VOLUME_DELETE"
				request.Action = "delete"
				request.AcknowledgeDataLoss = true
				existing := testVolume(request.Name)
				docker.volumes = []volume.Volume{existing}
				request.Version = nodecontrol.VolumeMetadata(existing).Version
			}
			raw, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			f := fixtures.New(t, kind, raw)
			endpoint := docker.store.rt.Endpoint
			manager := New(f.Store, f.Config, f.Keys)
			manager.Open = func(actual model.Runtime, files engine.TLSFiles) (*client.Client, error) {
				if actual.Endpoint != "unix:///var/run/docker.sock" {
					t.Error("volume opened a replacement Engine", actual.Endpoint)
				}
				return engine.NewManagementClient(model.Runtime{Endpoint: endpoint}, files)
			}
			if scenario == "create-fenced" || scenario == "delete-fenced" {
				var changed bool
				docker.beforeRead = func(path string) {
					if !changed && (strings.Contains(path, "/volumes") || strings.Contains(path, "/containers/json")) {
						changed = true
						if _, err := f.Store.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET attempt=attempt+1 WHERE id='op'`); err != nil {
							t.Error(err)
						}
					}
				}
			}
			if scenario == "different-request-node" {
				request.NodeID = "foreign"
			}
			if scenario == "different-request-engine" {
				request.EngineID = "foreign"
			}
			if scenario == "foreign-controller" {
				manager.Config.Controller.InstanceID = "foreign"
			}
			ctx := f.Context
			if scenario == "unbound-context" {
				ctx = context.Background()
			}
			result, err := manager.Do(ctx, request)
			if scenario == "create" || scenario == "delete" {
				if err != nil || docker.creates+docker.deletes != 1 || result.Name != "data" || result.NodeID != "node" || result.EngineID != "engine" {
					t.Fatal("pinned SDK operation failed", result.Name, docker.creates, docker.deletes, err)
				}
			} else {
				if err == nil || docker.creates+docker.deletes != 0 {
					t.Fatal("foreign or stale intent reached volume write", scenario, docker.creates, docker.deletes, err)
				}
				if scenario == "create-fenced" || scenario == "delete-fenced" {
					if !errors.Is(err, execution.ErrFenced) {
						t.Fatal("late volume write was not fenced", err)
					}
				}
			}
			record, err := execution.Load(t.Context(), f.Store, f.Task.ID, f.Task.Attempt)
			if err != nil {
				t.Fatal(err)
			}
			if (record.CommandStartedAt != nil) != (scenario == "create" || scenario == "delete") {
				t.Fatal("volume mutation marker was inaccurate", scenario)
			}
		})
	}
}
