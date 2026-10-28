package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func TestManagerCapabilityBelongsToFreshSelectedRuntime(t *testing.T) {
	for _, scenario := range []string{"manager", "other collector absent", "standalone", "management disabled", "runtime management disabled", "worker", "failed source", "missing cluster", "missing cluster identity", "missing time", "future time", "stale selected manager"} {
		t.Run(scenario, func(t *testing.T) {
			m, store, _ := fixtureMonitor()
			ctx := context.Background()
			if err := m.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			runtime := store.Runtimes[0]
			key := connection(runtime)
			base := m.sources[key]
			want := false
			switch scenario {
			case "manager":
				want = true
			case "other collector absent":
				for source := range m.sources {
					if source != key {
						delete(m.sources, source)
					}
				}
				want = true
			case "standalone":
				store.Runtimes[0].Type = model.RuntimeDocker
			case "management disabled":
				m.config.Management.Enabled = false
			case "runtime management disabled":
				store.Runtimes[0].ManagementEnabled = false
			case "worker":
				base.Info.Swarm.ControlAvailable = false
			case "failed source":
				base.Error = "unavailable"
			case "missing cluster":
				base.Info.Swarm.Cluster = nil
			case "missing cluster identity":
				copy := *base.Info.Swarm.Cluster
				copy.ID = ""
				base.Info.Swarm.Cluster = &copy
			case "missing time":
				base.At = time.Time{}
			case "future time":
				base.At = m.now().Add(time.Minute)
			case "stale selected manager":
				// A fresh different Manager may supply inventory, but cannot grant this endpoint authority.
				other := base
				other.At = m.now()
				other.Info.ID = "other-manager"
				m.sources["other-manager"] = other
				base.At = m.now().Add(-time.Minute)
			}
			m.sources[key] = base
			inventory, err := m.Inventory(ctx, runtime.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			if inventory.Overview.ManagerAvailable != want {
				t.Fatalf("manager authority=%t want=%t: %+v", inventory.Overview.ManagerAvailable, want, inventory.Overview)
			}
		})
	}
}
