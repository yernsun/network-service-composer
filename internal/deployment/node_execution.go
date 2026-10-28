package deployment

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/dockerresource"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/storage"
	"github.com/yernsun/network-service-composer/internal/volumemanager"
)

func (w *Worker) ConfigureExecution(verifier *execution.Verifier, internalTLS *tls.Config) {
	w.execution, w.executorTLS = verifier, internalTLS
	w.executor = &execution.Service{Verifier: verifier, Locker: w.locker, Execute: w.executeNodeAccepted}
}
func (w *Worker) InternalExecutionHandler(ctx context.Context) http.Handler {
	w.executorContext = ctx
	return w.executor.Handler(ctx)
}
func (w *Worker) WaitExecutors() {
	if w.executor != nil {
		w.executor.Wait()
	}
}

func (w *Worker) waitingExecution(ctx context.Context, op model.DeploymentOperation, reason string) error {
	// A Controller without node enrollment can retain a scheduling annotation,
	// but this timestamp never grants execution authority or establishes health.
	now := time.Now().UTC()
	if w.execution != nil {
		var err error
		now, err = w.execution.Now(ctx)
		if err != nil {
			return err
		}
	}
	record, err := execution.Load(ctx, w.store, op.ID, op.Attempt)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if record.CommandStartedAt != nil {
		return execution.ErrUnknown
	}
	record.Reference.OperationID, record.Reference.Attempt = op.ID, op.Attempt
	record.State, record.Reason, record.SelectedAt = "WAITING", reason, now
	return execution.Save(ctx, w.store, record, now)
}

