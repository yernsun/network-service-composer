package recovery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/projectlife"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// These reader fixtures model incomplete or changed restored ownership, rather
// than bypassing the public reconciliation boundary with a fabricated receipt.
type restoredOwnershipStore struct {
	storage.Store
	runtime                         model.Runtime
	app                             model.Application
	services                        []model.Service
	operation                       *model.DeploymentOperation
	runtimeErr, appErr, servicesErr error
}

func (s restoredOwnershipStore) GetDeploymentOperation(ctx context.Context, key string) (model.DeploymentOperation, error) {
	if s.operation != nil {
		return *s.operation, nil
	}
	return s.Store.GetDeploymentOperation(ctx, key)
}

func (s restoredOwnershipStore) GetRuntime(context.Context, string) (model.Runtime, error) {
	return s.runtime, s.runtimeErr
}
func (s restoredOwnershipStore) GetApplication(context.Context, string) (model.Application, error) {
	return s.app, s.appErr
}
func (s restoredOwnershipStore) ListServices(context.Context, storage.Page) ([]model.Service, error) {
	return s.services, s.servicesErr
}

func TestReconciliationRejectsEveryUnverifiedRestoredOwnershipBoundary(t *testing.T) {
	for _, scenario := range []string{"runtime disabled", "runtime management disabled", "application unmanaged", "application on another runtime", "application project missing", "service unmanaged", "service on another runtime", "service outside project", "no services", "operation revision stale", "input digest changed", "wrong target kind", "queued target", "missing original engine", "target missing", "runtime evidence unavailable", "application evidence unavailable", "service evidence unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			store, plan, reader, _ := runningApplicationFixture(t)
			ctx := context.Background()
			item := plan.Items[0]
			op, err := store.GetDeploymentOperation(ctx, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			rt, err := store.GetRuntime(ctx, op.RuntimeID)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.GetApplication(ctx, op.ApplicationID)
			if err != nil {
				t.Fatal(err)
			}
			services, err := store.ListServices(ctx, storage.Page{Limit: 200})
			if err != nil {
				t.Fatal(err)
			}
			view := restoredOwnershipStore{Store: store, runtime: rt, app: app, services: services}
			expected := RuntimeIdentity{EngineID: "original-engine"}
			switch scenario {
			case "runtime disabled":
				view.runtime.Enabled = false
			case "runtime management disabled":
				view.runtime.ManagementEnabled = false
			case "application unmanaged":
				view.app.Managed = false
			case "application on another runtime":
				view.app.RuntimeID = "another-runtime"
			case "application project missing":
				view.app.ProjectName = ""
			case "service unmanaged":
				view.services[0].Managed = false
			case "service on another runtime":
				view.services[0].RuntimeID = "another-runtime"
			case "service outside project":
				view.services[0].ServiceKey = "another-project/web"
			case "no services":
				view.services = nil
			case "operation revision stale":
				item.Revision--
			case "input digest changed":
				item.InputDigest = "changed-input"
			case "wrong target kind":
				item.Kind = "certificate"
			case "queued target":
				item.State = "QUEUED"
			case "missing original engine":
				expected.EngineID = ""
			case "target missing":
				item.ID = "missing-operation"
			case "runtime evidence unavailable":
				view.runtimeErr = storage.ErrNotFound
			case "application evidence unavailable":
				view.appErr = storage.ErrNotFound
			case "service evidence unavailable":
				view.servicesErr = errors.New("restored service inventory unavailable")
			}
			reader.Store = view
			if proof, err := reader.Capture(ctx, item, expected); err == nil || proof != nil {
				t.Fatal("unverified restored target produced a usable receipt", scenario)
			}
			preserved, err := store.GetDeploymentOperation(ctx, op.ID)
			if err != nil || preserved.Revision != op.Revision || preserved.Status != op.Status || string(preserved.EncryptedInput) != string(op.EncryptedInput) {
				t.Fatal("ownership refusal changed original evidence", err)
			}
		})
	}
}

