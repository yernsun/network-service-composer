package nginx

import (
	"path/filepath"
	"strings"
	"testing"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
)

func TestEveryHTTPSUpstreamRequiresTrustedPeer(t *testing.T) {
	for _, ca := range []string{"", "/run/nsc/private-ca-bundle.pem"} {
		for _, external := range []bool{false, true} {
			root := t.TempDir()
			route := &gatewayv2.SnapshotRoute{Id: "route", Host: "app.example.test", Path: "/", ServiceId: "service", TargetPort: 443, UpstreamProtocol: "https", UpstreamName: "nsc_service", UpstreamServerName: "upstream.example.test"}
			if external {
				route.ServiceId = ""
				route.ExternalUrl = "https://upstream.example.test/"
			}
			snapshot := &gatewayv2.Snapshot{ChallengeUpstream: "http://controller:8080", Domains: []*gatewayv2.SnapshotDomain{{Id: "domain", Host: route.Host, HttpEnabled: true}}, Services: []*gatewayv2.SnapshotService{{Id: "service", QualifiedName: "web", DnsName: "web"}}, Routes: []*gatewayv2.SnapshotRoute{route}}
			if err := NewRenderer(config.NGINXConfig{UpstreamCAFile: ca}).Render(snapshot, root); err != nil {
				t.Fatal(err)
			}
			text := readTestFile(t, filepath.Join(root, "domains", route.Host+".conf"))
			bundle := ca
			if bundle == "" {
				bundle = "/etc/ssl/certs/ca-certificates.crt"
			}
			for _, want := range []string{"proxy_ssl_verify on;", "proxy_ssl_verify_depth 5;", "proxy_ssl_trusted_certificate " + nginxQuotedValue(bundle) + ";", "proxy_ssl_name upstream.example.test;"} {
				if !strings.Contains(text, want) {
					t.Errorf("missing upstream verification: %s", want)
				}
			}
		}
	}
}
