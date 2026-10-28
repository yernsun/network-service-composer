package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yernsun/network-service-composer/internal/model"
)

const applicationNetworkOwner = "io.network-service-composer.application"

// Serialize by network name as well as application: two applications may
// request the same explicit name. Never adopt a Docker object by name alone.
func (w *Worker) ensureApplicationNetwork(ctx context.Context, runtime model.Runtime, value material) error {
	ownerLabel, ownerID := applicationNetworkOwner, value.application.ID
	if scope := value.revision.ConfigurationSource.ServiceScope; scope != nil {
		ownerLabel, ownerID = "io.network-service-composer.project", scope.ProjectID
	}
	name := value.revision.ConfigurationSource.NetworkName
	if name == "" {
		return nil
	}
	if w.locker == nil {
		return fmt.Errorf("application network creation requires a coordination lock")
	}
	lock, err := w.locker.TryLock(ctx, "application-network:"+runtime.PoolID+":"+name)
	if err != nil {
		return err
	}
	defer lock.Release(context.WithoutCancel(ctx))
	args := w.dockerArgs(runtime)
	raw, err := w.runner.Run(ctx, ".", nil, append(args, "network", "ls", "--filter", "name="+name, "--format", "{{.ID}} {{.Name}}"))
	if err != nil {
		return fmt.Errorf("list application networks: %w", err)
	}
	ids, err := exactNetworkIDs(raw, name)
	if err != nil {
		return err
	}
	if len(ids) > 1 {
		return fmt.Errorf("application network %q is ambiguous", name)
	}
	if len(ids) == 0 {
		if err := lock.Check(ctx); err != nil {
			return err
		}
		_, err = w.runner.Run(ctx, ".", nil, append(args, "network", "create", "--driver", "overlay", "--label", ownerLabel+"="+ownerID, "--label", "io.network-service-composer.revision="+value.revision.ID, name))
		if err != nil {
			return fmt.Errorf("create application network: %w", err)
		}
	}
	// Verify after creation too: a concurrent external creator must never cause
	// NSC to attach to an unrelated network. Unknown results fail closed.
	raw, err = w.runner.Run(ctx, ".", nil, append(args, "network", "inspect", name, "--format", "{{json .}}"))
	if err != nil {
		return fmt.Errorf("inspect application network: %w", err)
	}
	var network struct {
		Name   string
		Driver string
		Scope  string
		Labels map[string]string
	}
	if err = json.Unmarshal([]byte(raw), &network); err != nil {
		return fmt.Errorf("decode application network: %w", err)
	}
	if network.Name != name || network.Driver != "overlay" || network.Scope != "swarm" || network.Labels[ownerLabel] != ownerID {
		return fmt.Errorf("application network %q conflicts with an existing network's ownership or driver", name)
	}
	return lock.Check(ctx)
}

func (w *Worker) cleanupInstallationNetwork(ctx context.Context, runtime model.Runtime, application model.Application, revisionID string) error {
	if runtime.Type != model.RuntimeSwarm {
		return nil
	}
	revision, err := w.store.GetApplicationRevision(ctx, revisionID)
	if err != nil {
		return err
	}
	name := revision.ConfigurationSource.NetworkName
	if name == "" {
		return nil
	}
	if w.locker == nil {
		return fmt.Errorf("network cleanup requires a coordination lock")
	}
	lock, err := w.locker.TryLock(ctx, "application-network:"+runtime.PoolID+":"+name)
	if err != nil {
		return err
	}
	defer lock.Release(context.WithoutCancel(ctx))
	args := w.dockerArgs(runtime)
	raw, err := w.runner.Run(ctx, ".", nil, append(args, "network", "ls", "--filter", "name="+name, "--format", "{{.ID}} {{.Name}}"))
	if err != nil {
		return err
	}
	ids, err := exactNetworkIDs(raw, name)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if len(ids) != 1 {
		return fmt.Errorf("ambiguous application network during cleanup")
	}
	raw, err = w.runner.Run(ctx, ".", nil, append(args, "network", "inspect", ids[0], "--format", "{{json .}}"))
	if err != nil {
		return err
	}
	var network struct {
		ID         string
		Labels     map[string]string
		Containers map[string]json.RawMessage
	}
	if err := json.Unmarshal([]byte(raw), &network); err != nil {
		return err
	}
	if network.Labels[applicationNetworkOwner] != application.ID || network.Labels["io.network-service-composer.revision"] != revisionID {
		return nil
	}
	if len(network.Containers) != 0 {
		return fmt.Errorf("application network is still in use; retained")
	}
	if network.ID == "" {
		return fmt.Errorf("application network identity is unavailable; retained")
	}
	if err := lock.Check(ctx); err != nil {
		return err
	}
	// Docker also refuses removal while any service references this network.
	_, err = w.runner.Run(ctx, ".", nil, append(args, "network", "rm", network.ID))
	return err
}

// Docker's name filter matches substrings, not anchored regular expressions.
// Check the returned name explicitly so similarly named networks are untouched.
func exactNetworkIDs(raw, name string) ([]string, error) {
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid application network listing")
		}
		if fields[1] == name {
			ids = append(ids, fields[0])
		}
	}
	return ids, nil
}
