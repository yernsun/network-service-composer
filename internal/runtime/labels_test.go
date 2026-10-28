package runtime

import (
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func defaultPolicy() model.ReadinessPolicy {
	return model.ReadinessPolicy{Mode: model.ReadinessAuto, Interval: 2 * time.Second, Timeout: time.Second, SuccessThreshold: 2, FailureThreshold: 3, StartupGracePeriod: 2 * time.Second, StartupTimeout: time.Minute, TCPFallback: true}
}

func TestParseLabels(t *testing.T) {
	value, err := ParseLabels(map[string]string{"nsc.enable": "true", "nsc.port": "8080", "nsc.routes.public.host": "API.Example.com", "nsc.routes.public.path": "/api", "nsc.routes.public.entrypoints": "http,https", "nsc.routes.public.tls": "managed", "nsc.routes.public.websocket": "true"}, defaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !value.Enabled || value.Port != 8080 || len(value.Routes) != 1 {
		t.Fatalf("unexpected config: %#v", value)
	}
	route := value.Routes[0]
	if route.Host != "api.example.com" || route.Path != "/api/" || route.TLSMode != model.TLSManaged || !route.HTTPEnabled || !route.HTTPSEnabled || !route.WebsocketEnabled {
		t.Fatalf("unexpected route: %#v", route)
	}
}

func TestParseLabelsDefaultsRouteToHTTPOnly(t *testing.T) {
	value, err := ParseLabels(map[string]string{
		"nsc.enable":           "true",
		"nsc.port":             "8080",
		"nsc.routes.main.host": "http-only.example.com",
	}, defaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Routes) != 1 || !value.Routes[0].HTTPEnabled || value.Routes[0].HTTPSEnabled || value.Routes[0].TLSMode != model.TLSDisabled {
		t.Fatalf("route without entrypoints must default to HTTP only: %#v", value.Routes)
	}
}
func TestParseLabelsRejectsInvalid(t *testing.T) {
	for _, labels := range []map[string]string{
		{"nsc.enable": "yes"},
		{"nsc.enable": "true", "nsc.port": "70000"},
		{"nsc.enable": "true", "nsc.routes.x.host": "*.example.com"},
		{"nsc.enable": "true", "nsc.routes.x.host": "api.example.com", "nsc.routes.x.path": "/bad path"},
		{"nsc.enable": "true", "nsc.routes.x.host": "api.example.com", "nsc.routes.x.path": "/.well-known/acme-challenge/token"},
		{"nsc.enable": "true", "nsc.routes.x.host": "api.example.com", "nsc.routes.x.tls": "disabled", "nsc.routes.y.host": "API.EXAMPLE.COM", "nsc.routes.y.tls": "managed"},
		{"nsc.enable": "true", "nsc.routes.x.host": "api.example.com", "nsc.routes.x.server-name": "127.0.0.1"},
		{"nsc.enable": "true", "nsc.readiness.scheme": "ftp"},
	} {
		if _, err := ParseLabels(labels, defaultPolicy()); err == nil {
			t.Fatalf("expected error for %#v", labels)
		}
	}
}

func FuzzParseLabels(f *testing.F) {
	for _, seed := range [][3]string{{"8080", "http", "/"}, {"65536", "https", "/api"}, {"abc", "ftp", "/;"}, {"", "", ""}} {
		f.Add(seed[0], seed[1], seed[2])
	}
	f.Fuzz(func(t *testing.T, port, protocol, routePath string) {
		labels := map[string]string{
			"nsc.enable":           "true",
			"nsc.port":             port,
			"nsc.protocol":         protocol,
			"nsc.routes.main.host": "fuzz.example.com",
			"nsc.routes.main.path": routePath,
			"nsc.readiness.mode":   "auto",
		}
		value, err := ParseLabels(labels, defaultPolicy())
		if err != nil {
			return
		}
		if value.Protocol != "http" && value.Protocol != "https" {
			t.Fatalf("invalid accepted protocol %q", value.Protocol)
		}
		if value.Port < 0 || value.Port > 65535 {
			t.Fatalf("invalid accepted port %d", value.Port)
		}
		if len(value.Routes) != 1 || value.Routes[0].Host != "fuzz.example.com" || value.Routes[0].Path == "" || value.Routes[0].Path[0] != '/' {
			t.Fatalf("invalid accepted route: %#v", value.Routes)
		}
	})
}
