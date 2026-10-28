package nginx

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
)

func TestClientIPSourceRenderingAndValidation(t *testing.T) {
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP", "CF-Connecting-IP", "x-client-IP", "A"} {
		if !ValidClientIPHeader(header) {
			t.Fatal(header)
		}
	}
	for _, header := range []string{"", "proxy_protocol", "X_IP", "1-IP", "X;deny", "X\nY", "X IP", strings.Repeat("X", 65)} {
		if ValidClientIPHeader(header) {
			t.Fatal(header)
		}
	}
	source := &gatewayv2.ClientIPSource{Header: "X-Forwarded-For", TrustedProxies: []string{" 10.0.0.1-10.0.0.3 ", "10.0.0.2/31", "2001:db8::1"}}
	before := append([]string(nil), source.TrustedProxies...)
	controls := []*gatewayv2.IPAccessControl{{Scope: "server", ResourceId: "dom_ip", ClientIp: source}}
	snapshot := &gatewayv2.Snapshot{ChallengeUpstream: "http://controller:8080", Domains: []*gatewayv2.SnapshotDomain{
		{Id: "dom_ip", Host: "ip.test", HttpEnabled: true, HttpsEnabled: true, TlsMode: "managed", CertificateId: "cert_ip", RedirectHttpToHttps: true, AcmeChallenge: "http-01"},
		{Id: "dom_socket", Host: "socket.test", HttpEnabled: true},
	}, Certificates: []*gatewayv2.SnapshotCertificate{{Id: "cert_ip", CertificatePem: "certificate", PrivateKeyPem: "key"}}, AccessControls: controls}
	root := t.TempDir()
	if err := NewRenderer(config.NGINXConfig{}).Render(snapshot, root); err != nil {
		t.Fatal(err)
	}
	raw := readTestFile(t, filepath.Join(root, "domains", DomainFileName("ip.test")))
	for _, expected := range []string{"set_real_ip_from 10.0.0.1/32;", "set_real_ip_from 10.0.0.2/31;", "set_real_ip_from 2001:db8::1/128;", "real_ip_header X-Forwarded-For;", "real_ip_recursive on;"} {
		if strings.Count(raw, expected) != 2 {
			t.Fatalf("expected source in both HTTP and HTTPS servers: %s\n%s", expected, raw)
		}
	}
	if strings.Contains(readTestFile(t, filepath.Join(root, "domains", DomainFileName("socket.test"))), "real_ip") {
		t.Fatal("source leaked into another Domain")
	}
	if !reflect.DeepEqual(source.TrustedProxies, before) {
		t.Fatal("render changed persisted trusted ranges")
	}
	for _, tc := range []struct {
		scope string
		value *gatewayv2.ClientIPSource
	}{
		{"location", source},
		{"server", &gatewayv2.ClientIPSource{Header: "X;deny", TrustedProxies: []string{"::1"}}},
		{"server", &gatewayv2.ClientIPSource{Header: "X-Real-IP"}},
		{"server", &gatewayv2.ClientIPSource{Header: "X-Real-IP", TrustedProxies: []string{"invalid"}}},
		{"server", &gatewayv2.ClientIPSource{Header: "X-Real-IP", TrustedProxies: make([]string, 1001)}},
	} {
		if _, err := renderClientIPSources([]*gatewayv2.IPAccessControl{{Scope: tc.scope, ResourceId: "dom_ip", ClientIp: tc.value}}); err == nil {
			t.Fatal("accepted invalid source", tc)
		}
	}
}
