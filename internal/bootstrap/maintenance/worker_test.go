package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func prepareWorker(t *testing.T, actions ...bootstrap.Action) (*Driver, *fixtureEngine, Repository, bootstrap.MaintenanceJob, string, []byte) {
	t.Helper()
	driver, engine, _ := maintenanceFixture(t)
	repository := sqlRepository(t, driver, engine)
	directory := filepath.Join(t.TempDir(), "metadata")
	journal, err := bootstrap.OpenJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	machine, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		t.Fatal(err)
	}
	driver.Record.MaintenanceIdentity = bootstrap.Digest(strings.TrimSpace(string(machine)))
	driver.Record.MaintenanceAnchor = bootstrap.Digest([]string{driver.Record.MaintenanceIdentity, directory})
	driver.Record.MetadataLayout = 1
	installation, document, err := repository.Installation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	installation.MaintenanceAnchor = driver.Record.MaintenanceAnchor
	data := []byte("isolated-worker-secret")
	sum := sha256.Sum256(data)
	installation.CredentialSecret.SpecDigest = hex.EncodeToString(sum[:])
	if err = repository.Store.WriteControlDocuments(t.Context(), []storage.ControlWrite{write(document.ID, document.Kind, document.ParentID, installation, document.Revision)}); err != nil {
		t.Fatal(err)
	}
	if err = journal.Save(driver.Record); err != nil {
		t.Fatal(err)
	}
	journal.Close()
	provider := Provider{Repository: repository, Engine: engine, Directory: directory, Runtime: func(*bootstrap.Record) Runtime { return driver.Runtime }}
	action := bootstrap.Verify
	if len(actions) > 0 {
		action = actions[0]
	}
	plan, err := provider.Plan(t.Context(), bootstrap.PlanRequest{Action: action, Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	job, err := provider.Start(t.Context(), bootstrap.ExecuteRequest{PlanHash: plan.Hash, Revision: 2, IdempotencyKey: "independent-worker-key", Actor: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	service := engine.services[job.Runner.ID]
	iteration := service.JobStatus.JobIteration
	engine.tasks = []swarm.Task{{ID: "own-task", ServiceID: service.ID, NodeID: "node-one", Spec: service.Spec.TaskTemplate, JobIteration: &iteration, Status: swarm.TaskStatus{State: swarm.TaskStateRunning}}}
	return driver, engine, repository, job, directory, data
}
func testHealth(bootstrap.Installation, bootstrap.MaintenanceCredential) (func(context.Context) error, error) {
	return func(context.Context) error { return nil }, nil
}
func TestIndependentWorkerPersistsAndResumesRealJournal(t *testing.T) {
	_, engine, repository, job, directory, data := prepareWorker(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := executeWorker(ctx, repository, engine, bootstrap.MaintenanceCredential{}, data, directory, job.ID, job.Runner.ID, "own-task", testHealth, testHealth); err != nil {
		t.Fatal(err)
	}
	saved, _, err := repository.Job(t.Context(), job.ID)
	if err != nil || saved.State != bootstrap.Succeeded || saved.TaskID != "own-task" || saved.Operation == nil {
		t.Fatal(saved, err)
	}
	record, err := bootstrap.ReadJournal(directory)
	if err != nil || record.Revision != 3 || len(record.Operations) != 1 || record.Operations[0].State != bootstrap.Succeeded {
		t.Fatal(record, err)
	}
	if _, err = os.Stat(directory + ".offline"); !os.IsNotExist(err) {
		t.Fatal("worker created or mounted offline private identity materials")
	}
}
func TestWorkerCannotReplaceBusyForeignOrUnacknowledgedExecutor(t *testing.T) {
	for _, scenario := range []string{"busy", "copied", "secret", "service", "task", "task-node", "iteration", "engine", "credential", "claim", "unacknowledged", "task-id", "task-service", "missing-task", "no-iteration", "no-job-status", "runner-drift", "record-id", "record-layout", "record-identity", "record-anchor", "record-corrupt", "manager", "cluster-missing", "cluster-other", "node-other", "info-unavailable", "binding", "operation-plan", "job-unavailable", "installation-unavailable", "private-material", "runner-unavailable", "owner-node-engine", "owner-anchor", "engine-info-other"} {
		t.Run(scenario, func(t *testing.T) {
			_, engine, repository, job, directory, data := prepareWorker(t)
			taskID, serviceID := "own-task", job.Runner.ID
			factory := testHealth
			bindingFactory := testHealth
			var workerEngine Engine = engine
			switch scenario {
			case "private-material":
				if err := os.WriteFile(filepath.Join(directory, "unrelated-private-material"), []byte("disposable non-metadata fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			case "engine-info-other":
				workerEngine = engineFunc(func(ctx context.Context, method, path string, input, output any) error {
					if method == "GET" && path == "/info" {
						return encodeFixture(map[string]any{"ID": "other-engine", "Swarm": map[string]any{"NodeID": "node-one", "ControlAvailable": true, "Cluster": map[string]string{"ID": "cluster-fixture"}}}, output)
					}
					return engine.Engine(ctx, method, path, input, output)
				})
			case "runner-unavailable":
				engine.fail = "GET /services/" + serviceID
			case "owner-node-engine", "owner-anchor":
				changeInstallation(t, repository, func(value *bootstrap.Installation) {
					if scenario == "owner-anchor" {
						value.MaintenanceAnchor = "other-writer-boundary"
						return
					}
					identity := value.Nodes["one"]
					identity.EngineID = "foreign-engine"
					value.Nodes["one"] = identity
				})
			case "busy":
				lock, err := bootstrap.OpenJournal(directory)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			case "copied":
				other := filepath.Join(t.TempDir(), "copied")
				os.Mkdir(other, 0700)
				raw, _ := os.ReadFile(filepath.Join(directory, "installation.json"))
				os.WriteFile(filepath.Join(other, "installation.json"), raw, 0600)
				directory = other
			case "secret":
				data = []byte("different-secret")
			case "service":
				serviceID = "other-service"
			case "task":
				taskID = ""
			case "task-node":
				engine.tasks[0].NodeID = "other-node"
			case "iteration":
				iteration := swarm.Version{Index: 999}
				engine.tasks[0].JobIteration = &iteration
			case "engine":
				record, err := bootstrap.ReadJournal(directory)
				if err != nil {
					t.Fatal(err)
				}
				identity := record.Nodes["one"]
				identity.EngineID = "foreign-engine"
				record.Nodes["one"] = identity
				journal, err := bootstrap.OpenJournal(directory)
				if err != nil {
					t.Fatal(err)
				}
				journal.Save(&record)
				journal.Close()
			case "credential":
				factory = func(bootstrap.Installation, bootstrap.MaintenanceCredential) (func(context.Context) error, error) {
					return nil, errors.New("missing private maintenance identity")
				}
			case "claim":
				value, document, err := repository.Installation(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				value.OperationID = "other-job"
				repository.Store.WriteControlDocuments(t.Context(), []storage.ControlWrite{write(document.ID, document.Kind, document.ParentID, value, document.Revision)})
			case "task-id":
				engine.tasks[0].ID = "other-task"
			case "task-service":
				engine.tasks[0].ServiceID = "other-service"
			case "missing-task":
				engine.tasks = nil
			case "no-iteration":
				engine.tasks[0].JobIteration = nil
			case "no-job-status":
				service := engine.services[serviceID]
				service.JobStatus = nil
				engine.services[serviceID] = service
			case "runner-drift":
				service := engine.services[serviceID]
				service.Spec.Name = "external"
				engine.services[serviceID] = service
			case "record-id", "record-layout", "record-identity", "record-anchor", "operation-plan":
				journal, err := bootstrap.OpenJournal(directory)
				if err != nil {
					t.Fatal(err)
				}
				record, err := journal.Read()
				if err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "record-id":
					record.ID = "other"
				case "record-layout":
					record.MetadataLayout = 0
				case "record-identity":
					record.MaintenanceIdentity = "other"
				case "record-anchor":
					record.MaintenanceAnchor = "other"
				case "operation-plan":
					plan := job.Plan
					plan.Hash = "other"
					record.Operations = []bootstrap.Operation{{ID: "old-private-operation", Key: job.Key, Plan: plan, State: bootstrap.Unknown}}
				}
				if err = journal.Save(&record); err != nil {
					t.Fatal(err)
				}
				journal.Close()
			case "record-corrupt":
				if err := os.WriteFile(filepath.Join(directory, "installation.json"), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "manager", "cluster-missing", "cluster-other", "node-other":
				workerEngine = engineFunc(func(ctx context.Context, method, path string, input, output any) error {
					if path != "/info" {
						return engine.Engine(ctx, method, path, input, output)
					}
					swarm := map[string]any{"NodeID": "node-one", "ControlAvailable": true, "Cluster": map[string]string{"ID": engine.record.ClusterID}}
					switch scenario {
					case "manager":
						swarm["ControlAvailable"] = false
					case "cluster-missing":
						delete(swarm, "Cluster")
					case "cluster-other":
						swarm["Cluster"] = map[string]string{"ID": "other"}
					case "node-other":
						swarm["NodeID"] = "other"
					}
					return encodeFixture(map[string]any{"ID": "engine-one", "Swarm": swarm}, output)
				})
			case "info-unavailable":
				engine.fail = "GET /info"
			case "binding":
				bindingFactory = func(bootstrap.Installation, bootstrap.MaintenanceCredential) (func(context.Context) error, error) {
					return nil, errors.New("original data binding unavailable")
				}
			case "job-unavailable", "installation-unavailable":
				repository.Store = repositoryFault{Store: repository.Store, get: func(key string) (storage.ControlDocument, error, bool) {
					if scenario == "job-unavailable" && key == "installation-job:"+job.ID || scenario == "installation-unavailable" && key == bootstrap.InstallationDocumentID {
						return storage.ControlDocument{}, storage.ErrNotFound, true
					}
					return storage.ControlDocument{}, nil, false
				}}
			case "unacknowledged":
				job.Acknowledged = false
				if err := repository.SaveJob(t.Context(), &job); err != nil {
					t.Fatal(err)
				}
			}
			duration := 3 * time.Second
			if scenario == "unacknowledged" {
				duration = 15 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), duration)
			defer cancel()
			before := len(engine.calls)
			if err := executeWorker(ctx, repository, workerEngine, bootstrap.MaintenanceCredential{}, data, directory, job.ID, serviceID, taskID, factory, bindingFactory); err == nil {
				t.Fatal("unconfirmed worker executed", scenario)
			}
			for _, call := range engine.calls[before:] {
				if strings.HasPrefix(call, "POST /services/") {
					t.Fatal("blocked worker mutated infrastructure", call)
				}
			}
		})
	}
}
func TestWorkerSecretLoadNeverPrintsConnectionOrKey(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "credential")
	raw, _ := json.Marshal(bootstrap.MaintenanceCredential{DSN: "not-a-valid-postgres-url\x00", PrivateKey: "isolated-private-key"})
	for _, data := range [][]byte{[]byte("not-json"), raw} {
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		err := RunWorker(ctx, directory, "job", "service", "task", file)
		cancel()
		if err == nil || strings.Contains(err.Error(), "isolated-private-key") || strings.Contains(err.Error(), "not-a-valid-postgres-url") {
			t.Fatal("worker leaked credentials", err)
		}
	}
	if err := RunWorker(t.Context(), directory, "job", "service", "task", filepath.Join(directory, "missing")); err == nil {
		t.Fatal("missing Secret accepted")
	}
}

func TestIndependentWorkerReconcilesAnInterruptedPrivateOperation(t *testing.T) {
	_, engine, repository, job, directory, data := prepareWorker(t)
	journal, err := bootstrap.OpenJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	record, err := journal.Read()
	if err != nil {
		t.Fatal(err)
	}
	plan := job.Plan
	plan.Steps[0].State = bootstrap.Unknown
	record.Operations = []bootstrap.Operation{{ID: "interrupted-worker-operation", Key: job.Key, Plan: plan, State: bootstrap.Unknown, StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}}
	if err = journal.Save(&record); err != nil {
		t.Fatal(err)
	}
	journal.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err = executeWorker(ctx, repository, engine, bootstrap.MaintenanceCredential{}, data, directory, job.ID, job.Runner.ID, "own-task", testHealth, testHealth); err != nil {
		t.Fatal(err)
	}
	saved, _, err := repository.Job(t.Context(), job.ID)
	if err != nil || saved.State != bootstrap.Succeeded || saved.Operation == nil || saved.Operation.ID != "interrupted-worker-operation" {
		t.Fatal("resume replaced operation identity", err)
	}
	record, err = bootstrap.ReadJournal(directory)
	if err != nil || len(record.Operations) != 1 || record.Revision != 3 {
		t.Fatal("resume duplicated private history", err)
	}
}

func TestCompletedLocalOperationRepairsOnlyItsMissingDatabaseReceipt(t *testing.T) {
	_, engine, repository, job, directory, data := prepareWorker(t)
	before := len(engine.calls)
	journal, err := bootstrap.OpenJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	record, err := journal.Read()
	if err != nil {
		t.Fatal(err)
	}
	record.Revision = 3
	record.Operations = []bootstrap.Operation{{ID: "already-complete", Key: job.Key, Plan: job.Plan, State: bootstrap.Succeeded}}
	if err = journal.Save(&record); err != nil {
		t.Fatal(err)
	}
	journal.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err = executeWorker(ctx, repository, engine, bootstrap.MaintenanceCredential{}, data, directory, job.ID, job.Runner.ID, "own-task", testHealth, testHealth); err != nil {
		t.Fatal(err)
	}
	saved, _, err := repository.Job(t.Context(), job.ID)
	if err != nil || saved.State != bootstrap.Succeeded || saved.Operation.ID != "already-complete" {
		t.Fatal("local completion was discarded", err)
	}
	installed, _, err := repository.Installation(t.Context())
	if err != nil || installed.Revision != 3 || installed.OperationID != "" {
		t.Fatal("receipt repair changed revision or retained claim", err)
	}
	assertNoInfrastructureWrite(t, engine.calls[before:])
}

func TestExpiredComponentPlanStillRetainsItsAcknowledgedExecutorReceipt(t *testing.T) {
	driver, engine, repository, job, directory, data := prepareWorker(t)
	observed, err := driver.Observe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	expired, err := bootstrap.NewPlan(*driver.Record, bootstrap.Verify, observed, job.Plan.Steps, time.Now().Add(-20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	job.Plan = expired.WithScope(job.Plan.Scope)
	storeJobFixture(t, repository, job)
	before := len(engine.calls)
	err = executeWorker(t.Context(), repository, engine, bootstrap.MaintenanceCredential{}, data, directory, job.ID, job.Runner.ID, "own-task", testHealth, testHealth)
	if err == nil {
		t.Fatal("expired component approval executed")
	}
	record, err := bootstrap.ReadJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	receipt := Latest(&record, job.Runner.ID)
	if receipt.SpecDigest != job.Runner.SpecDigest || receipt.Version != job.Runner.Version {
		t.Fatal("acknowledged executor receipt was lost before component execution")
	}
	saved, _, err := repository.Job(t.Context(), job.ID)
	if err != nil || saved.State != bootstrap.Blocked {
		t.Fatal("expired approval did not retain an explicit blocked outcome", err)
	}
	assertNoInfrastructureWrite(t, engine.calls[before:])
}

type receiptLossStore struct {
	*storage.SQLStore
	fail    *bool
	initial bool
}

func (s receiptLossStore) WriteControlDocuments(ctx context.Context, writes []storage.ControlWrite) error {
	for _, write := range writes {
		if write.Document.Kind != "system-installation-job" || !*s.fail {
			continue
		}
		var job storedJob
		if err := json.Unmarshal(write.Document.Payload, &job); err != nil {
			return err
		}
		lost := s.initial && job.Operation == nil
		if job.Operation != nil {
			for _, step := range job.Operation.Plan.Steps {
				if step.Key == "update:controller" && step.State == bootstrap.Succeeded {
					lost = true
				}
			}
		}
		if lost {
			*s.fail = false
			return errors.New("database checkpoint unavailable after local persistence")
		}
	}
	return s.SQLStore.WriteControlDocuments(ctx, writes)
}
func TestDatabaseCheckpointLossStopsWritesAndResumesWithoutRepeatingAnUpdate(t *testing.T) {
	for _, initial := range []bool{true, false} {
		t.Run(fmt.Sprint(initial), func(t *testing.T) {
			driver, engine, repository, job, directory, data := prepareWorker(t, bootstrap.Repair)
			pin := driver.Record.Database
			fail := true
			failing := Repository{Store: receiptLossStore{SQLStore: repository.Store.(*storage.SQLStore), fail: &fail, initial: initial}}
			before := len(engine.calls)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			err := executeWorker(ctx, failing, engine, bootstrap.MaintenanceCredential{}, data, directory, job.ID, job.Runner.ID, "own-task", testHealth, testHealth)
			if err == nil || fail {
				t.Fatal("lost database checkpoint was hidden or not exercised", err)
			}
			value, _, err := repository.Installation(t.Context())
			if err != nil || value.OperationID != job.ID {
				t.Fatal("database outage released the writer claim", err)
			}
			if initial {
				assertNoInfrastructureWrite(t, engine.calls[before:])
				return
			}
			if engine.services["controller"].Spec.TaskTemplate.ForceUpdate != 1 {
				t.Fatal("expected one acknowledged update before checkpoint loss")
			}
			saved, _, err := repository.Job(t.Context(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			saved.UpdatedAt = time.Now().Add(-time.Minute)
			storeJobFixture(t, repository, saved)
			engine.tasks[0].Status.State = swarm.TaskStateFailed
			provider := Provider{Repository: repository, Engine: engine, Directory: directory, Runtime: func(*bootstrap.Record) Runtime { return driver.Runtime }}
			resumed, err := provider.Start(t.Context(), bootstrap.ExecuteRequest{OperationID: job.ID, PlanHash: job.Plan.Hash, Revision: job.Plan.Revision, IdempotencyKey: "resume-database-checkpoint-loss"})
			if err != nil {
				t.Fatal(err)
			}
			service := engine.services[job.Runner.ID]
			iteration := service.JobStatus.JobIteration
			engine.tasks = []swarm.Task{{ID: "resumed-task", ServiceID: service.ID, NodeID: "node-one", JobIteration: &iteration, Spec: service.Spec.TaskTemplate, Status: swarm.TaskStatus{State: swarm.TaskStateRunning}}}
			if err = executeWorker(ctx, repository, engine, bootstrap.MaintenanceCredential{}, data, directory, resumed.ID, resumed.Runner.ID, "resumed-task", testHealth, testHealth); err != nil {
				t.Fatal(err)
			}
			saved, _, err = repository.Job(t.Context(), job.ID)
			if err != nil || saved.State != bootstrap.Succeeded || engine.services["controller"].Spec.TaskTemplate.ForceUpdate != 1 {
				t.Fatal("checkpoint reconciliation repeated an acknowledged update", err)
			}
			record, err := bootstrap.ReadJournal(directory)
			if err != nil || record.Database != pin || len(record.Operations) != 1 || record.Operations[0].State != bootstrap.Succeeded {
				t.Fatal("recovery replaced data ownership or operation history", err)
			}
		})
	}
}

func TestWorkerWaitsForItsDurableAcknowledgementBeforeChangingServices(t *testing.T) {
	_, engine, repository, job, directory, data := prepareWorker(t)
	job.Acknowledged = false
	if err := repository.SaveJob(t.Context(), &job); err != nil {
		t.Fatal(err)
	}
	saved := make(chan error, 1)
	go func() {
		timer := time.NewTimer(1100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-t.Context().Done():
			saved <- t.Context().Err()
			return
		case <-timer.C:
		}
		job.Acknowledged = true
		saved <- repository.SaveJob(t.Context(), &job)
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := executeWorker(ctx, repository, engine, bootstrap.MaintenanceCredential{}, data, directory, job.ID, job.Runner.ID, "own-task", testHealth, testHealth); err != nil {
		t.Fatal("a later durable acknowledgement could not continue", err)
	}
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
}
