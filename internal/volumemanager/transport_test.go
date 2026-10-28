package volumemanager

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/pki"
	"github.com/yernsun/network-service-composer/internal/storage"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func volumePeer(t *testing.T, dir, ca, key, id string) (config.ControllerInternalConfig, *tls.Config) {
	t.Helper()
	uri, _ := controlplane.IdentityURI(id)
	certPath, keyPath := filepath.Join(dir, id+".pem"), filepath.Join(dir, id+".key")
	if err := pki.Issue(ca, key, certPath, keyPath, pki.CertificateOptions{CommonName: id, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, URIs: []*url.URL{uri}, Server: true, Client: true, Duration: time.Hour}); err != nil {
		t.Fatal(err)
	}
	cfg, err := controlplane.InternalTLS(ca, certPath, keyPath, id)
	if err != nil {
		t.Fatal(err)
	}
	return config.ControllerInternalConfig{CAFile: ca, CertFile: certPath, KeyFile: keyPath}, cfg
}
func TestBoundVolumeTransportAndOfflineInventory(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	ca, key := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	if err := pki.InitCA(ca, key, "volume fixture CA", time.Hour); err != nil {
		t.Fatal(err)
	}
	internal, _ := volumePeer(t, dir, ca, key, "caller")
	_, serverTLS := volumePeer(t, dir, ca, key, "worker")
	mode := "ok"
	requests := 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/internal/v1/volumes" {
			t.Error("wrong internal endpoint")
		}
		peer, err := controlplane.CertificateIdentity(r.TLS.PeerCertificates[0])
		if err != nil || peer != "caller" {
			t.Error("caller not verified")
		}
		var req Request
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.NodeID != "worker-node" {
			t.Error("target not frozen")
		}
		if mode != "ok" {
			w.WriteHeader(409)
			json.NewEncoder(w).Encode(map[string]string{"code": mode})
			return
		}
		json.NewEncoder(w).Encode(Result{NodeID: "worker-node", EngineID: "engine", Complete: true, Items: []Item{}})
	}))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()
	f.store.rt.Endpoint = "node://worker-node"
	f.m.Config.Controller.InstanceID = "caller"
	f.m.Config.Controller.Internal = internal
	binding := config.ControllerNodeConfig{InstanceID: "worker", NodeID: "worker-node", EngineID: "engine", URL: server.URL}
	f.m.Config.Controller.Nodes = []config.ControllerNodeConfig{binding}
	req := Request{RuntimeID: "rt", NodeID: "worker-node", Action: "list"}
	result, err := f.m.Do(t.Context(), req)
	if err != nil || !result.Complete {
		t.Fatalf("transport=%+v %v", result, err)
	}
	for _, tc := range []struct {
		code     string
		expected error
	}{{"volume.identity", ErrIdentity}, {"volume.blocked", ErrBlocked}, {"volume.precondition", storage.ErrPrecondition}, {"volume.conflict", storage.ErrConflict}, {"volume.not_found", storage.ErrNotFound}} {
		mode = tc.code
		_, err = f.m.Do(t.Context(), req)
		if !errors.Is(err, tc.expected) {
			t.Fatalf("%s -> %v", tc.code, err)
		}
	}
	before := requests
	f.m.Config.Controller.Nodes[0].InstanceID = "wrong-peer"
	if _, err = f.m.Do(t.Context(), req); err == nil || requests != before {
		t.Fatal("same CA impersonation reached body")
	}
	f.m.Config.Controller.Nodes[0] = binding
	server.Close()
	snapshot := nodecontrol.Report{NodeID: "worker-node", EngineID: "engine", Observation: nodecontrol.Observation{At: time.Now().Add(-time.Hour), VolumeDetails: []nodecontrol.VolumeInfo{{Name: "retained", Kind: "named"}}}}
	f.m.ReadNode = func(context.Context, string) (nodecontrol.Report, error) { return snapshot, nodecontrol.ErrStale }
	result, err = f.m.Do(t.Context(), req)
	if err != nil || result.Complete || len(result.Items) != 1 || result.Items[0].BlockedReasons[0] != "inventory_unknown" {
		t.Fatalf("offline report authorized mutation: %+v %v", result, err)
	}
	req.Action = "inspect"
	req.Name = "missing"
	if _, err = f.m.Do(t.Context(), req); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("retained missing volume=%v", err)
	}
	snapshot.EngineID = "different"
	req.Action = "list"
	if _, err = f.m.Do(t.Context(), req); err == nil {
		t.Fatal("foreign engine snapshot displayed")
	}
}
func TestVolumeInternalHandlerRequiresVerifiedPeerAndFrozenOperation(t *testing.T) {
	f := newFixture(t)
	f.m.Config.Controller.InstanceID = "self"
	f.m.Config.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: "caller", NodeID: "caller-node"}, {InstanceID: "self", NodeID: "node", EngineID: "engine"}}
	f.store.rt.Endpoint = "node://node"
	uri, _ := controlplane.IdentityURI("caller")
	cert := &x509.Certificate{URIs: []*url.URL{uri}}
	verified := &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{cert}}, PeerCertificates: []*x509.Certificate{cert}}
	call := func(method, body string, state *tls.ConnectionState) int {
		r := httptest.NewRequest(method, "/internal/v1/volumes", strings.NewReader(body))
		r.TLS = state
		w := httptest.NewRecorder()
		f.m.InternalHandler().ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cache enabled")
		}
		return w.Code
	}
	for _, state := range []*tls.ConnectionState{nil, {}, {PeerCertificates: []*x509.Certificate{cert}}, {VerifiedChains: [][]*x509.Certificate{{cert}}}, {VerifiedChains: [][]*x509.Certificate{{cert}}, PeerCertificates: []*x509.Certificate{{}}}} {
		if got := call("POST", `{"runtime_id":"rt","node_id":"node","action":"list"}`, state); got != 403 {
			t.Fatalf("unverified=%d", got)
		}
	}
	for _, body := range []string{`{`, `{ "unexpected": true }`, `{} {}`} {
		if got := call("POST", body, verified); got != 400 {
			t.Fatalf("bad JSON=%d", got)
		}
	}
	if got := call("GET", "{}", verified); got != 403 {
		t.Fatalf("GET=%d", got)
	}
	req := f.freeze(t, Request{Action: "create", Name: "data"})
	raw, _ := json.Marshal(req)
	if got := call("POST", string(raw), verified); got != 403 {
		t.Fatalf("worker mismatch=%d", got)
	}
}
func TestWorkerServiceEvidenceRequiresFreshProvisionedManager(t *testing.T) {
	f := newFixture(t)
	f.store.rt.Type = model.RuntimeSwarm
	f.store.rt.ClusterID = "cluster"
	f.m.Config.Controller.Nodes = []config.ControllerNodeConfig{{NodeID: "manager", EngineID: "manager-engine", ClusterID: "cluster"}}
	report := nodecontrol.Report{NodeID: "manager", EngineID: "manager-engine", Observation: nodecontrol.Observation{Info: system.Info{Swarm: swarm.Info{ControlAvailable: true, Cluster: &swarm.ClusterInfo{ID: "cluster"}}}, Nodes: []swarm.Node{{ID: "worker", Status: swarm.NodeStatus{State: swarm.NodeStateReady}, Spec: swarm.NodeSpec{Availability: swarm.NodeAvailabilityActive}}}, Services: []swarm.Service{{ID: "retained-service"}}}}
	f.m.ReadNode = func(context.Context, string) (nodecontrol.Report, error) { return report, nil }
	dir := t.TempDir()
	ca, key := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	if err := pki.InitCA(ca, key, "consumer fixture CA", time.Hour); err != nil {
		t.Fatal(err)
	}
	internal, _ := volumePeer(t, dir, ca, key, "caller")
	_, peerTLS := volumePeer(t, dir, ca, key, "manager")
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.Action != "consumers" || req.EngineID != "manager-engine" {
			t.Error("consumer request lacks manager identity")
		}
		_ = json.NewEncoder(w).Encode(Result{NodeID: "manager", EngineID: "manager-engine", Complete: true, Nodes: report.Observation.Nodes, Services: []swarm.Service{{ID: "new-service-not-in-report"}}})
	}))
	server.TLS = peerTLS
	server.StartTLS()
	defer server.Close()
	f.m.Config.Controller.InstanceID = "caller"
	f.m.Config.Controller.Internal = internal
	f.m.Config.Controller.Nodes[0].InstanceID = "manager"
	f.m.Config.Controller.Nodes[0].URL = server.URL
	services, err := f.m.services(t.Context(), f.store.rt, nil, system.Info{}, "worker", true)
	if err != nil || len(services) != 1 || services[0].ID != "new-service-not-in-report" {
		t.Fatalf("fresh manager=%v %v", services, err)
	}
	f.m.ReadNode = func(context.Context, string) (nodecontrol.Report, error) { return report, nodecontrol.ErrStale }
	if _, err = f.m.services(t.Context(), f.store.rt, nil, system.Info{}, "worker", true); !errors.Is(err, ErrIdentity) {
		t.Fatal("stale manager admitted")
	}
	f.m.ReadNode = func(context.Context, string) (nodecontrol.Report, error) {
		report.EngineID = "replacement"
		return report, nil
	}
	if _, err = f.m.services(t.Context(), f.store.rt, nil, system.Info{}, "worker", true); !errors.Is(err, ErrIdentity) {
		t.Fatal("manager identity changed")
	}
}
