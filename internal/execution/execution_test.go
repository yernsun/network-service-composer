package execution

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/hoststats"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type executionLeader struct {
	controlplane.StateStore
	state controlplane.State
	lost  bool
}

func (s *executionLeader) Current(context.Context) (controlplane.State, error) { return s.state, nil }
func (s *executionLeader) Validate(_ context.Context, id string, term int64) error {
	if s.lost || id != s.state.ID || term != s.state.Term {
		return ErrFenced
	}
	return nil
}

type executionLocks struct {
	mu     sync.Mutex
	owners map[string]int64
}

func (l *executionLocks) check(_ context.Context, key string, owner int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if owner <= 0 || l.owners[key] != owner {
		return lease.ErrLost
	}
	return nil
}
func (l *executionLocks) set(key string, owner int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.owners[key] = owner
}
func (l *executionLocks) TryLock(_ context.Context, key string) (lease.Lease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.owners[key] != 0 {
		return nil, lease.ErrBusy
	}
	l.owners[key] = 77
	return executionLease{l, key, 77}, nil
}

type executionLease struct {
	locks *executionLocks
	key   string
	owner int64
}

func (l executionLease) Check(ctx context.Context) error              { return l.locks.check(ctx, l.key, l.owner) }
func (l executionLease) SessionID(ctx context.Context) (int64, error) { return l.owner, l.Check(ctx) }
func (l executionLease) Release(context.Context) error                { l.locks.set(l.key, 0); return nil }

type executionFixture struct {
	v      *Verifier
	store  *storage.SQLStore
	leader *executionLeader
	locks  *executionLocks
	now    time.Time
	op     model.DeploymentOperation
	rt     model.Runtime
	ref    Reference
}

