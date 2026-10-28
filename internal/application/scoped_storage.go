package application

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// validateScopedStorage runs after interpolation and normal Compose validation.
// Project credentials authorize application resources, not host paths or global
// volumes. Console administrators retain the existing host-mount contract.
func validateScopedStorage(resolved string) error {
	var document map[string]any
	if err := yaml.Unmarshal([]byte(resolved), &document); err != nil {
		return fmt.Errorf("invalid scoped storage configuration")
	}
	volumes, _ := stringMap(document["volumes"])
	for _, raw := range volumes {
		definition, _ := stringMap(raw)
		for _, key := range []string{"external", "name", "driver", "driver_opts"} {
			if value, present := definition[key]; present && value != false {
				return fmt.Errorf("project API keys require application-owned local volumes")
			}
		}
	}
	services, _ := stringMap(document["services"])
	for _, raw := range services {
		service, _ := stringMap(raw)
		mounts, _ := service["volumes"].([]any)
		for _, mount := range mounts {
			if short, ok := mount.(string); ok {
				parts := strings.Split(short, ":")
				if len(parts) > 1 && strings.HasPrefix(parts[0], "/") {
					return fmt.Errorf("project API keys cannot mount host paths")
				}
			} else if definition, ok := stringMap(mount); ok && definition["type"] == "bind" {
				return fmt.Errorf("project API keys cannot mount host paths")
			}
		}
	}
	return nil
}
