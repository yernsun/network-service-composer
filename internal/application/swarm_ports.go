package application

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"strconv"
	"strings"
)

// Swarm does not retain Compose expose metadata in its Service specification.
// Preserve reviewed ports as discovery labels without publishing host ports.
func SwarmServicePorts(raw string, specs []ServiceSpec) (string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return "", err
	}
	services, err := optionalMapping(doc, "services")
	if err != nil {
		return "", err
	}
	for _, spec := range specs {
		if !spec.PortCertain || spec.TargetPort == 0 {
			continue
		}
		service, ok := stringMap(services[spec.Name])
		if !ok {
			return "", fmt.Errorf("reviewed service %s is absent", spec.Name)
		}
		deploy, _ := stringMap(service["deploy"])
		if deploy == nil {
			deploy = map[string]any{}
		}
		labels, ok := stringMap(deploy["labels"])
		if !ok {
			labels = map[string]any{}
			if list, ok := deploy["labels"].([]any); ok {
				for _, value := range list {
					text, ok := value.(string)
					if !ok {
						return "", fmt.Errorf("invalid service label")
					}
					key, value, found := strings.Cut(text, "=")
					if !found {
						value = ""
					}
					labels[key] = value
				}
			}
		}
		if _, exists := labels["nsc.port"]; !exists {
			labels["nsc.port"] = strconv.Itoa(spec.TargetPort)
		}
		deploy["labels"] = labels
		service["deploy"] = deploy
	}
	data, err := yaml.Marshal(doc)
	return string(data), err
}
