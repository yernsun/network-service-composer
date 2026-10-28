package deployment

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"
	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/dockerresource"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/lease"
	metricsx "github.com/yernsun/network-service-composer/internal/metrics"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/projectlife"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
	"github.com/yernsun/network-service-composer/internal/volumemanager"
)

type CommandRunner interface {
	Run(context.Context, string, []string, []string) (string, error)
}

type ExecRunner struct{}

const deploymentPath = "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

func (ExecRunner) Run(ctx context.Context, directory string, environment, arguments []string) (string, error) {
	if len(arguments) == 0 {
		return "", errors.New("empty command")
	}
	command := exec.CommandContext(ctx, arguments[0], arguments[1:]...)
	command.Dir = directory
	// Deployment commands deliberately do not inherit the Controller process
	// environment. In particular, this prevents Compose from resolving a
	// missing application variable from NSC's own environment.
	command.Env = append([]string{deploymentPath}, environment...)
	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s: %w", arguments[0], err)
	}
	return string(output), nil
}

type Worker struct {
	store           storage.Store
	locker          lease.Locker
	keyring         *security.Keyring
	config          config.Config
	runner          CommandRunner
	lifecycle       projectlife.Runtime
	logger          *slog.Logger
	workerID        string
	metrics         *metricsx.Metrics
	execution       *execution.Verifier
	executor        *execution.Service
	executorTLS     *tls.Config
	executorContext context.Context
	openEngine      func(model.Runtime, engine.TLSFiles) (*client.Client, error)
}

