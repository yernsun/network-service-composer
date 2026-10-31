package install

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

type interceptedHost struct {
	base      Engine
	intercept func(context.Context, string, string, any, any) (bool, error)
}

func (h interceptedHost) Engine(c context.Context, m, p string, in, out any) error {
	if h.intercept != nil {
		handled, err := h.intercept(c, m, p, in, out)
		if handled {
			return err
		}
	}
	return h.base.Engine(c, m, p, in, out)
}
func (h interceptedHost) Run(c context.Context, s string, b []byte) ([]byte, error) {
	return h.base.Run(c, s, b)
}
func (h interceptedHost) Privileged(s string) string { return h.base.Privileged(s) }
func (h interceptedHost) Stream(c context.Context, s string) (net.Conn, error) {
	return h.base.Stream(c, s)
}

func TestAcknowledgedSwarmInitializationCanResumeWithoutReinitializing(t *testing.T) {
	i, c := installerFixture(t, 1)
	if _, err := i.Plan(t.Context(), bootstrap.Init); err != nil {
		t.Fatal(err)
	}
	host := i.Hosts["one"]
	i.Hosts["one"] = interceptedHost{base: host, intercept: func(ctx context.Context, m, p string, in, out any) (bool, error) {
		if p == "/info" && len(c.nodes) > 0 {
			var info NodeInfo
			if err := host.Engine(ctx, m, p, in, &info); err != nil {
				return true, err
			}
			info.Swarm.ControlAvailable = false
			return true, encode(info, out)
		}
		return false, nil
	}}
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	receipt, err := i.prepareSwarm(ctx, "one")
	if err == nil || receipt.ID == "" || len(c.nodes) != 1 {
		t.Fatal("acknowledged Node ID was lost", err)
	}
	i.Hosts["one"] = host
	result, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "swarm:one", Receipt: &receipt, State: bootstrap.Unknown})
	var retry *bootstrap.RetrySafe
	if result != nil || !errors.As(err, &retry) {
		t.Fatal("acknowledged initialization was not safely retryable", err)
	}
	if _, err = i.prepareSwarm(t.Context(), "one"); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, call := range c.calls {
		if call == "POST /swarm/init" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("resume reinitialized Swarm")
	}
}

