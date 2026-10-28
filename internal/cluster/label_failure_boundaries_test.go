package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type labelBoundaryClient struct {
	*collectionBoundaryClient
	inspectCalls, updates int
	mode                  string
}

func (c *labelBoundaryClient) Info(ctx context.Context) (system.Info, error) {
	if c.mode == "info" {
		return system.Info{}, c.failure
	}
	return c.Client.Info(ctx)
}
func (c *labelBoundaryClient) NodeInspect(ctx context.Context, key string) (swarm.Node, error) {
	c.inspectCalls++
	if c.mode == "inspect" || c.mode == "reinspect failed" && c.inspectCalls == 2 || c.mode == "post-write inspect" && c.inspectCalls == 2 {
		return swarm.Node{}, c.failure
	}
	return c.Client.NodeInspect(ctx, key)
}
func (c *labelBoundaryClient) NodeUpdate(ctx context.Context, key string, version swarm.Version, spec swarm.NodeSpec) error {
	c.updates++
	if c.mode == "update" || c.mode == "reinspect failed" {
		return c.failure
	}
	return c.Client.NodeUpdate(ctx, key, version, spec)
}
func TestNodeLabelFailuresDoNotRetryOrOverwriteNewerCache(t *testing.T) {
	for _, mode := range []string{"missing runtime", "disabled runtime", "Docker runtime", "factory", "info", "inspect", "update", "reinspect failed", "post-write inspect", "newer cache"} {
		t.Run(mode, func(t *testing.T) {
			m, s, base := fixtureMonitor()
			ctx := context.Background()
			if e := m.Refresh(ctx); e != nil {
				t.Fatal(e)
			}
			failure := errors.New("node update dependency unavailable")
			client := &labelBoundaryClient{collectionBoundaryClient: &collectionBoundaryClient{Client: base, failure: failure}, mode: mode}
			m.factory = func(model.Runtime, engine.TLSFiles) (engine.MonitoringClient, error) {
				if mode == "factory" {
					return nil, failure
				}
				return client, nil
			}
			runtimeID := s.Runtimes[0].ID
			want := error(failure)
			switch mode {
			case "missing runtime":
				runtimeID = "missing"
				want = storage.ErrNotFound
			case "disabled runtime":
				s.Runtimes[0].Enabled = false
				want = ErrReadOnly
			case "Docker runtime":
				s.Runtimes[0].Type = model.RuntimeDocker
				want = ErrReadOnly
			case "newer cache":
				for k, v := range m.sources {
					nodes := append([]swarm.Node(nil), v.Nodes...)
					for i := range nodes {
						if nodes[i].ID == "node-a" {
							nodes[i].Version.Index = 100
							nodes[i].Spec.Labels = map[string]string{"newer": "preserve"}
						}
					}
					v.Nodes = nodes
					m.sources[k] = v
				}
			}
			updated, e := m.UpdateLabels(ctx, runtimeID, "node-a", 7, map[string]string{"zone": "changed"})
			if mode == "newer cache" {
				if e != nil || updated.Labels["zone"] != "changed" {
					t.Fatal("successful update response lost", updated, e)
				}
				for _, v := range m.sources {
					for _, n := range v.Nodes {
						if n.ID == "node-a" && (n.Version.Index != 100 || n.Spec.Labels["newer"] != "preserve") {
							t.Fatal("older completion overwrote newer cache", n)
						}
					}
				}
			} else if !errors.Is(e, want) {
				t.Fatal("label failure changed identity", e, want)
			}
			if client.updates > 1 {
				t.Fatal("failed label mutation was automatically retried")
			}
			noClient := mode == "missing runtime" || mode == "disabled runtime" || mode == "Docker runtime" || mode == "factory"
			if !noClient && client.closed.Load() != 1 {
				t.Fatal("label operation leaked Docker client")
			}
			if noClient || mode == "info" || mode == "inspect" {
				if client.updates != 0 {
					t.Fatal("label write bypassed failed preflight")
				}
			}
		})
	}
}
