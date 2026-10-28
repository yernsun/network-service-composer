package deployment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestNodeSchedulingRetainsIntentWhenAuthorityOrTargetIsUnavailable(t *testing.T) {
	for _, scenario := range []string{"legacy-target", "changed-target", "leader-replaced", "lease-busy", "non-session-lock", "missing-application", "volume-invalid-input", "volume-foreign-engine", "volume-offline-node", "remote-executor-unreachable", "clock-unavailable", "runtime-lookup-unavailable", "runtime-missing", "controller-without-enrollment"} {
		t.Run(scenario, func(t *testing.T) {
			f, w, runner, locker := nodeWorkerFixture(t)
			_, queued := installFixtureApplication(t, f, "services:\n  web:\n    image: nginx:alpine\n")
			ctx := t.Context()
			wantWaiting := true
			switch scenario {
			case "legacy-target":
				if _, err := f.store.DB().ExecContext(ctx, `UPDATE deployment_operations SET runtime_identity='' WHERE id=?`, queued.ID); err != nil {
					t.Fatal(err)
				}
			case "changed-target":
				if _, err := f.store.DB().ExecContext(ctx, `UPDATE runtimes SET endpoint='node://replacement' WHERE id=?`, f.runtime.ID); err != nil {
					t.Fatal(err)
				}
			case "leader-replaced":
				wantWaiting = false
				if _, err := (controlplane.SQLState{Store: f.store}).Claim(ctx, controlplane.Identity{ID: "successor", LockOwner: 11}, time.Minute); err != nil {
					t.Fatal(err)
				}
			case "lease-busy":
				locker.set(execution.StorageLock(f.runtime), 12)
			case "non-session-lock":
				wantWaiting = false
				w.locker = lease.New(f.store.DB(), storage.SQLite)
			case "missing-application":
				wantWaiting = false
				if _, err := f.store.DB().ExecContext(ctx, `UPDATE deployment_operations SET application_id='missing' WHERE id=?`, queued.ID); err != nil {
					t.Fatal(err)
				}
			case "volume-invalid-input", "volume-foreign-engine", "volume-offline-node":
				body := []byte(`{"node_id":"original-node","engine_id":"foreign-engine"}`)
				if scenario == "volume-invalid-input" {
					body = []byte(`{`)
				}
				if scenario == "volume-offline-node" {
					body = []byte(`{"node_id":"original-node","engine_id":"original-engine"}`)
					locker.set(nodecontrol.LockKey("original-node"), 0)
				}
				ciphertext, err := f.keyring.Encrypt(body, []byte("deployment-operation:"+queued.ID))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.store.DB().ExecContext(ctx, `UPDATE deployment_operations SET operation_type='VOLUME_CREATE',application_id='',encrypted_input=? WHERE id=?`, ciphertext, queued.ID); err != nil {
					t.Fatal(err)
				}
				if scenario == "volume-invalid-input" {
					wantWaiting = false
				}
			case "remote-executor-unreachable":
				w.config.Controller.InstanceID = "remote-scheduler"
				w.workerID = "remote-scheduler"
				if _, err := (controlplane.SQLState{Store: f.store}).Claim(ctx, controlplane.Identity{ID: w.workerID, LockOwner: 11}, time.Minute); err != nil {
					t.Fatal(err)
				}
				short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
				ctx = short
				wantWaiting = false
			case "controller-without-enrollment":
				w.execution = nil
				w.executor = nil
			case "runtime-lookup-unavailable":
				wantWaiting = false
				w.store = nodeRuntimeLookupStore{Store: f.store, err: errors.New("control store unavailable")}
			case "runtime-missing":
				w.store = nodeRuntimeLookupStore{Store: f.store, err: storage.ErrNotFound}
			case "clock-unavailable":
				wantWaiting = false
				locker.set(nodecontrol.LockKey("original-node"), 0)
				w.execution.Now = func(context.Context) (time.Time, error) { return time.Time{}, errors.New("clock unavailable") }
			}
			err := w.claimAndRun(ctx)
			if wantWaiting && err != nil {
				t.Fatal("temporary unavailability lost task", err)
			}
			if !wantWaiting && err == nil {
				t.Fatal("invalid authority unexpectedly executed")
			}
			actual, readErr := f.store.GetDeploymentOperation(t.Context(), queued.ID)
			if readErr != nil || actual.Status != model.OperationRunning || len(runner.calls) != 0 {
				t.Fatal("unavailable authority touched Docker or failed business intent", actual.Status, len(runner.calls), readErr)
			}
			if wantWaiting {
				record, err := execution.Load(t.Context(), f.store, queued.ID, actual.Attempt)
				if err != nil || record.State != "WAITING" || record.Reason == "" {
					t.Fatal(record, err)
				}
			}
		})
	}
}

