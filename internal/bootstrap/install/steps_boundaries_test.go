package install

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestRecordedResourcesRequireExactIdentitiesBeforeAnyContinuation(t *testing.T) {
	i, c := installerFixture(t, 1)
	infrastructure(t, i)
	for _, key := range []string{"volume:postgresql", "network:control", "secret:postgresql"} {
		t.Run(key, func(t *testing.T) {
			r := i.Record.Resources[key]
			host := i.Hosts["one"].(*fakeHost)
			var labels map[string]any
			switch r.Kind {
			case "volume":
				labels = host.volumes[r.ID]["Labels"].(map[string]any)
			case "network":
				labels = c.networks[r.ID]["Labels"].(map[string]any)
			case "secret":
				labels = c.secrets[r.ID]["Spec"].(map[string]any)["Labels"].(map[string]any)
			}
			original := labels["io.nsc.installation"]
			labels["io.nsc.installation"] = "another-installation"
			if err := i.Check(t.Context(), bootstrap.Step{Key: key, Receipt: &r}); err == nil {
				t.Fatal("foreign resource receipt accepted")
			}
			labels["io.nsc.installation"] = original
			c.fail = "GET /" + map[string]string{"volume": "volumes", "network": "networks", "secret": "secrets"}[r.Kind] + "/" + r.ID
			if err := i.Check(t.Context(), bootstrap.Step{Key: key, Receipt: &r}); err == nil {
				t.Fatal("missing resource accepted")
			}
			c.fail = ""
		})
	}
	h := i.Hosts["one"].(*fakeHost)
	original := h.engineID
	h.engineID = "replaced-engine"
	if err := i.Check(t.Context(), bootstrap.Step{Key: "controller-ready"}); err == nil {
		t.Fatal("replaced engine continued")
	}
	h.engineID = original
	node := h.nodeID
	h.nodeID = "replaced-node"
	if err := i.Check(t.Context(), bootstrap.Step{Key: "controller-ready"}); err == nil {
		t.Fatal("replaced node continued")
	}
	r := i.Record.Resources["swarm:one"]
	if err := i.Check(t.Context(), bootstrap.Step{Key: "swarm:one", Receipt: &r}); err == nil {
		t.Fatal("replaced Swarm receipt accepted")
	}
	h.nodeID = node
	cluster := h.clusterID
	h.clusterID = "another-cluster"
	if err := i.Check(t.Context(), bootstrap.Step{Key: "controller-ready"}); err == nil {
		t.Fatal("foreign manager continued")
	}
	h.clusterID = cluster
	h.gid = "901"
	if _, err := i.socketGID(t.Context()); err == nil {
		t.Fatal("changed socket group accepted")
	}
	h.gid = "invalid"
	if _, err := i.socketGID(t.Context()); err == nil {
		t.Fatal("invalid socket group accepted")
	}
}

