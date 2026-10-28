package deployment

import (
	"context"
	"fmt"
	"slices"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/dockerresource"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func (w *Worker) allowedDockerResourceNames(ctx context.Context, runtime model.Runtime) ([]string, []string, error) {
	secrets := append([]string(nil), w.config.Management.ExternalSecrets...)
	configs := append([]string(nil), w.config.Management.ExternalConfigs...)
	if runtime.Type != model.RuntimeSwarm || runtime.ClusterID == "" {
		return secrets, configs, nil
	}
	for after := ""; ; {
		rows, err := w.store.ListDockerResources(ctx, runtime.ClusterID, "", storage.Page{After: after, Limit: 200})
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

func (w *Worker) verifyFrozenDockerResources(ctx context.Context, runtime model.Runtime, frozen []model.DockerResourceBinding, resolved string) error {
	names, err := applicationx.ExternalResourceNames(resolved)
	if err != nil {
		return err
	}
	if runtime.Type != model.RuntimeSwarm {
		return nil
	}
	needed := map[string]bool{}
	for _, kind := range []string{"config", "secret"} {
		for _, name := range names[kind] {
			static := kind == "config" && slices.Contains(w.config.Management.ExternalConfigs, name) || kind == "secret" && slices.Contains(w.config.Management.ExternalSecrets, name)
			if !static {
				needed[kind+":"+name] = true
			}
		}
	}
	if len(needed) == 0 {
		return nil
	}
	if runtime.ClusterID == "" || len(frozen) != len(needed) {
		return dockerresource.ErrIdentity
	}
	listed, err := (dockerresource.Manager{Store: w.store, Config: w.config, Reports: w.executionReports()}).Do(ctx, dockerresource.Request{RuntimeID: runtime.ID, Action: "list"})
	if err != nil {
		return err
	}
	for _, binding := range frozen {
		if binding.ClusterID != runtime.ClusterID || !needed[binding.Kind+":"+binding.Name] || binding.DockerID == "" {
			return dockerresource.ErrIdentity
		}
		delete(needed, binding.Kind+":"+binding.Name)
		found := false
		for _, item := range listed.Items {
			if item.Kind == binding.Kind && item.Name == binding.Name && item.ID == binding.DockerID && item.Owned {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s %q no longer resolves to frozen Docker ID", binding.Kind, binding.Name)
		}
	}
	if len(needed) > 0 {
		return dockerresource.ErrIdentity
	}
	return nil
}
