package nginx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
)

func TestRenderDNSFragments(t *testing.T) {
	directory := t.TempDir()
	snapshot := &gatewayv2.Snapshot{PoolId: "pool_test", ConfigRevision: 2, ChallengeUpstream: "http://controller:8080",
		Domains:  []*gatewayv2.SnapshotDomain{{Id: "dom_example", Host: "example.com", HttpEnabled: true}},
		Services: []*gatewayv2.SnapshotService{{Id: "svc_ready", QualifiedName: "hello_web", DnsName: "hello_web"}},
		Routes: []*gatewayv2.SnapshotRoute{
			{Id: "route_api", Host: "example.com", Path: "/api/", ServiceId: "svc_ready", TargetPort: 9000,
				UpstreamProtocol: "http", UpstreamName: "nsc_hello_web_a1b2c3d4", WebsocketEnabled: true},
		},
	}
	renderer := NewRenderer(config.NGINXConfig{WorkerProcesses: "auto"})
	if err := renderer.Render(snapshot, directory); err != nil {
		t.Fatal(err)
	}
	upstream := readTestFile(t, filepath.Join(directory, "upstreams", "nsc_hello_web_a1b2c3d4.conf"))
	for _, expected := range []string{"upstream nsc_hello_web_a1b2c3d4", "zone nsc_hello_web_a1b2c3d4 64k", "server hello_web:9000 resolve"} {
		if !strings.Contains(upstream, expected) {
			t.Errorf("upstream fragment does not contain %q:\n%s", expected, upstream)
		}
	}
	if strings.Contains(upstream, "10.") || strings.Contains(upstream, "tasks.") {
		t.Fatalf("upstream contains an IP/task backend:\n%s", upstream)
	}
	domain := readTestFile(t, filepath.Join(directory, "domains", "example.com.conf"))
	for _, expected := range []string{"listen 80;", "proxy_set_header Host $nsc_forwarded_host;", "set $nsc_forwarded_host $http_host;", "if ($nsc_forwarded_host = '') { set $nsc_forwarded_host $host; }", "location ^~ /api/", "proxy_set_header Upgrade", "proxy_pass http://nsc_hello_web_a1b2c3d4", "location / { return 404; }"} {
		if !strings.Contains(domain, expected) {
			t.Errorf("domain fragment does not contain %q:\n%s", expected, domain)
		}
	}
	main := string(renderer.MainConfig("/tmp/nginx.pid"))
	for _, expected := range []string{"resolver 127.0.0.11 valid=5s ipv6=off;", "resolver_timeout 2s;", "include upstreams/*.conf;", "include domains/*.conf;", "nsc_json", "map $host $nsc_domain_id", "map $host $nsc_failure_reason"} {
		if !strings.Contains(main, expected) {
			t.Errorf("main config does not contain %q", expected)
		}
	}
	if strings.Index(main, "map $host $nsc_domain_id") > strings.Index(main, "log_format nsc_json") {
		t.Fatal("bootstrap observability variables must be registered before log_format is parsed")
	}
	system := readTestFile(t, filepath.Join(directory, "system", "default.conf"))
	for _, expected := range []string{"location = /_nsc/generation-health", "X-NSC-Config-Revision 2", "X-Request-ID $nsc_request_id always", `"config_revision":2`} {
		if !strings.Contains(system, expected) {
			t.Errorf("system fragment does not contain %q:\n%s", expected, system)
		}
	}
}

func TestDomainWithoutRoutesCannotServeGatewayStaticFiles(t *testing.T) {
	directory := t.TempDir()
	snapshot := &gatewayv2.Snapshot{ChallengeUpstream: "http://controller:8080",
		Domains: []*gatewayv2.SnapshotDomain{{Id: "dom_empty", Host: "empty.example.com", HttpEnabled: true, TlsMode: "disabled"}}}
	if err := NewRenderer(config.NGINXConfig{}).Render(snapshot, directory); err != nil {
		t.Fatal(err)
	}
	domain := readTestFile(t, filepath.Join(directory, "domains", "empty.example.com.conf"))
	if !strings.Contains(domain, "location / { return 404; }") {
		t.Fatalf("empty Domain can fall through to the static document root:\n%s", domain)
	}
}

