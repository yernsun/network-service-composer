package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type repositoryFault struct {
	storage.Store
	get        func(string) (storage.ControlDocument, error, bool)
	writeError error
	auditError error
}

func (s repositoryFault) AppendAuditEvent(ctx context.Context, event model.AuditEvent) error {
	if s.auditError != nil {
		return s.auditError
	}
	return s.Store.AppendAuditEvent(ctx, event)
}
func (s repositoryFault) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if s.get != nil {
		if value, err, handled := s.get(key); handled {
			return value, err
		}
	}
	return s.Store.GetControlDocument(ctx, key)
}
func (s repositoryFault) WriteControlDocuments(ctx context.Context, writes []storage.ControlWrite) error {
	if s.writeError != nil {
		return s.writeError
	}
	return s.Store.WriteControlDocuments(ctx, writes)
}
func TestInstallationDocumentsCannotSubstituteNamesKindsOrParents(t *testing.T) {
	for _, kind := range []string{"installation", "plan", "job"} {
		for _, scenario := range []string{"missing", "json", "kind", "parent", "id"} {
			t.Run(kind+"/"+scenario, func(t *testing.T) {
				driver, engine, _ := maintenanceFixture(t)
				repository := sqlRepository(t, driver, engine)
				id, documentKind, parent := "system:installation", bootstrap.InstallationDocumentKind, driver.Record.ID
				var payload any
				if kind == "installation" {
					value, _, err := repository.Installation(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					if scenario == "id" {
						value.ID = "other"
					}
					payload = value
				}
				if kind == "plan" {
					id, documentKind = "installation-plan:hash", "system-installation-plan"
					payload = bootstrap.Plan{Hash: "hash", InstallationID: parent}
					if scenario == "id" {
						payload = bootstrap.Plan{Hash: "other", InstallationID: parent}
					}
				}
				if kind == "job" {
					id, documentKind = "installation-job:job", "system-installation-job"
					payload = storedJob{MaintenanceJob: bootstrap.MaintenanceJob{ID: "job", InstallationID: parent}}
					if scenario == "id" {
						payload = storedJob{MaintenanceJob: bootstrap.MaintenanceJob{ID: "other", InstallationID: parent}}
					}
				}
				raw, _ := json.Marshal(payload)
				document := storage.ControlDocument{ID: id, Kind: documentKind, ParentID: parent, Payload: raw}
				if scenario == "json" {
					document.Payload = []byte(`{`)
				}
				if scenario == "kind" {
					document.Kind = "foreign"
				}
				if scenario == "parent" {
					document.ParentID = "other"
				}
				repository.Store = repositoryFault{Store: repository.Store, get: func(key string) (storage.ControlDocument, error, bool) {
					if key != id {
						return storage.ControlDocument{}, nil, false
					}
					if scenario == "missing" {
						return document, storage.ErrNotFound, true
					}
					return document, nil, true
				}}
				var err error
				switch kind {
				case "installation":
					_, _, err = repository.Installation(t.Context())
				case "plan":
					_, err = repository.Plan(t.Context(), "hash")
				case "job":
					_, _, err = repository.Job(t.Context(), "job")
				}
				if err == nil {
					t.Fatal("unverified document became ownership evidence")
				}
			})
		}
	}
}
func TestWebPreflightFailureAndConcurrentClaimsHaveExplicitRecovery(t *testing.T) {
	for _, scenario := range []string{"executor-missing", "executor-drift", "baseline", "claim-write", "plan-write", "job-read", "expired-plan"} {
		t.Run(scenario, func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			repository := sqlRepository(t, driver, engine)
			provider := Provider{Repository: repository, Engine: engine, Directory: "/var/lib/nsc-bootstrap", Runtime: func(*bootstrap.Record) Runtime { return driver.Runtime }}
			if scenario == "plan-write" {
				provider.Repository.Store = repositoryFault{Store: repository.Store, writeError: errors.New("database unavailable")}
				if _, err := provider.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: 2}); err == nil {
					t.Fatal("unpersisted plan returned success")
				}
				return
			}
			plan, err := provider.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: 2})
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "executor-missing":
				delete(engine.services, "executor")
			case "executor-drift":
				value := engine.services["executor"]
				value.Spec.Name = "external"
				engine.services[value.ID] = value
			case "baseline":
				value, document, _ := repository.Installation(t.Context())
				value.Release = nil
				repository.Store.WriteControlDocuments(t.Context(), []storage.ControlWrite{write(document.ID, document.Kind, document.ParentID, value, document.Revision)})
			case "claim-write":
				provider.Repository.Store = repositoryFault{Store: repository.Store, writeError: errors.New("database unavailable")}
			case "job-read":
				provider.Repository.Store = repositoryFault{Store: repository.Store, get: func(key string) (storage.ControlDocument, error, bool) {
					if len(key) > 17 && key[:17] == "installation-job:" {
						return storage.ControlDocument{}, errors.New("unavailable"), true
					}
					return storage.ControlDocument{}, nil, false
				}}
			case "expired-plan":
				plan.ExpiresAt = time.Now().Add(-time.Minute)
				document, _ := repository.Store.GetControlDocument(t.Context(), "installation-plan:"+plan.Hash)
				repository.Store.WriteControlDocuments(t.Context(), []storage.ControlWrite{write(document.ID, document.Kind, document.ParentID, plan, document.Revision)})
			}
			job, err := provider.Start(t.Context(), bootstrap.ExecuteRequest{PlanHash: plan.Hash, Revision: 2, IdempotencyKey: "preflight-failure-fixture"})
			if err == nil {
				t.Fatal("incomplete job preflight passed")
			}
			if scenario == "executor-missing" || scenario == "executor-drift" || scenario == "baseline" {
				saved, _, err := repository.Job(t.Context(), job.ID)
				if err != nil || saved.State != bootstrap.Blocked {
					t.Fatal("preflight left an unexplained pending claim", saved.State, err)
				}
				value, _, _ := repository.Installation(t.Context())
				if value.OperationID != "" {
					t.Fatal("definite preflight failure retained its claim")
				}
			}
		})
	}
}

