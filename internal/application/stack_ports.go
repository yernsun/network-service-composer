package application

import (
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

// StackPortNumbers adapts resolved Compose port scalars to Docker's legacy
// Stack schema, which requires integers where the Compose spec also accepts
// strings. Only execution material is converted; saved source stays intact.
// Port admission must run first (ranges and invalid values are not coerced).
func StackPortNumbers(raw string) (string, error) {
	var document map[string]any
	if err := yaml.Unmarshal([]byte(raw), &document); err != nil {
		return "", err
	}
	services, _ := document["services"].(map[string]any)
	for name, definition := range services {
		service, _ := definition.(map[string]any)
		ports, _ := service["ports"].([]any)
		for index, entry := range ports {
			port, _ := entry.(map[string]any)
			for _, field := range []string{"target", "published"} {
				if value, ok := port[field].(string); ok {
					if field == "published" && value == "" {
						delete(port, field)
						continue
					}
					number, err := strconv.Atoi(value)
					if err != nil || number < 0 || number > 65535 || field == "target" && number == 0 {
						return "", fmt.Errorf("service %s ports[%d].%s must be a valid port number for Swarm", name, index, field)
					}
					port[field] = number
				}
			}
		}
	}
	encoded, err := yaml.Marshal(document)
	return string(encoded), err
}
