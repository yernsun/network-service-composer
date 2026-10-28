package runtime

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func TestLabelReadinessRejectsInvalidOverrides(t *testing.T) {
	invalid := map[string][]string{
		"port":                        {"zero", "0", "-1", "65536"},
		"protocol":                    {"tcp"},
		"readiness.mode":              {"unknown"},
		"readiness.path":              {"relative", "/query?secret", "/fragment#x", "/quote\"", "/control\x00", "/space\u00a0inside"},
		"readiness.scheme":            {"tcp"},
		"readiness.success-threshold": {"bad", "0", "-1"},
		"readiness.failure-threshold": {"bad", "0", "-1"},
		"readiness.tcp-fallback":      {"yes"},
	}
	for _, field := range []string{"interval", "timeout", "startup-grace-period", "startup-timeout"} {
		invalid["readiness."+field] = []string{"invalid", "0s", "-1s"}
	}
	for field, values := range invalid {
		for _, raw := range values {
			t.Run(field+"/"+fmt.Sprintf("%q", raw), func(t *testing.T) {
				labels := map[string]string{"nsc.enable": "true", "nsc." + field: raw}
				if _, err := ParseLabels(labels, defaultPolicy()); err == nil {
					t.Fatalf("invalid %s accepted: %q", field, raw)
				}
			})
		}
	}
	for name, change := range map[string]func(*model.ReadinessPolicy){
		"timeout zero":             func(p *model.ReadinessPolicy) { p.Timeout = 0 },
		"interval zero":            func(p *model.ReadinessPolicy) { p.Interval = 0 },
		"timeout exceeds interval": func(p *model.ReadinessPolicy) { p.Timeout = p.Interval + time.Second },
		"HTTP without path":        func(p *model.ReadinessPolicy) { p.Mode = model.ReadinessHTTP },
	} {
		t.Run(name, func(t *testing.T) {
			p := defaultPolicy()
			change(&p)
			if _, err := ParseLabels(map[string]string{"nsc.enable": "true"}, p); err == nil {
				t.Fatal("invalid readiness accepted")
			}
		})
	}
}

func TestLabelReadinessOverridesAndDisabledBoundary(t *testing.T) {
	labels := map[string]string{"nsc.enable": "true", "nsc.port": " 65535 ", "nsc.protocol": " HTTPS ", "nsc.readiness.path": "/health", "nsc.readiness.scheme": " HTTPS ", "nsc.readiness.interval": "5s", "nsc.readiness.timeout": "3s", "nsc.readiness.startup-grace-period": "10s", "nsc.readiness.startup-timeout": "2m", "nsc.readiness.success-threshold": "1", "nsc.readiness.failure-threshold": "4", "nsc.readiness.tcp-fallback": "false"}
	for _, mode := range []model.ReadinessMode{model.ReadinessAuto, model.ReadinessDocker, model.ReadinessTCP, model.ReadinessHTTP, model.ReadinessGrace} {
		labels["nsc.readiness.mode"] = strings.ToUpper(string(mode))
		got, err := ParseLabels(labels, defaultPolicy())
		want := model.ReadinessPolicy{Mode: mode, Path: "/health", Scheme: "https", Interval: 5 * time.Second, Timeout: 3 * time.Second, StartupGracePeriod: 10 * time.Second, StartupTimeout: 2 * time.Minute, SuccessThreshold: 1, FailureThreshold: 4, TCPFallback: false}
		if err != nil || got.Readiness != want || got.Protocol != "https" || got.Port != 65535 {
			t.Fatalf("overrides not preserved: %+v %v", got, err)
		}
	}
	labels["nsc.enable"] = "false"
	labels["nsc.port"] = "invalid"
	got, err := ParseLabels(labels, defaultPolicy())
	if err != nil || got.Enabled || got.Port != 0 || got.Readiness != defaultPolicy() || len(got.Routes) != 0 {
		t.Fatalf("disabled service interpreted routing labels: %+v %v", got, err)
	}
}

