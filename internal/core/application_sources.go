package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"strings"

	"github.com/yernsun/network-service-composer/internal/storage"
)

type ApplicationSource struct {
	BaselineRevisionID string   `json:"baseline_revision_id"`
	ServiceID          string   `json:"service_id,omitempty"`
	Services           []string `json:"services,omitempty"`
	RevisionID         string   `json:"revision_id,omitempty"`
	Format             string   `json:"format,omitempty"`

	ComposeYAML string `json:"compose_yaml"`
	RuntimeEnv  string `json:"runtime_env"`
}

type ApplicationDraftInput struct {
	ApplicationSource
	ApplicationRevision int64 `json:"application_revision"`
}

type ApplicationDraft struct {
	ApplicationDraftInput
	Revision      int64  `json:"revision"`
	ApplicationID string `json:"application_id"`
}

type applicationDraftReceipt struct {
	Digest string           `json:"digest"`
	Draft  ApplicationDraft `json:"draft"`
}

// Source content is confidential, including plaintext variables. API callers
// must authorize the sensitive read before invoking this method.
func (c *Core) ApplicationSource(ctx context.Context, application, revision string) (ApplicationSource, error) {
	a, err := c.store.GetApplication(ctx, application)
	if err != nil {
		return ApplicationSource{}, err
	}
	if revision == "" {
		revision = a.ActiveRevisionID
		if revision == "" {
			revision = a.DesiredRevisionID
		}
	}
	if revision == "" {
		return ApplicationSource{}, storage.ErrNotFound
	}
	r, err := c.store.GetApplicationRevision(ctx, revision)
	if err != nil {
		return ApplicationSource{}, err
	}
	if r.ApplicationID != application {
		return ApplicationSource{}, storage.ErrNotFound
	}
	compose, _, err := c.keyring.Decrypt(r.EncryptedCompose, revisionAAD(r.ID, "compose"))
	if err != nil {
		return ApplicationSource{}, err
	}
	env, _, err := c.keyring.Decrypt(r.EncryptedEnvironment, revisionAAD(r.ID, "environment"))
	if err != nil {
		return ApplicationSource{}, err
	}
	format := "yaml"
	if json.Valid(compose) {
		format = "json"
	}
	return ApplicationSource{BaselineRevisionID: a.ActiveRevisionID, RevisionID: r.ID, Format: format, ComposeYAML: string(compose), RuntimeEnv: string(env)}, nil
}

func (c *Core) ApplicationDraft(ctx context.Context, application string, serviceIDs ...string) (ApplicationDraft, error) {
	recordID := application
	if len(serviceIDs) > 0 && serviceIDs[0] != "" {
		service, err := c.store.GetService(ctx, serviceIDs[0])
		if err != nil {
			return ApplicationDraft{}, err
		}
		if service.ApplicationID != application {
			return ApplicationDraft{}, storage.ErrNotFound
		}
		recordID = service.ID
	}
	a, err := c.store.GetApplication(ctx, application)
	if err != nil {
		return ApplicationDraft{}, err
	}
	var value ApplicationDraft
	doc, err := c.readEncrypted(ctx, recordID+":source-draft", "application-source-draft", &value)
	if errors.Is(err, storage.ErrNotFound) {
		return ApplicationDraft{ApplicationID: application, ApplicationDraftInput: ApplicationDraftInput{ApplicationRevision: a.Revision}}, nil
	}
	if err != nil {
		return ApplicationDraft{}, err
	}
	if doc.ParentID != application {
		return ApplicationDraft{}, storage.ErrNotFound
	}
	value.Revision = doc.Revision
	return value, nil
}

