package application

import (
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

// SetServiceReplicas edits one Compose service without rewriting unrelated
// definitions through a generic map. An aliased service/deploy block is copied
// before editing so a shared anchor cannot change another service.
func SetServiceReplicas(source, name string, replicas int) (string, error) {
	if err := ValidateServiceScaling(source, name, replicas); err != nil {
		return "", err
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(source), &document); err != nil {
		return "", err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return "", fmt.Errorf("Compose source must be a mapping")
	}
	services := mappingValue(document.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return "", fmt.Errorf("Compose services must be a mapping")
	}
	service := mappingValue(services, name)
	if service == nil {
		return "", fmt.Errorf("service %q is absent from the Compose source", name)
	}
	if service.Kind == yaml.AliasNode {
		var value any
		if err := service.Decode(&value); err != nil {
			return "", err
		}
		if err := service.Encode(value); err != nil {
			return "", err
		}
	}
	if service.Kind != yaml.MappingNode {
		return "", fmt.Errorf("service %q must be a mapping", name)
	}
	deploy := mappingValue(service, "deploy")
	if deploy == nil {
		deploy = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		service.Content = append(service.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "deploy"}, deploy)
	} else if deploy.Kind == yaml.AliasNode {
		var value any
		if err := deploy.Decode(&value); err != nil {
			return "", err
		}
		if err := deploy.Encode(value); err != nil {
			return "", err
		}
	}
	if deploy.Kind != yaml.MappingNode {
		return "", fmt.Errorf("service %q deploy must be a mapping", name)
	}
	count := mappingValue(deploy, "replicas")
	if count == nil {
		count = &yaml.Node{}
		deploy.Content = append(deploy.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "replicas"}, count)
	}
	count.Kind, count.Tag, count.Value, count.Style = yaml.ScalarNode, "!!int", strconv.Itoa(replicas), 0
	encoded, err := yaml.Marshal(&document)
	return string(encoded), err
}

// ValidateServiceScaling checks the Compose constraints before a runtime or
// source-level scale. Global and job workloads do not have a replica count.
func ValidateServiceScaling(source, name string, replicas int) error {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(source), &document); err != nil {
		return err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("Compose source must be a mapping")
	}
	services := mappingValue(document.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return fmt.Errorf("Compose services must be a mapping")
	}
	service := mappingValue(services, name)
	if service == nil {
		return fmt.Errorf("service %q is absent from the Compose source", name)
	}
	if service.Kind == yaml.AliasNode {
		service = service.Alias
	}
	if service == nil || service.Kind != yaml.MappingNode {
		return fmt.Errorf("service %q must be a mapping", name)
	}
	if deploy := mappingValue(service, "deploy"); deploy != nil {
		if deploy.Kind == yaml.AliasNode {
			deploy = deploy.Alias
		}
		if deploy.Kind != yaml.MappingNode {
			return fmt.Errorf("service %q deploy must be a mapping", name)
		}
		if mode := mappingValue(deploy, "mode"); mode != nil && mode.Value != "replicated" && mode.Value != "" {
			return fmt.Errorf("service %q deploy mode %q does not support replicas", name, mode.Value)
		}
	}
	if scale := mappingValue(service, "scale"); scale != nil && scale.Value != strconv.Itoa(replicas) {
		return fmt.Errorf("service %q has a conflicting Compose scale field", name)
	}
	if replicas > 1 && mappingValue(service, "container_name") != nil {
		return fmt.Errorf("service %q has a fixed container_name and cannot have multiple replicas", name)
	}
	return nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}
