package nginx

import (
	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTP3SharedListenerAndHeaderInheritance(t *testing.T) {
	snap := &gatewayv2.Snapshot{ChallengeUpstream: "http://127.0.0.1:1", Domains: []*gatewayv2.SnapshotDomain{
		{Id: "dom_on", Host: "on.example.com", HttpsEnabled: true, HttpEnabled: true, Http3Enabled: true, TlsMode: "custom", CertificateId: "cert_test"},
		{Id: "dom_off", Host: "off.example.com", HttpsEnabled: true, TlsMode: "custom", CertificateId: "cert_test"},
		{Id: "dom_pending", Host: "pending.example.com", HttpsEnabled: true, Http3Enabled: true, TlsMode: "managed"},
	}, Certificates: []*gatewayv2.SnapshotCertificate{{Id: "cert_test", CertificatePem: "cert", PrivateKeyPem: "key"}}, Services: []*gatewayv2.SnapshotService{{Id: "svc_test", QualifiedName: "test", DnsName: "localhost"}}, Routes: []*gatewayv2.SnapshotRoute{{Id: "route_test", Host: "on.example.com", Path: "/", ServiceId: "svc_test", TargetPort: 80, UpstreamProtocol: "http", UpstreamName: "nsc_test"}}, Snippets: []*gatewayv2.ConfigSnippet{{Scope: "http", Content: "add_header X-Global yes always;"}, {Scope: "location", ResourceId: "route_test", Content: "add_header X-Local yes always;"}}}
	dir := t.TempDir()
	r := NewRenderer(config.NGINXConfig{HTTP3AdvertisedPort: 18443})
	if err := r.Render(snap, dir); err != nil {
		t.Fatal(err)
	}
	on := readTestFile(t, filepath.Join(dir, "domains/on.example.com.conf"))
	off := readTestFile(t, filepath.Join(dir, "domains/off.example.com.conf"))
	system := readTestFile(t, filepath.Join(dir, "system/default.conf"))
	if strings.Count(system, "reuseport") != 1 || strings.Contains(on+off, "reuseport") {
		t.Fatal("duplicate listener options")
	}
	if !strings.Contains(off, "http3 off;") || strings.Contains(off, "Alt-Svc") || !strings.Contains(on, "http3 on;") || strings.Count(on, `add_header Alt-Svc 'h3=":18443"; ma=300' always;`) != 2 {
		t.Fatalf("bad h3 fragments: %s\n%s", on, off)
	}
	https := strings.Split(on, "listen 443 ssl;")[1]
	if !strings.Contains(https, "X-Global yes") || !strings.Contains(https, "X-Local yes") {
		t.Fatal("lost custom headers")
	}
	pending, _ := os.ReadFile(filepath.Join(dir, "domains/pending.example.com.conf"))
	if strings.Contains(string(pending), "quic") || strings.Contains(string(pending), "Alt-Svc") {
		t.Fatal("pending certificate advertised")
	}
	snap.Domains[0].HttpsEnabled = false
	if err := r.Render(snap, t.TempDir()); err == nil {
		t.Fatal("accepted HTTP/3 without HTTPS")
	}
}