func TestCLIMaintenanceClaimIsExclusiveAndBoundToItsPlan(t *testing.T) {
	driver, engine, _ := maintenanceFixture(t)
	repository := sqlRepository(t, driver, engine)
	plan, err := driver.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Verify, Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.ClaimCLI(t.Context(), plan, "short", "cli"); err == nil {
		t.Fatal("unbounded key accepted")
	}
	stale := plan
	stale.Revision--
	if _, err = repository.ClaimCLI(t.Context(), stale, "stale-request-key", "cli"); err == nil {
		t.Fatal("stale revision claimed installation")
	}
	job, err := repository.ClaimCLI(t.Context(), plan, "original-request-key", "cli")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := repository.ClaimCLI(t.Context(), plan, "original-request-key", "cli")
	if err != nil || duplicate.ID != job.ID {
		t.Fatal("duplicate lost original claim", err)
	}
	different := plan
	different.Hash = "other"
	if _, err = repository.ClaimCLI(t.Context(), different, "original-request-key", "cli"); err == nil {
		t.Fatal("request key changed its plan")
	}
	if _, err = repository.ClaimCLI(t.Context(), plan, "concurrent-request-key", "cli"); err == nil {
		t.Fatal("concurrent writer acquired claim")
	}
}
func TestCheckpointRetainsLocalEvidenceAndCannotOverwriteAnotherClaim(t *testing.T) {
	for _, scenario := range []string{"local-only-with-claim", "different-job", "foreign-installation", "unavailable-mirror", "changed-mirror"} {
		t.Run(scenario, func(t *testing.T) {
			driver, engine, journal := maintenanceFixture(t)
			repository := sqlRepository(t, driver, engine)
			plan, err := driver.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Verify, Revision: 2})
			if err != nil {
				t.Fatal(err)
			}
			job, err := repository.ClaimCLI(t.Context(), plan, "checkpoint-claim-key", "cli")
			if err != nil {
				t.Fatal(err)
			}
			checkpoint := Checkpointer{Journal: journal, Repository: repository, Engine: engine, Job: &job}
			switch scenario {
			case "local-only-with-claim":
				checkpoint.Job = nil
			case "different-job":
				copy := job
				copy.ID = "other"
				checkpoint.Job = &copy
			case "foreign-installation":
				driver.Record.MaintenanceAnchor = "other"
			case "unavailable-mirror":
				engine.fail = "POST /configs/"
			case "changed-mirror":
				engine.control["ID"] = "other"
			}
			before := len(engine.calls)
			if err = checkpoint.Save(driver.Record); err == nil {
				t.Fatal("unconfirmed checkpoint returned success")
			}
			if journal.saved != 1 || driver.Record.CheckpointRevision != 1 {
				t.Fatal("local intent was not retained first")
			}
			if scenario != "unavailable-mirror" {
				for _, call := range engine.calls[before:] {
					if strings.HasPrefix(call, "POST ") {
						t.Fatal("unverified mirror was overwritten")
					}
				}
			}
			value, _, err := repository.Installation(t.Context())
			if err != nil || value.OperationID != job.ID {
				t.Fatal("checkpoint lost original writer claim", err)
			}
		})
	}
}

