package install

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/bootstrap/maintenance"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type preparationCheckpointFailure struct {
	DatabaseConnector
	key   string
	state bootstrap.State
	fired bool
}

func (f *preparationCheckpointFailure) Open(ctx context.Context, cfg config.Config, host Engine, container string, readOnly bool) (storage.Store, error) {
	store, err := f.DatabaseConnector.Open(ctx, cfg, host, container, readOnly)
	if err != nil || readOnly {
		return store, err
	}
	return &preparationCheckpointStore{Store: store, failure: f}, nil
}

type preparationCheckpointStore struct {
	storage.Store
	failure *preparationCheckpointFailure
}

func (s *preparationCheckpointStore) ValidateRecoverySchema(ctx context.Context) error {
	return s.Store.(interface{ ValidateRecoverySchema(context.Context) error }).ValidateRecoverySchema(ctx)
}
func (s *preparationCheckpointStore) RecoveryPrepared(ctx context.Context) (bool, error) {
	return s.Store.(interface {
		RecoveryPrepared(context.Context) (bool, error)
	}).RecoveryPrepared(ctx)
}
func (s *preparationCheckpointStore) WriteControlDocuments(ctx context.Context, writes []storage.ControlWrite) error {
	fail := false
	for _, write := range writes {
		if s.failure.fired || write.Document.Kind != "system-installation-job" {
			continue
		}
		var job bootstrap.MaintenanceJob
		if json.Unmarshal(write.Document.Payload, &job) != nil || job.Operation == nil {
			continue
		}
		for _, step := range job.Operation.Plan.Steps {
			if step.Key == s.failure.key && step.State == s.failure.state {
				fail = true
			}
		}
	}
	if err := s.Store.WriteControlDocuments(ctx, writes); err != nil {
		return err
	}
	if fail {
		s.failure.fired = true
		return context.Canceled
	}
	return nil
}

// A lost checkpoint acknowledgment can occur on either side of a component
// mutation. Exercise the public CLI coordinator, its SQL claim, local journal
// and read-only reconciliation instead of reconstructing internal step state.
func TestPreparationResumesDurablePhaseCheckpointsWithoutReplacingData(t *testing.T) {
	for _, key := range []string{"prepare:backup", "prepare:images", "prepare:materials", "prepare:secret", "prepare:control", "prepare:executor", "prepare:files:one", "prepare:publish", "prepare:controller", "prepare:controller-ready", "update:nginx", "verify:nginx", "update:nginx-sandbox", "verify:nginx-sandbox", "prepare:handoff", "prepare:complete"} {
		for _, state := range []bootstrap.State{bootstrap.Running, bootstrap.Succeeded} {
			t.Run(key+"/"+string(state), func(t *testing.T) {
				installer, host, _ := webPreparationFixture(t)
				failure := &preparationCheckpointFailure{DatabaseConnector: installer.Database, key: key, state: state}
				installer.Database = failure
				plan, err := installer.Plan(t.Context(), bootstrap.Upgrade)
				if err != nil {
					t.Fatal(err)
				}
				database := installer.Record.Database
				operation, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "durable-preparation")
				if err == nil || !failure.fired {
					t.Fatal("checkpoint interruption was not retained", operation.State, err)
				}
				resumed, resumeErr := installer.ResumeMaintenance(t.Context(), operation.ID)
				// A creation whose actual ID was never returned has no ownership receipt.
				// Its name/label must not be used to create or adopt another object.
				unknown := state == bootstrap.Running && (key == "prepare:secret" || key == "prepare:control" || key == "prepare:executor")
				if unknown {
					if resumeErr == nil || resumed.State != bootstrap.Unknown {
						t.Fatal("unconfirmed mutation gained success", resumed.State, resumeErr)
					}
				} else if resumeErr != nil || resumed.ID != operation.ID || resumed.State != bootstrap.Succeeded {
					t.Fatal("durable continuation did not reconcile", resumed.State, resumeErr)
				}
				if installer.Record.Database != database {
					t.Fatal("interrupted preparation changed its data pin")
				}
				if host.cluster.services[database.ServiceID].ID != database.ServiceID {
					t.Fatal("database service was replaced")
				}
			})
		}
	}
}

