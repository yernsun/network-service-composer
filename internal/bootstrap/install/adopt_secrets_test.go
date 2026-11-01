package install

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

type secretReadEngine struct {
	embeddedEngine
	tasks    []swarm.Task
	commands []string
}

func (e *secretReadEngine) Engine(ctx context.Context, method, path string, input, out any) error {
	if strings.HasPrefix(path, "/tasks?") {
		return encode(e.tasks, out)
	}
	return e.embeddedEngine.Engine(ctx, method, path, input, out)
}
func (e *secretReadEngine) Run(ctx context.Context, command string, input []byte) ([]byte, error) {
	if strings.HasPrefix(command, bootstrap.LocalDockerCLI+" exec -i ") {
		e.commands = append(e.commands, command)
		return []byte("isolated-runtime-secret"), nil
	}
	return e.embeddedEngine.Run(ctx, command, input)
}

func TestAdoptionSwarmSecretRequiresExactRunningServiceAndNodeTask(t *testing.T) {
	i, cluster := adoptionFixture(t, 1)
	service := cluster.services[i.Config.Adoption.ServiceIDs["controller"]]
	var copy swarm.Service
	if err := encode(service, &copy); err != nil {
		t.Fatal(err)
	}
	service = copy
	service.Spec.TaskTemplate.ContainerSpec.Mounts = nil
	service.Spec.TaskTemplate.ContainerSpec.Secrets = []*swarm.SecretReference{{SecretID: "existing-secret-id", File: &swarm.SecretReferenceFileTarget{Name: "credential", UID: "65532", Mode: 0400}}}
	task := swarm.Task{ServiceID: service.ID, NodeID: i.Record.Nodes["one"].NodeID, Status: swarm.TaskStatus{State: swarm.TaskStateRunning, ContainerStatus: &swarm.ContainerStatus{ContainerID: "original-component-container"}}}
	engine := &secretReadEngine{embeddedEngine: i.Hosts["one"], tasks: []swarm.Task{task}}
	i.Hosts["one"] = engine
	value, err := i.registeredFile(t.Context(), "one", service, "/run/secrets/credential", true)
	if err != nil || string(value) != "isolated-runtime-secret" || len(engine.commands) != 1 {
		t.Fatal("original protected Secret reference was not read", err)
	}
	if !strings.Contains(engine.commands[0], "'original-component-container' cat -- '/run/secrets/credential'") {
		t.Fatal("read did not bind the actual known task")
	}
	for _, scenario := range []string{"missing", "multiple", "wrong-service", "wrong-node", "not-running", "no-container", "empty-container"} {
		t.Run(scenario, func(t *testing.T) {
			next := task
			next.Status.ContainerStatus = &swarm.ContainerStatus{ContainerID: "original-component-container"}
			engine.tasks = []swarm.Task{next}
			switch scenario {
			case "missing":
				engine.tasks = nil
			case "multiple":
				engine.tasks = append(engine.tasks, next)
			case "wrong-service":
				engine.tasks[0].ServiceID = "foreign-service"
			case "wrong-node":
				engine.tasks[0].NodeID = "foreign-node"
			case "not-running":
				engine.tasks[0].Status.State = swarm.TaskStateShutdown
			case "no-container":
				engine.tasks[0].Status.ContainerStatus = nil
			case "empty-container":
				engine.tasks[0].Status.ContainerStatus.ContainerID = ""
			}
			count := len(engine.commands)
			if _, err := i.registeredFile(t.Context(), "one", service, "/run/secrets/credential", true); err == nil {
				t.Fatal("missing or ambiguous task allowed secret access")
			}
			if len(engine.commands) != count {
				t.Fatal("secret was read before task ownership was established")
			}
		})
	}
}

type localReadEngine struct{ embeddedEngine }

func (e localReadEngine) Run(ctx context.Context, command string, _ []byte) ([]byte, error) {
	return exec.CommandContext(ctx, "sh", "-c", command).Output()
}

func TestAdoptionPrivateFileReferenceRejectsPublicHostPermissions(t *testing.T) {
	i, _ := adoptionFixture(t, 1)
	file := filepath.Join(t.TempDir(), "private's credential")
	if err := os.WriteFile(file, []byte("isolated-private-reference"), 0600); err != nil {
		t.Fatal(err)
	}
	i.Hosts["one"] = localReadEngine{i.Hosts["one"]}
	service := swarm.Service{Spec: swarm.ServiceSpec{TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Mounts: []mount.Mount{{Type: mount.TypeBind, Source: file, Target: "/run/nsc/credential"}}}}}}
	value, err := i.registeredFile(t.Context(), "one", service, "/run/nsc/credential", true)
	if err != nil || string(value) != "isolated-private-reference" {
		t.Fatal("private file with a quoted path failed", err)
	}
	if err = os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	value, err = i.registeredFile(t.Context(), "one", service, "/run/nsc/credential", true)
	if err == nil || len(value) != 0 {
		t.Fatal("a public host file was accepted as a protected credential")
	}
}
