package application

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSetServiceReplicasChangesOnlySelectedService(t *testing.T) {
	base := `# application comment
services:
  web:
    image: nginx:stable
    deploy: &shared
      replicas: 2 # keep comment
  worker:
    image: busybox:stable
    deploy: *shared
`
	updated, err := SetServiceReplicas(base, "worker", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated, "# application comment") || !strings.Contains(updated, "# keep comment") {
		t.Fatalf("Compose comments were lost: %s", updated)
	}
	var document struct {
		Services map[string]struct {
			Deploy struct {
				Replicas int `yaml:"replicas"`
			} `yaml:"deploy"`
		} `yaml:"services"`
	}
	if err = yaml.Unmarshal([]byte(updated), &document); err != nil {
		t.Fatal(err)
	}
	if document.Services["web"].Deploy.Replicas != 2 || document.Services["worker"].Deploy.Replicas != 0 {
		t.Fatalf("replica change affected another service: %#v", document.Services)
	}
	if _, err = SetServiceReplicas(base, "missing", 1); err == nil {
		t.Fatal("missing service was accepted")
	}
}

func TestScaleRejectsNonReplicatedComposeServices(t *testing.T) {
	for _, source := range []string{
		"services:\n  web:\n    image: nginx:stable\n    deploy:\n      mode: global\n",
		"services:\n  web:\n    image: nginx:stable\n    deploy:\n      mode: replicated-job\n",
		"services:\n  web:\n    image: nginx:stable\n    scale: 1\n",
		"services:\n  web:\n    image: nginx:stable\n    container_name: web\n",
	} {
		if err := ValidateServiceScaling(source, "web", 2); err == nil {
			t.Fatalf("unsupported scale was accepted: %s", source)
		}
	}
}
