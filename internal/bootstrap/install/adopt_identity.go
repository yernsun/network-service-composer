package install

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/pki"
	"github.com/yernsun/network-service-composer/internal/storage"
	"gopkg.in/yaml.v3"
)

// Read only a file already mounted by the explicitly registered service. The
// existing service ID and spec are approved evidence; names/labels cannot select
// another service or authorize provisioning a replacement identity.
func (i *Installer) registeredFile(ctx context.Context, node string, service swarm.Service, filename string, private ...bool) ([]byte, error) {
	if !path.IsAbs(filename) || path.Clean(filename) != filename {
		return nil, fmt.Errorf("registered component file is not an absolute clean path")
	}
	host := i.Hosts[node]
	read := func(source string) ([]byte, error) {
		command := "cat -- " + bootstrap.ShellQuote(source)
		if len(private) > 0 && private[0] {
			command = "set -eu\nnsc_mode=$(stat -c '%a' -- " + bootstrap.ShellQuote(source) + ")\ntest \"$((0$nsc_mode & 077))\" -eq 0\n" + command
		}
		return host.Run(ctx, host.Privileged(command), nil)
	}
	for _, m := range service.Spec.TaskTemplate.ContainerSpec.Mounts {
		source := ""
		switch m.Type {
		case mount.TypeBind:
			source = m.Source
		case mount.TypeVolume:
			var volume struct{ Name, Mountpoint string }
			if err := host.Engine(ctx, "GET", "/volumes/"+m.Source, nil, &volume); err != nil {
				return nil, err
			}
			if volume.Name != m.Source {
				return nil, fmt.Errorf("registered file volume identity differs")
			}
			source = volume.Mountpoint
		}
		if source == "" {
			continue
		}
		if filename == m.Target {
			return read(source)
		}
		if strings.HasPrefix(filename, m.Target+"/") {
			relative := strings.TrimPrefix(filename, m.Target+"/")
			return read(path.Join(source, relative))
		}
	}
	for _, secret := range service.Spec.TaskTemplate.ContainerSpec.Secrets {
		if secret.File == nil {
			continue
		}
		target := secret.File.Name
		if !path.IsAbs(target) {
			target = path.Join("/run/secrets", target)
		}
		if target != filename {
			continue
		}
		container, err := i.registeredContainer(ctx, node, service.ID)
		if err != nil {
			return nil, err
		}
		// Swarm provides the isolated secret mount; values never leave the verified
		// channel for a public log, Docker service specification or journal.
		return host.Run(ctx, host.Privileged("docker exec -i "+bootstrap.ShellQuote(container)+" cat -- "+bootstrap.ShellQuote(filename)), nil)
	}
	return nil, fmt.Errorf("registered component file has no verified mount or Secret reference")
}

func agentConfigPath(service swarm.Service) string {
	args := append(append([]string{}, service.Spec.TaskTemplate.ContainerSpec.Command...), service.Spec.TaskTemplate.ContainerSpec.Args...)
	for n, arg := range args {
		if arg == "--config" && n+1 < len(args) {
			return args[n+1]
		}
		if strings.HasPrefix(arg, "--config=") {
			return strings.TrimPrefix(arg, "--config=")
		}
	}
	return "/etc/nsc/agent.yaml"
}

func (i *Installer) registeredGateway(ctx context.Context, node string, service swarm.Service, expectedMode, pool string, caPEM []byte) (string, bool, error) {
	data, err := i.registeredFile(ctx, node, service, agentConfigPath(service))
	if err != nil {
		return "", false, err
	}
	cfg := config.Defaults()
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return "", false, fmt.Errorf("registered agent configuration cannot be decoded")
	}
	if cfg.Agent.Mode != expectedMode || cfg.Agent.PoolID != pool {
		return "", false, fmt.Errorf("registered agent role or Pool differs")
	}
	expected, err := pki.GatewayIdentityURI(pool, cfg.Agent.GatewayID)
	if err != nil {
		return "", false, err
	}
	cert, err := i.registeredFile(ctx, node, service, cfg.Agent.TLSCertFile)
	if err != nil {
		return "", false, err
	}
	expired, err := verifyRegisteredCertificate(cert, caPEM, expected.String())
	if err != nil {
		return "", false, err
	}
	return cfg.Agent.GatewayID, expired, nil
}

