package maintenance

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type Provider struct {
	Repository Repository
	Engine     Engine
	Directory  string
	Runtime    func(*bootstrap.Record) Runtime
}

func (p *Provider) View(ctx context.Context) (bootstrap.Installation, error) {
	value, _, err := p.Repository.Installation(ctx)
	if err != nil {
		return value, err
	}
	value.AllowedOperations = []bootstrap.Action{}
	value.Drift = []string{}
	if value.OperationID != "" {
		job, err := p.Operation(ctx, value.OperationID)
		if err != nil {
			return value, err
		}
		value.State = job.State
	}
	actual, err := p.Runtime(record(value)).Observe(ctx)
	value.Health = "UNVERIFIED"
	if err != nil {
		value.Drift = append(value.Drift, "observation_unavailable")
		return value, nil
	}
	value.Topology = actual.Topology
	if actual.Healthy {
		value.Health = "VERIFIED"
	}
	for key, expected := range value.Components {
		actual := actual.Components[key]
		if actual.ID != expected.ID || actual.SpecDigest != expected.SpecDigest {
			value.Drift = append(value.Drift, "external_drift:"+key)
		}
	}
	if err = p.prepared(ctx, value); err != nil {
		value.Drift = append(value.Drift, "cli_preparation_required")
		return value, nil
	}
	if value.OperationID == "" && len(value.Drift) == 0 {
		value.AllowedOperations = []bootstrap.Action{bootstrap.Verify, bootstrap.Repair}
		if value.Release != nil && actual.Healthy {
			value.AllowedOperations = append(value.AllowedOperations, bootstrap.Upgrade)
		}
		if len(value.History) > 0 {
			value.AllowedOperations = append(value.AllowedOperations, bootstrap.Rollback)
		}
	}
	return value, nil
}
func (p *Provider) prepared(ctx context.Context, value bootstrap.Installation) error {
	if !filepath.IsAbs(p.Directory) || filepath.Clean(p.Directory) != p.Directory || p.Directory == "/" || value.MetadataLayout != 1 || value.Executor.ID == "" || value.Control.ID == "" || value.CredentialSecret.ID == "" || value.MaintenanceNodeID == "" || value.MaintenanceID == "" || value.MaintenanceExpiresAt == nil || !time.Now().Before(*value.MaintenanceExpiresAt) {
		return &bootstrap.MaintenanceError{Code: "cli_preparation_required"}
	}
	var secret struct {
		ID   string
		Spec struct{ Labels map[string]string }
	}
	if err := p.Engine.Engine(ctx, "GET", "/secrets/"+url.PathEscape(value.CredentialSecret.ID), nil, &secret); err != nil {
		return err
	}
	if secret.ID != value.CredentialSecret.ID || secret.Spec.Labels["io.nsc.installation"] != value.ID {
		return &bootstrap.MaintenanceError{Code: "credential_unconfirmed"}
	}
	return nil
}
func (p *Provider) Plan(ctx context.Context, input bootstrap.PlanRequest) (bootstrap.Plan, error) {
	value, _, err := p.Repository.Installation(ctx)
	if err != nil {
		return bootstrap.Plan{}, err
	}
	if value.OperationID != "" {
		return bootstrap.Plan{}, &bootstrap.MaintenanceError{Code: "operation_active"}
	}
	if err = p.prepared(ctx, value); err != nil {
		return bootstrap.Plan{}, err
	}
	r := record(value)
	driver := Driver{Record: r, Runtime: p.Runtime(r)}
	plan, err := driver.Plan(ctx, input)
	if err != nil {
		return plan, err
	}
	err = p.Repository.Store.WriteControlDocuments(storage.WithSystemBootstrap(ctx), []storage.ControlWrite{write("installation-plan:"+plan.Hash, "system-installation-plan", value.ID, plan, 0)})
	return plan, err
}
func (p *Provider) Start(ctx context.Context, input bootstrap.ExecuteRequest) (bootstrap.MaintenanceJob, error) {
	if !bootstrap.ValidIdempotencyKey(input.IdempotencyKey) {
		return bootstrap.MaintenanceJob{}, &bootstrap.MaintenanceError{Code: "idempotency_required"}
	}
	value, document, err := p.Repository.Installation(ctx)
	if err != nil {
		return bootstrap.MaintenanceJob{}, err
	}
	if input.OperationID != "" {
		existing, _, err := p.Repository.Job(ctx, input.OperationID)
		if err != nil {
			return existing, err
		}
		if existing.InstallationID != value.ID || existing.Plan.Hash != input.PlanHash || existing.Plan.Revision != input.Revision {
			return existing, &bootstrap.MaintenanceError{Code: "idempotency_conflict"}
		}
		return p.resumeRunner(ctx, existing, value)
	}
	keyHash := bootstrap.Digest([]string{value.ID, input.IdempotencyKey})
	jobID := "maint-" + keyHash[:32]
	if existing, _, err := p.Repository.Job(ctx, jobID); err == nil {
		if existing.Plan.Hash != input.PlanHash || existing.Plan.Revision != input.Revision {
			return existing, &bootstrap.MaintenanceError{Code: "idempotency_conflict"}
		}
		if existing.State == bootstrap.Succeeded || existing.State == bootstrap.Failed || existing.State == bootstrap.Blocked {
			return existing, nil
		}
		return p.resumeRunner(ctx, existing, value)
	} else if !errors.Is(err, storage.ErrNotFound) {
		return existing, err
	}
	if value.OperationID != "" {
		return bootstrap.MaintenanceJob{}, &bootstrap.MaintenanceError{Code: "operation_active"}
	}
	if err = p.prepared(ctx, value); err != nil {
		return bootstrap.MaintenanceJob{}, err
	}
	plan, err := p.Repository.Plan(ctx, input.PlanHash)
	if err != nil {
		return bootstrap.MaintenanceJob{}, err
	}
	r := record(value)
	driver := Driver{Record: r, Runtime: p.Runtime(r)}
	observation, err := driver.Observe(ctx)
	if err != nil {
		return bootstrap.MaintenanceJob{}, err
	}
	if input.Revision != value.Revision || plan.Validate(*r, input.PlanHash, observation, time.Now()) != nil {
		return bootstrap.MaintenanceJob{}, &bootstrap.MaintenanceError{Code: "stale_plan"}
	}
	job := bootstrap.MaintenanceJob{ExecutionMode: "service", ID: jobID, InstallationID: value.ID, State: bootstrap.Pending, Plan: plan, Actor: input.Actor, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Key: keyHash}
	value.OperationID = job.ID
	value.State = bootstrap.Pending
	err = p.Repository.Store.WriteControlDocuments(storage.WithSystemBootstrap(ctx), []storage.ControlWrite{write(bootstrap.InstallationDocumentID, bootstrap.InstallationDocumentKind, value.ID, value, document.Revision), jobWrite(job, 0)})
	if err != nil {
		return job, err
	}
	job.Revision = 1
	return p.launch(ctx, job, value)
}
func (p *Provider) launch(ctx context.Context, job bootstrap.MaintenanceJob, value bootstrap.Installation) (bootstrap.MaintenanceJob, error) {
	var actual swarm.Service
	if err := p.Engine.Engine(ctx, "GET", "/services/"+url.PathEscape(value.Executor.ID), nil, &actual); err != nil {
		return job, failBeforeExecution(ctx, p.Repository, job, "executor_unavailable")
	}
	if actual.ID != value.Executor.ID || bootstrap.Digest(actual.Spec) != value.Executor.SpecDigest || actual.Version.Index < value.Executor.Version || actual.Spec.Mode.ReplicatedJob == nil || actual.Spec.TaskTemplate.ContainerSpec == nil {
		return job, failBeforeExecution(ctx, p.Repository, job, "executor_drift")
	}
	spec := actual.Spec
	container := *spec.TaskTemplate.ContainerSpec
	if value.Release == nil {
		return job, failBeforeExecution(ctx, p.Repository, job, "baseline_missing")
	}
	// Last verified image remains the rescue executor even after a failed rollout.
	container.Image = value.Release.Images.Controller
	container.Args = []string{"bootstrap-worker", "--state-dir", p.Directory, "--job", job.ID, "--service-id", actual.ID}
	container.Env = []string{"NSC_MAINTENANCE_TASK_ID={{.Task.ID}}"}
	spec.TaskTemplate.ContainerSpec = &container
	spec.TaskTemplate.ForceUpdate++
	spec.TaskTemplate.Placement = &swarm.Placement{Constraints: []string{"node.id == " + value.MaintenanceNodeID}}
	job.Runner = bootstrap.Resource{Kind: "service", ID: actual.ID, Version: actual.Version.Index, SpecDigest: bootstrap.Digest(spec), Image: container.Image}
	job.Acknowledged = false
	job.State = bootstrap.Pending
	if err := p.Repository.SaveJob(ctx, &job); err != nil {
		return job, err
	}
	path := fmt.Sprintf("/services/%s/update?version=%d&registryAuthFrom=spec", actual.ID, actual.Version.Index)
	if err := p.Engine.Engine(ctx, "POST", path, spec, nil); err != nil {
		job.State = bootstrap.Unknown
		job.Reason = "executor_update_unconfirmed"
		return job, p.saveError(ctx, &job, err)
	}
	return p.acknowledge(ctx, job, value)
}
func (p *Provider) saveError(ctx context.Context, job *bootstrap.MaintenanceJob, cause error) error {
	if err := p.Repository.SaveJob(ctx, job); err != nil {
		return err
	}
	return cause
}
func (p *Provider) acknowledge(ctx context.Context, job bootstrap.MaintenanceJob, value bootstrap.Installation) (bootstrap.MaintenanceJob, error) {
	var actual swarm.Service
	if err := p.Engine.Engine(ctx, "GET", "/services/"+url.PathEscape(job.Runner.ID), nil, &actual); err != nil {
		return job, err
	}
	if actual.ID != job.Runner.ID || bootstrap.Digest(actual.Spec) != job.Runner.SpecDigest || actual.Version.Index <= job.Runner.Version || actual.JobStatus == nil {
		return job, &bootstrap.MaintenanceError{Code: "executor_update_unconfirmed"}
	}
	current, document, err := p.Repository.Installation(ctx)
	if err != nil {
		return job, err
	}
	if current.ID != value.ID || current.OperationID != job.ID {
		return job, &bootstrap.MaintenanceError{Code: "executor_claim_changed"}
	}
	saved, jobDocument, err := p.Repository.Job(ctx, job.ID)
	if err != nil {
		return job, err
	}
	if saved.Plan.Hash != job.Plan.Hash {
		return job, storage.ErrConflict
	}
	if saved.Acknowledged {
		return saved, nil
	}
	if saved.Revision != job.Revision {
		return job, storage.ErrConflict
	}
	job.Runner = Receipt(actual)
	job.Acknowledged = true
	job.Reason = ""
	current.Executor = job.Runner
	err = p.Repository.Store.WriteControlDocuments(storage.WithSystemBootstrap(ctx), []storage.ControlWrite{write(bootstrap.InstallationDocumentID, bootstrap.InstallationDocumentKind, current.ID, current, document.Revision), jobWrite(job, jobDocument.Revision)})
	if err == nil {
		job.Revision = jobDocument.Revision + 1
	}
	return job, err
}
func (p *Provider) Operation(ctx context.Context, id string) (bootstrap.MaintenanceJob, error) {
	job, _, err := p.Repository.Job(ctx, id)
	if err != nil {
		return job, err
	}
	value, _, err := p.Repository.Installation(ctx)
	if err != nil {
		return job, err
	}
	if job.InstallationID != value.ID {
		return job, storage.ErrNotFound
	}
	if job.State == bootstrap.Succeeded || job.State == bootstrap.Failed || job.State == bootstrap.Blocked {
		return job, nil
	}
	if !job.Acknowledged && job.Runner.ID != "" {
		updated, err := p.acknowledge(ctx, job, value)
		if err == nil {
			job = updated
		} else if !isUnconfirmed(err) {
			return job, err
		}
	}
	if !job.Acknowledged && time.Since(job.UpdatedAt) > 20*time.Second {
		job.State = bootstrap.Unknown
		job.Reason = "executor_acknowledgement_unconfirmed"
		if err = p.Repository.SaveJob(ctx, &job); err != nil {
			return job, err
		}
	}
	if job.Acknowledged && time.Since(job.UpdatedAt) > 20*time.Second {
		stopped, err := p.runnerStopped(ctx, job)
		if err != nil {
			return job, err
		}
		if stopped {
			job.State = bootstrap.Unknown
			job.Reason = "executor_stopped_reconcile_required"
			if err = p.Repository.SaveJob(ctx, &job); err != nil {
				return job, err
			}
		}
	}
	return job, nil
}
func isUnconfirmed(err error) bool {
	var value *bootstrap.MaintenanceError
	return errors.As(err, &value) && value.Code == "executor_update_unconfirmed"
}
func (p *Provider) runnerStopped(ctx context.Context, job bootstrap.MaintenanceJob) (bool, error) {
	var runner swarm.Service
	if err := p.Engine.Engine(ctx, "GET", "/services/"+url.PathEscape(job.Runner.ID), nil, &runner); err != nil {
		return false, err
	}
	if runner.ID != job.Runner.ID || bootstrap.Digest(runner.Spec) != job.Runner.SpecDigest || runner.JobStatus == nil {
		return false, &bootstrap.MaintenanceError{Code: "executor_update_unconfirmed"}
	}
	filters := url.QueryEscape(fmt.Sprintf(`{"service":[%q]}`, job.Runner.ID))
	var tasks []swarm.Task
	if err := p.Engine.Engine(ctx, "GET", "/tasks?filters="+filters, nil, &tasks); err != nil {
		return false, err
	}
	found := false
	for _, task := range tasks {
		if task.ServiceID != runner.ID || task.JobIteration == nil || *task.JobIteration != runner.JobStatus.JobIteration || task.Spec.ContainerSpec == nil || !strings.Contains(strings.Join(task.Spec.ContainerSpec.Args, " "), job.ID) {
			continue
		}
		found = true
		switch task.Status.State {
		case swarm.TaskStateComplete, swarm.TaskStateFailed, swarm.TaskStateShutdown, swarm.TaskStateRejected:
		default:
			return false, nil
		}
	}
	return found, nil
}
func (p *Provider) resumeRunner(ctx context.Context, job bootstrap.MaintenanceJob, value bootstrap.Installation) (bootstrap.MaintenanceJob, error) {
	if job.ExecutionMode == "cli" || strings.HasPrefix(job.ID, "cli-") {
		return job, &bootstrap.MaintenanceError{Code: "cli_resume_required"}
	}
	observed, err := p.Operation(ctx, job.ID)
	if err != nil {
		return observed, err
	}
	job = observed
	if job.State != bootstrap.Unknown {
		return job, nil
	}
	if !job.Acknowledged && job.Runner.ID == "" {
		// A runner cannot be updated until its intent is durably stored. An
		// absent intent proves launch has not reached Docker; retain the same
		// claim and verify the original executor receipt in launch.
		return p.launch(ctx, job, value)
	}
	if !job.Acknowledged {
		var actual swarm.Service
		if err = p.Engine.Engine(ctx, "GET", "/services/"+job.Runner.ID, nil, &actual); err != nil {
			return job, err
		}
		if actual.ID == value.Executor.ID && bootstrap.Digest(actual.Spec) == value.Executor.SpecDigest && actual.Version.Index >= value.Executor.Version {
			return p.launch(ctx, job, value)
		}
	}
	stopped, err := p.runnerStopped(ctx, job)
	if err != nil {
		return job, err
	}
	if !stopped {
		return job, &bootstrap.MaintenanceError{Code: "old_executor_not_stopped"}
	}
	if value.OperationID != job.ID {
		return job, &bootstrap.MaintenanceError{Code: "executor_claim_changed"}
	}
	return p.launch(ctx, job, value)
}
