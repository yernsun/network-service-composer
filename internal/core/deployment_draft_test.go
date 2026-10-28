package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestDeploymentDraftIsEncryptedOwnedAndVersioned(t *testing.T) {
	c, _, env := environmentBoundaryFixture(t)
	ctx := context.Background()
	first := json.RawMessage(`{"source":"file","raw":"PASSWORD=private-marker"}`)
	value, err := c.SaveDeploymentDraft(ctx, env.ID, "author", first, 0)
	if err != nil || value.Revision != 1 {
		t.Fatalf("save: %+v %v", value, err)
	}
	doc, err := c.store.GetControlDocument(ctx, deploymentDraftKey(env.ID, "author"))
	if err != nil || strings.Contains(string(doc.Payload), "private-marker") {
		t.Fatal("draft was not encrypted", err)
	}
	read, err := c.ReadDeploymentDraft(ctx, env.ID, "author")
	if err != nil || string(read.Data) != string(first) || read.Revision != 1 {
		t.Fatal("author cannot resume draft", err)
	}
	other, err := c.ReadDeploymentDraft(ctx, env.ID, "other")
	if err != nil || other.Revision != 0 || len(other.Data) != 0 {
		t.Fatal("cross-user draft leak", err)
	}
	if _, err = c.SaveDeploymentDraft(ctx, env.ID, "author", json.RawMessage(`{"source":"image"}`), 0); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("stale draft overwrite accepted", err)
	}
	if err = c.DeleteDeploymentDraft(ctx, env.ID, "author", 0); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("stale draft deletion accepted", err)
	}
	if err = c.DeleteDeploymentDraft(ctx, env.ID, "author", 1); err != nil {
		t.Fatal("draft deletion failed", err)
	}
	read, err = c.ReadDeploymentDraft(ctx, env.ID, "author")
	if err != nil || read.Revision != 0 || len(read.Data) != 0 {
		t.Fatal("deleted draft remained", err)
	}
}
