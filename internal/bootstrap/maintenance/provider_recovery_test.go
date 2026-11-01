package maintenance

import (
	"context"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

func TestProviderDoesNotReuseAnIdempotencyKeyForAnotherApproval(t *testing.T) {
	for _, change := range []string{"hash", "revision"} {
		t.Run(change, func(t *testing.T) {
			p, _, engine, _ := providerFixture(t)
			plan, _ := launchRepair(t, p)
			input := bootstrap.ExecuteRequest{PlanHash: plan.Hash, Revision: plan.Revision, IdempotencyKey: "provider-boundary-key"}
			if change == "hash" {
				input.PlanHash = "other-plan"
			} else {
				input.Revision++
			}
			before := len(engine.calls)
			if _, err := p.Start(t.Context(), input); err == nil {
				t.Fatal("an idempotency key changed its approval")
			}
			assertNoInfrastructureWrite(t, engine.calls[before:])
		})
	}
}

func TestDurableRunnerIntentCanRetryOnlyWhenTheOriginalCASWasNotApplied(t *testing.T) {
	p, _, engine, _ := providerFixture(t)
	plan, err := p.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	engine.fail = "POST /services/executor/update"
	job, err := p.Start(t.Context(), bootstrap.ExecuteRequest{PlanHash: plan.Hash, Revision: 2, IdempotencyKey: "unapplied-executor"})
	if err == nil || job.Acknowledged || job.Runner.ID == "" {
		t.Fatal("unapplied runner did not retain its durable intent", err)
	}
	engine.fail = ""
	resumed, err := p.Start(t.Context(), bootstrap.ExecuteRequest{OperationID: job.ID, PlanHash: plan.Hash, Revision: 2, IdempotencyKey: "resume-unapplied-executor"})
	if err != nil || !resumed.Acknowledged || resumed.ID != job.ID {
		t.Fatal("the unchanged original executor could not continue", err)
	}
}

func TestProgressQueriesPreserveTerminalOutcomesAndDoNotFenceALiveRunner(t *testing.T) {
	for _, state := range []bootstrap.State{bootstrap.Succeeded, bootstrap.Failed, bootstrap.Blocked, bootstrap.Running} {
		t.Run(string(state), func(t *testing.T) {
			p, _, engine, repository := providerFixture(t)
			_, job := launchRepair(t, p)
			job.State = state
			job.UpdatedAt = time.Now().Add(-time.Minute)
			storeJobFixture(t, repository, job)
			if state == bootstrap.Running {
				service := engine.services[job.Runner.ID]
				iteration := service.JobStatus.JobIteration
				engine.tasks = []swarm.Task{{ID: "active-owned-task", ServiceID: service.ID, NodeID: "node-one", Spec: service.Spec.TaskTemplate, JobIteration: &iteration, Status: swarm.TaskStatus{State: swarm.TaskStateRunning, ContainerStatus: &swarm.ContainerStatus{ContainerID: "active-owned-container"}}}}
			}
			before := len(engine.calls)
			value, err := p.Operation(t.Context(), job.ID)
			if err != nil || value.State != state {
				t.Fatal("a progress query changed a confirmed outcome or fenced a live runner", value.State, err)
			}
			assertNoInfrastructureWrite(t, engine.calls[before:])
		})
	}
}

func TestPreparationCannotBorrowAMaintenanceSecretFromAnotherInstallation(t *testing.T) {
	for _, scenario := range []string{"identifier", "installation-label"} {
		t.Run(scenario, func(t *testing.T) {
			p, _, engine, repository := providerFixture(t)
			owner, _, err := repository.Installation(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			p.Engine = engineFunc(func(ctx context.Context, method, path string, input, output any) error {
				if method == "GET" && path == "/secrets/"+owner.CredentialSecret.ID {
					identifier, label := owner.CredentialSecret.ID, owner.ID
					if scenario == "identifier" {
						identifier = "foreign-secret"
					} else {
						label = "foreign-installation"
					}
					return encodeFixture(map[string]any{"ID": identifier, "Spec": map[string]any{"Labels": map[string]string{"io.nsc.installation": label}}}, output)
				}
				return engine.Engine(ctx, method, path, input, output)
			})
			if _, err = p.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: 2}); err == nil {
				t.Fatal("foreign maintenance authority was accepted")
			}
			assertNoInfrastructureWrite(t, engine.calls)
		})
	}
}

func TestCLIJobIdentifierCannotBeReclassifiedAsAWebExecutor(t *testing.T) {
	driver, engine, _ := maintenanceFixture(t)
	repository := sqlRepository(t, driver, engine)
	plan, err := driver.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	job, err := repository.ClaimCLI(t.Context(), plan, "original-cli-writer", "cli")
	if err != nil {
		t.Fatal(err)
	}
	job.ExecutionMode = "service"
	if err = repository.SaveJob(t.Context(), &job); err != nil {
		t.Fatal(err)
	}
	provider := Provider{Repository: repository, Engine: engine}
	before := len(engine.calls)
	if _, err = provider.Start(t.Context(), bootstrap.ExecuteRequest{OperationID: job.ID, PlanHash: plan.Hash, Revision: 2, IdempotencyKey: "web-reclassification"}); err == nil {
		t.Fatal("an independent CLI writer became a Web executor")
	}
	assertNoInfrastructureWrite(t, engine.calls[before:])
}
