package core

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestApplicationDraftPreservesExecutionAndEncryptedRetry(t *testing.T) {
	ctx := context.Background()
	c, app, history := restoreBoundaryFixture(t)
	source, err := c.ApplicationSource(ctx, app.ID, history)
	if err != nil || !strings.Contains(source.ComposeYAML, strings.Repeat("a", 64)) {
		t.Fatal("history source lost", err)
	}
	if _, err := c.ApplicationSource(ctx, "missing", history); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("foreign history exposed", err)
	}
	draft, err := c.ApplicationDraft(ctx, app.ID)
	if err != nil || draft.Revision != 0 {
		t.Fatal("absent draft not empty", err)
	}
	input := ApplicationDraftInput{ApplicationSource: ApplicationSource{ComposeYAML: "incomplete: [", RuntimeEnv: "PASSWORD=confidential-draft-marker\n"}, ApplicationRevision: app.Revision}
	draft, err = c.SaveApplicationDraft(ctx, app.ID, input, 0, "save-one")
	if err != nil || draft.Revision != 1 {
		t.Fatal("save failed", err)
	}
	fresh, err := c.ApplicationDraft(ctx, app.ID)
	if err != nil || !reflect.DeepEqual(fresh, draft) {
		t.Fatal("draft did not survive reread", err)
	}
	current, err := c.store.GetApplication(ctx, app.ID)
	if err != nil || current.Revision != app.Revision || current.DesiredRevisionID != app.DesiredRevisionID || current.ActiveRevisionID != app.ActiveRevisionID {
		t.Fatal("saving changed execution", err)
	}
	replayed, err := c.SaveApplicationDraft(ctx, app.ID, input, 0, "save-one")
	if err != nil || !reflect.DeepEqual(replayed, draft) {
		t.Fatal("retry changed result", err)
	}
	changed := input
	changed.RuntimeEnv = "PASSWORD=other\n"
	if _, err := c.SaveApplicationDraft(ctx, app.ID, changed, 0, "save-one"); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("idempotency key reused for changed input", err)
	}
	if _, err := c.SaveApplicationDraft(ctx, app.ID, changed, 0, "save-two"); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("stale draft overwritten", err)
	}
	changed.ApplicationRevision++
	if _, err := c.SaveApplicationDraft(ctx, app.ID, changed, 1, "save-three"); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("changed application not checked", err)
	}
	for _, kind := range []string{"application-source-draft", "application-draft-receipt"} {
		docs, err := c.store.ListControlDocuments(ctx, kind, app.ID, storage.Page{Limit: 200})
		if err != nil || len(docs) != 1 {
			t.Fatal("unexpected persisted drafts", kind, err)
		}
		for _, doc := range docs {
			if strings.Contains(string(doc.Payload), "confidential-draft-marker") || strings.Contains(string(doc.Payload), "incomplete") {
				t.Fatal("source persisted unencrypted")
			}
		}
	}
	if _, err := c.ReencryptControlSecrets(ctx); err != nil {
		t.Fatal(err)
	}
	fresh, err = c.ApplicationDraft(ctx, app.ID)
	if err != nil || fresh.RuntimeEnv != input.RuntimeEnv {
		t.Fatal("rotation lost source", err)
	}
}

func TestServiceDraftsAreIndependentAndCannotOverwriteNewerInput(t *testing.T) {
	ctx := context.Background()
	c, app, _ := restoreBoundaryFixture(t)
	services := []model.Service{
		{ID: "svc-draft-web", ExternalID: "draft-web", RuntimeID: app.RuntimeID, ApplicationID: app.ID, ServiceKey: app.ProjectName + "/web", Name: "web", Managed: true},
		{ID: "svc-draft-worker", ExternalID: "draft-worker", RuntimeID: app.RuntimeID, ApplicationID: app.ID, ServiceKey: app.ProjectName + "/worker", Name: "worker", Managed: true},
	}
	for i := range services {
		if err := c.store.UpsertService(ctx, &services[i]); err != nil {
			t.Fatal(err)
		}
	}
	source, err := c.ServiceSource(ctx, services[0].ID)
	if err != nil || source.RevisionID != app.ActiveRevisionID || len(source.Services) != 1 || source.Services[0] != "web" {
		t.Fatal("wrong source scope", source.Services, err)
	}
	input := ApplicationDraftInput{ApplicationSource: ApplicationSource{ComposeYAML: "services: {web: {image: nginx:alpine}}", RevisionID: app.ActiveRevisionID}, ApplicationRevision: app.Revision}
	first, err := c.SaveServiceDraft(ctx, services[0].ID, input, 0, "shared-key")
	if err != nil {
		t.Fatal(err)
	}
	input.ComposeYAML = "services: {worker: {image: nginx:alpine}}"
	second, err := c.SaveServiceDraft(ctx, services[1].ID, input, 0, "shared-key")
	if err != nil {
		t.Fatal(err)
	}
	for i, expected := range []ApplicationDraft{first, second} {
		actual, err := c.ServiceDraft(ctx, services[i].ID)
		if err != nil || !reflect.DeepEqual(actual, expected) {
			t.Fatal("independent draft was overwritten", err)
		}
	}
	if _, err = c.SaveServiceDraft(ctx, services[0].ID, input, 0, "stale"); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("stale ETag accepted", err)
	}
	input.RevisionID = "older-source"
	if _, err = c.SaveServiceDraft(ctx, services[1].ID, input, 1, "older-source"); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("stale source accepted", err)
	}
	aggregate, err := c.ApplicationDraft(ctx, app.ID)
	if err != nil || aggregate.Revision != 0 {
		t.Fatal("service draft changed aggregate draft", err)
	}
	if _, err = c.ApplicationDraft(ctx, "foreign", services[0].ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("foreign service accepted", err)
	}
	current, err := c.store.GetApplication(ctx, app.ID)
	if err != nil || current.ActiveRevisionID != app.ActiveRevisionID || current.Revision != app.Revision {
		t.Fatal("draft changed active version", err)
	}
}