func TestPreparationRequiresTheOriginalClaimAndExplicitApproval(t *testing.T) {
	for _, scenario := range []string{"wrong-action", "no-adoption-receipt", "unfinished-adoption", "wrong-approval", "wrong-revision", "missing-preparation-proof", "missing-owner", "foreign-writer", "missing-resume-references", "missing-resume-operation"} {
		t.Run(scenario, func(t *testing.T) {
			installer, host, _ := webPreparationFixture(t)
			plan, err := installer.Plan(t.Context(), bootstrap.Upgrade)
			if err != nil {
				t.Fatal(err)
			}
			before := bootstrap.Digest(host.cluster.services)
			switch scenario {
			case "wrong-action":
				_, err = installer.PlanWebPreparation(t.Context(), bootstrap.Repair)
			case "no-adoption-receipt":
				installer.Record.Operations = nil
				_, err = installer.PlanWebPreparation(t.Context(), bootstrap.Upgrade)
			case "unfinished-adoption":
				installer.Record.Operations[0].State = bootstrap.Running
				_, err = installer.PlanWebPreparation(t.Context(), bootstrap.Upgrade)
			case "wrong-approval":
				_, err = installer.ExecuteWebPreparation(t.Context(), plan, "unapproved", "claim-boundary")
			case "wrong-revision":
				installer.Record.Revision++
				_, err = installer.ExecuteWebPreparation(t.Context(), plan, plan.Hash, "claim-boundary")
			case "missing-preparation-proof":
				plan.Scope.Preparation = nil
				_, err = installer.ExecuteWebPreparation(t.Context(), plan, plan.Hash, "claim-boundary")
			case "missing-owner":
				installer.Record.MaintenanceUID = -1
				_, err = installer.ExecuteWebPreparation(t.Context(), plan, plan.Hash, "claim-boundary")
			case "foreign-writer":
				installer.Record.Operations = append(installer.Record.Operations, bootstrap.Operation{State: bootstrap.Unknown})
				_, err = installer.PlanWebPreparation(t.Context(), bootstrap.Upgrade)
			case "missing-resume-references":
				_, err = installer.ResumeWebPreparation(t.Context(), "unrecorded")
			case "missing-resume-operation":
				installer.Record.Preparation = &bootstrap.PreparationReferences{}
				_, err = installer.ResumeWebPreparation(t.Context(), "unrecorded")
			default:
				err = errors.New("unknown scenario")
			}
			if err == nil || bootstrap.Digest(host.cluster.services) != before {
				t.Fatal("an unapproved or unowned continuation changed resources", err)
			}
		})
	}
}

func TestPreparationPlansCannotDisplaceExistingWritersOrPartialAuthority(t *testing.T) {
	for _, scenario := range []string{"PENDING", "RUNNING", "UNKNOWN", "BLOCKED", "active-sql-claim", "private-preparation", "reserved-executor", "changed-data-pin", "unsupported-release", "offline-reference-in-metadata"} {
		t.Run(scenario, func(t *testing.T) {
			installer, host, _ := webPreparationFixture(t)
			switch scenario {
			case "PENDING", "RUNNING", "UNKNOWN", "BLOCKED":
				installer.Record.Operations = append(installer.Record.Operations, bootstrap.Operation{State: bootstrap.State(scenario)})
			case "active-sql-claim":
				adoptStore(t, installer, func(store storage.Store) error {
					repository := maintenance.Repository{Store: store}
					owner, document, err := repository.Installation(t.Context())
					if err != nil {
						return err
					}
					owner.OperationID = "unfenced-writer"
					document.Payload = mustPreparationJSON(owner)
					return store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: document, Expected: document.Revision}})
				})
			case "private-preparation":
				installer.Record.Preparation = &bootstrap.PreparationReferences{}
			case "reserved-executor":
				installer.Record.Resources["reserve:executor"] = bootstrap.Resource{ID: "already-reserved"}
			case "changed-data-pin":
				installer.Record.Database.Volume = "foreign-volume"
			case "unsupported-release":
				installer.Target.Protocol++
			case "offline-reference-in-metadata":
				installer.OriginalCAPrivateKeyFile = filepath.Join(installer.Directory, "ca.key")
			}
			before := bootstrap.Digest(host.cluster.services)
			if _, err := installer.PlanWebPreparation(t.Context(), bootstrap.Upgrade); err == nil {
				t.Fatal("an existing writer or partial authority was displaced")
			}
			if bootstrap.Digest(host.cluster.services) != before {
				t.Fatal("rejected planning changed a service")
			}
		})
	}
}

