package deployment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestSwarmInspectionRejectsIncompleteOrMalformedEvidence(t *testing.T) {
	failure := errors.New("Docker unavailable")
	for _, tc := range []struct {
		name, services, inspect    string
		serviceError, inspectError error
		want                       string
		count                      int
	}{
		{"empty", "\n\n", "", nil, nil, "", 0},
		{"valid", " demo_web | 2/2\n\ndemo_api|1/1 ", "demo_web|4| COMPLETED \n\ndemo_api|9|", nil, nil, "", 2},
		{"list failure", "partial", "", failure, nil, "Docker unavailable", 0},
		{"missing delimiter", "demo_web", "", nil, nil, "parse Swarm stack", 0},
		{"missing name", "|1/1", "", nil, nil, "parse Swarm stack", 0},
		{"inspect failure", "demo_web|1/1", "partial", nil, failure, "Docker unavailable", 0},
		{"malformed inspection", "demo_web|1/1", "demo_web|1", nil, nil, "parse Swarm service inspection", 0},
		{"unexpected service", "demo_web|1/1", "other|1|completed", nil, nil, "unexpected service", 0},
		{"invalid version", "demo_web|1/1", "demo_web|not-a-version|completed", nil, nil, "version", 0},
		{"negative version", "demo_web|1/1", "demo_web|-1|completed", nil, nil, "version", 0},
		{"zero version", "demo_web|1/1", "demo_web|0|completed", nil, nil, "omitted service", 0},
		{"missing inspected service", "demo_web|1/1", "\n", nil, nil, "omitted service", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &recordingRunner{run: func(call runnerCall) (string, error) {
				if strings.Contains(strings.Join(call.arguments, " "), "stack services") {
					return tc.services, tc.serviceError
				}
				return tc.inspect, tc.inspectError
			}}
			worker := NewWorkerWithRunner(nil, nil, nil, config.Defaults(), nil, runner)
			states, output, err := worker.swarmStackState(context.Background(), model.Runtime{Type: model.RuntimeSwarm}, "demo")
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) || states != nil {
					t.Fatalf("bad evidence accepted: %#v %v", states, err)
				}
				return
			}
			if err != nil || len(states) != tc.count || !strings.Contains(output, tc.services) {
				t.Fatalf("inspection %#v %q %v", states, output, err)
			}
			if tc.count > 0 {
				if states["demo_web"].version != 4 || states["demo_web"].updateState != "completed" || states["demo_api"].replicas != "1/1" {
					t.Fatalf("inspection parsed incorrectly: %#v", states)
				}
			}
		})
	}
}

func TestSwarmConvergenceUnknownAndRollbackStatesAreNotSuccess(t *testing.T) {
	for _, state := range []string{"paused", "rollback_paused", "rollback_completed", "updating", "rollback_started", "future-state"} {
		for _, version := range []uint64{1, 2} {
			baseline := map[string]swarmServiceState{"web": {version: 1, replicas: "2/2", updateState: "completed"}}
			changed, ready, err := evaluateSwarmConvergence(baseline, map[string]swarmServiceState{"web": {version: version, replicas: "2/2", updateState: state}})
			terminal := state == "paused" || state == "rollback_paused" || state == "rollback_completed"
			if ready || changed != (version == 2) || (err != nil) != (version == 2 && terminal) {
				t.Fatalf("%s v%d: changed=%t ready=%t error=%v", state, version, changed, ready, err)
			}
		}
	}
	for _, tc := range []struct {
		value string
		ready bool
	}{{"0/0", true}, {" 2 / 2 ", true}, {"1/2", false}, {"1", false}, {"1/1/1", false}, {"bad/1", false}, {"1/bad", false}} {
		if swarmReplicasReady(tc.value) != tc.ready {
			t.Fatalf("replica readiness %q", tc.value)
		}
	}
	changed, ready, err := evaluateSwarmConvergence(map[string]swarmServiceState{"removed": {version: 1}}, map[string]swarmServiceState{"retained": {version: 1, replicas: "1/1"}})
	if err != nil || !changed || !ready {
		t.Fatalf("service replacement: %t %t %v", changed, ready, err)
	}
}

func TestSwarmWaitRequiresConvergenceAndHonorsCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, replicas, state string
		baseline              uint64
		cancel, fail          bool
		wantError             bool
	}{
		{"changed ready", "1/1", "completed", 1, false, false, false},
		{"paused update", "1/1", "paused", 1, false, false, true},
		{"incomplete canceled", "0/1", "updating", 1, true, false, true},
		{"inspection unavailable", "1/1", "completed", 1, true, true, true},
		{"unchanged canceled", "1/1", "completed", 2, true, false, true},
		{"stable no-op observed twice", "1/1", "completed", 2, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if tc.cancel {
				cancel()
			}
			runner := &recordingRunner{run: func(call runnerCall) (string, error) {
				if tc.fail {
					return "unavailable", errors.New("unavailable")
				}
				if strings.Contains(strings.Join(call.arguments, " "), "stack services") {
					return "demo_web|" + tc.replicas, nil
				}
				return "demo_web|2|" + tc.state, nil
			}}
			worker := NewWorkerWithRunner(nil, nil, nil, config.Defaults(), nil, runner)
			_, err := worker.waitForSwarm(ctx, model.Runtime{Type: model.RuntimeSwarm}, "demo", map[string]swarmServiceState{"demo_web": {version: tc.baseline, replicas: "1/1", updateState: "completed"}})
			if (err != nil) != tc.wantError {
				t.Fatalf("wait error: %v", err)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if tc.name == "stable no-op observed twice" && len(runner.calls) != 4 {
				t.Fatalf("no-op accepted before second observation: %d calls", len(runner.calls))
			}
		})
	}
}
