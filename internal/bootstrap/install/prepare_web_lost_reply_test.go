package install

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/bootstrap/maintenance"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestPreparationReconcilesControllerAndGatewayRepliesWithoutOverwritingDrift(t *testing.T) {
	for _, scenario := range []string{"controller-not-sent", "controller-applied", "controller-drift", "controller-replaced", "gateway-not-sent", "gateway-applied", "gateway-drift"} {
		t.Run(scenario, func(t *testing.T) {
			installer, host, _ := webPreparationFixture(t)
			plan, err := installer.Plan(t.Context(), bootstrap.Upgrade)
			if err != nil {
				t.Fatal(err)
			}
			component := "controller"
			if strings.HasPrefix(scenario, "gateway-") {
				component = "nginx"
			}
			identifier := installer.Config.Adoption.ServiceIDs[component]
			fired := false
			installer.Hosts["one"] = interceptedHost{base: host, intercept: func(ctx context.Context, method, path string, input, output any) (bool, error) {
				if method == "GET" && strings.HasPrefix(path, "/services/") && strings.TrimPrefix(path, "/services/") == identifier {
					if _, ok := host.cluster.services[identifier]; !ok {
						return true, errors.New("original service is absent")
					}
				}
				if fired || method != "POST" || !strings.HasPrefix(path, "/services/"+identifier+"/update") {
					return false, nil
				}
				fired = true
				if strings.HasSuffix(scenario, "not-sent") {
					return true, context.DeadlineExceeded
				}
				if err := host.Engine(ctx, method, path, input, output); err != nil {
					return true, err
				}
				service := host.cluster.services[identifier]
				if strings.HasSuffix(scenario, "drift") {
					service.Spec.TaskTemplate.ContainerSpec.Env = append(service.Spec.TaskTemplate.ContainerSpec.Env, "EXTERNAL_CHANGE=preserve")
					host.cluster.services[identifier] = service
				}
				if scenario == "controller-replaced" {
					delete(host.cluster.services, identifier)
					service.ID = "foreign-replacement"
					host.cluster.services[service.ID] = service
				}
				return true, context.DeadlineExceeded
			}}
			database := installer.Record.Database
			operation, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "lost-component-reply")
			if err == nil || !fired || operation.State != bootstrap.Unknown {
				t.Fatal("lost component acknowledgment did not retain UNKNOWN", operation.State, err)
			}
			beforeResume := bootstrap.Digest(host.cluster.services)
			resumed, resumeErr := installer.ResumeMaintenance(t.Context(), operation.ID)
			untrusted := strings.HasSuffix(scenario, "drift") || scenario == "controller-replaced"
			if untrusted {
				if resumeErr == nil || resumed.State == bootstrap.Succeeded || (installer.Record.Operations[len(installer.Record.Operations)-1].State != bootstrap.Unknown && installer.Record.Operations[len(installer.Record.Operations)-1].State != bootstrap.Blocked) || bootstrap.Digest(host.cluster.services) != beforeResume {
					t.Fatal("external changes or a namesake service were overwritten", resumed.State, resumeErr)
				}
			} else if resumeErr != nil || resumed.State != bootstrap.Succeeded || resumed.ID != operation.ID {
				t.Fatal("the recorded exact service could not be reconciled", resumed.State, resumeErr)
			}
			if installer.Record.Database != database {
				t.Fatal("component recovery moved the database")
			}
		})
	}
}

func TestPreparationResumeRequiresItsOriginalSQLClaimAndJournalPlan(t *testing.T) {
	for _, scenario := range []string{"operation-key", "plan-hash", "preparation-proof", "missing-operation", "missing-job", "changed-claim", "changed-node", "changed-service", "changed-files"} {
		t.Run(scenario, func(t *testing.T) {
			installer, host, operation := pausedPreparation(t, "prepare:controller")
			switch scenario {
			case "operation-key":
				installer.Record.Operations[len(installer.Record.Operations)-1].Key = "foreign-key"
			case "plan-hash":
				installer.Record.Operations[len(installer.Record.Operations)-1].Plan.Hash = "foreign-plan"
			case "preparation-proof":
				installer.Record.Operations[len(installer.Record.Operations)-1].Plan.Scope.Preparation = nil
			case "missing-operation":
				operation.ID = "unrecorded-operation"
			case "missing-job", "changed-claim":
				adoptStore(t, installer, func(store storage.Store) error {
					repository := maintenance.Repository{Store: store}
					owner, document, err := repository.Installation(t.Context())
					if err != nil {
						return err
					}
					if scenario == "changed-claim" {
						owner.OperationID = "foreign-active-writer"
						document.Payload = mustPreparationJSON(owner)
						return store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: document, Expected: document.Revision}})
					}
					job, err := store.GetControlDocument(t.Context(), "installation-job:"+owner.OperationID)
					if err != nil {
						return err
					}
					return store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: job, Expected: job.Revision, Remove: true}})
				})
			case "changed-node":
				host.engineID = "foreign-engine"
			case "changed-service":
				service := host.cluster.services[installer.Config.Adoption.ServiceIDs["nginx"]]
				service.Spec.TaskTemplate.ContainerSpec.Env = append(service.Spec.TaskTemplate.ContainerSpec.Env, "EXTERNAL_CHANGE=preserve")
				host.cluster.services[service.ID] = service
			case "changed-files":
				host.prepared["controller.yaml"] = append(host.prepared["controller.yaml"], []byte("\n# external change\n")...)
			}
			before := bootstrap.Digest(host.cluster.services)
			if _, err := installer.ResumeMaintenance(t.Context(), operation.ID); err == nil {
				t.Fatal("an unowned or drifted continuation was accepted")
			}
			if bootstrap.Digest(host.cluster.services) != before {
				t.Fatal("a rejected continuation changed services")
			}
		})
	}
}