func NewWorker(store storage.Store, locker lease.Locker, keyring *security.Keyring, cfg config.Config, metrics *metricsx.Metrics, logger *slog.Logger) *Worker {
	workerID := strings.TrimSpace(cfg.Controller.InstanceID)
	if workerID == "" {
		workerID = id.New(id.Operation)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{store: store, locker: locker, keyring: keyring, config: cfg, runner: ExecRunner{}, lifecycle: projectlife.NewDocker(cfg, store, keyring), logger: logger, workerID: workerID, metrics: metrics}
}

func NewWorkerWithRunner(store storage.Store, locker lease.Locker, keyring *security.Keyring, cfg config.Config, logger *slog.Logger, runner CommandRunner) *Worker {
	value := NewWorker(store, locker, keyring, cfg, nil, logger)
	value.runner = runner
	return value
}

func (w *Worker) Run(ctx context.Context) error {
	if !w.config.Management.Enabled {
		<-ctx.Done()
		return ctx.Err()
	}
	ticker := time.NewTicker(w.config.Management.PollInterval.Std())
	defer ticker.Stop()
	for {
		if err := w.claimAndRun(ctx); err != nil && !errors.Is(err, storage.ErrNotFound) && !errors.Is(err, context.Canceled) {
			w.logger.ErrorContext(ctx, "deployment worker", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *Worker) claimAndRun(ctx context.Context) error {
	now := time.Now().UTC()
	operation, err := w.store.ClaimDeploymentOperation(ctx, w.workerID, now, now.Add(-w.config.Management.StaleAfter.Std()))
	if err != nil {
		return err
	}
	ctx = storage.WithDeploymentOperation(ctx, operation)
	runtime, err := w.store.GetRuntime(ctx, operation.RuntimeID)
	if err != nil {
		if execution.NodeID(operation.ExecutionIdentity.Endpoint) != "" {
			if errors.Is(err, storage.ErrNotFound) {
				return w.waitingExecution(ctx, operation, "frozen_target_requires_review")
			}
			// A failed control-store read cannot establish that Docker rejected
			// this frozen task. Retain it for the next healthy scheduling term.
			return err
		}
		return w.fail(ctx, &operation, "runtime_not_found", err)
	}
	if execution.NodeID(runtime.Endpoint) != "" {
		if w.execution == nil {
			return w.waitingExecution(ctx, operation, "verified_executor_unavailable")
		}
		if !w.config.Management.Enabled || !runtime.Enabled || !runtime.ManagementEnabled {
			return w.waitingExecution(ctx, operation, "management_disabled")
		}
		return w.runNodeOperation(ctx, runtime, operation)
	}
	if !w.config.Management.Enabled || !runtime.ManagementEnabled {
		return w.fail(ctx, &operation, "management_disabled", errors.New("global and runtime management switches must both be enabled"))
	}
	if !runtime.Enabled {
		return w.fail(ctx, &operation, "runtime_disabled", errors.New("runtime was disabled before operation execution"))
	}
	if operation.Type == "DOCKER_RESOURCE_CREATE" || operation.Type == "DOCKER_RESOURCE_DELETE" || operation.Type == "VOLUME_CREATE" || operation.Type == "VOLUME_DELETE" {
		return w.runDockerResourceOperation(ctx, runtime, &operation)
	}
	managedApplication, err := w.store.GetApplication(ctx, operation.ApplicationID)
	if err != nil {
		return w.fail(ctx, &operation, "application_not_found", err)
	}
	if !managedApplication.Managed {
		return w.fail(ctx, &operation, "management_disabled", errors.New("application is externally managed"))
	}
	if err = w.store.CheckProjectResource(ctx, managedApplication.ID); err != nil {
		return w.fail(ctx, &operation, "project_deleting", err)
	}
	var storageGate lease.Lease
	defer func() {
		if storageGate != nil {
			_ = storageGate.Release(context.Background())
		}
	}()
	leaseKey := "deployment:" + runtime.ID + ":" + managedApplication.ProjectName
	var held lease.Lease
	for held == nil {
		if storageGate == nil {
			storageGate, err = w.locker.TryLock(ctx, volumemanager.LockKey(runtime))
		} else {
			err = storageGate.Check(ctx)
		}
		if err == nil {
			held, err = w.locker.TryLock(ctx, leaseKey)
		}
		if err == nil {
			break
		}
		if !errors.Is(err, lease.ErrBusy) {
			return w.fail(ctx, &operation, "lease_failed", err)
		}
		if heartbeatErr := w.store.HeartbeatDeploymentOperation(ctx, operation.ID, w.workerID, time.Now().UTC()); heartbeatErr != nil {
			return heartbeatErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(w.config.Management.PollInterval.Std()):
		}
	}
	defer held.Release(context.Background())
	runCtx, cancel := context.WithTimeout(ctx, w.config.Management.DeployTimeout.Std())
	defer cancel()
	stopHeartbeat := make(chan struct{})
	ownershipLost := make(chan error, 1)
	heartbeatTimeout := 2 * w.config.Management.HeartbeatInterval.Std()
	if heartbeatTimeout < time.Second {
		heartbeatTimeout = time.Second
	}
	if heartbeatTimeout > 10*time.Second {
		heartbeatTimeout = 10 * time.Second
	}
	var heartbeat sync.WaitGroup
	heartbeat.Add(1)
	go func() {
		defer heartbeat.Done()
		ticker := time.NewTicker(w.config.Management.HeartbeatInterval.Std())
		defer ticker.Stop()
		for {
			select {
			case <-stopHeartbeat:
				return
			case <-runCtx.Done():
				return
			case now := <-ticker.C:
				heartbeatCtx, heartbeatCancel := context.WithTimeout(context.WithoutCancel(ctx), heartbeatTimeout)
				heartbeatErr := w.store.HeartbeatDeploymentOperation(heartbeatCtx, operation.ID, w.workerID, now.UTC())
				if heartbeatErr == nil {
					heartbeatErr = held.Check(heartbeatCtx)
				}
				if heartbeatErr == nil {
					heartbeatErr = storageGate.Check(heartbeatCtx)
				}
				heartbeatCancel()
				if heartbeatErr != nil {
					select {
					case ownershipLost <- heartbeatErr:
					default:
					}
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		close(stopHeartbeat)
		heartbeat.Wait()
	}()
	err = w.execute(runCtx, runtime, &operation)
	select {
	case ownershipErr := <-ownershipLost:
		// Docker may have accepted the command before ownership disappeared.
		// Preserve the nonterminal operation and its immutable retry evidence;
		// a former owner has no authority to roll back or remove runtime objects.
		return fmt.Errorf("deployment operation ownership lost: %w", ownershipErr)
	default:
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if runCtx.Err() != nil {
		// The CLI can return a killed-process error instead of a context error
		// after Docker accepted work. A child execution deadline cannot prove
		// failure or authorize compensation; retain immutable stale-retry intent.
		return runCtx.Err()
	}
	if errors.Is(err, storage.ErrOperationOwnership) {
		return err
	}
	checkCtx, checkCancel := context.WithTimeout(runCtx, heartbeatTimeout)
	ownershipErr := held.Check(checkCtx)
	if ownershipErr == nil {
		ownershipErr = storageGate.Check(checkCtx)
	}
	if ownershipErr == nil {
		if heartbeatErr := w.store.HeartbeatDeploymentOperation(checkCtx, operation.ID, w.workerID, time.Now().UTC()); heartbeatErr != nil {
			ownershipErr = errors.Join(storage.ErrOperationOwnership, heartbeatErr)
		}
	}
	checkCancel()
	if ownershipErr != nil {
		return fmt.Errorf("deployment operation ownership lost: %w", ownershipErr)
	}
	if err != nil {
		return w.handleExecutionFailure(runCtx, runtime, &operation, err)
	}
	operation.Status = model.OperationSucceeded
	return w.finish(runCtx, &operation)
}

type dockerResourceInput struct {
	Kind                   string `json:"kind"`
	Name                   string `json:"name,omitempty"`
	ID                     string `json:"id,omitempty"`
	Content                []byte `json:"content,omitempty"`
	AcknowledgeRestoreRisk bool   `json:"acknowledge_restore_risk,omitempty"`
}

func (w *Worker) runDockerResourceOperation(ctx context.Context, runtime model.Runtime, operation *model.DeploymentOperation) error {
	volumeOperation := operation.Type == "VOLUME_CREATE" || operation.Type == "VOLUME_DELETE"
	if !volumeOperation && (runtime.Type != model.RuntimeSwarm || runtime.ClusterID == "") || len(operation.EncryptedInput) == 0 {
		return w.fail(ctx, operation, "resource_identity_unavailable", dockerresource.ErrIdentity)
	}
	leaseKey := "docker-resource:" + runtime.ClusterID
	if volumeOperation {
		leaseKey = volumemanager.LockKey(runtime)
	}
	held, err := w.locker.TryLock(ctx, leaseKey)
	if err != nil {
		// Keep the durable operation running until its stale heartbeat permits
		// another worker to claim it. A lease miss cannot mean execution failed.
		return err
	}
	defer held.Release(context.Background())
	runCtx, cancel := context.WithTimeout(ctx, w.config.Management.DeployTimeout.Std())
	defer cancel()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(w.config.Management.HeartbeatInterval.Std())
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-runCtx.Done():
				return
			case at := <-ticker.C:
				check, end := context.WithTimeout(runCtx, 5*time.Second)
				if w.store.HeartbeatDeploymentOperation(check, operation.ID, w.workerID, at.UTC()) != nil || held.Check(check) != nil {
					cancel()
				}
				end()
			}
		}
	}()
	defer func() { close(stop); <-done }()
	return w.executeDockerResourceBody(runCtx, runtime, operation)
}

func (w *Worker) executeDockerResourceBody(runCtx context.Context, runtime model.Runtime, operation *model.DeploymentOperation) error {
	ctx := runCtx
	volumeOperation := operation.Type == "VOLUME_CREATE" || operation.Type == "VOLUME_DELETE"
	if !volumeOperation && (runtime.Type != model.RuntimeSwarm || runtime.ClusterID == "") || len(operation.EncryptedInput) == 0 {
		return w.fail(ctx, operation, "resource_identity_unavailable", dockerresource.ErrIdentity)
	}
	plaintext, _, err := w.keyring.Decrypt(operation.EncryptedInput, []byte("deployment-operation:"+operation.ID))
	if err != nil {
		return w.fail(ctx, operation, "resource_input_unavailable", errors.New("encrypted resource input unavailable"))
	}
	defer func() {
		for i := range plaintext {
			plaintext[i] = 0
		}
	}()
	if volumeOperation {
		return w.runVolumeOperation(runCtx, operation, plaintext)
	}
	var input dockerResourceInput
	if err := json.Unmarshal(plaintext, &input); err != nil {
		return w.fail(ctx, operation, "resource_input_invalid", errors.New("resource input invalid"))
	}
	defer func() {
		for i := range input.Content {
			input.Content[i] = 0
		}
	}()
	action := "create"
	if operation.Type == "DOCKER_RESOURCE_DELETE" {
		action = "delete"
	}
	manager := dockerresource.Manager{Store: w.store, Config: w.config, Reports: w.executionReports(), Open: w.openEngine}
	result, err := manager.Do(runCtx, dockerresource.Request{RuntimeID: runtime.ID, Action: action, Kind: input.Kind, ID: input.ID, Name: input.Name,
		Content: input.Content, OperationID: operation.ID, AcknowledgeRestoreRisk: input.AcknowledgeRestoreRisk})
	if runCtx.Err() != nil {
		// Identity/precondition wrappers may hide a transport deadline while
		// retrying an earlier accepted request. Preserve its reconciliation input.
		return runCtx.Err()
	}
	if err != nil {
		err = w.nodeResourceError(ctx, err)
	}
	if err != nil {
		if errors.Is(err, dockerresource.ErrIdentity) || errors.Is(err, dockerresource.ErrUnowned) || errors.Is(err, dockerresource.ErrInUse) || errors.Is(err, storage.ErrConflict) {
			return w.fail(ctx, operation, "resource_precondition_failed", errors.New("Docker resource precondition failed"))
		}
		return err // uncertain Docker outcome: retain encrypted input for stale retry
	}
	if action == "create" {
		err = w.store.RecordDockerResource(runCtx, storage.DockerResource{ClusterID: runtime.ClusterID, Kind: input.Kind, DockerID: result.ID, Name: input.Name,
			CreateOperationID: operation.ID, CreatedAt: time.Now().UTC()})
	} else {
		err = w.store.DeleteDockerResource(runCtx, runtime.ClusterID, input.Kind, input.ID)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil // Docker absence was verified by Manager after an earlier commit.
		}
	}
	if err != nil {
		return err
	} // preserve retry evidence after DB failure
	operation.Status = model.OperationSucceeded
	operation.RedactedLog = "Docker resource operation completed"
	return w.finish(ctx, operation)
}

type operationInput struct {
	ServiceRevisionID  string                   `json:"service_revision_id,omitempty"`
	EntrypointIntentID string                   `json:"entrypoint_intent_id,omitempty"`
	Entrypoint         *model.ServiceEntrypoint `json:"entrypoint,omitempty"`
	Incremental        bool                     `json:"incremental,omitempty"`
	DeferredStatus     model.ApplicationStatus  `json:"deferred_status,omitempty"`
	ServiceName        string                   `json:"service_name"`
	ServiceKey         string                   `json:"service_key"`
	RuntimeObjectID    string                   `json:"runtime_object_id,omitempty"`
	ServiceNames       []string                 `json:"service_names"`
	RouteID            string                   `json:"route_id"`
	Replicas           int                      `json:"replicas"`
}

type material struct {
	incremental bool

	application model.Application
	revision    model.ApplicationRevision
	resolved    string
	environment map[string]string
	services    []applicationx.ServiceSpec
}

func (w *Worker) execute(ctx context.Context, runtime model.Runtime, operation *model.DeploymentOperation) error {
	var input operationInput
	if len(operation.EncryptedInput) > 0 {
		plaintext, _, err := w.keyring.Decrypt(operation.EncryptedInput, []byte("deployment-operation:"+operation.ID))
		if err != nil {
			return fmt.Errorf("decrypt operation input: %w", err)
		}
		if err = json.Unmarshal(plaintext, &input); err != nil {
			return fmt.Errorf("decode operation input: %w", err)
		}
	}
	application, err := w.store.GetApplication(ctx, operation.ApplicationID)
	if err != nil {
		return err
	}
	// A stale or forged ordinary application must not execute against the
	// original Compose namespace of a protected system application.
	if application.System {
		return storage.ErrSystemProtected
	}
	if (operation.Type == "SCALE" || operation.Type == "RESTART" || operation.Type == "REMOVE_RUNTIME") && operation.RevisionID != "" && operation.RevisionID != application.ActiveRevisionID {
		return storage.ErrPrecondition
	}
	// Compose attachment redeploys the whole project. A newer aggregate must
	// therefore be reviewed again rather than rolling back unrelated services.
	if operation.Type == "DOMAIN_ATTACH" && runtime.Type != model.RuntimeSwarm && operation.RevisionID != "" && operation.RevisionID != application.ActiveRevisionID {
		return storage.ErrPrecondition
	}
	if operation.ServiceID != "" {
		service, readErr := w.store.GetService(ctx, operation.ServiceID)
		if readErr != nil {
			return readErr
		}
		if !service.Managed || service.System || service.ApplicationID != application.ID || service.RuntimeID != runtime.ID {
			return storage.ErrPrecondition
		}
		if input.ServiceKey != "" && input.ServiceKey != service.ServiceKey {
			return storage.ErrPrecondition
		}
		if input.RuntimeObjectID != "" && service.RuntimeObjectID != input.RuntimeObjectID {
			return storage.ErrPrecondition
		}
		if operation.Type == "DOMAIN_ATTACH" && input.ServiceRevisionID != "" {
			if err = w.checkDomainAttachIntent(ctx, runtime, application, service, input); err != nil {
				return err
			}
		}
	}
	targets, err := w.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{RuntimeID: runtime.ID})
	if err != nil {
		return err
	}
	for _, target := range targets {
		if target.Namespace == application.ProjectName && target.ApplicationID != "" && target.ApplicationID != application.ID {
			owner, e := w.store.GetApplication(ctx, target.ApplicationID)
			if e != nil {
				return e
			}
			if owner.System {
				return storage.ErrSystemProtected
			}
		}
	}
	if operation.Type == "ADOPT" {
		if _, err := w.loadMaterial(ctx, runtime, application, operation.RevisionID); err != nil {
			return err
		}
		return w.adoptApplication(ctx, runtime, application, operation.RevisionID, input.ServiceNames)
	}
	if operation.Type == "UPGRADE" && input.DeferredStatus != "" {
		revision, readErr := w.store.GetApplicationRevision(ctx, operation.RevisionID)
		if readErr != nil {
			return readErr
		}
		scope := revision.ConfigurationSource.ServiceScope
		if operation.ServiceID == "" || scope == nil || !scope.DeferredUntilStart || len(scope.Names) != 1 || application.ActiveRevisionID != scope.BaseRevisionID {
			return storage.ErrPrecondition
		}
		if err := w.store.DeferServiceRevision(ctx, application.ID, revision.ID, scope.Names[0], input.DeferredStatus, time.Now().UTC()); err != nil {
			return err
		}
		operation.RedactedLog = "saved service replica configuration for the next application start\n"
		return nil
	}
	if err := w.verifyExecutionNode(ctx, runtime); err != nil {
		return err
	}
	if operation.Type == "RESTART" || operation.Type == "SCALE" || operation.Type == "REMOVE_RUNTIME" {
		if application.Status != model.ApplicationRunning && operation.Type != "REMOVE_RUNTIME" {
			return fmt.Errorf("cannot %s service while application is %s", strings.ToLower(operation.Type), application.Status)
		}
		if operation.Type == "SCALE" {
			service, readErr := w.store.GetService(ctx, operation.ServiceID)
			if readErr != nil {
				return readErr
			}
			if service.RemovedAt != nil || input.RuntimeObjectID != "" && service.RuntimeObjectID != input.RuntimeObjectID {
				return storage.ErrPrecondition
			}
		}
		output, runErr := w.runServiceOperation(ctx, runtime, application, operation.Type, input)
		operation.RedactedLog = truncateLog(output)
		if runErr == nil && operation.Type == "REMOVE_RUNTIME" {
			return w.store.MarkServiceRuntimeRemoved(ctx, operation.ServiceID, time.Now().UTC())
		}
		return runErr
	}
	if operation.Type == "STOP" || operation.Type == "DOWN" {
		if runtime.Type != model.RuntimeSwarm && operation.Type == "STOP" && (application.Status == model.ApplicationStopped || application.Status == model.ApplicationDown) {
			operation.RedactedLog = fmt.Sprintf("application is already %s; Docker state was not changed\n", application.Status)
			return nil
		}
		if runtime.Type != model.RuntimeSwarm && operation.Type == "DOWN" && application.Status == model.ApplicationDown {
			operation.RedactedLog = "application is already DOWN; Docker state was not changed\n"
			return nil
		}

		output, runErr := w.stopApplication(ctx, runtime, application, operation.Type)
		operation.RedactedLog = truncateLog(output)
		if runErr != nil {
			return runErr
		}
		status := model.ApplicationStopped
		if operation.Type == "DOWN" {
			status = model.ApplicationDown
		}
		return w.store.SetApplicationStatus(ctx, application.ID, status, "", time.Now().UTC())
	}
	if operation.Type == "START" && application.Status == model.ApplicationRunning && runtime.Type != model.RuntimeSwarm {
		operation.RedactedLog = "application is already RUNNING; Docker state was not changed\n"
		return nil
	}
	revisionID := operation.RevisionID
	if revisionID == "" {
		if operation.Type == "DOMAIN_ATTACH" || operation.Type == "DOMAIN_DETACH" {
			revisionID = application.ActiveRevisionID
		} else {
			revisionID = application.DesiredRevisionID
		}
		if revisionID == "" {
			revisionID = application.ActiveRevisionID
		}
	}
	deployment, err := w.loadMaterial(ctx, runtime, application, revisionID)
	if err != nil {
		return err
	}
	deployment.incremental = input.Incremental
	if input.Incremental && deployment.revision.ConfigurationSource.ServiceScope != nil {
		scope := deployment.revision.ConfigurationSource.ServiceScope
		if !applicationx.ReplicaOverridesMatch(scope.Names, application.ReplicaOverrides, scope.ObservedReplicaOverrides) {
			return &composeAdmissionError{cause: storage.ErrPrecondition}
		}
		if scope.ReplicaOverrides != nil {
			application.ReplicaOverrides = applicationx.MergeScopedReplicaOverrides(scope.Names, application.ReplicaOverrides, scope.ReplicaOverrides)
			deployment.application.ReplicaOverrides = application.ReplicaOverrides
		}
	}
	if input.Incremental && deployment.revision.ConfigurationSource.ServiceScope == nil {
		return &composeAdmissionError{cause: errors.New("missing incremental scope")}
	}
	if deployment.incremental && application.ActiveRevisionID != deployment.revision.ConfigurationSource.ServiceScope.BaseRevisionID {
		return &composeAdmissionError{cause: storage.ErrPrecondition}
	}
	if runtime.Type == model.RuntimeSwarm && (operation.Type == "DOMAIN_ATTACH" || operation.Type == "DOMAIN_DETACH") {
		name := applicationServiceName(runtime.Type, application.ProjectName, input.ServiceName)
		if name == "" {
			return errors.New("domain operation is missing its service")
		}
		deployment.resolved, err = applicationx.SelectServices(deployment.resolved, []string{name})
		if err != nil {
			return err
		}
		filtered := []applicationx.ServiceSpec{}
		for _, spec := range deployment.services {
			if spec.Name == name {
				filtered = append(filtered, spec)
			}
		}
		deployment.services = filtered
	}
	if deployment.incremental && deployment.revision.ConfigurationSource.ServiceScope.Mode == "delete" {
		scope := deployment.revision.ConfigurationSource.ServiceScope
		if operation.ServiceID == "" || len(scope.Names) != 1 {
			return &composeAdmissionError{cause: storage.ErrPrecondition}
		}
		service, readErr := w.store.GetService(ctx, operation.ServiceID)
		if readErr != nil {
			return readErr
		}
		if service.ApplicationID != application.ID || service.Revision != scope.TargetServiceRevision || service.System || !service.Managed || service.RemovedAt != nil || service.RuntimeObjectID != scope.TargetRuntimeObjectID {
			return &composeAdmissionError{cause: storage.ErrPrecondition}
		}
		if runtime.Type == model.RuntimeSwarm {
			if checkErr := w.checkServiceVersions(ctx, runtime, deployment); checkErr != nil {
				return &composeAdmissionError{cause: checkErr}
			}
		}
		output, removeErr := w.runServiceOperation(ctx, runtime, application, "REMOVE_RUNTIME", operationInput{ServiceName: scope.Names[0], ServiceKey: service.ServiceKey, RuntimeObjectID: scope.TargetRuntimeObjectID})
		operation.RedactedLog = redactLog(output, deployment.environment, deployment.revision.SecretKeys)
		if removeErr != nil {
			return removeErr
		}
		// A full activation removes only the omitted target from owned inventory.
		// All other definitions come from the frozen active source and were not
		// redeployed. Routes become unavailable but retain their history.
		return w.activateApplication(ctx, runtime, application, revisionID, deployment.services)
	}
	output, err := w.deploy(ctx, runtime, deployment)
	operation.RedactedLog = redactLog(output, deployment.environment, deployment.revision.SecretKeys)
	if err != nil {
		return err
	}
	if operation.Type == "DOMAIN_ATTACH" {
		if input.RouteID == "" {
			return errors.New("domain attach operation is missing route_id")
		}
		return w.store.SetApplicationRouteStatusByID(ctx, application.ID, input.RouteID, model.RouteActive, "", time.Now().UTC())
	}
	if operation.Type == "DOMAIN_DETACH" {
		return w.store.CompleteApplicationDomainDetach(ctx, application.ID, operation.ServiceID, time.Now().UTC())
	}
	var activationScope *model.ServiceDeploymentScope
	if deployment.incremental {
		activationScope = deployment.revision.ConfigurationSource.ServiceScope
	}
	if err = w.activateApplication(ctx, runtime, application, revisionID, deployment.services, activationScope); err != nil {
		return err
	}
	return nil
}

func (w *Worker) checkDomainAttachIntent(ctx context.Context, runtime model.Runtime, app model.Application, service model.Service, input operationInput) error {
	current, err := w.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
	if err != nil {
		return err
	}
	name := applicationServiceName(runtime.Type, app.ProjectName, service.ServiceKey)
	head := current.ConfigurationSource.ServiceRevisions[name]
	if head == "" {
		head = current.ID
	}
	if head != input.ServiceRevisionID {
		return storage.ErrPrecondition
	}
	if input.EntrypointIntentID == "" {
		return nil
	}
	if input.Entrypoint == nil {
		return storage.ErrPrecondition
	}
	if current.ConfigurationSource.EntrypointIntents != nil {
		intent := current.ConfigurationSource.EntrypointIntents[name]
		if intent.ID != input.EntrypointIntentID || intent.Entry != *input.Entrypoint {
			return storage.ErrPrecondition
		}
		return nil
	}
	if head != current.ID {
		current, err = w.store.GetApplicationRevision(ctx, head)
		if err != nil {
			return err
		}
	}
	if current.ApplicationID != app.ID || current.ConfigurationSource.Entrypoint == nil || *current.ConfigurationSource.Entrypoint != *input.Entrypoint {
		return storage.ErrPrecondition
	}
	return nil
}

func (w *Worker) loadMaterial(ctx context.Context, runtime model.Runtime, application model.Application, revisionID string) (material, error) {
	revision, err := w.store.GetApplicationRevision(ctx, revisionID)
	if err != nil {
		return material{}, err
	}
	resolved, _, err := w.keyring.Decrypt(revision.EncryptedResolved, []byte("application-revision:"+revision.ID+":resolved"))
	if err != nil {
		return material{}, fmt.Errorf("decrypt resolved Compose: %w", err)
	}
	environmentText, _, err := w.keyring.Decrypt(revision.EncryptedEnvironment, []byte("application-revision:"+revision.ID+":environment"))
	if err != nil {
		return material{}, fmt.Errorf("decrypt application environment: %w", err)
	}
	environment, err := applicationx.ParseDotEnv(string(environmentText))
	if err != nil {
		return material{}, fmt.Errorf("decode application environment: %w", err)
	}
	pool, err := w.store.GetPool(ctx, runtime.PoolID)
	if err != nil {
		return material{}, fmt.Errorf("load Gateway Pool for application validation: %w", err)
	}
	// The revision is already fully interpolated. Escape literal dollar signs
	// before reusing Preflight for execution-time policy validation, otherwise a
	// Compose `$${CONTAINER_VAR}` becomes `${CONTAINER_VAR}` in the revision and
	// would be interpolated a second time against an empty Controller scope.
	allowedSecrets, allowedConfigs, err := w.allowedDockerResourceNames(ctx, runtime)
	if err != nil {
		return material{}, err
	}
	preflight, err := applicationx.Preflight(applicationx.PreflightInput{RestrictedHostAccess: revision.ConfigurationSource.RestrictedHostAccess, ComposeYAML: applicationx.EscapeForComposeCLI(string(resolved)), Backend: runtime.Type,
		AllowedSecrets: allowedSecrets, AllowedConfigs: allowedConfigs,
		ProtectedNetworks: []string{pool.BackendNetwork}, DataNodeID: revision.ConfigurationSource.DeploymentPlan.DataNodeID, RuntimeNodeID: applicationx.RuntimeNodeID(runtime.Endpoint)})
	if err != nil {
		return material{}, fmt.Errorf("validate stored resolved Compose: %w", err)
	}
	if err := w.verifyFrozenDockerResources(ctx, runtime, revision.ConfigurationSource.DockerResources, string(resolved)); err != nil {
		return material{}, fmt.Errorf("Docker resource binding changed: %w", err)
	}
	if name := revision.ConfigurationSource.NetworkName; name != "" && revision.ConfigurationSource.ServiceScope == nil {
		if runtime.Type != model.RuntimeSwarm {
			return material{}, errors.New("application network requires Swarm")
		}
		if err := applicationx.ValidateApplicationNetwork(name, pool.BackendNetwork, string(resolved)); err != nil {
			return material{}, err
		}
	}
	return material{application: application, revision: revision, resolved: string(resolved), environment: environment, services: preflight.Services}, nil
}

func (w *Worker) deploy(ctx context.Context, runtime model.Runtime, value material) (string, error) {
	if value.incremental {
		scope := value.revision.ConfigurationSource.ServiceScope
		if scope == nil || len(scope.Names) == 0 || runtime.Type == model.RuntimeDocker && (len(scope.Names) != 1 || scope.RequestedReplicas == nil) {
			return "", &composeAdmissionError{cause: errors.New("invalid service deployment scope")}
		}
		var err error
		value.resolved, err = applicationx.SelectServices(value.resolved, scope.Names)
		if err != nil {
			return "", err
		}
		specs := []applicationx.ServiceSpec{}
		for _, spec := range value.services {
			for _, name := range scope.Names {
				if spec.Name == name {
					specs = append(specs, spec)
					break
				}
			}
		}
		value.services = specs
	}
	if err := w.validateComposePlacement(ctx, runtime, value); err != nil {
		return "", &composeAdmissionError{cause: err}
	}
	if err := w.verifyRetainedStorage(ctx, value.application, value.revision.ConfigurationSource.DeploymentPlan.DataNodeID); err != nil {
		return "", &composeAdmissionError{cause: err}
	}
	if err := w.verifyNewStorage(ctx, runtime, value); err != nil {
		return "", &composeAdmissionError{cause: err}
	}
	directory, composePath, overridePath, commandEnvironment, cleanup, err := w.materialize(ctx, runtime, value)
	if err != nil {
		return "", err
	}
	defer cleanup()
	args := w.dockerArgs(runtime)
	if runtime.Type == model.RuntimeSwarm {
		managerOutput, managerErr := w.runner.Run(ctx, directory, commandEnvironment, append(args, "info", "--format", "{{.Swarm.ControlAvailable}}"))
		if managerErr != nil {
			return managerOutput, fmt.Errorf("verify Swarm manager: %w", managerErr)
		}
		if strings.TrimSpace(managerOutput) != "true" {
			return managerOutput, errors.New("Swarm runtime must connect to a manager")
		}
		if err := w.ensureApplicationNetwork(ctx, runtime, value); err != nil {
			return "", err
		}
		// Capture the manager-committed Service versions before submitting the
		// Stack update. `docker stack deploy` returns after accepting an update,
		// not after its tasks have converged. Without this baseline an old 2/2
		// replica count can be mistaken for success while the candidate task is
		// still being scheduled (and may subsequently be rejected).
		baseline, _, baselineErr := w.swarmStackState(ctx, runtime, value.application.ProjectName)
		if value.incremental {
			if baselineErr != nil {
				return "", &composeAdmissionError{cause: baselineErr}
			}
			for _, name := range value.revision.ConfigurationSource.ServiceScope.Names {
				_, exists := baseline[value.application.ProjectName+"_"+name]
				if exists && value.revision.ConfigurationSource.ServiceScope.Mode == "create" {
					return "", &composeAdmissionError{cause: fmt.Errorf("service %q already exists; refresh and use update", name)}
				}
			}
			if err := w.checkServiceVersions(ctx, runtime, value); err != nil {
				return "", &composeAdmissionError{cause: err}
			}
		}
		if err := w.markNewStorageMutation(ctx, value); err != nil {
			return "", &composeAdmissionError{cause: err}
		}
		args = append(args, "stack", "deploy", "--with-registry-auth", "--compose-file", composePath)
		if overridePath != "" {
			args = append(args, "--compose-file", overridePath)
		}
		args = append(args, value.application.ProjectName)
		output, runErr := w.runner.Run(ctx, directory, commandEnvironment, args)
		if runErr != nil {
			return output, runErr
		}
		convergence, convergeErr := w.waitForSwarm(ctx, runtime, value.application.ProjectName, baseline, value.services...)
		if convergeErr == nil && value.revision.ConfigurationSource.DeploymentPlan.DataNodeID != "" {
			convergeErr = w.captureRetainedStorage(ctx, runtime, value.application)
		}
		return output + convergence, convergeErr
	}
	args = append(args, "compose", "--project-name", value.application.ProjectName, "--file", composePath)
	if overridePath != "" {
		args = append(args, "--file", overridePath)
	}
	args = append(args, "up", "-d", "--wait", "--no-build")
	if value.incremental {
		scope := value.revision.ConfigurationSource.ServiceScope
		args = append(args, "--scale", scope.Names[0]+"="+strconv.Itoa(*scope.RequestedReplicas), scope.Names[0])
		return w.runner.Run(ctx, directory, commandEnvironment, args)
	}
	args = append(args, "--remove-orphans")
	overrides := replicaOverridesForServices(value.application.ReplicaOverrides, value.services)
	for _, name := range sortedReplicaOverrideNames(overrides) {
		args = append(args, "--scale", name+"="+strconv.Itoa(overrides[name]))
	}
	return w.runner.Run(ctx, directory, commandEnvironment, args)
}

func (w *Worker) stopApplication(ctx context.Context, runtime model.Runtime, application model.Application, action string) (string, error) {
	args := w.dockerArgs(runtime)
	if runtime.Type == model.RuntimeSwarm {
		if applicationx.RuntimeNodeID(runtime.Endpoint) != "" {
			runtime.Endpoint = localDockerEndpoint
		}
		return w.stopSwarmApplication(ctx, runtime, application)
	}
	revisionID := application.ActiveRevisionID
	if revisionID == "" {
		revisionID = application.DesiredRevisionID
	}
	value, err := w.loadMaterial(ctx, runtime, application, revisionID)
	if err != nil {
		return "", err
	}
	if err := w.verifyRetainedStorage(ctx, value.application, value.revision.ConfigurationSource.DeploymentPlan.DataNodeID); err != nil {
		return "", &composeAdmissionError{cause: err}
	}
	directory, composePath, overridePath, commandEnvironment, cleanup, err := w.materialize(ctx, runtime, value)
	if err != nil {
		return "", err
	}
	defer cleanup()
	args = append(args, "compose", "--project-name", application.ProjectName, "--file", composePath)
	if overridePath != "" {
		args = append(args, "--file", overridePath)
	}
	if action == "STOP" {
		args = append(args, "stop")
	} else {
		// Intentionally omit --volumes and image removal flags.
		args = append(args, "down", "--remove-orphans")
	}
	return w.runner.Run(ctx, directory, commandEnvironment, args)
}

func (w *Worker) runServiceOperation(ctx context.Context, runtime model.Runtime, application model.Application, action string, input operationInput) (string, error) {
	value, err := w.loadMaterial(ctx, runtime, application, application.ActiveRevisionID)
	if err != nil {
		return "", err
	}
	overrides := map[string]int{}
	if action == "SCALE" {
		name := applicationServiceName(runtime.Type, application.ProjectName, input.ServiceKey)
		if name == "" {
			name = applicationServiceName(runtime.Type, application.ProjectName, input.ServiceName)
		}
		overrides[name] = input.Replicas
	}
	name := applicationServiceName(runtime.Type, application.ProjectName, input.ServiceKey)
	if name == "" {
		name = applicationServiceName(runtime.Type, application.ProjectName, input.ServiceName)
	}
	selected, selectErr := applicationx.SelectServices(value.resolved, []string{name})
	if selectErr != nil {
		return "", selectErr
	}
	checked := value
	checked.resolved = selected
	if err := applicationx.ValidateDataNodePolicy(selected, value.revision.ConfigurationSource.DeploymentPlan.DataNodeID, overrides); err != nil {
		return "", &composeAdmissionError{cause: err}
	}
	if err := w.verifyRetainedStorage(ctx, application, value.revision.ConfigurationSource.DeploymentPlan.DataNodeID); err != nil {
		return "", &composeAdmissionError{cause: err}
	}
	if action != "REMOVE_RUNTIME" && (action != "SCALE" || input.Replicas != 0) {
		if err = w.validateComposePlacement(ctx, runtime, checked, overrides); err != nil {
			return "", &composeAdmissionError{cause: err}
		}
	}
	args := w.dockerArgs(runtime)
	if runtime.Type == model.RuntimeSwarm {
		serviceName := input.ServiceKey
		if serviceName == "" {
			serviceName = application.ProjectName + "_" + input.ServiceName
		}
		if action == "RESTART" {
			return w.runSwarmStorageOperation(ctx, runtime, value, append(args, "service", "update", "--force", serviceName))
		}
		if action == "REMOVE_RUNTIME" {
			identity, inspectErr := w.runner.Run(ctx, ".", nil, append(args, "service", "inspect", "--format", "{{.ID}}", serviceName))
			if inspectErr != nil {
				if strings.Contains(identity, "No such service") {
					return "service is already absent\n", nil
				}
				return identity, inspectErr
			}
			identity = strings.TrimSpace(identity)
			if identity == "" {
				return "", storage.ErrPrecondition
			}
			if input.RuntimeObjectID != "" && input.RuntimeObjectID != identity {
				return "", storage.ErrPrecondition
			}
			return w.runner.Run(ctx, ".", nil, append(args, "service", "rm", identity))
		}
		return w.runSwarmStorageOperation(ctx, runtime, value, append(args, "service", "scale", serviceName+"="+strconv.Itoa(input.Replicas)))
	}
	if err := w.verifyRetainedStorage(ctx, value.application, value.revision.ConfigurationSource.DeploymentPlan.DataNodeID); err != nil {
		return "", &composeAdmissionError{cause: err}
	}
	directory, composePath, overridePath, commandEnvironment, cleanup, err := w.materialize(ctx, runtime, value)
	if err != nil {
		return "", err
	}
	defer cleanup()
	args = append(args, "compose", "--project-name", application.ProjectName, "--file", composePath)
	if overridePath != "" {
		args = append(args, "--file", overridePath)
	}
	if action == "REMOVE_RUNTIME" {
		present, inspectErr := w.runner.Run(ctx, directory, commandEnvironment, append(args, "ps", "--all", "--quiet", input.ServiceName))
		if inspectErr != nil {
			return present, inspectErr
		}
		if strings.TrimSpace(present) == "" {
			return "service containers are already absent\n", nil
		}
		if input.RuntimeObjectID != "" && !strings.Contains(present, input.RuntimeObjectID) {
			return "", storage.ErrPrecondition
		}
		return w.runner.Run(ctx, directory, commandEnvironment, append(args, "rm", "--stop", "--force", input.ServiceName))
	}
	if action == "RESTART" {
		args = append(args, "restart", input.ServiceName)
	} else {
		args = append(args, "up", "-d", "--wait", "--no-build", "--scale", input.ServiceName+"="+strconv.Itoa(input.Replicas), input.ServiceName)
	}
	return w.runner.Run(ctx, directory, commandEnvironment, args)
}

func (w *Worker) materialize(ctx context.Context, runtime model.Runtime, value material) (string, string, string, []string, func(), error) {
	if err := os.MkdirAll(w.config.Management.WorkDir, 0o700); err != nil {
		return "", "", "", nil, func() {}, err
	}
	directory, err := os.MkdirTemp(w.config.Management.WorkDir, "operation-")
	if err != nil {
		return "", "", "", nil, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	networkName := value.revision.ConfigurationSource.NetworkName
	if value.revision.ConfigurationSource.ServiceScope != nil {
		networkName = ""
	}
	executionYAML, err := applicationx.DeploymentNetworks(value.resolved, networkName)
	if err == nil && runtime.Type == model.RuntimeSwarm {
		if value.revision.CatalogAppVersionID != "" {
			executionYAML, err = applicationx.SwarmServicePorts(executionYAML, value.services)
		}
		if err == nil {
			executionYAML, err = applicationx.StackPortNumbers(executionYAML)
		}
	}
	if err != nil {
		cleanup()
		return "", "", "", nil, func() {}, err
	}
	composePath := filepath.Join(directory, "compose.yaml")
	if err = os.WriteFile(composePath, []byte(applicationx.EscapeForComposeCLI(executionYAML)), 0o600); err != nil {
		cleanup()
		return "", "", "", nil, func() {}, err
	}
	override, err := w.networkOverride(ctx, value.application, runtime, value.services)
	if err != nil {
		cleanup()
		return "", "", "", nil, func() {}, err
	}
	overridePath := ""
	if override != "" {
		overridePath = filepath.Join(directory, "nsc-network.override.yaml")
		if err = os.WriteFile(overridePath, []byte(override), 0o600); err != nil {
			cleanup()
			return "", "", "", nil, func() {}, err
		}
	}
	commandEnvironment := []string(nil)
	if value.application.RegistryCredentialRef != "" {
		credential, ok := w.config.Registries[value.application.RegistryCredentialRef]
		if !ok {
			cleanup()
			return "", "", "", nil, func() {}, fmt.Errorf("registry credential reference %q is not configured", value.application.RegistryCredentialRef)
		}
		secret, readErr := os.ReadFile(credential.ConfigFile)
		if readErr != nil {
			cleanup()
			return "", "", "", nil, func() {}, fmt.Errorf("read registry credential: %w", readErr)
		}
		var document map[string]any
		if json.Unmarshal(secret, &document) != nil || document["auths"] == nil {
			cleanup()
			return "", "", "", nil, func() {}, errors.New("registry credential must be a Docker config JSON document with auths")
		}
		configDirectory := filepath.Join(directory, "docker-config")
		if err = os.Mkdir(configDirectory, 0o700); err != nil {
			cleanup()
			return "", "", "", nil, func() {}, err
		}
		if err = os.WriteFile(filepath.Join(configDirectory, "config.json"), secret, 0o600); err != nil {
			cleanup()
			return "", "", "", nil, func() {}, err
		}
		commandEnvironment = []string{"DOCKER_CONFIG=" + configDirectory}
	}
	return directory, composePath, overridePath, commandEnvironment, cleanup, nil
}

func (w *Worker) networkOverride(ctx context.Context, application model.Application, runtime model.Runtime, specs []applicationx.ServiceSpec) (string, error) {
	definitionNames := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		definitionNames[spec.Name] = struct{}{}
	}
	services, err := w.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: application.ID})
	if err != nil {
		return "", err
	}
	networkNames := make(map[string]struct{})
	for _, service := range services {
		// Adoption can precede importing routes. Preserve the existing pool
		// attachment proven by discovery so a later deployment does not remove
		// the application's working ingress connection.
		name := applicationServiceName(runtime.Type, application.ProjectName, service.ServiceKey)
		if _, exists := definitionNames[name]; exists && service.Managed && service.RoutingEligible && service.DNSStatus == model.DNSReady {
			// Once a service has binding history, its desired bindings take
			// precedence over stale discovery. In particular, an explicit last
			// detach must not recreate the network it is trying to remove.
			history, historyErr := w.store.ListRoutesFiltered(ctx, storage.ConsoleFilter{ServiceID: service.ID}, storage.Page{Limit: 1})
			if historyErr != nil {
				return "", historyErr
			}
			if len(history) == 0 {
				networkNames[name] = struct{}{}
			}
		}
		bindings, listErr := w.store.ListServiceDomainBindings(ctx, service.ID)
		if listErr != nil {
			return "", listErr
		}
		for _, binding := range bindings {
			// PENDING/ERROR are desired bindings even though ERROR routes are
			// excluded from snapshots. Keeping them in the override lets an
			// explicit redeploy recover a failed network attachment.
			if binding.Route.Status != model.RouteDisabled {
				name := applicationServiceName(runtime.Type, application.ProjectName, service.ServiceKey)
				if _, exists := definitionNames[name]; !exists {
					break
				}
				networkNames[name] = struct{}{}
				break
			}
		}
	}
	replicaOverrides := map[string]int{}
	if runtime.Type == model.RuntimeSwarm {
		for name, replicas := range replicaOverridesForServices(application.ReplicaOverrides, specs) {
			replicaOverrides[name] = replicas
		}
	}
	allNames := make(map[string]struct{}, len(networkNames)+len(replicaOverrides))
	for name := range networkNames {
		allNames[name] = struct{}{}
	}
	for name := range replicaOverrides {
		allNames[name] = struct{}{}
	}
	if len(allNames) == 0 {
		return "", nil
	}
	names := make([]string, 0, len(allNames))
	for name := range allNames {
		names = append(names, name)
	}
	sort.Strings(names)
	var output strings.Builder
	output.WriteString("services:\n")
	for _, name := range names {
		fmt.Fprintf(&output, "  %s:\n", strconv.Quote(name))
		if _, exists := networkNames[name]; exists {
			if runtime.Type == model.RuntimeDocker {
				qualifiedName := application.ProjectName + "_" + name
				output.WriteString("    networks:\n      nsc_backend:\n        aliases:\n          - ")
				output.WriteString(strconv.Quote(qualifiedName))
				output.WriteByte('\n')
			} else {
				output.WriteString("    networks:\n      nsc_backend: {}\n")
			}
		}
		if replicas, exists := replicaOverrides[name]; exists {
			fmt.Fprintf(&output, "    deploy:\n      replicas: %d\n", replicas)
		}
	}
	if len(networkNames) > 0 {
		pool, poolErr := w.store.GetPool(ctx, runtime.PoolID)
		if poolErr != nil {
			return "", poolErr
		}
		output.WriteString("networks:\n  nsc_backend:\n    external: true\n    name: ")
		output.WriteString(strconv.Quote(pool.BackendNetwork))
		output.WriteByte('\n')
	}
	return output.String(), nil
}

func sortedReplicaOverrideNames(values map[string]int) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func replicaOverridesForServices(overrides map[string]int, specs []applicationx.ServiceSpec) map[string]int {
	allowed := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		allowed[spec.Name] = struct{}{}
	}
	result := make(map[string]int, len(overrides))
	for name, replicas := range overrides {
		if _, exists := allowed[name]; exists {
			result[name] = replicas
		}
	}
	return result
}

