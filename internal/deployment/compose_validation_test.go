package deployment

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"gopkg.in/yaml.v3"
)

func TestComposeAdmissionFailsBeforeAnyDockerMutation(t *testing.T) {
	f := newWorkerFixture(t)
	rt := f.runtime
	rt.Type = model.RuntimeSwarm
	runner := &recordingRunner{}
	w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, runner)
	value := material{resolved: "services:\n  db:\n    image: mysql:8.4\n    volumes: [data:/data]\nvolumes:\n  data: {}\n"}
	_, err := w.deploy(context.Background(), rt, value)
	var admission *composeAdmissionError
	if !errors.As(err, &admission) || len(runner.calls) != 0 {
		t.Fatalf("admission mutated Docker: %v %+v", err, runner.calls)
	}
}

func TestPortMaterialUsesRuntimeCompatibleNumbers(t *testing.T) {
	f := newWorkerFixture(t)
	w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, &recordingRunner{})
	for _, runtimeType := range []model.RuntimeType{model.RuntimeDocker, model.RuntimeSwarm} {
		rt := f.runtime
		rt.Type = runtimeType
		value := material{resolved: "services: {db: {image: 'mysql:8.4', ports: [{target: 3306, published: '13306'}]}}"}
		_, path, _, _, cleanup, err := w.materialize(context.Background(), rt, value)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		cleanup()
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := yaml.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		port := document["services"].(map[string]any)["db"].(map[string]any)["ports"].([]any)[0].(map[string]any)
		var want any = "13306"
		if runtimeType == model.RuntimeSwarm {
			want = 13306
		}
		if port["published"] != want {
			t.Fatalf("%s port is incompatible: %s", runtimeType, raw)
		}
	}
}

func TestPortAdmissionFailsBeforeDockerMutation(t *testing.T) {
	f := newWorkerFixture(t)
	for _, runtimeType := range []model.RuntimeType{model.RuntimeDocker, model.RuntimeSwarm} {
		rt := f.runtime
		rt.Type = runtimeType
		runner := &recordingRunner{}
		w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, runner)
		value := material{resolved: "services:\n  db:\n    image: mysql:8.4\n    ports: [{target: 3306, published: '65536'}]\n"}
		_, err := w.deploy(context.Background(), rt, value)
		var admission *composeAdmissionError
		if !errors.As(err, &admission) || !strings.Contains(err.Error(), "COMPOSE_PORT_INVALID") || len(runner.calls) != 0 {
			t.Fatalf("invalid publication reached Docker: %v %+v", err, runner.calls)
		}
	}
}

func TestEmptyStackEvidenceIsDistinctFromUnavailableDocker(t *testing.T) {
	f := newWorkerFixture(t)
	for _, tc := range []struct {
		output string
		err    error
		empty  bool
	}{
		{"Nothing found in stack: new-app\n", nil, true},
		{"Nothing found in stack: different-app\n", nil, false},
		{"Nothing found in stack: new-app\n", errors.New("manager unavailable"), false},
		{"permission denied\n", nil, false},
	} {
		runner := &recordingRunner{run: func(call runnerCall) (string, error) { return tc.output, tc.err }}
		w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, runner)
		states, evidence, err := w.swarmStackState(context.Background(), f.runtime, "new-app")
		if (err == nil) != tc.empty || evidence != tc.output || len(states) != 0 || len(runner.calls) != 1 {
			t.Fatalf("incorrect empty-stack evidence: %+v %q %v", states, evidence, err)
		}
	}
}

