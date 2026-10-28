package core

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

var projectSlug = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,46}[a-z0-9])?$`)
var environmentSlug = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)

type ProjectInput struct {
	Name               string            `json:"name"`
	Slug               string            `json:"slug"`
	Description        string            `json:"description"`
	DefaultRuntimeID   string            `json:"default_runtime_id"`
	InitialEnvironment *EnvironmentInput `json:"initial_environment,omitempty"`
}

type ProjectSettings struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	DefaultRuntimeID string `json:"default_runtime_id"`
}

type EnvironmentSettings struct {
	DisplayName string   `json:"display_name"`
	Kind        string   `json:"kind"`
	GatewayIDs  []string `json:"gateway_ids"`
}

func normalizeEnvironment(e *Environment) {
	if e.DisplayName == "" {
		e.DisplayName = e.Name
	}
	if e.Kind == "" {
		e.Kind = "custom"
		if e.Name == "prod" || e.Name == "production" {
			e.Kind = "production"
		}
	}
	if e.GatewayIDs == nil {
		e.GatewayIDs = []string{}
	}
}

func (c *Core) validateEnvironmentMetadata(ctx context.Context, e Environment) error {
	if strings.TrimSpace(e.DisplayName) == "" || len(e.DisplayName) > 255 {
		return invalid("validation.project_name", "display_name", "a display name of at most 255 bytes is required")
	}
	if !slices.Contains([]string{"production", "development", "testing", "sandbox", "custom"}, e.Kind) {
		return invalid("validation.environment_kind", "kind", "unsupported environment kind")
	}
	rt, err := c.store.GetRuntime(ctx, e.RuntimeID)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, key := range e.GatewayIDs {
		g, err := c.store.GetGateway(ctx, key)
		if err != nil || g.PoolID != rt.PoolID || seen[key] {
			return invalid("validation.environment_gateway", "gateway_ids", "gateways must be unique and belong to the environment runtime pool")
		}
		seen[key] = true
	}
	return nil
}

func (c *Core) projectRuntime(ctx context.Context, explicit string) (string, error) {
	if explicit == "" {
		explicit = c.config.Management.DefaultRuntimeID
	}
	if explicit != "" {
		_, err := c.requireManagement(ctx, explicit)
		if err != nil {
			return "", invalid("validation.project_runtime", "default_runtime_id", "select an enabled, manageable runtime")
		}
		return explicit, nil
	}
	var selected string
	for page := (storage.Page{Limit: 200}); ; {
		items, err := c.store.ListRuntimes(ctx, page)
		if err != nil {
			return "", err
		}
		for _, rt := range items {
			if !rt.Enabled || !rt.ManagementEnabled || (rt.Type == model.RuntimeSwarm && c.store.Dialect() == storage.SQLite) {
				continue
			}
			if selected != "" {
				return "", invalid("validation.project_runtime", "default_runtime_id", "select a default runtime; more than one is eligible")
			}
			selected = rt.ID
		}
		if len(items) < page.Limit {
			break
		}
		page.After = items[len(items)-1].ID
	}
	if selected == "" {
		return "", invalid("validation.project_runtime", "default_runtime_id", "no manageable runtime is configured")
	}
	return selected, nil
}

func (c *Core) environmentRuntime(ctx context.Context, p Project) (string, error) {
	if p.DefaultRuntimeID != "" {
		return p.DefaultRuntimeID, nil
	}
	envs, err := c.allProjectEnvironments(ctx, p.ID)
	if err != nil {
		return "", err
	}
	var selected string
	for _, env := range envs {
		if selected != "" && selected != env.RuntimeID {
			return "", invalid("validation.project_runtime", "runtime_id", "configure a default project runtime")
		}
		selected = env.RuntimeID
	}
	if selected == "" {
		return "", invalid("validation.project_runtime", "runtime_id", "configure a default project runtime")
	}
	return selected, nil
}

func (c *Core) allProjectEnvironments(ctx context.Context, key string) ([]Environment, error) {
	result := []Environment{}
	for page := (storage.Page{Limit: 200}); ; {
		items, err := c.ListEnvironments(ctx, key, page)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if len(items) < page.Limit {
			return result, nil
		}
		page.After = items[len(items)-1].ID
	}
}

// The optional initial environment and the idempotency record commit with the project.
func (c *Core) CreateProjectWithEnvironment(ctx context.Context, in ProjectInput, idem string) (Project, error) {
	if err := c.requireControlManagement(); err != nil {
		return Project{}, err
	}
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	if strings.EqualFold(in.Name, "Gateway") {
		return Project{}, storage.ErrSystemProtected
	}
	if in.Name == "" || len(in.Name) > 255 || len(in.Description) > 4096 {
		return Project{}, invalid("validation.project_name", "name", "project name or description is invalid")
	}
	if !projectSlug.MatchString(in.Slug) {
		return Project{}, invalid("validation.application_identity_invalid", "slug", "invalid project slug")
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return Project{}, err
	}
	fingerprint := digestString(string(raw))
	requestID := "project-request:" + digestString(idem)
	readExisting := func() (Project, error) {
		var saved struct {
			ProjectID   string `json:"project_id"`
			Fingerprint string `json:"fingerprint"`
		}
		saved, _, e := readControl[struct {
			ProjectID   string `json:"project_id"`
			Fingerprint string `json:"fingerprint"`
		}](ctx, c.store, requestID, "project-request")
		if e != nil {
			return Project{}, e
		}
		if saved.Fingerprint != fingerprint {
			return Project{}, storage.ErrConflict
		}
		return c.GetProject(ctx, saved.ProjectID)
	}
	if idem != "" {
		if p, e := readExisting(); !errors.Is(e, storage.ErrNotFound) {
			return p, e
		}
	}
	p := Project{Name: in.Name, Slug: in.Slug, Description: in.Description, DefaultRuntimeID: in.DefaultRuntimeID}
	p.ID = id.New("prj")
	writes := []storage.ControlWrite{}
	if in.InitialEnvironment != nil || in.DefaultRuntimeID != "" {
		p.DefaultRuntimeID, err = c.projectRuntime(ctx, in.DefaultRuntimeID)
		if err != nil {
			return Project{}, err
		}
	}
	if in.InitialEnvironment != nil {
		i := *in.InitialEnvironment
		if i.Name == "" {
			i.Name = "production"
		}
		if i.Kind == "" {
			i.Kind = "production"
		}
		if i.ApplicationID != "" || i.CopyEnvironmentID != "" || i.RegistryCredentialRef != "" || (i.RuntimeID != "" && i.RuntimeID != p.DefaultRuntimeID) {
			return Project{}, invalid("validation.project_initial_environment", "initial_environment", "initial environment must be empty and use the project runtime")
		}
		if !environmentSlug.MatchString(i.Name) {
			return Project{}, invalid("validation.application_identity_invalid", "initial_environment/name", "invalid environment slug")
		}
		e := Environment{ProjectID: p.ID, Name: i.Name, DisplayName: i.DisplayName, Kind: i.Kind, GatewayIDs: i.GatewayIDs, RuntimeID: p.DefaultRuntimeID}
		e.ID = id.New("env")
		prefix := p.Slug + "-" + e.Name
		if len(prefix) > 54 {
			prefix = strings.TrimRight(prefix[:54], "-")
		}
		e.ProjectName = prefix + "-" + e.ID[len(e.ID)-8:]
		normalizeEnvironment(&e)
		if err = c.validateEnvironmentMetadata(ctx, e); err != nil {
			return Project{}, err
		}
		p.InitialEnvironmentID = e.ID
		w, err := c.controlWrite(e.ID, "environment", p.ID, e, 0)
		if err != nil {
			return Project{}, err
		}
		writes = append(writes, w)
		w, err = c.controlWrite("environment-name:"+p.ID+":"+e.Name, "environment-name", p.ID, map[string]string{"environment_id": e.ID}, 0)
		if err != nil {
			return Project{}, err
		}
		writes = append(writes, w)
	}
	w, err := c.controlWrite(p.ID, "project", "", p, 0)
	if err != nil {
		return Project{}, err
	}
	writes = append([]storage.ControlWrite{w}, writes...)
	w, err = c.controlWrite("project-slug:"+p.Slug, "project-slug", "", map[string]string{"project_id": p.ID}, 0)
	if err != nil {
		return Project{}, err
	}
	writes = append(writes, w)
	if idem != "" {
		w, err = c.controlWrite(requestID, "project-request", "", map[string]string{"project_id": p.ID, "fingerprint": fingerprint}, 0)
		if err != nil {
			return Project{}, err
		}
		writes = append(writes, w)
	}
	if err = c.store.WriteControlDocuments(ctx, writes); err != nil {
		if idem != "" && errors.Is(err, storage.ErrConflict) {
			if existing, e := readExisting(); !errors.Is(e, storage.ErrNotFound) {
				return existing, e
			}
		}
		return Project{}, err
	}
	return c.GetProject(ctx, p.ID)
}

func (c *Core) UpdateProject(ctx context.Context, key string, in ProjectSettings, expected int64) (Project, error) {
	if err := c.requireControlManagement(); err != nil {
		return Project{}, err
	}
	p, err := c.GetProject(ctx, key)
	if err != nil {
		return p, err
	}
	if p.System {
		return p, storage.ErrSystemProtected
	}
	if p.Revision != expected {
		return p, storage.ErrPrecondition
	}
	in.Name = strings.TrimSpace(in.Name)
	if strings.EqualFold(in.Name, "Gateway") {
		return p, storage.ErrSystemProtected
	}
	in.Description = strings.TrimSpace(in.Description)
	if in.Name == "" || len(in.Name) > 255 || len(in.Description) > 4096 {
		return p, invalid("validation.project_name", "name", "invalid project name or description")
	}
	if in.DefaultRuntimeID != "" {
		if _, err = c.requireManagement(ctx, in.DefaultRuntimeID); err != nil {
			return p, invalid("validation.project_runtime", "default_runtime_id", "select an enabled, manageable runtime")
		}
	}
	p.Name, p.Description, p.DefaultRuntimeID = in.Name, in.Description, in.DefaultRuntimeID
	if err = c.saveControl(ctx, p.ID, "project", "", p, expected); err != nil {
		return p, err
	}
	return c.GetProject(ctx, key)
}

func (c *Core) UpdateEnvironment(ctx context.Context, key string, in EnvironmentSettings, expected int64) (Environment, error) {
	if err := c.requireControlManagement(); err != nil {
		return Environment{}, err
	}
	e, err := c.GetEnvironment(ctx, key)
	if err != nil {
		return e, err
	}
	if e.System {
		return e, storage.ErrSystemProtected
	}
	if e.Revision != expected {
		return e, storage.ErrPrecondition
	}
	previousGateways := e.GatewayIDs
	e.DisplayName, e.Kind, e.GatewayIDs = strings.TrimSpace(in.DisplayName), in.Kind, in.GatewayIDs
	if err = c.validateEnvironmentMetadata(ctx, e); err != nil {
		return e, err
	}
	if e.ApplicationID != "" && !slices.Equal(previousGateways, e.GatewayIDs) {
		app, readErr := c.store.GetApplication(ctx, e.ApplicationID)
		if readErr != nil {
			return e, readErr
		}
		if app.ActiveRevisionID != "" {
			revision, readErr := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
			if readErr != nil {
				return e, readErr
			}
			intents, readErr := c.revisionEntrypointIntents(ctx, revision)
			if readErr != nil {
				return e, readErr
			}
			for _, intent := range intents {
				if !slices.Contains(e.GatewayIDs, intent.Entry.GatewayID) {
					return e, invalid("validation.environment_gateway_in_use", "gateway_ids", "an ingress intent still references this gateway")
				}
			}
		}
		services, err := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: e.ApplicationID})
		if err != nil {
			return e, err
		}
		for _, service := range services {
			bindings, err := c.store.ListServiceDomainBindings(ctx, service.ID)
			if err != nil {
				return e, err
			}
			for _, b := range bindings {
				if !b.Route.Enabled {
					continue
				}
				for page := (storage.Page{Limit: 200}); ; {
					gateways, err := c.store.ListGateways(ctx, page)
					if err != nil {
						return e, err
					}
					for _, g := range gateways {
						if g.PoolID == b.Domain.PoolID && !slices.Contains(e.GatewayIDs, g.ID) {
							return e, invalid("validation.environment_gateway_in_use", "gateway_ids", "an active domain still uses this gateway pool")
						}
					}
					if len(gateways) < page.Limit {
						break
					}
					page.After = gateways[len(gateways)-1].ID
				}
			}
		}
	}
	if err = c.saveControl(ctx, e.ID, "environment", e.ProjectID, e, expected); err != nil {
		return e, err
	}
	return c.GetEnvironment(ctx, key)
}

func (c *Core) DeleteEmptyEnvironment(ctx context.Context, key string, expected int64) error {
	if err := c.requireControlManagement(); err != nil {
		return err
	}
	e, err := c.GetEnvironment(ctx, key)
	if err != nil {
		return err
	}
	return c.store.DeleteEmptyEnvironment(ctx, e.ID, e.ProjectID, expected)
}