func TestUncertainSwarmAndDatabaseStepsRequireReadOnlyProof(t *testing.T) {
	i, c := installerFixture(t, 1)
	infrastructure(t, i)
	r := i.Record.Resources["swarm:one"]
	receipt, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "swarm:one", Receipt: &r, State: bootstrap.Unknown})
	var retry *bootstrap.RetrySafe
	if receipt != nil || !errors.As(err, &retry) {
		t.Fatal("acknowledged node did not require verified continuation", err)
	}
	h := i.Hosts["one"].(*fakeHost)
	original := h.engineID
	h.engineID = "foreign-engine"
	if _, err = i.Reconcile(t.Context(), bootstrap.Step{Key: "swarm:one", Receipt: &r}); err == nil {
		t.Fatal("foreign node reconciled")
	}
	h.engineID = original
	bad := r
	bad.ID = "unknown-node"
	if _, err = i.Reconcile(t.Context(), bootstrap.Step{Key: "swarm:one", Receipt: &bad}); err == nil {
		t.Fatal("unconfirmed node acknowledged")
	}
	if _, err = i.Reconcile(t.Context(), bootstrap.Step{Key: "swarm:external", Receipt: &r}); err == nil {
		t.Fatal("external node selected")
	}
	if _, err = i.prepareSwarm(t.Context(), "one"); err != nil {
		t.Fatal("reusing recorded Manager failed", err)
	}
	node := c.nodes[h.nodeID]
	node.Spec.Role = swarm.NodeRoleWorker
	c.nodes[node.ID] = node
	if _, err = i.prepareSwarm(t.Context(), "one"); err == nil {
		t.Fatal("role changed without authorization")
	}
	node.Spec.Role = swarm.NodeRoleManager
	c.nodes[node.ID] = node
	originalIdentity := i.Record.Nodes["one"]
	changed := originalIdentity
	changed.NodeID = ""
	i.Record.Nodes["one"] = changed
	if _, err = i.prepareSwarm(t.Context(), "one"); err == nil {
		t.Fatal("node joining after approval was claimed")
	}
	i.Record.Nodes["one"] = originalIdentity
	if err = i.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error {
		if err := store.Migrate(t.Context()); err != nil {
			return err
		}
		p := i.seedRecord()
		p.State = bootstrap.Pending
		return writeInstallation(t.Context(), store, p, 0)
	}); err != nil {
		t.Fatal(err)
	}
	_, err = i.Reconcile(t.Context(), bootstrap.Step{Key: "seed"})
	if !errors.As(err, &retry) {
		t.Fatal("verified partial initialization cannot continue", err)
	}
	if err = i.initializeDatabase(t.Context()); err != nil {
		t.Fatal(err)
	}
	receipt, err = i.Reconcile(t.Context(), bootstrap.Step{Key: "seed"})
	if err != nil || receipt == nil {
		t.Fatal("completed initialization not confirmed", err)
	}
	if err = i.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error {
		old, err := store.GetControlDocument(t.Context(), bootstrap.InstallationDocumentID)
		if err != nil {
			return err
		}
		p := i.seedRecord()
		p.ID = "foreign-installation"
		old.Payload, _ = json.Marshal(p)
		return store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: old, Expected: old.Revision}})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = i.Reconcile(t.Context(), bootstrap.Step{Key: "seed"}); err == nil {
		t.Fatal("foreign database claimed")
	}
	if err = i.finalizeDatabase(t.Context()); err == nil {
		t.Fatal("foreign database finalized")
	}
}

func TestDriverStepsPreserveVerificationAndFailureResults(t *testing.T) {
	i, mode := consoleFixture(t)
	// Exercise the actual dispatch used by the durable executor, including final
	// offline writes. Positive NGINX access itself is verified by the separate
	// handoff test and real isolated acceptance.
	for _, key := range []string{"seed", "controller-ready", "gateways-ready", "validation:bootstrap", "validation-ready", "finalize"} {
		r, err := i.Apply(t.Context(), bootstrap.Step{Key: key})
		if err != nil || r.ID == "" {
			t.Fatal(key, err)
		}
		i.Record.Resources[key] = r
	}
	mode.Store("denied")
	for _, key := range []string{"controller-ready", "gateways-ready", "validation-ready", "handoff", "validation:bootstrap"} {
		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
		_, err := i.Apply(ctx, bootstrap.Step{Key: key})
		cancel()
		if err == nil {
			t.Fatal("denied verification succeeded", key)
		}
	}
	delete(i.Record.Resources, "reserve:controller")
	if _, err := i.Apply(t.Context(), bootstrap.Step{Key: "start:controller"}); err == nil {
		t.Fatal("unreserved service updated")
	}
	for _, key := range []string{"controller-ready", "handoff", "gateways-ready", "validation-ready"} {
		if receipt, err := i.Reconcile(t.Context(), bootstrap.Step{Key: key}); err == nil || receipt != nil {
			t.Fatal("read-only step inferred completion")
		}
	}
	var output strings.Builder
	if err := i.Handoff(&output); err != nil || !strings.Contains(output.String(), "certificate_sha256") {
		t.Fatal("handoff not inspectable", err)
	}
}

