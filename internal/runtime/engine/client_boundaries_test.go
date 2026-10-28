package engine

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"github.com/yernsun/network-service-composer/internal/model"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerTLSCredentialsRequireCompleteMaterial(t *testing.T) {
	for mask := 1; mask < 8; mask++ {
		files := TLSFiles{}
		if mask&1 != 0 {
			files.CA = "missing-ca"
		}
		if mask&2 != 0 {
			files.Certificate = "missing-cert"
		}
		if mask&4 != 0 {
			files.Key = "missing-key"
		}
		if c, e := NewMonitoringClient(model.Runtime{Endpoint: "tcp://127.0.0.1:1"}, files); e == nil || c != nil {
			t.Fatalf("partial/missing TLS material accepted: %d", mask)
		}
	}
	if provider, e := New(model.Runtime{Endpoint: "tcp://[invalid"}, "network", providerReadiness(), TLSFiles{}); e == nil || provider != nil {
		t.Fatal("invalid runtime endpoint accepted")
	}
	for _, typ := range []model.RuntimeType{model.RuntimeDocker, model.RuntimeSwarm} {
		provider, e := New(model.Runtime{Type: typ, Endpoint: "unix:///nonexistent-nsc-fixture.sock"}, "network", providerReadiness(), TLSFiles{})
		if e != nil {
			t.Fatal(e)
		}
		if e = provider.Close(); e != nil {
			t.Fatal(e)
		}
	}
}
func TestDockerClientUsesConfiguredTLSAndPreservesRemoteErrors(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("API-Version", "1.56")
		if r.URL.Path == "/_ping" {
			w.Write([]byte("OK"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/info") {
			w.Write([]byte(`{"ID":"isolated-engine","Name":"fixture"}`))
			return
		}
		w.WriteHeader(503)
		w.Write([]byte(`{"message":"isolated engine unavailable"}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	ca, cert, key := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	pair := server.TLS.Certificates[0]
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]})
	raw, e := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	if e != nil {
		t.Fatal(e)
	}
	for name, data := range map[string][]byte{ca: certPEM, cert: certPEM, key: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw})} {
		if e = os.WriteFile(name, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	c, e := NewMonitoringClient(model.Runtime{Endpoint: "tcp://" + strings.TrimPrefix(server.URL, "https://")}, TLSFiles{CA: ca, Certificate: cert, Key: key})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	info, e := c.Info(context.Background())
	if e != nil || info.ID != "isolated-engine" {
		t.Fatalf("configured TLS endpoint failed: %+v %v", info, e)
	}
	// Construction must not silently downgrade a broken certificate trust chain.
	if e = os.WriteFile(ca, []byte("invalid PEM"), 0600); e != nil {
		t.Fatal(e)
	}
	if bad, e := NewMonitoringClient(model.Runtime{Endpoint: "tcp://127.0.0.1:1"}, TLSFiles{CA: ca, Certificate: cert, Key: key}); e == nil || bad != nil {
		t.Fatal("invalid trust root accepted")
	}
	for _, operation := range []func() error{
		func() error { _, e := c.ContainerList(context.Background()); return e }, func() error { _, e := c.ContainerInspect(context.Background(), "container"); return e }, func() error { _, e := c.ServiceList(context.Background()); return e }, func() error { _, e := c.TaskList(context.Background()); return e }, func() error { _, e := c.NodeList(context.Background()); return e }, func() error { _, e := c.NetworkList(context.Background()); return e }, func() error { _, e := c.ContainerStats(context.Background(), "container"); return e }, func() error { _, e := c.NodeInspect(context.Background(), "node"); return e },
	} {
		if e := operation(); e == nil || !strings.Contains(e.Error(), "isolated engine unavailable") {
			t.Fatal("remote error hidden", e)
		}
	}
}
