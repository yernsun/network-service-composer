package dockerresource

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/security"
)

func TestReadOnlyResourceInventorySurvivesOriginalManagerLossWithoutGrantingWrites(t *testing.T) {
	f := newResourceFixture(t)
	endpoint := f.runtime.Endpoint
	if _, err := f.store.DB().ExecContext(t.Context(), `UPDATE runtimes SET endpoint='node://original-node' WHERE id=?`, f.runtime.ID); err != nil {
		t.Fatal(err)
	}
	keys, err := security.NewKeyring("fixture", map[string]string{"fixture": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if err != nil {
		t.Fatal(err)
	}
	active := true
	originalLive := false
	now := time.Now().UTC()
	original := nodecontrol.Binding{InstanceID: "original", NodeID: "original-node", EngineID: "original-engine", ClusterID: f.runtime.ClusterID}
	alternate := nodecontrol.Binding{InstanceID: "alternate", NodeID: "node-fixture", EngineID: "engine-fixture", ClusterID: f.runtime.ClusterID}
	reports := &nodecontrol.Reports{Store: f.store, Keys: keys, Bindings: map[string]nodecontrol.Binding{"original": original, "alternate": alternate}, Now: func(context.Context) (time.Time, error) { return now, nil }, CheckOwner: func(_ context.Context, key string, _ int64) error {
		if active && key == nodecontrol.LockKey(alternate.NodeID) || originalLive && key == nodecontrol.LockKey(original.NodeID) {
			return nil
		}
		return nodecontrol.ErrStale
	}}
	session, err := reports.Begin(t.Context(), alternate.InstanceID, alternate.NodeID, alternate.EngineID, "boot", 11)
	if err != nil {
		t.Fatal(err)
	}
	info := system.Info{ID: alternate.EngineID, Swarm: swarm.Info{NodeID: alternate.NodeID, ControlAvailable: true, Cluster: &swarm.ClusterInfo{ID: f.runtime.ClusterID}}}
	if err = reports.Accept(t.Context(), alternate.InstanceID, nodecontrol.Report{NodeID: alternate.NodeID, EngineID: alternate.EngineID, Generation: session.Generation, Sequence: 1, Observation: nodecontrol.Observation{At: now, Complete: true, Info: info}}); err != nil {
		t.Fatal(err)
	}
	f.manager.Config.Controller.InstanceID = "alternate"
	f.manager.Config.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: original.InstanceID, NodeID: original.NodeID, EngineID: original.EngineID, ClusterID: original.ClusterID}, {InstanceID: alternate.InstanceID, NodeID: alternate.NodeID, EngineID: alternate.EngineID, ClusterID: alternate.ClusterID}}
	f.manager.Reports = reports
	f.manager.Config.Management.Enabled = false
	f.manager.Open = func(actual model.Runtime, files engine.TLSFiles) (*client.Client, error) {
		if actual.Endpoint != "unix:///var/run/docker.sock" {
			t.Error("selected manager opened an unrelated Engine", actual.Endpoint)
		}
		return engine.NewManagementClient(model.Runtime{Endpoint: endpoint}, files)
	}
	f.engine.configs = []swarm.Config{{ID: "preserved-config", Spec: swarm.ConfigSpec{Annotations: swarm.Annotations{Name: "fixture"}, Data: []byte("private-resource-content")}}}
	listed, err := f.manager.Do(t.Context(), Request{RuntimeID: f.runtime.ID, Action: "list"})
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != "preserved-config" {
		t.Fatal("same-cluster inventory failed after original Manager loss", listed, err)
	}
	uri, _ := controlplane.IdentityURI("original")
	cert := &x509.Certificate{URIs: []*url.URL{uri}}
	call := func(action string, kinds ...string) *httptest.ResponseRecorder {
		input := Request{RuntimeID: "runtime-fixture", Action: action}
		if len(kinds) > 0 {
			input.Kind = kinds[0]
		}
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/internal/v1/docker-resources", strings.NewReader(string(raw)))
		r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}, VerifiedChains: [][]*x509.Certificate{{cert}}}
		w := httptest.NewRecorder()
		f.manager.InternalHandler().ServeHTTP(w, r)
		return w
	}
	response := call("list")
	if response.Code != 200 || strings.Contains(response.Body.String(), "private-resource-content") || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(response.Code, response.Body)
	}
	if response = call("create"); response.Code != 403 {
		t.Fatal("inventory endpoint accepted mutation", response.Code)
	}
	if response = call("arbitrary-command"); response.Code != 403 {
		t.Fatal("inventory endpoint accepted arbitrary action", response.Code)
	}

	for _, kind := range []string{"config", "secret", "volume", "arbitrary-command"} {
		response = call("list", kind)
		want := 200
		if kind == "volume" || kind == "arbitrary-command" {
			want = 403
		}
		if response.Code != want {
			t.Fatal("inventory kind boundary", kind, response.Code, response.Body)
		}
	}
	// Another live Manager cannot make this local executor's stale identity safe.
	originalLive = true
	originalSession, err := reports.Begin(t.Context(), original.InstanceID, original.NodeID, original.EngineID, "boot", 12)
	if err != nil {
		t.Fatal(err)
	}
	originalInfo := system.Info{ID: original.EngineID, Swarm: swarm.Info{NodeID: original.NodeID, ControlAvailable: true, Cluster: &swarm.ClusterInfo{ID: original.ClusterID}}}
	if err = reports.Accept(t.Context(), original.InstanceID, nodecontrol.Report{NodeID: original.NodeID, EngineID: original.EngineID, Generation: originalSession.Generation, Sequence: 1, Observation: nodecontrol.Observation{At: now, Complete: true, Info: originalInfo}}); err != nil {
		t.Fatal(err)
	}
	seq := uint64(1)
	for _, mode := range []string{"lease-lost", "engine-drift", "node-drift", "no-control", "missing-cluster"} {
		self := &f.manager.Config.Controller.Nodes[1]
		self.EngineID = alternate.EngineID
		self.NodeID = alternate.NodeID
		active = true
		info.Swarm.ControlAvailable = true
		info.Swarm.Cluster = &swarm.ClusterInfo{ID: alternate.ClusterID}
		switch mode {
		case "engine-drift":
			self.EngineID = "replacement-engine"
		case "node-drift":
			self.NodeID = "replacement-node"
		case "no-control":
			info.Swarm.ControlAvailable = false
		case "missing-cluster":
			info.Swarm.Cluster = nil
		}
		seq++
		if err = reports.Accept(t.Context(), alternate.InstanceID, nodecontrol.Report{NodeID: alternate.NodeID, EngineID: alternate.EngineID, Generation: session.Generation, Sequence: seq, Observation: nodecontrol.Observation{At: now, Complete: true, Info: info}}); err != nil {
			t.Fatal(err)
		}
		if mode == "lease-lost" {
			active = false
		}
		if response = call("list"); response.Code != 403 {
			t.Fatal("unverified local identity accepted", mode, response.Code, response.Body)
		}
	}
	f.manager.Config.Controller.Nodes[1].EngineID = alternate.EngineID
	f.manager.Config.Controller.Nodes[1].NodeID = alternate.NodeID
	originalLive = false
	active = false
	if response = call("list"); response.Code == 200 {
		t.Fatal("lost collector lease kept old manager available")
	}
	if _, err = f.manager.Do(t.Context(), Request{RuntimeID: f.runtime.ID, Action: "list"}); err == nil {
		t.Fatal("public inventory reused lost-owner evidence")
	}
	raw, _ := json.Marshal(listed)
	if strings.Contains(string(raw), "private-resource-content") || f.engine.mutations.Load() != 0 {
		t.Fatal("read-only failover leaked content or wrote to Docker")
	}
}
