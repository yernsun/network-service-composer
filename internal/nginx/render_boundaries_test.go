package nginx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
	"google.golang.org/protobuf/proto"
)

func renderBoundarySnapshot() *gatewayv2.Snapshot {
	return &gatewayv2.Snapshot{ChallengeUpstream: "http://controller:8080", Domains: []*gatewayv2.SnapshotDomain{{Id: "domain", Host: "app.example.test", HttpEnabled: true}}, Services: []*gatewayv2.SnapshotService{{Id: "service", QualifiedName: "app_web", DnsName: "app_web"}}, Routes: []*gatewayv2.SnapshotRoute{{Id: "route", Host: "app.example.test", Path: "/", ServiceId: "service", TargetPort: 8080, UpstreamProtocol: "http", UpstreamName: "nsc_app_web"}}}
}

func TestRendererRejectsInvalidSnapshotRelationships(t *testing.T) {
	for _, stage := range []string{"nil snapshot", "nil snippet", "duplicate snippet", "invalid snippet", "nil service", "service ID", "service DNS", "service qualified name", "duplicate service", "nil certificate", "certificate ID", "duplicate certificate", "nil domain", "domain ID", "domain host", "duplicate host", "no entrypoint", "HTTPS without TLS", "redirect no HTTP", "redirect no HTTPS", "nil route", "route ID", "route service ID", "route upstream name", "route host", "route path", "route protocol", "port zero", "port too high", "upstream SNI", "missing service", "missing domain", "upstream collision"} {
		t.Run(stage, func(t *testing.T) {
			s := renderBoundarySnapshot()
			switch stage {
			case "nil snapshot":
				s = nil
			case "nil snippet":
				s.Snippets = []*gatewayv2.ConfigSnippet{nil}
			case "duplicate snippet":
				s.Snippets = []*gatewayv2.ConfigSnippet{{Scope: "http", Content: "gzip on;"}, {Scope: "http", Content: "gzip off;"}}
			case "invalid snippet":
				s.Snippets = []*gatewayv2.ConfigSnippet{{Scope: "http", Content: "invalid_directive on;"}}
			case "nil service":
				s.Services[0] = nil
			case "service ID":
				s.Services[0].Id = "../service"
			case "service DNS":
				s.Services[0].DnsName = "127.0.0.1"
			case "service qualified name":
				s.Services[0].QualifiedName = ""
			case "duplicate service":
				s.Services = append(s.Services, s.Services[0])
			case "nil certificate":
				s.Certificates = []*gatewayv2.SnapshotCertificate{nil}
			case "certificate ID":
				s.Certificates = []*gatewayv2.SnapshotCertificate{{Id: "../key"}}
			case "duplicate certificate":
				s.Certificates = []*gatewayv2.SnapshotCertificate{{Id: "cert"}, {Id: "cert"}}
			case "nil domain":
				s.Domains[0] = nil
			case "domain ID":
				s.Domains[0].Id = "../domain"
			case "domain host":
				s.Domains[0].Host = "bad;host"
			case "duplicate host":
				s.Domains = append(s.Domains, s.Domains[0])
			case "no entrypoint":
				s.Domains[0].HttpEnabled = false
			case "HTTPS without TLS":
				s.Domains[0].HttpsEnabled = true
			case "redirect no HTTP":
				s.Domains[0].HttpEnabled = false
				s.Domains[0].HttpsEnabled = true
				s.Domains[0].TlsMode = "custom"
				s.Domains[0].RedirectHttpToHttps = true
			case "redirect no HTTPS":
				s.Domains[0].RedirectHttpToHttps = true
			case "nil route":
				s.Routes[0] = nil
			case "route ID":
				s.Routes[0].Id = "../route"
			case "route service ID":
				s.Routes[0].ServiceId = "../service"
			case "route upstream name":
				s.Routes[0].UpstreamName = "arbitrary"
			case "route host":
				s.Routes[0].Host = "bad;host"
			case "route path":
				s.Routes[0].Path = "relative"
			case "route protocol":
				s.Routes[0].UpstreamProtocol = "tcp"
			case "port zero":
				s.Routes[0].TargetPort = 0
			case "port too high":
				s.Routes[0].TargetPort = 65536
			case "upstream SNI":
				s.Routes[0].UpstreamServerName = "bad;host"
			case "missing service":
				s.Routes[0].ServiceId = "missing"
			case "missing domain":
				s.Routes[0].Host = "other.example.test"
			case "upstream collision":
				other := proto.Clone(s.Routes[0]).(*gatewayv2.SnapshotRoute)
				other.Id = "other"
				other.TargetPort = 9090
				s.Routes = append(s.Routes, other)
			}
			dir := t.TempDir()
			if e := NewRenderer(config.NGINXConfig{}).Render(s, dir); e == nil {
				t.Fatal("invalid snapshot accepted")
			}
			if _, e := os.Stat(filepath.Join(dir, "system", "default.conf")); !os.IsNotExist(e) {
				t.Fatal("invalid snapshot published completion fragment")
			}
		})
	}
}