func newExecutionFixture(t *testing.T) *executionFixture {
	t.Helper()
	s, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "execution.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err = s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	ring, err := security.NewKeyring("fixture", map[string]string{"fixture": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	if err != nil {
		t.Fatal(err)
	}
	f := &executionFixture{store: s, locks: &executionLocks{owners: map[string]int64{}}, now: time.Now().UTC(), leader: &executionLeader{state: controlplane.State{Identity: controlplane.Identity{ID: "controller-a"}, Term: 3}}}
	cfg := config.Defaults()
	cfg.Security.SessionKey = "isolated-fixture-session-key-32-bytes"
	cfg.Management.Enabled = true
	cfg.Controller.InstanceID = "controller-a"
	for _, suffix := range []string{"a", "b", "c"} {
		cfg.Controller.Nodes = append(cfg.Controller.Nodes, config.ControllerNodeConfig{InstanceID: "controller-" + suffix, NodeID: "node-" + suffix, EngineID: "engine-" + suffix, ClusterID: "cluster-one", URL: "https://controller-" + suffix + ":8443"})
	}
	bindings := map[string]nodecontrol.Binding{}
	nodes := map[string]model.RuntimeExecutionIdentity{}
	for _, node := range cfg.Controller.Nodes {
		bindings[node.InstanceID] = nodecontrol.Binding{InstanceID: node.InstanceID, NodeID: node.NodeID, EngineID: node.EngineID, ClusterID: node.ClusterID}
		nodes["node://"+node.NodeID] = model.RuntimeExecutionIdentity{NodeID: node.NodeID, EngineID: node.EngineID}
	}
	s.ConfigureOperationNodes(nodes)
	reports := &nodecontrol.Reports{Store: s, Keys: ring, Bindings: bindings, Now: func(context.Context) (time.Time, error) { return f.now, nil }, CheckOwner: f.locks.check}
	f.v = &Verifier{Store: s, Config: cfg, Reports: reports, Leader: f.leader, CheckOwner: f.locks.check, Now: reports.Now, Keys: ring}
	pool := model.GatewayPool{ID: "pool", Name: "fixture", BackendNetwork: "fixture", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: f.now, UpdatedAt: f.now}
	if err = s.CreatePool(t.Context(), &pool); err != nil {
		t.Fatal(err)
	}
	f.rt = model.Runtime{ID: "runtime", PoolID: pool.ID, Name: "fixture", Type: model.RuntimeDocker, Endpoint: "node://node-a", Enabled: true, ManagementEnabled: true, Revision: 1, CreatedAt: f.now, UpdatedAt: f.now}
	if err = s.CreateRuntime(t.Context(), &f.rt); err != nil {
		t.Fatal(err)
	}
	f.op = model.DeploymentOperation{ID: "operation", RuntimeID: f.rt.ID, Type: "VOLUME_CREATE", Status: model.OperationQueued, Metadata: model.Metadata{Revision: 1, CreatedAt: f.now, UpdatedAt: f.now}}
	f.op.EncryptedInput, err = ring.Encrypt([]byte(`{"runtime_id":"runtime","node_id":"node-a","engine_id":"engine-a","action":"create","name":"fixture"}`), []byte("deployment-operation:"+f.op.ID))
	if err != nil {
		t.Fatal(err)
	}
	f.op.EncryptionKeyID = ring.PrimaryID()
	if _, err = s.CreateDeploymentOperation(t.Context(), &f.op); err != nil {
		t.Fatal(err)
	}
	f.op, err = s.ClaimDeploymentOperation(t.Context(), f.leader.state.ID, f.now, f.now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	f.report(t, "a", true)
	f.report(t, "b", true)
	f.report(t, "c", true)
	target, err := f.v.Select(t.Context(), f.rt)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := f.v.InputDigest(f.op)
	if err != nil {
		t.Fatal(err)
	}
	f.locks.set(StorageLock(f.rt), 11)
	f.ref = Reference{OperationID: f.op.ID, Attempt: f.op.Attempt, LeaderID: f.leader.state.ID, LeaderTerm: 3, RuntimeID: f.rt.ID, RuntimeFingerprint: RuntimeFingerprint(f.rt), Type: f.op.Type, InputSHA256: digest, Target: target, Locks: []LockProof{{Key: StorageLock(f.rt), Owner: 11}}}
	return f
}
func (f *executionFixture) report(t *testing.T, suffix string, manager bool) {
	t.Helper()
	node, engine, instance := "node-"+suffix, "engine-"+suffix, "controller-"+suffix
	f.locks.set(nodecontrol.LockKey(node), 11)
	session, err := f.v.Reports.Begin(t.Context(), instance, node, engine, "boot-"+suffix, 11)
	if err != nil {
		t.Fatal(err)
	}
	info := system.Info{ID: engine, Swarm: swarm.Info{NodeID: node, ControlAvailable: manager, Managers: 3}}
	if manager {
		info.Swarm.Cluster = &swarm.ClusterInfo{ID: "cluster-one"}
	}
	managers := []swarm.Node{}
	for _, id := range []string{"node-a", "node-b", "node-c"} {
		managers = append(managers, swarm.Node{ID: id, Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager}, ManagerStatus: &swarm.ManagerStatus{Reachability: swarm.ReachabilityReachable, Leader: id == "node-a"}})
	}
	if err = f.v.Reports.Accept(t.Context(), instance, nodecontrol.Report{NodeID: node, EngineID: engine, Generation: session.Generation, Sequence: session.Sequence + 1, Observation: nodecontrol.Observation{Health: &hoststats.Health{InstanceID: instance, Execution: "enabled"}, Info: info, Nodes: managers, Complete: true, At: f.now}}); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionSelectPreservesComposeAndVerifiesSwarmFailover(t *testing.T) {
	f := newExecutionFixture(t)
	f.locks.set(nodecontrol.LockKey("node-a"), 0)
	if _, err := f.v.Select(t.Context(), f.rt); !errors.Is(err, ErrUnavailable) {
		t.Fatal("Compose moved to another Engine", err)
	}
	rt := f.rt
	rt.Type = model.RuntimeSwarm
	rt.ClusterID = "cluster-one"
	target, err := f.v.Select(t.Context(), rt)
	if err != nil || target.NodeID != "node-b" || target.EngineID != "engine-b" {
		t.Fatal("verified manager was not selected", target, err)
	}
	if _, err = f.v.SelectPinned(t.Context(), rt, "node-a", "engine-a"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("volume followed manager failover", err)
	}
	if _, err = f.v.SelectPinned(t.Context(), rt, "node-b", "foreign"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("foreign Engine accepted", err)
	}
	f.report(t, "b", false)
	target, err = f.v.Select(t.Context(), rt)
	if err != nil || target.NodeID != "node-c" {
		t.Fatal("worker was used as manager", target, err)
	}
	f.now = f.now.Add(31 * time.Second)
	if _, err = f.v.Select(t.Context(), rt); !errors.Is(err, ErrUnavailable) {
		t.Fatal("stale inventory allowed mutation", err)
	}
}

