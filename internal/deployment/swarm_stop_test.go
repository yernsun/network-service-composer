package deployment

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/projectlife"
)

type stopRuntimeFixture struct {
	observation           projectlife.Observation
	observeErr, removeErr error
	observed, removed     int
	services              []model.Service
}

func (f *stopRuntimeFixture) Observe(_ context.Context, _ model.Runtime, _ model.Application, services []model.Service) (projectlife.Observation, error) {
	f.observed++
	f.services = services
	return f.observation, f.observeErr
}

func (f *stopRuntimeFixture) Remove(_ context.Context, _ model.Runtime, _ model.Application, _ []model.Service, _ projectlife.Observation, force bool) error {
	if !force {
		return errors.New("STOP must explicitly authorize stopping running objects")
	}
	f.removed++
	return f.removeErr
}

func TestSwarmStopRequiresExitAndDataReuseEvidence(t *testing.T) {
	for _, mode := range []string{"removed", "already stopped", "offline", "ownership", "named volume", "bind mount", "slow exit", "changed target"} {
		t.Run(mode, func(t *testing.T) {
			f := newWorkerFixture(t)
			app, _ := installFixtureApplication(t, f, "services:\n  web:\n    image: example.test/web:fixed\n")
			runtime := f.runtime
			runtime.Type = model.RuntimeSwarm
			local := &stopRuntimeFixture{observation: projectlife.Observation{Objects: []projectlife.Object{{ID: "owned", Kind: "swarm-service", Active: true}}}}
			var want error
			switch mode {
			case "already stopped":
				app.Status = model.ApplicationStopped
			case "offline":
				want = errors.New("node unavailable")
				local.observeErr = want
			case "ownership":
				want = projectlife.ErrOwnership
				local.observeErr = want
			case "named volume":
				want = projectlife.ErrDataRetention
				local.observation.Preserved = []string{"volume:data"}
			case "bind mount":
				want = projectlife.ErrDataRetention
				local.observation.Preserved = []string{"bind:/data"}
			case "slow exit":
				want = context.DeadlineExceeded
				local.removeErr = want
			case "changed target":
				want = projectlife.ErrChanged
				local.removeErr = want
			}
			runner := &recordingRunner{}
			w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, runner)
			w.lifecycle = local
			output, err := w.stopApplication(context.Background(), runtime, app, "STOP")
			if !errors.Is(err, want) {
				t.Fatalf("unexpected stop result: %v", err)
			}
			if local.observed != 1 || len(local.services) != 1 || local.services[0].ApplicationID != app.ID {
				t.Fatal("runtime did not receive explicitly owned inventory")
			}
			if len(runner.calls) != 0 {
				t.Fatal("STOP used stack cleanup instead of owned service removal")
			}
			blocked := local.observeErr != nil || len(local.observation.Preserved) > 0
			if blocked && local.removed != 0 {
				t.Fatal("removed workload without ownership/data evidence")
			}
			if !blocked && local.removed != 1 {
				t.Fatal("STOP skipped live execution based on database status")
			}
			if err != nil && strings.Contains(output, "verified owned services, tasks and containers removed") {
				t.Fatal("unknown exit reported as success")
			}
			if err == nil && !strings.Contains(output, "shared infrastructure retained") {
				t.Fatal("successful exit evidence missing")
			}
		})
	}
}
