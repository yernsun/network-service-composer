package maintenance

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

type interruptedVerificationRuntime struct {
	Runtime
	interrupted bool
}

func (r *interruptedVerificationRuntime) Verify(ctx context.Context, key string) error {
	if key == "controller" && !r.interrupted {
		r.interrupted = true
		return errors.New("isolated executor interruption after acknowledged update")
	}
	return r.Runtime.Verify(ctx, key)
}
func TestResumeAcknowledgedRolloutWaitsForOriginalControllerBinding(t *testing.T) {
	for _, action := range []bootstrap.Action{bootstrap.Repair, bootstrap.Upgrade} {
		t.Run(string(action), func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			original := driver.Runtime.(*DockerRuntime)
			driver.Runtime = &interruptedVerificationRuntime{Runtime: original}
			target := release("b")
			input := bootstrap.PlanRequest{Action: action, Revision: driver.Record.Revision}
			if action == bootstrap.Upgrade {
				input.Release = &target
			} else {
				input.Components = []string{"controller"}
			}
			plan, err := driver.Plan(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			driver.Target = plan.Scope.Release
			before := driver.Record.Database
			executor := bootstrap.Executor{Journal: driver.Journal, Driver: driver}
			operation, err := executor.Execute(t.Context(), driver.Record, plan, plan.Hash, "interrupted-acknowledged-rollout")
			if err == nil || operation.State != bootstrap.Unknown {
				t.Fatal("interruption did not preserve unfinished verification", operation.State, err)
			}
			writes := 0
			for _, call := range engine.calls {
				if strings.HasPrefix(call, "POST /services/controller/update") {
					writes++
				}
			}
			checks := 0
			original.Physical = func(context.Context) error {
				checks++
				if checks == 1 {
					return errors.New("original Controller listener is restarting")
				}
				return nil
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			operation, err = executor.Resume(ctx, driver.Record, operation.ID)
			if err != nil || operation.State != bootstrap.Succeeded || checks < 2 {
				t.Fatal("original binding was not rechecked before continuation", operation.State, checks, err)
			}
			afterWrites := 0
			for _, call := range engine.calls {
				if strings.HasPrefix(call, "POST /services/controller/update") {
					afterWrites++
				}
			}
			if afterWrites != writes || driver.Record.Database != before {
				t.Fatal("resume repeated the acknowledged update or replaced database ownership")
			}
		})
	}
}
func TestCompletedStepBindingRecheckRemainsBoundedAndRejectsDrift(t *testing.T) {
	for _, scenario := range []string{"pending", "timeout", "external-spec", "schema"} {
		t.Run(scenario, func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			runtime := driver.Runtime.(*DockerRuntime)
			checks := 0
			runtime.Physical = func(context.Context) error { checks++; return errors.New("original binding unavailable") }
			step := bootstrap.Step{Key: "update:controller", State: bootstrap.Succeeded}
			if scenario == "pending" {
				step.State = bootstrap.Pending
			}
			if scenario == "external-spec" || scenario == "schema" {
				runtime.Physical = func(context.Context) error { checks++; return nil }
				if scenario == "schema" {
					engine.schema = false
				} else {
					s := engine.services["controller"]
					s.Spec.Name = "external-change"
					engine.services[s.ID] = s
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			err := driver.Check(ctx, step)
			if err == nil {
				t.Fatal("unconfirmed or changed binding passed")
			}
			if scenario == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("binding recheck ignored operation deadline", err)
			}
			if scenario == "pending" && (checks != 1 || errors.Is(err, context.DeadlineExceeded)) {
				t.Fatal("a new write waited through unconfirmed preflight", checks, err)
			}
			assertNoInfrastructureWrite(t, engine.calls)
		})
	}
}