func TestCreationFailureRetainsKnownIDsAndNeverClaimsByName(t *testing.T) {
	for _, path := range []string{"POST /networks/create", "POST /secrets/create", "POST /volumes/create", "POST /services/create"} {
		t.Run(path, func(t *testing.T) {
			i, c := installerFixture(t, 1)
			infrastructure(t, i)
			c.fail = path
			key := map[string]string{"POST /networks/create": "network:control", "POST /secrets/create": "secret:postgresql", "POST /volumes/create": "volume:postgresql", "POST /services/create": "reserve:controller"}[path]
			if _, err := i.Apply(t.Context(), bootstrap.Step{Key: key}); err == nil {
				t.Fatal("failed create succeeded")
			}
			receipt, err := i.Reconcile(t.Context(), bootstrap.Step{Key: key, State: bootstrap.Unknown})
			if err != nil || receipt != nil {
				t.Fatal("failed create was claimed through names", err)
			}
		})
	}
}

func TestMissingPrivateRuntimeMaterialsBlockWithoutGeneratingReplacements(t *testing.T) {
	for _, name := range []string{"postgres-password", "admin-password", "session-key", "encryption-key", "one-controller.crt", "one-controller.key", "one-gateway.crt", "one-sandbox.key", "ca.crt", "console.key"} {
		t.Run(name, func(t *testing.T) {
			i, _ := installerFixture(t, 1)
			infrastructure(t, i)
			os.Remove(i.material(name))
			if _, err := i.localConfig(); err == nil && (name == "postgres-password" || name == "admin-password" || name == "session-key" || name == "encryption-key") {
				t.Fatal("missing offline credential accepted")
			}
			if _, err := i.nodeFiles(i.Config.Nodes[0]); err == nil {
				t.Fatal("missing runtime identity accepted")
			}
			if _, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "configure:one"}); err == nil {
				t.Fatal("missing key distribution considered complete")
			}
			if _, err := os.Stat(i.material(name)); !os.IsNotExist(err) {
				t.Fatal("missing private material silently regenerated")
			}
		})
	}
}

func TestPlannerRejectsForeignTopologyAndFrozenConfiguration(t *testing.T) {
	i, c := installerFixture(t, 1)
	changed := i.Config
	changed.Name = "another"
	i.Config = changed
	if _, err := i.Plan(t.Context(), bootstrap.Init); err == nil {
		t.Fatal("changed config accepted")
	}
	i.Record.ConfigDigest = bootstrap.Digest(i.Config)
	i.Config.Mode = "unsupported"
	if _, err := i.Plan(t.Context(), bootstrap.Init); err == nil {
		t.Fatal("invalid config accepted")
	}
	i.Config.Mode = "single"
	i.Record.ConfigDigest = bootstrap.Digest(i.Config)
	h := i.Hosts["one"].(*fakeHost)
	h.active = true
	h.nodeID = "node-one"
	h.clusterID = "external"
	i.Record.ClusterID = "expected"
	if _, err := i.Observe(t.Context()); err == nil {
		t.Fatal("foreign recorded cluster accepted")
	}
	i.Record.ClusterID = ""
	c.fail = "GET /services"
	if _, err := i.Observe(t.Context()); err == nil {
		t.Fatal("unobserved services accepted")
	}
	c.fail = "GET /networks"
	if _, err := i.Observe(t.Context()); err == nil {
		t.Fatal("unobserved network conflicts accepted")
	}
}