func TestSwarmManagerAndMembershipFailuresNeverRebuildTheCluster(t *testing.T) {
	for _, scenario := range []string{"init-unavailable", "join-unavailable", "tokens-unavailable", "wrong-token-cluster", "node-unavailable", "label-update-unavailable", "changed-engine", "changed-cluster", "worker-entry"} {
		t.Run(scenario, func(t *testing.T) {
			i, c := installerFixture(t, 3)
			if _, err := i.Plan(t.Context(), bootstrap.Init); err != nil {
				t.Fatal(err)
			}
			key := "one"
			if strings.HasPrefix(scenario, "join") || scenario == "tokens-unavailable" || scenario == "wrong-token-cluster" {
				if _, err := i.prepareSwarm(t.Context(), "one"); err != nil {
					t.Fatal(err)
				}
				key = "two"
			}
			switch scenario {
			case "init-unavailable":
				c.fail = "POST /swarm/init"
			case "join-unavailable":
				c.fail = "POST /swarm/join"
			case "tokens-unavailable":
				c.fail = "GET /swarm"
			case "wrong-token-cluster":
				i.Record.ClusterID = "foreign"
			case "node-unavailable":
				c.fail = "GET /nodes/node-one"
			case "label-update-unavailable":
				c.fail = "POST /nodes/node-one/update?version=1"
			case "changed-engine":
				i.Hosts["one"].(*fakeHost).engineID = "replaced"
			case "changed-cluster", "worker-entry":
				if _, err := i.prepareSwarm(t.Context(), "one"); err != nil {
					t.Fatal(err)
				}
				if scenario == "changed-cluster" {
					i.Hosts["one"].(*fakeHost).clusterID = "foreign"
				} else {
					i.Hosts["one"].(*fakeHost).worker = true
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
			defer cancel()
			if _, err := i.prepareSwarm(ctx, key); err == nil {
				t.Fatal("unverified Swarm step succeeded")
			}
			for _, call := range c.calls {
				if strings.Contains(call, "/leave") || strings.Contains(call, "/remove") {
					t.Fatal("failure rebuilt or removed Swarm resources")
				}
			}
		})
	}
}

func TestAdditionalWorkerRetainsRoleAndMayUseItsOwnSocketGroup(t *testing.T) {
	i, c := installerFixture(t, 3)
	i.Config.Nodes = append(i.Config.Nodes, bootstrap.Node{Name: "worker", Address: "192.0.2.4", SSHUser: "root", SSHPort: 22})
	i.Hosts["worker"] = &fakeHost{cluster: c, name: "worker", engineID: "engine-worker", gid: "901", architecture: "aarch64", osType: "linux", clusterID: "cluster-id", volumes: map[string]map[string]any{}}
	i.Record.Nodes["worker"] = bootstrap.Identity{InstanceID: "ctrl_worker", GatewayID: "gw_worker", SandboxID: "gw_sandbox"}
	i.Record.ConfigDigest = bootstrap.Digest(i.Config)
	if _, err := i.Plan(t.Context(), bootstrap.Init); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"one", "two", "three", "worker"} {
		if _, err := i.prepareSwarm(t.Context(), key); err != nil {
			t.Fatal(key, err)
		}
	}
	id := i.Record.Nodes["worker"].NodeID
	if c.nodes[id].Spec.Role != swarm.NodeRoleWorker {
		t.Fatal("worker promoted without approval")
	}
	if _, err := i.prepareSwarm(t.Context(), "worker"); err != nil {
		t.Fatal("existing worker cannot be reused", err)
	}
}

func TestLostInspectionAfterCreationRetainsReceiptForReadOnlyReconciliation(t *testing.T) {
	i, c := installerFixture(t, 1)
	infrastructure(t, i)
	expected := fmt.Sprintf("service-%d", c.next+1)
	c.fail = "GET /services/" + expected
	spec := i.serviceSpec("controller", "900", true)
	spec.Name += "-reserved-proof"
	receipt, err := i.createService(t.Context(), spec)
	if err == nil || receipt.ID != expected || receipt.SpecDigest == "" {
		t.Fatal("acknowledged service receipt discarded", err)
	}
	c.fail = ""
	actual, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "reserve:controller", Receipt: &receipt, State: bootstrap.Unknown})
	if err != nil || actual == nil || actual.ID != expected {
		t.Fatal("known service was not reconciled", err)
	}
	service := c.services[expected]
	service.Spec.TaskTemplate.ForceUpdate++
	c.services[expected] = service
	if _, err = i.Reconcile(t.Context(), bootstrap.Step{Key: "reserve:controller", Receipt: &receipt}); err == nil {
		t.Fatal("external service modification adopted")
	}
	attempts := 0
	if err = wait(t.Context(), func() error {
		attempts++
		if attempts == 1 {
			return fmt.Errorf("not ready")
		}
		return nil
	}); err != nil || attempts != 2 {
		t.Fatal("bounded readiness retry failed", err)
	}
}

func TestAcknowledgedJoinResumesWithoutTreatingItAsAnExternalJoin(t *testing.T) {
	i, _ := installerFixture(t, 3)
	infrastructure(t, i)
	identity := i.Record.Nodes["three"]
	receipt := i.Record.Resources["swarm:three"]
	identity.NodeID = ""
	i.Record.Nodes["three"] = identity
	_, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "swarm:three", State: bootstrap.Unknown, Receipt: &receipt})
	var retry *bootstrap.RetrySafe
	if !errors.As(err, &retry) {
		t.Fatal("acknowledged node identity did not permit read-only continuation", err)
	}
	if i.Record.Nodes["three"].NodeID != receipt.ID {
		t.Fatal("acknowledged Node ID was not preserved")
	}
	result, err := i.Apply(t.Context(), bootstrap.Step{Key: "swarm:three"})
	if err != nil || result.ID != receipt.ID {
		t.Fatal("resumed preparation treated its own acknowledged join as foreign", err)
	}
}
