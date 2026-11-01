package install

import (
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/bootstrap/maintenance"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/storage"
	"gopkg.in/yaml.v3"
)

func TestLegacyPreparationAcceptsSupportedOriginalLayoutsWithoutMutation(t *testing.T) {
	for _, scenario := range []string{"single-replica", "ordinary-environment", "postgresql-uri", "service-id-dns", "tasks-dns", "reserved-console-port"} {
		t.Run(scenario, func(t *testing.T) {
			installer, host, _ := webPreparationFixture(t)
			service := host.cluster.services[installer.Config.Adoption.ServiceIDs["controller"]]
			cfg := config.Defaults()
			if err := yaml.Unmarshal(host.files["controller.yaml"], &cfg); err != nil {
				t.Fatal(err)
			}
			connection, err := url.Parse(cfg.Storage.DSN)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "reserved-console-port":
				service.Spec.EndpointSpec.Ports = append(service.Spec.EndpointSpec.Ports, port(8443, uint32(installer.Config.ConsolePort), "tcp", swarm.PortConfigPublishModeIngress))
			case "single-replica":
				replicas := uint64(1)
				service.Spec.Mode = swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &replicas}}
			case "ordinary-environment":
				service.Spec.TaskTemplate.ContainerSpec.Env = append(service.Spec.TaskTemplate.ContainerSpec.Env, "LANG=C")
			case "postgresql-uri":
				connection.Scheme = "postgresql"
			case "service-id-dns":
				connection.Host = installer.Record.Database.ServiceID + ":5432"
			case "tasks-dns":
				connection.Host = "tasks." + host.cluster.services[installer.Record.Database.ServiceID].Spec.Name + ":5432"
			}
			cfg.Storage.DSN = connection.String()
			raw, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			host.files["controller.yaml"] = raw
			if err = os.WriteFile(installer.Config.Adoption.ControllerConfigFile, raw, 0600); err != nil {
				t.Fatal(err)
			}
			// Represent the already-adopted receipt of this supported original layout.
			// Both actual object and registration must agree before planning starts.
			host.cluster.services[service.ID] = service
			receipt := serviceReceipt(service)
			installer.Record.Resources["reserve:controller"] = receipt
			adoptStore(t, installer, func(store storage.Store) error {
				repository := maintenance.Repository{Store: store}
				owner, document, err := repository.Installation(t.Context())
				if err != nil {
					return err
				}
				owner.Components["controller"] = receipt
				document.Payload = mustPreparationJSON(owner)
				return store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: document, Expected: document.Revision}})
			})
			before := bootstrap.Digest(host.cluster.services)
			plan, err := installer.PlanWebPreparation(t.Context(), bootstrap.Upgrade)
			if err != nil || plan.Scope.Preparation == nil || bootstrap.Digest(host.cluster.services) != before {
				t.Fatal("supported original layout was changed or rejected", err)
			}
			if scenario == "reserved-console-port" {
				operation, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "reuse-owned-console-port")
				if err != nil || operation.State != bootstrap.Succeeded {
					t.Fatal("the original reserved console port could not be reused", operation.State, err)
				}
			}
		})
	}
}

func TestLegacyPreparationPreservesExistingManagerTopologyAndConsistentSecurity(t *testing.T) {
	for _, scenario := range []string{"two-managers", "additional-worker", "inconsistent-security", "invalid-ca", "missing-ca"} {
		t.Run(scenario, func(t *testing.T) {
			installer, host, _ := webPreparationFixture(t)
			node := bootstrap.Node{Name: "two", Address: "127.0.0.2", SSHUser: "root", SSHPort: 22, Manager: scenario != "additional-worker"}
			installer.Config.Nodes = append(installer.Config.Nodes, node)
			installer.Config.Mode = "multi"
			second := *host.fakeHost
			second.name, second.nodeID, second.engineID = "two", "node-two", "engine-two"
			second.worker = !node.Manager
			second.files = map[string][]byte{}
			for name, data := range host.files {
				second.files[name] = append([]byte{}, data...)
			}
			member := host.cluster.nodes[host.nodeID]
			member.ID = "node-two"
			member.Status.Addr = node.Address
			member.Spec.Role = swarm.NodeRoleWorker
			member.ManagerStatus = nil
			identity := bootstrap.Identity{NodeID: member.ID, EngineID: second.engineID, SocketGID: host.gid}
			if node.Manager {
				member.Spec.Role = swarm.NodeRoleManager
				member.ManagerStatus = &swarm.ManagerStatus{Reachability: swarm.ReachabilityReachable}
				identity.InstanceID, identity.GatewayID = "controller-two", "gateway-two"
				cfg := config.Defaults()
				if err := yaml.Unmarshal(second.files["controller.yaml"], &cfg); err != nil {
					t.Fatal(err)
				}
				cfg.Controller.InstanceID = identity.InstanceID
				raw, err := yaml.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				second.files["controller.yaml"] = raw
			}
			if scenario == "inconsistent-security" {
				second.files["controller-secrets/session-key"] = []byte(strings.Repeat("different-disposable-session", 2))
			}
			if scenario == "invalid-ca" {
				second.files["controller-secrets/ca.crt"] = []byte("invalid public certificate")
			}
			if scenario == "missing-ca" {
				delete(second.files, "controller-secrets/ca.crt")
			}
			host.cluster.nodes[member.ID] = member
			installer.Record.Nodes[node.Name] = identity
			installer.Hosts[node.Name] = &second
			installer.Record.ConfigDigest = bootstrap.Digest(installer.Config)
			if err := installer.Journal.Save(installer.Record); err != nil {
				t.Fatal(err)
			}
			installer.Record.Topology = managerTopology([]swarm.Node{host.cluster.nodes[host.nodeID], member})
			adoptStore(t, installer, func(store storage.Store) error {
				repository := maintenance.Repository{Store: store}
				owner, document, err := repository.Installation(t.Context())
				if err != nil {
					return err
				}
				owner.Nodes = installer.Record.Nodes
				owner.ConfigDigest = installer.Record.ConfigDigest
				owner.Topology = installer.Record.Topology
				document.Payload = mustPreparationJSON(owner)
				return store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: document, Expected: document.Revision}})
			})
			before := bootstrap.Digest(host.cluster.services)
			plan, err := installer.PlanWebPreparation(t.Context(), bootstrap.Upgrade)
			if scenario == "two-managers" || scenario == "additional-worker" {
				if err != nil || plan.Scope.Topology.Managers != installer.Record.Topology.Managers || plan.Scope.Topology.TolerableFailures != 0 {
					t.Fatal("existing topology was replaced or misrepresented", err)
				}
			} else if err == nil {
				t.Fatal("inconsistent Controller security or trust was accepted")
			}
			if bootstrap.Digest(host.cluster.services) != before {
				t.Fatal("planning mutated the existing cluster")
			}
		})
	}
}
