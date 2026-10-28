package core

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestNormalizeHost(t *testing.T) {
	value, err := NormalizeHost("BÜCHER.Example.")
	if err != nil {
		t.Fatal(err)
	}
	if value != "xn--bcher-kva.example" {
		t.Fatalf("unexpected host %q", value)
	}
	for _, invalid := range []string{"*.example.com", "https://example.com", "127.0.0.1", "bad host"} {
		if _, err := NormalizeHost(invalid); err == nil {
			t.Errorf("expected %q to fail", invalid)
		}
	}
}

func TestCatalogVersionInfersManifestAndREADMEFields(t *testing.T) {
	files := map[string]string{
		"docker/docker-compose.yml": "services:\n  web:\n    image: nginx:1.29-alpine\n    environment:\n      PASSWORD: ${PASSWORD:?required}\n",
		"docker/.env.example":       "PASSWORD=\n",
		"nsc-app.yaml":              "version: 2.4.1\nvariables:\n  - key: PASSWORD\n    required: true\n    secret: true\n",
		"README.md":                 "# README application\n\nInferred description.\n",
	}
	value, err := (&Core{}).catalogVersionFromFiles("source", "demo", "", "", "", "commit", "artifact", "", files, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if value.Name != "README application" || value.Description != "Inferred description." || value.Version != "2.4.1" || len(value.Services) != 1 {
		t.Fatalf("unexpected inferred catalog version: %#v", value)
	}
	if _, err = (&Core{}).catalogVersionFromFiles("source", "demo", "", "", "", "commit", "artifact", "", map[string]string{
		"docker/docker-compose.yml": "services:\n  web:\n    image: nginx:alpine\n",
	}, time.Now()); err == nil || !strings.Contains(err.Error(), "version is required") {
		t.Fatalf("missing version was accepted: %v", err)
	}
}

func TestCatalogURLAndAddressPolicy(t *testing.T) {
	for _, value := range []string{"http://example.com/index.json", "https://localhost/index.json", "https://127.0.0.1/index.json", "https://user@example.com/index.json"} {
		if err := validateCatalogURL(value); err == nil {
			t.Fatalf("unsafe catalog URL %q was accepted", value)
		}
	}
	if err := validateCatalogURL("https://catalog.example.com/index.json"); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "169.254.1.1", "100.64.0.1", "192.0.2.1", "198.18.0.1", "203.0.113.1", "::1", "2001:db8::1"} {
		if publicCatalogAddress(net.ParseIP(address)) {
			t.Fatalf("private catalog address %s was accepted", address)
		}
	}
	if !publicCatalogAddress(net.ParseIP("8.8.8.8")) {
		t.Fatal("public catalog address was rejected")
	}
}

func TestCatalogPinMustBeCryptographicallyBoundToFetchedIndex(t *testing.T) {
	commit := strings.Repeat("a", 40)
	if !catalogURLContainsCommit("https://raw.example/repository/"+commit+"/index.json", commit) {
		t.Fatal("immutable Git URL did not recognize its commit path segment")
	}
	if catalogURLContainsCommit("https://raw.example/repository/main/index.json?commit="+commit, commit) {
		t.Fatal("mutable URL query was accepted as a Git commit binding")
	}
	payload := []byte(`{"schema_version":1,"versions":[]}`)
	if digestBytes(payload) != digestString(string(payload)) {
		t.Fatal("catalog byte digest is inconsistent")
	}
}

func FuzzNormalizeHost(f *testing.F) {
	for _, seed := range []string{"example.com", "BÜCHER.Example.", "*.example.com", "127.0.0.1", "bad host", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		value, err := NormalizeHost(input)
		if err != nil {
			return
		}
		if value != strings.ToLower(value) || strings.ContainsAny(value, "*/\\:#? \t\r\n") {
			t.Fatalf("unsafe normalized host %q from %q", value, input)
		}
		second, err := NormalizeHost(value)
		if err != nil || second != value {
			t.Fatalf("host normalization is not idempotent: %q -> %q (%v)", value, second, err)
		}
	})
}

func FuzzNormalizePath(f *testing.F) {
	for _, seed := range []string{"/", "/api", "/api//v1/../v2", "/; return 200;", "relative", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		value, err := NormalizePath(input)
		if err != nil {
			return
		}
		if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#\x00\r\n{};\"'\\") {
			t.Fatalf("unsafe normalized path %q from %q", value, input)
		}
		second, err := NormalizePath(value)
		if err != nil || second != value {
			t.Fatalf("path normalization is not idempotent: %q -> %q (%v)", value, second, err)
		}
	})
}