func providerFixture(t *testing.T) (*Provider, *Driver, *fixtureEngine, Repository) {
	t.Helper()
	driver, engine, _ := maintenanceFixture(t)
	repository := sqlRepository(t, driver, engine)
	return &Provider{Repository: repository, Engine: engine, Directory: "/var/lib/nsc-bootstrap", Runtime: func(*bootstrap.Record) Runtime { return driver.Runtime }}, driver, engine, repository
}
func changeInstallation(t *testing.T, repository Repository, change func(*bootstrap.Installation)) {
	t.Helper()
	value, document, err := repository.Installation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	change(&value)
	if err = repository.Store.WriteControlDocuments(t.Context(), []storage.ControlWrite{write(document.ID, document.Kind, value.ID, value, document.Revision)}); err != nil {
		t.Fatal(err)
	}
}
func storeJobFixture(t *testing.T, repository Repository, job bootstrap.MaintenanceJob) {
	t.Helper()
	_, document, err := repository.Job(t.Context(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.Store.WriteControlDocuments(t.Context(), []storage.ControlWrite{write(document.ID, document.Kind, job.InstallationID, storedJob{MaintenanceJob: job, KeyHash: job.Key}, document.Revision)}); err != nil {
		t.Fatal(err)
	}
}
func launchRepair(t *testing.T, p *Provider) (bootstrap.Plan, bootstrap.MaintenanceJob) {
	t.Helper()
	plan, err := p.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	job, err := p.Start(t.Context(), bootstrap.ExecuteRequest{PlanHash: plan.Hash, Revision: 2, IdempotencyKey: "provider-boundary-key"})
	if err != nil {
		t.Fatal(err)
	}
	return plan, job
}
func TestEveryMaintenanceEntryRefusesUnavailableOwnership(t *testing.T) {
	for _, entry := range []string{"view", "plan", "start", "operation", "active-operation"} {
		t.Run(entry, func(t *testing.T) {
			p, _, engine, repository := providerFixture(t)
			plan, job := launchRepair(t, p)
			if entry == "active-operation" {
				p.Repository.Store = repositoryFault{Store: repository.Store, get: func(key string) (storage.ControlDocument, error, bool) {
					if key == "installation-job:"+job.ID {
						return storage.ControlDocument{}, errors.New("database unavailable"), true
					}
					return storage.ControlDocument{}, nil, false
				}}
			} else {
				p.Repository.Store = repositoryFault{Store: repository.Store, get: func(key string) (storage.ControlDocument, error, bool) {
					if key == bootstrap.InstallationDocumentID {
						return storage.ControlDocument{}, errors.New("database unavailable"), true
					}
					return storage.ControlDocument{}, nil, false
				}}
			}
			before := len(engine.calls)
			var err error
			switch entry {
			case "view", "active-operation":
				_, err = p.View(t.Context())
			case "plan":
				_, err = p.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: 2})
			case "start":
				_, err = p.Start(t.Context(), bootstrap.ExecuteRequest{PlanHash: plan.Hash, Revision: 2, IdempotencyKey: "second-provider-key"})
			case "operation":
				_, err = p.Operation(t.Context(), job.ID)
			}
			if err == nil {
				t.Fatal("unavailable authority became maintenance permission", entry)
			}
			assertNoInfrastructureWrite(t, engine.calls[before:])
		})
	}
}
func TestWebMaintenancePreparationRequiresCompleteOriginalAuthority(t *testing.T) {
	for _, scenario := range []string{"executor", "control", "secret", "node", "identity", "expiry", "root-directory", "unclean-directory", "secret-unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			p, _, engine, repository := providerFixture(t)
			changeInstallation(t, repository, func(value *bootstrap.Installation) {
				switch scenario {
				case "executor":
					value.Executor.ID = ""
				case "control":
					value.Control.ID = ""
				case "secret":
					value.CredentialSecret.ID = ""
				case "node":
					value.MaintenanceNodeID = ""
				case "identity":
					value.MaintenanceID = ""
				case "expiry":
					value.MaintenanceExpiresAt = nil
				case "root-directory":
					p.Directory = "/"
				case "unclean-directory":
					p.Directory = "/var/lib/../nsc"
				case "secret-unavailable":
					engine.fail = "GET /secrets/"
				}
			})
			view, err := p.View(t.Context())
			if err != nil || len(view.AllowedOperations) != 0 || len(view.Drift) == 0 {
				t.Fatal("unprepared installation exposed maintenance", err)
			}
			if _, err = p.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: 2}); err == nil {
				t.Fatal("unprepared plan accepted")
			}
			if _, err = p.Start(t.Context(), bootstrap.ExecuteRequest{PlanHash: "unknown", Revision: 2, IdempotencyKey: "unprepared-provider-key"}); err == nil {
				t.Fatal("unprepared job accepted")
			}
			assertNoInfrastructureWrite(t, engine.calls)
		})
	}
}
func TestAvailableActionsRespectHealthDriftAndRecordedRollback(t *testing.T) {
	for _, scenario := range []string{"unhealthy", "drift", "rollback", "no-baseline"} {
		t.Run(scenario, func(t *testing.T) {
			p, _, engine, repository := providerFixture(t)
			switch scenario {
			case "unhealthy":
				engine.health = false
			case "drift":
				service := engine.services["nginx"]
				service.Spec.Name = "changed"
				engine.services[service.ID] = service
			case "rollback":
				changeInstallation(t, repository, func(value *bootstrap.Installation) {
					value.History = []bootstrap.ReleaseReceipt{{Release: *value.Release}}
				})
			case "no-baseline":
				changeInstallation(t, repository, func(value *bootstrap.Installation) { value.Release = nil })
			}
			view, err := p.View(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			operations := map[bootstrap.Action]bool{}
			for _, action := range view.AllowedOperations {
				operations[action] = true
			}
			if scenario == "drift" {
				if len(operations) != 0 || len(view.Drift) == 0 {
					t.Fatal("external drift retained write permission")
				}
				return
			}
			if !operations[bootstrap.Verify] || !operations[bootstrap.Repair] {
				t.Fatal("controlled verification and repair were hidden")
			}
			if (scenario == "unhealthy" || scenario == "no-baseline") && operations[bootstrap.Upgrade] {
				t.Fatal("upgrade advertised without its baseline or health")
			}
			if scenario == "rollback" && !operations[bootstrap.Rollback] {
				t.Fatal("recorded rollback unavailable")
			}
		})
	}
}
func TestTerminalJobIdempotencyAndResumeCannotChangeInstallationOrPlan(t *testing.T) {
	for _, scenario := range []string{"SUCCEEDED", "FAILED", "BLOCKED", "installation", "hash", "revision", "missing-plan", "concurrent"} {
		t.Run(scenario, func(t *testing.T) {
			p, _, engine, repository := providerFixture(t)
			plan, job := launchRepair(t, p)
			input := bootstrap.ExecuteRequest{PlanHash: plan.Hash, Revision: plan.Revision, IdempotencyKey: "provider-boundary-key"}
			switch scenario {
			case "SUCCEEDED", "FAILED", "BLOCKED":
				job.State = bootstrap.State(scenario)
				storeJobFixture(t, repository, job)
			case "installation":
				job.InstallationID = "foreign"
				_, document, readErr := repository.Job(t.Context(), job.ID)
				if readErr != nil {
					t.Fatal(readErr)
				}
				document.ParentID = job.InstallationID
				document.Payload, _ = json.Marshal(storedJob{MaintenanceJob: job, KeyHash: job.Key})
				p.Repository.Store = repositoryFault{Store: repository.Store, get: func(key string) (storage.ControlDocument, error, bool) {
					if key == document.ID {
						return document, nil, true
					}
					return storage.ControlDocument{}, nil, false
				}}
				input.OperationID = job.ID
			case "hash":
				input.OperationID = job.ID
				input.PlanHash = "foreign"
			case "revision":
				input.OperationID = job.ID
				input.Revision++
			case "concurrent":
				input.IdempotencyKey = "concurrent-provider-key"
			case "missing-plan":
				changeInstallation(t, repository, func(value *bootstrap.Installation) { value.OperationID = "" })
				input.PlanHash = "missing"
				input.IdempotencyKey = "missing-provider-plan"
			}
			before := len(engine.calls)
			saved, err := p.Start(t.Context(), input)
			if scenario == "SUCCEEDED" || scenario == "FAILED" || scenario == "BLOCKED" {
				if err != nil || saved.ID != job.ID || saved.State != job.State {
					t.Fatal("terminal request did not retain its result", err)
				}
			} else if err == nil {
				t.Fatal("changed ownership or approval resumed", scenario)
			}
			assertNoInfrastructureWrite(t, engine.calls[before:])
			if scenario == "installation" {
				if _, err = p.Operation(t.Context(), job.ID); !errors.Is(err, storage.ErrNotFound) {
					t.Fatal("foreign operation was readable", err)
				}
			}
		})
	}
}
func TestUnconfirmedRunnerReceiptRetainsUnknownAndItsClaim(t *testing.T) {
	for _, scenario := range []string{"id", "version", "spec", "iteration", "read"} {
		t.Run(scenario, func(t *testing.T) {
			p, _, engine, repository := providerFixture(t)
			plan, err := p.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: 2})
			if err != nil {
				t.Fatal(err)
			}
			engine.lost = true
			job, err := p.Start(t.Context(), bootstrap.ExecuteRequest{PlanHash: plan.Hash, Revision: 2, IdempotencyKey: "uncertain-receipt-key"})
			if err == nil || job.Acknowledged {
				t.Fatal("lost acknowledgement was hidden")
			}
			service := engine.services["executor"]
			switch scenario {
			case "id":
				service.ID = "foreign"
			case "version":
				service.Version.Index = job.Runner.Version
			case "spec":
				service.Spec.Name = "external"
			case "iteration":
				service.JobStatus = nil
			case "read":
				engine.fail = "GET /services/executor"
			}
			engine.services["executor"] = service
			job.UpdatedAt = time.Now().Add(-time.Minute)
			storeJobFixture(t, repository, job)
			observed, err := p.Operation(t.Context(), job.ID)
			if scenario == "read" {
				if err == nil {
					t.Fatal("unavailable runner returned confirmed evidence")
				}
				return
			}
			if err != nil || observed.State != bootstrap.Unknown || observed.Acknowledged || observed.Reason != "executor_acknowledgement_unconfirmed" {
				t.Fatal("unconfirmed receipt became success", observed.State, err)
			}
			value, _, err := repository.Installation(t.Context())
			if err != nil || value.OperationID != job.ID {
				t.Fatal("uncertain outcome released its writer fence", err)
			}
		})
	}
}
func TestOnlyCurrentStoppedIterationAuthorizesAReplacementRunner(t *testing.T) {
	for _, scenario := range []string{"running", "orphaned", "old-iteration", "no-iteration", "wrong-service", "other-job", "no-container", "no-task", "complete", "shutdown", "failed", "rejected"} {
		t.Run(scenario, func(t *testing.T) {
			p, _, engine, repository := providerFixture(t)
			plan, job := launchRepair(t, p)
			service := engine.services["executor"]
			iteration := service.JobStatus.JobIteration
			task := swarm.Task{ID: "current-task", ServiceID: service.ID, JobIteration: &iteration, Spec: service.Spec.TaskTemplate, Status: swarm.TaskStatus{State: swarm.TaskStateFailed}}
			switch scenario {
			case "running":
				task.Status.State = swarm.TaskStateRunning
			case "orphaned":
				task.Status.State = swarm.TaskState("orphaned")
			case "old-iteration":
				older := swarm.Version{Index: iteration.Index - 1}
				task.JobIteration = &older
			case "no-iteration":
				task.JobIteration = nil
			case "wrong-service":
				task.ServiceID = "other"
			case "other-job":
				copy := *task.Spec.ContainerSpec
				copy.Args = []string{"bootstrap-worker", "--job", "other-job"}
				task.Spec.ContainerSpec = &copy
			case "no-container":
				task.Spec.ContainerSpec = nil
			case "complete":
				task.Status.State = swarm.TaskStateComplete
			case "shutdown":
				task.Status.State = swarm.TaskStateShutdown
			case "rejected":
				task.Status.State = swarm.TaskStateRejected
			}
			engine.tasks = []swarm.Task{task}
			if scenario == "no-task" || scenario == "wrong-service" {
				p.Engine = engineFunc(func(ctx context.Context, method, path string, input, output any) error {
					if strings.HasPrefix(path, "/tasks?") {
						return encodeFixture([]swarm.Task{task}, output)
					}
					return engine.Engine(ctx, method, path, input, output)
				})
			}
			if scenario == "no-task" {
				p.Engine = engineFunc(func(ctx context.Context, method, path string, input, output any) error {
					if strings.HasPrefix(path, "/tasks?") {
						return encodeFixture([]swarm.Task{}, output)
					}
					return engine.Engine(ctx, method, path, input, output)
				})
			}
			job.State = bootstrap.Unknown
			storeJobFixture(t, repository, job)
			before := len(engine.calls)
			resumed, err := p.Start(t.Context(), bootstrap.ExecuteRequest{OperationID: job.ID, PlanHash: plan.Hash, Revision: 2, IdempotencyKey: "resume-current-iteration"})
			terminal := scenario == "complete" || scenario == "shutdown" || scenario == "failed" || scenario == "rejected"
			if terminal {
				if err != nil || resumed.ID != job.ID || !resumed.Acknowledged {
					t.Fatal("current stopped task could not resume", err)
				}
			} else {
				if err == nil {
					t.Fatal("unproven stop authorized replacement", scenario)
				}
				assertNoInfrastructureWrite(t, engine.calls[before:])
			}
		})
	}
}
func TestCrashBeforeDurableLaunchIntentCanRetryOnlyItsOriginalClaim(t *testing.T) {
	p, _, engine, repository := providerFixture(t)
	plan, err := p.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	job, err := repository.ClaimCLI(t.Context(), plan, "unlaunched-runner-key", "admin")
	if err != nil {
		t.Fatal(err)
	}
	// Model a Web claim interrupted before launch, rather than borrowing a
	// CLI writer's deliberately different recovery authority.
	installation, document, err := repository.Installation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	job.ID = "maint-unlaunched-fixture"
	job.ExecutionMode = "service"
	job.State = bootstrap.Pending
	job.UpdatedAt = time.Now().Add(-time.Minute)
	installation.OperationID = job.ID
	if err = repository.Store.WriteControlDocuments(t.Context(), []storage.ControlWrite{write(document.ID, document.Kind, installation.ID, installation, document.Revision), jobWrite(job, 0)}); err != nil {
		t.Fatal(err)
	}
	storeJobFixture(t, repository, job)
	resumed, err := p.Start(t.Context(), bootstrap.ExecuteRequest{OperationID: job.ID, PlanHash: plan.Hash, Revision: 2, IdempotencyKey: "retry-unlaunched-runner"})
	if err != nil || !resumed.Acknowledged || resumed.ID != job.ID || engine.services["executor"].Spec.TaskTemplate.ContainerSpec.Args[0] != "bootstrap-worker" {
		t.Fatal("durably absent intent could not resume its original object", err)
	}
}

