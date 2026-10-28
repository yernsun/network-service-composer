package nginx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/config"
)

func TestSandboxLoopbackIsPrivateToLocallyConstructedFixture(t *testing.T) {
	s := renderBoundarySnapshot()
	s.Services[0].Id = "svc_sandbox"
	s.Services[0].QualifiedName = "nsc_sandbox_fixture"
	s.Services[0].DnsName = "127.0.0.1"
	s.Routes[0].ServiceId = "svc_sandbox"
	r := NewRenderer(config.Defaults().NGINX)
	if err := r.Render(s, t.TempDir()); err == nil {
		t.Fatal("ordinary remote snapshot accepted a loopback upstream")
	}
	dir := t.TempDir()
	if err := r.RenderSandboxFixture(s, dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "upstreams", s.Routes[0].UpstreamName+".conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "server 127.0.0.1:8080;") || strings.Contains(string(raw), "resolve;") {
		t.Fatal("fixture still depends on Docker DNS", string(raw))
	}
	if err := r.Render(s, t.TempDir()); err == nil {
		t.Fatal("fixture rendering changed the ordinary renderer")
	}
	s.Services[0].DnsName = "127.0.0.2"
	if err := r.RenderSandboxFixture(s, t.TempDir()); err == nil {
		t.Fatal("fixture allowed arbitrary addresses")
	}
}