func applicationServiceName(runtimeType model.RuntimeType, projectName, serviceKey string) string {
	separator := projectName + "/"
	if runtimeType == model.RuntimeSwarm {
		separator = projectName + "_"
	}
	return strings.TrimPrefix(serviceKey, separator)
}

func (w *Worker) dockerArgs(runtime model.Runtime) []string {
	if applicationx.RuntimeNodeID(runtime.Endpoint) != "" {
		runtime.Endpoint = localDockerEndpoint
	}
	args := []string{w.config.Management.DockerCLI, "--host", runtime.Endpoint}
	if runtime.CredentialRef != "" {
		credential := w.config.Credentials[runtime.CredentialRef]
		args = append(args, "--tlsverify", "--tlscacert", credential.CAFile, "--tlscert", credential.CertFile, "--tlskey", credential.KeyFile)
	}
	return args
}

type swarmServiceState struct {
	name        string
	version     uint64
	replicas    string
	updateState string
}

func (w *Worker) swarmStackState(ctx context.Context, runtime model.Runtime, stack string) (map[string]swarmServiceState, string, error) {
	args := w.dockerArgs(runtime)
	serviceOutput, err := w.runner.Run(ctx, ".", nil, append(args, "stack", "services", stack, "--format", "{{.Name}}|{{.Replicas}}"))
	if err != nil {
		return nil, serviceOutput, err
	}
	states := make(map[string]swarmServiceState)
	// Docker reports an empty stack on stderr with exit status zero. The
	// runner combines streams; only this exact successful response is empty.
	if strings.TrimSpace(serviceOutput) == "Nothing found in stack: "+stack {
		return states, serviceOutput, nil
	}
	names := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(serviceOutput), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return nil, serviceOutput, fmt.Errorf("parse Swarm stack service state %q", line)
		}
		name := strings.TrimSpace(parts[0])
		states[name] = swarmServiceState{name: name, replicas: strings.TrimSpace(parts[1])}
		names = append(names, name)
	}
	if len(names) == 0 {
		return states, serviceOutput, nil
	}
	sort.Strings(names)
	inspectArgs := append(args, "service", "inspect", "--format", "{{.Spec.Name}}|{{.Version.Index}}|{{if .UpdateStatus}}{{.UpdateStatus.State}}{{end}}")
	inspectArgs = append(inspectArgs, names...)
	inspectOutput, err := w.runner.Run(ctx, ".", nil, inspectArgs)
	combined := serviceOutput + inspectOutput
	if err != nil {
		return nil, combined, err
	}
	for _, line := range strings.Split(strings.TrimSpace(inspectOutput), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			return nil, combined, fmt.Errorf("parse Swarm service inspection %q", line)
		}
		name := strings.TrimSpace(parts[0])
		state, exists := states[name]
		if !exists {
			return nil, combined, fmt.Errorf("Swarm inspection returned unexpected service %q", name)
		}
		version, parseErr := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)
		if parseErr != nil {
			return nil, combined, fmt.Errorf("parse Swarm service %q version: %w", name, parseErr)
		}
		state.version = version
		state.updateState = strings.ToLower(strings.TrimSpace(parts[2]))
		states[name] = state
	}
	for _, name := range names {
		if states[name].version == 0 {
			return nil, combined, fmt.Errorf("Swarm inspection omitted service %q", name)
		}
	}
	return states, combined, nil
}

