package application

import (
	"strings"
	"testing"
)

func TestSwarmServicePortsPreservesPlacementAndExplicitMetadata(t *testing.T) {
	raw, err := PinDataNode(dataNodeTemplate, "worker")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := SwarmServicePorts(raw, []ServiceSpec{{Name: "database", TargetPort: 3306, PortCertain: true}})
	if err != nil || !strings.Contains(rendered, "nsc.port: \"3306\"") || strings.Contains(rendered, "ports:") {
		t.Fatal("Swarm discovery port was lost or published", rendered, err)
	}
	if err := ValidateDataNodePolicy(rendered, "worker", nil); err != nil {
		t.Fatal("port metadata altered storage strategy", err)
	}
	explicit := strings.Replace(raw, "replicas: 1", "replicas: 1\n            labels: [nsc.port=3307, nsc.protocol=https]", 1)
	rendered, err = SwarmServicePorts(explicit, []ServiceSpec{{Name: "database", TargetPort: 3306, PortCertain: true}})
	if err != nil || !strings.Contains(rendered, "nsc.port: \"3307\"") || !strings.Contains(rendered, "nsc.protocol: https") {
		t.Fatal("explicit discovery metadata overwritten", rendered, err)
	}
}
