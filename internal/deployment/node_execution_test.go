package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/hoststats"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
)

type nodeFixtureLocker struct {
	mu     sync.Mutex
	owners map[string]int64
}

func (l *nodeFixtureLocker) TryLock(_ context.Context, key string) (lease.Lease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owners[key] != 0 {
		return nil, lease.ErrBusy
	}
	l.owners[key] = 11
	return nodeFixtureLease{l, key}, nil
}
func (l *nodeFixtureLocker) check(_ context.Context, key string, owner int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if owner != 11 || l.owners[key] != owner {
		return lease.ErrLost
	}
	return nil
}
func (l *nodeFixtureLocker) set(key string, value int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.owners[key] = value
}

type nodeFixtureLease struct {
	locker *nodeFixtureLocker
	key    string
}

func (l nodeFixtureLease) Check(ctx context.Context) error              { return l.locker.check(ctx, l.key, 11) }
func (l nodeFixtureLease) Release(context.Context) error                { l.locker.set(l.key, 0); return nil }
func (l nodeFixtureLease) SessionID(ctx context.Context) (int64, error) { return 11, l.Check(ctx) }

// The local orchestration fixture deliberately injects session proofs. Shared
// SQL admission rejects SQLite; real session-fence coverage runs separately.
func nodeWorkerFixture(t *testing.T) (workerFixture, *Worker, *recordingRunner, *nodeFixtureLocker) {
	t.Helper()
	f := newWorkerFixture(t)
	f.config.Security.SessionKey = "isolated-node-worker-session-key-32-bytes"
	f.config.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: f.config.Controller.InstanceID, NodeID: "original-node", EngineID: "original-engine", ClusterID: "original-cluster", URL: "https://controller.example.test:8443"}}
	f.core = core.New(f.store, f.config, f.keyring)
	f.runtime.Endpoint = "node://original-node"
	if err := f.store.UpdateRuntime(t.Context(), &f.runtime, f.runtime.Revision); err != nil {
		t.Fatal(err)
	}
	f.runtime.Revision++
	locker := &nodeFixtureLocker{owners: map[string]int64{nodecontrol.LockKey("original-node"): 11}}
	state := controlplane.SQLState{Store: f.store}
	if _, err := state.Claim(t.Context(), controlplane.Identity{ID: f.config.Controller.InstanceID, LockOwner: 11}, time.Minute); err != nil {
		t.Fatal(err)
	}
	reports := &nodecontrol.Reports{Store: f.store, Keys: f.keyring, Bindings: map[string]nodecontrol.Binding{f.config.Controller.InstanceID: {InstanceID: f.config.Controller.InstanceID, NodeID: "original-node", EngineID: "original-engine", ClusterID: "original-cluster"}}, Now: state.DatabaseTime, CheckOwner: locker.check}
	session, err := reports.Begin(t.Context(), f.config.Controller.InstanceID, "original-node", "original-engine", "boot", 11)
	if err != nil {
		t.Fatal(err)
	}
	now, err := state.DatabaseTime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = reports.Accept(t.Context(), f.config.Controller.InstanceID, nodecontrol.Report{NodeID: "original-node", EngineID: "original-engine", Generation: session.Generation, Sequence: 1, Observation: nodecontrol.Observation{Info: system.Info{ID: "original-engine", Swarm: swarm.Info{NodeID: "original-node"}}, Health: &hoststats.Health{InstanceID: f.config.Controller.InstanceID, Execution: "enabled"}, Complete: true, At: now}}); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{run: func(call runnerCall) (string, error) {
		if strings.Contains(strings.Join(call.arguments, " "), " info --format ") {
			return `{"ID":"original-engine","Swarm":{"NodeID":"original-node"}}`, nil
		}
		return "applied", nil
	}}
	w := NewWorkerWithRunner(f.store, locker, f.keyring, f.config, nil, runner)
	w.ConfigureExecution(&execution.Verifier{Store: f.store, Config: f.config, Keys: f.keyring, Leader: state, Reports: reports, CheckOwner: locker.check, Now: state.DatabaseTime}, nil)
	_ = w.InternalExecutionHandler(t.Context())
	return f, w, runner, locker
}

func TestNodeWorkerExecutesFrozenComposeOnItsOriginalEngine(t *testing.T) {
	f, w, runner, _ := nodeWorkerFixture(t)
	app, op := installFixtureApplication(t, f, "services:\n  web:\n    image: nginx:alpine\n")
	if err := w.claimAndRun(t.Context()); err != nil {
		t.Fatal(err)
	}
	w.WaitExecutors()
	actual, err := f.store.GetDeploymentOperation(t.Context(), op.ID)
	if err != nil || actual.Status != model.OperationSucceeded || actual.ExecutionIdentity.NodeID != "original-node" || actual.ExecutionIdentity.EngineID != "original-engine" {
		t.Fatal(actual, err)
	}
	record, err := execution.Load(t.Context(), f.store, op.ID, actual.Attempt)
	if err != nil || record.State != "COMPLETED" || record.CommandStartedAt == nil || record.Reference.Target.InstanceID != f.config.Controller.InstanceID {
		t.Fatal(record, err)
	}
	stored, err := f.store.GetApplication(t.Context(), app.ID)
	if err != nil || stored.Status != model.ApplicationRunning {
		t.Fatal(stored, err)
	}
	for _, call := range runner.calls {
		for _, argument := range call.arguments {
			if strings.HasPrefix(argument, "node://") {
				t.Fatal("node binding escaped into a command", call.arguments)
			}
		}
	}
}

