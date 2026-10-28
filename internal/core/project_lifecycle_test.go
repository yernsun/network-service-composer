package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/projectlife"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type projectRuntimeFixture struct {
	objects       []projectlife.Object
	failure       error
	removeFailure error
	removed       int
	force         bool
}

func (f *projectRuntimeFixture) Observe(context.Context, model.Runtime, model.Application, []model.Service) (projectlife.Observation, error) {
	return projectlife.Observation{Objects: append([]projectlife.Object{}, f.objects...), Preserved: []string{"volume:kept"}, ObservedAt: time.Now().UTC()}, f.failure
}
func (f *projectRuntimeFixture) Remove(_ context.Context, _ model.Runtime, _ model.Application, _ []model.Service, _ projectlife.Observation, force bool) error {
	f.removed++
	f.force = force
	if f.removeFailure != nil {
		return f.removeFailure
	}
	if !force {
		for _, o := range f.objects {
			if o.Active {
				return projectlife.ErrRunning
			}
		}
	}
	f.objects = nil
	return nil
}

func TestProjectAtomicCreateSettingsAndRuntimeDefaults(t *testing.T) {
	c, rt, _ := controlFixture(t)
	ctx := context.Background()
	in := ProjectInput{Name: "项目 <one>", Slug: "9-example", Description: "description", InitialEnvironment: &EnvironmentInput{DisplayName: "生产"}}
	p, err := c.CreateProjectWithEnvironment(ctx, in, "create")
	if err != nil {
		t.Fatal(err)
	}
	if p.DefaultRuntimeID != rt.ID || p.InitialEnvironmentID == "" || p.Description != in.Description {
		t.Fatal(p)
	}
	again, err := c.CreateProjectWithEnvironment(ctx, in, "create")
	if err != nil || again.ID != p.ID {
		t.Fatal(again, err)
	}
	in.Description = "changed"
	if _, err = c.CreateProjectWithEnvironment(ctx, in, "create"); !errors.Is(err, storage.ErrConflict) {
		t.Fatal(err)
	}
	// The unique slug fails after initial project/environment writes; the whole transaction rolls back.
	if _, err = c.CreateProjectWithEnvironment(ctx, in, "duplicate"); !errors.Is(err, storage.ErrConflict) {
		t.Fatal(err)
	}
	projects, _ := c.ListProjects(ctx, storage.Page{Limit: 200})
	envs, _ := c.store.ListControlDocuments(ctx, "environment", "", storage.Page{Limit: 200})
	if len(projects) != 1 || len(envs) != 1 {
		t.Fatal("partial project persisted", projects, envs)
	}
	env, err := c.GetEnvironment(ctx, p.InitialEnvironmentID)
	if err != nil || env.DisplayName != "生产" || env.Name != "production" || env.Kind != "production" {
		t.Fatal(env, err)
	}
	updated, err := c.UpdateProject(ctx, p.ID, ProjectSettings{Name: "renamed", Description: "new", DefaultRuntimeID: rt.ID}, p.Revision)
	if err != nil || updated.Slug != p.Slug {
		t.Fatal(updated, err)
	}
	if _, err = c.UpdateProject(ctx, p.ID, ProjectSettings{Name: "stale"}, p.Revision); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal(err)
	}
	if _, err = c.CreateEnvironmentChecked(ctx, p.ID, EnvironmentInput{Name: "dev", DisplayName: "开发", Kind: "development"}, p.Revision); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal(err)
	}
	dev, err := c.CreateEnvironmentChecked(ctx, p.ID, EnvironmentInput{Name: "dev", DisplayName: "开发", Kind: "development"}, updated.Revision)
	if err != nil || dev.RuntimeID != rt.ID {
		t.Fatal(dev, err)
	}
	newenv, err := c.UpdateEnvironment(ctx, dev.ID, EnvironmentSettings{DisplayName: "研发", Kind: "testing", GatewayIDs: []string{}}, dev.Revision)
	if err != nil || newenv.Name != dev.Name || newenv.ProjectName != dev.ProjectName {
		t.Fatal(newenv, err)
	}
	if err = c.DeleteEmptyEnvironment(ctx, dev.ID, dev.Revision); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal(err)
	}
	if err = c.DeleteEmptyEnvironment(ctx, dev.ID, newenv.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = c.GetEnvironment(ctx, dev.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
	if err = c.DeleteEmptyEnvironment(ctx, env.ID, env.Revision); !errors.Is(err, storage.ErrConflict) {
		t.Fatal(err)
	}
	if _, err = c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "dev"}); err != nil {
		t.Fatal("deleted slug was not released", err)
	}
}

