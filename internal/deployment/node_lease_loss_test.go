package deployment

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/model"
)

type interruptedNodeRunner struct {
	base           CommandRunner
	beforeMutation bool
	entered        chan struct{}
	once           sync.Once
}

func (r *interruptedNodeRunner) Run(ctx context.Context, directory string, environment, arguments []string) (string, error) {
	command := strings.Join(arguments, " ")
	if r.beforeMutation && strings.Contains(command, " info --format ") || !r.beforeMutation && strings.Contains(command, " up ") {
		r.once.Do(func() { close(r.entered) })
		<-ctx.Done()
		return "", ctx.Err()
	}
	return r.base.Run(ctx, directory, environment, arguments)
}

func TestSchedulerReleasesDeadExecutorPromptlyAndRetainsUncertainty(t *testing.T) {
	for _, before := range []bool{false, true} {
		t.Run(map[bool]string{false: "after-write-marker", true: "before-write-marker"}[before], func(t *testing.T) {
			f, w, base, locker := nodeWorkerFixture(t)
			_, queued := installFixtureApplication(t, f, "services:\n  web:\n    image: nginx:alpine\n")
			runner := &interruptedNodeRunner{base: base, beforeMutation: before, entered: make(chan struct{})}
			w.runner = runner
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- w.claimAndRun(ctx) }()
			select {
			case <-runner.entered:
			case <-time.After(3 * time.Second):
				cancel()
				<-result
				w.WaitExecutors()
				t.Fatal("executor did not reach selected boundary")
			}
			current, err := f.store.GetDeploymentOperation(t.Context(), queued.ID)
			if err != nil {
				t.Fatal(err)
			}
			locker.set(execution.ExecutionLock(queued.ID, current.Attempt), 0)
			select {
			case err = <-result:
			case <-time.After(3 * time.Second):
				cancel()
				<-result
				w.WaitExecutors()
				t.Fatal("scheduler waited for deployment timeout instead of lost executor lease")
			}
			if before && err != nil || !before && !errors.Is(err, execution.ErrUnknown) {
				t.Fatal("wrong retained failure classification", before, err)
			}
			w.WaitExecutors()
			stored, err := f.store.GetDeploymentOperation(t.Context(), queued.ID)
			if err != nil || stored.Status != model.OperationRunning || len(stored.EncryptedInput) == 0 {
				t.Fatal("executor loss destroyed task or input", stored.Status, err)
			}
			record, err := execution.Load(t.Context(), f.store, queued.ID, stored.Attempt)
			if err != nil {
				t.Fatal(err)
			}
			want := "UNKNOWN"
			if before {
				want = "WAITING"
			}
			if record.State != want {
				t.Fatal("write boundary evidence was lost", record.State, record.Reason)
			}
		})
	}
}

func TestNodeHeartbeatCannotAcceptAnotherWorkerOrUntouchedRunningState(t *testing.T) {
	f, w, _, _ := nodeWorkerFixture(t)
	_, queued := installFixtureApplication(t, f, "services:\n  web:\n    image: nginx:alpine\n")
	op, err := f.store.ClaimDeploymentOperation(t.Context(), w.workerID, time.Now().UTC(), time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET worker_id='new-owner' WHERE id=?`, queued.ID); err != nil {
		t.Fatal(err)
	}
	if err = w.heartbeatNodeOperation(t.Context(), op); err == nil {
		t.Fatal("another worker's in-flight state became a completion receipt")
	}
	if _, err = f.store.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET worker_id=?,status='FAILED' WHERE id=?`, w.workerID, queued.ID); err != nil {
		t.Fatal(err)
	}
	if err = w.heartbeatNodeOperation(t.Context(), op); err != nil {
		t.Fatal("committed failure receipt was mistaken for a heartbeat failure", err)
	}
}
