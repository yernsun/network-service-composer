package deployment

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func TestSwarmDeploymentRequiresManagerAndConvergence(t *testing.T) {
	for _, mode := range []string{"manager unavailable", "worker node", "submission failed", "converged", "converged with override"} {
		t.Run(mode, func(t *testing.T) {
			f := newWorkerFixture(t)
			a, op := installFixtureApplication(t, f, "services:\n  web:\n    image: example.test/web:fixed\n")
			if mode == "converged with override" {
				a.ReplicaOverrides = map[string]int{"web": 2}
			}
			runtime := f.runtime
			runtime.Type = model.RuntimeSwarm
			failure := errors.New("Docker request failed")
			submitted := false
			project := a.ProjectName
			runner := &recordingRunner{run: func(call runnerCall) (string, error) {
				cmd := strings.Join(call.arguments, " ")
				switch {
				case strings.Contains(cmd, " info "):
					if mode == "manager unavailable" {
						return "unavailable", failure
					}
					if mode == "worker node" {
						return "false\n", nil
					}
					return "true\n", nil
				case strings.Contains(cmd, "stack deploy"):
					submitted = true
					if mode == "submission failed" {
						return "submission failed", failure
					}
					return "accepted\n", nil
				case strings.Contains(cmd, "stack services"):
					if !submitted {
						return "", nil
					}
					return project + "_web|1/1", nil
				case strings.Contains(cmd, "service inspect"):
					return project + "_web|2|completed", nil
				default:
					t.Errorf("unexpected Docker command %q", cmd)
					return "", failure
				}
			}}
			w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, runner)
			value, e := w.loadMaterial(context.Background(), runtime, a, op.RevisionID)
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			output, e := w.deploy(ctx, runtime, value)
			valid := strings.HasPrefix(mode, "converged")
			if valid {
				if e != nil || !strings.Contains(output, "accepted") || !submitted {
					t.Fatal("manager-accepted update failed convergence", output, e)
				}
			} else if e == nil {
				t.Fatal("unsupported or failed submission accepted")
			}
			if mode == "manager unavailable" || mode == "submission failed" {
				if !errors.Is(e, failure) {
					t.Fatal("original Docker failure hidden", e)
				}
			}
			if (mode == "manager unavailable" || mode == "worker node") && submitted {
				t.Fatal("submitted update without manager capability")
			}
			for _, call := range runner.calls {
				if call.directory != "." {
					if _, err := os.Stat(call.directory); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("deployment material retained after command", call.directory, err)
					}
				}
				cmd := strings.Join(call.arguments, " ")
				if strings.Contains(cmd, "stack deploy") && mode == "converged with override" && strings.Count(cmd, "--compose-file") != 2 {
					t.Fatal("saved overrides not included in stack submission", cmd)
				}
			}
		})
	}
}
func TestSwarmLifecycleCommandsPreserveProjectAndDataBoundaries(t *testing.T) {
	for _, action := range []string{"RESTART", "SCALE"} {
		for _, explicit := range []bool{false, true} {
			for _, fail := range []bool{false, true} {
				t.Run(action+"/"+map[bool]string{true: "explicit", false: "derived"}[explicit]+"/"+map[bool]string{true: "failure", false: "success"}[fail], func(t *testing.T) {
					f := newWorkerFixture(t)
					failure := errors.New("Docker command failed")
					runner := &recordingRunner{run: func(call runnerCall) (string, error) {
						if fail {
							return "failed", failure
						}
						return "", nil
					}}
					w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, runner)
					runtime := f.runtime
					runtime.Type = model.RuntimeSwarm
					app, op := installFixtureApplication(t, f, "services:\n  web:\n    image: nginx:1\n")
					app.ProjectName = "owned-project"
					app.ActiveRevisionID = op.RevisionID
					input := operationInput{ServiceName: "web", Replicas: 3}
					if explicit {
						input.ServiceKey = "owned-project_web"
					}
					var e error
					_, e = w.runServiceOperation(context.Background(), runtime, app, action, input)
					if fail && !errors.Is(e, failure) || !fail && e != nil {
						t.Fatal("lifecycle command result hidden", e)
					}
					first := strings.Join(runner.calls[0].arguments, " ")
					want := ""
					if action == "RESTART" {
						want = "service update --force owned-project_web"
					}
					if action == "SCALE" {
						want = "service scale owned-project_web=3"
					}
					if !strings.Contains(first, want) || strings.Contains(first, "--volumes") || strings.Contains(first, "--rmi") {
						t.Fatal("lifecycle command changed target or data boundary", first)
					}
					if fail && len(runner.calls) != 1 {
						t.Fatal("failed command was retried without reconciling")
					}
				})
			}
		}
	}
}
