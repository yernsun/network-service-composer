package execution

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// An independent current-state view lets each case change one authority while
// retaining the same frozen intent, rather than minting a new trusted request.
type authorityView struct {
	storage.Store
	op                                                 model.DeploymentOperation
	runtime                                            model.Runtime
	app                                                model.Application
	missingTask, missingRuntime, held, projectDeleting bool
	documents                                          map[string]storage.ControlDocument
}

func (s *authorityView) GetDeploymentOperation(context.Context, string) (model.DeploymentOperation, error) {
	if s.missingTask {
		return model.DeploymentOperation{}, storage.ErrNotFound
	}
	return s.op, nil
}
func (s *authorityView) GetRuntime(context.Context, string) (model.Runtime, error) {
	if s.missingRuntime {
		return model.Runtime{}, storage.ErrNotFound
	}
	return s.runtime, nil
}
func (s *authorityView) GetApplication(context.Context, string) (model.Application, error) {
	if s.app.ID == "" {
		return s.app, storage.ErrNotFound
	}
	return s.app, nil
}
func (s *authorityView) CheckProjectResource(context.Context, string) error {
	if s.projectDeleting {
		return storage.ErrConflict
	}
	return nil
}
func (s *authorityView) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if s.held && key == storage.RecoveryHoldID("deployment", s.op.ID) {
		return storage.ControlDocument{ID: key, Kind: "recovery-hold", Payload: []byte(`{}`)}, nil
	}
	if d, ok := s.documents[key]; ok {
		if d.ID == "" {
			return d, storage.ErrNotFound
		}
		return d, nil
	}
	return s.Store.GetControlDocument(ctx, key)
}

