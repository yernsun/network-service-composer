package core

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sort"
	"time"

	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/projectlife"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type ProjectDeletionWorker struct {
	Core   *Core
	Locker lease.Locker
}

func (w ProjectDeletionWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Core.config.Management.PollInterval.Std())
	defer ticker.Stop()
	for {
		if w.Core.config.Management.Enabled {
			if err := w.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.ErrorContext(ctx, "project deletion worker", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w ProjectDeletionWorker) RunOnce(ctx context.Context) error {
	if w.Core.config.Controller.RecoveryMode {
		return nil
	}
	for page := (storage.Page{Limit: 200}); ; {
		docs, err := w.Core.store.ListControlDocuments(ctx, "project-deletion", "", page)
		if err != nil {
			return err
		}
		for _, doc := range docs {
			task, err := w.Core.GetProjectDeletion(ctx, doc.ID)
			if err != nil {
				return err
			}
			if task.Status != "QUEUED" && task.Status != "RUNNING" {
				continue
			}
			if recoveryHeld, err := storage.RecoveryHeld(ctx, w.Core.store, "project-deletion", task.ID); err != nil {
				return err
			} else if recoveryHeld {
				continue
			}
			held, err := w.Locker.TryLock(ctx, "project-deletion:"+task.ProjectID)
			if errors.Is(err, lease.ErrBusy) {
				continue
			}
			if err != nil {
				return err
			}
			err = w.execute(ctx, task.ID, held)
			releaseErr := held.Release(context.WithoutCancel(ctx))
			if err != nil {
				return err
			}
			if releaseErr != nil {
				return releaseErr
			}
		}
		if len(docs) < page.Limit {
			return nil
		}
		page.After = docs[len(docs)-1].ID
	}
}

func (c *Core) saveDeletion(ctx context.Context, task *ProjectDeletion) error {
	w, err := c.controlWrite(task.ID, "project-deletion", task.ProjectID, *task, task.Revision)
	if err != nil {
		return err
	}
	if err = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{w}); err != nil {
		return err
	}
	// The committed revision is already known. A canceled follow-up read must
	// not leave the worker using the previous ETag when it records a failure.
	w.Document.Revision = task.Revision + 1
	w.Document.CreatedAt = task.CreatedAt
	task.ControlDocument = w.Document
	return nil
}

func (c *Core) failDeletion(ctx context.Context, task *ProjectDeletion, code, status string) error {
	task.ErrorCode, task.Status = code, status
	if len(task.Attempts) > 0 {
		attempt := &task.Attempts[len(task.Attempts)-1]
		now := c.now()
		attempt.FinishedAt = &now
		attempt.Status = status
		attempt.ErrorCode = code
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return c.saveDeletion(saveCtx, task)
}

func (w ProjectDeletionWorker) execute(parent context.Context, key string, held lease.Lease) error {
	c := w.Core
	if recoveryHeld, err := storage.RecoveryHeld(parent, c.store, "project-deletion", key); err != nil || recoveryHeld {
		return err
	}
	task, err := c.GetProjectDeletion(parent, key)
	if err != nil {
		return err
	}
	p, err := c.GetProject(parent, task.ProjectID)
	if err != nil {
		return err
	}
	if p.System {
		return storage.ErrSystemProtected
	}
	if task.Status == "RUNNING" {
		return c.failDeletion(parent, &task, "project.deletion.interrupted", "UNKNOWN")
	}
	if task.Status != "QUEUED" {
		return nil
	}
	ctx, cancel := context.WithTimeout(storage.WithProjectDeletion(parent, task.ID), c.config.Management.DeployTimeout.Std())
	defer cancel()
	// A lost controller lease cancels outstanding runtime calls. Every durable
	// completion also uses an ETag; an older worker cannot finish a newer attempt.
	monitorDone := make(chan struct{})
	defer close(monitorDone)
	go func() {
		ticker := time.NewTicker(c.config.Management.HeartbeatInterval.Std())
		defer ticker.Stop()
		for {
			select {
			case <-monitorDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if held.Check(ctx) != nil {
					cancel()
					return
				}
			}
		}
	}()
	task.Status = "RUNNING"
	task.Attempts = append(task.Attempts, DeletionAttempt{Number: len(task.Attempts) + 1, Actor: task.Actor, PreviewID: task.Preview.ID, StartedAt: c.now(), Status: "RUNNING"})
	if err = c.saveDeletion(ctx, &task); err != nil {
		return err
	}
	fail := func(e error) error {
		if leaseErr := held.Check(context.WithoutCancel(parent)); leaseErr != nil {
			return leaseErr
		}
		code, status := "project.deletion.execution_unknown", "UNKNOWN"
		if errors.Is(e, projectlife.ErrRunning) {
			code, status = "project.deletion.running", "BLOCKED"
		}
		if errors.Is(e, projectlife.ErrChanged) || errors.Is(e, storage.ErrPrecondition) {
			code, status = "project.deletion.scope_changed", "BLOCKED"
		}
		if errors.Is(e, projectlife.ErrOwnership) || errors.Is(e, storage.ErrConflict) {
			code, status = "project.deletion.ownership", "BLOCKED"
		}
		if errors.Is(e, projectlife.ErrDataRetention) {
			code, status = "project.deletion.data_retention", "BLOCKED"
		}
		return c.failDeletion(ctx, &task, code, status)
	}
	for i := range task.Preview.Applications {
		target := &task.Preview.Applications[i]
		app, err := c.store.GetApplication(ctx, target.ID)
		if err != nil {
			return fail(err)
		}
		rt, err := c.requireManagement(ctx, target.RuntimeID)
		if err != nil {
			return fail(err)
		}
		services, err := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
		if err != nil {
			return fail(err)
		}
		observed, err := c.ProjectRuntime.Observe(ctx, rt, app, services)
		if err != nil {
			return fail(err)
		}
		if target.Status == "SUCCEEDED" {
			if len(observed.Objects) > 0 {
				return fail(projectlife.ErrChanged)
			}
			continue
		}
		if !projectlife.ReviewedSubset(observed, target.Observation) {
			return fail(projectlife.ErrChanged)
		}
		if !task.Preview.Force && !projectlife.Stopped(observed) {
			return fail(projectlife.ErrRunning)
		}
		if err = held.Check(ctx); err != nil {
			return err
		}
		if len(observed.Objects) > 0 {
			if runtime, ok := c.ProjectRuntime.(projectlife.ProgressRuntime); ok {
				err = runtime.RemoveWithProgress(ctx, rt, app, services, target.Observation, task.Preview.Force, func(obj projectlife.Object) error {
					if err := held.Check(ctx); err != nil {
						return err
					}
					a := &task.Preview.Applications[i]
					if slices.Contains(a.CompletedObjects, obj.ID) {
						return nil
					}
					a.CompletedObjects = append(a.CompletedObjects, obj.ID)
					return c.saveDeletion(ctx, &task)
				})
			} else {
				err = c.ProjectRuntime.Remove(ctx, rt, app, services, target.Observation, task.Preview.Force)
			}
			if err != nil {
				return fail(err)
			}
		}
		observed, err = c.ProjectRuntime.Observe(ctx, rt, app, services)
		if err != nil {
			return fail(err)
		}
		if len(observed.Objects) > 0 {
			return fail(projectlife.ErrChanged)
		}
		if err = held.Check(ctx); err != nil {
			return err
		}
		task.Preview.Applications[i].Status = "SUCCEEDED"
		if err = c.saveDeletion(ctx, &task); err != nil {
			return err
		}
	}
	if task.Phase == "WORKLOADS" {
		task.Phase = "INGRESS"
		now := c.now()
		task.CleanupAt = &now
		if err = c.saveDeletion(ctx, &task); err != nil {
			return err
		}
	}
	if err = held.Check(ctx); err != nil {
		return err
	}
	if err = c.cleanupProjectIngress(ctx, &task); err != nil {
		return fail(err)
	}
	if err = held.Check(ctx); err != nil {
		return err
	}
	if err = c.finishProjectDeletion(ctx, &task); err != nil {
		return fail(err)
	}
	return nil
}

func (c *Core) cleanupProjectIngress(ctx context.Context, task *ProjectDeletion) error {
	owned := map[string]bool{}
	for _, r := range task.Preview.Resources {
		owned[r.ID] = true
	}
	for _, resource := range task.Preview.Resources {
		switch resource.Kind {
		case "route":
			route, err := c.store.GetRoute(ctx, resource.ID)
			if err != nil {
				return err
			}
			if !owned[route.ServiceID] {
				return projectlife.ErrOwnership
			}
			if route.Enabled {
				if err = c.store.DeleteRoute(ctx, route.ID, route.Revision); err != nil {
					return err
				}
			}
		case "domain":
			domain, err := c.store.GetDomain(ctx, resource.ID)
			if err != nil {
				return err
			}
			if domain.Enabled {
				if err = c.store.DeleteDomain(ctx, domain.ID, domain.Revision); err != nil {
					return err
				}
			}
		case "nginx-active":
			doc, err := c.store.GetControlDocument(ctx, resource.ID)
			if err != nil {
				return err
			}
			configuration, err := c.NginxConfiguration(ctx, doc.ParentID, "nginx-active")
			if err != nil {
				return err
			}
			retained := configuration.Snippets[:0]
			changed := false
			for _, snippet := range configuration.Snippets {
				if owned[snippet.ResourceID] {
					changed = true
				} else {
					retained = append(retained, snippet)
				}
			}
			configuration.Snippets = retained
			if changed {
				if err = c.saveControl(ctx, doc.ID, "nginx-active", doc.ParentID, configuration, doc.Revision); err != nil {
					return err
				}
			}
		}
	}
	for i := range task.Preview.Pools {
		pool := &task.Preview.Pools[i]
		if pool.CleanupRevision == 0 {
			revision, err := c.store.BumpPoolRevision(ctx, pool.ID)
			if err != nil {
				return err
			}
			pool.CleanupRevision = revision
			if err = c.saveDeletion(ctx, task); err != nil {
				return err
			}
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		complete := true
		for _, pool := range task.Preview.Pools {
			for _, key := range pool.GatewayIDs {
				evidence, err := c.GatewayEvidence(ctx, key)
				if err != nil {
					return err
				}
				if !evidence.LoadingConfirmed || evidence.StartupState != "CONFIRMED" || evidence.ConfigurationState != "CONFIRMED" || evidence.ConfirmedRevision < pool.CleanupRevision || evidence.LastConfirmedAt == nil || task.CleanupAt == nil || !evidence.LastConfirmedAt.After(*task.CleanupAt) {
					complete = false
				}
			}
		}
		if complete {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Core) finishProjectDeletion(ctx context.Context, task *ProjectDeletion) error {
	p, err := c.GetProject(ctx, task.ProjectID)
	if err != nil {
		return err
	}
	current, err := c.projectDeletionScope(ctx, p, task.Preview.Force, true)
	if err != nil {
		return err
	}
	owned := map[string]bool{}
	for _, r := range task.Preview.Resources {
		owned[r.ID] = true
	}
	for _, r := range current.Resources {
		if !owned[r.ID] {
			return projectlife.ErrChanged
		}
	}
	for _, pool := range current.Pools {
		matched := false
		for _, reviewed := range task.Preview.Pools {
			if reviewed.ID == pool.ID && slices.Equal(reviewed.GatewayIDs, pool.GatewayIDs) {
				matched = true
			}
		}
		if !matched {
			return projectlife.ErrChanged
		}
	}
	if len(current.Blockers) > 0 {
		return projectlife.ErrOwnership
	}
	for _, app := range current.Applications {
		if len(app.Observation.Objects) != 0 {
			return projectlife.ErrChanged
		}
	}
	writes := []storage.ControlWrite{}
	archive := func(key string, remove bool) error {
		doc, err := c.store.GetControlDocument(ctx, key)
		if err != nil {
			return err
		}
		doc.UpdatedAt = c.now()
		writes = append(writes, storage.ControlWrite{Document: doc, Expected: doc.Revision, Remove: remove, Archive: !remove})
		return nil
	}
	if err = archive(p.ID, false); err != nil {
		return err
	}
	if err = archive("project-slug:"+p.Slug, true); err != nil {
		return err
	}
	ids := []string{p.ID}
	for _, r := range current.Resources {
		if r.Kind == "nginx-active" || r.Kind == "nginx-draft" || r.Kind == "pool" {
			continue
		}
		ids = append(ids, r.ID)
		if r.Kind == "environment" {
			env, err := c.GetEnvironment(ctx, r.ID)
			if err != nil {
				return err
			}
			if err = archive(r.ID, false); err != nil {
				return err
			}
			if err = archive("environment-name:"+p.ID+":"+env.Name, true); err != nil {
				return err
			}
		}
		if r.Kind == "application" {
			if err = archive("application-environment:"+r.ID, false); err != nil {
				return err
			}
		}
	}
	// Keep encrypted configuration and history for audit, but remove them from
	// the live control-plane namespace so old webhook/policy reads cannot run.
	for _, owner := range ids {
		for _, kind := range []string{"variables", "environment-compose", "image-policy", "image-webhook", entrypointIntentKind} {
			for page := (storage.Page{Limit: 200}); ; {
				docs, err := c.store.ListControlDocuments(ctx, kind, owner, page)
				if err != nil {
					return err
				}
				for _, doc := range docs {
					if err = archive(doc.ID, false); err != nil {
						return err
					}
				}
				if len(docs) < page.Limit {
					break
				}
				page.After = docs[len(docs)-1].ID
			}
		}
	}
	for _, key := range ids {
		if err = archive("project-guard:"+key, false); err != nil {
			return err
		}
	}
	for _, pool := range task.Preview.Pools {
		if err = archive("project-ingress-guard:"+pool.ID, true); err != nil {
			return err
		}
	}
	// Archive child records while their live parent relationships can still be
	// checked. Tombstones are written last, in the same transaction.
	order := func(kind string) int {
		switch kind {
		case "environment":
			return 1
		case "project":
			return 2
		case "project-guard":
			return 3
		}
		return 0
	}
	sort.SliceStable(writes, func(i, j int) bool { return order(writes[i].Document.Kind) < order(writes[j].Document.Kind) })
	completed := *task
	completed.Attempts = append([]DeletionAttempt{}, task.Attempts...)
	now := c.now()
	completed.Status = "SUCCEEDED"
	completed.Phase = "COMPLETE"
	completed.ErrorCode = ""
	completed.FinishedAt = &now
	if len(completed.Attempts) > 0 {
		a := &completed.Attempts[len(completed.Attempts)-1]
		a.Status = "SUCCEEDED"
		a.FinishedAt = &now
	}
	w, err := c.controlWrite(task.ID, "project-deletion", task.ProjectID, completed, task.Revision)
	if err != nil {
		return err
	}
	writes = append(writes, w)
	return c.store.MutateProjectLifecycle(ctx, storage.ProjectLifecycleMutation{ProjectID: p.ID, Expected: p.Revision, Resources: current.Resources, Gateways: deletionGatewayIDs(current.Pools), Writes: writes, Finishing: true})
}
