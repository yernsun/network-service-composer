package install

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

func (i *Installer) preflight(ctx context.Context, o Observation) error {
	if i.Config.AdminPasswordFile != "" {
		password, err := bootstrap.ReadPrivate(i.Config.AdminPasswordFile)
		if err != nil {
			return err
		}
		size := len(strings.TrimSpace(string(password)))
		clear(password)
		if size < 16 || size > 72 {
			return fmt.Errorf("administrator password requires 16–72 bytes")
		}
	}
	for _, n := range i.Config.Nodes {
		if _, err := strconv.ParseUint(o.GIDs[n.Name], 10, 32); err != nil {
			return fmt.Errorf("node Docker socket group is invalid")
		}
		host := i.Hosts[n.Name]
		command := `set -eu
stat -c '%a' /var/run/docker.sock
nsc_parent=` + bootstrap.ShellQuote(i.Config.NodeDir) + `
while test ! -e "$nsc_parent"; do nsc_parent=${nsc_parent%/*}; test -n "$nsc_parent" || nsc_parent=/; done
df -Pk "$nsc_parent" | awk 'NR==2 {print $4}'
` + listenerEvidenceCommand + "\n"
		data, err := host.Run(ctx, host.Privileged(command), nil)
		if err != nil {
			return fmt.Errorf("node %s preflight could not verify socket, disk and listeners", n.Name)
		}
		lines := strings.Fields(string(data))
		if len(lines) < 2 {
			return fmt.Errorf("node %s preflight evidence is incomplete", n.Name)
		}
		mode, err := strconv.ParseUint(lines[0], 8, 32)
		if err != nil || mode&0o060 != 0o060 {
			return fmt.Errorf("Docker socket group must permit non-root Controller read/write access")
		}
		free, err := strconv.ParseUint(lines[1], 10, 64)
		if err != nil || free < 2*1024*1024 {
			return fmt.Errorf("node %s requires at least 2 GiB free provisioning space", n.Name)
		}
		for _, endpoint := range lines[2:] {
			_, port, ok := strings.Cut(endpoint, ":")
			if !ok {
				return fmt.Errorf("listener evidence is invalid")
			}
			value, err := strconv.ParseUint(port, 16, 16)
			if err != nil {
				return fmt.Errorf("listener evidence is invalid")
			}
			if i.reservedPort(value) {
				return fmt.Errorf("node %s has an occupied bootstrap listener port", n.Name)
			}
		}
		identity := i.Record.Nodes[n.Name]
		if identity.EngineID != "" && identity.EngineID != o.Nodes[n.Name].ID {
			return fmt.Errorf("Docker Engine differs from the frozen installation identity")
		}
		identity.SocketGID = o.GIDs[n.Name]
		identity.EngineID = o.Nodes[n.Name].ID
		identity.NodeID = o.Nodes[n.Name].Swarm.NodeID
		i.Record.Nodes[n.Name] = identity
	}
	return i.Journal.Save(i.Record)
}

func (i *Installer) reservedPort(value uint64) bool {
	return value == 80 || value == 443 || value == uint64(i.Config.ConsolePort) || value == 9443 || value == 9444
}

// Kernel socket tables repeat a header for every file. UDP sockets have a
// different state from TCP listeners, so both must be checked explicitly.
const listenerEvidenceCommand = `awk 'FNR > 1 && (index(FILENAME, "udp") || $4 == "0A") {print $2}' /proc/net/tcp /proc/net/tcp6 /proc/net/udp /proc/net/udp6`
