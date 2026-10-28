package deployment

import (
	"encoding/json"
	"github.com/moby/moby/api/types/volume"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/volumemanager"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVolumeWorkerReconcilesLostCreateAndPersistsOwnership(t *testing.T) {
	f := newWorkerFixture(t)
	var created volume.Volume
	creates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		if strings.HasPrefix(path, "/v") {
			path = path[strings.Index(path[1:], "/")+1:]
		}
		switch path {
		case "/_ping":
			w.Header().Set("API-Version", "1.52")
			w.Write([]byte("OK"))
		case "/info":
			json.NewEncoder(w).Encode(map[string]string{"ID": "fixture-engine"})
		case "/volumes":
			list := []volume.Volume{}
			if created.Name != "" {
				list = append(list, created)
			}
			json.NewEncoder(w).Encode(map[string]any{"Volumes": list})
		case "/volumes/create":
			var req struct {
				Name   string
				Labels map[string]string
			}
			json.NewDecoder(r.Body).Decode(&req)
			created = volume.Volume{Name: "generated", Driver: "local", Scope: "local", CreatedAt: "2026-10-01T00:00:00Z", Labels: req.Labels}
			creates++
			w.WriteHeader(503)
			json.NewEncoder(w).Encode(map[string]string{"message": "lost acknowledgement"})
		case "/containers/json":
			json.NewEncoder(w).Encode([]any{})
		default:
			t.Errorf("unexpected path %s", path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	rt := f.runtime
	rt.Endpoint = server.URL
	if err := f.store.UpdateRuntime(t.Context(), &rt, rt.Revision); err != nil {
		t.Fatal(err)
	}
	request := volumemanager.Request{RuntimeID: rt.ID, NodeID: "fixture-engine", EngineID: "fixture-engine", Action: "create"}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	op := model.DeploymentOperation{ID: id.New(id.Operation), RuntimeID: rt.ID, Type: "VOLUME_CREATE", Status: model.OperationQueued, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	op.EncryptedInput, err = f.keyring.Encrypt(raw, []byte("deployment-operation:"+op.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.CreateDeploymentOperation(t.Context(), &op); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(f.store, lease.New(f.store.DB(), f.store.Dialect()), f.keyring, f.config, nil, nil)
	if err := worker.claimAndRun(t.Context()); err == nil {
		t.Fatal("unknown Docker result became success")
	}
	pending, err := f.store.GetDeploymentOperation(t.Context(), op.ID)
	if err != nil || pending.Status != model.OperationRunning || len(pending.EncryptedInput) == 0 {
		t.Fatalf("unknown operation was discarded: %v %+v", err, pending)
	}
	if err := worker.runVolumeOperation(t.Context(), &pending, raw); err != nil {
		t.Fatal(err)
	}
	done, err := f.store.GetDeploymentOperation(t.Context(), op.ID)
	if err != nil || done.Status != model.OperationSucceeded || len(done.EncryptedInput) != 0 || creates != 1 {
		t.Fatalf("reconciliation state wrong: %v %+v creates=%d", err, done, creates)
	}
	result, err := volumemanager.New(f.store, f.config, f.keyring).Do(t.Context(), volumemanager.Request{RuntimeID: rt.ID, Action: "list"})
	if err != nil || len(result.Items) != 1 || !result.Items[0].Owned {
		t.Fatalf("ownership not persisted: %v %+v", err, result)
	}
}

func TestVolumeWorkerRecordsInvalidInputsWithoutExecutingDocker(t *testing.T) {
	for _, tc := range []struct{ name, input, code string }{
		{"invalid JSON", `{`, "volume_input_invalid"},
		{"missing frozen target", `{"action":"create"}`, "volume_precondition_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWorkerFixture(t)
			now := time.Now().UTC()
			op := model.DeploymentOperation{ID: id.New(id.Operation), RuntimeID: f.runtime.ID, Type: "VOLUME_CREATE", Status: model.OperationQueued, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			raw := tc.input
			if tc.name == "missing frozen target" {
				raw = `{"runtime_id":"` + f.runtime.ID + `","action":"create"}`
			}
			encrypted, err := f.keyring.Encrypt([]byte(raw), []byte("deployment-operation:"+op.ID))
			if err != nil {
				t.Fatal(err)
			}
			op.EncryptedInput = encrypted
			if _, err = f.store.CreateDeploymentOperation(t.Context(), &op); err != nil {
				t.Fatal(err)
			}
			worker := NewWorker(f.store, lease.New(f.store.DB(), f.store.Dialect()), f.keyring, f.config, nil, nil)
			if err = worker.claimAndRun(t.Context()); err != nil {
				t.Fatal(err)
			}
			failed, err := f.store.GetDeploymentOperation(t.Context(), op.ID)
			if err != nil || failed.Status != model.OperationFailed || failed.ErrorCode != tc.code || len(failed.EncryptedInput) != 0 {
				t.Fatalf("unsafe failure record: %v %+v", err, failed)
			}
		})
	}
}