func TestProjectRuntimeAmbiguityAndMetadataValidation(t *testing.T) {
	c, rt, pool := controlFixture(t)
	ctx := context.Background()
	second, err := c.CreateRuntime(ctx, CreateRuntimeInput{PoolID: pool.ID, Name: "second", RuntimeType: model.RuntimeDocker, Endpoint: "unix:///tmp/fixture.sock", ManagementEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateProjectWithEnvironment(ctx, ProjectInput{Name: "Ambiguous", Slug: "ambiguous", InitialEnvironment: &EnvironmentInput{}}, "ambiguous"); err == nil {
		t.Fatal("ambiguous runtime selected")
	}
	c.config.Management.DefaultRuntimeID = rt.ID
	p, err := c.CreateProjectWithEnvironment(ctx, ProjectInput{Name: "Default", Slug: "default", InitialEnvironment: &EnvironmentInput{}}, "default")
	if err != nil || p.DefaultRuntimeID != rt.ID {
		t.Fatal(p, err)
	}
	if _, err = c.UpdateProject(ctx, p.ID, ProjectSettings{Name: "x", DefaultRuntimeID: "missing"}, p.Revision); err == nil {
		t.Fatal("invalid default accepted")
	}
	p, err = c.UpdateProject(ctx, p.ID, ProjectSettings{Name: "Default", DefaultRuntimeID: second.ID}, p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	e, err := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "new"})
	if err != nil || e.RuntimeID != second.ID {
		t.Fatal(e, err)
	}
	initial, _ := c.GetEnvironment(ctx, p.InitialEnvironmentID)
	if initial.RuntimeID != rt.ID {
		t.Fatal("existing environment moved")
	}
	for _, in := range []EnvironmentSettings{{DisplayName: "", Kind: "custom"}, {DisplayName: "Name", Kind: "wrong"}, {DisplayName: "Name", Kind: "custom", GatewayIDs: []string{"foreign"}}} {
		if _, err = c.UpdateEnvironment(ctx, e.ID, in, e.Revision); err == nil {
			t.Fatal("invalid metadata accepted", in)
		}
	}
}

func projectDeletionFixture(t *testing.T) (*Core, Project, Environment, model.Application, *projectRuntimeFixture, ProjectDeletionWorker) {
	t.Helper()
	c, rt, _ := controlFixture(t)
	ctx := context.Background()
	p, err := c.CreateProjectWithEnvironment(ctx, ProjectInput{Name: "Deletion", Slug: "deletion", DefaultRuntimeID: rt.ID, InitialEnvironment: &EnvironmentInput{Name: "production"}}, "project")
	if err != nil {
		t.Fatal(err)
	}
	e, err := c.GetEnvironment(ctx, p.InitialEnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	compose := "services:\n  web:\n    image: registry.example.com/demo@sha256:" + strings.Repeat("a", 64) + "\n    volumes: [data:/data]\nvolumes:\n  data: {}\n"
	op, err := c.DeployEnvironment(ctx, e.ID, compose, "test", "install", e.Revision)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := c.store.ClaimDeploymentOperation(ctx, "fixture", c.now(), c.now().Add(-time.Hour))
	if err != nil || claimed.ID != op.ID {
		t.Fatal(claimed, err)
	}
	claimed.Status = model.OperationSucceeded
	if err = c.store.FinishDeploymentOperation(ctx, &claimed, c.now()); err != nil {
		t.Fatal(err)
	}
	app, err := c.store.GetApplication(ctx, op.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	e, err = c.GetEnvironment(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	fake := &projectRuntimeFixture{objects: []projectlife.Object{{ID: "container-one", Kind: "container", Service: "web", Active: true}}}
	c.ProjectRuntime = fake
	sqlStore := c.store.(*storage.SQLStore)
	worker := ProjectDeletionWorker{Core: c, Locker: lease.New(sqlStore.DB(), sqlStore.Dialect())}
	return c, p, e, app, fake, worker
}

func TestProjectDeletionRejectsRunningAndPreservesOwnershipAndHistory(t *testing.T) {
	c, p, e, app, fake, worker := projectDeletionFixture(t)
	ctx := context.Background()
	if err := c.DeleteEmptyEnvironment(ctx, e.ID, e.Revision); !errors.Is(err, storage.ErrConflict) {
		t.Fatal(err)
	}
	preview, err := c.PreviewProjectDeletion(ctx, p.ID, false)
	if err != nil || len(preview.Blockers) != 1 || preview.Blockers[0].Code != "project.deletion.running" {
		t.Fatal(preview.Blockers, err)
	}
	if _, err = c.DeleteProject(ctx, p.ID, ProjectDeletionInput{PreviewID: preview.ID, ConfirmSlug: p.Slug}, preview.ProjectRevision, "normal", "admin"); err == nil || fake.removed != 0 {
		t.Fatal("running workload removed", err)
	}
	preview, err = c.PreviewProjectDeletion(ctx, p.ID, true)
	if err != nil || len(preview.Blockers) != 0 {
		t.Fatal(preview.Blockers, err)
	}
	in := ProjectDeletionInput{PreviewID: preview.ID, Force: true, ConfirmSlug: p.Slug}
	task, err := c.DeleteProject(ctx, p.ID, in, preview.ProjectRevision, "force", "admin")
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.DeleteProject(ctx, p.ID, in, preview.ProjectRevision, "force", "admin")
	if err != nil || again.ID != task.ID {
		t.Fatal(again, err)
	}
	if _, err = c.QueueApplicationOperation(ctx, app.ID, "START", "start-during-delete"); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("start admitted", err)
	}
	if _, err = c.SaveVariables(ctx, p.ID, VariableInput{}, 0); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("variable write admitted", err)
	}
	if _, err = c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "late"}); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("environment admitted", err)
	}
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	done, err := c.GetProjectDeletion(ctx, task.ID)
	if err != nil || done.Status != "SUCCEEDED" || fake.removed != 1 || !fake.force {
		t.Fatal(done, err, fake)
	}
	if _, err = c.GetProject(ctx, p.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("project remains", err)
	}
	if _, err = c.store.GetApplication(ctx, app.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("application remains", err)
	}
	apps, _ := c.store.ListApplications(ctx, "", storage.Page{Limit: 200})
	services, _ := c.store.ListServices(ctx, storage.Page{Limit: 200})
	if len(apps) != 0 || len(services) != 0 {
		t.Fatal("deleted inventory remains", apps, services)
	}
	revisions, err := c.store.ListApplicationRevisions(ctx, app.ID, storage.Page{})
	if err != nil || len(revisions) != 1 || len(revisions[0].EncryptedCompose) == 0 {
		t.Fatal("history lost", revisions, err)
	}
	if _, err = c.CreateProject(ctx, "Reused", p.Slug); err != nil {
		t.Fatal("project slug not released", err)
	}
	again, err = c.DeleteProject(ctx, p.ID, in, preview.ProjectRevision, "force", "admin")
	if err != nil || again.ID != task.ID {
		t.Fatal("completed idempotency lost", err)
	}
}