func TestReconciliationRejectsIdentityLossDuringReadOnlyObservation(t *testing.T) {
	for _, offline := range []bool{false, true} {
		store, plan, reader, _ := runningApplicationFixture(t)
		reads := 0
		reader.ReadInfo = func(context.Context, model.Runtime) (system.Info, error) {
			reads++
			if reads == 1 {
				return system.Info{ID: "original-engine"}, nil
			}
			if offline {
				return system.Info{}, errors.New("engine disconnected during observation")
			}
			return system.Info{ID: "replacement-engine"}, nil
		}
		if _, err := reader.Capture(context.Background(), plan.Items[0], RuntimeIdentity{EngineID: "original-engine"}); err == nil {
			t.Fatal("identity loss during observation produced a receipt")
		}
		if held, err := storage.RecoveryHeld(context.Background(), store, "deployment", plan.Items[0].ID); err != nil || !held {
			t.Fatal("read failure cleared hold", err)
		}
	}
}

func TestReconciliationCanonicalizesEquivalentOwnedRuntimeEvidence(t *testing.T) {
	_, plan, reader, state := runningApplicationFixture(t)
	state.Objects = append(state.Objects, state.Objects[0])
	state.Objects[1].ID = "second-owned-container"
	state.Nodes = []string{"worker-b", "worker-a"}
	state.Preserved = []string{"volume:b", "volume:a"}
	proof, err := reader.Capture(context.Background(), plan.Items[0], RuntimeIdentity{EngineID: "original-engine"})
	if err != nil {
		t.Fatal(err)
	}
	// A later SDK observation owns new slices; it does not edit the receipt's
	// already captured object list through a shared fixture backing array.
	state.Objects = append([]projectlife.Object(nil), state.Objects...)
	state.Objects[0], state.Objects[1] = state.Objects[1], state.Objects[0]
	state.Nodes[0], state.Nodes[1] = state.Nodes[1], state.Nodes[0]
	state.Preserved[0], state.Preserved[1] = state.Preserved[1], state.Preserved[0]
	state.ObservedAt = state.ObservedAt.Add(time.Millisecond)
	if err = reader.Validate(context.Background(), plan.Items[0], proof); err != nil {
		t.Fatal("equivalent inventory ordering invalidated fresh evidence", err)
	}
}

