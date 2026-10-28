package nginx

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
)

// Run in an isolated container with NSC_TEST_NGINX_BINARY set; no live Gateway
// or DNS infrastructure is used. The upstream enforces the console's same-origin
// rule so a rendered header regression cannot pass merely by serving HTML.
func TestForwardedAuthorityWithRealNGINX(t *testing.T) {
	binary := os.Getenv("NSC_TEST_NGINX_BINARY")
	if binary == "" {
		t.Skip("requires isolated real NGINX")
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		_, _ = io.WriteString(w, r.Host)
	}))
	defer backend.Close()
	_, rawPort, err := net.SplitHostPort(backend.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	r := NewRenderer(config.NGINXConfig{WorkerProcesses: "1"})
	snapshot := &gatewayv2.Snapshot{PoolId: "pool_test", ConfigRevision: 1, ChallengeUpstream: backend.URL,
		Domains:  []*gatewayv2.SnapshotDomain{{Id: "dom_test", Host: "console.example.test", HttpEnabled: true}},
		Services: []*gatewayv2.SnapshotService{{Id: "svc_sandbox", QualifiedName: "nsc_sandbox_fixture", DnsName: "127.0.0.1"}},
		Routes:   []*gatewayv2.SnapshotRoute{{Id: "route_test", Host: "console.example.test", Path: "/", ServiceId: "svc_sandbox", TargetPort: int32(port), UpstreamProtocol: "http", UpstreamName: "nsc_fixture", WebsocketEnabled: true}}}
	if err = r.RenderSandboxFixture(snapshot, root); err != nil {
		t.Fatal(err)
	}
	if err = r.WriteMainConfig(filepath.Join(root, "nginx.conf"), filepath.Join(root, "nginx.pid")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-p", root, "-c", "nginx.conf", "-g", "daemon off;")
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Signal(os.Interrupt); _ = cmd.Wait() }()
	for {
		conn, e := net.DialTimeout("tcp", "127.0.0.1:80", 100*time.Millisecond)
		if e == nil {
			conn.Close()
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("NGINX did not listen")
		case <-time.After(20 * time.Millisecond):
		}
	}
	client := &http.Client{Timeout: 3 * time.Second}
	for _, tc := range []struct {
		host, origin string
		status       int
	}{{"console.example.test:8200", "http://console.example.test:8200", 200}, {"console.example.test", "http://console.example.test", 200}, {"console.example.test:8200", "http://console.example.test:8201", 403}} {
		req, _ := http.NewRequest("GET", "http://127.0.0.1/", nil)
		req.Host = tc.host
		req.Header.Set("Origin", tc.origin)
		res, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(res.Body)
		res.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		if res.StatusCode != tc.status || tc.status == 200 && string(body) != tc.host {
			t.Fatalf("authority %q origin %q: %d %q", tc.host, tc.origin, res.StatusCode, body)
		}
	}
	// HTTP/1.0 absolute-form requests may omit Host; retain NGINX's host fallback.
	conn, err := net.DialTimeout("tcp", "127.0.0.1:80", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err = io.WriteString(conn, "GET http://console.example.test/ HTTP/1.0\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != 200 || string(body) != "console.example.test" {
		t.Fatalf("Host fallback: %d %q %v", res.StatusCode, body, err)
	}
}
