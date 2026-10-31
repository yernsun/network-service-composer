package install

import (
	"path/filepath"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	nettypes "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/swarm"
)

func bind(source, target string, readOnly bool) mount.Mount {
	return mount.Mount{Type: mount.TypeBind, Source: source, Target: target, ReadOnly: readOnly}
}
func network(id string) swarm.NetworkAttachmentConfig {
	return swarm.NetworkAttachmentConfig{Target: id}
}
func port(target, published uint32, protocol nettypes.IPProtocol, mode swarm.PortConfigPublishMode) swarm.PortConfig {
	return swarm.PortConfig{TargetPort: target, PublishedPort: published, Protocol: protocol, PublishMode: mode}
}

func (i *Installer) serviceSpec(key, gid string, reserved bool) swarm.ServiceSpec {
	one := uint64(1)
	delay := 5 * time.Second
	grace := 30 * time.Second
	policy := &swarm.UpdateConfig{Parallelism: 1, Delay: 30 * time.Second, Monitor: 30 * time.Second, FailureAction: swarm.UpdateFailureActionPause, Order: swarm.UpdateOrderStopFirst}
	c := &swarm.ContainerSpec{StopGracePeriod: &grace, ReadOnly: true, Mounts: []mount.Mount{{Type: mount.TypeTmpfs, Target: "/tmp"}}}
	s := swarm.ServiceSpec{Annotations: swarm.Annotations{Name: i.name(key), Labels: map[string]string{"io.nsc.installation": i.Record.ID, "io.nsc.component": key}},
		TaskTemplate: swarm.TaskSpec{ContainerSpec: c, RestartPolicy: &swarm.RestartPolicy{Condition: swarm.RestartPolicyConditionAny, Delay: &delay}, Placement: &swarm.Placement{}},
		Mode:         swarm.ServiceMode{Global: &swarm.GlobalService{}}, UpdateConfig: policy, RollbackConfig: policy, EndpointSpec: &swarm.EndpointSpec{Mode: swarm.ResolutionModeVIP}}
	control := i.Record.Resources["network:control"].ID
	ingress := i.Record.Resources["network:ingress"].ID
	isolated := i.Record.Resources["network:sandbox"].ID
	s.TaskTemplate.Networks = []swarm.NetworkAttachmentConfig{network(control)}
	base := i.nodeDir()
	switch key {
	case "postgresql":
		c.Image = i.Config.Images.PostgreSQL
		c.ReadOnly = false
		c.Env = []string{"POSTGRES_USER=nsc", "POSTGRES_DB=nsc", "POSTGRES_PASSWORD_FILE=/run/secrets/postgres-password", "PGDATA=/var/lib/postgresql/18/docker"}
		c.Mounts = append(c.Mounts, mount.Mount{Type: mount.TypeVolume, Source: i.Record.Resources["volume:postgresql"].ID, Target: "/var/lib/postgresql"})
		c.Secrets = []*swarm.SecretReference{{SecretID: i.Record.Resources["secret:postgresql"].ID, SecretName: i.name("postgresql"), File: &swarm.SecretReferenceFileTarget{Name: "postgres-password", UID: "0", GID: "0", Mode: 0o400}}}
		c.Healthcheck = &container.HealthConfig{Test: []string{"CMD-SHELL", "pg_isready -h 127.0.0.1 -U nsc -d nsc"}, Interval: 3 * time.Second, Timeout: 2 * time.Second, Retries: 30}
		s.Mode = swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &one}}
		s.TaskTemplate.Placement.Constraints = []string{"node.id == " + i.Record.Nodes[i.Config.DataNode].NodeID}
		s.TaskTemplate.Networks = append(s.TaskTemplate.Networks, network(isolated))
	case "controller":
		c.Image = i.Config.Images.Controller
		c.User = "65532:" + gid
		c.Command = []string{"/usr/local/bin/nsc-controller"}
		c.Args = []string{"serve", "--config", "/etc/nsc/controller.yaml"}
		c.Mounts = append(c.Mounts, bind(filepath.Join(base, "controller.yaml"), "/etc/nsc/controller.yaml", true), bind(filepath.Join(base, "controller-secrets"), "/run/nsc", true), bind("/var/run/docker.sock", "/var/run/docker.sock", true))
		c.Healthcheck = &container.HealthConfig{Test: []string{"CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/readyz"}, Interval: 5 * time.Second, Timeout: 3 * time.Second, Retries: 3, StartPeriod: 120 * time.Second}
		s.TaskTemplate.Networks = append(s.TaskTemplate.Networks, network(ingress))
		s.TaskTemplate.Placement.Constraints = []string{"node.labels.io.nsc.controller." + i.Record.ID + " == true"}
		s.EndpointSpec.Ports = []swarm.PortConfig{port(8443, uint32(i.Config.ConsolePort), nettypes.TCP, swarm.PortConfigPublishModeIngress), port(9443, 9443, nettypes.TCP, swarm.PortConfigPublishModeHost), port(9444, 9444, nettypes.TCP, swarm.PortConfigPublishModeHost)}
	case "nginx", "nginx-sandbox":
		c.Image = i.Config.Images.Gateway
		c.User = "101:101"
		c.Command = []string{"/usr/local/bin/nsc-agent"}
		c.Args = []string{"--config", "/etc/nsc/agent.yaml"}
		role := "gateway"
		if key == "nginx-sandbox" {
			role = "sandbox"
		}
		c.Sysctls = map[string]string{"net.ipv4.ip_unprivileged_port_start": "0"}
		c.Mounts = append(c.Mounts, bind(filepath.Join(base, role+".yaml"), "/etc/nsc/agent.yaml", true), bind(filepath.Join(base, role+"-secrets"), "/run/nsc", true), bind(filepath.Join(base, role+"-state"), "/var/lib/nsc", false), bind(filepath.Join(base, role+"-cache"), "/var/cache/nginx", false))
		c.Healthcheck = &container.HealthConfig{Test: []string{"CMD", "/usr/local/bin/nsc-agent", "--healthcheck", "--config", "/etc/nsc/agent.yaml"}, Interval: 5 * time.Second, Timeout: 5 * time.Second, Retries: 3, StartPeriod: 60 * time.Second}
		if key == "nginx" {
			s.TaskTemplate.Networks = append(s.TaskTemplate.Networks, network(ingress))
			s.TaskTemplate.Placement.Constraints = []string{"node.labels.io.nsc.controller." + i.Record.ID + " == true"}
			s.EndpointSpec.Ports = []swarm.PortConfig{port(80, 80, nettypes.TCP, swarm.PortConfigPublishModeIngress), port(443, 443, nettypes.TCP, swarm.PortConfigPublishModeIngress), port(443, 443, nettypes.UDP, swarm.PortConfigPublishModeIngress)}
		} else {
			// Sandbox readiness is authenticated heartbeat plus a real validation.
			// Idle Sandbox processes deliberately have no production generation.
			c.Healthcheck = &container.HealthConfig{Test: []string{"NONE"}}
			s.Mode = swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &one}}
			s.TaskTemplate.Networks = []swarm.NetworkAttachmentConfig{network(isolated)}
			s.TaskTemplate.Placement.Constraints = []string{"node.id == " + i.Record.Nodes[i.Config.DataNode].NodeID}
		}
	}
	if reserved {
		s.TaskTemplate.Placement.Constraints = []string{"node.id == 0000000000000000000000000"}
	}
	return s
}