func TestRendererReportsEachUnwritableArtifact(t *testing.T) {
	for _, path := range []string{"certificates", "upstreams", "domains", "system", "custom", "custom/http.conf", "certificates/cert.crt", "certificates/cert.key", "upstreams/nsc_app_web.conf", "domains/app.example.test.conf", "system/default.conf"} {
		t.Run(path, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, path)
			if e := os.MkdirAll(filepath.Dir(target), 0700); e != nil {
				t.Fatal(e)
			}
			if strings.Contains(path, "/") {
				if e := os.Mkdir(target, 0700); e != nil {
					t.Fatal(e)
				}
			} else if e := os.WriteFile(target, []byte("preserve"), 0600); e != nil {
				t.Fatal(e)
			}
			s := renderBoundarySnapshot()
			s.Certificates = []*gatewayv2.SnapshotCertificate{{Id: "cert", CertificatePem: "cert", PrivateKeyPem: "private"}}
			if e := NewRenderer(config.NGINXConfig{}).Render(s, dir); e == nil {
				t.Fatal("unwritable generation reported complete")
			}
			if !strings.Contains(path, "/") {
				raw, e := os.ReadFile(target)
				if e != nil || string(raw) != "preserve" {
					t.Fatal("renderer replaced conflicting file")
				}
			}
		})
	}
	r := NewRenderer(config.NGINXConfig{})
	dir := t.TempDir()
	file := filepath.Join(dir, "blocked")
	if e := os.WriteFile(file, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := r.WriteMainConfig(filepath.Join(file, "nginx.conf"), "/tmp/nginx.pid"); e == nil {
		t.Fatal("failed main directory creation ignored")
	}
	if e := r.WriteMainConfig(dir, "/tmp/nginx.pid"); e == nil {
		t.Fatal("main file write failure ignored")
	}
	path := filepath.Join(dir, "safe", "nginx.conf")
	if e := r.WriteMainConfig(path, "/tmp/path with spaces/nginx.pid"); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(readTestFile(t, path), `path\ with\ spaces`) {
		t.Fatal("pid path not escaped")
	}
}

func TestRendererIdentifierAndEndpointValidationBoundaries(t *testing.T) {
	for _, value := range []string{"", "127.0.0.1", strings.Repeat("a", 254), "UPPER.example", "bad_name.example", "a..example", strings.Repeat("a", 64) + ".example", "-bad.example", "bad-.example"} {
		if validateHost(value) == nil {
			t.Fatalf("invalid exact host accepted: %q", value)
		}
	}
	if e := validateHost("a-1.example"); e != nil {
		t.Fatal(e)
	}
	for _, value := range []string{"", "127.0.0.1", strings.Repeat("a", 254), "bad;name"} {
		if validateDockerDNSName(value) == nil {
			t.Fatal("invalid Docker DNS accepted")
		}
	}
	if e := validateDockerDNSName("App_1.web-2"); e != nil {
		t.Fatal(e)
	}
	for _, value := range []string{"", "relative", "/white space", "/control\x01", "/quote\"", "/back\\slash"} {
		if validatePath(value) == nil {
			t.Fatal("unsafe route path accepted")
		}
	}
	for _, value := range []string{"", "https://%", "tcp://host", "http:///path", "http://user:secret@host", "http://host?query", "http://host#fragment", "http://host/;command", "http://host/{command}"} {
		if _, e := validateUpstreamURL(value); e == nil {
			t.Fatal("unsafe challenge upstream accepted")
		}
	}
	if value, e := validateUpstreamURL("https://controller:8443///"); e != nil || value != "https://controller:8443" {
		t.Fatal("valid challenge URL not normalized", e)
	}
	for _, value := range []string{"", strings.Repeat("a", 129), "bad/name"} {
		if safeID(value) {
			t.Fatal("unsafe resource identifier accepted")
		}
	}
	if !safeID("AbC_123-test") {
		t.Fatal("valid resource identifier rejected")
	}
	for _, value := range []string{"arbitrary", "nsc_" + strings.Repeat("a", 220), "nsc_UPPER", "nsc_bad-name"} {
		if safeUpstreamName(value) {
			t.Fatal("unsafe upstream identifier accepted")
		}
	}
	if !safeUpstreamName("nsc_app_123") {
		t.Fatal("valid upstream rejected")
	}
	for _, value := range []string{"bad", "0", "-1", "1025"} {
		if safeWorkerProcesses(value) != "auto" {
			t.Fatal("invalid worker count retained")
		}
	}
	for _, value := range []string{"auto", "1", "1024"} {
		if safeWorkerProcesses(value) != value {
			t.Fatal("valid worker count changed")
		}
	}
}