func (c *Core) SaveApplicationDraft(ctx context.Context, application string, input ApplicationDraftInput, expected int64, idem string, serviceIDs ...string) (ApplicationDraft, error) {
	recordID := application
	if len(serviceIDs) > 0 && serviceIDs[0] != "" {
		service, err := c.store.GetService(ctx, serviceIDs[0])
		if err != nil {
			return ApplicationDraft{}, err
		}
		if service.ApplicationID != application {
			return ApplicationDraft{}, storage.ErrNotFound
		}
		recordID = service.ID
	}
	a, err := c.store.GetApplication(ctx, application)
	if err != nil {
		return ApplicationDraft{}, err
	}
	if err := c.store.CheckProjectResource(ctx, application); err != nil {
		return ApplicationDraft{}, err
	}
	if !a.Managed {
		return ApplicationDraft{}, invalid("validation.service_adoption_required", "", "application is externally managed")
	}
	if strings.TrimSpace(idem) == "" {
		return ApplicationDraft{}, controlInvalid("idempotency_key", "an idempotency key is required")
	}
	if len(input.ComposeYAML) > 2<<20 || len(input.RuntimeEnv) > 1<<20 {
		return ApplicationDraft{}, controlInvalid("compose_yaml", "draft exceeds the source size limit")
	}
	// Saving incomplete source is allowed. Only preflight may accept it for execution.
	raw, err := json.Marshal(struct {
		Input    ApplicationDraftInput
		Expected int64
	}{input, expected})
	if err != nil {
		return ApplicationDraft{}, err
	}
	digest := sha256.Sum256(raw)
	keyHash := sha256.Sum256([]byte(recordID + "\x00" + idem))
	receiptKey := "application-draft-save:" + hex.EncodeToString(keyHash[:])
	readReceipt := func() (ApplicationDraft, error) {
		var receipt applicationDraftReceipt
		doc, err := c.readEncrypted(ctx, receiptKey, "application-draft-receipt", &receipt)
		if err != nil {
			return ApplicationDraft{}, err
		}
		if doc.ParentID != application || receipt.Digest != hex.EncodeToString(digest[:]) {
			return ApplicationDraft{}, storage.ErrConflict
		}
		return receipt.Draft, nil
	}
	if saved, err := readReceipt(); err == nil {
		return saved, nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return ApplicationDraft{}, err
	}
	if input.RevisionID != "" && input.RevisionID != a.ActiveRevisionID {
		return ApplicationDraft{}, storage.ErrPrecondition
	}
	if recordID != application {
		input.ServiceID = recordID
	}
	value := ApplicationDraft{ApplicationDraftInput: input, ApplicationID: application, Revision: expected + 1}
	draft, err := c.encryptedWrite(recordID+":source-draft", "application-source-draft", application, value, expected)
	if err != nil {
		return ApplicationDraft{}, err
	}
	receipt, err := c.encryptedWrite(receiptKey, "application-draft-receipt", application, applicationDraftReceipt{Digest: hex.EncodeToString(digest[:]), Draft: value}, 0)
	if err != nil {
		return ApplicationDraft{}, err
	}
	err = c.store.WriteApplicationDocuments(ctx, application, input.ApplicationRevision, []storage.ControlWrite{draft, receipt})
	if errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrPrecondition) {
		if saved, replayErr := readReceipt(); replayErr == nil {
			return saved, nil
		} else if !errors.Is(replayErr, storage.ErrNotFound) {
			return ApplicationDraft{}, replayErr
		}
		return ApplicationDraft{}, storage.ErrPrecondition
	}
	if err != nil {
		return ApplicationDraft{}, err
	}
	return value, nil
}

func (c *Core) ServiceSource(ctx context.Context, serviceID string) (ApplicationSource, error) {
	service, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return ApplicationSource{}, err
	}
	if service.ApplicationID == "" {
		return ApplicationSource{}, storage.ErrNotFound
	}
	source, err := c.ApplicationSource(ctx, service.ApplicationID, "")
	if err != nil {
		return source, err
	}
	app, err := c.store.GetApplication(ctx, service.ApplicationID)
	if err != nil {
		return source, err
	}
	name := strings.TrimPrefix(service.ServiceKey, app.ProjectName+"_")
	if name == service.ServiceKey {
		name = strings.TrimPrefix(name, app.ProjectName+"/")
	}
	source.ComposeYAML, err = applicationx.SelectServices(source.ComposeYAML, []string{name})
	source.ServiceID = service.ID
	source.Services = []string{name}
	source.Format = "yaml"
	return source, err
}
func (c *Core) ServiceDraft(ctx context.Context, serviceID string) (ApplicationDraft, error) {
	service, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return ApplicationDraft{}, err
	}
	return c.ApplicationDraft(ctx, service.ApplicationID, serviceID)
}
func (c *Core) SaveServiceDraft(ctx context.Context, serviceID string, input ApplicationDraftInput, expected int64, idem string) (ApplicationDraft, error) {
	service, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return ApplicationDraft{}, err
	}
	return c.SaveApplicationDraft(ctx, service.ApplicationID, input, expected, idem, serviceID)
}