func TestRouteLabelInvalidFieldsCannotCreateEntrypoints(t *testing.T) {
	invalid := map[string][]string{
		"tls": {"other"}, "entrypoints": {"https", "http,http", "https,https", "http,tcp", "http,"},
		"redirect-http-to-https": {"yes", "true"}, "priority": {"high"}, "strip-path": {"yes"}, "websocket": {"yes"},
		"port": {"invalid", "0", "-1", "65536"}, "protocol": {"tcp"}, "server-name": {"*.example.test"},
		"path": {"relative", "/bad;directive"},
		"host": {"", "127.0.0.1", "\x00.example", "a..test", strings.Repeat("a", 64) + ".test", strings.Repeat("a.", 128) + "test", "-bad.test", "bad-.test"},
	}
	for field, values := range invalid {
		for _, raw := range values {
			t.Run(field+"/"+fmt.Sprintf("%q", raw), func(t *testing.T) {
				labels := map[string]string{"nsc.enable": "true", "nsc.routes.main.host": "app.example.test", "nsc.routes.main." + field: raw}
				// Repeated HTTPS must reach the duplicate check rather than TLS compatibility.
				if raw == "https,https" {
					labels["nsc.routes.main.tls"] = "custom"
				}
				if _, err := ParseLabels(labels, defaultPolicy()); err == nil {
					t.Fatalf("invalid %s accepted: %q", field, raw)
				}
			})
		}
	}
	for _, key := range []string{"nsc.routes.", "nsc.routes.no-field", "nsc.routes..host", "nsc.routes.main."} {
		if _, err := ParseLabels(map[string]string{"nsc.enable": "true", key: "x"}, defaultPolicy()); err == nil {
			t.Fatalf("malformed route key accepted: %q", key)
		}
	}
	if _, err := ParseLabels(map[string]string{"nsc.enable": "true", "nsc.routes.main.host": "app.test", "nsc.routes.main.entrypoints": "https", "nsc.routes.main.tls": "custom", "nsc.routes.main.redirect-http-to-https": "true"}, defaultPolicy()); err == nil {
		t.Fatal("redirect accepted without HTTP entrypoint")
	}
}

func TestRouteLabelCanonicalOrderAndHostPolicy(t *testing.T) {
	labels := map[string]string{"nsc.enable": "true", "nsc.protocol": "https"}
	for _, name := range []string{"z", "a"} {
		for field, value := range map[string]string{"host": "BÜCHER.Example.", "path": "/v1/../api", "tls": "custom", "entrypoints": " HTTPS, HTTP ", "redirect-http-to-https": "true", "priority": "-3", "strip-path": "true", "websocket": "false", "port": "443", "server-name": "ORIGIN.Example.", "protocol": "https"} {
			labels["nsc.routes."+name+"."+field] = value
		}
	}
	got, err := ParseLabels(labels, defaultPolicy())
	if err != nil || len(got.Routes) != 2 {
		t.Fatalf("compatible host rules rejected: %+v %v", got, err)
	}
	want := model.DiscoveredRoute{ExternalKey: "a", Host: "xn--bcher-kva.example", Path: "/api/", TLSMode: model.TLSCustom, HTTPEnabled: true, HTTPSEnabled: true, HTTPSRedirect: true, Priority: -3, StripPath: true, TargetPort: 443, UpstreamProtocol: "https", UpstreamServerName: "origin.example"}
	if !reflect.DeepEqual(got.Routes[0], want) || got.Routes[1].ExternalKey != "z" {
		t.Fatalf("route semantics changed: %+v", got.Routes)
	}
	labels["nsc.routes.z.redirect-http-to-https"] = "false"
	if _, err := ParseLabels(labels, defaultPolicy()); err == nil {
		t.Fatal("conflicting host entry policy accepted")
	}
	labels = map[string]string{"nsc.enable": "true", "nsc.routes.main.host": "app.test", "nsc.routes.main.strip-path": "", "nsc.routes.main.protocol": "http"}
	got, err = ParseLabels(labels, defaultPolicy())
	if err != nil || got.Routes[0].StripPath || got.Routes[0].UpstreamProtocol != "http" {
		t.Fatalf("explicit defaults: %+v %v", got, err)
	}
}