func TestExecutionClaimRejectsExpiredAndForgedAuthority(t *testing.T) {
	for _, scenario := range []string{"term", "leader", "attempt", "runtime", "input", "lock-owner", "missing-lock", "heartbeat", "disabled", "recovery", "rebound", "legacy"} {
		t.Run(scenario, func(t *testing.T) {
			f := newExecutionFixture(t)
			ref := f.ref
			switch scenario {
			case "term":
				ref.LeaderTerm--
			case "leader":
				ref.LeaderID = "controller-b"
			case "attempt":
				ref.Attempt++
			case "runtime":
				ref.RuntimeID = "foreign"
			case "input":
				ref.InputSHA256 = "forged"
			case "lock-owner":
				f.locks.set(StorageLock(f.rt), 12)
			case "missing-lock":
				ref.Locks = nil
			case "heartbeat":
				f.now = f.now.Add(f.v.Config.Management.StaleAfter.Std() + time.Second)
			case "disabled":
				f.v.Config.Management.Enabled = false
			case "recovery":
				f.v.Config.Controller.RecoveryMode = true
			case "rebound":
				if _, err := f.store.DB().ExecContext(t.Context(), `UPDATE runtimes SET endpoint='node://node-b' WHERE id='runtime'`); err != nil {
					t.Fatal(err)
				}
			case "legacy":
				if _, err := f.store.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET runtime_identity='' WHERE id='operation'`); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := f.v.CheckClaim(t.Context(), ref); !errors.Is(err, ErrFenced) {
				t.Fatal("forged or expired claim accepted", err)
			}
		})
	}
}

func TestExecutionAcceptanceIsDurableAndNeverReplaysCompletedAttempt(t *testing.T) {
	f := newExecutionFixture(t)
	record := Record{Reference: f.ref, State: "SELECTED", SelectedAt: f.now}
	if err := Save(t.Context(), f.store, record, f.now); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	service := &Service{Verifier: f.v, Locker: f.locks, Execute: func(ctx context.Context, op model.DeploymentOperation, rt model.Runtime) error {
		calls.Add(1)
		if err := CheckContext(ctx); err != nil {
			return err
		}
		if err := MarkCommand(ctx); err != nil {
			return err
		}
		op.Status = model.OperationSucceeded
		return f.store.FinishDeploymentOperation(ctx, &op, f.now)
	}}
	request := Request{OperationID: f.op.ID, Attempt: f.op.Attempt, LeaderTerm: 3, Target: f.ref.Target.Identity()}
	if err := service.Accept(t.Context(), t.Context(), "controller-a", request); err != nil {
		t.Fatal(err)
	}
	service.Wait()
	actual, err := f.store.GetDeploymentOperation(t.Context(), f.op.ID)
	if err != nil || actual.Status != model.OperationSucceeded || calls.Load() != 1 {
		t.Fatal(actual, calls.Load(), err)
	}
	latest, err := Load(t.Context(), f.store, f.op.ID, f.op.Attempt)
	if err != nil || latest.State != "COMPLETED" || latest.CommandStartedAt == nil || latest.ExecutionOwner <= 0 {
		t.Fatal(latest, err)
	}
	if err = service.Accept(t.Context(), t.Context(), "controller-a", request); err == nil || calls.Load() != 1 {
		t.Fatal("duplicate acceptance replayed work", err)
	}
	if _, err = NewSQLVerifier(f.store, f.v.Config, f.v.Reports, f.v.Keys); err == nil {
		t.Fatal("SQLite admitted distributed executor authority")
	}
}

func TestExecutionInputDigestSurvivesKeyRotation(t *testing.T) {
	f := newExecutionFixture(t)
	first, err := f.v.InputDigest(f.op)
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := f.v.Keys.Decrypt(f.op.EncryptedInput, []byte("deployment-operation:"+f.op.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plain)
	ring, err := security.NewKeyring("rotated", map[string]string{"rotated": "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="})
	if err != nil {
		t.Fatal(err)
	}
	f.op.EncryptedInput, err = ring.Encrypt(plain, []byte("deployment-operation:"+f.op.ID))
	if err != nil {
		t.Fatal(err)
	}
	f.v.Keys = ring
	second, err := f.v.InputDigest(f.op)
	if err != nil || second != first {
		t.Fatal("encryption changed frozen execution intent", err)
	}
	if _, err = json.Marshal(f.ref); err != nil {
		t.Fatal(err)
	}
}

type advancedSessionStore struct {
	storage.Store
	advance  func()
	advanced bool
}

func (s *advancedSessionStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if key == "node-session:node-a" && !s.advanced {
		s.advanced = true
		s.advance()
	}
	return s.Store.GetControlDocument(ctx, key)
}

func TestExecutionCollectorProgressDoesNotChangeItsIdentityLease(t *testing.T) {
	f := newExecutionFixture(t)
	f.v.Store = &advancedSessionStore{Store: f.store, advance: func() { f.report(t, "a", false) }}
	target, err := f.v.Select(t.Context(), f.rt)
	if err != nil || target.NodeID != "node-a" || target.CollectorGeneration != f.ref.Target.CollectorGeneration || target.CollectorOwner != f.ref.Target.CollectorOwner {
		t.Fatal("healthy report sequence fenced execution", target, err)
	}
}