func TestReviewedAttemptResumesButUnknownAttemptOnlyInspects(t *testing.T) {
	for _, reviewed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unreviewed", true: "reviewed"}[reviewed], func(t *testing.T) {
			f, w, runner, locker := nodeWorkerFixture(t)
			_, queued := installFixtureApplication(t, f, "services:\n  web:\n    image: nginx:alpine\n")
			op, err := f.store.ClaimDeploymentOperation(t.Context(), w.workerID, time.Now().UTC(), time.Now().UTC().Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			state := "UNKNOWN"
			if reviewed {
				state = "RECONCILED"
			}
			if err = execution.Save(t.Context(), f.store, execution.Record{Reference: execution.Reference{OperationID: op.ID, Attempt: op.Attempt}, State: state, CommandStartedAt: &now, SelectedAt: now}, now); err != nil {
				t.Fatal(err)
			}
			if _, err = f.store.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET heartbeat_at=? WHERE id=?`, now.Add(-time.Minute), op.ID); err != nil {
				t.Fatal(err)
			}
			if !reviewed {
				locker.set(nodecontrol.LockKey("original-node"), 0)
			}
			if err = w.claimAndRun(t.Context()); err != nil {
				t.Fatal(err)
			}
			w.WaitExecutors()
			actual, err := f.store.GetDeploymentOperation(t.Context(), queued.ID)
			if err != nil {
				t.Fatal(err)
			}
			if reviewed {
				if actual.Status != model.OperationSucceeded || actual.Attempt != 2 || len(runner.calls) == 0 {
					t.Fatal("reviewed immutable task did not resume", actual.Status, actual.Attempt)
				}
			} else {
				if actual.Status != model.OperationRunning || len(runner.calls) != 0 {
					t.Fatal("unknown effects were replayed", actual.Status, len(runner.calls))
				}
				record, err := execution.Load(t.Context(), f.store, op.ID, actual.Attempt)
				if err != nil || record.State != "UNKNOWN" || record.CommandStartedAt == nil || record.Reason != "reconciliation_executor_unavailable" {
					t.Fatal(record, err)
				}
			}
		})
	}
}

func TestNodePreconditionFailureDoesNotBecomeAnUnknownDockerMutation(t *testing.T) {
	f, w, runner, _ := nodeWorkerFixture(t)
	_, queued := installFixtureApplication(t, f, "services:\n  web:\n    image: nginx:alpine\n")
	if _, err := f.store.DB().ExecContext(t.Context(), `UPDATE application_revisions SET encrypted_resolved=? WHERE id=?`, []byte("invalid ciphertext"), queued.RevisionID); err != nil {
		t.Fatal(err)
	}
	if err := w.claimAndRun(t.Context()); err != nil {
		t.Fatal(err)
	}
	w.WaitExecutors()
	actual, err := f.store.GetDeploymentOperation(t.Context(), queued.ID)
	if err != nil || actual.Status != model.OperationFailed || actual.ErrorCode != "execution_precondition_failed" {
		t.Fatal(actual.Status, actual.ErrorCode, err)
	}
	for _, call := range runner.calls {
		if strings.Contains(strings.Join(call.arguments, " "), " up ") {
			t.Fatal("bad frozen material reached Docker")
		}
	}
}

func TestNodeResourceErrorsRetainUncertainEffects(t *testing.T) {
	f, w, _, locker := nodeWorkerFixture(t)
	_, queued := installFixtureApplication(t, f, "services:\n  web:\n    image: nginx:alpine\n")
	op, err := f.store.ClaimDeploymentOperation(t.Context(), w.workerID, time.Now().UTC(), time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	app, err := f.store.GetApplication(t.Context(), op.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	target, err := w.execution.Select(t.Context(), f.runtime)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := w.execution.InputDigest(op)
	if err != nil {
		t.Fatal(err)
	}
	locks := []execution.LockProof{{Key: execution.StorageLock(f.runtime), Owner: 11}, {Key: "deployment:" + f.runtime.ID + ":" + app.ProjectName, Owner: 11}}
	for _, proof := range locks {
		locker.set(proof.Key, proof.Owner)
	}
	locker.set(execution.ExecutionLock(op.ID, op.Attempt), 11)
	record := execution.Record{Reference: execution.Reference{OperationID: op.ID, Attempt: op.Attempt, LeaderID: w.workerID, LeaderTerm: 1, RuntimeID: f.runtime.ID, RuntimeFingerprint: execution.RuntimeFingerprint(f.runtime), ApplicationID: op.ApplicationID, Namespace: app.ProjectName, RevisionID: op.RevisionID, Type: op.Type, InputSHA256: digest, Locks: locks, Target: target}, State: "RUNNING", ExecutionOwner: 11, SelectedAt: time.Now().UTC()}
	// Read the actual durable term instead of assuming fixture startup order.
	leader, err := w.execution.Leader.Current(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	record.Reference.LeaderTerm = leader.Term
	if err = execution.Save(t.Context(), f.store, record, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	ctx := execution.WithRecord(storage.WithDeploymentOperation(t.Context(), op), w.execution, record)
	sentinel := errors.New("resource failure")
	if err = w.nodeResourceError(t.Context(), sentinel); !errors.Is(err, sentinel) {
		t.Fatal("legacy result changed", err)
	}
	if err = w.nodeResourceError(ctx, sentinel); !errors.Is(err, sentinel) {
		t.Fatal("untouched resource was uncertain", err)
	}
	if err = execution.BeforeMutation(ctx, op.ID, op.RuntimeID); err != nil {
		t.Fatal(err)
	}
	if err = w.nodeResourceError(ctx, sentinel); !errors.Is(err, execution.ErrUnknown) {
		t.Fatal("accepted mutation became ordinary failure", err)
	}
	actual, err := f.store.GetDeploymentOperation(t.Context(), queued.ID)
	if err != nil || actual.Status != model.OperationRunning {
		t.Fatal(actual.Status, err)
	}
}

type nodeRuntimeLookupStore struct {
	storage.Store
	err error
}

func (s nodeRuntimeLookupStore) GetRuntime(context.Context, string) (model.Runtime, error) {
	return model.Runtime{}, s.err
}
