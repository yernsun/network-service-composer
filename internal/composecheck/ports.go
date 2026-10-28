package composecheck

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/docker/go-connections/nat"
)

type publishedPort struct {
	service, mode string
	nat.PortMapping
}

// PortDiagnostics checks the resolved candidate, not the HTTP entrypoint.
// Docker remains the authority for ports already allocated on the runtime.
func PortDiagnostics(doc map[string]any, swarm bool) []Diagnostic {
	diagnostics := []Diagnostic{}
	seen := map[string][]publishedPort{}
	services := mapping(doc["services"])
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		service := mapping(services[name])
		for index, entry := range slice(service["ports"]) {
			ports, mode, code := parsePublishedPort(entry, swarm)
			if code == "" && (service["network_mode"] == "host" || swarm && mode == "ingress" && mapping(service["deploy"])["endpoint_mode"] == "dnsrr") {
				code = "COMPOSE_PORT_NETWORK_CONFLICT"
			}
			if code == "" {
				for _, port := range ports {
					if !fixedPort(port.Binding.HostPort) {
						continue
					}
					key := port.Port.Proto() + "/" + port.Binding.HostPort
					for _, other := range seen[key] {
						// Host-mode services may run on different nodes. Docker checks
						// their allocation; a shared port alone proves no collision.
						if swarm && mode == "host" && other.mode == "host" && name != other.service {
							continue
						}
						if swarm || overlappingAddress(port.Binding.HostIP, other.Binding.HostIP) {
							code = "COMPOSE_PORT_CONFLICT"
							break
						}
					}
					seen[key] = append(seen[key], publishedPort{name, mode, port})
				}
			}
			if code != "" {
				diagnostics = append(diagnostics, issue(code, "error", name, "services", name, "ports", strconv.Itoa(index)))
			}
		}
	}
	return diagnostics
}

func fixedPort(raw string) bool { return raw != "" && raw != "0" && !strings.Contains(raw, "-") }
func overlappingAddress(a, b string) bool {
	return a == b || a == "" || b == "" || a == "0.0.0.0" || b == "0.0.0.0" || a == "::" || b == "::"
}

func parsePublishedPort(entry any, swarm bool) ([]nat.PortMapping, string, string) {
	mode, raw := "ingress", text(entry)
	if value, ok := entry.(map[string]any); ok {
		target := text(value["target"])
		number, err := strconv.Atoi(target)
		if err != nil || number < 1 || number > 65535 {
			return nil, mode, "COMPOSE_PORT_INVALID"
		}
		protocol := text(value["protocol"])
		if protocol == "" {
			protocol = "tcp"
		}
		if v := text(value["mode"]); v != "" {
			mode = v
		}
		if mode != "ingress" && mode != "host" {
			return nil, mode, "COMPOSE_PORT_INVALID"
		}
		published, address := text(value["published"]), text(value["host_ip"])
		if swarm && strings.Contains(published, "-") {
			return nil, mode, "SWARM_PORT_RANGE_UNSUPPORTED"
		}
		raw = fmt.Sprintf("%s:%s/%s", published, target, protocol)
		if address != "" {
			if strings.Contains(address, ":") {
				address = "[" + strings.Trim(address, "[]") + "]"
			}
			raw = address + ":" + raw
		}
	}
	ports, err := nat.ParsePortSpec(raw)
	if err != nil {
		return nil, mode, "COMPOSE_PORT_INVALID"
	}
	for i, port := range ports {
		if port.Port.Int() == 0 || port.Port.Proto() != "tcp" && port.Port.Proto() != "udp" {
			return nil, mode, "COMPOSE_PORT_INVALID"
		}
		if swarm && port.Binding.HostIP != "" {
			return nil, mode, "SWARM_PORT_HOST_IP_UNSUPPORTED"
		}
		if swarm && strings.Contains(port.Binding.HostPort, "-") {
			return nil, mode, "SWARM_PORT_RANGE_UNSUPPORTED"
		}
		if ip := net.ParseIP(port.Binding.HostIP); ip != nil {
			ports[i].Binding.HostIP = ip.String()
		}
	}
	return ports, mode, ""
}
