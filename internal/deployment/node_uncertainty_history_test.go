package deployment

import (
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestAcceptedUncertaintySurvivesUnavailableAndMissingIntermediateAttempts(t *testing.T) {
	for _, intermediate := range []string{"unavailable", "missing-record", "waiting-without-marker"} {
		t.Run(intermediate, func(t *testing.T) {
			f, w, runner, locker := nodeWorkerFixture(t)
			_, queued := installFixtureApplication(t, f, "services:\n  web:\n    image: nginx:alpine\n")
			op, err := f.store.ClaimDeploymentOperation(t.Context(), w.workerID, time.Now().UTC(), time.Now().UTC().Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if err = execution.Save(t.Context(), f.store, execution.Record{Reference: execution.Reference{OperationID: op.ID, Attempt: op.Attempt}, State: "UNKNOWN", CommandStartedAt: &now, SelectedAt: now}, now); err != nil {
				t.Fatal(err)
			}
			locker.set(nodecontrol.LockKey("original-node"), 0)
			stale := func() {
				if _, err := f.store.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET heartbeat_at=? WHERE id=?`, time.Now().UTC().Add(-time.Minute), op.ID); err != nil {
					t.Fatal(err)
				}
			}
			stale()
			if err = w.claimAndRun(t.Context()); err != nil {
				t.Fatal(err)
			}
			second, err := execution.Load(t.Context(), f.store, op.ID, 2)
			if err != nil || second.CommandStartedAt == nil {
				t.Fatal("unavailable observer erased accepted uncertainty", second.State, err)
			}
			if intermediate == "missing-record" {
				if err = f.store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: second.ControlDocument, Expected: second.Revision, Remove: true}}); err != nil {
					t.Fatal(err)
				}
			}
			if intermediate == "waiting-without-marker" {
				second.State, second.CommandStartedAt = "WAITING", nil
				if err = execution.Save(t.Context(), f.store, second, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			}
			locker.set(nodecontrol.LockKey("original-node"), 11)
			stale()
			if err = w.claimAndRun(t.Context()); err != nil {
				t.Fatal(err)
			}
			w.WaitExecutors()
			actual, err := f.store.GetDeploymentOperation(t.Context(), queued.ID)
			if err != nil || actual.Attempt != 3 || actual.Status != model.OperationRunning || len(runner.calls) != 0 {
				t.Fatal("restored observer blindly replayed an earlier accepted mutation", actual.Status, actual.Attempt, len(runner.calls), err)
			}
			third, err := execution.Load(t.Context(), f.store, op.ID, 3)
			if err != nil || third.State != "UNKNOWN" || third.CommandStartedAt == nil || third.ReconciledAt == nil {
				t.Fatal("successor lost the unresolved write history", third.State, err)
			}
		})
	}
}
