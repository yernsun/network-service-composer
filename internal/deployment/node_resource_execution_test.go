package deployment

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/execution/fixtures"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/storage"
	"github.com/yernsun/network-service-composer/internal/volumemanager"
)

func TestAcceptedNodeResourcesCompleteOnlyThroughFrozenSDKContext(t *testing.T) {
	for _, kind := range []string{"DOCKER_RESOURCE_CREATE", "DOCKER_RESOURCE_DELETE", "VOLUME_CREATE", "VOLUME_DELETE"} {
		t.Run(kind, func(t *testing.T) {
			value := volume.Volume{Name: "data", Driver: "local", Scope: "local", CreatedAt: "2026-10-01T00:00:00Z"}
			var input any = dockerResourceInput{Kind: "config", Name: "fixture", Content: []byte("isolated content")}
			if kind == "DOCKER_RESOURCE_DELETE" {
				input = dockerResourceInput{Kind: "config", ID: "existing", AcknowledgeRestoreRisk: true}
			}
			if strings.HasPrefix(kind, "VOLUME_") {
				request := volumemanager.Request{RuntimeID: "rt", NodeID: "node", EngineID: "engine", OperationID: "op", Name: value.Name, Action: "create"}
				if kind == "VOLUME_DELETE" {
					request.Action = "delete"
					request.Version = nodecontrol.VolumeMetadata(value).Version
					request.AcknowledgeDataLoss = true
				}
				input = request
			}
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			f := fixtures.New(t, kind, raw)
			var mutations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				path := r.URL.Path
				if strings.HasPrefix(path, "/v") {
					path = path[strings.Index(path[1:], "/")+1:]
				}
				respond := func(v any) {
					if err := json.NewEncoder(w).Encode(v); err != nil {
						t.Error(err)
					}
				}
				switch {
				case path == "/_ping":
					w.Header().Set("API-Version", client.MaxAPIVersion)
					w.WriteHeader(200)
				case path == "/info":
					respond(f.Info)
				case path == "/nodes":
					respond(f.Nodes)
				case path == "/services" || path == "/containers/json":
					respond([]any{})
				case path == "/configs" && r.Method == "GET":
					items := []swarm.Config{}
					if kind == "DOCKER_RESOURCE_DELETE" {
						items = append(items, swarm.Config{ID: "existing", Spec: swarm.ConfigSpec{Annotations: swarm.Annotations{Name: "fixture", Labels: map[string]string{"com.nsc.docker-resource.owner": "nsc", "com.nsc.docker-resource.cluster": "cluster", "com.nsc.docker-resource.operation": "original"}}}})
					}
					respond(items)
				case path == "/configs/create" && r.Method == "POST":
					mutations.Add(1)
					respond(map[string]string{"ID": "created"})
				case path == "/configs/existing" && r.Method == "DELETE":
					mutations.Add(1)
					w.WriteHeader(204)
				case path == "/volumes" && r.Method == "GET":
					respond(map[string]any{"Volumes": []volume.Volume{}})
				case path == "/volumes/create" && r.Method == "POST":
					var create struct{ Labels map[string]string }
					if err := json.NewDecoder(r.Body).Decode(&create); err != nil {
						t.Error(err)
					}
					created := value
					created.Labels = create.Labels
					mutations.Add(1)
					respond(created)
				case path == "/volumes/data" && r.Method == "DELETE":
					mutations.Add(1)
					w.WriteHeader(204)
				case path == "/volumes/data" && r.Method == "GET":
					if kind == "VOLUME_CREATE" {
						w.WriteHeader(404)
						respond(map[string]string{"message": "not found"})
					} else {
						respond(value)
					}
				default:
					t.Error("unexpected SDK operation", r.Method, path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			if kind == "DOCKER_RESOURCE_DELETE" {
				if err = f.Store.RecordDockerResource(t.Context(), storage.DockerResource{ClusterID: "cluster", Kind: "config", DockerID: "existing", Name: "fixture", CreateOperationID: "original", CreatedAt: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
			}
			worker := NewWorkerWithRunner(f.Store, nil, f.Keys, f.Config, nil, &recordingRunner{})
			worker.openEngine = func(actual model.Runtime, files engine.TLSFiles) (*client.Client, error) {
				if actual.Endpoint != "unix:///var/run/docker.sock" {
					t.Error("accepted task escaped its Engine binding", actual.Endpoint)
				}
				return engine.NewManagementClient(model.Runtime{Endpoint: server.URL}, files)
			}
			if err = worker.executeNodeAccepted(t.Context(), f.Task, f.Runtime); err == nil {
				t.Fatal("unbound internal helper executed a resource")
			}
			if err = worker.executeNodeAccepted(f.Context, f.Task, f.Runtime); err != nil {
				t.Fatal(err)
			}
			stored, err := f.Store.GetDeploymentOperation(t.Context(), f.Task.ID)
			if err != nil || stored.Status != model.OperationSucceeded || mutations.Load() != 1 {
				t.Fatal("accepted SDK task did not complete once", stored.Status, mutations.Load(), err)
			}
			record, err := execution.Load(t.Context(), f.Store, f.Task.ID, f.Task.Attempt)
			if err != nil || record.CommandStartedAt == nil {
				t.Fatal("SDK write lost its durable marker", err)
			}
			if strings.HasPrefix(kind, "VOLUME_") && kind == "VOLUME_CREATE" {
				if _, err = f.Store.GetControlDocument(t.Context(), "volume-operation:"+f.Task.ID); err != nil {
					t.Fatal("created volume identity not retained", err)
				}
			}
		})
	}
}

func TestAcceptedResourceIdentityMismatchAndLostWriteReplyRetainIntent(t *testing.T) {
	for _, kind := range []string{"DOCKER_RESOURCE_CREATE", "VOLUME_CREATE"} {
		for _, mode := range []string{"foreign-engine", "lost-write-reply"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				var input any = dockerResourceInput{Kind: "config", Name: "fixture", Content: []byte("isolated content")}
				if kind == "VOLUME_CREATE" {
					input = volumemanager.Request{RuntimeID: "rt", NodeID: "node", EngineID: "engine", OperationID: "op", Action: "create", Name: "fixture"}
				}
				raw, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				f := fixtures.New(t, kind, raw)
				info := f.Info
				if mode == "foreign-engine" {
					info.ID = "replacement-engine"
				}
				var writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					path := r.URL.Path
					if strings.HasPrefix(path, "/v") {
						path = path[strings.Index(path[1:], "/")+1:]
					}
					w.Header().Set("Content-Type", "application/json")
					var response any = []any{}
					switch {
					case path == "/_ping":
						w.Header().Set("API-Version", client.MaxAPIVersion)
						return
					case path == "/info":
						response = info
					case path == "/nodes":
						response = f.Nodes
					case r.Method == "POST":
						writes.Add(1)
						w.WriteHeader(503)
						response = map[string]string{"message": "write accepted but reply lost"}
					case strings.HasPrefix(path, "/volumes/"):
						w.WriteHeader(404)
						response = map[string]string{"message": "not found"}
					case path == "/volumes":
						response = map[string]any{"Volumes": []volume.Volume{}}
					}
					if err := json.NewEncoder(w).Encode(response); err != nil {
						t.Error(err)
					}
				}))
				defer server.Close()
				worker := NewWorkerWithRunner(f.Store, nil, f.Keys, f.Config, nil, &recordingRunner{})
				worker.openEngine = func(_ model.Runtime, files engine.TLSFiles) (*client.Client, error) {
					return engine.NewManagementClient(model.Runtime{Endpoint: server.URL}, files)
				}
				err = worker.executeNodeAccepted(f.Context, f.Task, f.Runtime)
				want := execution.ErrUnavailable
				if mode == "lost-write-reply" {
					want = execution.ErrUnknown
				}
				if !errors.Is(err, want) {
					t.Fatal("resource infrastructure boundary became business failure", err, want)
				}
				stored, readErr := f.Store.GetDeploymentOperation(t.Context(), f.Task.ID)
				record, recordErr := execution.Load(t.Context(), f.Store, f.Task.ID, f.Task.Attempt)
				if readErr != nil || recordErr != nil || stored.Status != model.OperationRunning {
					t.Fatal("uncertainty lost durable intent", stored.Status, readErr, recordErr)
				}
				if (record.CommandStartedAt != nil) != (mode == "lost-write-reply") || writes.Load() > 1 {
					t.Fatal("SDK acknowledgement loss replayed or lost write marker", writes.Load())
				}
			})
		}
	}
}
