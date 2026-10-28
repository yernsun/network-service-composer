package application

import (
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
)

func hostService() map[string]any {
	return map[string]any{
		"image":   "example.test/app:fixed",
		"volumes": []any{map[string]any{"type": "bind", "source": "/srv/app/data", "target": "/data", "read_only": true, "bind": map[string]any{"create_host_path": false}}},
		"deploy":  map[string]any{"placement": map[string]any{"constraints": []any{"node.id == node-one"}}},
	}
}

func TestHostRestrictedMountsPreserveConfiguration(t *testing.T) {
	for _, backend := range []model.RuntimeType{model.RuntimeDocker, model.RuntimeSwarm} {
		service := hostService()
		if backend == model.RuntimeDocker {
			service["container_name"] = "existing-app"
			service["extra_hosts"] = map[string]any{"host.docker.internal": "host-gateway", "db.internal": "192.0.2.10"}
		}
		source := composeFixture(t, service, nil)
		got, err := Preflight(PreflightInput{ComposeYAML: source, Backend: backend, RuntimeNodeID: "node-one"})
		if err != nil || got.ComposeYAML != source || got.ResolvedYAML != source || len(got.Services) != 1 {
			t.Fatalf("bound %s mount was lost or rejected: %v", backend, err)
		}
	}
	service := hostService()
	service["volumes"] = []any{"/srv/app/data:/data:ro"}
	if _, err := Preflight(PreflightInput{ComposeYAML: composeFixture(t, service, nil), RuntimeNodeID: "node-one"}); err != nil {
		t.Fatal("short bind syntax rejected", err)
	}
}

func TestHostMountsRejectMissingForeignAndUnsafeRestrictions(t *testing.T) {
	for _, scenario := range []string{"unbound runtime", "missing restriction", "foreign node", "hostname only", "relative source", "root source", "traversal", "system interface", "docker socket", "invalid target", "automatic host path", "propagation", "invalid read only", "invalid options", "unknown mount field", "invalid constraint", "invalid container name", "invalid extra host", "host list", "build", "privileged", "container name on swarm"} {
		t.Run(scenario, func(t *testing.T) {
			service := hostService()
			mount := service["volumes"].([]any)[0].(map[string]any)
			input := PreflightInput{RuntimeNodeID: "node-one", Backend: model.RuntimeDocker}
			switch scenario {
			case "unbound runtime":
				input.RuntimeNodeID = ""
			case "missing restriction":
				delete(service, "deploy")
			case "foreign node":
				input.RuntimeNodeID = "node-two"
			case "hostname only":
				service["deploy"] = map[string]any{"placement": map[string]any{"constraints": []any{"node.hostname == node-one"}}}
			case "relative source":
				mount["source"] = "./data"
			case "root source":
				mount["source"] = "/"
			case "traversal":
				mount["source"] = "/srv/../etc"
			case "system interface":
				mount["source"] = "/proc/1"
			case "docker socket":
				mount["source"] = "/var/run/docker.sock"
			case "invalid target":
				mount["target"] = "data"
			case "automatic host path":
				mount["bind"] = map[string]any{"create_host_path": true}
			case "propagation":
				mount["bind"] = map[string]any{"propagation": "rshared"}
			case "invalid read only":
				mount["read_only"] = "yes"
			case "invalid options":
				mount["bind"] = "invalid"
			case "unknown mount field":
				mount["volume"] = map[string]any{}
			case "invalid constraint":
				service["deploy"] = map[string]any{"placement": map[string]any{"constraints": []any{123}}}
			case "invalid container name":
				service["container_name"] = "bad/name"
			case "invalid extra host":
				service["extra_hosts"] = map[string]any{"db": "other.example"}
			case "host list":
				service["extra_hosts"] = []any{"db:192.0.2.1"}
			case "build":
				service["build"] = "."
			case "privileged":
				service["privileged"] = true
			case "container name on swarm":
				service["container_name"] = "fixed"
				input.Backend = model.RuntimeSwarm
			}
			input.ComposeYAML = composeFixture(t, service, nil)
			if result, err := Preflight(input); scenario == "unbound runtime" || scenario == "missing restriction" || scenario == "hostname only" {
				if err != nil || len(result.Services) != 1 {
					t.Fatalf("standalone bind requires no placement: %v", err)
				}
			} else if err == nil {
				t.Fatal("unsafe host configuration accepted")
			}
		})
	}
	for _, source := range []string{"./data:/data", "/:/data", "/srv/data:/data:rshared", "/srv/data:/data:ro:extra"} {
		service := hostService()
		service["volumes"] = []any{source}
		if _, err := Preflight(PreflightInput{ComposeYAML: composeFixture(t, service, nil), RuntimeNodeID: "node-one"}); err == nil {
			t.Fatal("unsafe short mount accepted", source)
		}
	}
}

func TestRuntimeNodeIdentityIsNotInferredFromAddresses(t *testing.T) {
	if RuntimeNodeID("node://node-one") != "node-one" {
		t.Fatal("bound identity lost")
	}
	for _, endpoint := range []string{"", "unix:///var/run/docker.sock", "tcp://node-one:2375", "node://", "node://node-one/path", "node://node-one?other", "node://node-one\n"} {
		if RuntimeNodeID(endpoint) != "" {
			t.Fatal("unbound endpoint accepted", endpoint)
		}
	}
	service := hostService()
	delete(service, "volumes")
	delete(service, "deploy")
	if _, err := Preflight(PreflightInput{ComposeYAML: composeFixture(t, service, nil)}); err != nil {
		t.Fatal("ordinary image deployment regressed", err)
	}
	service["volumes"] = []any{"/srv/data:/data"}
	if _, err := Preflight(PreflightInput{ComposeYAML: composeFixture(t, service, nil)}); err != nil {
		t.Fatal("standalone bind rejected", err)
	}
}

func TestHostTemporaryMountPreservesSizeAndRejectsInvalidPaths(t *testing.T) {
	for _, scenario := range []string{"valid", "relative target", "root target", "source", "negative size", "invalid size", "unknown option", "invalid options"} {
		t.Run(scenario, func(t *testing.T) {
			mount := map[string]any{"type": "tmpfs", "target": "/tmp", "tmpfs": map[string]any{"size": 16777216}}
			switch scenario {
			case "relative target":
				mount["target"] = "tmp"
			case "root target":
				mount["target"] = "/"
			case "source":
				mount["source"] = "/srv/data"
			case "negative size":
				mount["tmpfs"] = map[string]any{"size": -1}
			case "invalid size":
				mount["tmpfs"] = map[string]any{"size": "invalid"}
			case "unknown option":
				mount["tmpfs"] = map[string]any{"mode": 777}
			case "invalid options":
				mount["tmpfs"] = "invalid"
			}
			service := hostService()
			service["volumes"] = []any{mount}
			source := composeFixture(t, service, nil)
			result, err := Preflight(PreflightInput{ComposeYAML: source, Backend: model.RuntimeSwarm, RuntimeNodeID: "node-one"})
			if scenario == "valid" {
				if err != nil || result.ResolvedYAML != source {
					t.Fatal("temporary mount was lost", err)
				}
			} else if err == nil {
				t.Fatal("invalid temporary mount accepted")
			}
		})
	}
}
