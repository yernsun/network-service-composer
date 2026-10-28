package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

const identityKey = "community:identities"

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Enabled  bool   `json:"enabled"`
	KeyEpoch int64  `json:"key_epoch,omitempty"`
	Revision int64  `json:"revision"`
}

type identityCredential struct {
	User         User   `json:"user"`
	PasswordHash string `json:"password_hash"`
	TokenHash    string `json:"token_hash"`
}
type identityDirectory struct {
	Users []identityCredential `json:"users"`
}

// BootstrapIdentity imports the configured installation identity exactly once.
// Restarting never restores revoked passwords, tokens, or disabled accounts.
func (c *Core) BootstrapIdentity(ctx context.Context) error {
	_, _, err := readControl[identityDirectory](ctx, c.store, identityKey, "identities")
	if err == nil {
		return nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if c.config.Security.AdminToken == "" {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(c.config.Security.AdminToken), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	v := identityDirectory{Users: []identityCredential{{User: User{ID: "user_bootstrap", Username: c.config.Security.AdminUsername, Role: "administrator", Enabled: true, Revision: 1}, PasswordHash: string(hash), TokenHash: identityDigest(c.config.Security.AdminToken)}}}
	err = c.saveControl(ctx, identityKey, "identities", "", v, 0)
	if errors.Is(err, storage.ErrConflict) {
		_, _, err = readControl[identityDirectory](ctx, c.store, identityKey, "identities")
	}
	return err
}

func identityDigest(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func (c *Core) Authenticate(ctx context.Context, username, password, token string) (User, error) {
	v, _, err := readControl[identityDirectory](ctx, c.store, identityKey, "identities")
	if err != nil {
		return User{}, err
	}
	for _, entry := range v.Users {
		if !entry.User.Enabled {
			continue
		}
		if token != "" {
			if entry.TokenHash != "" && subtle.ConstantTimeCompare([]byte(entry.TokenHash), []byte(identityDigest(token))) == 1 {
				return entry.User, nil
			}
		} else if entry.User.Username == username && bcrypt.CompareHashAndPassword([]byte(entry.PasswordHash), []byte(password)) == nil {
			return entry.User, nil
		}
	}
	return User{}, storage.ErrNotFound
}

func (c *Core) Users(ctx context.Context) ([]User, error) {
	v, _, err := readControl[identityDirectory](ctx, c.store, identityKey, "identities")
	users := make([]User, 0, len(v.Users))
	for _, entry := range v.Users {
		users = append(users, entry.User)
	}
	return users, err
}

func (c *Core) User(ctx context.Context, key string) (User, error) {
	users, err := c.Users(ctx)
	if err != nil {
		return User{}, err
	}
	for _, user := range users {
		if user.ID == key {
			return user, nil
		}
	}
	return User{}, storage.ErrNotFound
}

type UserInput struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	Enabled  bool   `json:"enabled"`
	Password string `json:"password,omitempty"`
}

// The directory CAS also serializes last-administrator and name uniqueness checks.
func (c *Core) SaveUser(ctx context.Context, key string, in UserInput, expected int64) (User, error) {
	if strings.TrimSpace(in.Username) != in.Username || len(in.Username) < 1 || len(in.Username) > 100 || strings.ContainsAny(in.Username, "\r\n\t") {
		return User{}, controlInvalid("username", "provide a username of 1–100 characters")
	}
	if in.Role != "administrator" && in.Role != "viewer" {
		return User{}, controlInvalid("role", "expected administrator or viewer")
	}
	if (key == "" || in.Password != "") && (len(in.Password) < 16 || len(in.Password) > 72) {
		return User{}, controlInvalid("password", "password must contain 16–72 bytes")
	}
	v, d, err := readControl[identityDirectory](ctx, c.store, identityKey, "identities")
	if err != nil {
		return User{}, err
	}
	index := -1
	for i, entry := range v.Users {
		if entry.User.ID == key {
			index = i
		}
		if entry.User.ID != key && strings.EqualFold(entry.User.Username, in.Username) {
			return User{}, storage.ErrConflict
		}
	}
	var entry identityCredential
	revokeKeys := false
	if key == "" {
		entry.User.ID = id.New("user")
	} else {
		if index < 0 {
			return User{}, storage.ErrNotFound
		}
		entry = v.Users[index]
		if expected != entry.User.Revision {
			return User{}, storage.ErrPrecondition
		}
		revokeKeys = entry.User.Role != in.Role || (entry.User.Enabled && !in.Enabled)
	}
	entry.User.Username, entry.User.Role, entry.User.Enabled = in.Username, in.Role, in.Enabled
	if revokeKeys {
		entry.User.KeyEpoch++
	}
	entry.User.Revision++
	if in.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err != nil {
			return User{}, err
		}
		entry.PasswordHash = string(hash)
		entry.TokenHash = "" // A password reset also revokes the prior API credential.
	}
	if index < 0 {
		v.Users = append(v.Users, entry)
	} else {
		v.Users[index] = entry
	}
	admins := 0
	for _, user := range v.Users {
		if user.User.Enabled && user.User.Role == "administrator" {
			admins++
		}
	}
	if admins == 0 {
		return User{}, controlInvalid("role", "at least one enabled administrator is required")
	}
	identityWrite, err := c.controlWrite(identityKey, "identities", "", v, d.Revision)
	if err != nil {
		return User{}, err
	}
	writes := []storage.ControlWrite{identityWrite}
	if revokeKeys {
		keys, revision, readErr := c.openAPIKeys(ctx)
		if readErr != nil {
			return User{}, readErr
		}
		changed := false
		for i := range keys.Keys {
			key := &keys.Keys[i]
			if key.RevokedAt == nil && (key.OwnerUserID == entry.User.ID || !key.ProjectScoped() && key.CreatedBy == entry.User.ID) {
				now := c.now()
				key.RevokedAt = &now
				key.Revision++
				changed = true
			}
		}
		if changed {
			keyWrite, writeErr := c.controlWrite(openAPIKeysControlKey, "openapi-keys", "", keys, revision)
			if writeErr != nil {
				return User{}, writeErr
			}
			writes = append(writes, keyWrite)
		}
	}
	err = c.store.WriteControlDocuments(ctx, writes)
	return entry.User, err
}

// ChangePassword invalidates browser sessions and the legacy user Bearer token.
// Project API keys are retained because their ownership is tied to KeyEpoch.
func (c *Core) ChangePassword(ctx context.Context, key, current, replacement string) (User, error) {
	if len(replacement) < 16 || len(replacement) > 72 {
		return User{}, controlInvalid("new_password", "password must contain 16–72 bytes")
	}
	v, d, err := readControl[identityDirectory](ctx, c.store, identityKey, "identities")
	if err != nil {
		return User{}, err
	}
	for i := range v.Users {
		entry := &v.Users[i]
		if entry.User.ID != key {
			continue
		}
		if !entry.User.Enabled || bcrypt.CompareHashAndPassword([]byte(entry.PasswordHash), []byte(current)) != nil {
			return User{}, storage.ErrNotFound
		}
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(replacement), bcrypt.DefaultCost)
		if hashErr != nil {
			return User{}, hashErr
		}
		entry.PasswordHash = string(hash)
		entry.TokenHash = ""
		entry.User.Revision++
		if err = c.saveControl(ctx, identityKey, "identities", "", v, d.Revision); err != nil {
			return User{}, err
		}
		return entry.User, nil
	}
	return User{}, storage.ErrNotFound
}

func (c *Core) RotateUserToken(ctx context.Context, key string, expected int64) (string, error) {
	v, d, err := readControl[identityDirectory](ctx, c.store, identityKey, "identities")
	if err != nil {
		return "", err
	}
	for i := range v.Users {
		entry := &v.Users[i]
		if entry.User.ID != key {
			continue
		}
		if !entry.User.Enabled || entry.User.Revision != expected {
			return "", storage.ErrPrecondition
		}
		raw := make([]byte, 32)
		// Go 1.27 fills the buffer or terminates on entropy failure; Read never returns an error.
		rand.Read(raw)
		token := "nsc_" + hex.EncodeToString(raw)
		entry.TokenHash = identityDigest(token)
		entry.User.Revision++
		if err = c.saveControl(ctx, identityKey, "identities", "", v, d.Revision); err != nil {
			return "", err
		}
		return token, nil
	}
	return "", storage.ErrNotFound
}
