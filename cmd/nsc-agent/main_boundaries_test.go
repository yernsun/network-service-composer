package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/id"
	"gopkg.in/yaml.v3"
)

func TestAgentCommandValidatesInputsBeforeConnecting(t *testing.T) {
	for _, stage := range []string{"version", "invalid flag", "missing config", "invalid config", "generation failure", "NGINX start failure", "healthcheck"} {
		t.Run(stage, func(t *testing.T) {
			args := []string{}
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			switch stage {
			case "version":
				args = []string{"--version"}
			case "invalid flag":
				args = []string{"--unsupported"}
			default:
				args = []string{"--config", path}
				if stage == "invalid config" {
					if e := os.WriteFile(path, []byte("agent: {}"), 0600); e != nil {
						t.Fatal(e)
					}
				} else if stage != "missing config" {
					generation := filepath.Join(dir, "generations")
					if stage == "generation failure" {
						if e := os.WriteFile(generation, []byte("reserved path"), 0600); e != nil {
							t.Fatal(e)
						}
					}
					raw, e := yaml.Marshal(map[string]any{"agent": map[string]any{"gateway_id": id.New(id.Gateway), "pool_id": id.New(id.Pool), "controller_address": "127.0.0.1:1", "server_name": "fixture", "ca_cert_file": "missing-ca", "tls_cert_file": "missing-cert", "tls_key_file": "missing-key", "state_dir": dir}, "nginx": map[string]any{"binary": filepath.Join(dir, "missing-nginx"), "generation_dir": generation, "main_config_path": filepath.Join(dir, "nginx.conf")}})
					if e != nil {
						t.Fatal(e)
					}
					if e = os.WriteFile(path, raw, 0600); e != nil {
						t.Fatal(e)
					}
					if _, e = config.LoadAgent(path); e != nil {
						t.Fatal("startup fixture was not valid", e)
					}
				}
			}
			oldArgs := os.Args
			if stage == "healthcheck" {
				args = append(args, "--healthcheck")
			}
			os.Args = append([]string{"nsc-agent"}, args...)
			defer func() { os.Args = oldArgs }()
			e := run()
			if stage == "version" {
				if e != nil {
					t.Fatal(e)
				}
			} else if e == nil {
				t.Fatal("invalid startup reported success", stage)
			}
			if stage == "NGINX start failure" && !strings.Contains(e.Error(), "missing-nginx") {
				t.Fatal("failed before testing NGINX startup", e)
			}
			if stage == "healthcheck" {
				if !strings.Contains(e.Error(), "gateway.healthcheck") {
					t.Fatal(e)
				}
				if _, statErr := os.Stat(filepath.Join(dir, "generations")); !os.IsNotExist(statErr) {
					t.Fatal("healthcheck prepared state", statErr)
				}
			}
		})
	}
}