func TestPreparationResumesCompletedLocalIdentityButNeverRegeneratesPartialMaterial(t *testing.T) {
	for _, scenario := range []string{"partial-directory", "stale-completion", "completed-before-reply", "changed-original-ca"} {
		t.Run(scenario, func(t *testing.T) {
			installer, _, operation := pausedPreparation(t, "prepare:materials")
			switch scenario {
			case "partial-directory":
				if err := os.Mkdir(installer.materialDir(), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(installer.material("ca.key"), []byte("truncated disposable identity fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			case "stale-completion", "completed-before-reply":
				if err := installer.makePreparationMaterials(); err != nil {
					t.Fatal(err)
				}
				if scenario == "stale-completion" {
					if err := os.WriteFile(installer.material("complete.json"), []byte("unconfirmed-manifest"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "changed-original-ca":
				if err := os.WriteFile(installer.Record.Preparation.CACertificateFile, []byte("changed public CA fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := installer.materialDigest()
			database := installer.Record.Database
			resumed, err := installer.ResumeMaintenance(t.Context(), operation.ID)
			if scenario == "completed-before-reply" {
				if err != nil || resumed.State != bootstrap.Succeeded || resumed.ID != operation.ID {
					t.Fatal("completed original identity could not reconcile", resumed.State, err)
				}
			} else {
				if err == nil || resumed.State == bootstrap.Succeeded {
					t.Fatal("partial or changed identity was regenerated", resumed.State, err)
				}
				after, _ := installer.materialDigest()
				if before != after && scenario != "changed-original-ca" {
					t.Fatal("unconfirmed local keys were replaced")
				}
			}
			if installer.Record.Database != database {
				t.Fatal("identity recovery changed the database pin")
			}
		})
	}
}

func TestPreparationApprovalAndIdempotencyRemainBoundAfterPlanning(t *testing.T) {
	for _, scenario := range []string{"changed-proof", "raced-claim", "same-key-different-plan", "historical-failed-adoption", "historical-other-operation", "missing-ca-file", "missing-key-file"} {
		t.Run(scenario, func(t *testing.T) {
			installer, host, _ := webPreparationFixture(t)
			if scenario == "historical-failed-adoption" {
				installer.Record.Operations = append(installer.Record.Operations, bootstrap.Operation{State: bootstrap.Failed, Plan: bootstrap.Plan{Action: bootstrap.Adopt}})
			}
			if scenario == "historical-other-operation" {
				installer.Record.Operations = append(installer.Record.Operations, bootstrap.Operation{State: bootstrap.Succeeded, Plan: bootstrap.Plan{Action: bootstrap.Verify}})
			}
			if scenario == "missing-ca-file" {
				installer.OriginalCACertificateFile = filepath.Join(t.TempDir(), "missing.crt")
			}
			if scenario == "missing-key-file" {
				installer.OriginalCAPrivateKeyFile = filepath.Join(t.TempDir(), "missing.key")
			}
			plan, err := installer.PlanWebPreparation(t.Context(), bootstrap.Upgrade)
			if scenario == "missing-ca-file" || scenario == "missing-key-file" {
				if err == nil {
					t.Fatal("missing original CA material was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "historical-failed-adoption" || scenario == "historical-other-operation" {
				return
			}
			key := "exact-preparation-approval"
			if scenario == "same-key-different-plan" {
				operation, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, key)
				if err != nil || operation.State != bootstrap.Succeeded {
					t.Fatal(err)
				}
				plan.Hash = "another-approval"
			}
			if scenario == "changed-proof" {
				plan.Scope.Preparation.Proof = "other-evidence"
			}
			if scenario == "raced-claim" {
				adoptStore(t, installer, func(store storage.Store) error {
					repository := maintenance.Repository{Store: store}
					owner, document, err := repository.Installation(t.Context())
					if err != nil {
						return err
					}
					owner.OperationID = "another-writer"
					document.Payload = mustPreparationJSON(owner)
					return store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: document, Expected: document.Revision}})
				})
			}
			before := bootstrap.Digest(host.cluster.services)
			if _, err := installer.ExecuteWebPreparation(t.Context(), plan, plan.Hash, key); err == nil {
				t.Fatal("changed approval or concurrent claim was accepted")
			}
			if bootstrap.Digest(host.cluster.services) != before {
				t.Fatal("a rejected approval changed resources")
			}
		})
	}
}

func TestPreparedNodeFilesResumeOnlyTheirOriginalUnchangedIntent(t *testing.T) {
	for _, scenario := range []string{"changed-node", "changed-local-copy", "already-written-local-copy"} {
		t.Run(scenario, func(t *testing.T) {
			installer, host, _ := webPreparationFixture(t)
			host.lostFiles = true
			plan, err := installer.Plan(t.Context(), bootstrap.Upgrade)
			if err != nil {
				t.Fatal(err)
			}
			operation, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "lost-node-file-reply")
			if err == nil || operation.State != bootstrap.Unknown {
				t.Fatal("lost node-file reply was not retained", err)
			}
			local := filepath.Join(installer.preparationDirectory(), "prepared-one.yaml")
			switch scenario {
			case "changed-node":
				host.prepared["controller.yaml"] = append(host.prepared["controller.yaml"], []byte("\n# external change\n")...)
			case "changed-local-copy":
				if err := os.WriteFile(local, []byte("external local configuration"), 0600); err != nil {
					t.Fatal(err)
				}
			case "already-written-local-copy":
				if err := os.WriteFile(local, host.prepared["controller.yaml"], 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := bootstrap.Digest(host.cluster.services)
			resumed, err := installer.ResumeMaintenance(t.Context(), operation.ID)
			if scenario == "already-written-local-copy" {
				if err != nil || resumed.State != bootstrap.Succeeded {
					t.Fatal("the exact original local copy could not continue", resumed.State, err)
				}
			} else if err == nil || resumed.State == bootstrap.Succeeded || bootstrap.Digest(host.cluster.services) != before {
				t.Fatal("unapproved node or local files were overwritten", resumed.State, err)
			}
		})
	}
}