func evaluateSwarmConvergence(baseline, current map[string]swarmServiceState) (changed, ready bool, err error) {
	if len(current) == 0 {
		return len(baseline) > 0, false, nil
	}
	changed = len(baseline) != len(current)
	ready = true
	for name, state := range current {
		previous, existed := baseline[name]
		serviceChanged := !existed || previous.version != state.version
		changed = changed || serviceChanged
		if !swarmReplicasReady(state.replicas) {
			ready = false
		}
		switch state.updateState {
		case "", "completed":
		case "paused", "rollback_paused", "rollback_completed":
			if serviceChanged {
				return changed, false, fmt.Errorf("Swarm service %q update entered %s", name, state.updateState)
			}
			ready = false
		default:
			// updating, rollback_started, and future non-terminal states must
			// never be reported as converged merely because old tasks still
			// satisfy the replica count.
			ready = false
		}
	}
	for name := range baseline {
		if _, exists := current[name]; !exists {
			changed = true
		}
	}
	return changed, ready, nil
}

func swarmReplicasReady(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), "/")
	if len(parts) != 2 {
		return false
	}
	running, runningErr := strconv.Atoi(strings.TrimSpace(parts[0]))
	desired, desiredErr := strconv.Atoi(strings.TrimSpace(parts[1]))
	return runningErr == nil && desiredErr == nil && running == desired
}