func TestReconciliationSwarmRequiresOriginalClusterAndVersionedOwnedObjects(t *testing.T) {
	for _, scenario := range []string{"valid", "missing runtime cluster", "wrong runtime cluster", "unversioned service"} {
		t.Run(scenario, func(t *testing.T) {
			store, plan, reader, state := runningApplicationFixture(t)
			ctx := context.Background()
			op, err := store.GetDeploymentOperation(ctx, plan.Items[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			rt, err := store.GetRuntime(ctx, op.RuntimeID)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.GetApplication(ctx, op.ApplicationID)
			if err != nil {
				t.Fatal(err)
			}
			services, err := store.ListServices(ctx, storage.Page{Limit: 200})
			if err != nil {
				t.Fatal(err)
			}
			// SQLite remains a Compose store. These explicit reader metadata
			// fixtures exercise the Swarm reconciliation contract, not SQLite support.
			rt.Type, rt.ClusterID = model.RuntimeSwarm, "original-cluster"
			op.ExecutionIdentity = model.RuntimeExecutionIdentity{PoolID: rt.PoolID, Type: rt.Type, Endpoint: rt.Endpoint, CredentialRef: rt.CredentialRef, ClusterID: rt.ClusterID}
			services[0].ServiceKey = app.ProjectName + "_web"
			state.Objects[0].Kind, state.Objects[0].Version = "service", 9
			state.Retained = []projectlife.RetainedVolume{{NodeID: "worker-b", Name: "data-b", Target: "/b", CreatedAt: state.ObservedAt}, {NodeID: "worker-a", Name: "data-a", Target: "/a", CreatedAt: state.ObservedAt}}
			switch scenario {
			case "missing runtime cluster":
				rt.ClusterID = ""
			case "wrong runtime cluster":
				rt.ClusterID = "other-cluster"
			case "unversioned service":
				state.Objects[0].Version = 0
			}
			reader.Store = restoredOwnershipStore{Store: store, runtime: rt, app: app, services: services, operation: &op}
			reader.ReadInfo = func(context.Context, model.Runtime) (system.Info, error) {
				return system.Info{ID: "original-engine", Swarm: swarm.Info{NodeID: "original-node", ControlAvailable: true, Cluster: &swarm.ClusterInfo{ID: "original-cluster"}}}, nil
			}
			proof, err := reader.Capture(ctx, plan.Items[0], RuntimeIdentity{EngineID: "original-engine", NodeID: "original-node", ClusterID: "original-cluster"})
			if scenario != "valid" {
				if err == nil || proof != nil {
					t.Fatal("ambiguous Swarm target produced release evidence", scenario)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			state.Retained[0], state.Retained[1] = state.Retained[1], state.Retained[0]
			if err = reader.Validate(ctx, plan.Items[0], proof); err != nil {
				t.Fatal("equivalent retained storage ordering invalidated evidence", err)
			}
		})
	}
}

type interruptedHoldReader struct {
	storage.Store
	cancel context.CancelFunc
}

func (s interruptedHoldReader) GetDeploymentOperation(ctx context.Context, id string) (model.DeploymentOperation, error) {
	value, err := s.Store.GetDeploymentOperation(ctx, id)
	s.cancel()
	return value, err
}

func TestReconciliationCannotInferPermissionFromMissingOrUnavailableHold(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		store, plan, reader, _ := runningApplicationFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if unavailable {
			reader.Store = interruptedHoldReader{Store: store, cancel: cancel}
		} else {
			for _, key := range []string{storage.RecoveryBarrierID, storage.RecoveryHoldID("deployment", plan.Items[0].ID)} {
				doc, err := store.GetControlDocument(ctx, key)
				if err != nil {
					t.Fatal(err)
				}
				if err = store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: doc, Expected: doc.Revision, Remove: true}}); err != nil {
					t.Fatal(err)
				}
			}
		}
		if proof, err := reader.Capture(ctx, plan.Items[0], RuntimeIdentity{EngineID: "original-engine"}); err == nil || proof != nil {
			t.Fatal("unknown release permission produced a receipt", unavailable)
		}
		preserved, err := store.GetDeploymentOperation(context.Background(), plan.Items[0].ID)
		if err != nil || preserved.Status != model.OperationRunning || preserved.Revision != plan.Items[0].Revision {
			t.Fatal("hold refusal rewrote original operation evidence", err)
		}
	}
}

func TestReconciliationDoesNotConvertStandaloneInterruptedResourceCommandsIntoApplicationRecovery(t *testing.T) {
	store, ring, rt := recoveryFixture(t)
	ctx, now := context.Background(), time.Now().UTC()
	op := recoveryOperation(t, store, ring, rt, now)
	claimed, err := store.ClaimDeploymentOperation(ctx, "old-resource-owner", now, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Prepare(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	reader := LiveReconciler{Store: store, Keys: ring}
	if proof, err := reader.Capture(ctx, plan.Items[0], RuntimeIdentity{EngineID: "original-engine"}); err == nil || proof != nil {
		t.Fatal("standalone resource replay was approved as an Application")
	}
	preserved, err := store.GetDeploymentOperation(ctx, op.ID)
	if err != nil || preserved.Status != model.OperationRunning || preserved.Attempt != claimed.Attempt || preserved.Revision != claimed.Revision {
		t.Fatal("unsupported resource outcome was guessed", err)
	}
}