func (i *Installer) adoptionGatewayIdentities(ctx context.Context, cfg config.Config, services map[string]swarm.Service, unavailable []string) ([]string, error) {
	ca, err := i.registeredFile(ctx, i.Config.MaintenanceNode, services["controller"], cfg.Server.GRPCClientCAFile)
	if err != nil {
		return nil, err
	}
	drift := []string{}
	for _, node := range i.Config.Nodes {
		if !node.Manager {
			continue
		}
		if contains(unavailable, node.Name) {
			drift = append(drift, "node_identity_unverified:"+i.Record.Nodes[node.Name].NodeID)
			continue
		}
		controllerCert, err := i.registeredFile(ctx, node.Name, services["controller"], cfg.Controller.Internal.CertFile)
		if err != nil {
			return nil, err
		}
		expected, err := controlplane.IdentityURI(i.Record.Nodes[node.Name].InstanceID)
		if err != nil {
			return nil, err
		}
		expired, err := verifyRegisteredCertificate(controllerCert, ca, expected.String())
		if err != nil {
			return nil, err
		}
		if expired {
			drift = append(drift, "controller_identity_expired:"+i.Record.Nodes[node.Name].NodeID)
		}
		id, expired, err := i.registeredGateway(ctx, node.Name, services["nginx"], "production", i.Record.PoolID, ca)
		if err != nil {
			return nil, err
		}
		binding := i.Record.Nodes[node.Name]
		binding.GatewayID = id
		i.Record.Nodes[node.Name] = binding
		if expired {
			drift = append(drift, "gateway_identity_expired:"+binding.NodeID)
		}
	}
	id, expired, err := i.registeredGateway(ctx, i.Config.DataNode, services["nginx-sandbox"], "sandbox", i.Record.PoolID, ca)
	if err != nil {
		return nil, err
	}
	binding := i.Record.Nodes[i.Config.DataNode]
	binding.SandboxID = id
	i.Record.Nodes[i.Config.DataNode] = binding
	if expired {
		drift = append(drift, "sandbox_identity_expired:"+binding.NodeID)
	}
	return drift, nil
}

func verifyRegisteredCertificate(cert, caPEM []byte, expected string) (bool, error) {
	leafBlock, _ := pem.Decode(cert)
	caBlock, _ := pem.Decode(caPEM)
	if leafBlock == nil || caBlock == nil {
		return false, fmt.Errorf("registered trust evidence is invalid")
	}
	leaf, err := x509.ParseCertificate(leafBlock.Bytes)
	if err != nil {
		return false, err
	}
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return false, err
	}
	if err = leaf.CheckSignatureFrom(ca); err != nil {
		return false, fmt.Errorf("registered identity has a different trust authority")
	}
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != expected {
		return false, fmt.Errorf("registered certificate identity differs")
	}
	now := timeNow()
	return now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter), nil
}

func (i *Installer) adoptedConsoleAsset(ctx context.Context, store storage.Store, cfg config.Config, service swarm.Service, expected string) (string, error) {
	filename := cfg.Server.ConsoleHTTPSCertFile
	if filename == "" {
		filename = cfg.Server.HTTPTLSCertFile
	}
	if filename == "" {
		return expected, nil
	}
	data, err := i.registeredFile(ctx, i.Config.MaintenanceNode, service, filename)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", fmt.Errorf("registered console certificate is invalid")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	fingerprint := sha256.Sum256(leaf.Raw)
	selected := ""
	for page := (storage.Page{Limit: 200}); ; {
		values, err := store.ListCertificates(ctx, page)
		if err != nil {
			return "", err
		}
		for _, value := range values {
			if value.PoolID != i.Record.PoolID || value.Fingerprint != hex.EncodeToString(fingerprint[:]) {
				continue
			}
			if expected != "" && value.ID != expected {
				continue
			}
			if selected != "" {
				return "", fmt.Errorf("console certificate asset is ambiguous")
			}
			selected = value.ID
		}
		if len(values) < page.Limit {
			break
		}
		page.After = values[len(values)-1].ID
	}
	if selected == "" {
		return "", fmt.Errorf("existing console certificate requires a matching asset before adoption")
	}
	return selected, nil
}

func (i *Installer) registeredContainer(ctx context.Context, node, serviceID string) (string, error) {
	var info NodeInfo
	if err := i.Hosts[node].Engine(ctx, "GET", "/info", nil, &info); err != nil {
		return "", err
	}
	filters, _ := json.Marshal(map[string][]string{"service": {serviceID}, "desired-state": {"running"}})
	var tasks []swarm.Task
	if err := i.entry().Engine(ctx, "GET", "/tasks?filters="+url.QueryEscape(string(filters)), nil, &tasks); err != nil {
		return "", err
	}
	selected := ""
	for _, task := range tasks {
		if task.ServiceID != serviceID || task.NodeID != info.Swarm.NodeID || task.Status.State != swarm.TaskStateRunning || task.Status.ContainerStatus == nil {
			continue
		}
		if selected != "" || task.Status.ContainerStatus.ContainerID == "" {
			return "", fmt.Errorf("registered Secret task identity is ambiguous")
		}
		selected = task.Status.ContainerStatus.ContainerID
	}
	if selected == "" {
		return "", fmt.Errorf("registered Secret requires the original running component task")
	}
	return selected, nil
}
