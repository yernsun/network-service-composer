package recovery

import (
	"context"
	"encoding/json"
	"github.com/moby/moby/client"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestRecoveryIdentityTransactionRejectsRefreezeAndChangedPhysicalTarget(t *testing.T) {
	for _, scenario := range []string{"legacy-valid", "existing-frozen", "changed-target", "stale-revision", "legacy-node-with-offline-receipt"} {
		t.Run(scenario, func(t *testing.T) {
			store, plan, _, _ := runningApplicationFixture(t, func(s *storage.SQLStore, op model.DeploymentOperation) {
				if scenario != "existing-frozen" {
					if _, err := s.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET runtime_identity='' WHERE id=?`, op.ID); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "legacy-node-with-offline-receipt" {
					if _, err := s.DB().ExecContext(t.Context(), `UPDATE runtimes SET endpoint='node://original-node' WHERE id=?`, op.RuntimeID); err != nil {
						t.Fatal(err)
					}
				}
			})
			before, err := store.GetDeploymentOperation(t.Context(), plan.Items[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			rt, err := store.GetRuntime(t.Context(), before.RuntimeID)
			if err != nil {
				t.Fatal(err)
			}
			identity := model.RuntimeExecutionIdentity{PoolID: rt.PoolID, Type: rt.Type, Endpoint: rt.Endpoint, CredentialRef: rt.CredentialRef, ClusterID: rt.ClusterID}
			if scenario == "legacy-node-with-offline-receipt" {
				identity.NodeID, identity.EngineID = "original-node", "original-engine"
			}
			expected := before.Revision
			if scenario == "stale-revision" {
				expected--
			}
			if scenario == "changed-target" {
				identity.Endpoint = "node://different-engine"
			}
			review, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			err = store.ApplyRecoveryReviewWithIdentities(t.Context(), plan.BarrierRevision, plan.Fingerprint, []string{storage.RecoveryHoldID("deployment", before.ID)}, review, []storage.ReviewedExecutionIdentity{{OperationID: before.ID, ExpectedRevision: expected, Identity: identity}})
			after, readErr := store.GetDeploymentOperation(t.Context(), before.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			valid := scenario == "legacy-valid" || scenario == "legacy-node-with-offline-receipt"
			if valid {
				if err != nil || after.ExecutionIdentity != identity || after.Revision != before.Revision+1 {
					t.Fatal("reviewed legacy identity was not frozen", scenario, after.ExecutionIdentity, err)
				}
			} else {
				if err == nil || after.ExecutionIdentity != before.ExecutionIdentity || after.Revision != before.Revision {
					t.Fatal("failed review rewrote target", scenario, err)
				}
				if held, err := storage.RecoveryHeld(t.Context(), store, "deployment", before.ID); err != nil || !held {
					t.Fatal("failed identity review cleared hold", err)
				}
			}
		})
	}
}

func TestReviewedExecutionPreservesExistingIdentityAndFreezesLegacyOnlyOnce(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "frozen", true: "legacy"}[legacy], func(t *testing.T) {
			store, plan, reader, _ := runningApplicationFixture(t, func(s *storage.SQLStore, op model.DeploymentOperation) {
				if legacy {
					if _, err := s.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET runtime_identity='' WHERE id=?`, op.ID); err != nil {
						t.Fatal(err)
					}
				}
				now := time.Now().UTC()
				if err := execution.Save(t.Context(), s, execution.Record{Reference: execution.Reference{OperationID: op.ID, Attempt: op.Attempt}, State: "UNKNOWN", CommandStartedAt: &now, SelectedAt: now}, now); err != nil {
					t.Fatal(err)
				}
			})
			before, err := store.GetDeploymentOperation(t.Context(), plan.Items[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			proof, err := reader.Capture(t.Context(), plan.Items[0], RuntimeIdentity{EngineID: "original-engine"})
			if err != nil {
				t.Fatal(err)
			}
			if proof.Evidence.FrozenTarget.Endpoint == "" || proof.Evidence.FrozenTarget.PoolID == "" {
				t.Fatal("review did not bind physical intent", proof.Evidence.FrozenTarget)
			}
			plan.Items[0].Decision, plan.Items[0].Reconciliation = "reconcile", proof
			plan.ActivationReviewed = true
			if err = Release(t.Context(), store, plan, reader.Validate); err != nil {
				t.Fatal(err)
			}
			after, err := store.GetDeploymentOperation(t.Context(), before.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Status != model.OperationRunning || after.Attempt != before.Attempt || string(after.EncryptedInput) != string(before.EncryptedInput) {
				t.Fatal("review guessed execution or destroyed intent", after.Status)
			}
			if legacy {
				if after.ExecutionIdentity != proof.Evidence.FrozenTarget || after.Revision != before.Revision+1 {
					t.Fatal("legacy target was not atomically frozen", after.ExecutionIdentity)
				}
			} else if after.ExecutionIdentity != before.ExecutionIdentity || after.Revision != before.Revision {
				t.Fatal("existing frozen target changed")
			}
			record, err := execution.Load(t.Context(), store, before.ID, before.Attempt)
			if err != nil || record.State != "RECONCILED" || record.ReconciledAt == nil || record.CommandStartedAt == nil {
				t.Fatal("review did not separate acknowledgment from reconciliation", record.State, err)
			}
			if err = Release(t.Context(), store, plan, reader.Validate); err == nil {
				t.Fatal("review replay changed an already frozen task")
			}
		})
	}
}

func TestRecoveryCaptureKeepsOriginalBindingAcrossManagerInspection(t *testing.T) {
	for _, scenario := range []string{"compose-original", "compose-foreign", "compose-no-enrollment", "swarm-alternate", "swarm-foreign-cluster", "frozen-rebind"} {
		t.Run(scenario, func(t *testing.T) {
			store, plan, reader, state := runningApplicationFixture(t)
			op, err := store.GetDeploymentOperation(t.Context(), plan.Items[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			rt, err := store.GetRuntime(t.Context(), op.RuntimeID)
			if err != nil {
				t.Fatal(err)
			}
			services, err := store.ListServices(t.Context(), storage.Page{Limit: 200})
			if err != nil {
				t.Fatal(err)
			}
			rt.Endpoint, rt.ClusterID = "node://original-node", "cluster"
			op.ExecutionIdentity = model.RuntimeExecutionIdentity{}
			reader.Config.Controller.InstanceID = "alternate-controller"
			reader.Config.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: "original-controller", NodeID: "original-node", EngineID: "original-engine", ClusterID: "cluster"}, {InstanceID: "alternate-controller", NodeID: "alternate-node", EngineID: "alternate-engine", ClusterID: "cluster"}}
			expected := RuntimeIdentity{EngineID: "original-engine", NodeID: "original-node", ClusterID: "cluster"}
			if scenario == "compose-foreign" {
				expected.EngineID, expected.NodeID = "alternate-engine", "alternate-node"
			}
			if scenario == "compose-no-enrollment" {
				reader.Config.Controller.Nodes = nil
			}
			if scenario == "swarm-alternate" || scenario == "swarm-foreign-cluster" {
				rt.Type = model.RuntimeSwarm
				services[0].ServiceKey = "restore-app_web"
				state.Objects[0].Kind, state.Objects[0].Version = "service", 3
				expected.EngineID, expected.NodeID = "alternate-engine", "alternate-node"
				if scenario == "swarm-foreign-cluster" {
					reader.Config.Controller.Nodes[1].ClusterID = "foreign"
				}
			}
			if scenario == "frozen-rebind" {
				op.ExecutionIdentity = model.RuntimeExecutionIdentity{PoolID: rt.PoolID, Type: rt.Type, Endpoint: "node://former-node", ClusterID: rt.ClusterID}
			}
			reader.Store = &reconciliationRuntimeStore{Store: store, runtime: rt, operation: &op, services: services}
			observedEndpoint := ""
			reader.ReadInfo = func(_ context.Context, actual model.Runtime) (system.Info, error) {
				observedEndpoint = actual.Endpoint
				return system.Info{ID: expected.EngineID, Swarm: swarm.Info{NodeID: expected.NodeID, ControlAvailable: true, Cluster: &swarm.ClusterInfo{ID: expected.ClusterID}}}, nil
			}
			proof, err := reader.Capture(t.Context(), plan.Items[0], expected)
			if scenario == "compose-original" || scenario == "swarm-alternate" {
				if err != nil || proof.Evidence.FrozenTarget.Endpoint != "node://original-node" || proof.Evidence.FrozenTarget.NodeID != "original-node" || proof.Evidence.FrozenTarget.EngineID != "original-engine" {
					t.Fatal("inspection changed frozen execution host", err)
				}
				if scenario == "swarm-alternate" && observedEndpoint != "node://alternate-node" {
					t.Fatal("alternate verified Manager was not used", observedEndpoint)
				}
			} else if err == nil || proof != nil {
				t.Fatal("unreviewed physical change produced recovery authority", scenario)
			}
		})
	}
}

func TestRecoveryReleaseRejectsMissingLegacyIdentityAndChangedAttemptRecord(t *testing.T) {
	for _, scenario := range []string{"missing-legacy-proof", "missing-legacy-target", "record-corrupt", "record-without-proof"} {
		t.Run(scenario, func(t *testing.T) {
			store, plan, _, _ := runningApplicationFixture(t, func(s *storage.SQLStore, op model.DeploymentOperation) {
				if scenario == "missing-legacy-proof" || scenario == "missing-legacy-target" {
					if _, err := s.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET runtime_identity='' WHERE id=?`, op.ID); err != nil {
						t.Fatal(err)
					}
				} else {
					now := time.Now().UTC()
					if err := execution.Save(t.Context(), s, execution.Record{Reference: execution.Reference{OperationID: op.ID, Attempt: op.Attempt}, State: "UNKNOWN", SelectedAt: now}, now); err != nil {
						t.Fatal(err)
					}
					if scenario == "record-corrupt" {
						if _, err := s.DB().ExecContext(t.Context(), `UPDATE control_documents SET payload='{}' WHERE id=?`, execution.RecordID(op.ID, op.Attempt)); err != nil {
							t.Fatal(err)
						}
					}
				}
			})
			plan.ActivationReviewed = true
			plan.Items[0].Decision = "reconcile"
			if scenario == "missing-legacy-target" {
				plan.Items[0].Reconciliation = &Reconciliation{}
			}
			// Even an incorrectly permissive upper-level validator cannot bypass
			// the release transaction's required physical identity/attempt evidence.
			if err := Release(t.Context(), store, plan, func(context.Context, Item, *Reconciliation) error { return nil }); err == nil {
				t.Fatal("incomplete recovery authority released task", scenario)
			}
			if held, err := storage.RecoveryHeld(t.Context(), store, "deployment", plan.Items[0].ID); err != nil || !held {
				t.Fatal("failed review cleared hold", err)
			}
		})
	}
}

func TestRecoveryComposeWorkerMayHaveNoManagerClusterObject(t *testing.T) {
	cfg := config.Defaults()
	cfg.Controller.InstanceID = "worker-controller"
	cfg.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: cfg.Controller.InstanceID, NodeID: "worker", EngineID: "engine", ClusterID: "cluster"}}
	for _, kind := range []model.RuntimeType{model.RuntimeDocker, model.RuntimeSwarm} {
		t.Run(string(kind), func(t *testing.T) {
			rt := model.Runtime{Type: kind, Endpoint: "node://worker", ClusterID: "cluster"}
			reader := LiveReconciler{Config: cfg, Open: func(actual model.Runtime, _ engine.TLSFiles) (*client.Client, error) {
				if actual.Endpoint != "unix:///var/run/docker.sock" {
					t.Fatal("recovery opened foreign Engine", actual.Endpoint)
				}
				return client.New(client.WithHost("http://fixture.example"), client.WithVersion("1.53"), client.WithHTTPClient(&http.Client{Transport: recoveryInfoTransport(func(request *http.Request) (*http.Response, error) {
					if request.Method != "GET" || !strings.HasSuffix(request.URL.Path, "/info") {
						t.Fatal("recovery mutated worker", request.Method, request.URL.Path)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"ID":"engine","Swarm":{"NodeID":"worker"}}`))}, nil
				})}))
			}}
			info, err := reader.readInfo(t.Context(), rt)
			if kind == model.RuntimeDocker {
				if err != nil || info.ID != "engine" {
					t.Fatal("Compose required a Manager-only Cluster object", info, err)
				}
			} else if err == nil {
				t.Fatal("Swarm Manager accepted absent cluster evidence")
			}
		})
	}
}
