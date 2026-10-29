package deployment

import (
	"context"
	"errors"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/projectlife"
	"github.com/yernsun/network-service-composer/internal/storage"
	"testing"
	"time"
)

type retentionRuntimeFixture struct {
	stopRuntimeFixture
	verifyErr      error
	verifyFailures int
	afterObserve   func()
}

func (f *retentionRuntimeFixture) Observe(ctx context.Context, rt model.Runtime, app model.Application, services []model.Service) (projectlife.Observation, error) {
	observed, err := f.stopRuntimeFixture.Observe(ctx, rt, app, services)
	if f.afterObserve != nil {
		f.afterObserve()
	}
	return observed, err
}

func (f *retentionRuntimeFixture) VerifyRetention(context.Context, model.Runtime, model.Application, []projectlife.RetainedVolume) error {
	if f.verifyFailures > 0 {
		f.verifyFailures--
		return projectlife.ErrDataRetention
	}
	return f.verifyErr
}
func TestRetainedStorageSurvivesUnknownExitAndRejectsReplacement(t *testing.T) {
	f := newWorkerFixture(t)
	app, operation := installFixtureApplication(t, f, "services:\n  web:\n    image: example.test/web:fixed\n")
	if _, err := f.store.DB().Exec(`UPDATE application_revisions SET configuration_source=? WHERE id=?`, `{"deployment_plan":{"data_node_id":"worker"}}`, operation.RevisionID); err != nil {
		t.Fatal(err)
	}
	volume := projectlife.RetainedVolume{NodeID: "worker", Name: "original-data", Owner: app.ProjectName, CreatedAt: time.Now().UTC()}
	life := &retentionRuntimeFixture{stopRuntimeFixture: stopRuntimeFixture{observation: projectlife.Observation{Retained: []projectlife.RetainedVolume{volume}, Preserved: []string{"volume:original-data"}}, removeErr: context.DeadlineExceeded}}
	w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, &recordingRunner{})
	w.lifecycle = life
	rt := f.runtime
	rt.Type = model.RuntimeSwarm
	if _, err := w.stopSwarmApplication(context.Background(), rt, app); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("unknown exit lost", err)
	}
	if err := w.verifyRetainedStorage(context.Background(), app, "worker"); !errors.Is(err, projectlife.ErrDataRetention) {
		t.Fatal("unknown stop outcome permits subsequent start", err)
	}
	retainedBefore, _, err := w.retainedStorage(context.Background(), app)
	if err != nil || !retainedBefore.RecoveryPending || retainedBefore.Volumes[0].Name != volume.Name {
		t.Fatal("unknown stop lost durable evidence", err)
	}
	life.removeErr = nil
	if _, err := w.stopSwarmApplication(context.Background(), rt, app); err != nil {
		t.Fatal("verified stop retry failed", err)
	}
	if err := w.verifyRetainedStorage(context.Background(), app, "worker"); err != nil {
		t.Fatal("confirmed stop did not unblock original storage", err)
	}
	if err := w.verifyRetainedStorage(context.Background(), app, "other-node"); !errors.Is(err, projectlife.ErrDataRetention) {
		t.Fatal("replacement node accepted", err)
	}
	life.verifyErr = projectlife.ErrDataRetention
	if err := w.verifyRetainedStorage(context.Background(), app, "worker"); err == nil {
		t.Fatal("missing or stale original volume accepted")
	}
	life.verifyErr = nil
	changed := life.observation
	changed.Retained = append([]projectlife.RetainedVolume{}, changed.Retained...)
	changed.Retained[0].Name = "replacement-data"
	if err := w.recordRetainedStorage(context.Background(), app, &changed); !errors.Is(err, projectlife.ErrDataRetention) {
		t.Fatal("external replacement overwrote retention record", err)
	}
	retained, _, err := w.retainedStorage(context.Background(), app)
	if err != nil || retained.Volumes[0].Name != volume.Name {
		t.Fatal("failed replacement destroyed original evidence", err)
	}
}
func TestMissingRetentionRecordCannotRestartAcceptedStatefulRevision(t *testing.T) {
	f := newWorkerFixture(t)
	app, op := installFixtureApplication(t, f, "services:\n  web:\n    image: example.test/web:fixed\n")
	w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, &recordingRunner{})
	if err := w.verifyRetainedStorage(context.Background(), app, "worker"); err != nil {
		t.Fatal("first installation incorrectly needs existing volumes", err)
	}
	if _, err := f.store.DB().Exec(`UPDATE application_revisions SET configuration_source=? WHERE id=?`, `{"deployment_plan":{"data_node_id":"worker"}}`, op.RevisionID); err != nil {
		t.Fatal(err)
	}
	app.Status = model.ApplicationFailed
	if err := w.verifyRetainedStorage(context.Background(), app, "worker"); !errors.Is(err, projectlife.ErrDataRetention) {
		t.Fatal("unknown first installation permits empty-data retry", err)
	}
	app.Status = model.ApplicationPending
	app.ActiveRevisionID = op.RevisionID
	if err := w.verifyRetainedStorage(context.Background(), app, "worker"); !errors.Is(err, projectlife.ErrDataRetention) {
		t.Fatal("missing accepted retention record permits empty data", err)
	}
	app.ActiveRevisionID = "absent"
	if err := w.verifyRetainedStorage(context.Background(), app, "worker"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("unknown active revision accepted", err)
	}
}

