package nginx

import (
	"path/filepath"
	"strings"
	"testing"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
)

func TestIPAccessControlsRenderedBeforeEveryResponse(t *testing.T) {
	snapshot := &gatewayv2.Snapshot{ChallengeUpstream: "http://controller:8080", Domains: []*gatewayv2.SnapshotDomain{{Id: "dom_acl", Host: "acl.test", HttpEnabled: true, TlsMode: "disabled"}}, Routes: []*gatewayv2.SnapshotRoute{
		{Id: "route_return", Host: "acl.test", Path: "/static", Kind: "return", ResponseCode: 200, ResponseValue: "hello"},
		{Id: "route_redirect", Host: "acl.test", Path: "/redirect", Kind: "redirect", ResponseCode: 308, ResponseValue: "https://example.test/"},
		{Id: "route_proxy", Host: "acl.test", Path: "/proxy", Kind: "proxy", UpstreamProtocol: "http", ExternalUrl: "http://example.test"},
		{Id: "route_paused", Host: "acl.test", Path: "/paused", Kind: "return", ResponseCode: 503, Paused: true},
	}, AccessControls: []*gatewayv2.IPAccessControl{{Scope: "server", ResourceId: "dom_acl", Access: []string{"10.0.0.0/8"}, Forbidden: []string{"10.0.0.10-10.0.0.20"}}, {Scope: "location", ResourceId: "route_return", Access: []string{"10.0.0.11"}}}}
	root := t.TempDir()
	r := NewRenderer(config.NGINXConfig{})
	if err := r.Render(snapshot, root); err != nil {
		t.Fatal(err)
	}
	body := readTestFile(t, filepath.Join(root, "domains", "acl.test.conf"))
	for _, path := range []string{"/static", "/redirect", "/proxy", "/paused", "/"} {
		marker := "location "
		start := strings.Index(body, marker+"^~ "+path+" {")
		if path == "/" {
			start = strings.Index(body, marker+"/ {")
		}
		if start < 0 {
			t.Fatalf("missing %s: %s", path, body)
		}
		block := body[start:]
		end := strings.Index(block, "    }\n")
		block = block[:end]
		if !strings.Contains(block, "ip_forbidden; return 403") || !strings.Contains(block, "ip_not_allowed; return 403") {
			t.Fatalf("unguarded %s: %s", path, block)
		}
	}
	global := readTestFile(t, filepath.Join(root, "custom", "http.conf"))
	if strings.Contains(global, "10.0.0.10-10.0.0.20") || !strings.Contains(global, "10.0.0.10/31 1;") {
		t.Fatal(global)
	}
	snapshot.Domains[0].HttpsEnabled = true
	snapshot.Domains[0].TlsMode = "managed"
	snapshot.Domains[0].AcmeChallenge = "http-01"
	snapshot.Domains[0].RedirectHttpToHttps = true
	if err := r.Render(snapshot, root); err != nil {
		t.Fatal(err)
	}
	body = readTestFile(t, filepath.Join(root, "domains", "acl.test.conf"))
	challenge := body[strings.Index(body, "location ^~ /.well-known/acme-challenge/"):]
	challenge = challenge[:strings.Index(challenge, "    }\n")]
	if strings.Contains(challenge, "ip_forbidden") || !strings.Contains(challenge, "allow all") {
		t.Fatal(challenge)
	}
	if strings.Index(body, "ip_forbidden") > strings.Index(body, "return 308") {
		t.Fatal("redirect precedes ACL")
	}
	// Raw input does not change on render.
	if snapshot.AccessControls[0].Forbidden[0] != "10.0.0.10-10.0.0.20" {
		t.Fatal("renderer mutated input")
	}
}

func TestIPAccessControlInvalidConfigurations(t *testing.T) {
	for _, values := range [][]*gatewayv2.IPAccessControl{{nil}, {{Scope: "http", ResourceId: "dom"}}, {{Scope: "server"}}, {{Scope: "server", ResourceId: "dom"}, {Scope: "server", ResourceId: "dom"}}, {{Scope: "server", ResourceId: "dom", Access: []string{"invalid"}}}} {
		if _, _, err := renderIPAccessControls(values, nil); err == nil {
			t.Fatal("accepted invalid controls")
		}
	}
	_, _, err := renderIPAccessControls([]*gatewayv2.IPAccessControl{{Scope: "server", ResourceId: "dom", Access: []string{"::1"}}}, map[string]string{"server:dom": "allow all;"})
	if err == nil {
		t.Fatal("accepted conflicting manual ACL")
	}
}
