package application

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// PinDataNode derives executable placement from a reviewed node selection.
// Catalog bytes stay immutable; only the application revision is rendered.
func PinDataNode(raw, node string) (string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return "", err
	}
	services, err := optionalMapping(doc, "services")
	if err != nil {
		return "", err
	}
	needsNode := false
	for _, value := range services {
		service, _ := stringMap(value)
		if mounts, _ := service["volumes"].([]any); len(mounts) == 0 {
			continue
		}
		needsNode = true
		if !nodeIdentity.MatchString(node) {
			return "", fmt.Errorf("a valid data node must be selected")
		}
		deploy, _ := stringMap(service["deploy"])
		if deploy == nil {
			deploy = map[string]any{}
		}
		deploy["replicas"] = 1
		placement, _ := stringMap(deploy["placement"])
		if placement == nil {
			placement = map[string]any{}
		}
		constraints := []any{}
		if existing, ok := placement["constraints"].([]any); ok {
			for _, constraint := range existing {
				text, ok := constraint.(string)
				if !ok {
					return "", fmt.Errorf("invalid placement constraint")
				}
				if strings.ReplaceAll(text, " ", "") == "node.id=="+node {
					continue
				}
				if strings.HasPrefix(strings.ReplaceAll(text, " ", ""), "node.id") {
					return "", fmt.Errorf("template must not override the selected data node")
				}
				constraints = append(constraints, text)
			}
		}
		placement["constraints"] = append(constraints, "node.id == "+node)
		deploy["placement"] = placement
		service["deploy"] = deploy
	}
	if !needsNode {
		return raw, nil
	}
	data, err := yaml.Marshal(doc)
	return string(data), err
}

// ValidateDataNodePolicy is also used by the executor, including replica overrides.
func ValidateDataNodePolicy(raw, node string, overrides map[string]int) error {
	if node == "" {
		return nil
	}
	if !nodeIdentity.MatchString(node) {
		return fmt.Errorf("invalid data node")
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return err
	}
	services, err := optionalMapping(doc, "services")
	if err != nil {
		return err
	}
	usedVolumes := map[string]string{}
	for name, value := range services {
		service, _ := stringMap(value)
		mounts, _ := service["volumes"].([]any)
		if len(mounts) == 0 {
			continue
		}
		deploy, _ := stringMap(service["deploy"])
		restricted, err := restrictedToNode(deploy, node)
		if err != nil || !restricted {
			return fmt.Errorf("service %s must retain its data node", name)
		}
		if deploy["mode"] == "global" {
			return fmt.Errorf("persistent service %s cannot use global mode", name)
		}
		replicas := fmt.Sprint(deploy["replicas"])
		if replicas != "1" {
			return fmt.Errorf("service %s must have one replica", name)
		}
		if replicas, exists := overrides[name]; exists && replicas != 1 {
			return fmt.Errorf("service %s cannot change persistent replicas", name)
		}
		for _, key := range []string{"update_config", "rollback_config"} {
			policy, _ := stringMap(deploy[key])
			if policy["order"] != "stop-first" {
				return fmt.Errorf("service %s requires stop-first %s", name, key)
			}
		}
		for _, mount := range mounts {
			switch m := mount.(type) {
			case string:
				parts := strings.Split(m, ":")
				if len(parts) < 2 || parts[0] == "" || strings.HasPrefix(parts[0], "/") {
					return fmt.Errorf("service %s requires named persistent volumes", name)
				}
				if previous, exists := usedVolumes[parts[0]]; exists && previous != name {
					return fmt.Errorf("persistent volume %s cannot have multiple writers", parts[0])
				}
				usedVolumes[parts[0]] = name

			case map[string]any:
				if m["type"] != "volume" || m["source"] == nil || m["source"] == "" {
					return fmt.Errorf("service %s requires named persistent volumes", name)
				}
				source, _ := m["source"].(string)
				if source == "" {
					return fmt.Errorf("invalid named volume")
				}
				if previous, exists := usedVolumes[source]; exists && previous != name {
					return fmt.Errorf("persistent volume %s cannot have multiple writers", source)
				}
				usedVolumes[source] = name

			default:
				return fmt.Errorf("invalid persistent mount")
			}
		}
	}
	volumes, _ := stringMap(doc["volumes"])
	for name := range usedVolumes {
		definition, exists := volumes[name]
		if !exists {
			return fmt.Errorf("persistent volume %s must be declared", name)
		}
		volume, err := composeResourceDefinition("volume", name, definition)
		if err != nil {
			return err
		}
		if volume["external"] == true || (volume["driver"] != nil && volume["driver"] != "" && volume["driver"] != "local") || volume["driver_opts"] != nil {
			return fmt.Errorf("persistent volume %s must use managed local storage", name)
		}
	}
	return nil
}

// ValidateDataNodeChange protects the frozen storage layout even for raw edits.
func ValidateDataNodeChange(before, after, oldNode, newNode string) error {
	if oldNode == "" {
		return nil
	}
	if newNode != oldNode {
		return fmt.Errorf("changing the data node requires a data migration")
	}
	var old, next map[string]any
	if err := yaml.Unmarshal([]byte(before), &old); err != nil {
		return err
	}
	if err := yaml.Unmarshal([]byte(after), &next); err != nil {
		return err
	}
	if !reflect.DeepEqual(old["volumes"], next["volumes"]) {
		return fmt.Errorf("persistent volume definitions cannot change")
	}
	oldServices, _ := stringMap(old["services"])
	nextServices, _ := stringMap(next["services"])
	for name, value := range oldServices {
		service, _ := stringMap(value)
		mounts, _ := service["volumes"].([]any)
		if len(mounts) == 0 {
			continue
		}
		candidate, _ := stringMap(nextServices[name])
		if !reflect.DeepEqual(service["volumes"], candidate["volumes"]) {
			return fmt.Errorf("service %s persistent mounts cannot change", name)
		}
	}
	return ValidateDataNodePolicy(after, newNode, nil)
}

func ExternalNetworkNames(raw string) ([]string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, err
	}
	networks, err := optionalMapping(doc, "networks")
	if err != nil {
		return nil, err
	}
	names := []string{}
	for name, value := range networks {
		definition, err := composeResourceDefinition("network", name, value)
		if err != nil {
			return nil, err
		}
		if definition["external"] == true {
			if configured, ok := definition["name"].(string); ok && configured != "" {
				name = configured
			}
			names = append(names, name)
		}
	}
	return names, nil
}

// ManagedVolumeNames resolves the names Docker will create for this stack.
func ManagedVolumeNames(raw, project string) ([]string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, err
	}
	volumes, err := optionalMapping(doc, "volumes")
	if err != nil {
		return nil, err
	}
	names := []string{}
	for key, value := range volumes {
		definition, err := composeResourceDefinition("volume", key, value)
		if err != nil {
			return nil, err
		}
		name := project + "_" + key
		if external, _ := definition["external"].(bool); external {
			name = key
		}
		if explicit, ok := definition["name"].(string); ok && explicit != "" {
			name = explicit
		}
		names = append(names, name)
	}
	return names, nil
}
