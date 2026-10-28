package execution

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/hoststats"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// The authority checks below run against real advisory/named session locks and
// the same SQL transaction used for task completion. No Docker socket is used.
func TestExecutionAcrossDatabases(t *testing.T) {
	for _, driver := range []string{"postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			dsn := os.Getenv(map[string]string{"postgres": "NSC_TEST_EXECUTION_POSTGRES_DSN", "mysql": "NSC_TEST_EXECUTION_MYSQL_DSN"}[driver])
			if dsn == "" {
				t.Skip("isolated external store not configured")
			}
			for _, scenario := range []string{"complete", "leader-changed", "attempt-changed", "management-disabled", "collector-generation", "resource-lease-lost", "recovery-held", "record-replaced"} {
				t.Run(scenario, func(t *testing.T) {
					store, err := storage.Open(driver, dsn)
					if err != nil {
						t.Fatal(err)
					}
					defer store.Close()
					store.DB().SetMaxOpenConns(16)
					if err = store.Migrate(t.Context()); err != nil {
						t.Fatal(err)
					}
					prefix := id.New("exec")
					cfg := config.Defaults()
					cfg.Storage.Driver = driver
					cfg.Management.Enabled = true
					cfg.Management.DeployTimeout = config.Duration(20 * time.Second)
					cfg.Security.SessionKey = "isolated-sql-execution-session-key-32-bytes"
					cfg.Controller.InstanceID = prefix + "-controller"
					node, engine, cluster := prefix+"-node", prefix+"-engine", prefix+"-cluster"
					cfg.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: cfg.Controller.InstanceID, NodeID: node, EngineID: engine, ClusterID: cluster, URL: "https://controller.example.test:8443"}}
					ring, err := security.NewKeyring("fixture", map[string]string{"fixture": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
					if err != nil {
						t.Fatal(err)
					}
					locker := lease.New(store.DB(), store.Dialect())
					leaderLock, err := locker.TryLock(t.Context(), "controller:leader")
					if err != nil {
						t.Fatal(err)
					}
					defer leaderLock.Release(context.Background())
					leaderOwner, err := leaderLock.(lease.SessionLease).SessionID(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					state := controlplane.SQLState{Store: store}
					term, err := state.Claim(t.Context(), controlplane.Identity{ID: cfg.Controller.InstanceID, LockOwner: leaderOwner, URL: "https://controller.example.test:8443"}, time.Minute)
					if err != nil {
						t.Fatal(err)
					}
					now, err := state.DatabaseTime(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					pool := model.GatewayPool{ID: prefix + "-pool", Name: prefix, BackendNetwork: prefix, ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
					if err = store.CreatePool(t.Context(), &pool); err != nil {
						t.Fatal(err)
					}
					rt := model.Runtime{ID: prefix + "-runtime", PoolID: pool.ID, Name: prefix, Endpoint: "node://" + node, Type: model.RuntimeDocker, Enabled: true, ManagementEnabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
					if err = store.CreateRuntime(t.Context(), &rt); err != nil {
						t.Fatal(err)
					}
					store.ConfigureOperationNodes(map[string]model.RuntimeExecutionIdentity{rt.Endpoint: {NodeID: node, EngineID: engine}})
					collectorLock, err := locker.TryLock(t.Context(), nodecontrol.LockKey(node))
					if err != nil {
						t.Fatal(err)
					}
					defer collectorLock.Release(context.Background())
					collectorOwner, err := collectorLock.(lease.SessionLease).SessionID(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					reports := &nodecontrol.Reports{Store: store, Keys: ring, Bindings: map[string]nodecontrol.Binding{cfg.Controller.InstanceID: {InstanceID: cfg.Controller.InstanceID, NodeID: node, EngineID: engine, ClusterID: cluster}}, Now: state.DatabaseTime, CheckOwner: func(ctx context.Context, key string, owner int64) error {
						return lease.CheckOwner(ctx, store.DB(), store.Dialect(), key, owner)
					}}
					session, err := reports.Begin(t.Context(), cfg.Controller.InstanceID, node, engine, "first-boot", collectorOwner)
					if err != nil {
						t.Fatal(err)
					}
					if err = reports.Accept(t.Context(), cfg.Controller.InstanceID, nodecontrol.Report{NodeID: node, EngineID: engine, Generation: session.Generation, Sequence: 1, Observation: nodecontrol.Observation{Health: &hoststats.Health{InstanceID: cfg.Controller.InstanceID, Execution: "enabled"}, Info: system.Info{ID: engine, Swarm: swarm.Info{NodeID: node}}, Complete: true, At: now}}); err != nil {
						t.Fatal(err)
					}
					v, err := NewSQLVerifier(store, cfg, reports, ring)
					if err != nil {
						t.Fatal(err)
					}
					op := model.DeploymentOperation{ID: prefix + "-operation", RuntimeID: rt.ID, Type: "VOLUME_CREATE", Status: model.OperationQueued, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
					op.EncryptedInput, err = ring.Encrypt([]byte(`{"name":"fixture-volume","private":"fixture-value"}`), []byte("deployment-operation:"+op.ID))
					if err != nil {
						t.Fatal(err)
					}
					op.EncryptionKeyID = ring.PrimaryID()
					if _, err = store.CreateDeploymentOperation(t.Context(), &op); err != nil {
						t.Fatal(err)
					}
					op, err = store.ClaimDeploymentOperation(t.Context(), cfg.Controller.InstanceID, now, now.Add(-time.Minute))
					if err != nil {
						t.Fatal(err)
					}
					resourceLock, err := locker.TryLock(t.Context(), StorageLock(rt))
					if err != nil {
						t.Fatal(err)
					}
					defer resourceLock.Release(context.Background())
					resourceOwner, err := resourceLock.(lease.SessionLease).SessionID(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					target, err := v.Select(t.Context(), rt)
					if err != nil {
						t.Fatal(err)
					}
					digest, err := v.InputDigest(op)
					if err != nil {
						t.Fatal(err)
					}
					record := Record{Reference: Reference{OperationID: op.ID, Attempt: op.Attempt, LeaderID: cfg.Controller.InstanceID, LeaderTerm: term.Term, RuntimeID: rt.ID, RuntimeFingerprint: RuntimeFingerprint(rt), Type: op.Type, InputSHA256: digest, Locks: []LockProof{{Key: StorageLock(rt), Owner: resourceOwner}}, Target: target}, State: "SELECTED", SelectedAt: now}
					if err = Save(t.Context(), store, record, now); err != nil {
						t.Fatal(err)
					}
					entered, release := make(chan struct{}), make(chan struct{})
					result := make(chan error, 1)
					service := &Service{Verifier: v, Locker: locker, Execute: func(ctx context.Context, task model.DeploymentOperation, _ model.Runtime) error {
						if err := MarkCommand(ctx); err != nil {
							result <- err
							close(entered)
							return err
						}
						close(entered)
						<-release
						task.Status = model.OperationSucceeded
						// A deliberately late response ignores cancellation. The SQL
						// fence must still reject its metadata commit.
						check, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
						defer cancel()
						err := store.FinishDeploymentOperation(check, &task, now)
						result <- err
						return err
					}}
					if err = service.Accept(t.Context(), t.Context(), cfg.Controller.InstanceID, Request{OperationID: op.ID, Attempt: op.Attempt, LeaderTerm: term.Term, Target: target.Identity()}); err != nil {
						t.Fatal(err)
					}
					select {
					case <-entered:
					case <-time.After(10 * time.Second):
						close(release)
						service.Wait()
						t.Fatal("executor did not reach command boundary")
					}
					switch scenario {
					case "leader-changed":
						if _, err = state.Claim(t.Context(), controlplane.Identity{ID: cfg.Controller.InstanceID + "-successor", LockOwner: leaderOwner}, time.Minute); err != nil {
							t.Fatal(err)
						}
					case "attempt-changed":
						_, err = store.DB().ExecContext(t.Context(), storage.BindSQL(store.Dialect(), `UPDATE deployment_operations SET attempt=attempt+1 WHERE id=:id`), op.ID)
					case "management-disabled":
						rt.ManagementEnabled = false
						err = store.UpdateRuntime(t.Context(), &rt, rt.Revision)
					case "collector-generation":
						_, err = reports.Begin(t.Context(), cfg.Controller.InstanceID, node, engine, "second-boot", collectorOwner)
					case "resource-lease-lost":
						err = resourceLock.Release(t.Context())
					case "recovery-held":
						err = store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: storage.ControlDocument{ID: storage.RecoveryHoldID("deployment", op.ID), Kind: "recovery-hold", Payload: []byte(`{}`), CreatedAt: now, UpdatedAt: now}}})
					case "record-replaced":
						current, e := Load(t.Context(), store, op.ID, op.Attempt)
						if e != nil {
							t.Fatal(e)
						}
						current.ExecutionOwner++
						err = Save(t.Context(), store, current, now)
					}
					if err != nil {
						close(release)
						service.Wait()
						t.Fatal(err)
					}
					close(release)
					service.Wait()
					err = <-result
					actual, e := store.GetDeploymentOperation(t.Context(), op.ID)
					if e != nil {
						t.Fatal(e)
					}
					if scenario == "complete" {
						if err != nil || actual.Status != model.OperationSucceeded {
							t.Fatal("valid transaction was fenced", actual.Status, err)
						}
					} else if !errors.Is(err, storage.ErrOperationOwnership) || actual.Status != model.OperationRunning || len(actual.EncryptedInput) == 0 {
						t.Fatal("late executor committed or erased retry evidence", actual.Status, err)
					}
					// Every row below is owned by this isolated run. Fixed authority
					// keys are removed only after checking the recorded identity.
					current, e := state.Current(t.Context())
					if e != nil || !strings.HasPrefix(current.ID, prefix) {
						t.Fatal("unexpected fixture leader", e)
					}
					doc, e := store.GetControlDocument(t.Context(), "controller:leader")
					if e != nil {
						t.Fatal(e)
					}
					if e = store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: doc, Expected: doc.Revision, Remove: true}}); e != nil {
						t.Fatal(e)
					}
					for _, kind := range []string{RecordKind, "node-session", "node-report", "recovery-hold", "operation-scope"} {
						docs, e := store.ListControlDocuments(t.Context(), kind, "", storage.Page{Limit: 200})
						if e != nil {
							t.Fatal(e)
						}
						for _, doc := range docs {
							if strings.Contains(doc.ID, prefix) || strings.Contains(doc.ParentID, prefix) {
								if e = store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: doc, Expected: doc.Revision, Remove: true}}); e != nil {
									t.Fatal(e)
								}
							}
						}
					}
					for _, table := range []string{"deployment_operations", "runtimes", "gateway_pools"} {
						if _, e = store.DB().ExecContext(t.Context(), storage.BindSQL(store.Dialect(), "DELETE FROM "+table+" WHERE id=:id"), map[string]string{"deployment_operations": op.ID, "runtimes": rt.ID, "gateway_pools": pool.ID}[table]); e != nil {
							t.Fatal(e)
						}
					}
					if _, e = json.Marshal(record); e != nil {
						t.Fatal(e)
					}
				})
			}
		})
	}
}
