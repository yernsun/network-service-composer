package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/storage"
)

type openAPIKeyBoundaryStore struct {
	storage.Store
	readErr  error
	writeErr error
}

func (s openAPIKeyBoundaryStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if key == openAPIKeysControlKey && s.readErr != nil {
		return storage.ControlDocument{}, s.readErr
	}
	return s.Store.GetControlDocument(ctx, key)
}

func (s openAPIKeyBoundaryStore) WriteControlDocuments(ctx context.Context, writes []storage.ControlWrite) error {
	if len(writes) == 1 && writes[0].Document.ID == openAPIKeysControlKey && s.writeErr != nil {
		return s.writeErr
	}
	return s.Store.WriteControlDocuments(ctx, writes)
}

func TestOpenAPIKeyLifecycleValidatesScopeAndPreservesActiveKeysOnStoreFailure(t *testing.T) {
	c, runtime, _ := controlFixture(t)
	ctx := context.Background()
	project, err := c.CreateProject(ctx, "API keys", "api-keys")
	if err != nil {
		t.Fatal(err)
	}
	environment, err := c.CreateEnvironment(ctx, project.ID, EnvironmentInput{Name: "prod", RuntimeID: runtime.ID})
	if err != nil {
		t.Fatal(err)
	}
	if keys, err := c.ListOpenAPIKeys(ctx); err != nil || len(keys) != 0 {
		t.Fatal("absent key directory did not read empty", keys, err)
	}
	for _, name := range []string{"", strings.Repeat("x", 101), " leading", "trailing ", "line\nbreak"} {
		if _, _, err := c.IssueOpenAPIKey(ctx, name, environment.ID, "admin"); err == nil {
			t.Fatalf("invalid key name %q accepted", name)
		}
	}
	if _, _, err := c.IssueOpenAPIKey(ctx, "missing", "env_missing", "admin"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing environment accepted: %v", err)
	}
	key, token, err := c.IssueOpenAPIKey(ctx, "pipeline", environment.ID, "admin")
	if err != nil || !strings.HasPrefix(token, "nsc_api_") || key.EnvironmentID != environment.ID {
		t.Fatal("key was not scoped or issued", key, err)
	}
	if _, _, err = c.IssueOpenAPIKey(ctx, "PIPELINE", environment.ID, "admin"); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate active name accepted: %v", err)
	}
	for _, candidate := range []string{"", "nsc_wrong", "nsc_api_" + strings.Repeat("0", 64)} {
		if _, err = c.AuthenticateOpenAPIKey(ctx, candidate); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("unissued key accepted: %v", err)
		}
	}
	if got, err := c.AuthenticateOpenAPIKey(ctx, token); err != nil || got.ID != key.ID {
		t.Fatal("issued key rejected", got, err)
	}
	if err = c.RevokeOpenAPIKey(ctx, "apikey_missing", key.Revision); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("unknown key revocation succeeded: %v", err)
	}
	if err = c.RevokeOpenAPIKey(ctx, key.ID, key.Revision+1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale revocation succeeded: %v", err)
	}

	originalStore := c.store
	storeFailure := errors.New("isolated key storage failure")
	c.store = openAPIKeyBoundaryStore{Store: originalStore, readErr: storeFailure}
	if _, err = c.ListOpenAPIKeys(ctx); !errors.Is(err, storeFailure) {
		t.Fatalf("key listing hid storage failure: %v", err)
	}
	if _, err = c.AuthenticateOpenAPIKey(ctx, token); !errors.Is(err, storeFailure) {
		t.Fatalf("authentication hid storage failure: %v", err)
	}
	if _, _, err = c.IssueOpenAPIKey(ctx, "next", environment.ID, "admin"); !errors.Is(err, storeFailure) {
		t.Fatalf("issuance hid storage failure: %v", err)
	}
	if err = c.RevokeOpenAPIKey(ctx, key.ID, key.Revision); !errors.Is(err, storeFailure) {
		t.Fatalf("revocation hid storage failure: %v", err)
	}
	c.store = openAPIKeyBoundaryStore{Store: originalStore, writeErr: storeFailure}
	if _, _, err = c.IssueOpenAPIKey(ctx, "next", environment.ID, "admin"); !errors.Is(err, storeFailure) {
		t.Fatalf("failed key write was reported successful: %v", err)
	}
	if err = c.RevokeOpenAPIKey(ctx, key.ID, key.Revision); !errors.Is(err, storeFailure) {
		t.Fatalf("failed revocation write was reported successful: %v", err)
	}
	c.store = originalStore
	if got, err := c.AuthenticateOpenAPIKey(ctx, token); err != nil || got.ID != key.ID {
		t.Fatal("storage failures changed the active key", got, err)
	}
	if err = c.RevokeOpenAPIKey(ctx, key.ID, key.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = c.AuthenticateOpenAPIKey(ctx, token); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("revoked token stayed valid: %v", err)
	}
	if err = c.RevokeOpenAPIKey(ctx, key.ID, key.Revision+1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("revoked key accepted another write: %v", err)
	}
	keys, err := c.ListOpenAPIKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0].RevokedAt == nil || keys[0].Revision != 2 {
		t.Fatal("revocation metadata was not retained", keys, err)
	}
}

func TestOpenAPIKeyCannotTargetProtectedSystemEnvironment(t *testing.T) {
	c, registration, objects := systemFixture(t)
	project, err := c.EnsureGatewayProject(context.Background(), registration, registration.ClusterID, objects)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = c.IssueOpenAPIKey(context.Background(), "system", project.InitialEnvironmentID, "admin"); !errors.Is(err, storage.ErrSystemProtected) {
		t.Fatalf("system environment accepted an API key: %v", err)
	}
}
