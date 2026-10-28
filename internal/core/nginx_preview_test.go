package core

import (
	"context"
	"errors"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
	"testing"
)

func TestCurrentNginxPreviewIsReadOnly(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	before, _ := c.store.GetPool(ctx, pool.ID)
	seen := []model.Snapshot{}
	render := func(s model.Snapshot) (nginxx.PreviewDocument, error) {
		seen = append(seen, s)
		return nginxx.PreviewDocument{}, nil
	}
	in := NginxPreviewInput{Snippets: []model.ConfigSnippet{{Scope: "http", Content: "gzip on;"}}}
	result, e := c.PreviewNginx(ctx, pool.ID, in, render)
	if e != nil {
		t.Fatal(e)
	}
	if len(seen) != 2 || len(seen[0].Snippets) != 0 || len(seen[1].Snippets) != 1 || seen[0].Revision != seen[1].Revision {
		t.Fatal(seen)
	}
	after, _ := c.store.GetPool(ctx, pool.ID)
	draft, _ := c.NginxConfiguration(ctx, pool.ID, "nginx-draft")
	if result.ConfigRevision != before.DesiredRevision || after.DesiredRevision != before.DesiredRevision || draft.Revision != 0 {
		t.Fatal(result, after, draft)
	}
	in.DomainID = "dom_unknown"
	if _, e = c.PreviewNginx(ctx, pool.ID, in, render); !errors.Is(e, storage.ErrNotFound) {
		t.Fatal(e)
	}
	in.DomainID = ""
	in.Snippets[0].Content = "gzip on;\ninclude /etc/passwd;"
	_, e = c.PreviewNginx(ctx, pool.ID, in, render)
	var validation ValidationError
	if !errors.As(e, &validation) || validation.Field != "snippets.0.content" || validation.Params["line"] != 2 {
		t.Fatal(e)
	}
	c.config.Management.Enabled = false
	in.Snippets[0].Content = "gzip on;"
	if _, e = c.PreviewNginx(ctx, pool.ID, in, render); e == nil {
		t.Fatal("disabled management allowed preview")
	}
}
