package application

import (
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
)

func TestScopedStorageRejectsResolvedHostAccess(t *testing.T) {
	for _, mount := range []any{"/etc:/host:ro", map[string]any{"type": "bind", "source": "/home", "target": "/host", "read_only": true}, "${HOST_PATH}:/host:ro"} {
		service := map[string]any{"image": "example.test/app:fixed", "volumes": []any{mount}}
		values := []EnvironmentVariable{{Key: "HOST_PATH", Value: func() *string { v := "/var/lib/docker"; return &v }()}}
		input := PreflightInput{ComposeYAML: composeFixture(t, service, nil), Backend: model.RuntimeDocker, Environment: EnvironmentInput{Values: &values}}
		if _, err := Preflight(input); err != nil {
			t.Fatal("administrator mount rejected", err)
		}
		input.RestrictedHostAccess = true
		if _, err := Preflight(input); err == nil || !strings.Contains(err.Error(), "project API keys") {
			t.Fatalf("scoped host access permitted: %v", err)
		}
	}
}

func TestScopedStorageLocalVolumesAndIndirectHostAccess(t *testing.T) {
	base := "services:\n  web:\n    image: example.test/app:fixed\n    volumes: [data:/data]\nvolumes:\n  data: "
	for _, definition := range []string{"{}", "{external: false}"} {
		if _, err := Preflight(PreflightInput{ComposeYAML: base + definition, RestrictedHostAccess: true}); err != nil {
			t.Fatal("application-owned volume rejected", err)
		}
	}
	for _, definition := range []string{"{external: true}", "{name: another-project-data}", "{driver: local}", "{driver_opts: {device: /etc, o: bind, type: none}}"} {
		if err := validateScopedStorage(base + definition); err == nil {
			t.Fatal("global or driver-based volume access permitted")
		}
	}
	if err := validateScopedStorage("["); err == nil {
		t.Fatal("malformed storage accepted")
	}
	for _, mounts := range []string{"[/data]", "[{type: volume, source: data, target: /data}]", "[{type: tmpfs, target: /cache}]"} {
		if err := validateScopedStorage("services:\n  web:\n    volumes: " + mounts); err != nil {
			t.Fatal(err)
		}
	}
}