func (w *Worker) runNodeOperation(ctx context.Context, current model.Runtime, op model.DeploymentOperation) error {
	v := w.execution
	rt, err := v.FrozenRuntime(op, current)
	if err != nil {
		return w.waitingExecution(ctx, op, "frozen_target_requires_review")
	}
	// Accepted mutations from an older term are never blindly replayed. Only a
	// read-only reconciliation may authorize a fresh attempt.
	if op.Attempt > 1 {
		// An unavailable reconciliation can leave an intermediate waiting
		// attempt without a marker. That cannot erase an earlier accepted
		// mutation. Only an explicit reviewed record closes this history.
		for attempt := op.Attempt - 1; attempt > 0; attempt-- {
			previous, e := execution.Load(ctx, w.store, op.ID, attempt)
			if e != nil && !errors.Is(e, storage.ErrNotFound) {
				return e
			}
			if previous.State == "RECONCILED" {
				break
			}
			if previous.CommandStartedAt != nil {
				return w.reconcileNodeOperation(ctx, rt, op, previous)
			}
		}
	}
	leader, err := v.Leader.Current(ctx)
	if err != nil || leader.ID != w.workerID || v.Leader.Validate(ctx, leader.ID, leader.Term) != nil {
		return execution.ErrFenced
	}
	var app model.Application
	resource := op.Type == "VOLUME_CREATE" || op.Type == "VOLUME_DELETE" || op.Type == "DOCKER_RESOURCE_CREATE" || op.Type == "DOCKER_RESOURCE_DELETE"
	keys := []string{execution.StorageLock(rt)}
	if !resource {
		app, err = w.store.GetApplication(ctx, op.ApplicationID)
		if err != nil {
			return err
		}
		keys = append(keys, "deployment:"+rt.ID+":"+app.ProjectName)
	} else if strings.HasPrefix(op.Type, "DOCKER_RESOURCE_") {
		keys = []string{"docker-resource:" + rt.ClusterID}
	}
	proofs := []execution.LockProof{}
	held := []lease.Lease{}
	defer func() {
		for i := len(held) - 1; i >= 0; i-- {
			_ = held[i].Release(context.Background())
		}
	}()
	for _, key := range keys {
		lock, e := w.locker.TryLock(ctx, key)
		if e != nil {
			return w.waitingExecution(ctx, op, "execution_lease_unavailable")
		}
		held = append(held, lock)
		session, ok := lock.(lease.SessionLease)
		if !ok {
			return execution.ErrFenced
		}
		owner, e := session.SessionID(ctx)
		if e != nil || owner <= 0 {
			return execution.ErrFenced
		}
		proofs = append(proofs, execution.LockProof{Key: key, Owner: owner})
	}
	target, err := v.Select(ctx, rt)
	if op.Type == "VOLUME_CREATE" || op.Type == "VOLUME_DELETE" {
		raw, _, e := w.keyring.Decrypt(op.EncryptedInput, []byte("deployment-operation:"+op.ID))
		if e != nil {
			return e
		}
		defer clear(raw)
		var input volumemanager.Request
		if json.Unmarshal(raw, &input) != nil {
			return execution.ErrFenced
		}
		target, err = v.SelectPinned(ctx, rt, input.NodeID, input.EngineID)
	}
	if err != nil {
		return w.waitingExecution(ctx, op, "verified_executor_unavailable")
	}
	digest, err := v.InputDigest(op)
	if err != nil {
		return err
	}
	now, err := v.Now(ctx)
	if err != nil {
		return err
	}
	record := execution.Record{Reference: execution.Reference{OperationID: op.ID, Attempt: op.Attempt, LeaderID: leader.ID, LeaderTerm: leader.Term, RuntimeID: rt.ID, RuntimeFingerprint: execution.RuntimeFingerprint(rt), ApplicationID: op.ApplicationID, Namespace: app.ProjectName, ServiceID: op.ServiceID, RevisionID: op.RevisionID, Type: op.Type, InputSHA256: digest, Locks: proofs, Target: target}, State: "SELECTED", SelectedAt: now}
	if _, _, err = v.CheckClaim(ctx, record.Reference); err != nil {
		return err
	}
	if err = execution.Save(ctx, w.store, record, now); err != nil {
		return err
	}
	// The Leader retains task and resource leases while the selected node owns
	// its separate execution lease. Database state survives an HTTP disconnect.
	run, cancel := context.WithTimeout(ctx, w.config.Management.DeployTimeout.Std())
	defer cancel()
	request := execution.Request{OperationID: op.ID, Attempt: op.Attempt, LeaderTerm: leader.Term, Target: target.Identity()}
	if target.InstanceID == w.config.Controller.InstanceID {
		err = w.executor.Accept(run, w.executorContext, leader.ID, request)
	} else {
		var binding config.ControllerNodeConfig
		for _, candidate := range w.config.Controller.Nodes {
			if candidate.InstanceID == target.InstanceID && candidate.NodeID == target.NodeID {
				binding = candidate
				break
			}
		}
		err = execution.Dispatch(run, w.executorTLS, binding, request)
	}
	// A lost acceptance response is deliberately treated like an accepted
	// request until durable execution or an untouched SELECTED record proves it.
	accepted := err == nil
	started := time.Now()
	timer := time.NewTicker(w.config.Management.HeartbeatInterval.Std())
	defer timer.Stop()
	for {
		actual, e := w.store.GetDeploymentOperation(run, op.ID)
		if e != nil {
			return e
		}
		if actual.Attempt != op.Attempt {
			return execution.ErrFenced
		}
		if actual.Status != model.OperationRunning {
			return nil
		}
		status, e := execution.Load(run, w.store, op.ID, op.Attempt)
		if e != nil {
			return e
		}
		if status.State == "UNKNOWN" {
			return execution.ErrUnknown
		}
		if status.ExecutionOwner > 0 && v.CheckOwner(run, execution.ExecutionLock(op.ID, op.Attempt), status.ExecutionOwner) != nil {
			// Completion and lease release can happen between the two reads.
			// An already committed operation is authoritative over this poll.
			latest, readErr := w.store.GetDeploymentOperation(run, op.ID)
			if readErr != nil {
				return readErr
			}
			if latest.Attempt != op.Attempt {
				return execution.ErrFenced
			}
			if latest.Status != model.OperationRunning {
				return nil
			}
			if status.CommandStartedAt == nil {
				return w.waitingExecution(run, op, "executor_connection_unavailable")
			}
			status.State, status.Reason = "UNKNOWN", "executor_lease_unavailable"
			at, clockErr := v.Now(run)
			if clockErr == nil {
				_ = execution.Save(run, w.store, status, at)
			}
			return execution.ErrUnknown
		}
		if !accepted && status.CommandStartedAt == nil && time.Since(started) > 10*time.Second {
			return w.waitingExecution(run, op, "executor_connection_unavailable")
		}
		for _, lock := range held {
			if lock.Check(run) != nil {
				return execution.ErrFenced
			}
		}
		if v.Leader.Validate(run, leader.ID, leader.Term) != nil {
			return execution.ErrFenced
		}
		if e = w.heartbeatNodeOperation(run, op); e != nil {
			return e
		}
		select {
		case <-run.Done():
			return run.Err()
		case <-timer.C:
		}
	}
}

