package volumemanager

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/moby/moby/client"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/pki"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Only scripts/test-volume-swarm.sh supplies these disposable nested engines.
func TestIsolatedTwoNodeVolumeLifecycle(t *testing.T) {
	managerEndpoint, workerEndpoint := os.Getenv("NSC_VOLUME_TEST_MANAGER"), os.Getenv("NSC_VOLUME_TEST_WORKER")
	if managerEndpoint == "" || workerEndpoint == "" {
		t.Skip("requires disposable two-node volume fixture")
	}
	cli, err := engine.NewManagementClient(model.Runtime{Endpoint: managerEndpoint}, engine.TLSFiles{})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	manager, err := cli.Info(t.Context(), client.InfoOptions{})
	if err != nil || !strings.HasPrefix(manager.Info.Name, "nsc-volume-e2e-") || manager.Info.Swarm.Cluster == nil {
		t.Fatalf("not a disposable manager: %v", err)
	}
	workerCLI, err := engine.NewManagementClient(model.Runtime{Endpoint: workerEndpoint}, engine.TLSFiles{})
	if err != nil {
		t.Fatal(err)
	}
	defer workerCLI.Close()
	worker, err := workerCLI.Info(t.Context(), client.InfoOptions{})
	if err != nil || !strings.HasPrefix(worker.Info.Name, "nsc-volume-e2e-") || worker.Info.Swarm.NodeID == manager.Info.Swarm.NodeID {
		t.Fatalf("not a separate disposable worker: %v", err)
	}
	f := newFixture(t)
	f.store.rt.Type = model.RuntimeSwarm
	f.store.rt.ClusterID = manager.Info.Swarm.Cluster.ID
	f.store.rt.Endpoint = workerEndpoint
	dir := t.TempDir()
	ca, key := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	if err := pki.InitCA(ca, key, "isolated volume Manager", time.Hour); err != nil {
		t.Fatal(err)
	}
	internal, _ := volumePeer(t, dir, ca, key, "caller")
	_, managerTLS := volumePeer(t, dir, ca, key, "manager")
	relay := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.Action != "consumers" {
			w.WriteHeader(403)
			return
		}
		nodes, e := cli.NodeList(r.Context(), client.NodeListOptions{})
		if e != nil {
			w.WriteHeader(503)
			return
		}
		services, e := cli.ServiceList(r.Context(), client.ServiceListOptions{})
		if e != nil {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(Result{NodeID: manager.Info.Swarm.NodeID, EngineID: manager.Info.ID, Complete: true, Nodes: nodes.Items, Services: services.Items})
	}))
	relay.TLS = managerTLS
	relay.StartTLS()
	defer relay.Close()
	binding := config.ControllerNodeConfig{InstanceID: "manager", NodeID: manager.Info.Swarm.NodeID, EngineID: manager.Info.ID, ClusterID: f.store.rt.ClusterID, URL: relay.URL}
	f.m.Config.Controller.InstanceID = "caller"
	f.m.Config.Controller.Internal = internal
	f.m.Config.Controller.Nodes = []config.ControllerNodeConfig{binding}
	f.m.ReadNode = func(_ctx context.Context, _ string) (nodecontrol.Report, error) {
		nodes, e := cli.NodeList(_ctx, client.NodeListOptions{})
		if e != nil {
			return nodecontrol.Report{}, e
		}
		services, e := cli.ServiceList(_ctx, client.ServiceListOptions{})
		return nodecontrol.Report{NodeID: manager.Info.Swarm.NodeID, EngineID: manager.Info.ID, Observation: nodecontrol.Observation{Info: manager.Info, Nodes: nodes.Items, Services: services.Items}}, e
	}
	freeze := func(req Request, node, engineID string) Request {
		req = f.freeze(t, req)
		req.NodeID = node
		req.EngineID = engineID
		raw, _ := json.Marshal(req)
		encrypted, e := f.m.Keys.Encrypt(raw, []byte("deployment-operation:op"))
		if e != nil {
			t.Fatal(e)
		}
		f.store.ops[0].EncryptedInput = encrypted
		return req
	}
	docker := func(endpoint string, args ...string) string {
		t.Helper()
		out, e := exec.Command("docker", append([]string{"--host", endpoint}, args...)...).CombinedOutput()
		if e != nil {
			t.Fatalf("private Docker %v: %v %s", args, e, out)
		}
		return strings.TrimSpace(string(out))
	}
	named := "nsc-volume-fixture-data"
	create := freeze(Request{Action: "create", Name: named}, worker.Info.Swarm.NodeID, worker.Info.ID)
	result, err := f.m.Do(t.Context(), create)
	if err != nil || result.Name != named {
		t.Fatalf("worker create=%+v %v", result, err)
	}
	anonymous := freeze(Request{Action: "create"}, worker.Info.Swarm.NodeID, worker.Info.ID)
	result, err = f.m.Do(t.Context(), anonymous)
	if err != nil || result.Name == "" {
		t.Fatalf("anonymous create=%+v %v", result, err)
	}
	anonymousName := result.Name
	// A stopped real container remains a data consumer, even after its task exits.
	docker(workerEndpoint, "run", "--name", "nsc-volume-consumer", "-v", named+":/data", "alpine:3.24", "sh", "-c", "echo retained-marker >/data/probe")
	listReq := Request{RuntimeID: "rt", NodeID: worker.Info.Swarm.NodeID, Action: "inspect", Name: named}
	result, err = f.m.Do(t.Context(), listReq)
	if err != nil || len(result.Items[0].UsedBy) == 0 {
		t.Fatalf("stopped container reference missed: %+v %v", result, err)
	}
	deletion := freeze(Request{Action: "delete", Name: named, Version: result.Items[0].Version, AcknowledgeDataLoss: true}, worker.Info.Swarm.NodeID, worker.Info.ID)
	if _, err = f.m.Do(t.Context(), deletion); !errors.Is(err, ErrBlocked) {
		t.Fatalf("stopped data removed: %v", err)
	}
	docker(workerEndpoint, "rm", "nsc-volume-consumer")
	if got := docker(workerEndpoint, "run", "--rm", "-v", named+":/data", "alpine:3.24", "cat", "/data/probe"); got != "retained-marker" {
		t.Fatal("data did not survive stopped consumer")
	}
	docker(managerEndpoint, "service", "create", "--name", "nsc-volume-zero", "--replicas", "0", "--no-resolve-image", "--constraint", "node.id == "+worker.Info.Swarm.NodeID, "--mount", "type=volume,source="+named+",target=/data", "alpine:3.24", "sleep", "600")
	if _, err = f.m.Do(t.Context(), deletion); !errors.Is(err, ErrBlocked) {
		t.Fatalf("zero-replica Service data removed: %v", err)
	}
	// Same name on manager is a separate volume; the worker Service cannot use it.
	docker(managerEndpoint, "volume", "create", named)
	f.store.rt.Endpoint = managerEndpoint
	f.m.Config.Controller.Nodes = nil
	result, err = f.m.Do(t.Context(), Request{RuntimeID: "rt", NodeID: manager.Info.Swarm.NodeID, Action: "inspect", Name: named})
	if err != nil || len(result.Items[0].UsedBy) != 0 {
		t.Fatalf("cross-node references mixed: %+v %v", result, err)
	}
	managerDelete := freeze(Request{Action: "delete", Name: named, Version: result.Items[0].Version, AcknowledgeDataLoss: true}, manager.Info.Swarm.NodeID, manager.Info.ID)
	if _, err = f.m.Do(t.Context(), managerDelete); err != nil {
		t.Fatal(err)
	}
	docker(workerEndpoint, "volume", "inspect", named)
	docker(managerEndpoint, "service", "rm", "nsc-volume-zero")
	f.store.rt.Endpoint = workerEndpoint
	f.m.Config.Controller.Nodes = []config.ControllerNodeConfig{binding}
	deletion = freeze(Request{Action: "delete", Name: named, Version: deletion.Version, AcknowledgeDataLoss: true}, worker.Info.Swarm.NodeID, worker.Info.ID)
	if _, err = f.m.Do(t.Context(), deletion); err != nil {
		t.Fatal(err)
	}
	result, err = f.m.Do(t.Context(), Request{RuntimeID: "rt", NodeID: worker.Info.Swarm.NodeID, Action: "inspect", Name: anonymousName})
	if err != nil {
		t.Fatal(err)
	}
	anonymousDelete := freeze(Request{Action: "delete", Name: anonymousName, Version: result.Items[0].Version, AcknowledgeDataLoss: true}, worker.Info.Swarm.NodeID, worker.Info.ID)
	if _, err = f.m.Do(t.Context(), anonymousDelete); err != nil {
		t.Fatal(err)
	}
}
