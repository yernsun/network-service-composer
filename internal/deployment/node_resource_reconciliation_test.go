package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/volumemanager"
)

func TestVolumeReconciliationRetainsOriginalEngineAndRequiresCompleteVolumeEvidence(t *testing.T) {
	f, w, _, _ := nodeWorkerFixture(t)
	request := volumemanager.Request{NodeID: "original-node", EngineID: "original-engine"}
	op := model.DeploymentOperation{ID: "volume-review", Type: "VOLUME_CREATE"}
	set := func(raw []byte) {
		var err error
		op.EncryptedInput, err = f.keyring.Encrypt(raw, []byte("deployment-operation:"+op.ID))
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(request)
	set(raw)
	target, err := w.reconciliationTarget(t.Context(), f.runtime, op)
	if err != nil || target.NodeID != request.NodeID || target.EngineID != request.EngineID {
		t.Fatal("volume evidence moved to another Engine", target, err)
	}
	request.EngineID = "replacement"
	raw, _ = json.Marshal(request)
	set(raw)
	if _, err = w.reconciliationTarget(t.Context(), f.runtime, op); err == nil {
		t.Fatal("replacement Engine established recovery evidence")
	}
	set([]byte("{"))
	if _, err = w.reconciliationTarget(t.Context(), f.runtime, op); !errors.Is(err, execution.ErrUnknown) {
		t.Fatal(err)
	}
	op.EncryptedInput = []byte("broken-envelope")
	if _, err = w.reconciliationTarget(t.Context(), f.runtime, op); err == nil {
		t.Fatal("unreadable input rebound the target")
	}
	report := nodecontrol.Report{Observation: nodecontrol.Observation{Complete: true, VolumeDetails: []nodecontrol.VolumeInfo{{Name: "preserved-data", Version: "metadata-digest"}}}}
	if _, err = w.reconciliationInventory(t.Context(), f.runtime, op, report); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatal("incomplete volume inventory confirmed reconciliation", err)
	}
	report.Observation.VolumeInventoryComplete = true
	inventory, err := w.reconciliationInventory(t.Context(), f.runtime, op, report)
	if err != nil || !strings.Contains(string(inventory), "preserved-data") {
		t.Fatal(string(inventory), err)
	}
	op.Type = "INSTALL"
	if _, err = w.reconciliationTarget(t.Context(), f.runtime, op); err != nil {
		t.Fatal(err)
	}
	if _, err = w.reconciliationInventory(t.Context(), f.runtime, op, report); err != nil {
		t.Fatal(err)
	}
}

func TestConfigReconciliationInspectsActualResourcesWithoutReadingOrWritingTheirContent(t *testing.T) {
	f, w, _, _ := nodeWorkerFixture(t)
	f.runtime.Type = model.RuntimeSwarm
	f.runtime.ClusterID = "original-cluster"
	if _, err := f.store.DB().ExecContext(t.Context(), `UPDATE runtimes SET runtime_type='swarm',cluster_id='original-cluster' WHERE id=?`, f.runtime.ID); err != nil {
		t.Fatal(err)
	}
	report, err := w.execution.Reports.ReadLive(t.Context(), "original-node")
	if err != nil {
		t.Fatal(err)
	}
	report.Sequence++
	report.Observation.At = time.Now().UTC()
	report.Observation.Info.Swarm.ControlAvailable = true
	report.Observation.Info.Swarm.Cluster = &swarm.ClusterInfo{ID: "original-cluster"}
	if err = w.execution.Reports.Accept(t.Context(), f.config.Controller.InstanceID, report); err != nil {
		t.Fatal(err)
	}
	var writes atomic.Int32
	var unavailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			writes.Add(1)
		}
		out.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		if strings.HasPrefix(path, "/v") {
			path = path[strings.Index(path[1:], "/")+1:]
		}
		var value any = []any{}
		switch path {
		case "/_ping":
			out.Header().Set("API-Version", client.MaxAPIVersion)
			return
		case "/info":
			value = report.Observation.Info
		case "/configs":
			if unavailable.Load() {
				out.WriteHeader(503)
				return
			}
			value = []swarm.Config{{ID: "retained-config", Spec: swarm.ConfigSpec{Annotations: swarm.Annotations{Name: "fixture"}, Data: []byte("private-source-content")}}}
		}
		if err := json.NewEncoder(out).Encode(value); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	w.openEngine = func(_ model.Runtime, files engine.TLSFiles) (*client.Client, error) {
		return engine.NewManagementClient(model.Runtime{Endpoint: server.URL}, files)
	}
	op := model.DeploymentOperation{ID: "config-review", Type: "DOCKER_RESOURCE_CREATE"}
	raw, _ := json.Marshal(dockerResourceInput{Kind: "config", Content: []byte("private-frozen-input")})
	op.EncryptedInput, err = f.keyring.Encrypt(raw, []byte("deployment-operation:"+op.ID))
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := w.reconciliationInventory(context.Background(), f.runtime, op, report)
	if err != nil || !strings.Contains(string(inventory), "retained-config") || strings.Contains(string(inventory), "private-") || writes.Load() != 0 {
		t.Fatal("resource inspection did not preserve confidentiality and ownership", string(inventory), writes.Load(), err)
	}
	unavailable.Store(true)
	if _, err = w.reconciliationInventory(t.Context(), f.runtime, op, report); err == nil {
		t.Fatal("failed config inventory became confirmed evidence")
	}
	op.EncryptedInput, err = f.keyring.Encrypt([]byte("{"), []byte("deployment-operation:"+op.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.reconciliationInventory(t.Context(), f.runtime, op, report); !errors.Is(err, execution.ErrUnknown) {
		t.Fatal(err)
	}
	op.EncryptedInput = []byte("invalid")
	if _, err = w.reconciliationInventory(t.Context(), f.runtime, op, report); err == nil {
		t.Fatal("unreadable frozen resource was inspected with guessed input")
	}
}
