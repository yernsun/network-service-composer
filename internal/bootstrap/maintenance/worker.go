package maintenance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// RunWorker is the independent single-operation process. Service/task receipts,
// the original node, the approved job and the shared OS lock must all agree.
func RunWorker(ctx context.Context, directory, jobID, serviceID, taskID, credentialFile string) error {
	data, err := bootstrap.ReadPrivate(credentialFile)
	if err != nil {
		return fmt.Errorf("maintenance Secret is unavailable")
	}
	defer clear(data)
	var credential bootstrap.MaintenanceCredential
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&credential); err != nil {
		return fmt.Errorf("maintenance Secret is invalid")
	}
	store, err := storage.Open("postgres", credential.DSN)
	if err != nil {
		return fmt.Errorf("maintenance database cannot be opened")
	}
	defer store.Close()
	if err = store.Ping(ctx); err != nil {
		return fmt.Errorf("maintenance database is unavailable")
	}
	repository := Repository{Store: store}
	engine := NewLocalEngine()
	defer engine.Client.CloseIdleConnections()
	return executeWorker(ctx, repository, engine, credential, data, directory, jobID, serviceID, taskID, HealthClient, BindingClient)
}

func executeWorker(ctx context.Context, repository Repository, engine Engine, credential bootstrap.MaintenanceCredential, data []byte, directory, jobID, serviceID, taskID string, healthFactory, bindingFactory func(bootstrap.Installation, bootstrap.MaintenanceCredential) (func(context.Context) error, error)) error {
	var job bootstrap.MaintenanceJob
	var err error
	for {
		job, _, err = repository.Job(ctx, jobID)
		if err != nil {
			return err
		}
		if job.Acknowledged {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("executor acknowledgement is unconfirmed")
		case <-time.After(time.Second):
		}
	}
	installation, _, err := repository.Installation(ctx)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if job.InstallationID != installation.ID || installation.OperationID != job.ID || serviceID != installation.Executor.ID || serviceID != job.Runner.ID || hex.EncodeToString(sum[:]) != installation.CredentialSecret.SpecDigest {
		return &bootstrap.MaintenanceError{Code: "executor_claim_changed"}
	}
	if taskID == "" {
		return &bootstrap.MaintenanceError{Code: "executor_identity_unconfirmed"}
	}
	var task swarm.Task
	if err = engine.Engine(ctx, "GET", "/tasks/"+taskID, nil, &task); err != nil {
		return err
	}
	if task.ID != taskID || task.ServiceID != serviceID || task.NodeID != installation.MaintenanceNodeID {
		return &bootstrap.MaintenanceError{Code: "executor_identity_unconfirmed"}
	}
	var service swarm.Service
	if err = engine.Engine(ctx, "GET", "/services/"+serviceID, nil, &service); err != nil {
		return err
	}
	if bootstrap.Digest(service.Spec) != job.Runner.SpecDigest || service.JobStatus == nil || task.JobIteration == nil || *task.JobIteration != service.JobStatus.JobIteration {
		return &bootstrap.MaintenanceError{Code: "executor_identity_unconfirmed"}
	}
	journal, err := bootstrap.OpenJournal(directory)
	if err != nil {
		return uncertainExecutor(ctx, repository, job, "executor_busy")
	}
	defer journal.Close()
	record, err := journal.Read()
	if err != nil {
		return err
	}
	if err = bootstrap.ValidateMetadataContents(directory, record); err != nil {
		return failBeforeExecution(ctx, repository, job, "metadata_contains_private_material")
	}
	machine, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return err
	}
	identity := bootstrap.Digest(strings.TrimSpace(string(machine)))
	if record.MetadataLayout != 1 || record.ID != installation.ID || record.MaintenanceIdentity != identity || record.MaintenanceAnchor != bootstrap.Digest([]string{identity, directory}) || record.MaintenanceAnchor != installation.MaintenanceAnchor || bootstrap.Digest(record.Nodes) != bootstrap.Digest(installation.Nodes) {
		return failBeforeExecution(ctx, repository, job, "maintenance_node_mismatch")
	}
	var info struct {
		ID    string
		Swarm struct {
			NodeID           string
			ControlAvailable bool
			Cluster          *struct{ ID string }
		}
	}
	if err = engine.Engine(ctx, "GET", "/info", nil, &info); err != nil {
		return err
	}
	match := false
	for _, node := range record.Nodes {
		if node.NodeID == installation.MaintenanceNodeID && node.EngineID == info.ID {
			match = true
		}
	}
	if !match || info.Swarm.NodeID != installation.MaintenanceNodeID || !info.Swarm.ControlAvailable || info.Swarm.Cluster == nil || info.Swarm.Cluster.ID != record.ClusterID {
		return failBeforeExecution(ctx, repository, job, "maintenance_node_mismatch")
	}
	health, err := healthFactory(installation, credential)
	if err != nil {
		return failBeforeExecution(ctx, repository, job, "credential_unconfirmed")
	}

	physical, err := bindingFactory(installation, credential)
	if err != nil {
		return failBeforeExecution(ctx, repository, job, "credential_unconfirmed")
	}
	runtime := &DockerRuntime{RegistryAuth: credential.RegistryAuth, Engine: engine, Record: &record, Schema: SchemaVerifier(repository.Store), Physical: physical, Health: health}
	record.Resources["maintenance-executor"] = job.Runner
	job.TaskID = taskID
	checkpoint := &Checkpointer{Journal: journal, Repository: repository, Engine: engine, Job: &job}
	// The executor service was already changed by the approved launch. Retain
	// its verified receipt even when the component plan has expired or blocks
	// before Executor.Execute creates an operation in the private journal.
	if err = checkpoint.Save(&record); err != nil {
		return err
	}
	driver := &Driver{Record: &record, Runtime: runtime, Journal: checkpoint, Target: job.Plan.Scope.Release, Components: job.Plan.Scope.Components}
	executor := bootstrap.Executor{Journal: checkpoint, Driver: driver}
	resumed := false
	for _, operation := range record.Operations {
		if operation.Key != job.Key {
			continue
		}
		if operation.Plan.Hash != job.Plan.Hash {
			return failBeforeExecution(ctx, repository, job, "idempotency_conflict")
		}
		resumed = true
		_, err = executor.Resume(ctx, &record, operation.ID)
		break
	}
	if !resumed {
		_, err = executor.Execute(ctx, &record, job.Plan, job.Plan.Hash, job.Key)
	}
	if err != nil && job.Operation == nil {
		return failBeforeExecution(ctx, repository, job, "plan_or_executor_blocked")
	}
	// A completed resume does not pass through Executor.Save again.
	if err == nil && job.State != bootstrap.Succeeded {
		if saveErr := checkpoint.Save(&record); saveErr != nil {
			return saveErr
		}
	}
	return err
}
func failBeforeExecution(ctx context.Context, repository Repository, job bootstrap.MaintenanceJob, reason string) error {
	value, document, err := repository.Installation(ctx)
	if err != nil {
		return err
	}
	if value.OperationID != job.ID {
		return &bootstrap.MaintenanceError{Code: "executor_claim_changed"}
	}
	_, jobDocument, err := repository.Job(ctx, job.ID)
	if err != nil {
		return err
	}
	job.State = bootstrap.Blocked
	job.Reason = reason
	value.OperationID = ""
	value.State = bootstrap.Blocked
	err = repository.Store.WriteControlDocuments(storage.WithSystemBootstrap(ctx), []storage.ControlWrite{write(bootstrap.InstallationDocumentID, bootstrap.InstallationDocumentKind, value.ID, value, document.Revision), jobWrite(job, jobDocument.Revision)})
	if err != nil {
		return err
	}
	return &bootstrap.MaintenanceError{Code: reason}
}

// An unavailable node-local lock is not proof that an older writer stopped.
// Retain its claim and never overwrite a newer terminal receipt.
func uncertainExecutor(ctx context.Context, repository Repository, job bootstrap.MaintenanceJob, reason string) error {
	value, _, err := repository.Installation(ctx)
	if err != nil {
		return err
	}
	if value.OperationID != job.ID {
		return &bootstrap.MaintenanceError{Code: "executor_claim_changed"}
	}
	job.State, job.Reason = bootstrap.Unknown, reason
	if err = repository.SaveJob(ctx, &job); err != nil {
		return err
	}
	return &bootstrap.MaintenanceError{Code: reason}
}
