package projectlife

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
)

func remoteObservationEvidence(t *testing.T, f *engineFixture) NodeStorage {
	t.Helper()
	now := time.Now().UTC()
	raw := `{"Id":"remote-one","Image":"` + f.image + `","State":{"Running":true},"Config":{"Image":"` + f.image + `","Labels":{"com.docker.swarm.service.id":"service-one","com.docker.swarm.task.id":"task-one"}},"Mounts":[]}`
	var actual container.InspectResponse
	if err := json.Unmarshal([]byte(raw), &actual); err != nil {
		t.Fatal(err)
	}
	return NodeStorage{NodeID: "uninspected-worker", At: now, Complete: true, Volumes: map[string]time.Time{}, Images: map[string][]string{f.image: {}}, Containers: map[string]container.InspectResponse{actual.ID: actual}}
}

func TestRemoteStatelessObservationRequiresActualTaskIdentityAndNoImplicitData(t *testing.T) {
	for _, scenario := range []string{"complete", "incomplete report", "image missing", "implicit image data", "missing actual container", "container id replaced", "foreign service", "foreign task", "wrong image", "missing config", "missing state", "unexpected bind", "unexpected volume", "task declares unreviewed data"} {
		t.Run(scenario, func(t *testing.T) {
			d, rt, app, services, f := runtimeFixture(t, true)
			f.remoteTask = true
			f.mounts = nil
			evidence := remoteObservationEvidence(t, f)
			actual := evidence.Containers["remote-one"]
			switch scenario {
			case "incomplete report":
				evidence.Complete = false
			case "image missing":
				evidence.Images = nil
			case "implicit image data":
				evidence.Images[f.image] = []string{"/unmounted"}
			case "missing actual container":
				evidence.Containers = nil
			case "container id replaced":
				actual.ID = "replacement"
			case "foreign service":
				actual.Config.Labels["com.docker.swarm.service.id"] = "foreign"
			case "foreign task":
				actual.Config.Labels["com.docker.swarm.task.id"] = "foreign"
			case "wrong image":
				actual.Config.Image = "other@sha256:other"
			case "missing config":
				actual.Config = nil
			case "missing state":
				actual.State = nil
			case "unexpected bind":
				actual.Mounts = []container.MountPoint{{Type: "bind", Source: "/external", Destination: "/data"}}
			case "unexpected volume":
				actual.Mounts = []container.MountPoint{{Type: "volume", Name: "unreviewed", Destination: "/data"}}
			case "task declares unreviewed data":
				f.taskMounts = []map[string]string{{"Type": "volume", "Source": "old-data", "Target": "/old-data"}}
			}
			if evidence.Containers != nil {
				evidence.Containers["remote-one"] = actual
			}
			d.ReadNode = func(_ context.Context, node string) (NodeStorage, error) {
				if node != evidence.NodeID {
					return NodeStorage{}, ErrDataRetention
				}
				return evidence, nil
			}
			observed, err := d.Observe(t.Context(), rt, app, services)
			if scenario != "complete" {
				if !errors.Is(err, ErrDataRetention) || len(f.requests) != 0 {
					t.Fatal("remote unproven data accepted", err)
				}
				return
			}
			if err != nil || len(observed.Objects) != 3 || len(observed.Nodes) != 1 || observed.Nodes[0] != evidence.NodeID || len(observed.Preserved) != 0 || len(observed.Retained) != 0 {
				t.Fatal("stateless remote scope lost", observed, err)
			}
			// Terminal task records still require actual remote-container exit.
			f.active = false
			actual.State.Running = false
			evidence.Containers[actual.ID] = actual
			stopped, err := d.Observe(t.Context(), rt, app, services)
			if err != nil || !Stopped(stopped) {
				t.Fatal("actual stateless exit not observed", err)
			}
		})
	}
}

func TestPinnedServiceRejectsMultipleWritersAndConflictingNodePlacement(t *testing.T) {
	for _, scenario := range []string{"single fixed node", "two replicas", "global service", "missing replica count", "conflicting placement", "foreign task node"} {
		t.Run(scenario, func(t *testing.T) {
			d, rt, app, services, f := runtimeFixture(t, true)
			f.remoteTask = true
			f.dataNode = "uninspected-worker"
			evidence := remoteObservationEvidence(t, f)
			created := time.Now().UTC()
			evidence.Volumes["fixture-data"] = created
			evidence.VolumeOwners = map[string]string{"fixture-data": "fixture"}
			evidence.Images[f.image] = []string{"/data"}
			actual := evidence.Containers["remote-one"]
			actual.Mounts = []container.MountPoint{{Type: "volume", Name: "fixture-data", Destination: "/data"}}
			evidence.Containers[actual.ID] = actual
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/services") && r.Method == http.MethodGet {
					recorder := httptest.NewRecorder()
					f.serve(recorder, r)
					var values []map[string]any
					if err := json.Unmarshal(recorder.Body.Bytes(), &values); err != nil {
						t.Error(err)
						return
					}
					spec := values[0]["Spec"].(map[string]any)
					switch scenario {
					case "two replicas":
						spec["Mode"] = map[string]any{"Replicated": map[string]int{"Replicas": 2}}
					case "global service":
						spec["Mode"] = map[string]any{"Global": map[string]any{}}
					case "missing replica count":
						spec["Mode"] = map[string]any{"Replicated": map[string]any{}}
					case "conflicting placement":
						spec["TaskTemplate"].(map[string]any)["Placement"] = map[string]any{"Constraints": []string{"node.id == uninspected-worker", "node.id == foreign"}}
					case "foreign task node":
						spec["TaskTemplate"].(map[string]any)["Placement"] = map[string]any{"Constraints": []string{"node.id == another-worker"}}
					}
					w.Header().Set("API-Version", "1.54")
					_ = json.NewEncoder(w).Encode(values)
					return
				}
				f.serve(w, r)
			}))
			defer server.Close()
			rt.Endpoint = server.URL
			d.ReadNode = func(_ context.Context, node string) (NodeStorage, error) {
				v := evidence
				v.NodeID = node
				return v, nil
			}
			got, err := d.Observe(t.Context(), rt, app, services)
			if scenario == "single fixed node" {
				if err != nil || len(got.Retained) != 1 || !got.Ready {
					t.Fatal("single fixed writer rejected", err)
				}
				return
			}
			if !errors.Is(err, ErrDataRetention) || len(f.requests) != 0 {
				t.Fatal("unproven data writer accepted", err)
			}
		})
	}
}
