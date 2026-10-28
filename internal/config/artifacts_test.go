package config

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRepositoryManifestsParse(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	paths := []string{
		filepath.Join(root, "api", "openapi.yaml"),
		filepath.Join(root, "compose.yaml"),
		filepath.Join(root, "deploy", "swarm.yaml"),
	}
	workflows, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, workflows...)
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			decoder := yaml.NewDecoder(file)
			for {
				var document yaml.Node
				err = decoder.Decode(&document)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("parse YAML: %v", err)
				}
			}
		})
	}

	imageLock, err := os.ReadFile(filepath.Join(root, "build", "image-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lockDocument any
	if err = json.Unmarshal(imageLock, &lockDocument); err != nil {
		t.Fatalf("parse image lock: %v", err)
	}
}

func TestSwarmRolloutAdmissionPolicy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "swarm.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Services map[string]struct {
			Image  string `yaml:"image"`
			Grace  string `yaml:"stop_grace_period"`
			Deploy struct {
				Mode     string         `yaml:"mode"`
				Update   map[string]any `yaml:"update_config"`
				Rollback map[string]any `yaml:"rollback_config"`
			} `yaml:"deploy"`
			Health struct {
				Test []string `yaml:"test"`
			} `yaml:"healthcheck"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"controller", "gateway"} {
		svc := manifest.Services[name]
		if svc.Deploy.Mode != "global" || svc.Grace != "30s" || len(svc.Health.Test) == 0 || !strings.Contains(svc.Image, ":?") {
			t.Fatalf("unsafe Swarm service %s", name)
		}
		for _, policy := range []map[string]any{svc.Deploy.Update, svc.Deploy.Rollback} {
			if policy["parallelism"] != 1 || policy["order"] != "stop-first" || policy["failure_action"] != "pause" || policy["max_failure_ratio"] != 0 || policy["delay"] != "30s" || policy["monitor"] != "30s" {
				t.Fatalf("unsafe Swarm update/rollback policy: %s", name)
			}
		}
	}
	if strings.Join(manifest.Services["gateway"].Health.Test, " ") != "CMD /usr/local/bin/nsc-agent --healthcheck --config /etc/nsc/agent.yaml" {
		t.Fatal("Gateway healthcheck must inspect local loading evidence")
	}
}

func TestGatewayImageStopsGoEntrypointWithSIGTERM(t *testing.T) {
	path := filepath.Join("..", "..", "build", "Dockerfile.gateway")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "STOPSIGNAL SIGTERM") {
		t.Fatal("gateway image must override the NGINX base image SIGQUIT stop signal")
	}
}