func TestExecutionRefreshRejectsDrainedNodesAndUnknownResults(t *testing.T) {
	f := newWorkerFixture(t)
	rt := f.runtime
	rt.Type = model.RuntimeSwarm
	for _, mode := range []string{"ready", "drain", "empty", "invalid", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			runner := &recordingRunner{run: func(call runnerCall) (string, error) {
				cmd := strings.Join(call.arguments, " ")
				if strings.Contains(cmd, "node ls") {
					if mode == "unavailable" {
						return "", errors.New("offline")
					}
					if mode == "empty" {
						return "", nil
					}
					return "node-one", nil
				}
				if strings.Contains(cmd, "node inspect") {
					if mode == "invalid" {
						return "unknown", nil
					}
					availability := "active"
					if mode == "drain" {
						availability = "drain"
					}
					return `[{"ID":"node-one","Spec":{"Role":"worker","Availability":"` + availability + `"},"Description":{"Hostname":"worker-01"},"Status":{"State":"ready"}}]`, nil
				}
				t.Fatalf("unexpected Docker mutation: %s", cmd)
				return "", nil
			}}
			w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, runner)
			value := material{resolved: "services:\n  db:\n    image: mysql:8.4\n    volumes: [data:/data]\n    deploy:\n      placement:\n        constraints: ['node.hostname == worker-01']\nvolumes:\n  data: {}\n"}
			err := w.validateComposePlacement(context.Background(), rt, value)
			if (err == nil) != (mode == "ready") {
				t.Fatalf("%s admitted incorrectly: %v", mode, err)
			}
		})
	}
}

func TestStoredReplicaOverrideParticipatesInAdmission(t *testing.T) {
	f := newWorkerFixture(t)
	rt := f.runtime
	rt.Type = model.RuntimeSwarm
	runner := &recordingRunner{run: func(call runnerCall) (string, error) {
		if strings.Contains(strings.Join(call.arguments, " "), "node ls") {
			return "node-one", nil
		}
		return `[{"ID":"node-one","Spec":{"Role":"worker","Availability":"active"},"Description":{"Hostname":"worker-01"},"Status":{"State":"ready"}}]`, nil
	}}
	w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, runner)
	value := material{application: model.Application{ReplicaOverrides: map[string]int{"db": 3}}, resolved: "services:\n  db:\n    image: mysql:8.4\n    volumes: [/data]\n    deploy:\n      placement:\n        constraints: ['node.hostname == worker-01']\n"}
	err := w.validateComposePlacement(context.Background(), rt, value)
	if err == nil {
		t.Fatal("stored scale override bypassed local-storage warning")
	}
	value.application.ReplicaOverrides["db"] = 1
	if err = w.validateComposePlacement(context.Background(), rt, value); err != nil {
		t.Fatalf("single replica blocked: %v", err)
	}
}

func TestSwarmScaleUsesComposeServiceNameForAdmission(t *testing.T) {
	f := newWorkerFixture(t)
	raw := "services:\n  db:\n    image: mysql:8.4\n    volumes: [data:/data]\n    deploy:\n      placement:\n        constraints: ['node.hostname == worker-01']\nvolumes:\n  data: {}\n"
	app, revision := installFixtureApplication(t, f, raw)
	app.ActiveRevisionID = revision.RevisionID
	rt := f.runtime
	rt.Type = model.RuntimeSwarm
	runner := &recordingRunner{run: func(call runnerCall) (string, error) {
		cmd := strings.Join(call.arguments, " ")
		if strings.Contains(cmd, "node ls") {
			return "node-one", nil
		}
		if strings.Contains(cmd, "node inspect") {
			return `[{"ID":"node-one","Spec":{"Role":"worker","Availability":"active"},"Description":{"Hostname":"worker-01"},"Status":{"State":"ready"}}]`, nil
		}
		t.Fatalf("scale mutated Docker before warning acknowledgement: %s", cmd)
		return "", nil
	}}
	w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, runner)
	_, err := w.runServiceOperation(context.Background(), rt, app, "SCALE", operationInput{ServiceName: app.ProjectName + "_db", ServiceKey: app.ProjectName + "_db", Replicas: 3})
	var admission *composeAdmissionError
	if !errors.As(err, &admission) || !strings.Contains(err.Error(), "warnings require current confirmation") {
		t.Fatalf("qualified scale bypassed warning: %v", err)
	}
}
