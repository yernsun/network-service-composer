package core

import (
	"context"
	"fmt"
	"slices"
	"sort"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/dockerresource"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func (c *Core) allowedDockerResourceNames(ctx context.Context, runtime model.Runtime) ([]string, []string, error) {
	secrets := append([]string(nil), c.config.Management.ExternalSecrets...)
	configs := append([]string(nil), c.config.Management.ExternalConfigs...)
	if runtime.Type != model.RuntimeSwarm || runtime.ClusterID == "" {
		return secrets, configs, nil
	}
	for after := ""; ; {
		rows, err := c.store.ListDockerResources(ctx, runtime.ClusterID, "", storage.Page{After: after, Limit: 200})
		if err != nil {
			return nil, nil, err
		}
		for _, row := range rows {
			if row.Kind == "secret" && !slices.Contains(secrets, row.Name) {
				secrets = append(secrets, row.Name)
			}
			if row.Kind == "config" && !slices.Contains(configs, row.Name) {
				configs = append(configs, row.Name)
			}
		}
		if len(rows) < 200 {
			break
		}
		after = rows[len(rows)-1].DockerID
	}
	return secrets, configs, nil
}

func (c *Core) freezeDockerResources(ctx context.Context, runtime model.Runtime, resolved string) ([]model.DockerResourceBinding, error) {
	names, err := applicationx.ExternalResourceNames(resolved)
	if err != nil {
		return nil, err
	}
	if len(names["config"])+len(names["secret"]) == 0 {
		return nil, nil
	}
	if runtime.Type != model.RuntimeSwarm {
		return nil, nil // Compose retains its static external-resource allowlists.
	}
	managedNames := false
	for _, kind := range []string{"config", "secret"} {
		for _, name := range names[kind] {
			if kind == "config" && !slices.Contains(c.config.Management.ExternalConfigs, name) || kind == "secret" && !slices.Contains(c.config.Management.ExternalSecrets, name) {
				managedNames = true
			}
		}
	}
	if !managedNames {
		return nil, nil
	}
	if runtime.ClusterID == "" {
		return nil, dockerresource.ErrIdentity
	}
	listed, err := (dockerresource.Manager{Store: c.store, Config: c.config, Reports: c.HAReports}).Do(ctx, dockerresource.Request{RuntimeID: runtime.ID, Action: "list"})
	if err != nil {
		return nil, err
	}
	bindings := []model.DockerResourceBinding{}
	for _, kind := range []string{"config", "secret"} {
		for _, name := range names[kind] {
			static := kind == "config" && slices.Contains(c.config.Management.ExternalConfigs, name) || kind == "secret" && slices.Contains(c.config.Management.ExternalSecrets, name)
			if static {
				continue
			}
			found := false
			for _, item := range listed.Items {
				if item.Kind == kind && item.Name == name && item.Owned {
					bindings = append(bindings, model.DockerResourceBinding{ClusterID: runtime.ClusterID, Kind: kind, Name: name, DockerID: item.ID})
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("external %s %q has no verified NSC-owned object in this cluster", kind, name)
			}
		}
	}
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].Kind == bindings[j].Kind {
			return bindings[i].Name < bindings[j].Name
		}
		return bindings[i].Kind < bindings[j].Kind
	})
	return bindings, nil
}