func TestClaimAuthorityIsIndependentlyRequiredForFrozenTask(t *testing.T) {
	f := newExecutionFixture(t)
	cases := []string{"valid", "recovery-mode", "management-off", "no-leader", "no-clock", "no-session-check", "empty-leader", "empty-term", "empty-attempt", "recovery-hold", "missing-task", "terminal-task", "foreign-worker", "foreign-application", "foreign-service", "foreign-revision", "foreign-type", "missing-runtime", "runtime-off", "runtime-management-off", "physical-rebind", "legacy-no-pool", "legacy-no-type", "foreign-node", "missing-engine", "replaced-engine", "changed-cluster", "short-session-key", "missing-keyring", "corrupt-ciphertext", "future-heartbeat", "missing-heartbeat", "clock-unavailable", "zero-lock-owner", "display-name-change", "application-valid", "application-missing", "application-external", "application-system", "application-other-runtime", "application-other-namespace", "project-deleting", "application-lock-lost", "volume-delete", "config-create", "config-delete"}
	for _, scenario := range cases {
		t.Run(scenario, func(t *testing.T) {
			v := *f.v
			v.Config.Controller.Nodes = slices.Clone(f.v.Config.Controller.Nodes)
			view := &authorityView{Store: f.store, op: f.op, runtime: f.rt}
			v.Store = view
			ref := f.ref
			ref.Locks = slices.Clone(f.ref.Locks)
			wantValid := scenario == "valid" || scenario == "display-name-change" || scenario == "application-valid" || scenario == "volume-delete" || scenario == "config-create" || scenario == "config-delete"
			if strings.HasPrefix(scenario, "application") || scenario == "project-deleting" {
				view.app = model.Application{ID: "owned-app", RuntimeID: f.rt.ID, ProjectName: "owned-namespace", Managed: true}
				view.op.ApplicationID, view.op.Type = "owned-app", "INSTALL"
				ref.ApplicationID, ref.Namespace, ref.Type = "owned-app", "owned-namespace", "INSTALL"
				ref.Locks = append(ref.Locks, LockProof{Key: "deployment:runtime:owned-namespace", Owner: 11})
				f.locks.set("deployment:runtime:owned-namespace", 11)
			}
			switch scenario {
			case "recovery-mode":
				v.Config.Controller.RecoveryMode = true
			case "management-off":
				v.Config.Management.Enabled = false
			case "no-leader":
				v.Leader = nil
			case "no-clock":
				v.Now = nil
			case "no-session-check":
				v.CheckOwner = nil
			case "empty-leader":
				ref.LeaderID = ""
			case "empty-term":
				ref.LeaderTerm = 0
			case "empty-attempt":
				ref.Attempt = 0
			case "recovery-hold":
				view.held = true
			case "missing-task":
				view.missingTask = true
			case "terminal-task":
				view.op.Status = model.OperationSucceeded
			case "foreign-worker":
				view.op.WorkerID = "foreign"
			case "foreign-application":
				ref.ApplicationID = "foreign"
			case "foreign-service":
				ref.ServiceID = "foreign"
			case "foreign-revision":
				ref.RevisionID = "foreign"
			case "foreign-type":
				ref.Type = "VOLUME_DELETE"
			case "missing-runtime":
				view.missingRuntime = true
			case "runtime-off":
				view.runtime.Enabled = false
			case "runtime-management-off":
				view.runtime.ManagementEnabled = false
			case "physical-rebind":
				view.runtime.Endpoint = "node://node-b"
			case "legacy-no-pool":
				view.op.ExecutionIdentity.PoolID = ""
			case "legacy-no-type":
				view.op.ExecutionIdentity.Type = ""
			case "foreign-node":
				view.op.ExecutionIdentity.NodeID = "node-b"
			case "missing-engine":
				view.op.ExecutionIdentity.EngineID = ""
			case "replaced-engine":
				view.op.ExecutionIdentity.EngineID = "replacement"
			case "changed-cluster":
				view.runtime.ClusterID = "foreign"
				view.op.ExecutionIdentity.ClusterID = "foreign"
				ref.RuntimeFingerprint = RuntimeFingerprint(view.runtime)
			case "short-session-key":
				v.Config.Security.SessionKey = "short"
			case "missing-keyring":
				v.Keys = nil
			case "corrupt-ciphertext":
				view.op.EncryptedInput = []byte("corrupt")
			case "future-heartbeat":
				at := f.now.Add(time.Hour)
				view.op.HeartbeatAt = &at
			case "missing-heartbeat":
				view.op.HeartbeatAt = nil
			case "clock-unavailable":
				v.Now = func(context.Context) (time.Time, error) { return time.Time{}, errors.New("clock unavailable") }
			case "zero-lock-owner":
				ref.Locks[0].Owner = 0
			case "display-name-change":
				view.runtime.Name = "renamed display label"
				view.runtime.Revision++
			case "application-missing":
				view.app.ID = ""
			case "application-external":
				view.app.Managed = false
			case "application-system":
				view.app.System = true
			case "application-other-runtime":
				view.app.RuntimeID = "foreign"
			case "application-other-namespace":
				view.app.ProjectName = "foreign"
			case "project-deleting":
				view.projectDeleting = true
			case "application-lock-lost":
				ref.Locks[1].Owner = 12
			case "volume-delete":
				view.op.Type = "VOLUME_DELETE"
				ref.Type = view.op.Type
			case "config-create", "config-delete":
				view.runtime.Type, view.runtime.ClusterID = model.RuntimeSwarm, "cluster-one"
				view.op.ExecutionIdentity.Type, view.op.ExecutionIdentity.ClusterID = model.RuntimeSwarm, "cluster-one"
				ref.RuntimeFingerprint = RuntimeFingerprint(view.runtime)
				view.op.Type = "DOCKER_RESOURCE_CREATE"
				if scenario == "config-delete" {
					view.op.Type = "DOCKER_RESOURCE_DELETE"
				}
				ref.Type = view.op.Type
				ref.Locks = []LockProof{{Key: "docker-resource:cluster-one", Owner: 11}}
				f.locks.set("docker-resource:cluster-one", 11)
			}
			_, _, err := v.CheckClaim(t.Context(), ref)
			if wantValid && err != nil {
				t.Fatal("valid unchanged intent rejected", err)
			}
			if !wantValid && !errors.Is(err, ErrFenced) {
				t.Fatal("lost or forged authority accepted", err)
			}
		})
	}
}

