package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yernsun/network-service-composer/internal/storage"
)

// DeploymentDraft belongs to one user and environment. Data is encrypted in
// the control document; it may contain confidential Compose and environment
// inputs, but is never used as an executable deployment review.
type DeploymentDraft struct {
	EnvironmentID string          `json:"environment_id"`
	UserID        string          `json:"user_id"`
	Data          json.RawMessage `json:"data"`
	Revision      int64           `json:"revision"`
}

func deploymentDraftKey(environmentID, userID string) string {
	sum := sha256.Sum256([]byte(environmentID + "\x00" + userID))
	return "deployment-draft:" + hex.EncodeToString(sum[:])
}

func (c *Core) deploymentDraftScope(ctx context.Context, environmentID, userID string) error {
	if strings.TrimSpace(userID) == "" {
		return storage.ErrNotFound
	}
	env, err := c.GetEnvironment(ctx, environmentID)
	if err != nil {
		return err
	}
	if env.System {
		return storage.ErrSystemProtected
	}
	return c.store.CheckProjectResource(ctx, env.ProjectID)
}

func (c *Core) ReadDeploymentDraft(ctx context.Context, environmentID, userID string) (DeploymentDraft, error) {
	if err := c.deploymentDraftScope(ctx, environmentID, userID); err != nil {
		return DeploymentDraft{}, err
	}
	var value DeploymentDraft
	doc, err := c.readEncrypted(ctx, deploymentDraftKey(environmentID, userID), "deployment-draft", &value)
	if errors.Is(err, storage.ErrNotFound) {
		return DeploymentDraft{EnvironmentID: environmentID, UserID: userID}, nil
	}
	if err != nil {
		return DeploymentDraft{}, err
	}
	if doc.ParentID != environmentID || value.UserID != userID {
		return DeploymentDraft{}, storage.ErrNotFound
	}
	value.Revision = doc.Revision
	return value, nil
}

func (c *Core) SaveDeploymentDraft(ctx context.Context, environmentID, userID string, data json.RawMessage, expected int64) (DeploymentDraft, error) {
	if err := c.deploymentDraftScope(ctx, environmentID, userID); err != nil {
		return DeploymentDraft{}, err
	}
	if len(data) == 0 || len(data) > 3<<20 || !json.Valid(data) {
		return DeploymentDraft{}, controlInvalid("data", "invalid or oversized deployment draft")
	}
	value := DeploymentDraft{EnvironmentID: environmentID, UserID: userID, Data: data, Revision: expected + 1}
	write, err := c.encryptedWrite(deploymentDraftKey(environmentID, userID), "deployment-draft", environmentID, value, expected)
	if err != nil {
		return DeploymentDraft{}, err
	}
	if err = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{write}); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return DeploymentDraft{}, storage.ErrPrecondition
		}
		return DeploymentDraft{}, err
	}
	return value, nil
}

func (c *Core) DeleteDeploymentDraft(ctx context.Context, environmentID, userID string, expected int64) error {
	if err := c.deploymentDraftScope(ctx, environmentID, userID); err != nil {
		return err
	}
	key := deploymentDraftKey(environmentID, userID)
	doc, err := c.store.GetControlDocument(ctx, key)
	if errors.Is(err, storage.ErrNotFound) && expected == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	if doc.Kind != "deployment-draft" || doc.ParentID != environmentID {
		return storage.ErrNotFound
	}
	err = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: doc, Expected: expected, Remove: true}})
	if errors.Is(err, storage.ErrConflict) {
		return storage.ErrPrecondition
	}
	return err
}
