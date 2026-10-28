package core

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/storage"
)

const openAPIKeysControlKey = "community:openapi-keys"

// OpenAPIKey is either a legacy environment deployment key or a user-owned
// project key. The secret is returned only once at issuance.
type OpenAPIKey struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	EnvironmentID string     `json:"environment_id"`
	ProjectID     string     `json:"project_id,omitempty"`
	OwnerUserID   string     `json:"owner_user_id,omitempty"`
	OwnerKeyEpoch int64      `json:"owner_key_epoch,omitempty"`
	Scopes        []string   `json:"scopes,omitempty"`
	CreatedBy     string     `json:"created_by"`
	Revision      int64      `json:"revision"`
	CreatedAt     time.Time  `json:"created_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
}

var userAPIKeyScopes = map[string]bool{
	"services.read": true, "services.create": true, "services.edit": true,
	"services.deploy": true, "services.operate": true,
	"project_variables.read": true, "project_variables.edit": true,
	"environment_variables.read": true, "environment_variables.edit": true,
	"domains.read": true, "domains.create": true, "domains.edit": true,
	"domains.delete": true, "domains.publish": true,
}

func (key OpenAPIKey) HasScope(scope string) bool { return slices.Contains(key.Scopes, scope) }

func (key OpenAPIKey) ProjectScoped() bool { return key.ProjectID != "" && key.OwnerUserID != "" }

type openAPIKeyCredential struct {
	OpenAPIKey
	TokenHash string `json:"token_hash"`
}

type openAPIKeyDirectory struct {
	Keys []openAPIKeyCredential `json:"keys"`
}

func (c *Core) openAPIKeys(ctx context.Context) (openAPIKeyDirectory, int64, error) {
	v, document, err := readControl[openAPIKeyDirectory](ctx, c.store, openAPIKeysControlKey, "openapi-keys")
	if errors.Is(err, storage.ErrNotFound) {
		return openAPIKeyDirectory{}, 0, nil
	}
	return v, document.Revision, err
}

func (c *Core) ListOpenAPIKeys(ctx context.Context) ([]OpenAPIKey, error) {
	directory, _, err := c.openAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]OpenAPIKey, 0, len(directory.Keys))
	for _, credential := range directory.Keys {
		keys = append(keys, credential.OpenAPIKey)
	}
	return keys, nil
}

func (c *Core) ListLegacyOpenAPIKeys(ctx context.Context) ([]OpenAPIKey, error) {
	keys, err := c.ListOpenAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	legacy := make([]OpenAPIKey, 0, len(keys))
	for _, key := range keys {
		if !key.ProjectScoped() {
			legacy = append(legacy, key)
		}
	}
	return legacy, nil
}

func (c *Core) ListUserAPIKeys(ctx context.Context, userID string) ([]OpenAPIKey, error) {
	if _, err := c.User(ctx, userID); err != nil {
		return nil, err
	}
	keys, err := c.ListOpenAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	result := []OpenAPIKey{}
	for _, key := range keys {
		if key.OwnerUserID == userID {
			result = append(result, key)
		}
	}
	return result, nil
}

func (c *Core) IssueUserAPIKey(ctx context.Context, name, projectID, ownerID, actorID string, scopes []string) (OpenAPIKey, string, error) {
	if name == "" || len(name) > 100 || strings.TrimSpace(name) != name || strings.ContainsAny(name, "\r\n\t") {
		return OpenAPIKey{}, "", controlInvalid("name", "provide a name of 1–100 characters")
	}
	project, err := c.GetProject(ctx, projectID)
	if err != nil {
		return OpenAPIKey{}, "", err
	}
	if project.System {
		return OpenAPIKey{}, "", storage.ErrSystemProtected
	}
	owner, err := c.User(ctx, ownerID)
	if err != nil {
		return OpenAPIKey{}, "", err
	}
	if !owner.Enabled || len(scopes) == 0 {
		return OpenAPIKey{}, "", controlInvalid("scopes", "enabled owner and at least one scope required")
	}
	unique := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		if !userAPIKeyScopes[scope] || unique[scope] {
			return OpenAPIKey{}, "", controlInvalid("scopes", "unknown or duplicate scope")
		}
		unique[scope] = true
	}
	scopes = slices.Clone(scopes)
	slices.Sort(scopes)
	directory, revision, err := c.openAPIKeys(ctx)
	if err != nil {
		return OpenAPIKey{}, "", err
	}
	for _, key := range directory.Keys {
		if key.OwnerUserID == ownerID && key.ProjectID == projectID && key.RevokedAt == nil && strings.EqualFold(key.Name, name) {
			return OpenAPIKey{}, "", storage.ErrConflict
		}
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return OpenAPIKey{}, "", err
	}
	token := "nsc_api_" + hex.EncodeToString(raw)
	key := OpenAPIKey{ID: id.New("apikey"), Name: name, ProjectID: projectID, OwnerUserID: ownerID, OwnerKeyEpoch: owner.KeyEpoch,
		Scopes: slices.Clone(scopes), CreatedBy: actorID, Revision: 1, CreatedAt: c.now()}
	directory.Keys = append(directory.Keys, openAPIKeyCredential{OpenAPIKey: key, TokenHash: identityDigest(token)})
	if err = c.saveControl(ctx, openAPIKeysControlKey, "openapi-keys", "", directory, revision); err != nil {
		return OpenAPIKey{}, "", err
	}
	return key, token, nil
}

func (c *Core) IssueOpenAPIKey(ctx context.Context, name, environmentID, actor string) (OpenAPIKey, string, error) {
	if name == "" || len(name) > 100 || strings.TrimSpace(name) != name || strings.ContainsAny(name, "\r\n\t") {
		return OpenAPIKey{}, "", controlInvalid("name", "provide a name of 1–100 characters")
	}
	environment, err := c.GetEnvironment(ctx, environmentID)
	if err != nil {
		return OpenAPIKey{}, "", err
	}
	if environment.System {
		return OpenAPIKey{}, "", storage.ErrSystemProtected
	}
	directory, revision, err := c.openAPIKeys(ctx)
	if err != nil {
		return OpenAPIKey{}, "", err
	}
	for _, key := range directory.Keys {
		if key.EnvironmentID == environmentID && key.RevokedAt == nil && strings.EqualFold(key.Name, name) {
			return OpenAPIKey{}, "", storage.ErrConflict
		}
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return OpenAPIKey{}, "", err
	}
	token := "nsc_api_" + hex.EncodeToString(raw)
	key := OpenAPIKey{ID: id.New("apikey"), Name: name, EnvironmentID: environmentID, CreatedBy: actor, Revision: 1, CreatedAt: c.now()}
	directory.Keys = append(directory.Keys, openAPIKeyCredential{OpenAPIKey: key, TokenHash: identityDigest(token)})
	if err = c.saveControl(ctx, openAPIKeysControlKey, "openapi-keys", "", directory, revision); err != nil {
		return OpenAPIKey{}, "", err
	}
	return key, token, nil
}

func (c *Core) AuthenticateOpenAPIKey(ctx context.Context, token string) (OpenAPIKey, error) {
	if len(token) != len("nsc_api_")+64 || !strings.HasPrefix(token, "nsc_api_") {
		return OpenAPIKey{}, storage.ErrNotFound
	}
	directory, _, err := c.openAPIKeys(ctx)
	if err != nil {
		return OpenAPIKey{}, err
	}
	digest := identityDigest(token)
	for _, credential := range directory.Keys {
		if credential.RevokedAt == nil && subtle.ConstantTimeCompare([]byte(credential.TokenHash), []byte(digest)) == 1 {
			if credential.ProjectScoped() {
				owner, ownerErr := c.User(ctx, credential.OwnerUserID)
				if ownerErr != nil || !owner.Enabled || owner.KeyEpoch != credential.OwnerKeyEpoch {
					return OpenAPIKey{}, storage.ErrNotFound
				}
			}
			return credential.OpenAPIKey, nil
		}
	}
	return OpenAPIKey{}, storage.ErrNotFound
}

func (c *Core) RevokeUserAPIKey(ctx context.Context, ownerID, keyID string, expected int64) error {
	directory, revision, err := c.openAPIKeys(ctx)
	if err != nil {
		return err
	}
	for index := range directory.Keys {
		key := &directory.Keys[index]
		if key.ID != keyID || key.OwnerUserID != ownerID {
			continue
		}
		if key.Revision != expected || key.RevokedAt != nil {
			return storage.ErrPrecondition
		}
		now := c.now()
		key.RevokedAt = &now
		key.Revision++
		return c.saveControl(ctx, openAPIKeysControlKey, "openapi-keys", "", directory, revision)
	}
	return storage.ErrNotFound
}

func (c *Core) RevokeOpenAPIKey(ctx context.Context, keyID string, expected int64) error {
	directory, revision, err := c.openAPIKeys(ctx)
	if err != nil {
		return err
	}
	for index := range directory.Keys {
		key := &directory.Keys[index]
		if key.ID != keyID || key.ProjectScoped() {
			continue
		}
		if key.Revision != expected || key.RevokedAt != nil {
			return storage.ErrPrecondition
		}
		now := c.now()
		key.RevokedAt = &now
		key.Revision++
		return c.saveControl(ctx, openAPIKeysControlKey, "openapi-keys", "", directory, revision)
	}
	return storage.ErrNotFound
}
