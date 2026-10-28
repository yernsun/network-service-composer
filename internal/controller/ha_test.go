package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/controlplane"
)

func TestReadinessRequiresRunningLeaderWorkersAndLiveAuthority(t *testing.T) {
	controller, err := New(t.Context(), controllerConfigFixture(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.store.Close()
	defer controller.processLock.Close()
	if controller.isReady() {
		t.Fatal("unstarted Controller was ready")
	}
	stop := make(chan struct{})
	entered := make(chan struct{})
	controller.background = []Background{controllerBackgroundFunc(func(ctx context.Context) error {
		close(entered)
		select {
		case <-stop:
			return errors.New("fixture worker stopped")
		case <-ctx.Done():
			return ctx.Err()
		}
	})}
	controller.ready.Store(true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- controller.election.Run(ctx, controller.runLeader) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Leader did not start workers")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !controller.isReady() {
		if time.Now().After(deadline) {
			t.Fatal("live Leader workers stayed unready")
		}
		time.Sleep(time.Millisecond)
	}
	controller.config.Controller.RecoveryMode = true
	if controller.isReady() {
		t.Fatal("recovery mode was ready")
	}
	controller.config.Controller.RecoveryMode = false
	close(stop)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("worker failure was hidden")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker shutdown hung")
	}
	if controller.isReady() || controller.schedulerTerm.Load() != 0 {
		t.Fatal("stopped scheduler remained ready")
	}
	// A valid database lease naming a process that has no active workers is not
	// sufficient readiness evidence, even when it names this same instance.
	state := controlplane.SQLState{Store: controller.store}
	if _, err = state.Claim(t.Context(), controlplane.Identity{ID: controller.config.Controller.InstanceID}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if controller.isReady() {
		t.Fatal("database lease fabricated scheduler readiness")
	}
}