// Completion can commit after the scheduler's status read but before its
// heartbeat. A failed heartbeat alone cannot turn that receipt into failure.
func (w *Worker) heartbeatNodeOperation(ctx context.Context, op model.DeploymentOperation) error {
	err := w.store.HeartbeatDeploymentOperation(ctx, op.ID, w.workerID, time.Now().UTC())
	if err == nil {
		return nil
	}
	latest, readErr := w.store.GetDeploymentOperation(ctx, op.ID)
	if readErr == nil && latest.Attempt == op.Attempt && latest.WorkerID == op.WorkerID && (latest.Status == model.OperationSucceeded || latest.Status == model.OperationFailed) {
		return nil
	}
	return err
}

type nodeCommandRunner struct {
	worker    *Worker
	raw       CommandRunner
	operation model.DeploymentOperation
	runtime   model.Runtime
}

func (r nodeCommandRunner) Run(ctx context.Context, directory string, environment, arguments []string) (string, error) {
	if err := execution.CheckContext(ctx); err != nil {
		return "", err
	}
	probe := *r.worker
	probe.runner = r.raw
	// Identity and quorum are checked immediately before each command as well
	// as against the independently renewed observation lease.
	target, _, err := execution.BoundTarget(ctx, r.operation.ID, r.runtime.ID)
	if err != nil {
		return "", err
	}
	if err = probe.verifyExecutionNode(ctx, model.Runtime{Type: r.runtime.Type, Endpoint: "node://" + target.NodeID, ClusterID: target.ClusterID}); err != nil {
		return "", execution.ErrUnavailable
	}
	for _, arg := range arguments {
		if strings.Contains("|up|down|deploy|create|update|rm|restart|stop|start|scale|", "|"+arg+"|") {
			if err = execution.MarkCommand(ctx); err != nil {
				return "", err
			}
			break
		}
	}
	output, err := r.raw.Run(ctx, directory, environment, arguments)
	if check := execution.CheckContext(ctx); check != nil {
		return output, check
	}
	if err != nil {
		return output, errors.Join(execution.ErrUnknown, err)
	}
	return output, nil
}

func (w *Worker) executeNodeAccepted(ctx context.Context, op model.DeploymentOperation, rt model.Runtime) error {
	target, bound, err := execution.BoundTarget(ctx, op.ID, rt.ID)
	if err != nil || !bound {
		return execution.ErrFenced
	}
	local := *w
	local.workerID = op.WorkerID
	rt.Endpoint = "node://" + target.NodeID
	local.runner = nodeCommandRunner{worker: &local, raw: w.runner, operation: op, runtime: rt}
	if strings.HasPrefix(op.Type, "DOCKER_RESOURCE_") || strings.HasPrefix(op.Type, "VOLUME_") {
		return local.executeDockerResourceBody(ctx, rt, &op)
	}
	err = local.execute(ctx, rt, &op)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if check := execution.CheckContext(ctx); check != nil {
		return check
	}
	if err != nil {
		if errors.Is(err, execution.ErrFenced) || errors.Is(err, execution.ErrUnavailable) || errors.Is(err, execution.ErrUnknown) || errors.Is(err, storage.ErrOperationOwnership) {
			return err
		}
		record, readErr := execution.Load(ctx, w.store, op.ID, op.Attempt)
		if readErr != nil || record.CommandStartedAt != nil {
			return execution.ErrUnknown
		}
		return local.fail(ctx, &op, "execution_precondition_failed", err)
	}
	op.Status = model.OperationSucceeded
	return local.finish(ctx, &op)
}

func (w *Worker) nodeResourceError(ctx context.Context, err error) error {
	started, bound, readErr := execution.MutationStarted(ctx)
	if !bound {
		return err
	}
	if readErr != nil || started {
		return execution.ErrUnknown
	}
	if errors.Is(err, dockerresource.ErrIdentity) || errors.Is(err, volumemanager.ErrIdentity) {
		return execution.ErrUnavailable
	}
	return err
}

func (w *Worker) executionReports() *nodecontrol.Reports {
	if w.execution == nil {
		return nil
	}
	return w.execution.Reports
}