func TestCaptureStorageRecordsDataBeforeReadinessFailure(t *testing.T) {
	for _, mode := range []string{"healthy", "unhealthy", "starting", "unknown storage"} {
		t.Run(mode, func(t *testing.T) {
			f := newWorkerFixture(t)
			app, _ := installFixtureApplication(t, f, "services:\n  web:\n    image: example.test/web:fixed\n")
			life := &retentionRuntimeFixture{stopRuntimeFixture: stopRuntimeFixture{observation: projectlife.Observation{Retained: []projectlife.RetainedVolume{{NodeID: "worker", Name: "original-data", Owner: app.ProjectName, CreatedAt: time.Now().UTC()}}, Ready: mode == "healthy", Unhealthy: mode == "unhealthy"}}}
			if mode == "unknown storage" {
				life.observeErr = projectlife.ErrDataRetention
			}
			w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, &recordingRunner{})
			w.lifecycle = life
			guard, stop := context.WithTimeout(t.Context(), 30*time.Second)
			defer stop()
			ctx, cancel := context.WithCancel(guard)
			defer cancel()
			observations := 0
			life.afterObserve = func() {
				observations++
				// A second observation means the first retained-volume record has
				// completed. Cancel at that boundary, not during a timed SQL write.
				if mode == "unknown storage" || mode == "starting" && observations == 2 {
					cancel()
				}
			}
			err := w.captureRetainedStorage(ctx, f.runtime, app)
			if mode == "healthy" && err != nil {
				t.Fatal("healthy mounted service rejected", err)
			}
			if mode == "unhealthy" && !errors.Is(err, projectlife.ErrReadiness) {
				t.Fatal("failed readiness misreported", err)
			}
			if (mode == "starting" || mode == "unknown storage") && !errors.Is(err, context.Canceled) {
				t.Fatal("unknown readiness accepted", err)
			}
			_, _, recordErr := w.retainedStorage(context.Background(), app)
			if (recordErr == nil) != (mode != "unknown storage") {
				t.Fatal("unknown data accepted or known data lost", recordErr)
			}
		})
	}
}

