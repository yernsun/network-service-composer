package application

import (
	"strings"
	"testing"
)

const dataNodeTemplate = `services:
  database:
    image: example.test/db:1
    volumes: [data:/data]
    deploy:
      replicas: 1
      update_config: {order: stop-first}
      rollback_config: {order: stop-first}
volumes:
  data: {}
`

func TestDataNodePolicyFreezesPersistentPlacementAndWriters(t *testing.T) {
	raw, err := PinDataNode(dataNodeTemplate, "node-one")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateDataNodePolicy(raw, "node-one", nil); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDataNodeChange(raw, raw, "node-one", "node-one"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, node, raw string
		overrides       map[string]int
	}{
		{name: "foreign node", node: "node-two", raw: raw},
		{name: "invalid node", node: "../node", raw: raw},
		{name: "zero replicas", node: "node-one", raw: raw, overrides: map[string]int{"database": 0}},
		{name: "multiple replicas", node: "node-one", raw: raw, overrides: map[string]int{"database": 2}},
		{name: "global", node: "node-one", raw: strings.Replace(raw, "replicas: 1", "replicas: 1\n            mode: global", 1)},
		{name: "external", node: "node-one", raw: strings.Replace(raw, "data: {}", "data: {external: true}", 1)},
		{name: "foreign driver", node: "node-one", raw: strings.Replace(raw, "data: {}", "data: {driver: remote}", 1)},
		{name: "start first", node: "node-one", raw: strings.Replace(raw, "stop-first", "start-first", 1)},
		{name: "anonymous", node: "node-one", raw: strings.Replace(raw, "data:/data", "/data", 1)},
		{name: "invalid yaml", node: "node-one", raw: "["},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ValidateDataNodePolicy(tc.raw, tc.node, tc.overrides) == nil {
				t.Fatal("unsafe persistent policy accepted")
			}
		})
	}
	for _, after := range []string{strings.Replace(raw, "data:/data", "other:/data", 1), strings.Replace(raw, "data: {}", "data: {external: true}", 1), strings.Replace(raw, "volumes:", "removed:", 1)} {
		if ValidateDataNodeChange(raw, after, "node-one", "node-one") == nil {
			t.Fatal("storage replacement accepted")
		}
	}
	if ValidateDataNodeChange(raw, raw, "node-one", "node-two") == nil {
		t.Fatal("data migration silently accepted")
	}
	if _, err := PinDataNode(dataNodeTemplate, ""); err == nil {
		t.Fatal("no data node accepted")
	}
	stateless := "services:\n  web:\n    image: nginx:1\n"
	if pinned, err := PinDataNode(stateless, ""); err != nil || pinned != stateless {
		t.Fatal("stateless app needs data node", err)
	}
}

func TestDataNodeMayDifferFromManagerWithoutHostPrivileges(t *testing.T) {
	raw, err := PinDataNode(dataNodeTemplate, "worker-node")
	if err != nil {
		t.Fatal(err)
	}
	result, err := Preflight(PreflightInput{ComposeYAML: raw, Backend: "swarm", RuntimeNodeID: "manager-node", DataNodeID: "worker-node"})
	if err != nil || !result.SwarmCompatible {
		t.Fatal("manager transport incorrectly restricts data placement", err)
	}
	for _, field := range []string{"extra_hosts: [host.test:127.0.0.1]", "container_name: privileged-name", "volumes: [/srv/data:/data]"} {
		candidate := strings.Replace(raw, "image: example.test/db:1", "image: example.test/db:1\n        "+field, 1)
		if field[:7] == "volumes" {
			candidate = strings.Replace(raw, "data:/data", "/srv/data:/data", 1)
		}
		if _, err := Preflight(PreflightInput{ComposeYAML: candidate, Backend: "swarm", RuntimeNodeID: "manager-node", DataNodeID: "worker-node"}); err == nil {
			t.Fatal("remote placement granted host privileges", field)
		}
	}
}

func TestManagedVolumeNamesForInitialStorageAdmission(t *testing.T) {
	names, err := ManagedVolumeNames("services: {}\nvolumes:\n  data: {}\n  explicit: {name: actual-name}\n", "stack")
	if err != nil || len(names) != 2 {
		t.Fatal(names, err)
	}
	found := map[string]bool{}
	for _, name := range names {
		found[name] = true
	}
	if !found["stack_data"] || !found["actual-name"] {
		t.Fatal(names)
	}
	if _, err := ManagedVolumeNames("volumes: []", "stack"); err == nil {
		t.Fatal("invalid volume mapping accepted")
	}
}

func TestManagedExternalVolumeNamesUseDockerIdentity(t *testing.T) {
	names, err := ManagedVolumeNames("volumes:\n  data: {external: true}\n  other: {external: true, name: original-volume}\n", "stack")
	if err != nil || len(names) != 2 {
		t.Fatalf("external names=%v err=%v", names, err)
	}
	if !(names[0] == "data" && names[1] == "original-volume" || names[1] == "data" && names[0] == "original-volume") {
		t.Fatalf("external volume got project prefix: %v", names)
	}
}
