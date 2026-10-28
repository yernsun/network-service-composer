package cluster

import (
	"fmt"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestSummaryDistinguishesUnknownDisabledAndPartialInventories(t *testing.T) {
	for _, stage := range []string{"disabled", "unobserved", "empty engine", "standalone", "worker", "worker with node details", "missing cluster identity", "expired", "failed collection", "node down", "worker direct alias", "ambiguous node identity", "newer manager"} {
		t.Run(stage, func(t *testing.T) {
			m, store, cli := fixtureMonitor()
			now := time.Now()
			m.now = func() time.Time { return now }
			rt := store.Runtimes[0]
			rt.ID = "runtime-a"
			src := source{Info: cli.System, Nodes: append([]swarm.Node(nil), cli.Nodes...), At: now}
			m.runtimes = map[string]model.Runtime{rt.ID: rt}
			m.sources = map[string]source{connection(rt): src}
			nodes, ready, managers, connected, stale, unknown, rows := 2, 2, 1, 1, 0, 0, 1
			complete := true
			available := true
			switch stage {
			case "disabled":
				rt.Enabled = false
				m.runtimes[rt.ID] = rt
				nodes, ready, managers, connected = 0, 0, 0, 0
				available = false
			case "unobserved":
				delete(m.sources, connection(rt))
				nodes, ready, managers, connected = 0, 0, 0, 0
				unknown = 1
				complete = false
				available = false
			case "empty engine":
				src.Info.ID = ""
				nodes, ready, managers, connected = 0, 0, 0, 0
				unknown = 1
				complete = false
				available = false
			case "standalone":
				rt.Type = model.RuntimeDocker
				m.runtimes[rt.ID] = rt
				src.Info.Swarm = swarm.Info{}
				src.Nodes = nil
				nodes, ready, managers = 1, 1, 0
			case "worker":
				src.Info.Swarm.ControlAvailable = false
				src.Nodes = nil
				nodes, ready, managers = 1, 1, 0
				complete = false
			case "worker with node details":
				src.Info.Swarm.ControlAvailable = false
				nodes, ready = 1, 1
				complete = false
			case "missing cluster identity":
				src.Info.Swarm.Cluster = &swarm.ClusterInfo{}
				nodes, ready = 1, 1
				complete = false
			case "expired":
				src.At = now.Add(-time.Minute)
				stale, connected = 2, 0
				complete = false
			case "failed collection":
				src.Error = "collection_failed"
				stale, connected = 2, 0
				complete = false
			case "node down":
				src.Nodes[1].Status.State = swarm.NodeStateDown
				ready = 1
			case "worker direct alias", "ambiguous node identity":
				worker := rt
				worker.ID = "runtime-c"
				worker.Type = model.RuntimeDocker
				worker.Endpoint = "tcp://worker.invalid:2376"
				m.runtimes[worker.ID] = worker
				m.sources[connection(worker)] = source{Info: system.Info{ID: "worker-engine", NCPU: 12, Swarm: swarm.Info{NodeID: "node-b"}}, At: now}
				connected = 2
				rows = 2
				if stage == "ambiguous node identity" {
					other := rt
					other.ID = "runtime-b"
					other.Endpoint = "tcp://other.invalid:2376"
					m.runtimes[other.ID] = other
					otherSource := src
					otherSource.Info.ID = "other-engine"
					otherSource.Info.Swarm.Cluster = &swarm.ClusterInfo{ID: "other-cluster"}
					m.sources[connection(other)] = otherSource
					nodes, ready, managers, connected, rows = 5, 5, 2, 3, 3
				}
			case "newer manager":
				other := rt
				other.ID = "runtime-b"
				other.Endpoint = "tcp://newer.invalid:2376"
				m.runtimes[other.ID] = other
				fresh := src
				fresh.Info.ID = "fresh-engine"
				fresh.Nodes = append([]swarm.Node(nil), src.Nodes[:1]...)
				fresh.Nodes[0].Description.Resources.NanoCPUs = 8e9
				src.At = now.Add(-time.Second)
				m.sources[connection(other)] = fresh
				nodes, ready, rows = 1, 1, 2
			}
			if stage != "unobserved" {
				m.sources[connection(rt)] = src
			}
			before := cli.StatsCalls
			summary, runtimes := m.Summary()
			if cli.StatsCalls != before {
				t.Fatal("summary performed new collection")
			}
			if summary.Nodes != nodes || summary.ReadyNodes != ready || summary.Managers != managers || summary.ConnectedNodes != connected || summary.StaleNodes != stale || summary.UnknownRuntimes != unknown || summary.Complete != complete || len(runtimes) != rows {
				t.Fatalf("incorrect evidence summary: %+v runtimes=%+v", summary, runtimes)
			}
			if runtimes[0].Available != available {
				t.Fatal("availability confused with completeness", runtimes[0])
			}
			if nodes == 0 && !summary.ObservedAt.IsZero() {
				t.Fatal("unobserved resources invented timestamp")
			}
			if stage == "newer manager" && summary.CPUs != 8 {
				t.Fatal("older manager overrode newer physical node evidence", summary)
			}
		})
	}
}

func TestSummaryDeduplicationPrefersFreshSpecificNodeEvidence(t *testing.T) {
	for _, dockerFirst := range []bool{false, true} {
		for _, dockerStale := range []bool{false, true} {
			for _, swarmStale := range []bool{false, true} {
				t.Run(fmt.Sprintf("docker-first=%t/docker-stale=%t/swarm-stale=%t", dockerFirst, dockerStale, swarmStale), func(t *testing.T) {
					m, store, cli := fixtureMonitor()
					now := time.Now()
					m.now = func() time.Time { return now }
					manager := store.Runtimes[0]
					manager.ID = "a-manager"
					worker := manager
					worker.ID = "b-worker"
					if dockerFirst {
						manager.ID = "b-manager"
						worker.ID = "a-worker"
					}
					worker.Endpoint = "tcp://direct.invalid:2376"
					worker.Type = model.RuntimeDocker
					managed := source{Info: cli.System, Nodes: append([]swarm.Node(nil), cli.Nodes...), At: now}
					direct := source{Info: system.Info{ID: "direct-engine", NCPU: 8, Swarm: swarm.Info{NodeID: "node-a", Cluster: &swarm.ClusterInfo{ID: "cluster-a"}}}, At: now.Add(-time.Second)}
					if dockerStale {
						direct.At = now.Add(-time.Minute)
					}
					if swarmStale {
						managed.At = now.Add(-time.Minute)
					}
					m.runtimes = map[string]model.Runtime{manager.ID: manager, worker.ID: worker}
					m.sources = map[string]source{connection(manager): managed, connection(worker): direct}
					summary, rows := m.Summary()
					if summary.Nodes != 2 || len(rows) != 2 {
						t.Fatal("duplicate physical node counted", summary)
					}
					expectedStale := 0
					if swarmStale {
						expectedStale = 1
						if dockerStale {
							expectedStale = 2
						}
					}
					if summary.StaleNodes != expectedStale {
						t.Fatal("fresh direct evidence lost", summary)
					}
					expectedManagers := 1
					if swarmStale && !dockerStale {
						expectedManagers = 0
					}
					if summary.Managers != expectedManagers {
						t.Fatal("stale manager role presented as fresh", summary)
					}
				})
			}
		}
	}
}
