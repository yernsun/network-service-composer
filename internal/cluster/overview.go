package cluster

import (
	"github.com/yernsun/network-service-composer/internal/model"
	"sort"
	"time"
)

// NodeSummary describes known physical nodes. Complete is false when a Runtime
// has not been observed or an observation is stale; unknown nodes are not zeros.
type NodeSummary struct {
	Nodes           int       `json:"nodes"`
	ReadyNodes      int       `json:"ready_nodes"`
	Managers        int       `json:"managers"`
	ConnectedNodes  int       `json:"connected_nodes"`
	StaleNodes      int       `json:"stale_nodes"`
	UnknownRuntimes int       `json:"unknown_runtimes"`
	CPUs            float64   `json:"cpus"`
	MemoryBytes     int64     `json:"memory_bytes"`
	Complete        bool      `json:"complete"`
	ObservedAt      time.Time `json:"observed_at"`
}
type RuntimeSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
	NodeSummary
}

// Summary only reads the shared collector cache; it never contacts Docker.
func (m *Monitor) Summary() (NodeSummary, []RuntimeSummary) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := m.now()
	aliases := map[string]string{}
	for _, s := range m.sources {
		if s.Info.ID != "" && s.Info.Swarm.Cluster != nil && s.Info.Swarm.Cluster.ID != "" && s.Info.Swarm.NodeID != "" {
			aliases[s.Info.ID] = "swarm:" + s.Info.Swarm.Cluster.ID + ":" + s.Info.Swarm.NodeID
		}
	}
	// Workers may omit ClusterInfo in /info. Match their stable NodeID only
	// when a configured manager identifies it in exactly one cluster.
	nodeKeys := map[string]string{}
	for _, s := range m.sources {
		if s.Info.Swarm.Cluster == nil || s.Info.Swarm.Cluster.ID == "" {
			continue
		}
		for _, n := range s.Nodes {
			key := "swarm:" + s.Info.Swarm.Cluster.ID + ":" + n.ID
			if old, ok := nodeKeys[n.ID]; ok && old != key {
				nodeKeys[n.ID] = ""
			} else if !ok {
				nodeKeys[n.ID] = key
			}
		}
	}
	for _, s := range m.sources {
		if s.Info.ID != "" && aliases[s.Info.ID] == "" && s.Info.Swarm.NodeID != "" {
			if key := nodeKeys[s.Info.Swarm.NodeID]; key != "" {
				aliases[s.Info.ID] = key
			}
		}
	}
	all := map[string]Node{}
	rows := make([]RuntimeSummary, 0, len(m.runtimes))
	total := NodeSummary{Complete: true}
	ids := make([]string, 0, len(m.runtimes))
	for id := range m.runtimes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rt := m.runtimes[id]
		row := RuntimeSummary{ID: id, Name: rt.Name, Kind: string(rt.Type), Enabled: rt.Enabled, NodeSummary: NodeSummary{Complete: true}}
		s, ok := m.sources[connection(rt)]
		if !rt.Enabled || !ok || s.Info.ID == "" {
			row.Complete = false
			row.Error = "unavailable"
			if rt.Enabled {
				total.UnknownRuntimes++
				total.Complete = false
			}
			rows = append(rows, row)
			continue
		}
		row.Available = true
		row.Error = s.Error
		clusterID := ""
		if s.Info.Swarm.Cluster != nil {
			clusterID = s.Info.Swarm.Cluster.ID
		}
		if rt.Type == model.RuntimeSwarm && clusterID != "" {
			for _, candidate := range m.sources {
				if candidate.Info.Swarm.Cluster != nil && candidate.Info.Swarm.Cluster.ID == clusterID && candidate.Info.Swarm.ControlAvailable && (!s.Info.Swarm.ControlAvailable || candidate.At.After(s.At)) {
					s = candidate
				}
			}
		}
		nodes := map[string]Node{}
		if rt.Type == model.RuntimeSwarm && s.Info.Swarm.ControlAvailable && clusterID != "" {
			for _, sn := range s.Nodes {
				nodes["swarm:"+clusterID+":"+sn.ID] = swarmNode(sn, s.At)
			}
		} else {
			key := aliases[s.Info.ID]
			if key == "" {
				key = "engine:" + s.Info.ID
			}
			nodes[key] = Node{ID: nodeID(s.Info), EngineID: s.Info.ID, Role: "standalone", State: "ready", CPUs: float64(s.Info.NCPU), MemoryBytes: s.Info.MemTotal, ObservedAt: s.At}
			for _, sn := range s.Nodes {
				if sn.ID == s.Info.Swarm.NodeID {
					nodes[key] = swarmNode(sn, s.At)
				}
			}
			if rt.Type == model.RuntimeSwarm {
				row.Complete = false
				row.Error = "manager_required"
			}
		}
		for key, n := range nodes {
			n.Stale = s.Error != "" || now.Sub(n.ObservedAt) > 30*time.Second
			for _, direct := range m.sources {
				dk := aliases[direct.Info.ID]
				if dk == "" {
					dk = "engine:" + direct.Info.ID
				}
				if direct.Info.ID != "" && dk == key && direct.Error == "" && now.Sub(direct.At) <= 30*time.Second {
					n.Connected = true
				}
			}
			addNode(&row.NodeSummary, n)
			old, exists := all[key]
			if !exists || old.Stale && !n.Stale || old.Stale == n.Stale && (old.Role == "standalone" && n.Role != "standalone" || old.Role == n.Role && n.ObservedAt.After(old.ObservedAt)) {
				n.Connected = n.Connected || old.Connected
				all[key] = n
			} else if n.Connected {
				old.Connected = true
				all[key] = old
			}
		}
		if !row.Complete {
			total.Complete = false
		}
		rows = append(rows, row)
	}
	for _, n := range all {
		addNode(&total, n)
	}
	return total, rows
}
func addNode(v *NodeSummary, n Node) {
	v.Nodes++
	if n.State == "ready" {
		v.ReadyNodes++
	}
	if n.Role == "manager" {
		v.Managers++
	}
	if n.Connected {
		v.ConnectedNodes++
	}
	if n.Stale {
		v.StaleNodes++
		v.Complete = false
	}
	v.CPUs += n.CPUs
	v.MemoryBytes += n.MemoryBytes
	if v.ObservedAt.IsZero() || n.ObservedAt.Before(v.ObservedAt) {
		v.ObservedAt = n.ObservedAt
	}
}