func TestStalePollingAndLostLocksCannotOverwriteCompletionOrReleaseAWriter(t *testing.T) {
	p, _, _, repository := providerFixture(t)
	_, original := launchRepair(t, p)
	stale := original
	completed := original
	completed.State = bootstrap.Succeeded
	if err := repository.SaveJob(t.Context(), &completed); err != nil {
		t.Fatal(err)
	}
	stale.State = bootstrap.Unknown
	if err := repository.SaveJob(t.Context(), &stale); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("stale poll overwrote terminal state", err)
	}
	saved, _, err := repository.Job(t.Context(), original.ID)
	if err != nil || saved.State != bootstrap.Succeeded {
		t.Fatal("completion was lost", err)
	}
	// Even the current revision cannot turn a terminal receipt back into UNKNOWN.
	saved.State = bootstrap.Unknown
	if err = repository.SaveJob(t.Context(), &saved); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("terminal state regressed", err)
	}
	p, _, _, repository = providerFixture(t)
	_, original = launchRepair(t, p)
	if err = uncertainExecutor(t.Context(), repository, original, "executor_busy"); err == nil {
		t.Fatal("busy writer was not reported")
	}
	value, _, err := repository.Installation(t.Context())
	if err != nil || value.OperationID != original.ID {
		t.Fatal("busy old writer lost its claim", err)
	}
	saved, _, err = repository.Job(t.Context(), original.ID)
	if err != nil || saved.State != bootstrap.Unknown || saved.Reason != "executor_busy" {
		t.Fatal("unproven old writer became a definite failure", err)
	}
}

