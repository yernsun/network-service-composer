package dockerresource

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/execution/fixtures"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestNodeSDKMutationRequiresLiveAuthorityAtTheActualWrite(t *testing.T) {
	for _, kind := range []string{"config", "secret"} {
		for _, scenario := range []string{"create", "create-fenced", "delete", "delete-fenced", "quorum-lost", "foreign-engine", "foreign-node", "no-node-binding", "wrong-cluster-binding", "foreign-controller", "node-inventory-unavailable"} {
			t.Run(kind+"/"+scenario, func(t *testing.T) {
				request := Request{RuntimeID: "rt", OperationID: "op", Kind: kind, Action: "create", Name: "fixture", Content: []byte("isolated fixture content")}
				typeName := "DOCKER_RESOURCE_CREATE"
				if scenario == "delete" || scenario == "delete-fenced" {
					typeName = "DOCKER_RESOURCE_DELETE"
					request.Action, request.ID, request.AcknowledgeRestoreRisk = "delete", "existing", true
				}
				raw, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				f := fixtures.New(t, typeName, raw)
				state := &resourceEngine{info: f.Info, nodes: f.Nodes, inventoryReads: map[string]int{}}
				if request.Action == "delete" {
					labels := map[string]string{labelOwner: "nsc", labelCluster: "cluster", labelOperation: "original-create"}
					if kind == "config" {
						state.configs = []swarm.Config{{ID: request.ID, Spec: swarm.ConfigSpec{Annotations: swarm.Annotations{Name: request.Name, Labels: labels}}}}
					} else {
						state.secrets = []swarm.Secret{{ID: request.ID, Spec: swarm.SecretSpec{Annotations: swarm.Annotations{Name: request.Name, Labels: labels}}}}
					}
					if err = f.Store.RecordDockerResource(t.Context(), storage.DockerResource{ClusterID: "cluster", Kind: kind, DockerID: request.ID, Name: request.Name, CreateOperationID: "original-create", CreatedAt: time.Now().UTC()}); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "create-fenced" || scenario == "delete-fenced" {
					state.onInventoryRead = func(resource string, count int) {
						if resource == kind && count == 1 {
							if _, err := f.Store.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET attempt=attempt+1 WHERE id='op'`); err != nil {
								t.Error(err)
							}
						}
					}
				}
				if scenario == "quorum-lost" {
					state.info.Swarm.Managers = 3
				}
				if scenario == "foreign-engine" {
					state.info.ID = "replacement"
				}
				if scenario == "foreign-node" {
					state.info.Swarm.NodeID = "replacement"
				}
				if scenario == "node-inventory-unavailable" {
					state.failPath = "/nodes"
				}
				server := httptest.NewServer(state)
				defer server.Close()
				manager := Manager{Store: f.Store, Config: f.Config, Open: func(actual model.Runtime, files engine.TLSFiles) (*client.Client, error) {
					if actual.Endpoint != "unix:///var/run/docker.sock" {
						t.Error("bound request opened a different Engine", actual.Endpoint)
					}
					return engine.NewManagementClient(model.Runtime{Endpoint: server.URL}, files)
				}}
				if scenario == "no-node-binding" {
					manager.Config.Controller.Nodes = nil
				}
				if scenario == "wrong-cluster-binding" {
					manager.Config.Controller.Nodes = append([]config.ControllerNodeConfig(nil), f.Config.Controller.Nodes...)
					manager.Config.Controller.Nodes[0].ClusterID = "foreign"
				}
				if scenario == "foreign-controller" {
					manager.Config.Controller.InstanceID = "foreign"
				}
				result, err := manager.Do(f.Context, request)
				if scenario == "create" || scenario == "delete" {
					if err != nil || state.mutations.Load() != 1 || result.ID == "" {
						t.Fatal("authorized SDK mutation did not complete", result.ID, state.mutations.Load(), err)
					}
				} else {
					if err == nil || state.mutations.Load() != 0 {
						t.Fatal("lost authority reached SDK mutation", scenario, state.mutations.Load(), err)
					}
					if scenario == "create-fenced" || scenario == "delete-fenced" {
						if !errors.Is(err, execution.ErrFenced) {
							t.Fatal("last-moment authority loss was not fenced", err)
						}
					}
				}
				record, readErr := execution.Load(t.Context(), f.Store, f.Task.ID, f.Task.Attempt)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if (record.CommandStartedAt != nil) != (scenario == "create" || scenario == "delete") {
					t.Fatal("mutation marker did not describe actual write boundary", scenario)
				}
			})
		}
	}
}
