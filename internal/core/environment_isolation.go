package core

import (
	"context"
	"fmt"
	"github.com/yernsun/network-service-composer/internal/storage"
	"gopkg.in/yaml.v3"
	"sort"
	"strings"
)

type IsolationResource struct {
	Kind       string   `json:"kind"`
	Name       string   `json:"name"`
	SharedWith []string `json:"shared_with"`
}

func isolationResources(compose string) []IsolationResource {
	var doc map[string]any
	if yaml.Unmarshal([]byte(compose), &doc) != nil {
		return nil
	}
	resources := map[string]IsolationResource{}
	add := func(kind, name string) {
		resources[kind+":"+name] = IsolationResource{Kind: kind, Name: name, SharedWith: []string{}}
	}
	for _, kind := range []string{"volumes", "networks"} {
		items, _ := doc[kind].(map[string]any)
		for key, raw := range items {
			item, _ := raw.(map[string]any)
			name, _ := item["name"].(string)
			external, _ := item["external"].(bool)
			if name != "" || external {
				if name == "" {
					name = key
				}
				add(kind, name)
			}
		}
	}
	services, _ := doc["services"].(map[string]any)
	for _, raw := range services {
		svc, _ := raw.(map[string]any)
		ports, _ := svc["ports"].([]any)
		for _, raw := range ports {
			switch value := raw.(type) {
			case string:
				parts := strings.Split(value, ":")
				if len(parts) > 1 {
					protocol := "tcp"
					if strings.Contains(parts[len(parts)-1], "/") {
						protocol = strings.Split(parts[len(parts)-1], "/")[1]
					}
					add("ports", parts[len(parts)-2]+"/"+protocol)
				}
			case map[string]any:
				if value["published"] != nil {
					protocol := "tcp"
					if v, ok := value["protocol"].(string); ok {
						protocol = v
					}
					add("ports", fmt.Sprint(value["published"])+"/"+protocol)
				}
			}
		}
	}
	result := []IsolationResource{}
	for _, v := range resources {
		result = append(result, v)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Kind+result[i].Name < result[j].Kind+result[j].Name })
	return result
}
func (c *Core) environmentIsolation(ctx context.Context, env Environment, compose string) ([]IsolationResource, error) {
	resources := isolationResources(compose)
	if len(resources) == 0 {
		return resources, nil
	}
	page := storage.Page{Limit: 200}
	for {
		apps, e := c.store.ListApplications(ctx, env.RuntimeID, page)
		if e != nil {
			return nil, e
		}
		for _, app := range apps {
			if app.ID == env.ApplicationID || app.ActiveRevisionID == "" {
				continue
			}
			revision, e := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
			if e != nil {
				return nil, e
			}
			raw, _, e := c.keyring.Decrypt(revision.EncryptedResolved, revisionAAD(revision.ID, "resolved"))
			if e != nil {
				return nil, e
			}
			for _, other := range isolationResources(string(raw)) {
				for i := range resources {
					if resources[i].Kind == other.Kind && resources[i].Name == other.Name {
						resources[i].SharedWith = append(resources[i].SharedWith, app.ID)
					}
				}
			}
		}
		if len(apps) < 200 {
			break
		}
		page.After = apps[len(apps)-1].ID
	}
	return resources, nil
}