func TestStatefulServiceCommandRequiresObservedStorageReadiness(t *testing.T) {
	for _, mode := range []string{"healthy", "temporary proof", "unknown proof", "unknown", "command failed"} {
		t.Run(mode, func(t *testing.T) {
			f := newWorkerFixture(t)
			app, op := installFixtureApplication(t, f, "services:\n  web: {image: example.test/web:fixed}\n")
			revision, err := f.store.GetApplicationRevision(context.Background(), op.RevisionID)
			if err != nil {
				t.Fatal(err)
			}
			revision.ConfigurationSource.DeploymentPlan.DataNodeID = "worker"
			life := &retentionRuntimeFixture{stopRuntimeFixture: stopRuntimeFixture{observation: projectlife.Observation{Ready: true, Retained: []projectlife.RetainedVolume{{NodeID: "worker", Name: "original-data", Owner: app.ProjectName, CreatedAt: time.Now().UTC()}}}}}
			if mode == "unknown" {
				life.observeErr = projectlife.ErrDataRetention
			}
			if mode == "temporary proof" {
				life.verifyFailures = 1
			}
			if mode == "unknown proof" {
				life.verifyErr = projectlife.ErrDataRetention
			}
			commandErr := errors.New("service update rejected")
			runner := &recordingRunner{}
			if mode == "command failed" {
				runner.run = func(runnerCall) (string, error) { return "rejected", commandErr }
			}
			w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, runner)
			w.lifecycle = life
			baseline := life.observation
			if err := w.recordRetainedStorage(context.Background(), app, &baseline); err != nil {
				t.Fatal(err)
			}
			timeout := 5 * time.Second
			if mode == "unknown" || mode == "unknown proof" {
				timeout = 20 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			_, err = w.runSwarmStorageOperation(ctx, f.runtime, material{application: app, revision: revision}, []string{"service", "update", "--force", "web"})
			ready := mode == "healthy" || mode == "temporary proof"
			if ready && err != nil || (mode == "unknown" || mode == "unknown proof") && !errors.Is(err, context.DeadlineExceeded) || mode == "command failed" && !errors.Is(err, commandErr) {
				t.Fatal("service command completion lost storage boundary", err)
			}
			retained, _, recordErr := w.retainedStorage(context.Background(), app)
			if recordErr != nil || retained.RecoveryPending == ready || retained.Volumes[0].Name != "original-data" {
				t.Fatal("unknown or rejected command lost the original storage recovery barrier", recordErr)
			}
		})
	}
}

func TestUnknownInitialStorageCannotBeBypassedByNewRevision(t *testing.T) {
	f := newWorkerFixture(t)
	app, op := installFixtureApplication(t, f, "services:\n  web: {image: example.test/web:fixed}\n")
	revision, err := f.store.GetApplicationRevision(context.Background(), op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	revision.ConfigurationSource.DeploymentPlan.DataNodeID = "worker"
	w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, &recordingRunner{})
	if err := w.markNewStorageMutation(context.Background(), material{application: app, revision: revision, resolved: "services: {}\nvolumes:\n  data: {}\n"}); err != nil {
		t.Fatal(err)
	}
	// Creating another revision returns the application to PENDING; the durable
	// uncertainty remains independently of that transient lifecycle status.
	app.Status = model.ApplicationPending
	if err := w.verifyRetainedStorage(context.Background(), app, "worker"); !errors.Is(err, projectlife.ErrDataRetention) {
		t.Fatal("empty-data retry allowed", err)
	}
	if err := w.markStorageRecoveryPending(context.Background(), app, false); !errors.Is(err, projectlife.ErrDataRetention) {
		t.Fatal("unknown original storage was cleared", err)
	}
}

func TestSwarmStartRevalidatesRunningApplicationInsteadOfTrustingStoredStatus(t *testing.T) {
	f := newWorkerFixture(t)
	app, operation := installFixtureApplication(t, f, "services:\n  web: {image: example.test/web:fixed}\n")
	app.Status = model.ApplicationRunning
	if err := f.store.UpdateApplication(context.Background(), &app, app.Revision); err != nil {
		t.Fatal(err)
	}
	rt := f.runtime
	rt.Type = model.RuntimeSwarm
	operation.Type = "START"
	operation.RevisionID = "missing-revision"
	runner := &recordingRunner{}
	w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, runner)
	err := w.execute(context.Background(), rt, &operation)
	if !errors.Is(err, storage.ErrNotFound) || len(runner.calls) != 0 {
		t.Fatal("stored RUNNING bypassed source revalidation", err, runner.calls)
	}
}
