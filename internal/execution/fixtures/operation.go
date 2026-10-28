// Package fixtures supplies isolated authority setup for Engine SDK behavior
// tests. Fake session proofs here do not establish shared-SQL or HA acceptance.
package fixtures

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/execution"
	"github.com/yernsun/network-service-composer/internal/hoststats"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type Operation struct {
	Store   *storage.SQLStore
	Config  config.Config
	Keys    *security.Keyring
	Runtime model.Runtime
	Task    model.DeploymentOperation
	Context context.Context
	Info    system.Info
	Nodes   []swarm.Node
	Reports *nodecontrol.Reports
}

func New(t testing.TB, kind string, input []byte) *Operation {
	t.Helper()
	ctx := context.Background()
	s, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "sdk.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	keys, err := security.NewKeyring("fixture", map[string]string{"fixture": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Management.Enabled = true
	cfg.Controller.InstanceID = "fixture-controller"
	cfg.Security.SessionKey = "isolated-sdk-session-key-at-least-32-bytes"
	cfg.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: cfg.Controller.InstanceID, NodeID: "node", EngineID: "engine", ClusterID: "cluster", URL: "https://controller.example.test:8443"}}
	now := time.Now().UTC()
	pool := model.GatewayPool{ID: "pool", Name: "fixture", BackendNetwork: "fixture", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = s.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	rt := model.Runtime{ID: "rt", PoolID: pool.ID, Name: "fixture", Type: model.RuntimeDocker, Endpoint: "node://node", Enabled: true, ManagementEnabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = s.CreateRuntime(ctx, &rt); err != nil {
		t.Fatal(err)
	}
	if kind == "DOCKER_RESOURCE_CREATE" || kind == "DOCKER_RESOURCE_DELETE" {
		// The isolated SQLite fixture supplies storage for SDK assertions only.
		// Normal Controller admission rejects Swarm/SQLite; real SQL session
		// authority is exercised by the separate external-store fixture.
		rt.Type, rt.ClusterID = model.RuntimeSwarm, "cluster"
		if _, err = s.DB().ExecContext(ctx, `UPDATE runtimes SET runtime_type='swarm',cluster_id='cluster' WHERE id='rt'`); err != nil {
			t.Fatal(err)
		}
	}
	s.ConfigureOperationNodes(map[string]model.RuntimeExecutionIdentity{rt.Endpoint: {NodeID: "node", EngineID: "engine"}})
	op := model.DeploymentOperation{ID: "op", RuntimeID: rt.ID, Type: kind, Status: model.OperationQueued, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	op.EncryptedInput, err = keys.Encrypt(input, []byte("deployment-operation:"+op.ID))
	if err != nil {
		t.Fatal(err)
	}
	op.EncryptionKeyID = keys.PrimaryID()
	if _, err = s.CreateDeploymentOperation(ctx, &op); err != nil {
		t.Fatal(err)
	}
	op, err = s.ClaimDeploymentOperation(ctx, cfg.Controller.InstanceID, now, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	state := controlplane.SQLState{Store: s}
	leader, err := state.Claim(ctx, controlplane.Identity{ID: cfg.Controller.InstanceID}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	reports := &nodecontrol.Reports{Store: s, Keys: keys, Bindings: map[string]nodecontrol.Binding{cfg.Controller.InstanceID: {InstanceID: cfg.Controller.InstanceID, NodeID: "node", EngineID: "engine", ClusterID: "cluster"}}, Now: state.DatabaseTime, CheckOwner: func(context.Context, string, int64) error { return nil }}
	session, err := reports.Begin(ctx, cfg.Controller.InstanceID, "node", "engine", "fixture-boot", 11)
	if err != nil {
		t.Fatal(err)
	}
	info := system.Info{ID: "engine", Swarm: swarm.Info{NodeID: "node", Managers: 1, ControlAvailable: true, Cluster: &swarm.ClusterInfo{ID: "cluster"}}}
	nodes := []swarm.Node{{ID: "node", Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager, Availability: swarm.NodeAvailabilityActive}, Status: swarm.NodeStatus{State: swarm.NodeStateReady}, ManagerStatus: &swarm.ManagerStatus{Leader: true, Reachability: swarm.ReachabilityReachable}}}
	if err = reports.Accept(ctx, cfg.Controller.InstanceID, nodecontrol.Report{NodeID: "node", EngineID: "engine", Generation: session.Generation, Sequence: 1, Observation: nodecontrol.Observation{Info: info, Nodes: nodes, Complete: true, At: now, Health: &hoststats.Health{InstanceID: cfg.Controller.InstanceID, Execution: "enabled"}}}); err != nil {
		t.Fatal(err)
	}
	v := &execution.Verifier{Store: s, Config: cfg, Reports: reports, Leader: state, Keys: keys, Now: state.DatabaseTime, CheckOwner: reports.CheckOwner}
	target, err := v.Select(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := v.InputDigest(op)
	if err != nil {
		t.Fatal(err)
	}
	locks := []execution.LockProof{{Key: execution.StorageLock(rt), Owner: 11}}
	if rt.Type == model.RuntimeSwarm {
		locks = []execution.LockProof{{Key: "docker-resource:" + rt.ClusterID, Owner: 11}}
	}
	record := execution.Record{Reference: execution.Reference{OperationID: op.ID, Attempt: op.Attempt, LeaderID: cfg.Controller.InstanceID, LeaderTerm: leader.Term, RuntimeID: rt.ID, RuntimeFingerprint: execution.RuntimeFingerprint(rt), Type: kind, InputSHA256: digest, Locks: locks, Target: target}, State: "RUNNING", ExecutionOwner: 11, SelectedAt: now}
	if err = execution.Save(ctx, s, record, now); err != nil {
		t.Fatal(err)
	}
	bound := execution.WithRecord(storage.WithDeploymentOperation(ctx, op), v, record)
	return &Operation{Store: s, Config: cfg, Keys: keys, Runtime: rt, Task: op, Context: bound, Info: info, Nodes: nodes, Reports: reports}
}