func TestFrozenRuntimeAndInputKeepPhysicalAndSecretIdentity(t *testing.T) {
	f := newExecutionFixture(t)
	for _, scenario := range []string{"ordinary-endpoint", "missing-binding", "wrong-cluster", "no-input", "missing-session-key"} {
		t.Run(scenario, func(t *testing.T) {
			v := *f.v
			op, rt := f.op, f.rt
			switch scenario {
			case "ordinary-endpoint":
				rt.Endpoint = "unix:///fixture/docker.sock"
				op.ExecutionIdentity.Endpoint = rt.Endpoint
				if _, err := v.FrozenRuntime(op, rt); err != nil {
					t.Fatal(err)
				}
			case "missing-binding":
				v.Config.Controller.Nodes = nil
				if _, err := v.FrozenRuntime(op, rt); !errors.Is(err, ErrFenced) {
					t.Fatal(err)
				}
			case "wrong-cluster":
				op.ExecutionIdentity.ClusterID = "foreign"
				rt.ClusterID = "foreign"
				if _, err := v.FrozenRuntime(op, rt); !errors.Is(err, ErrFenced) {
					t.Fatal(err)
				}
			case "no-input":
				op.EncryptedInput = nil
				digest, err := v.InputDigest(op)
				if err != nil || digest == f.ref.InputSHA256 {
					t.Fatal("empty intent reused another input MAC", err)
				}
			case "missing-session-key":
				v.Config.Security.SessionKey = ""
				if _, err := v.InputDigest(op); !errors.Is(err, ErrFenced) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestExecutionCapabilityCannotBeUsedAcrossResourcesOrAfterLeaseLoss(t *testing.T) {
	f := newExecutionFixture(t)
	record := Record{Reference: f.ref, State: "RUNNING", ExecutionOwner: 77, SelectedAt: f.now}
	f.locks.set(ExecutionLock(f.op.ID, f.op.Attempt), 77)
	if err := Save(t.Context(), f.store, record, f.now); err != nil {
		t.Fatal(err)
	}
	ctx := WithRecord(t.Context(), f.v, record)
	if target, bound, err := BoundTarget(ctx, f.op.ID, f.rt.ID); err != nil || !bound || target.NodeID != "node-a" {
		t.Fatal(target, bound, err)
	}
	for _, pair := range [][2]string{{"foreign", f.rt.ID}, {f.op.ID, "foreign"}} {
		if err := BeforeMutation(ctx, pair[0], pair[1]); !errors.Is(err, ErrFenced) {
			t.Fatal("capability crossed resource", err)
		}
	}
	if started, bound, err := MutationStarted(ctx); started || !bound || err != nil {
		t.Fatal(started, bound, err)
	}
	if err := BeforeMutation(ctx, f.op.ID, f.rt.ID); err != nil {
		t.Fatal(err)
	}
	if err := BeforeMutation(ctx, f.op.ID, f.rt.ID); err != nil {
		t.Fatal("repeated write lost its existing marker", err)
	}
	if started, bound, err := MutationStarted(ctx); !started || !bound || err != nil {
		t.Fatal(started, bound, err)
	}
	f.locks.set(ExecutionLock(f.op.ID, f.op.Attempt), 0)
	if err := BeforeMutation(ctx, f.op.ID, f.rt.ID); !errors.Is(err, ErrFenced) {
		t.Fatal("lost executor lease allowed mutation", err)
	}
	if err := CheckContext(context.Background()); !errors.Is(err, ErrFenced) {
		t.Fatal("unbound executor accepted", err)
	}
	if err := MarkCommand(context.Background()); !errors.Is(err, ErrFenced) {
		t.Fatal("unbound mutation accepted", err)
	}
	if started, bound, err := MutationStarted(context.Background()); started || bound || err != nil {
		t.Fatal(started, bound, err)
	}
	if err := BeforeMutation(context.Background(), "legacy", "direct"); err != nil {
		t.Fatal("legacy direct-engine operation acquired a distributed capability", err)
	}
	for _, scenario := range []string{"kind", "parent", "malformed", "operation", "attempt"} {
		t.Run(scenario, func(t *testing.T) {
			doc, err := f.store.GetControlDocument(t.Context(), RecordID(f.op.ID, f.op.Attempt))
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "kind":
				doc.Kind = "foreign"
			case "parent":
				doc.ParentID = "foreign"
			case "malformed":
				doc.Payload = []byte(`{}`)
			case "operation", "attempt":
				copy := record
				if scenario == "operation" {
					copy.Reference.OperationID = "foreign"
				} else {
					copy.Reference.Attempt++
				}
				doc.Payload, _ = json.Marshal(copy)
			}
			v := *f.v
			v.Store = &authorityView{Store: f.store, documents: map[string]storage.ControlDocument{doc.ID: doc}}
			if _, err = Load(t.Context(), v.Store, f.op.ID, f.op.Attempt); !errors.Is(err, ErrFenced) {
				t.Fatal("malformed durable authority accepted", scenario, err)
			}
		})
	}
}