func TestPreparationCreationRepliesRequireTheActualReturnedIDBeforeResume(t *testing.T) {
	for _, component := range []string{"secret", "control", "executor"} {
		for _, reply := range []string{"partial-id", "no-id", "foreign-receipt", "foreign-kind"} {
			t.Run(component+"/"+reply, func(t *testing.T) {
				installer, host, _ := webPreparationFixture(t)
				plan, err := installer.Plan(t.Context(), bootstrap.Upgrade)
				if err != nil {
					t.Fatal(err)
				}
				expectedPath := map[string]string{"secret": "/secrets/create", "control": "/configs/create", "executor": "/services/create"}[component]
				fired := false
				installer.Hosts["one"] = interceptedHost{base: host, intercept: func(ctx context.Context, method, path string, input, output any) (bool, error) {
					if fired || method != "POST" || path != expectedPath {
						return false, nil
					}
					fired = true
					var discarded map[string]any
					target := output
					if reply == "no-id" {
						target = &discarded
					}
					if err := host.Engine(ctx, method, path, input, target); err != nil {
						return true, err
					}
					if reply == "foreign-receipt" {
						data, _ := json.Marshal(output)
						var returned struct{ ID string }
						if err := json.Unmarshal(data, &returned); err != nil {
							t.Fatal(err)
						}
						switch component {
						case "secret":
							host.cluster.secrets[returned.ID]["Spec"].(map[string]any)["Labels"] = map[string]string{"io.nsc.installation": "foreign-installation"}
						case "control":
							host.cluster.configs[returned.ID]["Spec"].(map[string]any)["Labels"] = map[string]string{"io.nsc.installation": "foreign-installation"}
						case "executor":
							value := host.cluster.services[returned.ID]
							value.Spec.Name = "external-change"
							host.cluster.services[returned.ID] = value
						}
					}
					return true, context.DeadlineExceeded
				}}
				database := installer.Record.Database
				operation, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "interrupted-creation")
				if err == nil || !fired || operation.State != bootstrap.Unknown {
					t.Fatal("unacknowledged creation claimed success", operation.State, err)
				}
				if reply == "foreign-kind" {
					for index := range installer.Record.Operations[len(installer.Record.Operations)-1].Plan.Steps {
						step := &installer.Record.Operations[len(installer.Record.Operations)-1].Plan.Steps[index]
						if step.Key == "prepare:"+component && step.Receipt != nil {
							step.Receipt.Kind = "unrelated-kind"
						}
					}
					if err := installer.Journal.Save(installer.Record); err != nil {
						t.Fatal(err)
					}
				}
				before := bootstrap.Digest(host.cluster.services)
				secretCount, configCount := len(host.cluster.secrets), len(host.cluster.configs)
				resumed, err := installer.ResumeMaintenance(t.Context(), operation.ID)
				if reply == "partial-id" {
					if err != nil || resumed.State != bootstrap.Succeeded {
						t.Fatal("returned exact resource ID could not reconcile", resumed.State, err)
					}
				} else {
					if err == nil || resumed.State == bootstrap.Succeeded || bootstrap.Digest(host.cluster.services) != before || len(host.cluster.secrets) != secretCount || len(host.cluster.configs) != configCount {
						t.Fatal("an unconfirmed creation was adopted or duplicated", resumed.State, err)
					}
				}
				if installer.Record.Database != database {
					t.Fatal("creation recovery replaced the data pin")
				}
			})
		}
	}
}
