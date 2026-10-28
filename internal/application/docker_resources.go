package application

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExternalResourceNames returns only logical external names from a Compose
// source. It never reads file contents or resolves Docker objects itself.
func ExternalResourceNames(raw string) (map[string][]string, error) {
	var document map[string]any
	if err := yaml.Unmarshal([]byte(raw), &document); err != nil {
		return nil, err
	}
	result := map[string][]string{"config": {}, "secret": {}}
	for _, entry := range []struct{ Key, Kind string }{{"configs", "config"}, {"secrets", "secret"}} {
		definitions, err := optionalMapping(document, entry.Key)
		if err != nil {
			return nil, err
		}
		for logicalName, rawDefinition := range definitions {
			definition, err := composeResourceDefinition(entry.Kind, logicalName, rawDefinition)
			if err != nil {
				return nil, err
			}
			if external, _ := definition["external"].(bool); !external {
				continue
			}
			name := logicalName
			if configured := strings.TrimSpace(fmt.Sprint(definition["name"])); configured != "" && configured != "<nil>" {
				name = configured
			}
			result[entry.Kind] = append(result[entry.Kind], name)
		}
	}
	return result, nil
}
