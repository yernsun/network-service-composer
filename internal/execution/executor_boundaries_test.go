package execution

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestExecutorIndependentlyFencesRecordLeaseTargetAndCollector(t *testing.T) {
	f := newExecutionFixture(t)
	for _, scenario := range []string{"valid", "replaced-record", "changed-reference", "claim-revoked", "not-running", "no-owner", "different-instance", "missing-binding", "foreign-binding-instance", "foreign-engine", "foreign-cluster", "executor-lease-lost", "collector-expired", "collector-generation", "collector-owner", "compose-migration", "swarm-cross-cluster", "swarm-config", "swarm-volume", "swarm-volume-delete", "swarm-no-manager", "swarm-worker", "swarm-no-cluster", "swarm-no-quorum"} {
		t.Run(scenario, func(t *testing.T) {
			v := *f.v
			v.Config.Controller.Nodes = slices.Clone(f.v.Config.Controller.Nodes)
			view := &authorityView{Store: f.store, op: f.op, runtime: f.rt, documents: map[string]storage.ControlDocument{}}
			v.Store = view
			ref := f.ref
			ref.Locks = slices.Clone(f.ref.Locks)
			if len(scenario) >= 6 && scenario[:6] == "swarm-" {
				view.runtime.Type, view.runtime.ClusterID = model.RuntimeSwarm, "cluster-one"
				view.op.ExecutionIdentity.Type, view.op.ExecutionIdentity.ClusterID = model.RuntimeSwarm, "cluster-one"
				ref.RuntimeFingerprint = RuntimeFingerprint(view.runtime)
				ref.Type = "DOCKER_RESOURCE_CREATE"
				view.op.Type = ref.Type
				ref.Locks = []LockProof{{Key: "docker-resource:cluster-one", Owner: 11}}
			}
			f.locks.set(ExecutionLock(f.op.ID, f.op.Attempt), 77)
			f.locks.set("docker-resource:cluster-one", 11)
			f.locks.set(StorageLock(view.runtime), 11)
			record := Record{Reference: ref, State: "RUNNING", ExecutionOwner: 77, SelectedAt: f.now}
			wanted := scenario == "valid" || scenario == "swarm-config" || scenario == "swarm-volume" || scenario == "swarm-volume-delete"
			switch scenario {
			case "replaced-record", "changed-reference":
			case "claim-revoked":
				v.Config.Management.Enabled = false
			case "not-running":
				record.State = "SELECTED"
			case "no-owner":
				record.ExecutionOwner = 0
			case "different-instance":
				v.Config.Controller.InstanceID = "controller-c"
			case "missing-binding":
				record.Reference.Target.NodeID = "foreign"
			case "foreign-binding-instance":
				v.Config.Controller.Nodes[0].InstanceID = "foreign"
			case "foreign-engine":
				record.Reference.Target.EngineID = "foreign"
			case "foreign-cluster":
				record.Reference.Target.ClusterID = "foreign"
			case "executor-lease-lost":
				f.locks.set(ExecutionLock(f.op.ID, f.op.Attempt), 0)
			case "collector-expired":
				v.Config.Management.StaleAfter = 0
				view.op.HeartbeatAt = nil
				// Keep task authority fresh while only observation freshness expires.
				at := f.now.Add(31 * time.Second)
				view.op.HeartbeatAt = &at
				v.Config.Management.StaleAfter = f.v.Config.Management.StaleAfter
				v.Now = func(context.Context) (time.Time, error) { return at, nil }
				reports := *f.v.Reports
				reports.Now = v.Now
				v.Reports = &reports
			case "collector-generation":
				record.Reference.Target.CollectorGeneration++
			case "collector-owner":
				record.Reference.Target.CollectorOwner++
			case "compose-migration", "swarm-cross-cluster":
				target, err := f.v.SelectPinned(t.Context(), model.Runtime{Type: model.RuntimeSwarm, Endpoint: f.rt.Endpoint, ClusterID: "cluster-one", Enabled: true, ManagementEnabled: true}, "node-b", "engine-b")
				if err != nil {
					t.Fatal(err)
				}
				record.Reference.Target = target
				v.Config.Controller.InstanceID = "controller-b"
				if scenario == "swarm-cross-cluster" {
					view.runtime.ClusterID = "other-cluster"
					view.op.ExecutionIdentity.ClusterID = "other-cluster"
					v.Config.Controller.Nodes[0].ClusterID = "other-cluster"
					record.Reference.RuntimeFingerprint = RuntimeFingerprint(view.runtime)
				}
			case "swarm-volume", "swarm-volume-delete", "swarm-no-manager":
				view.op.Type = "VOLUME_CREATE"
				if scenario == "swarm-volume-delete" {
					view.op.Type = "VOLUME_DELETE"
				}
				record.Reference.Type = view.op.Type
				record.Reference.Locks = []LockProof{{Key: StorageLock(view.runtime), Owner: 11}}
			}
			if scenario == "swarm-worker" || scenario == "swarm-no-cluster" || scenario == "swarm-no-quorum" || scenario == "swarm-no-manager" {
				for _, suffix := range []string{"a", "b", "c"} {
					report, err := f.v.Reports.Read(t.Context(), "node-"+suffix)
					if err != nil {
						t.Fatal(err)
					}
					report.Sequence++
					if scenario == "swarm-worker" || scenario == "swarm-no-manager" {
						report.Observation.Info.Swarm.ControlAvailable = false
					}
					if scenario == "swarm-no-cluster" {
						report.Observation.Info.Swarm.Cluster = nil
					}
					if scenario == "swarm-no-quorum" {
						for i := range report.Observation.Nodes {
							report.Observation.Nodes[i].ManagerStatus.Reachability = swarm.ReachabilityUnreachable
						}
					}
					if err = f.v.Reports.Accept(t.Context(), "controller-"+suffix, report); err != nil {
						t.Fatal(err)
					}
				}
			}
			stored := record
			if scenario == "replaced-record" {
				stored.ExecutionOwner++
			}
			if scenario == "changed-reference" {
				stored.Reference.Type = "VOLUME_DELETE"
			}
			raw, err := json.Marshal(stored)
			if err != nil {
				t.Fatal(err)
			}
			key := RecordID(f.op.ID, f.op.Attempt)
			view.documents[key] = storage.ControlDocument{ID: key, Kind: RecordKind, ParentID: f.op.ID, Payload: raw}
			err = v.CheckExecutor(t.Context(), record)
			if wanted && err != nil {
				t.Fatal("valid executor rejected", err)
			}
			if !wanted && err == nil {
				t.Fatal("stale or foreign executor accepted")
			}
			// Restore signed observations altered by this scenario for the next
			// independent authority case; no task mutation is executed by this test.
			if scenario == "swarm-worker" || scenario == "swarm-no-cluster" || scenario == "swarm-no-quorum" || scenario == "swarm-no-manager" {
				for _, suffix := range []string{"a", "b", "c"} {
					f.report(t, suffix, true)
				}
			}
		})
	}
}

