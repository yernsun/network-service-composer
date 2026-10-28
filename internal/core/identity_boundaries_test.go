package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/storage"
)

func identityBoundaryFixture(t *testing.T) (*Core, User) {
	t.Helper()
	c, _, _ := controlFixture(t)
	c.config.Security.AdminUsername = "owner"
	c.config.Security.AdminToken = "owner-password-123456"
	if err := c.BootstrapIdentity(context.Background()); err != nil {
		t.Fatal(err)
	}
	u, err := c.Authenticate(context.Background(), "owner", c.config.Security.AdminToken, "")
	if err != nil {
		t.Fatal(err)
	}
	return c, u
}

func TestUserValidationDoesNotChangeIdentityDirectory(t *testing.T) {
	c, owner := identityBoundaryFixture(t)
	ctx := context.Background()
	before, err := c.store.GetControlDocument(ctx, identityKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, key string
		expected  int64
		change    func(*UserInput)
	}{
		{"empty name", "", 0, func(in *UserInput) { in.Username = "" }},
		{"whitespace name", "", 0, func(in *UserInput) { in.Username = " reader" }},
		{"long name", "", 0, func(in *UserInput) { in.Username = strings.Repeat("a", 101) }},
		{"control name", "", 0, func(in *UserInput) { in.Username = "read\ter" }},
		{"unsupported role", "", 0, func(in *UserInput) { in.Role = "operator" }},
		{"missing password", "", 0, func(in *UserInput) { in.Password = "" }},
		{"short password", "", 0, func(in *UserInput) { in.Password = strings.Repeat("a", 15) }},
		{"long password", "", 0, func(in *UserInput) { in.Password = strings.Repeat("a", 73) }},
		{"unknown identity", "missing", 1, func(in *UserInput) { in.Password = "" }},
		{"stale identity", owner.ID, 0, func(in *UserInput) { in.Username = owner.Username; in.Role = owner.Role; in.Password = "" }},
		{"disable last admin", owner.ID, owner.Revision, func(in *UserInput) {
			in.Username = owner.Username
			in.Role = owner.Role
			in.Password = ""
			in.Enabled = false
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			in := UserInput{Username: "reader", Role: "viewer", Enabled: true, Password: "reader-password-123456"}
			test.change(&in)
			if _, err := c.SaveUser(ctx, test.key, in, test.expected); err == nil {
				t.Fatal("invalid identity accepted")
			}
			after, err := c.store.GetControlDocument(ctx, identityKey)
			if err != nil || after.Revision != before.Revision || string(after.Payload) != string(before.Payload) {
				t.Fatalf("rejected edit changed directory: %v", err)
			}
		})
	}
}