func (w *Worker) waitForSwarm(ctx context.Context, runtime model.Runtime, stack string, baseline map[string]swarmServiceState, scope ...applicationx.ServiceSpec) (string, error) {
	filter := func(states map[string]swarmServiceState) map[string]swarmServiceState {
		if len(scope) == 0 {
			return states
		}
		out := map[string]swarmServiceState{}
		for _, spec := range scope {
			name := stack + "_" + spec.Name
			if state, ok := states[name]; ok {
				out[name] = state
			}
		}
		return out
	}
	baseline = filter(baseline)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	unchangedReadyObservations := 0
	var output string
	for {
		current, currentOutput, stateErr := w.swarmStackState(ctx, runtime, stack)
		output = currentOutput
		if stateErr == nil {
			current = filter(current)
			changed, ready, convergenceErr := evaluateSwarmConvergence(baseline, current)
			if len(scope) > 0 && len(current) != len(scope) {
				ready = false
			}
			if convergenceErr != nil {
				return "\n" + output, convergenceErr
			}
			if changed && ready {
				return "\n" + output, nil
			}
			if !changed && ready {
				// A legitimate no-op redeploy may not increment a Service
				// version. Require two observations separated by a poll interval
				// so an asynchronously visible update cannot reuse the old 2/2.
				unchangedReadyObservations++
				if unchangedReadyObservations >= 2 {
					return "\n" + output, nil
				}
			} else {
				unchangedReadyObservations = 0
			}
		}
		select {
		case <-ctx.Done():
			return "\n" + output, fmt.Errorf("wait for Swarm convergence: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (w *Worker) activateApplication(ctx context.Context, runtime model.Runtime, application model.Application, revisionID string, specs []applicationx.ServiceSpec, scopes ...*model.ServiceDeploymentScope) error {
	incremental := len(scopes) > 0 && scopes[0] != nil
	if incremental {
		filtered := []applicationx.ServiceSpec{}
		for _, spec := range specs {
			for _, name := range scopes[0].Names {
				if spec.Name == name {
					filtered = append(filtered, spec)
					break
				}
			}
		}
		specs = filtered
	}
	// Installation creates managed placeholders transactionally. Looking them
	// up by application avoids truncation on runtimes with more than 200
	// discovered services while still preserving any concurrent discovery data.
	existing, err := w.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: application.ID})
	if err != nil {
		return err
	}
	byKey := make(map[string]struct{}, len(existing))
	for _, service := range existing {
		byKey[service.ServiceKey] = struct{}{}
	}
	stableKeys := make([]string, 0, len(specs))
	for _, spec := range specs {
		stableKey := application.ProjectName + "/" + spec.Name
		if runtime.Type == model.RuntimeSwarm {
			stableKey = application.ProjectName + "_" + spec.Name
		}
		qualifiedName := application.ProjectName + "_" + spec.Name
		stableKeys = append(stableKeys, stableKey)
		if _, found := byKey[stableKey]; found {
			// Discovery may win the race with operation activation. Preserve its
			// runtime object, routability and readiness instead of replacing them
			// with an "awaiting discovery" placeholder.
			continue
		}
		now := time.Now().UTC()
		placeholder := model.Service{ID: id.New(id.Service), RuntimeID: runtime.ID, ServiceKey: stableKey, ExternalID: stableKey,
			ApplicationID: application.ID, Managed: true, Name: spec.Name, Namespace: application.ProjectName,
			QualifiedName: qualifiedName, DNSName: qualifiedName, DNSStatus: model.DNSMissing,
			DiscoveredProtocol: "http", DiscoveredPort: spec.TargetPort,
			Readiness: w.config.Controller.Endpoint.Readiness, Status: model.ServiceUnavailable,
			RoutingIssues: []string{"awaiting runtime discovery"}, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if createErr := w.store.UpsertService(ctx, &placeholder); createErr != nil {
			return createErr
		}
	}
	return w.finishApplicationActivation(ctx, runtime.ID, runtime.Type, application, revisionID, stableKeys, incremental)
}

func (w *Worker) adoptApplication(ctx context.Context, runtime model.Runtime, application model.Application, revisionID string, names []string) error {
	stableKeys := make([]string, 0, len(names))
	for _, name := range names {
		if runtime.Type == model.RuntimeSwarm {
			stableKeys = append(stableKeys, application.ProjectName+"_"+name)
		} else {
			stableKeys = append(stableKeys, application.ProjectName+"/"+name)
		}
	}
	services, err := w.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{RuntimeID: runtime.ID})
	if err != nil {
		return err
	}
	available := make(map[string]model.Service, len(services))
	for _, service := range services {
		if service.Status != model.ServiceRemoved {
			available[service.ServiceKey] = service
		}
	}
	for _, key := range stableKeys {
		service, exists := available[key]
		if !exists || (service.Managed && service.ApplicationID != application.ID) {
			return fmt.Errorf("adoption target service %q is missing or already managed", key)
		}
	}
	return w.finishApplicationActivation(ctx, runtime.ID, runtime.Type, application, revisionID, stableKeys)
}

func (w *Worker) finishApplicationActivation(ctx context.Context, runtimeID string, runtimeType model.RuntimeType, application model.Application, revisionID string, stableKeys []string, incremental ...bool) error {
	if len(incremental) > 0 && incremental[0] {
		return w.store.ActivateServiceRevision(ctx, runtimeID, application.ID, revisionID, revisionID, stableKeys, application.ReplicaOverrides, "", time.Now().UTC())
	}
	now := time.Now().UTC()
	activeNames := make(map[string]struct{}, len(stableKeys))
	for _, key := range stableKeys {
		activeNames[applicationServiceName(runtimeType, application.ProjectName, key)] = struct{}{}
	}
	overrides := make(map[string]int)
	for name, replicas := range application.ReplicaOverrides {
		if _, exists := activeNames[name]; exists {
			overrides[name] = replicas
		}
	}
	return w.store.ActivateApplicationRevision(ctx, runtimeID, application.ID, revisionID, revisionID, stableKeys, overrides, "", now)
}

func (w *Worker) handleExecutionFailure(ctx context.Context, runtime model.Runtime, operation *model.DeploymentOperation, deployErr error) error {
	if errors.Is(deployErr, errExecutionNode) {
		return w.fail(ctx, operation, "deployment_host_mismatch", deployErr)
	}
	operation.ErrorCode = "deployment_failed"
	operation.ErrorMessage = safeError(deployErr)
	var admission *composeAdmissionError
	if errors.As(deployErr, &admission) {
		operation.Status = model.OperationFailed
		return w.finish(ctx, operation)
	}
	application, appErr := w.store.GetApplication(ctx, operation.ApplicationID)
	if appErr == nil && (operation.Type == "DOMAIN_ATTACH" || operation.Type == "DOMAIN_DETACH") {
		operation.Status = model.OperationFailed
		// Domain networking is an independently retryable desired state. A
		// failed override must not demote an otherwise healthy active revision.
		if operation.Type == "DOMAIN_ATTACH" {
			var input operationInput
			if plaintext, _, decryptErr := w.keyring.Decrypt(operation.EncryptedInput, []byte("deployment-operation:"+operation.ID)); decryptErr == nil {
				_ = json.Unmarshal(plaintext, &input)
			}
			if input.RouteID != "" {
				if routeErr := w.store.SetApplicationRouteStatusByID(ctx, application.ID, input.RouteID, model.RouteError, operation.ErrorMessage, time.Now().UTC()); routeErr != nil {
					operation.ErrorMessage += "; persist binding failure: " + safeError(routeErr)
				}
			}
		}
		return w.finish(ctx, operation)
	}
	if len(operation.EncryptedInput) > 0 && isDeployOperation(operation.Type) {
		var input operationInput
		raw, _, err := w.keyring.Decrypt(operation.EncryptedInput, []byte("deployment-operation:"+operation.ID))
		if err != nil || json.Unmarshal(raw, &input) != nil || input.Incremental {
			operation.Status = model.OperationFailed
			operation.RedactedLog = truncateLog(operation.RedactedLog + "\nRetained existing services and partial results for explicit recovery.\n")
			if err == nil && input.Incremental && appErr == nil && application.ActiveRevisionID != "" {
				status := model.ApplicationRunning
				if input.DeferredStatus == model.ApplicationStopped || input.DeferredStatus == model.ApplicationDown {
					status = input.DeferredStatus
				}
				if stateErr := w.store.FailServiceRevision(ctx, application.ID, operation.RevisionID, status, operation.ErrorMessage, time.Now().UTC()); stateErr != nil {
					operation.ErrorMessage += "; restore application status: " + safeError(stateErr)
				}
			}
			return w.finish(ctx, operation)
		}
	}
	if appErr == nil && isDeployOperation(operation.Type) {
		if application.ActiveRevisionID != "" && application.ActiveRevisionID != operation.RevisionID {
			previous, loadErr := w.loadMaterial(ctx, runtime, application, application.ActiveRevisionID)
			if loadErr == nil {
				output, rollbackErr := w.deploy(ctx, runtime, previous)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				operation.RedactedLog = truncateLog(operation.RedactedLog + "\nrollback:\n" + redactLog(output, previous.environment, previous.revision.SecretKeys))
				if rollbackErr == nil {
					stableKeys := applicationServiceKeys(runtime.Type, application.ProjectName, previous.services)
					overrides := replicaOverridesForServices(application.ReplicaOverrides, previous.services)
					rollbackErr = w.store.ActivateApplicationRevision(ctx, runtime.ID, application.ID, application.ActiveRevisionID,
						operation.RevisionID, stableKeys, overrides, safeError(deployErr), time.Now().UTC())
					if rollbackErr == nil {
						operation.Status = model.OperationRolledBack
						return w.finish(ctx, operation)
					}
				}
				operation.ErrorMessage += "; rollback: " + safeError(rollbackErr)
			} else {
				operation.ErrorMessage += "; load rollback revision: " + safeError(loadErr)
			}
			operation.Status = model.OperationRollbackFailed
		} else if operation.Type == "INSTALL" && application.ActiveRevisionID == "" {
			// A failed first installation is cleaned up without deleting volumes
			// or images. Cleanup errors are recorded but never hide the root cause.
			output, cleanupErr := w.stopApplication(ctx, runtime, application, "DOWN")
			if ctx.Err() != nil {
				return ctx.Err()
			}
			operation.RedactedLog = truncateLog(operation.RedactedLog + "\ncleanup:\n" + output)
			if cleanupErr == nil {
				cleanupErr = w.cleanupInstallationNetwork(ctx, runtime, application, operation.RevisionID)
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if cleanupErr != nil {
				operation.ErrorMessage += "; cleanup: " + safeError(cleanupErr)
			}
			operation.Status = model.OperationFailed
		} else {
			// START and REDEPLOY reuse an already active immutable revision. A
			// failed command must not make the outage worse by tearing down the
			// last known runtime state or withdrawing its stable DNS Route.
			operation.Status = model.OperationFailed
			if updateErr := w.store.SetApplicationStatus(ctx, application.ID, application.Status, operation.ErrorMessage, time.Now().UTC()); updateErr != nil {
				operation.ErrorMessage += "; persist application failure: " + safeError(updateErr)
			}
			return w.finish(ctx, operation)
		}
		if updateErr := w.store.SetApplicationStatus(ctx, application.ID, model.ApplicationFailed, operation.ErrorMessage, time.Now().UTC()); updateErr != nil {
			operation.ErrorMessage += "; persist application failure: " + safeError(updateErr)
		}
		if routeErr := w.store.SetApplicationRouteStatus(ctx, application.ID, model.RouteError, operation.ErrorMessage, time.Now().UTC()); routeErr != nil {
			operation.ErrorMessage += "; persist route failure: " + safeError(routeErr)
		}
	} else {
		operation.Status = model.OperationFailed
	}
	return w.finish(ctx, operation)
}

func applicationServiceKeys(runtimeType model.RuntimeType, projectName string, specs []applicationx.ServiceSpec) []string {
	keys := make([]string, 0, len(specs))
	for _, spec := range specs {
		if runtimeType == model.RuntimeSwarm {
			keys = append(keys, projectName+"_"+spec.Name)
		} else {
			keys = append(keys, projectName+"/"+spec.Name)
		}
	}
	return keys
}

func (w *Worker) fail(ctx context.Context, operation *model.DeploymentOperation, code string, err error) error {
	operation.Status = model.OperationFailed
	operation.ErrorCode = code
	operation.ErrorMessage = safeError(err)
	return w.finish(ctx, operation)
}

func (w *Worker) finish(ctx context.Context, operation *model.DeploymentOperation) error {
	now := time.Now().UTC()
	if w.metrics != nil && operation.StartedAt != nil {
		w.metrics.DeploymentDuration.WithLabelValues(operation.Type, string(operation.Status)).Observe(now.Sub(*operation.StartedAt).Seconds())
	}
	return w.store.FinishDeploymentOperation(ctx, operation, now)
}

func isDeployOperation(value string) bool {
	switch value {
	case "INSTALL", "UPGRADE", "START", "REDEPLOY":
		return true
	default:
		return false
	}
}

func redactLog(value string, environment map[string]string, secretKeys []string) string {
	for _, key := range secretKeys {
		if secret := environment[key]; secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	// Defense in depth: redact long non-secret values too because Docker can
	// echo an entire environment assignment in an error.
	for _, secret := range environment {
		if len(secret) >= 4 {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	return truncateLog(value)
}

func truncateLog(value string) string {
	const maximum = 64 << 10
	if len(value) <= maximum {
		return value
	}
	return value[:maximum] + "\n[truncated]"
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	return truncateLog(err.Error())
}
