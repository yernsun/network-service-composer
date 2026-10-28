package core

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/projectlife"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type DeletionBlocker struct {
	Code       string `json:"code"`
	ResourceID string `json:"resource_id"`
}
type DeletionApplication struct {
	ID               string                  `json:"id"`
	Name             string                  `json:"name"`
	EnvironmentID    string                  `json:"environment_id"`
	RuntimeID        string                  `json:"runtime_id"`
	Observation      projectlife.Observation `json:"observation"`
	Status           string                  `json:"status"`
	CompletedObjects []string                `json:"completed_objects"`
}
type DeletionPool struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	GatewayIDs      []string `json:"gateway_ids"`
	ConfigRevision  int64    `json:"config_revision"`
	CleanupRevision int64    `json:"cleanup_revision"`
}
type ProjectDeletionPreview struct {
	storage.ControlDocument
	ProjectID       string                      `json:"project_id"`
	ProjectName     string                      `json:"project_name"`
	ProjectSlug     string                      `json:"project_slug"`
	ProjectRevision int64                       `json:"project_revision"`
	Force           bool                        `json:"force"`
	Resources       []storage.LifecycleResource `json:"resources"`
	Applications    []DeletionApplication       `json:"applications"`
	Pools           []DeletionPool              `json:"pools"`
	Blockers        []DeletionBlocker           `json:"blockers"`
	Preserved       []string                    `json:"preserved"`
	Fingerprint     string                      `json:"fingerprint"`
	ExpiresAt       time.Time                   `json:"expires_at"`
}
type ProjectDeletionInput struct {
	PreviewID   string `json:"preview_id"`
	Force       bool   `json:"force"`
	ConfirmSlug string `json:"confirm_slug"`
}
type DeletionAttempt struct {
	Number     int        `json:"number"`
	Actor      string     `json:"actor"`
	PreviewID  string     `json:"preview_id"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Status     string     `json:"status"`
	ErrorCode  string     `json:"error_code,omitempty"`
}
type ProjectDeletion struct {
	storage.ControlDocument
	ProjectID  string                 `json:"project_id"`
	Preview    ProjectDeletionPreview `json:"preview"`
	Status     string                 `json:"status"`
	Phase      string                 `json:"phase"`
	ErrorCode  string                 `json:"error_code,omitempty"`
	Actor      string                 `json:"actor"`
	Attempts   []DeletionAttempt      `json:"attempts"`
	CleanupAt  *time.Time             `json:"cleanup_at,omitempty"`
	FinishedAt *time.Time             `json:"finished_at,omitempty"`
}

func deletionResource(key, kind, name, env string, revision int64) storage.LifecycleResource {
	return storage.LifecycleResource{ID: key, Kind: kind, Name: name, EnvironmentID: env, Revision: revision}
}

func deletionGatewayIDs(pools []DeletionPool) map[string][]string {
	result := map[string][]string{}
	for _, pool := range pools {
		result[pool.ID] = pool.GatewayIDs
	}
	return result
}

func (c *Core) projectDeletionScope(ctx context.Context, p Project, force bool, observe bool) (ProjectDeletionPreview, error) {
	if p.System {
		return ProjectDeletionPreview{}, storage.ErrSystemProtected
	}
	v := ProjectDeletionPreview{ProjectID: p.ID, ProjectName: p.Name, ProjectSlug: p.Slug, ProjectRevision: p.Revision, Force: force, Resources: []storage.LifecycleResource{}, Applications: []DeletionApplication{}, Pools: []DeletionPool{}, Blockers: []DeletionBlocker{}, Preserved: []string{"volumes", "bind_mounts", "images", "networks", "secrets", "configs", "certificates", "runtimes", "gateway_pools"}, ExpiresAt: c.now().Add(5 * time.Minute)}
	block := func(code, key string) { v.Blockers = append(v.Blockers, DeletionBlocker{Code: code, ResourceID: key}) }
	envs, err := c.allProjectEnvironments(ctx, p.ID)
	if err != nil {
		return v, err
	}
	owned := map[string]bool{}
	apps := map[string]bool{}
	environmentApps := map[string]string{}
	for _, env := range envs {
		environmentApps[env.ID] = env.ApplicationID
		v.Resources = append(v.Resources, deletionResource(env.ID, "environment", env.DisplayName, env.ID, env.Revision))
		if env.ApplicationID == "" {
			continue
		}
		app, err := c.store.GetApplication(ctx, env.ApplicationID)
		if err != nil {
			return v, err
		}
		if apps[app.ID] || app.RuntimeID != env.RuntimeID || !app.Managed {
			block("project.deletion.ownership", app.ID)
			continue
		}
		apps[app.ID] = true
		claim, _, err := readControl[struct {
			EnvironmentID string `json:"environment_id"`
		}](ctx, c.store, "application-environment:"+app.ID, "application-environment")
		if err != nil || claim.EnvironmentID != env.ID {
			block("project.deletion.ownership", app.ID)
			continue
		}
		v.Resources = append(v.Resources, deletionResource(app.ID, "application", app.Name, env.ID, app.Revision))
		services, err := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
		if err != nil {
			return v, err
		}
		for _, s := range services {
			if !s.Managed || s.RuntimeID != env.RuntimeID {
				block("project.deletion.ownership", s.ID)
			}
			owned[s.ID] = true
			v.Resources = append(v.Resources, deletionResource(s.ID, "service", s.Name, env.ID, s.Revision))
		}
		for page := (storage.Page{Limit: 200}); ; {
			operations, err := c.store.ListDeploymentOperations(ctx, app.ID, page)
			if err != nil {
				return v, err
			}
			for _, op := range operations {
				if op.Status == model.OperationQueued || op.Status == model.OperationRunning {
					block("project.deletion.busy", op.ID)
				}
			}
			if len(operations) < page.Limit {
				break
			}
			page.After = operations[len(operations)-1].ID
		}
		target := DeletionApplication{ID: app.ID, Name: app.Name, EnvironmentID: env.ID, RuntimeID: env.RuntimeID, Status: "PENDING", CompletedObjects: []string{}, Observation: projectlife.Observation{Objects: []projectlife.Object{}, Preserved: []string{}}}
		if observe {
			rt, err := c.requireManagement(ctx, env.RuntimeID)
			if err != nil {
				block("project.deletion.runtime_unavailable", env.RuntimeID)
			} else {
				observeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				target.Observation, err = c.ProjectRuntime.Observe(observeCtx, rt, app, services)
				cancel()
				if err != nil {
					if errors.Is(err, projectlife.ErrOwnership) {
						block("project.deletion.ownership", app.ID)
					} else if errors.Is(err, projectlife.ErrDataRetention) {
						block("project.deletion.data_retention", app.ID)
					} else {
						block("project.deletion.runtime_unavailable", env.RuntimeID)
					}
				} else if !force && !projectlife.Stopped(target.Observation) {
					block("project.deletion.running", app.ID)
				}
			}
		}
		v.Applications = append(v.Applications, target)
	}
	// Legacy or partially repaired data can contain a reverse claim without
	// the matching environment link. It is evidence of unresolved ownership,
	// never evidence that the project is empty.
	for page := (storage.Page{Limit: 200}); ; {
		claims, err := c.store.ListControlDocuments(ctx, "application-environment", "", page)
		if err != nil {
			return v, err
		}
		for _, doc := range claims {
			var claim struct {
				EnvironmentID string `json:"environment_id"`
			}
			if err = json.Unmarshal(doc.Payload, &claim); err != nil {
				return v, err
			}
			if expected, belongs := environmentApps[claim.EnvironmentID]; belongs && (expected == "" || expected != doc.ParentID || doc.ID != "application-environment:"+expected) {
				block("project.deletion.ownership", doc.ID)
			}
		}
		if len(claims) < page.Limit {
			break
		}
		page.After = claims[len(claims)-1].ID
	}
	// The singular application claim is authoritative. Corrupt or legacy double
	// references must be resolved before either project may remove the workload.
	for page := (storage.Page{Limit: 200}); ; {
		items, err := c.ListEnvironments(ctx, "", page)
		if err != nil {
			return v, err
		}
		for _, other := range items {
			if other.ProjectID != p.ID && apps[other.ApplicationID] {
				block("project.deletion.shared_reference", other.ID)
			}
		}
		if len(items) < page.Limit {
			break
		}
		page.After = items[len(items)-1].ID
	}
	routes := []model.Route{}
	domainIDs := map[string]bool{}
	for page := (storage.Page{Limit: 200}); ; {
		items, err := c.store.ListRoutes(ctx, page)
		if err != nil {
			return v, err
		}
		routes = append(routes, items...)
		for _, r := range items {
			if owned[r.ServiceID] {
				domainIDs[r.DomainID] = true
			}
		}
		if len(items) < page.Limit {
			break
		}
		page.After = items[len(items)-1].ID
	}
	poolIDs := map[string]bool{}
	for key := range domainIDs {
		domain, err := c.store.GetDomain(ctx, key)
		if err != nil {
			return v, err
		}
		v.Resources = append(v.Resources, deletionResource(domain.ID, "domain", domain.Host, "", domain.Revision))
		poolIDs[domain.PoolID] = true
		for _, route := range routes {
			if route.DomainID != key {
				continue
			}
			if !owned[route.ServiceID] {
				block("project.deletion.shared_reference", route.ID)
			}
			v.Resources = append(v.Resources, deletionResource(route.ID, "route", route.Path, "", route.Revision))
		}
	}
	resourceIDs := map[string]bool{}
	for _, r := range v.Resources {
		resourceIDs[r.ID] = true
	}
	for poolID := range poolIDs {
		p, err := c.store.GetPool(ctx, poolID)
		if err != nil {
			return v, err
		}
		v.Resources = append(v.Resources, deletionResource(p.ID, "pool", p.Name, "", p.Revision))
		pool := DeletionPool{ID: p.ID, Name: p.Name, ConfigRevision: p.DesiredRevision, GatewayIDs: []string{}}
		head, headErr := c.store.GetControlDocument(ctx, poolID+":nginx-release-head")
		if headErr != nil && !errors.Is(headErr, storage.ErrNotFound) {
			return v, headErr
		}
		if headErr == nil {
			var ref map[string]string
			if err := json.Unmarshal(head.Payload, &ref); err != nil {
				return v, err
			}
			release, err := c.GetNginxRelease(ctx, poolID, ref["release_id"])
			if err != nil {
				return v, err
			}
			for _, target := range release.Targets {
				if target.Status == "PENDING" || target.Status == "RUNNING" || target.Status == "UNKNOWN" {
					block("project.deletion.busy", release.ID)
					break
				}
			}
		}
		for page := (storage.Page{Limit: 200}); ; {
			gateways, err := c.store.ListGateways(ctx, page)
			if err != nil {
				return v, err
			}
			for _, g := range gateways {
				if g.PoolID != poolID {
					continue
				}
				pool.GatewayIDs = append(pool.GatewayIDs, g.ID)
				if observe {
					evidence, err := c.GatewayEvidence(ctx, g.ID)
					if err != nil || !g.Enabled || !evidence.LoadingConfirmed || evidence.StartupState != "CONFIRMED" {
						block("project.deletion.gateway_unavailable", g.ID)
					}
				}
			}
			if len(gateways) < page.Limit {
				break
			}
			page.After = gateways[len(gateways)-1].ID
		}
		for _, kind := range []string{"nginx-active", "nginx-draft"} {
			config, err := c.NginxConfiguration(ctx, poolID, kind)
			if err != nil {
				return v, err
			}
			for _, snippet := range config.Snippets {
				// Shared HTTP snippets and user drafts are not rewritten by a
				// deletion. Explicitly associated active snippets are cleaned below.
				if snippet.Scope == "http" || (kind == "nginx-draft" && resourceIDs[snippet.ResourceID]) {
					block("project.deletion.shared_configuration", config.ID)
				}
			}
			if config.Revision > 0 {
				v.Resources = append(v.Resources, deletionResource(config.ID, kind, kind, "", config.Revision))
			}
		}
		if observe && len(pool.GatewayIDs) == 0 {
			block("project.deletion.gateway_unavailable", poolID)
		}
		sort.Strings(pool.GatewayIDs)
		v.Pools = append(v.Pools, pool)
	}
	sort.Slice(v.Resources, func(i, j int) bool { return v.Resources[i].ID < v.Resources[j].ID })
	sort.Slice(v.Pools, func(i, j int) bool { return v.Pools[i].ID < v.Pools[j].ID })
	// Runtime activity is checked separately; changing evidence timestamps must
	// not invalidate a review of the same identities and versions.
	fingerprintResources := append([]storage.LifecycleResource{}, v.Resources...)
	for i := range fingerprintResources {
		// Discovery advances service revisions for observation timestamps. Actual
		// ownership and runtime identities are checked again at submission; these
		// metadata ticks must not make a reviewed scope impossible to submit.
		if fingerprintResources[i].Kind == "service" {
			fingerprintResources[i].Revision = 0
		}
	}
	raw, err := json.Marshal(struct {
		Resources []storage.LifecycleResource
		Pools     []DeletionPool
	}{fingerprintResources, v.Pools})
	if err != nil {
		return v, err
	}
	v.Fingerprint = digestString(string(raw))
	return v, nil
}

func (c *Core) PreviewProjectDeletion(ctx context.Context, key string, force bool) (ProjectDeletionPreview, error) {
	if err := c.requireControlManagement(); err != nil {
		return ProjectDeletionPreview{}, err
	}
	p, err := c.GetProject(ctx, key)
	if err != nil {
		return ProjectDeletionPreview{}, err
	}
	v, err := c.projectDeletionScope(ctx, p, force, true)
	if err != nil {
		return v, err
	}
	v.ID = id.New("prjpreview")
	if err = c.saveControl(ctx, v.ID, "project-deletion-preview", p.ID, v, 0); err != nil {
		return v, err
	}
	v.ControlDocument, err = c.store.GetControlDocument(ctx, v.ID)
	return v, err
}

func (c *Core) GetProjectDeletion(ctx context.Context, key string) (ProjectDeletion, error) {
	v, d, err := readControl[ProjectDeletion](ctx, c.store, key, "project-deletion")
	v.ControlDocument = d
	return v, err
}

func (c *Core) reviewedProjectDeletion(ctx context.Context, key string, in ProjectDeletionInput, expected int64) (Project, ProjectDeletionPreview, error) {
	p, err := c.GetProject(ctx, key)
	if err != nil {
		return p, ProjectDeletionPreview{}, err
	}
	if p.System {
		return p, ProjectDeletionPreview{}, storage.ErrSystemProtected
	}
	if p.Revision != expected {
		return p, ProjectDeletionPreview{}, storage.ErrPrecondition
	}
	if in.ConfirmSlug != p.Slug {
		return p, ProjectDeletionPreview{}, invalid("project.deletion.confirm_slug", "confirm_slug", "type the project slug to confirm")
	}
	v, document, err := readControl[ProjectDeletionPreview](ctx, c.store, in.PreviewID, "project-deletion-preview")
	if err != nil {
		return p, v, err
	}
	v.ControlDocument = document
	v.Payload = nil
	if v.ProjectID != key || v.Force != in.Force || v.ProjectRevision != expected || !v.ExpiresAt.After(c.now()) {
		return p, v, storage.ErrPrecondition
	}
	current, err := c.projectDeletionScope(ctx, p, in.Force, true)
	if err != nil {
		return p, v, err
	}
	if current.Fingerprint != v.Fingerprint {
		return p, v, storage.ErrPrecondition
	}
	if len(current.Blockers) > 0 {
		return p, v, invalid(current.Blockers[0].Code, "preview_id", "resolve deletion blockers and create a new preview")
	}
	for i, a := range current.Applications {
		if !projectlife.ReviewedSubset(a.Observation, v.Applications[i].Observation) {
			return p, v, invalid("project.deletion.scope_changed", "preview_id", "runtime objects changed; create a new preview")
		}
	}
	v.Resources = current.Resources
	return p, v, nil
}

func (c *Core) DeleteProject(ctx context.Context, key string, in ProjectDeletionInput, expected int64, idem, actor string) (ProjectDeletion, error) {
	if err := c.requireControlManagement(); err != nil {
		return ProjectDeletion{}, err
	}
	if idem == "" {
		return ProjectDeletion{}, invalid("validation.idempotency_required", "idempotency_key", "an idempotency key is required")
	}
	taskID := "prjdel_" + digestString(key+":"+idem)
	if task, err := c.GetProjectDeletion(ctx, taskID); err == nil {
		if task.Preview.ID != in.PreviewID || task.Preview.Force != in.Force || task.Preview.ProjectSlug != in.ConfirmSlug {
			return task, storage.ErrConflict
		}
		return task, nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return task, err
	}
	p, v, err := c.reviewedProjectDeletion(ctx, key, in, expected)
	if err != nil {
		return ProjectDeletion{}, err
	}
	if p.DeletionID != "" {
		return ProjectDeletion{}, storage.ErrConflict
	}
	v.ID = in.PreviewID
	task := ProjectDeletion{ProjectID: key, Preview: v, Status: "QUEUED", Phase: "WORKLOADS", Actor: actor, Attempts: []DeletionAttempt{}}
	task.ID = taskID
	p.DeletionID = task.ID
	writes := []storage.ControlWrite{}
	for _, value := range []struct {
		key, kind, parent string
		value             any
		expected          int64
	}{{p.ID, "project", "", p, p.Revision}, {task.ID, "project-deletion", key, task, 0}} {
		w, err := c.controlWrite(value.key, value.kind, value.parent, value.value, value.expected)
		if err != nil {
			return task, err
		}
		writes = append(writes, w)
	}
	guardIDs := []string{p.ID}
	for _, r := range v.Resources {
		if r.Kind != "nginx-active" && r.Kind != "nginx-draft" && r.Kind != "pool" {
			guardIDs = append(guardIDs, r.ID)
		}
	}
	for _, resource := range guardIDs {
		w, err := c.controlWrite("project-guard:"+resource, "project-guard", resource, storage.ProjectResourceGuard{ProjectID: key, OperationID: task.ID}, 0)
		if err != nil {
			return task, err
		}
		writes = append(writes, w)
	}
	for _, pool := range v.Pools {
		w, err := c.controlWrite("project-ingress-guard:"+pool.ID, "project-ingress-guard", key, storage.ProjectResourceGuard{ProjectID: key, OperationID: task.ID}, 0)
		if err != nil {
			return task, err
		}
		writes = append(writes, w)
	}
	if err = c.store.MutateProjectLifecycle(ctx, storage.ProjectLifecycleMutation{ProjectID: key, Expected: expected, Resources: v.Resources, Gateways: deletionGatewayIDs(v.Pools), Writes: writes}); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			if existing, e := c.GetProjectDeletion(ctx, taskID); e == nil {
				if existing.Preview.ID != in.PreviewID || existing.Preview.Force != in.Force || existing.Preview.ProjectSlug != in.ConfirmSlug {
					return existing, storage.ErrConflict
				}
				return existing, nil
			}
		}
		return task, err
	}
	return c.GetProjectDeletion(ctx, task.ID)
}

func (c *Core) ResumeProjectDeletion(ctx context.Context, key string, in ProjectDeletionInput, expected int64, actor string) (ProjectDeletion, error) {
	if err := c.requireControlManagement(); err != nil {
		return ProjectDeletion{}, err
	}
	task, err := c.GetProjectDeletion(ctx, key)
	if err != nil {
		return task, err
	}
	if task.Revision != expected {
		return task, storage.ErrPrecondition
	}
	if !slices.Contains([]string{"FAILED", "UNKNOWN", "BLOCKED"}, task.Status) {
		return task, storage.ErrConflict
	}
	p, err := c.GetProject(ctx, task.ProjectID)
	if err != nil {
		return task, err
	}
	_, preview, err := c.reviewedProjectDeletion(ctx, task.ProjectID, in, p.Revision)
	if err != nil {
		return task, err
	}
	if preview.Force != task.Preview.Force {
		return task, storage.ErrConflict
	}
	previous := map[string]bool{}
	for _, r := range task.Preview.Resources {
		previous[r.ID] = true
	}
	for _, r := range preview.Resources {
		if !previous[r.ID] {
			return task, invalid("project.deletion.scope_changed", "preview_id", "the resource membership changed")
		}
	}
	preview.ID = in.PreviewID
	for i := range preview.Applications {
		for _, old := range task.Preview.Applications {
			if old.ID == preview.Applications[i].ID {
				preview.Applications[i].CompletedObjects = append([]string{}, old.CompletedObjects...)
			}
			if old.ID == preview.Applications[i].ID && old.Status == "SUCCEEDED" {
				if len(preview.Applications[i].Observation.Objects) > 0 {
					return task, invalid("project.deletion.scope_changed", "preview_id", "a removed application has new runtime objects")
				}
				preview.Applications[i].Status = "SUCCEEDED"
			}
		}
	}
	for i := range preview.Pools {
		for _, old := range task.Preview.Pools {
			if old.ID == preview.Pools[i].ID {
				preview.Pools[i].CleanupRevision = old.CleanupRevision
			}
		}
	}
	task.Preview = preview
	task.Status = "QUEUED"
	task.ErrorCode = ""
	task.Actor = actor
	if err = c.saveControl(ctx, task.ID, "project-deletion", task.ProjectID, task, expected); err != nil {
		return task, err
	}
	return c.GetProjectDeletion(ctx, task.ID)
}
