package application

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const ApplicationNetworkKey = "nsc_application"

var applicationNetworkName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func ValidateApplicationNetwork(name, gateway, raw string) error {
	if !applicationNetworkName.MatchString(name) || name == gateway || name == "ingress" || name == "docker_gwbridge" {
		return fmt.Errorf("invalid or protected application network name %q", name)
	}
	var document map[string]any
	if err := yaml.Unmarshal([]byte(raw), &document); err != nil {
		return err
	}
	networks, err := optionalMapping(document, "networks")
	if err != nil {
		return err
	}
	if _, exists := networks[ApplicationNetworkKey]; exists {
		return fmt.Errorf("network key %q is reserved for application networking", ApplicationNetworkKey)
	}
	for key, value := range networks {
		definition, err := composeResourceDefinition("network", key, value)
		if err != nil {
			return err
		}
		if configured, _ := definition["name"].(string); configured == name {
			return fmt.Errorf("network %q is owned by application networking", name)
		}
		if external, _ := definition["external"].(bool); external && key == name {
			return fmt.Errorf("external network %q conflicts with application networking", name)
		}
	}
	return nil
}

// DeploymentNetworks normalizes attachments before Compose merges the ingress
// override. Making the implicit default explicit prevents an ingress-only
// override from disconnecting a service from its original business network.
// The saved source is never changed; only the execution material is expanded.
func DeploymentNetworks(raw, name string) (string, error) {
	var document map[string]any
	if err := yaml.Unmarshal([]byte(raw), &document); err != nil {
		return "", err
	}
	services, err := optionalMapping(document, "services")
	if err != nil {
		return "", err
	}
	networks, err := optionalMapping(document, "networks")
	if err != nil {
		return "", err
	}
	if networks == nil {
		networks = map[string]any{}
	}
	for key, value := range services {
		service, ok := value.(map[string]any)
		if !ok {
			return "", fmt.Errorf("service %q must be a mapping", key)
		}
		attachments := map[string]any{}
		switch current := service["networks"].(type) {
		case nil:
		case []any:
			for _, value := range current {
				n, ok := value.(string)
				if !ok {
					return "", fmt.Errorf("service %q network must be a name", key)
				}
				attachments[n] = map[string]any{}
			}
		case map[string]any:
			for n, options := range current {
				attachments[n] = options
			}
		default:
			return "", fmt.Errorf("service %q networks must be a list or mapping", key)
		}
		if len(attachments) == 0 {
			attachments["default"] = map[string]any{}
			if _, exists := networks["default"]; !exists {
				networks["default"] = map[string]any{}
			}
		}
		if name != "" {
			attachments[ApplicationNetworkKey] = map[string]any{}
		}
		service["networks"] = attachments
	}
	if name != "" {
		networks[ApplicationNetworkKey] = map[string]any{"external": true, "name": name}
	}
	if len(networks) > 0 {
		document["networks"] = networks
	}
	encoded, err := yaml.Marshal(document)
	return strings.TrimSpace(string(encoded)) + "\n", err
}

// NamespaceApplicationNetwork prevents an incremental service from changing
// the definition of an application network still referenced by older services.
func NamespaceApplicationNetwork(raw, name string) (string, error) {
	if name == "" {
		return raw, nil
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return "", err
	}
	networks, _ := doc["networks"].(map[string]any)
	key := ApplicationNetworkKey + "_" + name
	if _, exists := networks[key]; exists {
		return "", fmt.Errorf("reserved network key collision")
	}
	if value, ok := networks[ApplicationNetworkKey]; ok {
		networks[key] = value
		delete(networks, ApplicationNetworkKey)
		services, _ := doc["services"].(map[string]any)
		for _, value := range services {
			service, _ := value.(map[string]any)
			refs, _ := service["networks"].(map[string]any)
			if v, ok := refs[ApplicationNetworkKey]; ok {
				refs[key] = v
				delete(refs, ApplicationNetworkKey)
			}
		}
	}
	out, err := yaml.Marshal(doc)
	return string(out), err
}

// An update without an explicit network change keeps NSC-managed attachments
// from the deployed service; it does not inherit the last-added service's network.
func PreserveManagedNetworks(base, next string) (string, error) {
	var before, after map[string]any
	if err := yaml.Unmarshal([]byte(base), &before); err != nil {
		return "", err
	}
	if err := yaml.Unmarshal([]byte(next), &after); err != nil {
		return "", err
	}
	oldServices, _ := before["services"].(map[string]any)
	services, _ := after["services"].(map[string]any)
	oldNetworks, _ := before["networks"].(map[string]any)
	networks, _ := after["networks"].(map[string]any)
	if networks == nil {
		networks = map[string]any{}
		after["networks"] = networks
	}
	for name, value := range services {
		service, _ := value.(map[string]any)
		old, _ := oldServices[name].(map[string]any)
		oldRefs, _ := old["networks"].(map[string]any)
		refs, _ := service["networks"].(map[string]any)
		if refs == nil {
			refs = map[string]any{}
			service["networks"] = refs
		}
		for key, options := range oldRefs {
			if key == ApplicationNetworkKey || strings.HasPrefix(key, ApplicationNetworkKey+"_") {
				refs[key] = options
				networks[key] = oldNetworks[key]
			}
		}
	}
	raw, err := yaml.Marshal(after)
	return string(raw), err
}

// A partial file reuses the Stack's existing implicit default network. Docker
// does not inherit its original Compose definition, so carry that reference
// explicitly before normalizing attachments. An explicit candidate default is
// still reviewed as a resource change.
func InheritImplicitDefaultNetwork(base, candidate string) (string, error) {
	var before, after map[string]any
	if err := yaml.Unmarshal([]byte(base), &before); err != nil {
		return "", err
	}
	if err := yaml.Unmarshal([]byte(candidate), &after); err != nil {
		return "", err
	}
	old, _ := before["networks"].(map[string]any)
	networks, _ := after["networks"].(map[string]any)
	if _, explicit := networks["default"]; explicit {
		return candidate, nil
	}
	definition, exists := old["default"]
	if !exists {
		return candidate, nil
	}
	if networks == nil {
		networks = map[string]any{}
		after["networks"] = networks
	}
	networks["default"] = definition
	out, err := yaml.Marshal(after)
	return string(out), err
}
