package deployment

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestServiceVersionAdmissionRejectsUnreviewedObjects(t *testing.T) {
	for _, tc := range []struct {
		name, mode, names, inspect string
		expected                   model.ServiceVersion
		failure                    bool
		allow                      bool
	}{
		{name: "new", mode: "create", allow: true},
		{name: "collision", mode: "create", names: "demo_hello"},
		{name: "unavailable", mode: "create", failure: true},
		{name: "update", mode: "update", names: "demo_hello", inspect: "id|42|demo", expected: model.ServiceVersion{ID: "id", Version: 42, Stack: "demo"}, allow: true},
		{name: "stale", mode: "update", names: "demo_hello", inspect: "id|43|demo", expected: model.ServiceVersion{ID: "id", Version: 42, Stack: "demo"}},
		{name: "recreated", mode: "update", names: "demo_hello", inspect: "new|42|demo", expected: model.ServiceVersion{ID: "id", Version: 42, Stack: "demo"}},
		{name: "foreign", mode: "update", names: "demo_hello", inspect: "id|42|other", expected: model.ServiceVersion{ID: "id", Version: 42, Stack: "demo"}},
		{name: "removed", mode: "update", expected: model.ServiceVersion{ID: "id", Version: 42, Stack: "demo"}},
		{name: "delete existing", mode: "delete", names: "demo_hello", inspect: "id|42|demo", expected: model.ServiceVersion{ID: "id", Version: 42, Stack: "demo"}, allow: true},
		{name: "delete retry after removal", mode: "delete", expected: model.ServiceVersion{ID: "id", Version: 42, Stack: "demo"}, allow: true},
		{name: "delete rejects replacement", mode: "delete", names: "demo_hello", inspect: "new|42|demo", expected: model.ServiceVersion{ID: "id", Version: 42, Stack: "demo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &recordingRunner{run: func(call runnerCall) (string, error) {
				if tc.failure {
					return "", errors.New("unavailable")
				}
				if strings.Contains(strings.Join(call.arguments, " "), "service ls") {
					return tc.names, nil
				}
				return tc.inspect, nil
			}}
			worker := NewWorkerWithRunner(nil, nil, nil, config.Defaults(), nil, runner)
			value := material{application: model.Application{ProjectName: "demo"}, revision: model.ApplicationRevision{ConfigurationSource: model.DeploymentSource{ServiceScope: &model.ServiceDeploymentScope{Mode: tc.mode, Names: []string{"hello"}, Observed: map[string]model.ServiceVersion{"hello": tc.expected}}}}}
			err := worker.checkServiceVersions(context.Background(), model.Runtime{Type: model.RuntimeSwarm}, value)
			if (err == nil) != tc.allow {
				t.Fatalf("allow=%v error=%v", tc.allow, err)
			}
		})
	}
}

func TestIncrementalExecutionSubmitsOnlySelectedServiceWithoutPrune(t *testing.T) {
	f := newWorkerFixture(t)
	app, _ := installFixtureApplication(t, f, "services: {old: {image: old:1}}")
	f.runtime.Type = model.RuntimeSwarm
	deployed := false
	runner := &recordingRunner{run: func(call runnerCall) (string, error) {
		command := strings.Join(call.arguments, " ")
		if strings.Contains(command, "info") {
			return "true", nil
		}
		if strings.Contains(command, "stack deploy") {
			if strings.Contains(command, "--prune") {
				t.Fatal("incremental deploy pruned Stack")
			}
			for i, arg := range call.arguments {
				if arg == "--compose-file" {
					raw, err := os.ReadFile(call.arguments[i+1])
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(raw), "old:") {
						t.Fatal("submitted retained service")
					}
					if !strings.Contains(string(raw), "hello:") {
						t.Fatal("missing candidate")
					}
				}
			}
			deployed = true
			return "created", nil
		}
		if strings.Contains(command, "stack services") {
			if deployed {
				return "demo_old|1/1\ndemo_hello|2/2", nil
			}
			return "demo_old|1/1", nil
		}
		if strings.Contains(command, "service ls") {
			return "demo_old", nil
		}
		if strings.Contains(command, "service inspect") {
			if deployed {
				return "demo_old|10|\ndemo_hello|11|", nil
			}
			return "demo_old|10|", nil
		}
		return "", nil
	}}
	worker := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, runner)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	value := material{application: app, incremental: true, resolved: "services: {old: {image: old:1}, hello: {image: hello:1, deploy: {replicas: 2}}}", services: []applicationx.ServiceSpec{{Name: "old"}, {Name: "hello"}}, revision: model.ApplicationRevision{ID: "candidate", ConfigurationSource: model.DeploymentSource{ServiceScope: &model.ServiceDeploymentScope{Mode: "create", Names: []string{"hello"}, Observed: map[string]model.ServiceVersion{"hello": {}}}}}}
	if _, err := worker.deploy(ctx, f.runtime, value); err != nil {
		t.Fatal(err)
	}
	if !deployed {
		t.Fatal("no deployment")
	}
}