func TestRenderEntrypointCombinationsAndTLS(t *testing.T) {
	directory := t.TempDir()
	snapshot := &gatewayv2.Snapshot{ChallengeUpstream: "http://controller:8080",
		Domains: []*gatewayv2.SnapshotDomain{
			{Id: "dom_http", Host: "http.example.com", HttpEnabled: true, TlsMode: "disabled"},
			{Id: "dom_https", Host: "https.example.com", HttpsEnabled: true, TlsMode: "custom", CertificateId: "cert_test"},
			{Id: "dom_both", Host: "both.example.com", HttpEnabled: true, HttpsEnabled: true, RedirectHttpToHttps: true, TlsMode: "custom", CertificateId: "cert_test"},
			{Id: "dom_http01", Host: "http01.example.com", HttpsEnabled: true, TlsMode: "managed", AcmeChallenge: "http-01"},
			{Id: "dom_dns01", Host: "dns01.example.com", HttpsEnabled: true, TlsMode: "managed", AcmeChallenge: "dns-01"},
		},
		Certificates: []*gatewayv2.SnapshotCertificate{{Id: "cert_test", CertificatePem: "certificate", PrivateKeyPem: "private-key"}},
	}
	if err := NewRenderer(config.NGINXConfig{}).Render(snapshot, directory); err != nil {
		t.Fatal(err)
	}
	https := readTestFile(t, filepath.Join(directory, "domains", "https.example.com.conf"))
	if strings.Contains(https, "listen 80;") || !strings.Contains(https, "listen 443 ssl;") || !strings.Contains(https, "http2 on;") {
		t.Fatalf("invalid HTTPS-only fragment:\n%s", https)
	}
	both := readTestFile(t, filepath.Join(directory, "domains", "both.example.com.conf"))
	if !strings.Contains(both, "listen 80;") || !strings.Contains(both, "return 308 https://$host$request_uri") || !strings.Contains(both, "listen 443 ssl;") {
		t.Fatalf("invalid dual-entrypoint redirect fragment:\n%s", both)
	}
	http01 := readTestFile(t, filepath.Join(directory, "domains", "http01.example.com.conf"))
	if !strings.Contains(http01, "listen 80;") || !strings.Contains(http01, "/.well-known/acme-challenge/") || !strings.Contains(http01, "location / { return 404; }") {
		t.Fatalf("HTTPS-only HTTP-01 domain omitted its challenge listener:\n%s", http01)
	}
	dns01 := readTestFile(t, filepath.Join(directory, "domains", "dns01.example.com.conf"))
	if strings.Contains(dns01, "listen 80;") || strings.Contains(dns01, "/.well-known/acme-challenge/") {
		t.Fatalf("DNS-01 domain unexpectedly opened an HTTP challenge listener:\n%s", dns01)
	}
	keyInfo, err := os.Stat(filepath.Join(directory, "certificates", "cert_test.key"))
	if err != nil || keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode=%v err=%v", keyInfo, err)
	}
}

func TestRenderPausedRouteReturnsExplicit503WithoutUpstream(t *testing.T) {
	directory := t.TempDir()
	snapshot := &gatewayv2.Snapshot{ChallengeUpstream: "http://controller:8080",
		Domains:  []*gatewayv2.SnapshotDomain{{Id: "dom_paused", Host: "paused.example.com", HttpEnabled: true}},
		Services: []*gatewayv2.SnapshotService{{Id: "svc_paused", QualifiedName: "demo_api", DnsName: "demo_api"}},
		Routes: []*gatewayv2.SnapshotRoute{{Id: "route_paused", Host: "paused.example.com", Path: "/", ServiceId: "svc_paused",
			TargetPort: 80, UpstreamProtocol: "http", UpstreamName: "nsc_demo_api_a1b2c3d4", Paused: true}},
	}
	if err := NewRenderer(config.NGINXConfig{}).Render(snapshot, directory); err != nil {
		t.Fatal(err)
	}
	domain := readTestFile(t, filepath.Join(directory, "domains", "paused.example.com.conf"))
	for _, expected := range []string{"set $nsc_failure_reason route_paused", "return 503"} {
		if !strings.Contains(domain, expected) {
			t.Fatalf("paused route is missing %q:\n%s", expected, domain)
		}
	}
	if strings.Contains(domain, "proxy_pass") {
		t.Fatalf("paused route must not proxy traffic:\n%s", domain)
	}
	if _, err := os.Stat(filepath.Join(directory, "upstreams", "nsc_demo_api_a1b2c3d4.conf")); !os.IsNotExist(err) {
		t.Fatalf("paused-only upstream must not be rendered, err=%v", err)
	}
}