func TestUserTokenRotationPasswordResetAndAdministratorHandoff(t *testing.T) {
	c, owner := identityBoundaryFixture(t)
	ctx := context.Background()
	second, err := c.SaveUser(ctx, "", UserInput{Username: "second", Role: "administrator", Enabled: true, Password: strings.Repeat("s", 72)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Authenticate(ctx, "second", strings.Repeat("s", 72), ""); err != nil {
		t.Fatal("maximum password rejected")
	}
	owner, err = c.SaveUser(ctx, owner.ID, UserInput{Username: owner.Username, Role: "viewer", Enabled: true}, owner.Revision)
	if err != nil || owner.Role != "viewer" {
		t.Fatalf("admin handoff failed: %+v %v", owner, err)
	}
	firstToken, err := c.RotateUserToken(ctx, second.ID, second.Revision)
	if err != nil {
		t.Fatal(err)
	}
	second, err = c.User(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondToken, err := c.RotateUserToken(ctx, second.ID, second.Revision)
	if err != nil || firstToken == secondToken || len(secondToken) != 68 || !strings.HasPrefix(secondToken, "nsc_") {
		t.Fatalf("rotation did not create fresh bounded token: %v", err)
	}
	if _, err = c.Authenticate(ctx, "", "", firstToken); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("revoked token accepted: %v", err)
	}
	if _, err = c.Authenticate(ctx, "wrong", "wrong", secondToken); err != nil {
		t.Fatal("valid bearer token not accepted")
	}
	if _, err = c.Authenticate(ctx, "second", strings.Repeat("s", 72), "wrong token"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("invalid bearer fell back to password")
	}
	second, err = c.User(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err = c.SaveUser(ctx, second.ID, UserInput{Username: second.Username, Role: second.Role, Enabled: true, Password: strings.Repeat("r", 16)}, second.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for _, credentials := range [][3]string{{"", "", secondToken}, {"second", strings.Repeat("s", 72), ""}, {"unknown", strings.Repeat("r", 16), ""}} {
		if _, err = c.Authenticate(ctx, credentials[0], credentials[1], credentials[2]); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("old credentials accepted: %v", err)
		}
	}
	if _, err = c.Authenticate(ctx, "second", strings.Repeat("r", 16), ""); err != nil {
		t.Fatal("minimum password rejected")
	}
	if _, err = c.RotateUserToken(ctx, second.ID, second.Revision-1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("stale token rotation accepted")
	}
	if _, err = c.User(ctx, "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("unknown user resolved")
	}
	if _, err = c.RotateUserToken(ctx, "missing", 1); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("unknown user got token")
	}
	owner, err = c.SaveUser(ctx, owner.ID, UserInput{Username: owner.Username, Role: owner.Role, Enabled: false}, owner.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.RotateUserToken(ctx, owner.ID, owner.Revision); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("disabled user got token")
	}
}

type identityBootstrapRaceStore struct{ storage.Store }

func (s identityBootstrapRaceStore) WriteControlDocuments(ctx context.Context, w []storage.ControlWrite) error {
	if err := s.Store.WriteControlDocuments(ctx, w); err != nil {
		return err
	}
	return storage.ErrConflict // Another initializer committed this identity first.
}

func TestIdentityBootstrapFailuresAndConcurrentWinner(t *testing.T) {
	failure := errors.New("identity storage unavailable")
	for _, kind := range []string{"no token", "oversized token", "read failure", "write failure", "concurrent winner", "conflict without directory"} {
		t.Run(kind, func(t *testing.T) {
			c, _, _ := controlFixture(t)
			ctx := context.Background()
			c.config.Security.AdminUsername = "owner"
			c.config.Security.AdminToken = "bootstrap-password-123456"
			original := c.store
			switch kind {
			case "no token":
				c.config.Security.AdminToken = ""
			case "oversized token":
				c.config.Security.AdminToken = strings.Repeat("x", 73)
			case "read failure":
				c.store = &nginxBoundaryStore{Store: original, method: "document", failure: failure}
			case "write failure":
				c.store = &nginxBoundaryStore{Store: original, method: "write", failure: failure}
			case "concurrent winner":
				c.store = identityBootstrapRaceStore{Store: original}
			case "conflict without directory":
				c.store = &nginxBoundaryStore{Store: original, method: "write", failure: storage.ErrConflict}
			}
			err := c.BootstrapIdentity(ctx)
			if kind == "no token" {
				if err != nil {
					t.Fatal(err)
				}
			} else if kind == "concurrent winner" {
				if err != nil {
					t.Fatalf("concurrent bootstrap failed: %v", err)
				}
				users, e := c.Users(ctx)
				if e != nil || len(users) != 1 {
					t.Fatalf("duplicate bootstrap identity: %+v %v", users, e)
				}
				return
			} else if err == nil {
				t.Fatal("failed bootstrap reported success")
			}
			if _, err = original.GetControlDocument(ctx, identityKey); !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("failed bootstrap persisted credentials: %v", err)
			}
		})
	}
}

func TestIdentityStorageFailureNeverReturnsUsableNewToken(t *testing.T) {
	failure := errors.New("identity directory unavailable")
	ctx := context.Background()
	for _, kind := range []string{"authenticate", "users", "user", "save read", "save write", "rotate read", "rotate write"} {
		t.Run(kind, func(t *testing.T) {
			c, owner := identityBoundaryFixture(t)
			method := "document"
			if strings.HasSuffix(kind, "write") {
				method = "write"
			}
			original := c.store
			c.store = &nginxBoundaryStore{Store: original, method: method, failure: failure}
			var err error
			switch kind {
			case "authenticate":
				_, err = c.Authenticate(ctx, owner.Username, "password", "")
			case "users":
				_, err = c.Users(ctx)
			case "user":
				_, err = c.User(ctx, owner.ID)
			case "save read", "save write":
				_, err = c.SaveUser(ctx, owner.ID, UserInput{Username: owner.Username, Role: owner.Role, Enabled: true}, owner.Revision)
			case "rotate read", "rotate write":
				var token string
				token, err = c.RotateUserToken(ctx, owner.ID, owner.Revision)
				if token != "" {
					t.Fatal("uncommitted credential exposed")
				}
			}
			if !errors.Is(err, failure) {
				t.Fatalf("identity outage hidden: %v", err)
			}
			c.store = original
			if _, err = c.Authenticate(ctx, "", "", c.config.Security.AdminToken); err != nil {
				t.Fatal("failed write revoked prior credential")
			}
		})
	}
}

func TestRoleChangeAtomicallyRevokesProjectAndLegacyKeys(t *testing.T) {
	c, _ := identityBoundaryFixture(t)
	ctx := context.Background()
	project, err := c.CreateProject(ctx, "Identity keys", "identity-keys")
	if err != nil {
		t.Fatal(err)
	}
	runtimes, err := c.store.ListRuntimes(ctx, storage.Page{Limit: 1})
	if err != nil || len(runtimes) != 1 {
		t.Fatalf("runtime fixture missing: %v", err)
	}
	env, err := c.CreateEnvironment(ctx, project.ID, EnvironmentInput{Name: "prod", RuntimeID: runtimes[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := c.SaveUser(ctx, "", UserInput{Username: "key-owner", Role: "viewer", Enabled: true, Password: "viewer-password-123456"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	projectKey, projectToken, err := c.IssueUserAPIKey(ctx, "project-key", project.ID, viewer.ID, "user_bootstrap", []string{"services.read"})
	if err != nil {
		t.Fatal(err)
	}
	if projectKey.OwnerUserID != viewer.ID {
		t.Fatal("wrong key owner")
	}
	_, legacyToken, err := c.IssueOpenAPIKey(ctx, "legacy-key", env.ID, viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	original := c.store
	failure := errors.New("batch unavailable")
	c.store = &nginxBoundaryStore{Store: original, method: "write", failure: failure}
	if _, err = c.SaveUser(ctx, viewer.ID, UserInput{Username: viewer.Username, Role: "administrator", Enabled: true}, viewer.Revision); !errors.Is(err, failure) {
		t.Fatalf("role change failure hidden: %v", err)
	}
	c.store = original
	unchanged, err := c.User(ctx, viewer.ID)
	if err != nil || unchanged.Role != "viewer" || unchanged.Revision != viewer.Revision {
		t.Fatalf("failed role change persisted user: %+v %v", unchanged, err)
	}
	if _, err = c.AuthenticateOpenAPIKey(ctx, projectToken); err != nil {
		t.Fatalf("failed role change revoked key: %v", err)
	}
	if _, err = c.AuthenticateOpenAPIKey(ctx, legacyToken); err != nil {
		t.Fatalf("failed role change revoked legacy key: %v", err)
	}
	changed, err := c.SaveUser(ctx, viewer.ID, UserInput{Username: viewer.Username, Role: "administrator", Enabled: true}, viewer.Revision)
	if err != nil || changed.KeyEpoch == viewer.KeyEpoch {
		t.Fatalf("role change did not advance epoch: %+v %v", changed, err)
	}
	if _, err = c.AuthenticateOpenAPIKey(ctx, projectToken); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("old key survived role change: %v", err)
	}
	if _, err = c.AuthenticateOpenAPIKey(ctx, legacyToken); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("legacy key survived role change: %v", err)
	}
	keys, err := c.ListUserAPIKeys(ctx, viewer.ID)
	if err != nil || len(keys) != 1 || keys[0].RevokedAt == nil {
		t.Fatalf("revocation did not persist: %+v %v", keys, err)
	}
	// A project key belongs to its target user. Changing the issuing
	// administrator's role must not revoke another user's grant.
	other, err := c.SaveUser(ctx, "", UserInput{Username: "other-key-owner", Role: "viewer", Enabled: true, Password: "other-password-123456"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, otherToken, err := c.IssueUserAPIKey(ctx, "other-key", project.ID, other.ID, "user_bootstrap", []string{"services.read"})
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := c.User(ctx, "user_bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.SaveUser(ctx, bootstrap.ID, UserInput{Username: bootstrap.Username, Role: "viewer", Enabled: true}, bootstrap.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = c.AuthenticateOpenAPIKey(ctx, otherToken); err != nil {
		t.Fatalf("issuer role change revoked another user's key: %v", err)
	}
}

func TestProjectAPIKeyInvalidGrantsAndStaleRevokePreserveActiveKey(t *testing.T) {
	c, owner := identityBoundaryFixture(t)
	ctx := context.Background()
	project, err := c.CreateProject(ctx, "Key validation", "key-validation")
	if err != nil {
		t.Fatal(err)
	}
	for _, scopes := range [][]string{nil, {"unknown.read"}, {"services.read", "services.read"}} {
		if _, _, err = c.IssueUserAPIKey(ctx, "invalid", project.ID, owner.ID, owner.ID, scopes); err == nil {
			t.Fatalf("invalid scopes accepted: %v", scopes)
		}
	}
	keys, err := c.ListUserAPIKeys(ctx, owner.ID)
	if err != nil || len(keys) != 0 {
		t.Fatalf("invalid grant persisted: %+v %v", keys, err)
	}
	key, token, err := c.IssueUserAPIKey(ctx, "release", project.ID, owner.ID, owner.ID, []string{"services.read"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = c.IssueUserAPIKey(ctx, "RELEASE", project.ID, owner.ID, owner.ID, []string{"services.deploy"}); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate active key name accepted: %v", err)
	}
	if err = c.RevokeUserAPIKey(ctx, owner.ID, key.ID, key.Revision+1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale ETag revoked key: %v", err)
	}
	if _, err = c.AuthenticateOpenAPIKey(ctx, token); err != nil {
		t.Fatalf("stale revoke disabled key: %v", err)
	}
	if legacy, err := c.ListLegacyOpenAPIKeys(ctx); err != nil || len(legacy) != 0 {
		t.Fatalf("project key entered legacy inventory: %+v %v", legacy, err)
	}
	if err = c.RevokeOpenAPIKey(ctx, key.ID, key.Revision); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("legacy revoke reached project key: %v", err)
	}
	if err = c.RevokeUserAPIKey(ctx, owner.ID, key.ID, key.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = c.AuthenticateOpenAPIKey(ctx, token); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("revoked key still authenticated: %v", err)
	}
}
