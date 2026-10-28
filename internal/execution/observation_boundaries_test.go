package execution

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestRestoredOrChangingInventoryNeverCreatesAnEligibleExecutor(t *testing.T) {
	f := newExecutionFixture(t)
	for _, scenario := range []string{"no-capability", "disabled-capability", "missing-instance", "missing-engine-enrollment", "missing-cluster-enrollment", "changed-engine-enrollment", "changed-report-info-engine", "changed-report-node", "changed-report-cluster", "session-disappeared", "session-wrong-kind", "session-wrong-parent", "session-malformed", "session-unowned", "session-generation-changed"} {
		t.Run(scenario, func(t *testing.T) {
			v := *f.v
			v.Config.Controller.Nodes = slices.Clone(f.v.Config.Controller.Nodes)
			binding := v.Config.Controller.Nodes[0]
			if scenario == "missing-instance" {
				binding.InstanceID = ""
			}
			if scenario == "missing-engine-enrollment" {
				binding.EngineID = ""
			}
			if scenario == "missing-cluster-enrollment" {
				binding.ClusterID = ""
			}
			if scenario == "changed-engine-enrollment" {
				binding.EngineID = "replacement-engine"
			}
			if scenario == "no-capability" || scenario == "disabled-capability" || scenario == "changed-report-info-engine" || scenario == "changed-report-node" || scenario == "changed-report-cluster" {
				// Simulate a restored signed inventory or a changed enrollment. The
				// executor must verify physical identity even after decryption.
				doc, err := f.store.GetControlDocument(t.Context(), "node-report:node-a")
				if err != nil {
					t.Fatal(err)
				}
				var envelope struct {
					KeyID      string `json:"key_id"`
					Ciphertext []byte `json:"ciphertext"`
				}
				if err = json.Unmarshal(doc.Payload, &envelope); err != nil {
					t.Fatal(err)
				}
				raw, _, err := f.v.Keys.Decrypt(envelope.Ciphertext, []byte("control:"+doc.ID))
				if err != nil {
					t.Fatal(err)
				}
				defer clear(raw)
				var report nodecontrol.Report
				if err = json.Unmarshal(raw, &report); err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "no-capability":
					report.Observation.Health = nil
				case "disabled-capability":
					report.Observation.Health.Execution = "disabled"
				case "changed-report-info-engine":
					report.Observation.Info.ID = "foreign"
				case "changed-report-node":
					report.Observation.Info.Swarm.NodeID = "foreign"
				case "changed-report-cluster":
					report.Observation.Info.Swarm.Cluster.ID = "foreign"
				}
				raw, err = json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				envelope.Ciphertext, err = f.v.Keys.Encrypt(raw, []byte("control:"+doc.ID))
				clear(raw)
				if err != nil {
					t.Fatal(err)
				}
				doc.Payload, err = json.Marshal(envelope)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: doc, Expected: doc.Revision}}); err != nil {
					t.Fatal(err)
				}
			}
			if len(scenario) > 8 && scenario[:8] == "session-" {
				doc, err := f.store.GetControlDocument(t.Context(), "node-session:node-a")
				if err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "session-disappeared":
					doc = storage.ControlDocument{}
				case "session-wrong-kind":
					doc.Kind = "unrelated"
				case "session-wrong-parent":
					doc.ParentID = "foreign"
				case "session-malformed":
					doc.Payload = []byte(`{`)
				case "session-unowned", "session-generation-changed":
					var session nodecontrol.Session
					if err = json.Unmarshal(doc.Payload, &session); err != nil {
						t.Fatal(err)
					}
					if scenario == "session-unowned" {
						session.Owner = 0
					} else {
						session.Generation++
					}
					doc.Payload, err = json.Marshal(session)
					if err != nil {
						t.Fatal(err)
					}
				}
				// The outer identity fence can race with a normal collector restart
				// after the signed inventory read. It must inspect the new session.
				v.Store = &authorityView{Store: f.store, documents: map[string]storage.ControlDocument{"node-session:node-a": doc}}
			}
			if _, _, err := v.observation(t.Context(), binding); err == nil {
				t.Fatal("unverified observation admitted an executor", scenario)
			}
			// Restore the real signed collector view before the next case.
			f.report(t, "a", true)
		})
	}
	if _, err := f.v.binding(""); !errors.Is(err, ErrUnavailable) {
		t.Fatal("empty binding accepted", err)
	}
	if NodeID("node://node-a/path") != "" || NodeID("node://") != "" || NodeID("tcp://engine") != "" {
		t.Fatal("ambiguous node URI accepted")
	}
}

func TestQuorumUsesActualVotingMembersAndSingleReachableLeader(t *testing.T) {
	info := system.Info{Swarm: swarm.Info{Managers: 3}}
	base := []swarm.Node{{ID: "a", Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager}, ManagerStatus: &swarm.ManagerStatus{Reachability: swarm.ReachabilityReachable, Leader: true}}, {ID: "b", Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager}, ManagerStatus: &swarm.ManagerStatus{Reachability: swarm.ReachabilityReachable}}, {ID: "c", Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager}, ManagerStatus: &swarm.ManagerStatus{Reachability: swarm.ReachabilityUnreachable}}, {ID: "worker", Spec: swarm.NodeSpec{Role: swarm.NodeRoleWorker}}}
	for _, scenario := range []string{"majority", "drained-manager", "no-leader", "two-leaders", "manager-status-unknown", "incomplete-members", "zero-managers"} {
		t.Run(scenario, func(t *testing.T) {
			nodes := slices.Clone(base)
			for i := range nodes {
				if nodes[i].ManagerStatus != nil {
					value := *nodes[i].ManagerStatus
					nodes[i].ManagerStatus = &value
				}
			}
			current := info
			switch scenario {
			case "drained-manager":
				nodes[1].Spec.Availability = swarm.NodeAvailabilityDrain
			case "no-leader":
				nodes[0].ManagerStatus.Leader = false
			case "two-leaders":
				nodes[1].ManagerStatus.Leader = true
			case "manager-status-unknown":
				nodes[1].ManagerStatus = nil
			case "incomplete-members":
				nodes = nodes[:2]
			case "zero-managers":
				nodes = nil
				current.Swarm.Managers = 0
			}
			want := scenario == "majority" || scenario == "drained-manager"
			if Quorum(current, nodes) != want {
				t.Fatal("voting authority confused with workload availability", scenario)
			}
		})
	}
}