func TestRenderRejectsInjectionAndArbitraryBackend(t *testing.T) {
	base := &gatewayv2.Snapshot{ChallengeUpstream: "http://controller:8080",
		Domains:  []*gatewayv2.SnapshotDomain{{Id: "dom_a", Host: "example.com", HttpEnabled: true}},
		Services: []*gatewayv2.SnapshotService{{Id: "svc_a", QualifiedName: "demo_api", DnsName: "demo_api"}},
		Routes:   []*gatewayv2.SnapshotRoute{{Id: "route_a", Host: "example.com", Path: "/; return 200;", ServiceId: "svc_a", TargetPort: 80, UpstreamProtocol: "http", UpstreamName: "nsc_demo_api_a1b2c3d4"}},
	}
	if err := NewRenderer(config.NGINXConfig{}).Render(base, t.TempDir()); err == nil {
		t.Fatal("expected unsafe route path to be rejected")
	}
	base.Routes[0].Path = "/"
	base.Services[0].DnsName = "127.0.0.1"
	if err := NewRenderer(config.NGINXConfig{}).Render(base, t.TempDir()); err == nil {
		t.Fatal("expected IP backend to be rejected")
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func FuzzNGINXPathValidation(f *testing.F) {
	for _, seed := range []string{"/", "/api/", "/;return 200;", "/line\nbreak", "relative", "/空"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if validatePath(value) != nil {
			return
		}
		if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "{};\"'\\\r\n\t") {
			t.Fatalf("unsafe NGINX path was accepted: %q", value)
		}
	})
}

func TestRenderFixedRedirectAndExternalProxyWithoutService(t *testing.T) {
	directory := t.TempDir()
	snapshot := &gatewayv2.Snapshot{ChallengeUpstream: "http://controller:8080", Domains: []*gatewayv2.SnapshotDomain{{Id: "dom_example", Host: "example.test", HttpEnabled: true}}, Routes: []*gatewayv2.SnapshotRoute{
		{Id: "route_hello", Host: "example.test", Path: "/hello", Kind: "return", Match: "exact", ResponseCode: 200, ResponseValue: "hello"},
		{Id: "route_away", Host: "example.test", Path: "/away", Kind: "redirect", Match: "exact", ResponseCode: 308, ResponseValue: "https://example.org/"},
		{Id: "route_external", Host: "example.test", Path: "/proxy/", Kind: "proxy", Match: "prefer", ExternalUrl: "https://internal.example.test/base/", UpstreamProtocol: "https", ReadTimeoutSeconds: 30, ConnectTimeoutSeconds: 5},
	}}
	if err := NewRenderer(config.NGINXConfig{}).Render(snapshot, directory); err != nil {
		t.Fatal(err)
	}
	got := readTestFile(t, filepath.Join(directory, "domains", "example.test.conf"))
	for _, line := range []string{"location = /hello", "return 200 \"hello\";", "location = /away", "return 308 \"https://example.org/\";", "proxy_pass https://internal.example.test/base/;", "proxy_ssl_name internal.example.test;", "proxy_read_timeout 30s;"} {
		if !strings.Contains(got, line) {
			t.Errorf("missing %q in %s", line, got)
		}
	}
}