func TestPinnedExecutionRejectsUnavailableOrDifferentPhysicalBinding(t *testing.T) {
	f := newExecutionFixture(t)
	for _, scenario := range []string{"valid", "no-reports", "no-lease-check", "runtime-disabled", "runtime-management-disabled", "management-disabled", "recovery", "credential-ambiguity", "unsupported-runtime", "unknown-node", "missing-engine", "wrong-engine", "different-compose-node", "empty-swarm-cluster", "wrong-swarm-cluster", "valid-swarm", "stale-observation"} {
		t.Run(scenario, func(t *testing.T) {
			v := *f.v
			rt := f.rt
			node, engine := "node-a", "engine-a"
			switch scenario {
			case "no-reports":
				v.Reports = nil
			case "no-lease-check":
				v.CheckOwner = nil
			case "runtime-disabled":
				rt.Enabled = false
			case "runtime-management-disabled":
				rt.ManagementEnabled = false
			case "management-disabled":
				v.Config.Management.Enabled = false
			case "recovery":
				v.Config.Controller.RecoveryMode = true
			case "credential-ambiguity":
				rt.CredentialRef = "foreign-binding"
			case "unsupported-runtime":
				rt.Type = "unsupported"
			case "unknown-node":
				node = "unknown"
			case "missing-engine":
				engine = ""
			case "wrong-engine":
				engine = "foreign"
			case "different-compose-node":
				node, engine = "node-b", "engine-b"
			case "empty-swarm-cluster":
				rt.Type = model.RuntimeSwarm
			case "wrong-swarm-cluster":
				rt.Type, rt.ClusterID = model.RuntimeSwarm, "foreign"
			case "valid-swarm":
				rt.Type, rt.ClusterID = model.RuntimeSwarm, "cluster-one"
			case "stale-observation":
				f.locks.set(nodecontrol.LockKey(node), 0)
				defer f.locks.set(nodecontrol.LockKey(node), 11)
			}
			_, err := v.SelectPinned(t.Context(), rt, node, engine)
			switch scenario {
			case "no-reports", "no-lease-check", "runtime-disabled", "runtime-management-disabled", "management-disabled", "recovery", "credential-ambiguity", "unsupported-runtime", "empty-swarm-cluster", "wrong-swarm-cluster":
				if _, err := v.Select(t.Context(), rt); !errors.Is(err, ErrUnavailable) {
					t.Fatal("general executor ignored the same unavailable authority", err)
				}
			}
			if scenario == "valid" || scenario == "valid-swarm" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrUnavailable) {
				t.Fatal("unsafe pinned executor accepted", err)
			}
		})
	}
}