func TestNodeWorkerWaitsForOriginalHostAndResumesWithoutLosingTask(t *testing.T) {
	for _, condition := range []string{"offline", "management-disabled"} {
		t.Run(condition, func(t *testing.T) {
			f, w, runner, locker := nodeWorkerFixture(t)
			_, op := installFixtureApplication(t, f, "services:\n  web:\n    image: nginx:alpine\n")
			if condition == "offline" {
				locker.set(nodecontrol.LockKey("original-node"), 0)
			} else {
				f.runtime.ManagementEnabled = false
				if err := f.store.UpdateRuntime(t.Context(), &f.runtime, f.runtime.Revision); err != nil {
					t.Fatal(err)
				}
				f.runtime.Revision++
			}
			if err := w.claimAndRun(t.Context()); err != nil {
				t.Fatal(err)
			}
			current, err := f.store.GetDeploymentOperation(t.Context(), op.ID)
			if err != nil || current.Status != model.OperationRunning || len(runner.calls) != 0 {
				t.Fatal("infrastructure outage became business failure", current, err)
			}
			record, err := execution.Load(t.Context(), f.store, op.ID, current.Attempt)
			if err != nil || record.State != "WAITING" || record.Reason == "" {
				t.Fatal(record, err)
			}
			if condition == "offline" {
				locker.set(nodecontrol.LockKey("original-node"), 11)
			} else {
				f.runtime.ManagementEnabled = true
				if err = f.store.UpdateRuntime(t.Context(), &f.runtime, f.runtime.Revision); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = f.store.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET heartbeat_at=? WHERE id=?`, time.Now().UTC().Add(-2*w.config.Management.StaleAfter.Std()), op.ID); err != nil {
				t.Fatal(err)
			}
			if err = w.claimAndRun(t.Context()); err != nil {
				t.Fatal(err)
			}
			w.WaitExecutors()
			current, err = f.store.GetDeploymentOperation(t.Context(), op.ID)
			if err != nil || current.Status != model.OperationSucceeded || current.Attempt != 2 {
				t.Fatal("restored original Engine did not resume task", current, err)
			}
		})
	}
}

func TestNodeWorkerRetainsAcceptedButUnconfirmedMutationWithoutCleanup(t *testing.T) {
	f, w, runner, _ := nodeWorkerFixture(t)
	_, op := installFixtureApplication(t, f, "services:\n  web:\n    image: nginx:alpine\n")
	base := runner.run
	runner.run = func(call runnerCall) (string, error) {
		if strings.Contains(strings.Join(call.arguments, " "), " up ") {
			return "request accepted, response unavailable", errors.New("connection reset")
		}
		return base(call)
	}
	if err := w.claimAndRun(t.Context()); !errors.Is(err, execution.ErrUnknown) {
		t.Fatal(err)
	}
	w.WaitExecutors()
	before := len(runner.calls)
	current, err := f.store.GetDeploymentOperation(t.Context(), op.ID)
	if err != nil || current.Status != model.OperationRunning {
		t.Fatal("lost response became terminal failure", current, err)
	}
	record, err := execution.Load(t.Context(), f.store, op.ID, current.Attempt)
	if err != nil || record.State != "UNKNOWN" || record.CommandStartedAt == nil {
		t.Fatal(record, err)
	}
	if _, err = f.store.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET heartbeat_at=? WHERE id=?`, time.Now().UTC().Add(-2*w.config.Management.StaleAfter.Std()), op.ID); err != nil {
		t.Fatal(err)
	}
	if err = w.claimAndRun(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err = f.store.GetDeploymentOperation(t.Context(), op.ID)
	if err != nil || current.Status != model.OperationRunning || current.Attempt != 2 {
		t.Fatal(current, err)
	}
	record, err = execution.Load(t.Context(), f.store, op.ID, current.Attempt)
	if err != nil || record.State != "UNKNOWN" || record.ReconciledAt == nil || record.InventorySHA256 == "" || len(runner.calls) != before {
		t.Fatal("uncertain mutation was replayed instead of inspected", record, err)
	}
	for _, call := range runner.calls {
		if strings.Contains(strings.Join(call.arguments, " "), " down") {
			t.Fatal("uncertain install triggered cleanup")
		}
	}
	if _, err = json.Marshal(record); err != nil {
		t.Fatal(err)
	}
}

func TestNodeHeartbeatHonorsCommittedReceiptAndRejectsNewAttempt(t *testing.T) {
	f, w, _, _ := nodeWorkerFixture(t)
	_, queued := installFixtureApplication(t, f, "services:\n  web:\n    image: nginx:alpine\n")
	op, err := f.store.ClaimDeploymentOperation(t.Context(), w.workerID, time.Now().UTC(), time.Now().UTC().Add(-time.Minute))
	if err != nil || op.ID != queued.ID {
		t.Fatal(op, err)
	}
	op.Status = model.OperationSucceeded
	if err = f.store.FinishDeploymentOperation(t.Context(), &op, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err = w.heartbeatNodeOperation(t.Context(), op); err != nil {
		t.Fatal("committed receipt lost to heartbeat race", err)
	}
	stale := op
	stale.Attempt--
	if err = w.heartbeatNodeOperation(t.Context(), stale); err == nil {
		t.Fatal("newer attempt was treated as the former executor's receipt")
	}
	actual, err := f.store.GetDeploymentOperation(t.Context(), op.ID)
	if err != nil || actual.Status != model.OperationSucceeded || actual.Attempt != op.Attempt {
		t.Fatal(actual, err)
	}
}