func TestLateAcknowledgementCannotOverwriteWorkerProgress(t *testing.T) {
	p, _, _, repository := providerFixture(t)
	_, job := launchRepair(t, p)
	stale := job
	stale.Acknowledged = false
	stale.Runner.Version--
	stale.Revision--
	job.State = bootstrap.Running
	job.Operation = &bootstrap.Operation{ID: "durable-progress", State: bootstrap.Running, Plan: job.Plan}
	if err := repository.SaveJob(t.Context(), &job); err != nil {
		t.Fatal(err)
	}
	installation, _, err := repository.Installation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	observed, err := p.acknowledge(t.Context(), stale, installation)
	if err != nil || observed.State != bootstrap.Running || observed.Operation == nil || observed.Operation.ID != "durable-progress" {
		t.Fatal("late ACK overwrote newer executor progress", err)
	}
}

func TestCheckpointFailuresRetainIntentWithoutAdvancingAnUnconfirmedMirror(t *testing.T) {
	for _, scenario := range []string{"installation-read", "job-read", "database-write", "audit-write", "mirror-read", "mirror-owner", "mirror-data", "cluster", "installation"} {
		t.Run(scenario, func(t *testing.T) {
			driver, engine, journal := maintenanceFixture(t)
			repository := sqlRepository(t, driver, engine)
			plan, err := driver.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Verify, Revision: 2})
			if err != nil {
				t.Fatal(err)
			}
			job, err := repository.ClaimCLI(t.Context(), plan, "checkpoint-outage-fixture", "cli")
			if err != nil {
				t.Fatal(err)
			}
			checkpoint := Checkpointer{Journal: journal, Repository: repository, Engine: engine, Job: &job}
			switch scenario {
			case "installation-read", "job-read":
				checkpoint.Repository.Store = repositoryFault{Store: repository.Store, get: func(key string) (storage.ControlDocument, error, bool) {
					if scenario == "installation-read" && key == bootstrap.InstallationDocumentID || scenario == "job-read" && key == "installation-job:"+job.ID {
						return storage.ControlDocument{}, errors.New("checkpoint authority unavailable"), true
					}
					return storage.ControlDocument{}, nil, false
				}}
			case "database-write":
				checkpoint.Repository.Store = repositoryFault{Store: repository.Store, writeError: errors.New("checkpoint database unavailable")}
			case "audit-write":
				checkpoint.Repository.Store = repositoryFault{Store: repository.Store, auditError: errors.New("audit unavailable")}
			case "mirror-read":
				engine.fail = "GET /configs/"
			case "mirror-owner":
				engine.control["Spec"].(map[string]any)["Labels"] = map[string]string{"io.nsc.installation": "foreign"}
			case "mirror-data":
				engine.control["Spec"].(map[string]any)["Data"] = []byte("foreign checkpoint")
			case "cluster":
				driver.Record.ClusterID = "foreign"
			case "installation":
				driver.Record.ID = "foreign"
			}
			before := len(engine.calls)
			if err = checkpoint.Save(driver.Record); err == nil {
				t.Fatal("unconfirmed checkpoint advanced", scenario)
			}
			if journal.saved != 1 {
				t.Fatal("private intent was not saved before external evidence")
			}
			for _, call := range engine.calls[before:] {
				if strings.HasPrefix(call, "POST ") {
					t.Fatal("failed checkpoint updated Swarm", scenario, call)
				}
			}
			value, _, err := repository.Installation(t.Context())
			if err != nil || value.OperationID != job.ID {
				t.Fatal("checkpoint failure released the original claim", err)
			}
		})
	}
}
func TestJobWritesAndCLIClaimsRequireCurrentAvailableAuthority(t *testing.T) {
	for _, scenario := range []string{"job-read", "job-write", "plan", "installation", "claim-read"} {
		t.Run(scenario, func(t *testing.T) {
			p, _, _, repository := providerFixture(t)
			plan, job := launchRepair(t, p)
			candidate := repository
			switch scenario {
			case "job-read":
				candidate.Store = repositoryFault{Store: repository.Store, get: func(key string) (storage.ControlDocument, error, bool) {
					if key == "installation-job:"+job.ID {
						return storage.ControlDocument{}, storage.ErrNotFound, true
					}
					return storage.ControlDocument{}, nil, false
				}}
			case "job-write":
				candidate.Store = repositoryFault{Store: repository.Store, writeError: errors.New("job checkpoint unavailable")}
			case "plan":
				job.Plan.Hash = "different"
			case "installation":
				job.InstallationID = "foreign"
			case "claim-read":
				candidate.Store = repositoryFault{Store: repository.Store, get: func(key string) (storage.ControlDocument, error, bool) {
					if key == bootstrap.InstallationDocumentID {
						return storage.ControlDocument{}, errors.New("ownership unavailable"), true
					}
					return storage.ControlDocument{}, nil, false
				}}
			}
			var err error
			if scenario == "claim-read" {
				_, err = candidate.ClaimCLI(t.Context(), plan, "unavailable-cli-claim", "cli")
			} else {
				err = candidate.SaveJob(t.Context(), &job)
			}
			if err == nil {
				t.Fatal("stale or unavailable authority accepted a job write", scenario)
			}
			saved, _, err := repository.Job(t.Context(), job.ID)
			if err != nil || saved.State != bootstrap.Pending || saved.Plan.Hash != plan.Hash {
				t.Fatal("failed write replaced the original job", err)
			}
		})
	}
	for _, state := range []bootstrap.State{bootstrap.Succeeded, bootstrap.Failed, bootstrap.Blocked} {
		p, _, _, repository := providerFixture(t)
		_, job := launchRepair(t, p)
		job.State = state
		if err := repository.SaveJob(t.Context(), &job); err != nil {
			t.Fatal(err)
		}
		job.State = bootstrap.Unknown
		if err := repository.SaveJob(t.Context(), &job); !errors.Is(err, storage.ErrConflict) {
			t.Fatal("a terminal outcome was reopened by a poll", state, err)
		}
	}
}
func TestAcknowledgementCannotChangeApprovalOrDisplaceAnotherReceipt(t *testing.T) {
	for _, scenario := range []string{"plan", "revision", "runner-read", "runner-drift", "runner-status"} {
		t.Run(scenario, func(t *testing.T) {
			p, _, engine, repository := providerFixture(t)
			_, job := launchRepair(t, p)
			stale := job
			stale.Runner.Version--
			stale.Revision--
			stale.Acknowledged = false
			installation, _, err := repository.Installation(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "plan" || scenario == "revision" {
				_, document, err := repository.Job(t.Context(), job.ID)
				if err != nil {
					t.Fatal(err)
				}
				shadow := job
				shadow.Acknowledged = false
				if scenario == "plan" {
					shadow.Plan.Hash = "foreign"
				}
				document.Payload, _ = json.Marshal(storedJob{MaintenanceJob: shadow, KeyHash: job.Key})
				p.Repository.Store = repositoryFault{Store: repository.Store, get: func(key string) (storage.ControlDocument, error, bool) {
					if key == document.ID {
						return document, nil, true
					}
					return storage.ControlDocument{}, nil, false
				}}
				if _, err = p.acknowledge(t.Context(), stale, installation); !errors.Is(err, storage.ErrConflict) {
					t.Fatal("stale ACK changed approval", err)
				}
			} else {
				job.State = bootstrap.Unknown
				storeJobFixture(t, repository, job)
				service := engine.services["executor"]
				if scenario == "runner-read" {
					engine.fail = "GET /services/executor"
				}
				if scenario == "runner-drift" {
					service.Spec.Name = "external"
				}
				if scenario == "runner-status" {
					service.JobStatus = nil
				}
				engine.services["executor"] = service
				before := len(engine.calls)
				if _, err = p.Start(t.Context(), bootstrap.ExecuteRequest{OperationID: job.ID, PlanHash: job.Plan.Hash, Revision: job.Plan.Revision, IdempotencyKey: "unconfirmed-runner-proof"}); err == nil {
					t.Fatal("unconfirmed current runner was replaced")
				}
				assertNoInfrastructureWrite(t, engine.calls[before:])
			}
		})
	}
}

func TestConsoleCannotLaunchOrResumeAnIndependentCLIWriter(t *testing.T) {
	for _, state := range []bootstrap.State{bootstrap.Pending, bootstrap.Running, bootstrap.Unknown} {
		t.Run(string(state), func(t *testing.T) {
			driver, engine, _ := maintenanceFixture(t)
			repository := sqlRepository(t, driver, engine)
			plan, err := driver.Plan(t.Context(), bootstrap.PlanRequest{Action: bootstrap.Repair, Revision: 2})
			if err != nil {
				t.Fatal(err)
			}
			job, err := repository.ClaimCLI(t.Context(), plan, "node-local-cli-key", "cli")
			if err != nil {
				t.Fatal(err)
			}
			job.State = state
			if err = repository.SaveJob(t.Context(), &job); err != nil {
				t.Fatal(err)
			}
			provider := Provider{Repository: repository, Engine: engine}
			engine.calls = nil
			_, err = provider.Start(t.Context(), bootstrap.ExecuteRequest{OperationID: job.ID, PlanHash: plan.Hash, Revision: plan.Revision, IdempotencyKey: "console-resume-key"})
			var blocked *bootstrap.MaintenanceError
			if !errors.As(err, &blocked) || blocked.Code != "cli_resume_required" {
				t.Fatal("console gained authority over a CLI writer", err)
			}
			for _, call := range engine.calls {
				if strings.HasPrefix(call, "POST ") {
					t.Fatal("console changed the runner before fencing the CLI", call)
				}
			}
			installation, _, err := repository.Installation(t.Context())
			if err != nil || installation.OperationID != job.ID {
				t.Fatal("CLI claim was discarded", err)
			}
		})
	}
}