func TestResourceLookupResponsesCannotRedirectRecordedOwnership(t *testing.T) {
	i, c := installerFixture(t, 1)
	infrastructure(t, i)
	host := i.Hosts["one"].(*fakeHost)
	for _, key := range []string{"volume:postgresql", "network:control", "secret:postgresql", "reserve:controller"} {
		receipt := i.Record.Resources[key]
		switch receipt.Kind {
		case "volume":
			host.volumes[receipt.ID]["Name"] = "foreign"
		case "network":
			c.networks[receipt.ID]["Id"] = "foreign"
		case "secret":
			c.secrets[receipt.ID]["ID"] = "foreign"
		case "service":
			service := c.services[receipt.ID]
			service.ID = "foreign"
			c.services[receipt.ID] = service
		}
		if err := i.Check(t.Context(), bootstrap.Step{Key: key, Receipt: &receipt}); err == nil {
			t.Fatal("recorded resource redirected to another ID", key)
		}
		switch receipt.Kind {
		case "volume":
			host.volumes[receipt.ID]["Name"] = receipt.ID
		case "network":
			c.networks[receipt.ID]["Id"] = receipt.ID
		case "secret":
			c.secrets[receipt.ID]["ID"] = receipt.ID
		case "service":
			service := c.services[receipt.ID]
			service.ID = receipt.ID
			c.services[receipt.ID] = service
		}
	}
	receipt := i.Record.Resources["reserve:controller"]
	c.fail = "GET /services/" + receipt.ID
	if err := i.Check(t.Context(), bootstrap.Step{Key: "reserve:controller", Receipt: &receipt}); err == nil {
		t.Fatal("unavailable service receipt trusted")
	}
	if _, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "reserve:controller", Receipt: &receipt}); err == nil {
		t.Fatal("unavailable service reconciled")
	}
	c.fail = ""
	for _, kind := range []string{"service", "swarm"} {
		receipt := bootstrap.Resource{Kind: kind}
		result, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "reserve:controller", Receipt: &receipt, State: bootstrap.Unknown})
		if err != nil || result != nil {
			t.Fatal("missing acknowledged ID reconstructed", err)
		}
	}
}

func TestResumedSwarmProofRejectsEveryUnreadyOrForeignIdentity(t *testing.T) {
	i, _ := installerFixture(t, 3)
	infrastructure(t, i)
	base := i.Hosts["two"]
	receipt := i.Record.Resources["swarm:two"]
	for _, scenario := range []string{"unavailable", "inactive", "non-manager", "missing-cluster"} {
		i.Hosts["two"] = interceptedHost{base: base, intercept: func(ctx context.Context, m, p string, in, out any) (bool, error) {
			if p != "/info" {
				return false, nil
			}
			if scenario == "unavailable" {
				return true, context.DeadlineExceeded
			}
			var info NodeInfo
			base.Engine(ctx, m, p, in, &info)
			switch scenario {
			case "inactive":
				info.Swarm.LocalNodeState = "inactive"
			case "non-manager":
				info.Swarm.ControlAvailable = false
			case "missing-cluster":
				info.Swarm.Cluster = nil
			}
			return true, encode(info, out)
		}}
		if _, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "swarm:two", Receipt: &receipt}); err == nil {
			t.Fatal("unconfirmed node allowed continuation", scenario)
		}
	}
	i.Hosts["two"] = base
	result, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "swarm:two", Receipt: &receipt})
	var retry *bootstrap.RetrySafe
	if result != nil || !errors.As(err, &retry) {
		t.Fatal("confirmed peer could not continue preparation", err)
	}
}

func TestUnavailableProvisioningDependenciesNeverAdvanceOrReplaceOwnedResources(t *testing.T) {
	i, c := installerFixture(t, 1)
	infrastructure(t, i)
	host := i.Hosts["one"].(*fakeHost)
	host.runErr = context.DeadlineExceeded
	for _, key := range []string{"reserve:controller", "start:controller", "configure:one"} {
		if _, err := i.Apply(t.Context(), bootstrap.Step{Key: key}); err == nil {
			t.Fatal("unavailable provisioning dependency ignored", key)
		}
	}
	if err := i.Check(t.Context(), bootstrap.Step{Key: "configure:one", Receipt: ptrResource(i.Record.Resources["configure:one"])}); err == nil {
		t.Fatal("unreadable configuration trusted")
	}
	host.runErr = nil
	os.Remove(i.material("postgres-password"))
	if _, err := i.Apply(t.Context(), bootstrap.Step{Key: "secret:postgresql"}); err == nil {
		t.Fatal("missing database password replaced")
	}
	if len(c.services) != 4 || len(host.volumes) != 1 || len(c.secrets) != 1 {
		t.Fatal("dependency failures created replacement resources")
	}
}
func ptrResource(r bootstrap.Resource) *bootstrap.Resource { return &r }
