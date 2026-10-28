package nginx

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
)

func TestIPAccessWithRealNGINX(t *testing.T) {
	binary := os.Getenv("NSC_TEST_NGINX_BINARY")
	if binary == "" {
		t.Skip("requires isolated real NGINX")
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/client" {
			_, _ = io.WriteString(w, "source:"+r.Header.Get("X-Real-IP"))
			return
		}
		_, _ = io.WriteString(w, "upstream")
	}))
	defer backend.Close()
	_, portText, _ := net.SplitHostPort(backend.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	snapshot := &gatewayv2.Snapshot{ConfigRevision: 1, ChallengeUpstream: backend.URL, Domains: []*gatewayv2.SnapshotDomain{
		{Id: "dom_acl", Host: "acl.test", HttpEnabled: true, TlsMode: "disabled"},
		{Id: "dom_redirect", Host: "redirect.test", HttpEnabled: true, HttpsEnabled: true, RedirectHttpToHttps: true, TlsMode: "managed", AcmeChallenge: "http-01"},
	}, Services: []*gatewayv2.SnapshotService{{Id: "svc_sandbox", QualifiedName: "nsc_sandbox_fixture", DnsName: "127.0.0.1"}}, Routes: []*gatewayv2.SnapshotRoute{
		{Id: "route_proxy", Host: "acl.test", Path: "/proxy", Kind: "proxy", ServiceId: "svc_sandbox", UpstreamName: "nsc_fixture", TargetPort: int32(port), UpstreamProtocol: "http"},
		{Id: "route_static", Host: "acl.test", Path: "/static", Kind: "return", ResponseCode: 200, ResponseValue: "static"},
		{Id: "route_redirect", Host: "acl.test", Path: "/redirect", Kind: "redirect", ResponseCode: 308, ResponseValue: "https://example.test/"},
		{Id: "route_restricted", Host: "acl.test", Path: "/restricted", Kind: "return", ResponseCode: 200, ResponseValue: "restricted"},
		{Id: "route_http", Host: "redirect.test", Path: "/restricted", Kind: "return", ResponseCode: 200, ResponseValue: "restricted"},
	}, AccessControls: []*gatewayv2.IPAccessControl{
		{Scope: "server", ResourceId: "dom_acl", Access: []string{"127.0.0.1-127.0.0.4", "::1"}, Forbidden: []string{"127.0.0.3"}},
		{Scope: "location", ResourceId: "route_restricted", Access: []string{"127.0.0.2", "127.0.0.5"}, Forbidden: []string{"127.0.0.3"}},
		{Scope: "server", ResourceId: "dom_redirect", Access: []string{"127.0.0.1-127.0.0.4"}, Forbidden: []string{"127.0.0.3"}},
		{Scope: "location", ResourceId: "route_http", Access: []string{"127.0.0.2"}},
	}}
	snapshot.Domains = append(snapshot.Domains, &gatewayv2.SnapshotDomain{Id: "dom_v6_block", Host: "v6-block.test", HttpEnabled: true, TlsMode: "disabled"})
	snapshot.Routes = append(snapshot.Routes, &gatewayv2.SnapshotRoute{Id: "route_v6_block", Host: "v6-block.test", Path: "/", Kind: "return", ResponseCode: 200, ResponseValue: "blocked"})
	snapshot.AccessControls = append(snapshot.AccessControls, &gatewayv2.IPAccessControl{Scope: "server", ResourceId: "dom_v6_block", Access: []string{"::1"}, Forbidden: []string{"::-::1"}})
	for _, tc := range []struct{ id, host, header string }{
		{"dom_forward", "forward.test", "X-Forwarded-For"},
		{"dom_real", "real.test", "X-Real-IP"},
		{"dom_custom", "custom.test", "CF-Connecting-IP"},
		{"dom_source_only", "source-only.test", "X-Forwarded-For"},
		{"dom_forward_redirect", "forward-redirect.test", "X-Forwarded-For"},
	} {
		domain := &gatewayv2.SnapshotDomain{Id: tc.id, Host: tc.host, HttpEnabled: true, TlsMode: "disabled"}
		if tc.id == "dom_forward_redirect" {
			domain.HttpsEnabled, domain.RedirectHttpToHttps, domain.TlsMode, domain.AcmeChallenge = true, true, "managed", "http-01"
		}
		snapshot.Domains = append(snapshot.Domains, domain)
		snapshot.Routes = append(snapshot.Routes, &gatewayv2.SnapshotRoute{Id: "route_" + tc.id, Host: tc.host, Path: "/client", Kind: "proxy", ServiceId: "svc_sandbox", UpstreamName: "nsc_fixture", TargetPort: int32(port), UpstreamProtocol: "http"})
		acl := &gatewayv2.IPAccessControl{Scope: "server", ResourceId: tc.id, ClientIp: &gatewayv2.ClientIPSource{Header: tc.header, TrustedProxies: []string{"127.0.0.1-127.0.0.2", "127.0.0.9", "::1", "2001:db8::9"}}}
		if tc.id != "dom_source_only" {
			acl.Access, acl.Forbidden = []string{"198.51.100.0/24", "2001:db8:1::/64"}, []string{"198.51.100.15", "2001:db8:1::15"}
		}
		snapshot.AccessControls = append(snapshot.AccessControls, acl)
	}
	snapshot.Routes = append(snapshot.Routes, &gatewayv2.SnapshotRoute{Id: "route_forward_restricted", Host: "forward.test", Path: "/restricted", Kind: "return", ResponseCode: 200, ResponseValue: "restricted"})
	snapshot.AccessControls = append(snapshot.AccessControls, &gatewayv2.IPAccessControl{Scope: "location", ResourceId: "route_forward_restricted", Access: []string{"198.51.100.11", "203.0.113.9"}})
	root := t.TempDir()
	renderer := NewRenderer(config.NGINXConfig{WorkerProcesses: "1"})
	if err := renderer.RenderSandboxFixture(snapshot, root); err != nil {
		t.Fatal(err)
	}
	if err := renderer.WriteMainConfig(filepath.Join(root, "nginx.conf"), filepath.Join(root, "nginx.pid")); err != nil {
		t.Fatal(err)
	}
	// Add private IPv6 loopback listeners solely to exercise geo address matching;
	// production listener policy is independent of this configuration feature.
	for _, host := range []string{"acl.test", "redirect.test", "v6-block.test", "forward.test"} {
		path := filepath.Join(root, "domains", DomainFileName(host))
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		raw = []byte(strings.ReplaceAll(string(raw), "listen 80;", "listen 80;\n    listen [::]:80;"))
		if err := os.WriteFile(path, raw, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	syntax := exec.CommandContext(ctx, binary, "-p", root, "-c", "nginx.conf", "-t")
	if out, err := syntax.CombinedOutput(); err != nil {
		t.Fatalf("syntax: %v %s", err, out)
	}
	cmd := exec.CommandContext(ctx, binary, "-p", root, "-c", "nginx.conf", "-g", "daemon off;")
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Signal(os.Interrupt); _ = cmd.Wait() }()
	for {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:80", 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("NGINX did not start")
		case <-time.After(20 * time.Millisecond):
		}
	}
	for _, source := range []string{"127.0.0.1", "127.0.0.2", "127.0.0.3", "127.0.0.4", "127.0.0.5"} {
		transport := &http.Transport{DialContext: (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}}).DialContext}
		client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		for _, tc := range []struct {
			host, path      string
			allowed         int
			routeRestricted bool
		}{{"acl.test", "/proxy", 200, false}, {"acl.test", "/static", 200, false}, {"acl.test", "/redirect", 308, false}, {"acl.test", "/restricted", 200, true}, {"acl.test", "/missing", 404, false}, {"redirect.test", "/", 308, false}, {"redirect.test", "/restricted", 308, true}, {"redirect.test", "/.well-known/acme-challenge/token", 200, false}} {
			want := tc.allowed
			if source == "127.0.0.3" || source == "127.0.0.5" || tc.routeRestricted && source != "127.0.0.2" {
				want = 403
			}
			if tc.path == "/.well-known/acme-challenge/token" {
				want = 200
			}
			req, _ := http.NewRequest("GET", "http://127.0.0.1"+tc.path, nil)
			req.Host = tc.host
			req.Header.Set("X-Forwarded-For", "127.0.0.2")
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
			if res.StatusCode != want {
				t.Errorf("source=%s host=%s path=%s got=%d want=%d", source, tc.host, tc.path, res.StatusCode, want)
			}
		}
		transport.CloseIdleConnections()
	}
	v6Client := &http.Client{Timeout: 3 * time.Second}
	for _, tc := range []struct {
		host, path string
		status     int
	}{{"acl.test", "/static", 200}, {"acl.test", "/restricted", 403}, {"v6-block.test", "/", 403}} {
		req, _ := http.NewRequest("GET", "http://[::1]"+tc.path, nil)
		req.Host = tc.host
		res, err := v6Client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		if res.StatusCode != tc.status {
			t.Errorf("IPv6 host=%s path=%s got=%d want=%d", tc.host, tc.path, res.StatusCode, tc.status)
		}
	}
	for _, tc := range []struct {
		name, host, peer, path, header, value, resolved string
		status                                          int
	}{
		{"single forwarded IP", "forward.test", "127.0.0.1", "/client", "X-Forwarded-For", "198.51.100.10", "198.51.100.10", 200},
		{"range endpoint trusted", "forward.test", "127.0.0.2", "/client", "X-Forwarded-For", "198.51.100.10", "198.51.100.10", 200},
		{"trusted proxy chain", "forward.test", "127.0.0.1", "/client", "X-Forwarded-For", "198.51.100.10, 127.0.0.9", "198.51.100.10", 200},
		{"untrusted middle hop", "forward.test", "127.0.0.1", "/client", "X-Forwarded-For", "198.51.100.10, 203.0.113.9, 127.0.0.9", "", 403},
		{"forbidden forwarded IP", "forward.test", "127.0.0.1", "/client", "X-Forwarded-For", "198.51.100.15", "", 403},
		{"untrusted peer spoof", "forward.test", "127.0.0.3", "/client", "X-Forwarded-For", "198.51.100.10", "", 403},
		{"missing header", "forward.test", "127.0.0.1", "/client", "", "", "", 403},
		{"invalid header", "forward.test", "127.0.0.1", "/client", "X-Forwarded-For", "invalid", "", 403},
		{"route allows resolved IP", "forward.test", "127.0.0.1", "/restricted", "X-Forwarded-For", "198.51.100.11", "", 200},
		{"route restricts resolved IP", "forward.test", "127.0.0.1", "/restricted", "X-Forwarded-For", "198.51.100.10", "", 403},
		{"route cannot relax Domain", "forward.test", "127.0.0.1", "/restricted", "X-Forwarded-For", "203.0.113.9", "", 403},
		{"X-Real-IP selection", "real.test", "127.0.0.1", "/client", "X-Real-IP", "198.51.100.10", "198.51.100.10", 200},
		{"wrong header ignored", "real.test", "127.0.0.1", "/client", "X-Forwarded-For", "198.51.100.10", "", 403},
		{"custom header selection", "custom.test", "127.0.0.1", "/client", "CF-Connecting-IP", "198.51.100.10", "198.51.100.10", 200},
		{"custom header untrusted peer", "custom.test", "127.0.0.3", "/client", "CF-Connecting-IP", "198.51.100.10", "", 403},
		{"source without ACL", "source-only.test", "127.0.0.1", "/client", "X-Forwarded-For", "203.0.113.9", "203.0.113.9", 200},
		{"source fallback", "source-only.test", "127.0.0.1", "/client", "X-Forwarded-For", "invalid", "127.0.0.1", 200},
		{"redirect allowed", "forward-redirect.test", "127.0.0.1", "/client", "X-Forwarded-For", "198.51.100.10", "", 308},
		{"redirect denied", "forward-redirect.test", "127.0.0.1", "/client", "X-Forwarded-For", "198.51.100.15", "", 403},
		{"ACME bypass", "forward-redirect.test", "127.0.0.3", "/.well-known/acme-challenge/token", "X-Forwarded-For", "198.51.100.15", "", 200},
		{"IPv6 proxy and client", "forward.test", "::1", "/client", "X-Forwarded-For", "2001:db8:1::10, 2001:db8::9", "2001:db8:1::10", 200},
		{"IPv6 forbidden client", "forward.test", "::1", "/client", "X-Forwarded-For", "2001:db8:1::15", "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &http.Transport{DialContext: (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(tc.peer)}}).DialContext}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			address := "127.0.0.1"
			if tc.peer == "::1" {
				address = "[::1]"
			}
			req, _ := http.NewRequest("GET", "http://"+address+tc.path, nil)
			req.Host = tc.host
			if tc.header != "" {
				req.Header.Set(tc.header, tc.value)
			}
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if err != nil || res.StatusCode != tc.status || tc.resolved != "" && string(body) != "source:"+tc.resolved {
				t.Fatalf("status=%d body=%s expected=%d source=%s err=%v", res.StatusCode, body, tc.status, tc.resolved, err)
			}
		})
	}

}
