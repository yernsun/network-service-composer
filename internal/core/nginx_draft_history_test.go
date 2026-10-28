package core

import (
	"context"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type duplicateDraftHistoryStore struct{ storage.Store }

func (s duplicateDraftHistoryStore) WriteControlDocuments(ctx context.Context, writes []storage.ControlWrite) error {
	if len(writes) == 2 && writes[1].Document.Kind == "nginx-draft-version" {
		writes[1].Document.ID = writes[0].Document.ID
	}
	return s.Store.WriteControlDocuments(ctx, writes)
}

func TestNginxDraftHistoryIsAtomicAndPreservesEachSavedRevision(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	first, err := c.SaveNginxDraft(ctx, pool.ID, NginxConfiguration{Snippets: []model.ConfigSnippet{{Scope: "http", Content: "gzip on;"}}}, "first-editor", 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.SaveNginxDraft(ctx, pool.ID, NginxConfiguration{Snippets: []model.ConfigSnippet{{Scope: "http", Content: "gzip off;"}}}, "second-editor", first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := c.ListNginxDraftVersions(ctx, pool.ID, storage.Page{Limit: 10})
	if err != nil || len(versions) != 2 || versions[0].Actor != "first-editor" || versions[1].Actor != "second-editor" || versions[0].Snippets[0].Content != "gzip on;" || versions[1].Snippets[0].Content != "gzip off;" {
		t.Fatalf("draft history lost saved revisions: %+v %v", versions, err)
	}
	if _, err = c.SaveNginxDraft(ctx, pool.ID, NginxConfiguration{Snippets: []model.ConfigSnippet{{Scope: "http", Content: "gzip on;"}}}, "stale-editor", first.Revision); err == nil {
		t.Fatal("stale draft write succeeded")
	}
	base := c.store
	c.store = duplicateDraftHistoryStore{Store: base}
	if _, err = c.SaveNginxDraft(ctx, pool.ID, NginxConfiguration{Snippets: []model.ConfigSnippet{{Scope: "http", Content: "gzip on;"}}}, "failed-editor", second.Revision); err == nil {
		t.Fatal("history insertion failure was hidden")
	}
	c.store = base
	current, err := c.NginxConfiguration(ctx, pool.ID, "nginx-draft")
	if err != nil || current.Revision != second.Revision || current.Snippets[0].Content != "gzip off;" {
		t.Fatalf("failed history write changed draft: %+v %v", current, err)
	}
	versions, err = c.ListNginxDraftVersions(ctx, pool.ID, storage.Page{Limit: 10})
	if err != nil || len(versions) != 2 {
		t.Fatalf("failed write left history: %+v %v", versions, err)
	}
}
