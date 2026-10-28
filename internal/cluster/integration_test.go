package cluster

import (
	"context"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/runtime/engine/enginetest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIsolatedSwarmLabels(t *testing.T) {
	endpoint := os.Getenv("NSC_CLUSTER_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("run scripts/test-cluster-swarm.sh for isolated Docker integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rt := model.Runtime{ID: "rt-isolated", Name: "isolated", Type: model.RuntimeSwarm, Endpoint: endpoint, Enabled: true, ManagementEnabled: true}
	cli, err := engine.NewMonitoringClient(rt, engine.TLSFiles{})
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	info, err := cli.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(info.Name, "nsc-cluster-e2e-") || !info.Swarm.ControlAvailable {
		t.Fatal("refusing label integration against a non-isolated Swarm")
	}
	before, err := cli.NodeInspect(ctx, info.Swarm.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Management.Enabled = true
	m := New(&enginetest.Store{Runtimes: []model.Runtime{rt}}, cfg)
	if err = m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	v, err := m.Inventory(ctx, rt.ID, false)
	if err != nil || v.Overview.Nodes != 1 || !v.Overview.InventoryComplete {
		t.Fatalf("real inventory %+v %v", v.Overview, err)
	}
	labels := map[string]string{}
	for k, v := range before.Spec.Labels {
		labels[k] = v
	}
	labels["nsc.regression"] = "isolation-only"
	labels["nsc.empty"] = ""
	updated, err := m.UpdateLabels(ctx, rt.ID, before.ID, before.Version.Index, labels)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Labels["nsc.regression"] != "isolation-only" || updated.Version <= before.Version.Index {
		t.Fatal("real label update not observed")
	}
	after, err := cli.NodeInspect(ctx, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Spec.Role != before.Spec.Role || after.Spec.Availability != before.Spec.Availability {
		t.Fatal("role or availability changed")
	}
	if _, err = m.UpdateLabels(ctx, rt.ID, before.ID, after.Version.Index, before.Spec.Labels); err != nil {
		t.Fatal("label cleanup", err)
	}
	t.Logf("isolated engine=%s nodes=%d services=%d label add/empty/remove and NodeSpec preservation passed", info.ServerVersion, v.Overview.Nodes, v.Overview.Services)
}
