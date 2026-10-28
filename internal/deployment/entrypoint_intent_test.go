package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"io"
	"log/slog"
	"testing"
)

func TestDomainAttachmentRejectsSupersededServiceBeforeDockerCommands(t *testing.T) {
	fixture := newWorkerFixture(t)
	ctx := t.Context()
	app, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: example.test/web:fixed\n    expose: [8080]\n")
	runner := &recordingRunner{}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err := worker.claimAndRun(ctx); err != nil {
		t.Fatal(err)
	}
	services, err := fixture.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
	if err != nil || len(services) != 1 {
		t.Fatal(services, err)
	}
	binding, child, err := fixture.core.CreateDomainBinding(ctx, services[0].ID, core.CreateDomainBindingInput{Domain: &core.DomainConfigInput{Host: "frozen.example.test", EntryPoints: []string{"http"}}, Route: core.RouteConfigInput{Path: "/", Backend: core.RouteBackendInput{Port: 8080, Protocol: "http"}}, IdempotencyKey: "frozen-attach"})
	if err != nil {
		t.Fatal(err)
	}
	app, err = fixture.store.GetApplication(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision, _, _, err := fixture.core.CreateApplicationRevision(ctx, app.ID, core.CreateRevisionInput{ApplicationPreflightInput: core.ApplicationPreflightInput{ComposeYAML: "services:\n  web:\n    image: example.test/web:updated\n    expose: [8080]\n"}, IdempotencyKey: "new-service"}, app.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = fixture.store.ActivateApplicationRevision(ctx, app.RuntimeID, app.ID, revision.ID, revision.ID, []string{services[0].ServiceKey}, nil, "", revision.CreatedAt); err != nil {
		t.Fatal(err)
	}
	before, err := fixture.store.GetRoute(ctx, binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	runner.calls = nil
	if err = worker.execute(ctx, fixture.runtime, child); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("stale service attachment executed", err)
	}
	if len(runner.calls) != 0 {
		t.Fatal("stale ingress ran Docker", runner.calls)
	}
	swarmRuntime := fixture.runtime
	swarmRuntime.Type = model.RuntimeSwarm
	if err = worker.execute(ctx, swarmRuntime, child); !errors.Is(err, storage.ErrPrecondition) || len(runner.calls) != 0 {
		t.Fatal("stale Swarm service attachment reached Docker", err, runner.calls)
	}
	route, err := fixture.store.GetRoute(ctx, binding.ID)
	if err != nil || route.Status != before.Status || route.Revision != before.Revision || route.TargetPort != before.TargetPort {
		t.Fatal("stale ingress changed route", route, err)
	}
}

type attachmentRevisionStore struct {
	storage.Store
	revisions map[string]model.ApplicationRevision
}

func (s attachmentRevisionStore) GetApplicationRevision(ctx context.Context, key string) (model.ApplicationRevision, error) {
	if revision, ok := s.revisions[key]; ok {
		return revision, nil
	}
	return s.Store.GetApplicationRevision(ctx, key)
}

func TestDomainAttachmentLegacyHeadsRequireSameApplicationAndEntry(t *testing.T) {
	fixture := newWorkerFixture(t)
	ctx := t.Context()
	app, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: example.test/web:fixed\n    expose: [8080]\n")
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), &recordingRunner{})
	if err := worker.claimAndRun(ctx); err != nil {
		t.Fatal(err)
	}
	app, err := fixture.store.GetApplication(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := fixture.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	services, err := fixture.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
	if err != nil || len(services) != 1 {
		t.Fatal(services, err)
	}
	entry := model.ServiceEntrypoint{Service: "web", Host: "legacy.example.test", Port: 8080, GatewayID: "fixture"}
	for _, scenario := range []string{"current head", "previous head", "missing head", "foreign head", "no entry", "different entry"} {
		t.Run(scenario, func(t *testing.T) {
			revision, previous := current, current
			revision.ConfigurationSource.Entrypoint = &entry
			revision.ConfigurationSource.ServiceRevisions = nil
			previous.ID, previous.ConfigurationSource.Entrypoint = "previous-head", &entry
			input := operationInput{ServiceRevisionID: revision.ID, EntrypointIntentID: "legacy-intent", Entrypoint: &entry}
			revisions := map[string]model.ApplicationRevision{revision.ID: revision}
			if scenario != "current head" {
				revision.ConfigurationSource.ServiceRevisions = map[string]string{"web": previous.ID}
				revisions[revision.ID] = revision
				input.ServiceRevisionID = previous.ID
				if scenario == "foreign head" {
					previous.ApplicationID = "foreign"
				}
				if scenario == "no entry" {
					previous.ConfigurationSource.Entrypoint = nil
				}
				if scenario == "different entry" {
					changed := entry
					changed.Port++
					previous.ConfigurationSource.Entrypoint = &changed
				}
				if scenario != "missing head" {
					revisions[previous.ID] = previous
				}
			}
			worker.store = attachmentRevisionStore{Store: fixture.store, revisions: revisions}
			err := worker.checkDomainAttachIntent(ctx, fixture.runtime, app, services[0], input)
			valid := scenario == "current head" || scenario == "previous head"
			if (err == nil) != valid {
				t.Fatal("legacy attachment ownership not enforced", scenario, err)
			}
		})
	}
}

func TestDomainAttachmentValidatesIntentIdentityWithoutLoadingDesiredCandidate(t *testing.T) {
	fixture := newWorkerFixture(t)
	ctx := t.Context()
	app, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: example.test/web:fixed\n    expose: [8080]\n")
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), &recordingRunner{})
	if err := worker.claimAndRun(ctx); err != nil {
		t.Fatal(err)
	}
	app, err := fixture.store.GetApplication(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := fixture.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	entry := model.ServiceEntrypoint{Service: "web", Host: "identity.example.test", Port: 8080, GatewayID: "fixture"}
	revision.ConfigurationSource.EntrypointIntents = map[string]model.ServiceEntrypointIntent{"web": {ID: "current-intent", Entry: entry}}
	source, err := json.Marshal(revision.ConfigurationSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.store.DB().ExecContext(ctx, "UPDATE application_revisions SET configuration_source=? WHERE id=?", string(source), revision.ID); err != nil {
		t.Fatal(err)
	}
	services, err := fixture.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
	if err != nil || len(services) != 1 {
		t.Fatal(services, err)
	}
	input := operationInput{ServiceRevisionID: revision.ID, EntrypointIntentID: "current-intent", Entrypoint: &entry}
	if err = worker.checkDomainAttachIntent(ctx, fixture.runtime, app, services[0], input); err != nil {
		t.Fatal(err)
	}
	input.EntrypointIntentID = "old-intent"
	if err = worker.checkDomainAttachIntent(ctx, fixture.runtime, app, services[0], input); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("old ingress intent accepted", err)
	}
	input.EntrypointIntentID, input.Entrypoint = "current-intent", nil
	if err = worker.checkDomainAttachIntent(ctx, fixture.runtime, app, services[0], input); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("missing frozen ingress accepted", err)
	}
}
