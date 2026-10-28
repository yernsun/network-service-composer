package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestOperationCapturesPhysicalIdentityAndNeverFollowsRuntimeEdits(t *testing.T) {
	s := newSQLiteStore(t)
	now := time.Now().UTC()
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "execution fixture", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.CreatePool(t.Context(), &pool); err != nil {
		t.Fatal(err)
	}
	rt := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "original engine", Type: model.RuntimeDocker, Endpoint: "node://node-a", Enabled: true, ManagementEnabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.CreateRuntime(t.Context(), &rt); err != nil {
		t.Fatal(err)
	}
	bindings := map[string]model.RuntimeExecutionIdentity{"node://node-a": {NodeID: "node-a", EngineID: "engine-a"}, "node://node-b": {NodeID: "node-b", EngineID: "engine-b"}}
	s.ConfigureOperationNodes(bindings)
	bindings["node://node-a"] = model.RuntimeExecutionIdentity{NodeID: "forged", EngineID: "forged"}
	create := func() model.DeploymentOperation {
		op := model.DeploymentOperation{ID: id.New(id.Operation), RuntimeID: rt.ID, Type: "VOLUME_CREATE", Status: model.OperationQueued, ExecutionIdentity: model.RuntimeExecutionIdentity{Endpoint: "node://forged", EngineID: "forged"}, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
		value, err := s.CreateDeploymentOperation(t.Context(), &op)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	first := create()
	if first.ExecutionIdentity.Endpoint != "node://node-a" || first.ExecutionIdentity.EngineID != "engine-a" || first.ExecutionIdentity.PoolID != rt.PoolID {
		t.Fatal("caller replaced frozen execution identity", first.ExecutionIdentity)
	}
	if _, err := s.DB().ExecContext(t.Context(), `UPDATE runtimes SET endpoint='node://node-b' WHERE id=?`, rt.ID); err != nil {
		t.Fatal(err)
	}
	second := create()
	if second.ExecutionIdentity.EngineID != "engine-b" {
		t.Fatal(second.ExecutionIdentity)
	}
	stored, err := s.GetDeploymentOperation(t.Context(), first.ID)
	if err != nil || stored.ExecutionIdentity != first.ExecutionIdentity {
		t.Fatal("existing task followed later Runtime configuration", stored.ExecutionIdentity, err)
	}
	if _, err = s.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET runtime_identity='' WHERE id=?`, first.ID); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.GetDeploymentOperation(t.Context(), first.ID)
	if err != nil || legacy.ExecutionIdentity.Endpoint != "" {
		t.Fatal("legacy task silently rebound to current engine", legacy.ExecutionIdentity, err)
	}
	if _, err = s.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET runtime_identity='{' WHERE id=?`, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetDeploymentOperation(t.Context(), first.ID); err == nil {
		t.Fatal("invalid frozen identity was accepted")
	}
	missing := model.DeploymentOperation{ID: id.New(id.Operation), RuntimeID: "missing", Type: "VOLUME_CREATE", Status: model.OperationQueued, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if _, err = s.CreateDeploymentOperation(t.Context(), &missing); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.GetDeploymentOperation(t.Context(), missing.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed admission retained an operation", err)
	}
}

func TestNodeOperationAdmissionRejectsIncompletePhysicalIdentityAtomically(t *testing.T) {
	for _, scenario := range []string{"unenrolled", "different-node", "missing-engine", "unknown-swarm-cluster"} {
		t.Run(scenario, func(t *testing.T) {
			s := newSQLiteStore(t)
			now := time.Now().UTC()
			pool := model.GatewayPool{ID: "pool", Name: "fixture", BackendNetwork: "fixture", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
			if err := s.CreatePool(t.Context(), &pool); err != nil {
				t.Fatal(err)
			}
			rt := model.Runtime{ID: "runtime", PoolID: pool.ID, Name: "fixture", Type: model.RuntimeDocker, Endpoint: "node://node", Enabled: true, ManagementEnabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
			if err := s.CreateRuntime(t.Context(), &rt); err != nil {
				t.Fatal(err)
			}
			nodes := map[string]model.RuntimeExecutionIdentity{rt.Endpoint: {NodeID: "node", EngineID: "engine"}}
			switch scenario {
			case "unenrolled":
				nodes = nil
			case "different-node":
				nodes[rt.Endpoint] = model.RuntimeExecutionIdentity{NodeID: "other", EngineID: "engine"}
			case "missing-engine":
				nodes[rt.Endpoint] = model.RuntimeExecutionIdentity{NodeID: "node"}
			case "unknown-swarm-cluster":
				if _, err := s.DB().ExecContext(t.Context(), `UPDATE runtimes SET runtime_type='swarm' WHERE id='runtime'`); err != nil {
					t.Fatal(err)
				}
			}
			s.ConfigureOperationNodes(nodes)
			op := model.DeploymentOperation{ID: "task", RuntimeID: rt.ID, Type: "VOLUME_CREATE", Status: model.OperationQueued, ExecutionIdentity: model.RuntimeExecutionIdentity{NodeID: "invented", EngineID: "invented"}, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			if _, err := s.CreateDeploymentOperation(t.Context(), &op); !errors.Is(err, ErrConflict) {
				t.Fatal("incomplete target entered the durable queue", err)
			}
			rows, err := s.ListDeploymentOperations(t.Context(), "", Page{Limit: 200})
			if err != nil || len(rows) != 0 {
				t.Fatal("failed admission left executable work", rows, err)
			}
		})
	}
}

func TestRuntimePhysicalIdentityCannotBeReplacedAndPendingTasksPreventCredentialRebinding(t *testing.T) {
	s := newSQLiteStore(t)
	now := time.Now().UTC()
	pool := model.GatewayPool{ID: "identity-pool", Name: "fixture", BackendNetwork: "fixture", Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := s.CreatePool(t.Context(), &pool); err != nil {
		t.Fatal(err)
	}
	rt := model.Runtime{ID: "identity-runtime", PoolID: pool.ID, Name: "fixture", Type: model.RuntimeDocker, Endpoint: "unix:///fixture/docker.sock", Enabled: true, ManagementEnabled: true, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := s.CreateRuntime(t.Context(), &rt); err != nil {
		t.Fatal(err)
	}
	rt.ClusterID = "cluster-original"
	if err := s.UpdateRuntime(t.Context(), &rt, rt.Revision); err != nil {
		t.Fatal(err)
	}
	rt.Revision++
	for _, replacement := range []string{"cluster-replacement", ""} {
		changed := rt
		changed.ClusterID = replacement
		if err := s.UpdateRuntime(t.Context(), &changed, rt.Revision); !errors.Is(err, ErrConflict) {
			t.Fatal("established ClusterID replaced", err)
		}
	}
	rt.Name = "renamed without identity change"
	if err := s.UpdateRuntime(t.Context(), &rt, rt.Revision); err != nil {
		t.Fatal(err)
	}
	rt.Revision++
	op := model.DeploymentOperation{ID: "identity-task", RuntimeID: rt.ID, Type: "VOLUME_CREATE", Status: model.OperationQueued, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if _, err := s.CreateDeploymentOperation(t.Context(), &op); err != nil {
		t.Fatal(err)
	}
	changed := rt
	changed.CredentialRef = "replacement-credentials"
	if err := s.UpdateRuntime(t.Context(), &changed, rt.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("pending intent followed replacement credentials", err)
	}
	claimed, err := s.ClaimDeploymentOperation(t.Context(), "fixture-worker", now, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	claimed.Status = model.OperationSucceeded
	if err = s.FinishDeploymentOperation(t.Context(), &claimed, now); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateRuntime(t.Context(), &changed, rt.Revision); err != nil {
		t.Fatal("completed task permanently blocked explicit rebind", err)
	}
	// SQLite has no Swarm admission. Seed its row solely to verify that switching
	// an established runtime back to Docker cannot bypass the identity guard.
	if _, err = s.DB().ExecContext(t.Context(), `UPDATE runtimes SET runtime_type='swarm' WHERE id=?`, rt.ID); err != nil {
		t.Fatal(err)
	}
	changed.Revision++
	if err = s.UpdateRuntime(t.Context(), &changed, changed.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("runtime type changed around existing identity", err)
	}
	stored, err := s.GetRuntime(t.Context(), rt.ID)
	if err != nil || stored.ClusterID != "cluster-original" || stored.Type != model.RuntimeSwarm {
		t.Fatal(stored, err)
	}
}
