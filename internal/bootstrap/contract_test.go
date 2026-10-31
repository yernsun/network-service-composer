package bootstrap

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStrictConfiguration(t *testing.T) {
	c := testConfig()
	data, _ := json.Marshal(c)
	if _, err := DecodeConfig(data); err != nil {
		t.Fatal(err)
	}
	defaults := c
	defaults.ConsolePort = 0
	defaults.Nodes[0].SSHPort = 0
	data, _ = json.Marshal(defaults)
	parsed, err := DecodeConfig(data)
	if err != nil || parsed.ConsolePort != 8443 || parsed.Nodes[0].SSHPort != 22 {
		t.Fatal("defaults", err)
	}
	for _, data := range []string{"{", "unknown: true", string(data) + "\n---\nmode: single"} {
		if _, err := DecodeConfig([]byte(data)); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Mode = "compose" }, func(c *Config) { c.Name = "x;cmd" }, func(c *Config) { c.NodeDir = "/" }, func(c *Config) { c.NodeDir = "relative" },
		func(c *Config) { c.NodeDir = "/srv/../srv/nsc" },
		func(c *Config) { c.ConsolePort = 443 }, func(c *Config) { c.ConsolePort = 65536 },
		func(c *Config) { c.ConsolePort = 9443 }, func(c *Config) { c.ConsolePort = 9444 },
		func(c *Config) { c.Images.Gateway = "nginx:latest" }, func(c *Config) { c.KnownHostsFile = "relative" }, func(c *Config) { c.AdminPasswordFile = "relative" },
		func(c *Config) { c.Nodes[0].Address = "host.invalid" },
		func(c *Config) { c.Nodes[0].Name = "../escape" },
		func(c *Config) { c.Nodes[0].SSHPort = 65536 }, func(c *Config) { c.Nodes[0].SSHUser = "-option" }, func(c *Config) { c.Nodes[0].SSHKeyFile = "relative" },
		func(c *Config) { c.Nodes[0].SSHPort = 0 }, func(c *Config) { c.Nodes = append(c.Nodes, c.Nodes[0]) }, func(c *Config) { c.DataNode = "missing" },
		func(c *Config) { c.MaintenanceNode = "missing" }, func(c *Config) { c.Nodes[0].Manager = false }, func(c *Config) { c.Mode = "multi" },
		func(c *Config) { c.Nodes = nil },
	} {
		altered := testConfig()
		mutate(&altered)
		if err := altered.Validate(); err == nil {
			t.Fatalf("invalid config accepted: %+v", altered)
		}
	}
	multi := testConfig()
	multi.Mode = "multi"
	multi.Nodes = append(multi.Nodes, Node{Name: "two", Address: "192.0.2.2", SSHUser: "operator", SSHPort: 22, Manager: true}, Node{Name: "three", Address: "192.0.2.3", SSHUser: "operator", SSHPort: 22, Manager: true})
	if err := multi.Validate(); err != nil {
		t.Fatal(err)
	}
	multi.Nodes[1].Manager = false
	if err := multi.Validate(); err == nil {
		t.Fatal("new two-Manager topology accepted")
	}
	multi.Nodes[0].Manager = false
	if err := multi.Validate(); err == nil {
		t.Fatal("worker entry permitted")
	}
	c = testConfig()
	c.Adoption = &Adoption{ControllerConfigFile: "/recovery/controller.yaml", ClusterID: "cluster", DataNodeID: "node", Volume: "data", ServiceIDs: map[string]string{"controller": "c", "nginx": "g", "nginx-sandbox": "s", "postgresql": "p"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Adoption.ServiceIDs["nginx"] = ""
	if err := c.Validate(); err == nil {
		t.Fatal("missing service identity")
	}
	c.Adoption.ServiceIDs["nginx"] = "c"
	if err := c.Validate(); err == nil {
		t.Fatal("duplicate adoption identity")
	}
	c.Adoption.Volume = ""
	if err := c.Validate(); err == nil {
		t.Fatal("adoption without storage proof")
	}
	if ValidIdempotencyKey(strings.Repeat("x", 129)) || ValidIdempotencyKey("invalid\nkey") || !ValidIdempotencyKey("valid-key") {
		t.Fatal("idempotency validation")
	}
}

func TestRegistryAuthenticationUsesOnlyAbsolutePrivateFileReferences(t *testing.T) {
	cfg := testConfig()
	cfg.RegistryAuthFile = "relative-auth.json"
	if err := cfg.Validate(); err == nil {
		t.Fatal("relative registry credential reference accepted")
	}
	cfg.RegistryAuthFile = "/private/registry-auth.json"
	if err := cfg.Validate(); err != nil {
		t.Fatal("absolute registry reference rejected", err)
	}
}
