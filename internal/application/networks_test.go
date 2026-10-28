package application

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDeploymentNetworksPreserveBusinessConnections(t *testing.T) {
	raw := "services:\n  web:\n    image: web:fixed\n  api:\n    image: api:fixed\n    networks: [private]\n  database:\n    image: db:fixed\n    networks:\n      private:\n        aliases: [database]\nnetworks:\n  private: {}\n"
	for _, name := range []string{"", "demoapp", "demoapp-dev"} {
		output, err := DeploymentNetworks(raw, name)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(output), &doc); err != nil {
			t.Fatal(err)
		}
		services := doc["services"].(map[string]any)
		for key, value := range services {
			networks := value.(map[string]any)["networks"].(map[string]any)
			if _, ok := networks[ApplicationNetworkKey]; ok != (name != "") {
				t.Fatalf("%s lost application attachment: %s", key, output)
			}
			original := "private"
			if key == "web" {
				original = "default"
			}
			if _, ok := networks[original]; !ok {
				t.Fatalf("%s lost original network: %s", key, output)
			}
		}
		aliases := services["database"].(map[string]any)["networks"].(map[string]any)["private"].(map[string]any)["aliases"].([]any)
		if len(aliases) != 1 || aliases[0] != "database" {
			t.Fatalf("lost DNS alias: %s", output)
		}
		if name != "" {
			network := doc["networks"].(map[string]any)[ApplicationNetworkKey].(map[string]any)
			if network["name"] != name || network["external"] != true {
				t.Fatalf("network name would receive Stack prefix: %s", output)
			}
		}
	}
}

func TestApplicationNetworkRejectsProtectedNamesAndSourceConflicts(t *testing.T) {
	for _, name := range []string{"nsc_backend", ApplicationNetworkKey} {
		raw := "services:\n  database:\n    image: db:fixed\n    networks: [" + name + "]\nnetworks:\n  " + name + ": {}\n"
		if _, _, err := validateCompose(raw, false, nil, nil, nil); err == nil {
			t.Fatalf("accepted reserved source network %s", name)
		}
	}
	for _, name := range []string{"", "../other", "bad name", "--driver", "nsc-ingress", "ingress", "docker_gwbridge"} {
		if err := ValidateApplicationNetwork(name, "nsc-ingress", "services: {}"); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	for _, raw := range []string{"[", "networks: []", "networks: {nsc_application: {}}", "networks: {private: {name: demoapp}}", "networks: {demoapp: {external: true}}", "networks: {private: []}"} {
		if err := ValidateApplicationNetwork("demoapp", "nsc-ingress", raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if err := ValidateApplicationNetwork("demoapp", "nsc-ingress", "networks: {private: {driver: overlay}}"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"[", "services: []", "services: {web: []}", "services: {web: {networks: 1}}", "services: {web: {networks: [1]}}", "networks: []"} {
		if _, err := DeploymentNetworks(raw, "demoapp"); err == nil {
			t.Fatalf("accepted invalid execution material: %s", raw)
		}
	}
}