func TestNormalizePath(t *testing.T) {
	tests := map[string]string{"": "/", "/": "/", "/api": "/api/", "/api//v1/../v2": "/api/v2/"}
	for input, want := range tests {
		got, err := NormalizePath(input)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("NormalizePath(%q)=%q want %q", input, got, want)
		}
	}
}

func TestServicePatchNullableOverrides(t *testing.T) {
	var patch ServicePatch
	if err := json.Unmarshal([]byte(`{"protocol_override":null,"target_port_override":8443,"readiness_override":null}`), &patch); err != nil {
		t.Fatal(err)
	}
	if !patch.ProtocolOverride.Set || patch.ProtocolOverride.Value != nil {
		t.Fatalf("explicit null was not preserved: %#v", patch.ProtocolOverride)
	}
	if !patch.TargetPortOverride.Set || patch.TargetPortOverride.Value == nil || *patch.TargetPortOverride.Value != 8443 {
		t.Fatalf("concrete override was not preserved: %#v", patch.TargetPortOverride)
	}
	if patch.EnabledOverride.Set {
		t.Fatalf("omitted override was not preserved: %#v", patch.EnabledOverride)
	}
	if !patch.ReadinessOverride.Set || patch.ReadinessOverride.Value != nil {
		t.Fatalf("nullable readiness override was not preserved: %#v", patch.ReadinessOverride)
	}
}

func TestNormalizePathRejectsWhitespaceAndControlCharacters(t *testing.T) {
	for _, value := range []string{"/with space", "/with\ttab", "/with\u00a0nbsp", "/with\x00nul", "/.well-known/acme-challenge/token"} {
		if _, err := NormalizePath(value); err == nil {
			t.Fatalf("NormalizePath(%q) accepted an unsafe path", value)
		}
	}
}

func TestValidateDockerEndpointMatchesSDKTransports(t *testing.T) {
	for _, value := range []string{"unix:///var/run/docker.sock", "tcp://docker.example.com:2376", "npipe:////./pipe/docker_engine"} {
		if err := validateDockerEndpoint(value); err != nil {
			t.Fatalf("validateDockerEndpoint(%q)=%v", value, err)
		}
	}
	for _, value := range []string{"http://docker.example.com:2375", "https://docker.example.com:2376", "ssh://docker.example.com", "unix://"} {
		if err := validateDockerEndpoint(value); err == nil {
			t.Fatalf("validateDockerEndpoint(%q) accepted an unsupported transport", value)
		}
	}
}

func TestValidateACMESelection(t *testing.T) {
	cfg := config.Defaults()
	cfg.ACME.DNSProviders["edge-dns"] = config.DNSProviderProfile{Provider: "cloudflare"}
	application := &Core{config: cfg}

	challenge, reference, err := application.validateACMESelection(context.Background(), model.TLSManaged, "", "")
	if err != nil || challenge != model.ACMEHTTP01 || reference != "" {
		t.Fatalf("default selection = %q %q, err=%v", challenge, reference, err)
	}
	challenge, reference, err = application.validateACMESelection(context.Background(), model.TLSManaged, model.ACMEDNS01, "edge-dns")
	if err != nil || challenge != model.ACMEDNS01 || reference != "edge-dns" {
		t.Fatalf("DNS selection = %q %q, err=%v", challenge, reference, err)
	}
	if _, _, err = application.validateACMESelection(context.Background(), model.TLSManaged, model.ACMEDNS01, "missing"); err == nil {
		t.Fatal("unconfigured DNS provider reference was accepted")
	}
	if _, _, err = application.validateACMESelection(context.Background(), model.TLSManaged, model.ACMEHTTP01, "edge-dns"); err == nil {
		t.Fatal("HTTP-01 accepted a DNS provider reference")
	}
	challenge, reference, err = application.validateACMESelection(context.Background(), model.TLSDisabled, model.ACMEDNS01, "edge-dns")
	if err != nil || challenge != "" || reference != "" {
		t.Fatalf("disabled TLS did not clear ACME selection: %q %q, err=%v", challenge, reference, err)
	}
}
