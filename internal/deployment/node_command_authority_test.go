package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/execution/fixtures"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestNodeCommandFencesBothSidesOfTheActualCommand(t *testing.T) {
	for _, scenario := range []string{"read-only", "mutation", "no-context", "foreign-operation", "identity-lost", "authority-lost-during-probe", "authority-lost-after-command", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			f := fixtures.New(t, "VOLUME_CREATE", []byte(`{"name":"fixture"}`))
			ctx := f.Context
			op := f.Task
			if scenario == "no-context" {
				ctx = t.Context()
			}
			if scenario == "foreign-operation" {
				op.ID = "foreign"
			}
			if scenario == "cancelled" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			commands := 0
			advance := func() {
				if _, err := f.Store.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET attempt=attempt+1 WHERE id='op'`); err != nil {
					t.Error(err)
				}
			}
			raw := &recordingRunner{run: func(call runnerCall) (string, error) {
				if strings.Contains(strings.Join(call.arguments, " "), " info --format ") {
					info := f.Info
					if scenario == "identity-lost" {
						info.ID = "replacement"
					}
					if scenario == "authority-lost-during-probe" {
						advance()
					}
					body, err := json.Marshal(info)
					return string(body), err
				}
				commands++
				if scenario == "authority-lost-after-command" {
					advance()
				}
				return "accepted", nil
			}}
			worker := NewWorkerWithRunner(f.Store, nil, f.Keys, f.Config, nil, raw)
			runner := nodeCommandRunner{worker: worker, raw: raw, operation: op, runtime: f.Runtime}
			args := []string{"docker", "--host", localDockerEndpoint, "compose", "up", "-d"}
			if scenario == "read-only" {
				args = []string{"docker", "--host", localDockerEndpoint, "inspect", "fixture"}
			}
			_, err := runner.Run(ctx, "", nil, args)
			if scenario == "mutation" || scenario == "read-only" {
				if err != nil || commands != 1 {
					t.Fatal("authorized command failed", commands, err)
				}
			} else {
				if err == nil {
					t.Fatal("lost command authority accepted", scenario)
				}
				want := 0
				if scenario == "authority-lost-after-command" {
					want = 1
				}
				if commands != want {
					t.Fatal("command crossed a failed authority boundary", scenario, commands)
				}
				if scenario == "identity-lost" && !errors.Is(err, execution.ErrUnavailable) {
					t.Fatal(err)
				}
			}
			record, err := execution.Load(t.Context(), f.Store, f.Task.ID, f.Task.Attempt)
			if err != nil {
				t.Fatal(err)
			}
			wantMarker := scenario == "mutation" || scenario == "authority-lost-after-command"
			if (record.CommandStartedAt != nil) != wantMarker {
				t.Fatal("command marker lost uncertainty boundary", scenario)
			}
			actual, err := f.Store.GetDeploymentOperation(t.Context(), f.Task.ID)
			if err != nil || actual.Status != model.OperationRunning {
				t.Fatal("command check guessed final task outcome", actual.Status, err)
			}
		})
	}
}
