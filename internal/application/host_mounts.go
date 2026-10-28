package application

import (
	"fmt"
	"net"
	"path"
	"regexp"
	"strconv"
	"strings"
)

var nodeIdentity = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var containerIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// RuntimeNodeID only accepts the explicitly provisioned node transport. A user
// supplied placement constraint cannot turn an unbound Docker endpoint into a
// trusted host identity.
func RuntimeNodeID(endpoint string) string {
	if strings.HasPrefix(endpoint, "node://") {
		value := strings.TrimPrefix(endpoint, "node://")
		if nodeIdentity.MatchString(value) {
			return value
		}
	}
	return ""
}

func restrictedToNode(raw any, nodeID string) (bool, error) {
	deploy, _ := stringMap(raw)
	placement, _ := stringMap(deploy["placement"])
	constraints, _ := placement["constraints"].([]any)
	restricted := false
	for _, rawConstraint := range constraints {
		constraint, ok := rawConstraint.(string)
		if !ok {
			return false, fmt.Errorf("placement constraints must be strings")
		}
		parts := strings.Split(constraint, "==")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != "node.id" {
			continue
		}
		requested := strings.TrimSpace(parts[1])
		if nodeID == "" {
			continue
		}
		if requested != nodeID {
			return false, fmt.Errorf("node.id constraint must match the bound runtime node")
		}
		restricted = true
	}
	return restricted, nil
}

func validateBindPath(source, target string) error {
	for _, value := range []string{source, target} {
		if !strings.HasPrefix(value, "/") || value == "/" || path.Clean(value) != value || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("bind mount source and target must be clean absolute paths below the filesystem root")
		}
	}
	for _, root := range []string{"/proc", "/sys", "/dev", "/run", "/var/run"} {
		if source == root || strings.HasPrefix(source, root+"/") {
			return fmt.Errorf("bind mounts of host system interfaces are forbidden")
		}
	}
	return nil
}

func validateBindMount(value map[string]any) error {
	source, _ := value["source"].(string)
	target, _ := value["target"].(string)
	if err := validateBindPath(source, target); err != nil {
		return err
	}
	for key := range value {
		if key != "type" && key != "source" && key != "target" && key != "read_only" && key != "bind" {
			return fmt.Errorf("unsupported bind mount field %s", key)
		}
	}
	if raw, exists := value["read_only"]; exists {
		if _, ok := raw.(bool); !ok {
			return fmt.Errorf("bind mount read_only must be a boolean")
		}
	}
	if raw, exists := value["bind"]; exists {
		options, ok := stringMap(raw)
		if !ok {
			return fmt.Errorf("bind mount options must be a mapping")
		}
		for key, v := range options {
			if key != "create_host_path" || v != false {
				return fmt.Errorf("bind mounts must preserve existing paths; only create_host_path: false is supported")
			}
		}
	}
	return nil
}

func validateLocalFields(service map[string]any, restricted bool) error {
	if !restricted {
		return nil
	}
	if raw, exists := service["container_name"]; exists {
		name, _ := raw.(string)
		if !containerIdentity.MatchString(name) {
			return fmt.Errorf("container_name must be a valid container name")
		}
	}
	if raw, exists := service["extra_hosts"]; exists {
		hosts, ok := stringMap(raw)
		if !ok {
			return fmt.Errorf("extra_hosts must be a hostname to address mapping")
		}
		for host, rawAddress := range hosts {
			address, _ := rawAddress.(string)
			if host == "" || strings.ContainsAny(host, " \t\r\n/:\x00") || (net.ParseIP(address) == nil && address != "host-gateway") {
				return fmt.Errorf("extra_hosts requires explicit IP addresses or host-gateway")
			}
		}
	}
	return nil
}

func validateTmpfsMount(value map[string]any) error {
	target, _ := value["target"].(string)
	if !strings.HasPrefix(target, "/") || target == "/" || path.Clean(target) != target || strings.ContainsAny(target, "\x00\r\n") {
		return fmt.Errorf("tmpfs target must be a clean absolute path below the filesystem root")
	}
	for key := range value {
		if key != "type" && key != "target" && key != "tmpfs" {
			return fmt.Errorf("unsupported tmpfs mount field %s", key)
		}
	}
	if value["tmpfs"] == nil {
		return nil
	}
	options, ok := stringMap(value["tmpfs"])
	if !ok {
		return fmt.Errorf("tmpfs options must be a mapping")
	}
	for key, raw := range options {
		number, err := strconv.ParseInt(fmt.Sprint(raw), 10, 64)
		if key != "size" || err != nil || number <= 0 {
			return fmt.Errorf("tmpfs supports only a positive size in bytes")
		}
	}
	return nil
}
