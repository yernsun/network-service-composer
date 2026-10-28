package application

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// RemoveService removes one service definition while keeping the remaining
// Compose source and all declared resources. Runtime volume data is untouched.
func RemoveService(source, name string) (string, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(source), &document); err != nil {
		return "", err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return "", fmt.Errorf("Compose must be a mapping")
	}
	root := document.Content[0]
	index := nodeKey(root, "services")
	if index < 0 || root.Content[index+1].Kind != yaml.MappingNode {
		return "", fmt.Errorf("services must be a mapping")
	}
	services := root.Content[index+1]
	item := nodeKey(services, name)
	if item < 0 {
		return "", fmt.Errorf("service %q is absent from the revision", name)
	}
	if len(services.Content) <= 2 {
		return "", fmt.Errorf("delete the application to remove its last service")
	}
	services.Content = append(services.Content[:item], services.Content[item+2:]...)
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return "", err
	}
	return out.String(), nil
}
