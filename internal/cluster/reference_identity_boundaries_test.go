package cluster

import (
	"context"
	"github.com/yernsun/network-service-composer/internal/model"
	"testing"
	"time"
)

func TestInventoryReferencesRequireSameActualEngineOrCluster(t *testing.T) {
	for _, mode := range []string{"local reference", "same engine", "foreign engine", "same cluster", "foreign cluster", "missing reference"} {
		t.Run(mode, func(t *testing.T) {
			m, s, _ := fixtureMonitor()
			ctx := context.Background()
			if e := m.Refresh(ctx); e != nil {
				t.Fatal(e)
			}
			rt := s.Runtimes[0]
			base := m.sources[connection(rt)]
			key, want := "shop/db", "linked"
			sw := mode == "same cluster" || mode == "foreign cluster"
			if sw {
				key = "service-web"
			}
			if mode == "local reference" {
				m.references[rt.ID] = map[string]string{key: "linked"}
			} else {
				other := rt
				other.ID = "other"
				other.Endpoint = "unix:///other-fixture.sock"
				other.Type = model.RuntimeDocker
				if sw {
					other.Type = model.RuntimeSwarm
				}
				m.runtimes[other.ID] = other
				src := base
				// A reference-only observation must not replace the selected
				// runtime's newer workload inventory when both see one engine.
				src.At = base.At.Add(-time.Second)
				src.Containers = nil
				src.Tasks = nil
				src.Services = nil
				src.Nodes = nil
				if mode == "foreign engine" {
					src.Info.ID = "foreign-engine"
					src.Info.Swarm.NodeID = "foreign-node"
				}
				if mode == "foreign cluster" {
					cluster := *src.Info.Swarm.Cluster
					cluster.ID = "foreign-cluster"
					src.Info.Swarm.Cluster = &cluster
					src.Info.Swarm.NodeID = "foreign-node"
				}
				m.sources[connection(other)] = src
				m.references[other.ID] = map[string]string{key: "linked"}
				if mode == "missing reference" {
					m.references[other.ID] = map[string]string{}
				}
			}
			if mode == "foreign engine" || mode == "foreign cluster" || mode == "missing reference" {
				want = ""
			}
			inv, e := m.Inventory(ctx, rt.ID, false)
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, w := range inv.Workloads {
				if (sw && w.ID == "swarm:service-web") || (!sw && w.Kind == "compose") {
					found = true
					if w.ServiceID != want {
						t.Fatal("reference crossed runtime identity", w.ServiceID, want)
					}
				}
			}
			if !found {
				t.Fatal("observed workload missing")
			}
		})
	}
}
func TestMetricEvidenceExpiresAndReadResultsCannotRewriteCache(t *testing.T) {
	now := time.Now().UTC()
	for _, mode := range []string{"none", "no success", "expired", "empty points", "fresh", "recent failure", "old success"} {
		t.Run(mode, func(t *testing.T) {
			m := &Monitor{metrics: map[string]*series{}, now: func() time.Time { return now }}
			success := Metric{At: now}
			s := &series{LastSuccess: &success}
			switch mode {
			case "no success":
				s.LastSuccess = nil
			case "expired":
				success.At = now.Add(-retention - time.Second)
				s.Points = []Metric{success}
			case "fresh":
				s.Points = []Metric{{At: now.Add(-retention - time.Second)}, success}
			case "recent failure":
				s.Points = []Metric{success, {At: now, Error: "unavailable"}}
			case "old success":
				success.At = now.Add(-31 * time.Second)
			}
			if mode != "none" {
				m.metrics["engine/container"] = s
			}
			v := m.Metrics("engine", "container")
			if mode == "none" || mode == "no success" || mode == "expired" {
				if v.Current != nil || v.UnavailableReason != "collecting" {
					t.Fatal("missing evidence became current", v)
				}
				return
			}
			if v.Current == nil {
				t.Fatal("last confirmed evidence lost")
			}
			wantStale := mode == "recent failure" || mode == "old success"
			if v.Current.Stale != wantStale {
				t.Fatal("stale evidence misrepresented", v)
			}
			if mode == "fresh" && len(v.Samples) != 1 {
				t.Fatal("expired samples retained", v)
			}
			v.Current.Stale = true
			v.Current.Error = "caller change"
			if s.LastSuccess.Error != "" || s.LastSuccess.Stale {
				t.Fatal("response mutation rewrote cache")
			}
		})
	}
}
