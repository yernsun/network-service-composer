package nginx

import (
	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewOriginsAndProductionBytes(t *testing.T) {
	snap := &gatewayv2.Snapshot{PoolId: "pool_test", ConfigRevision: 9, ChallengeUpstream: "http://controller:8080", Domains: []*gatewayv2.SnapshotDomain{{Id: "dom_one", Host: "one.test", HttpEnabled: true}}, Services: []*gatewayv2.SnapshotService{{Id: "svc_one", QualifiedName: "project_web", DnsName: "project_web"}}, Routes: []*gatewayv2.SnapshotRoute{{Id: "route_one", Host: "one.test", Path: "/", ServiceId: "svc_one", TargetPort: 80, UpstreamProtocol: "http", UpstreamName: "nsc_one"}}, Snippets: []*gatewayv2.ConfigSnippet{{Scope: "http", Content: "# heading\nadd_header X-Common same always;\nproxy_read_timeout 90s;"}, {Scope: "server", ResourceId: "dom_one", Content: "add_header X-Own same always;"}, {Scope: "location", ResourceId: "route_one", Content: "add_header X-Own replacement always;"}}}
	renderer := NewRenderer(config.Defaults().NGINX)
	directory := t.TempDir()
	if e := renderer.Render(snap, directory); e != nil {
		t.Fatal(e)
	}
	preview, e := renderer.Preview(snap)
	if e != nil {
		t.Fatal(e)
	}
	for file, content := range preview.Files {
		if file == "nginx.conf" || file == "complete.conf" {
			continue
		}
		raw, e := os.ReadFile(filepath.Join(directory, file))
		if e != nil || string(raw) != content {
			t.Fatalf("production bytes changed: %s: %v", file, e)
		}
	}
	if strings.Contains(preview.Content, "include ") || !strings.Contains(preview.Content, "upstream nsc_one") || !strings.Contains(preview.Content, "server_name one.test") {
		t.Fatal(preview.Content)
	}
	inherited := 0
	for _, span := range preview.Ranges {
		lines := strings.Split(preview.Files[span.File], "\n")
		line := lines[span.Start-1]
		if span.Start < 1 || span.End < span.Start {
			t.Fatal(span)
		}
		if span.Scope == "http" && span.InputLine == 2 {
			if !strings.Contains(line, "X-Common same") {
				t.Fatal(span, line)
			}
			if span.Inherited {
				inherited++
			}
		}
		if span.Scope == "location" && !strings.Contains(line, "replacement") {
			t.Fatal(span, line)
		}
	}
	if binary := os.Getenv("NSC_TEST_NGINX_BINARY"); binary != "" {
		main := filepath.Join(directory, "preview.conf")
		if err := os.WriteFile(main, []byte(preview.Content), 0600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(binary, "-t", "-p", directory+"/", "-c", main).CombinedOutput(); err != nil {
			t.Fatalf("complete preview is not valid nginx configuration: %v: %s", err, output)
		}
	}
	if inherited != 4 {
		t.Fatalf("expected common header in server and location, raw and expanded: %d", inherited)
	}
}
func TestPlaceholderExamplesParse(t *testing.T) {
	examples := map[string]string{"http": "client_max_body_size 20m;\ngzip on;\ngzip_types text/plain application/json;", "server": "client_max_body_size 20m;\nadd_header X-Example \"value\" always;", "location": "proxy_connect_timeout 5s;\nproxy_read_timeout 60s;\nproxy_send_timeout 60s;\nproxy_buffering on;"}
	for scope, example := range examples {
		if _, e := ParseSnippet(scope, example); e != nil {
			t.Fatal(scope, e)
		}
	}
}
