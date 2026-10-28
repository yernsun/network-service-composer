package nginx

import (
	"context"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
)

func TestUpstreamTrustWithRealNGINX(t *testing.T) {
	binary := os.Getenv("NSC_TEST_NGINX_BINARY")
	if binary == "" {
		t.Skip("requires isolated real NGINX")
	}
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "verified upstream") }))
	defer backend.Close()
	_, portText, _ := net.SplitHostPort(backend.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	for _, scenario := range []string{"trusted", "wrong-name", "untrusted"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			ca := filepath.Join(root, "upstream-ca.crt")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: backend.Certificate().Raw}), 0o600); err != nil {
				t.Fatal(err)
			}
			peerName := backend.Certificate().DNSNames[0]
			if scenario == "wrong-name" {
				peerName = "wrong.example.test"
			}
			if scenario == "untrusted" {
				ca = ""
			}
			r := NewRenderer(config.NGINXConfig{WorkerProcesses: "1", UpstreamCAFile: ca})
			snapshot := &gatewayv2.Snapshot{ConfigRevision: 1, ChallengeUpstream: "http://controller:8080",
				Domains:  []*gatewayv2.SnapshotDomain{{Id: "domain", Host: "tls.example.test", HttpEnabled: true}},
				Services: []*gatewayv2.SnapshotService{{Id: "svc_sandbox", QualifiedName: "nsc_sandbox_fixture", DnsName: "127.0.0.1"}},
				Routes:   []*gatewayv2.SnapshotRoute{{Id: "route", Host: "tls.example.test", Path: "/", ServiceId: "svc_sandbox", TargetPort: int32(port), UpstreamProtocol: "https", UpstreamServerName: peerName, UpstreamName: "nsc_fixture"}}}
			if err := r.RenderSandboxFixture(snapshot, root); err != nil {
				t.Fatal(err)
			}
			if err := r.WriteMainConfig(filepath.Join(root, "nginx.conf"), filepath.Join(root, "nginx.pid")); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "-p", root, "-c", "nginx.conf", "-g", "daemon off;")
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Signal(os.Interrupt); _ = cmd.Wait() }()
			client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second}
			defer client.CloseIdleConnections()
			for {
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1/", nil)
				req.Host = "tls.example.test"
				res, err := client.Do(req)
				if err == nil {
					body, _ := io.ReadAll(res.Body)
					res.Body.Close()
					want := http.StatusBadGateway
					if scenario == "trusted" {
						want = http.StatusOK
					}
					if res.StatusCode != want || want == http.StatusOK && string(body) != "verified upstream" {
						t.Fatalf("TLS boundary: got %d, want %d", res.StatusCode, want)
					}
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("isolated NGINX did not become reachable")
				case <-time.After(20 * time.Millisecond):
				}
			}
		})
	}
}
