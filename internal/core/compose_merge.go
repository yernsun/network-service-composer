package core

import (
	"context"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// MergeComposeFiles delegates override semantics to Docker's Stack parser. The
// isolated inputs cannot reference host files, build contexts or remote sources.
func (c *Core) MergeComposeFiles(ctx context.Context, environment string, files []string) (string, error) {
	if _, err := c.GetEnvironment(ctx, environment); err != nil {
		return "", err
	}
	if len(files) == 0 || len(files) > 16 {
		return "", controlInvalid("compose_files", "provide between one and sixteen Compose files")
	}
	total := 0
	for _, source := range files {
		total += len(source)
		if total > 2<<20 {
			return "", controlInvalid("compose_files", "Compose files exceed the source size limit")
		}
		var document map[string]any
		if err := yaml.Unmarshal([]byte(source), &document); err != nil {
			return "", controlInvalid("compose_files", "invalid YAML")
		}
		if document["include"] != nil {
			return "", controlInvalid("compose_files", "include is not supported")
		}
		services, _ := document["services"].(map[string]any)
		for _, value := range services {
			service, _ := value.(map[string]any)
			for _, key := range []string{"build", "env_file", "extends"} {
				if service[key] != nil {
					return "", controlInvalid("compose_files", "external file references are not supported during merge")
				}
			}
		}
		for _, key := range []string{"configs", "secrets"} {
			resources, _ := document[key].(map[string]any)
			for _, value := range resources {
				resource, _ := value.(map[string]any)
				if resource["file"] != nil {
					return "", controlInvalid("compose_files", "external file references are not supported during merge")
				}
			}
		}
	}
	directory, err := os.MkdirTemp("", "nsc-compose-merge-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(directory)
	args := []string{"stack", "config", "--skip-interpolation"}
	for index, source := range files {
		path := filepath.Join(directory, fmt.Sprintf("compose-%d.yaml", index))
		if err = os.WriteFile(path, []byte(source), 0600); err != nil {
			return "", err
		}
		args = append(args, "--compose-file", path)
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(bounded, c.config.Management.DockerCLI, args...)
	command.Dir = directory
	command.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
	output, err := command.Output()
	if err != nil {
		return "", controlInvalid("compose_files", "Docker Stack could not merge the supplied files")
	}
	return string(output), nil
}
