package main

import (
	"context"
	"errors"
	"testing"
)

func TestIndependentWorkerCLIOnlyAcceptsRecordedJobInputs(t *testing.T) {
	calls := 0
	run := func(ctx context.Context, directory, job, service, task, credential string) error {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("worker has no bounded lifetime")
		}
		if directory != "/var/lib/nsc-bootstrap" || job != "known-job" || service != "known-service" || task != "known-task" || credential != "/run/secrets/nsc-maintenance" {
			t.Fatal("worker input changed")
		}
		return errors.New("isolated runner result")
	}
	t.Setenv("NSC_MAINTENANCE_TASK_ID", "known-task")
	err := bootstrapWorkerWithRunner([]string{"--state-dir", "/var/lib/nsc-bootstrap", "--job", "known-job", "--service-id", "known-service"}, run)
	if err == nil || calls != 1 {
		t.Fatal("worker did not propagate its independent result")
	}
	for _, args := range [][]string{{}, {"--unknown"}, {"--state-dir", "/var/lib/nsc-bootstrap"}, {"--state-dir", "/var/lib/nsc-bootstrap", "--job", "known-job", "--service-id", "known-service", "arbitrary-command"}} {
		if err = bootstrapWorkerWithRunner(args, run); err == nil {
			t.Fatal("untyped worker command accepted")
		}
	}
	if calls != 1 {
		t.Fatal("invalid flags reached the worker")
	}
	if err = bootstrapWorkerCommand([]string{"--state-dir", t.TempDir(), "--job", "known-job", "--service-id", "known-service"}); err == nil {
		t.Fatal("worker proceeded without its private Secret")
	}
}