func TestProjectDeletionUnknownResumeAndCompletePagination(t *testing.T) {
	c, p, _, app, fake, worker := projectDeletionFixture(t)
	ctx := context.Background()
	services, _ := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
	sample := services[0]
	for i := 0; i < 205; i++ {
		s := sample
		s.ID = fmt.Sprintf("svc-extra-%03d", i)
		s.Name = fmt.Sprintf("extra-%03d", i)
		s.ServiceKey = app.ProjectName + "/" + s.Name
		s.ExternalID = s.ServiceKey
		if err := c.store.UpsertService(ctx, &s); err != nil {
			t.Fatal(err)
		}
	}
	fake.objects[0].Active = false
	preview, err := c.PreviewProjectDeletion(ctx, p.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, r := range preview.Resources {
		if r.Kind == "service" {
			count++
		}
	}
	if count != 206 {
		t.Fatal("truncated services", count)
	}
	in := ProjectDeletionInput{PreviewID: preview.ID, ConfirmSlug: p.Slug}
	task, err := c.DeleteProject(ctx, p.ID, in, preview.ProjectRevision, "normal", "admin")
	if err != nil {
		t.Fatal(err)
	}
	fake.removeFailure = context.DeadlineExceeded
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	failed, err := c.GetProjectDeletion(ctx, task.ID)
	if err != nil || failed.Status != "UNKNOWN" || len(failed.Attempts) != 1 {
		t.Fatal(failed, err)
	}
	if _, err = c.GetProject(ctx, p.ID); err != nil {
		t.Fatal("project discarded on unknown result", err)
	}
	fake.removeFailure = nil
	preview, err = c.PreviewProjectDeletion(ctx, p.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	in.PreviewID = preview.ID
	task, err = c.ResumeProjectDeletion(ctx, task.ID, in, failed.Revision, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	done, err := c.GetProjectDeletion(ctx, task.ID)
	if err != nil || done.Status != "SUCCEEDED" || len(done.Attempts) != 2 || done.Attempts[0].Status != "UNKNOWN" {
		t.Fatal(done, err)
	}
}

func TestEmptyProjectDeletionAndChangedPreview(t *testing.T) {
	c, _, _ := controlFixture(t)
	ctx := context.Background()
	p, err := c.CreateProject(ctx, "Empty", "empty")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := c.PreviewProjectDeletion(ctx, p.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.DeleteProject(ctx, p.ID, ProjectDeletionInput{PreviewID: preview.ID, ConfirmSlug: "wrong"}, p.Revision, "bad", "admin"); err == nil {
		t.Fatal("wrong confirmation accepted")
	}
	p, err = c.UpdateProject(ctx, p.ID, ProjectSettings{Name: "Changed"}, p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.DeleteProject(ctx, p.ID, ProjectDeletionInput{PreviewID: preview.ID, ConfirmSlug: p.Slug}, p.Revision, "stale", "admin"); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal(err)
	}
	preview, err = c.PreviewProjectDeletion(ctx, p.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	task, err := c.DeleteProject(ctx, p.ID, ProjectDeletionInput{PreviewID: preview.ID, ConfirmSlug: p.Slug}, p.Revision, "delete", "admin")
	if err != nil {
		t.Fatal(err)
	}
	s := c.store.(*storage.SQLStore)
	w := ProjectDeletionWorker{Core: c, Locker: lease.New(s.DB(), s.Dialect())}
	if err = w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	task, err = c.GetProjectDeletion(ctx, task.ID)
	if err != nil || task.Status != "SUCCEEDED" {
		t.Fatal(task, err)
	}
}

func TestProjectDeletionBlocksSharedIngressAndLateDiscovery(t *testing.T) {
	c, p, _, app, fake, worker := projectDeletionFixture(t)
	ctx := context.Background()
	services, err := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
	if err != nil {
		t.Fatal(err)
	}
	service := services[0]
	service.DNSStatus = model.DNSReady
	service.DNSName = app.ProjectName + "_web"
	service.RoutingEligible = true
	if err = c.store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	rt, err := c.store.GetRuntime(ctx, app.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := c.CreateDomain(ctx, CreateDomainInput{PoolID: rt.PoolID, DomainConfigInput: DomainConfigInput{Host: "owned.example.test", EntryPoints: []string{"http"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateRoute(ctx, CreateRouteInput{DomainID: domain.ID, RouteConfigInput: RouteConfigInput{Path: "/", Backend: RouteBackendInput{ServiceID: service.ID, Port: 80}}})
	if err != nil {
		t.Fatal(err)
	}
	foreign := service
	foreign.ID = "foreign-service"
	foreign.ServiceKey = "outside/web"
	foreign.ExternalID = "outside/web"
	foreign.ApplicationID = ""
	foreign.Managed = false
	if err = c.store.UpsertService(ctx, &foreign); err != nil {
		t.Fatal(err)
	}
	route, err := c.CreateRoute(ctx, CreateRouteInput{DomainID: domain.ID, RouteConfigInput: RouteConfigInput{Path: "/foreign", Backend: RouteBackendInput{ServiceID: foreign.ID, Port: 80}}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := c.PreviewProjectDeletion(ctx, p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	shared := false
	for _, b := range preview.Blockers {
		shared = shared || b.Code == "project.deletion.shared_reference"
	}
	if !shared {
		t.Fatal("shared ingress not blocked", preview.Blockers)
	}
	if _, err = c.DeleteProject(ctx, p.ID, ProjectDeletionInput{PreviewID: preview.ID, Force: true, ConfirmSlug: p.Slug}, preview.ProjectRevision, "shared", "admin"); err == nil || fake.removed != 0 {
		t.Fatal(err)
	}
	// Remove the fixture ingress relationship explicitly, without mutating any
	// runtime. The subsequent test focuses on late discovery after finalization.
	sqlStore := c.store.(*storage.SQLStore)
	if _, err = sqlStore.DB().Exec(`DELETE FROM routes WHERE domain_id=?`, domain.ID); err != nil {
		t.Fatal(err)
	}
	_ = route
	preview, err = c.PreviewProjectDeletion(ctx, p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	// Routine observed-service metadata ticks do not invalidate reviewed ownership.
	service.Name = services[0].Name
	if err = c.store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	task, err := c.DeleteProject(ctx, p.ID, ProjectDeletionInput{PreviewID: preview.ID, Force: true, ConfirmSlug: p.Slug}, preview.ProjectRevision, "late-discovery", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	task, err = c.GetProjectDeletion(ctx, task.ID)
	if err != nil || task.Status != "SUCCEEDED" {
		t.Fatal(task.Status, task.ErrorCode, err)
	}
	observation, err := c.store.NextObservation(ctx, rt.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := model.RuntimeSnapshot{RuntimeID: rt.ID, Observation: observation, ObservedAt: c.now(), Services: []model.DiscoveredService{{ServiceKey: service.ServiceKey, Name: service.Name}}}
	if _, _, err = c.store.ApplyRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err = c.store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	if _, err = c.store.GetService(ctx, service.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("late discovery revived deleted resource", err)
	}
	if _, err = c.store.GetService(ctx, foreign.ID); err != nil {
		t.Fatal("foreign service was removed", err)
	}
}

func TestProjectDeletionIngressWaitsForFreshLoadingAndCanResume(t *testing.T) {
	c, p, _, app, _, worker := projectDeletionFixture(t)
	ctx := context.Background()
	services, _ := c.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
	services[0].DNSStatus = model.DNSReady
	services[0].DNSName = app.ProjectName + "_web"
	services[0].RoutingEligible = true
	if err := c.store.UpsertService(ctx, &services[0]); err != nil {
		t.Fatal(err)
	}
	rt, _ := c.store.GetRuntime(ctx, app.RuntimeID)
	domain, err := c.CreateDomain(ctx, CreateDomainInput{PoolID: rt.PoolID, DomainConfigInput: DomainConfigInput{Host: "delete.example.test", EntryPoints: []string{"http"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateRoute(ctx, CreateRouteInput{DomainID: domain.ID, RouteConfigInput: RouteConfigInput{Path: "/", Backend: RouteBackendInput{ServiceID: services[0].ID, Port: 80}}})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := c.store.GetPool(ctx, rt.PoolID)
	now := c.now()
	gateway := model.Gateway{ID: "deletion-gateway", PoolID: pool.ID, Name: "Gateway", Enabled: true, DesiredRevision: pool.DesiredRevision, LastSeenAt: &now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err = c.store.UpsertGateway(ctx, &gateway); err != nil {
		t.Fatal(err)
	}
	record := func(revision int64) error {
		return c.RecordGatewayEvidence(ctx, gateway.ID, GatewayEvidence{StartupState: "CONFIRMED", ConfigurationState: "CONFIRMED", LoadingConfirmed: true, ConfirmedRevision: revision, RenderedDigest: "same", DiskDigest: "same", ObservedAt: c.now(), RefreshIntervalMillis: 5000})
	}
	if err = record(pool.DesiredRevision); err != nil {
		t.Fatal(err)
	}
	certificate := model.Certificate{ID: "retained-certificate", PoolID: pool.ID, PrimaryDomain: domain.Host, Provider: "custom", Status: model.CertificateActive, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err = c.store.CreateCertificate(ctx, &certificate); err != nil {
		t.Fatal(err)
	}
	if err = c.store.LinkCertificateDomains(ctx, certificate.ID, []string{domain.ID}); err != nil {
		t.Fatal(err)
	}
	otherDomain, err := c.CreateDomain(ctx, CreateDomainInput{PoolID: rt.PoolID, DomainConfigInput: DomainConfigInput{Host: "retained.example.test", EntryPoints: []string{"http"}}})
	if err != nil {
		t.Fatal(err)
	}
	var draft NginxConfiguration
	for _, snippet := range []model.ConfigSnippet{{Scope: "http", Content: "gzip on;"}, {Scope: "server", ResourceID: domain.ID, Content: "gzip on;"}} {
		draft, err = c.SaveNginxDraft(ctx, pool.ID, NginxConfiguration{Snippets: []model.ConfigSnippet{snippet}}, "editor", draft.Revision)
		if err != nil {
			t.Fatal(err)
		}
		blocked, e := c.PreviewProjectDeletion(ctx, p.ID, true)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, b := range blocked.Blockers {
			found = found || b.Code == "project.deletion.shared_configuration"
		}
		if !found {
			t.Fatal("shared configuration/draft did not block deletion", blocked.Blockers)
		}
		if _, e = c.DeleteProject(ctx, p.ID, ProjectDeletionInput{PreviewID: blocked.ID, Force: true, ConfirmSlug: p.Slug}, blocked.ProjectRevision, "shared-config", "admin"); e == nil {
			t.Fatal("shared configuration deletion accepted")
		}
	}
	retainedSnippet := model.ConfigSnippet{Scope: "server", ResourceID: otherDomain.ID, Content: "gzip off;"}
	draft, err = c.SaveNginxDraft(ctx, pool.ID, NginxConfiguration{Snippets: []model.ConfigSnippet{retainedSnippet}, Description: "Retain this user draft"}, "editor", draft.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.saveControl(ctx, pool.ID+":nginx-active", "nginx-active", pool.ID, NginxConfiguration{Snippets: []model.ConfigSnippet{{Scope: "server", ResourceID: domain.ID, Content: "gzip on;"}, retainedSnippet}}, 0); err != nil {
		t.Fatal(err)
	}
	preview, err := c.PreviewProjectDeletion(ctx, p.ID, true)
	if err != nil || len(preview.Blockers) > 0 {
		t.Fatal(preview.Blockers, err)
	}
	task, err := c.DeleteProject(ctx, p.ID, ProjectDeletionInput{PreviewID: preview.ID, Force: true, ConfirmSlug: p.Slug}, preview.ProjectRevision, "ingress", "admin")
	if err != nil {
		t.Fatal(err)
	}
	newGateway := gateway
	newGateway.ID = "unreviewed-gateway"
	if err = c.store.UpsertGateway(ctx, &newGateway); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("pool membership changed during deletion", err)
	}
	if err = c.store.LinkCertificateDomains(ctx, certificate.ID, []string{domain.ID}); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("certificate binding admitted during deletion", err)
	}
	c.config.Management.DeployTimeout = config.Duration(3 * time.Second)
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	task, _ = c.GetProjectDeletion(ctx, task.ID)
	if task.Status != "UNKNOWN" || task.Phase != "INGRESS" || task.Preview.Applications[0].Status != "SUCCEEDED" {
		t.Fatal(task.Status, task.Phase)
	}
	if task.Preview.Pools[0].CleanupRevision == 0 {
		t.Fatal("fixture must reach gateway-loading verification before timing out")
	}
	if _, err = c.GetProject(ctx, p.ID); err != nil {
		t.Fatal("deleted before load confirmation", err)
	}
	pool, _ = c.store.GetPool(ctx, rt.PoolID)
	if err = record(pool.DesiredRevision); err != nil {
		t.Fatal(err)
	}
	preview, err = c.PreviewProjectDeletion(ctx, p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	task, err = c.ResumeProjectDeletion(ctx, task.ID, ProjectDeletionInput{PreviewID: preview.ID, Force: true, ConfirmSlug: p.Slug}, task.Revision, "resumer")
	if err != nil {
		t.Fatal(err)
	}
	c.config.Management.DeployTimeout = config.Duration(3 * time.Second)
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	task, _ = c.GetProjectDeletion(ctx, task.ID)
	if task.Status != "SUCCEEDED" || len(task.Attempts) != 2 {
		t.Fatal(task.Status, task.ErrorCode)
	}
	if _, err = c.store.GetDomain(ctx, domain.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
	retained, err := c.store.GetCertificate(ctx, certificate.ID)
	if err != nil || len(retained.Domains) != 0 {
		t.Fatal("certificate must remain without a live deleted-domain binding", retained.Domains, err)
	}
	if err = c.store.UpsertGateway(ctx, &newGateway); err != nil {
		t.Fatal("completed deletion did not release pool membership guard", err)
	}
	active, err := c.NginxConfiguration(ctx, pool.ID, "nginx-active")
	if err != nil || len(active.Snippets) != 1 || active.Snippets[0] != retainedSnippet {
		t.Fatal("foreign active configuration changed", active, err)
	}
	retainedDraft, err := c.NginxConfiguration(ctx, pool.ID, "nginx-draft")
	if err != nil || retainedDraft.Revision != draft.Revision || retainedDraft.Description != draft.Description || len(retainedDraft.Snippets) != 1 || retainedDraft.Snippets[0] != retainedSnippet {
		t.Fatal("user draft changed", retainedDraft, err)
	}
	if _, err = c.store.GetDomain(ctx, otherDomain.ID); err != nil {
		t.Fatal("foreign domain removed", err)
	}
}

func TestProjectDeletionRechecksOfflinePendingAndReplacedEvidence(t *testing.T) {
	c, p, _, app, fake, worker := projectDeletionFixture(t)
	ctx := context.Background()
	fake.failure = errors.New("runtime offline")
	preview, err := c.PreviewProjectDeletion(ctx, p.ID, true)
	if err != nil || len(preview.Blockers) != 1 || preview.Blockers[0].Code != "project.deletion.runtime_unavailable" {
		t.Fatal("offline was treated as stopped", preview.Blockers, err)
	}
	fake.failure = nil
	preview, err = c.PreviewProjectDeletion(ctx, p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := c.QueueApplicationOperation(ctx, app.ID, "START", "concurrent-start")
	if err != nil {
		t.Fatal(err)
	}
	input := ProjectDeletionInput{PreviewID: preview.ID, Force: true, ConfirmSlug: p.Slug}
	if _, err = c.DeleteProject(ctx, p.ID, input, preview.ProjectRevision, "deletion", "admin"); err == nil || fake.removed != 0 {
		t.Fatal("pending operation admitted deletion", err)
	}
	claimed, err := c.store.ClaimDeploymentOperation(ctx, "fixture", c.now(), c.now().Add(-time.Hour))
	if err != nil || claimed.ID != queued.ID {
		t.Fatal(claimed, err)
	}
	claimed.Status = model.OperationSucceeded
	if err = c.store.FinishDeploymentOperation(ctx, &claimed, c.now()); err != nil {
		t.Fatal(err)
	}
	preview, err = c.PreviewProjectDeletion(ctx, p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	input.PreviewID = preview.ID
	task, err := c.DeleteProject(ctx, p.ID, input, preview.ProjectRevision, "deletion", "admin")
	if err != nil {
		t.Fatal(err)
	}
	fake.objects[0].ID = "unexpected-replacement"
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	task, err = c.GetProjectDeletion(ctx, task.ID)
	if err != nil || task.Status != "BLOCKED" || task.ErrorCode != "project.deletion.scope_changed" || fake.removed != 0 {
		t.Fatal("unreviewed replacement removed", task.Status, task.ErrorCode, err)
	}
	if _, err = c.GetProject(ctx, p.ID); err != nil {
		t.Fatal("blocked project disappeared", err)
	}
}

func TestProjectDeletionVolumeRetentionBlocksPreviewAndExecution(t *testing.T) {
	c, p, _, _, fake, worker := projectDeletionFixture(t)
	ctx := context.Background()
	fake.failure = projectlife.ErrDataRetention
	for _, force := range []bool{false, true} {
		preview, err := c.PreviewProjectDeletion(ctx, p.ID, force)
		if err != nil || len(preview.Blockers) != 1 || preview.Blockers[0].Code != "project.deletion.data_retention" {
			t.Fatal("retention reason missing", preview.Blockers, err)
		}
		if _, err = c.DeleteProject(ctx, p.ID, ProjectDeletionInput{PreviewID: preview.ID, Force: force, ConfirmSlug: p.Slug}, preview.ProjectRevision, "unsafe", "admin"); err == nil || fake.removed != 0 {
			t.Fatal("unsafe data removal admitted", force, err)
		}
	}
	fake.failure = nil
	preview, err := c.PreviewProjectDeletion(ctx, p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	task, err := c.DeleteProject(ctx, p.ID, ProjectDeletionInput{PreviewID: preview.ID, Force: true, ConfirmSlug: p.Slug}, preview.ProjectRevision, "reviewed", "admin")
	if err != nil {
		t.Fatal(err)
	}
	fake.failure = projectlife.ErrDataRetention
	if err = worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	task, err = c.GetProjectDeletion(ctx, task.ID)
	if err != nil || task.Status != "BLOCKED" || task.ErrorCode != "project.deletion.data_retention" || fake.removed != 0 {
		t.Fatal("execution did not preserve unsafe data", task.Status, task.ErrorCode, err)
	}
	if _, err = c.GetProject(ctx, p.ID); err != nil {
		t.Fatal("blocked project disappeared", err)
	}
}

func TestProjectDeletionRejectsHiddenLegacyApplicationClaim(t *testing.T) {
	c, p, e, _, fake, _ := projectDeletionFixture(t)
	ctx := context.Background()
	e.ApplicationID = ""
	if err := c.saveControl(ctx, e.ID, "environment", p.ID, e, e.Revision); err != nil {
		t.Fatal(err)
	}
	preview, err := c.PreviewProjectDeletion(ctx, p.ID, true)
	if err != nil || len(preview.Blockers) != 1 || preview.Blockers[0].Code != "project.deletion.ownership" {
		t.Fatal("hidden application treated as an empty environment", preview.Blockers, err)
	}
	if _, err = c.DeleteProject(ctx, p.ID, ProjectDeletionInput{PreviewID: preview.ID, Force: true, ConfirmSlug: p.Slug}, preview.ProjectRevision, "hidden", "admin"); err == nil || fake.removed != 0 {
		t.Fatal("project with unresolved legacy ownership was deleted", err)
	}
}

func TestProjectDeletionFreezesLateProbeAndEndpointLifecycle(t *testing.T) {
	c, p, _, app, _, worker := projectDeletionFixture(t)
	ctx := context.Background()
	s := c.store.(*storage.SQLStore)
	services, err := s.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
	if err != nil || len(services) != 1 {
		t.Fatal(services, err)
	}
	service := services[0]
	service.DNSName, service.DNSStatus, service.RoutingEligible = "fixture-web", model.DNSReady, true
	service.DiscoveredPort = 80
	service.Readiness = model.ReadinessPolicy{Mode: model.ReadinessTCP, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1}
	if err = s.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	rt, err := s.GetRuntime(ctx, app.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.BuildProbePlan(ctx, rt.PoolID)
	if err != nil || len(plan.Targets) != 1 {
		t.Fatal(plan, err)
	}
	now := c.now()
	endpoint := model.Endpoint{ID: "retained-instance", ServiceID: service.ID, ExternalID: "container-one", RuntimeState: model.EndpointRunning, ReadyState: model.Ready, HealthState: model.HealthUnknown, FirstSeenAt: now, LastSeenAt: now, CreatedAt: now, UpdatedAt: now, Revision: 1}
	if err = s.UpsertEndpoint(ctx, &endpoint); err != nil {
		t.Fatal(err)
	}
	preview, err := c.PreviewProjectDeletion(ctx, p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	task, err := c.DeleteProject(ctx, p.ID, ProjectDeletionInput{PreviewID: preview.ID, Force: true, ConfirmSlug: p.Slug}, preview.ProjectRevision, "late-probe", "admin")
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"during deletion", "after deletion"} {
		if phase == "after deletion" {
			if err = worker.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			finished, e := c.GetProjectDeletion(ctx, task.ID)
			if e != nil || finished.Status != "SUCCEEDED" {
				t.Fatal(finished.Status, finished.ErrorCode, e)
			}
		}
		var revision int64
		var status string
		if err = s.DB().QueryRow(`SELECT revision,status FROM services WHERE id=?`, service.ID).Scan(&revision, &status); err != nil {
			t.Fatal(err)
		}
		changed, e := s.UpdateServiceProbe(ctx, model.ProbeResult{PoolID: rt.PoolID, ServiceID: service.ID, Epoch: plan.Epoch, Revision: plan.Revision, TargetToken: plan.Targets[0].TargetToken, Sequence: 10, Success: false, Error: "late timeout", ObservedAt: now.Add(time.Minute)})
		if e != nil || changed {
			t.Fatal(phase, changed, e)
		}
		observation, e := s.NextObservation(ctx, rt.ID)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.MarkRuntimeOffline(ctx, rt.ID, observation, "offline", now.Add(time.Hour)); e != nil {
			t.Fatal(e)
		}
		if _, _, e = s.SweepEndpointLifecycle(ctx, rt.ID, observation, "drain-after-timeout", now.Add(2*time.Hour), time.Second, time.Second, time.Second); e != nil {
			t.Fatal(e)
		}
		if e = s.UpsertEndpoint(ctx, &endpoint); !errors.Is(e, storage.ErrConflict) {
			t.Fatal("late existing endpoint accepted", e)
		}
		late := endpoint
		late.ID, late.ExternalID = "late-instance", "late-container"
		if e = s.UpsertEndpoint(ctx, &late); !errors.Is(e, storage.ErrConflict) {
			t.Fatal("late new endpoint accepted", e)
		}
		var actualRevision int64
		var actualStatus string
		if e = s.DB().QueryRow(`SELECT revision,status FROM services WHERE id=?`, service.ID).Scan(&actualRevision, &actualStatus); e != nil {
			t.Fatal(e)
		}
		if revision != actualRevision || status != actualStatus {
			t.Fatal(phase, "service changed", revision, actualRevision, status, actualStatus)
		}
		endpoints, e := s.ListEndpoints(ctx, service.ID)
		if e != nil || len(endpoints) != 1 || endpoints[0].Revision != endpoint.Revision || endpoints[0].StaleSince != nil {
			t.Fatal(phase, endpoints, e)
		}
	}
}

func TestProjectLegacyRuntimeInheritanceRequiresAgreement(t *testing.T) {
	c, rt, pool := controlFixture(t)
	ctx := context.Background()
	p, err := c.CreateProject(ctx, "Legacy", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "ambiguous"}); err == nil {
		t.Fatal("empty legacy project inferred a runtime")
	}
	first, err := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "prod", RuntimeID: rt.ID})
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "dev"})
	if err != nil || inherited.RuntimeID != rt.ID {
		t.Fatal(inherited, err)
	}
	second, err := c.CreateRuntime(ctx, CreateRuntimeInput{PoolID: pool.ID, Name: "another", RuntimeType: model.RuntimeDocker, Endpoint: "unix:///tmp/another.sock", ManagementEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "foreign-runtime", RuntimeID: second.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "mixed"}); err == nil {
		t.Fatal("mixed legacy runtimes inferred a default")
	}
	p, err = c.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err = c.UpdateProject(ctx, p.ID, ProjectSettings{Name: p.Name, DefaultRuntimeID: second.ID}, p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	chosen, err := c.CreateEnvironment(ctx, p.ID, EnvironmentInput{Name: "explicit-default"})
	if err != nil || chosen.RuntimeID != second.ID {
		t.Fatal(chosen, err)
	}
	unchanged, err := c.GetEnvironment(ctx, first.ID)
	if err != nil || unchanged.RuntimeID != rt.ID || unchanged.ProjectName != first.ProjectName {
		t.Fatal("default changed existing environment identity", unchanged, err)
	}
}

type concurrentDeletionStore struct {
	storage.Store
	ready   sync.WaitGroup
	release chan struct{}
}

func (s *concurrentDeletionStore) MutateProjectLifecycle(ctx context.Context, in storage.ProjectLifecycleMutation) error {
	s.ready.Done()
	select {
	case <-s.release:
		return s.Store.MutateProjectLifecycle(ctx, in)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestProjectDeletionConcurrentIdempotencyRequiresSameReviewedRequest(t *testing.T) {
	for _, different := range []bool{false, true} {
		t.Run(fmt.Sprintf("different=%t", different), func(t *testing.T) {
			c, _, _ := controlFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			p, err := c.CreateProject(ctx, "Concurrent", "concurrent")
			if err != nil {
				t.Fatal(err)
			}
			preview, err := c.PreviewProjectDeletion(ctx, p.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			previews := []ProjectDeletionPreview{preview, preview}
			if different {
				previews[1], err = c.PreviewProjectDeletion(ctx, p.ID, true)
				if err != nil {
					t.Fatal(err)
				}
			}
			store := &concurrentDeletionStore{Store: c.store, release: make(chan struct{})}
			store.ready.Add(2)
			c.store = store
			type result struct {
				index int
				task  ProjectDeletion
				err   error
			}
			results := make(chan result, 2)
			for i, reviewed := range previews {
				go func() {
					task, err := c.DeleteProject(ctx, p.ID, ProjectDeletionInput{PreviewID: reviewed.ID, Force: reviewed.Force, ConfirmSlug: p.Slug}, reviewed.ProjectRevision, "one-command", "admin")
					results <- result{i, task, err}
				}()
			}
			ready := make(chan struct{})
			go func() { store.ready.Wait(); close(ready) }()
			select {
			case <-ready:
				close(store.release)
			case <-ctx.Done():
				t.Fatal("concurrent requests did not reach acceptance")
			}
			accepted, conflicts := 0, 0
			for range previews {
				got := <-results
				if errors.Is(got.err, storage.ErrConflict) {
					conflicts++
					continue
				}
				if got.err != nil {
					t.Fatal(got.err)
				}
				accepted++
				if got.task.Preview.ID != previews[got.index].ID || got.task.Preview.Force != previews[got.index].Force {
					t.Fatal("accepted a different reviewed request", got)
				}
			}
			if (different && (accepted != 1 || conflicts != 1)) || (!different && (accepted != 2 || conflicts != 0)) {
				t.Fatal(accepted, conflicts)
			}
			tasks, err := store.ListControlDocuments(ctx, "project-deletion", "", storage.Page{Limit: 200})
			if err != nil || len(tasks) != 1 {
				t.Fatal("duplicate durable deletion task", tasks, err)
			}
		})
	}
}
