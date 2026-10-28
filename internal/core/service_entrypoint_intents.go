package core

import (
	"context"
	"encoding/json"
	"errors"
	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"maps"
	"slices"
	"strings"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

const entrypointIntentKind = "service-entrypoint-intent"

func legacyEntrypointIntent(applicationID string, entry model.ServiceEntrypoint) model.ServiceEntrypointIntent {
	raw, _ := json.Marshal(entry)
	return model.ServiceEntrypointIntent{ID: "entryintent_legacy_" + digestString(applicationID+":"+string(raw)), Entry: entry}
}

// Read legacy per-service heads rather than mistaking the aggregate revision's
// last submitted entrypoint for every service's current intent.
func (c *Core) revisionEntrypointIntents(ctx context.Context, revision model.ApplicationRevision) (map[string]model.ServiceEntrypointIntent, error) {
	if revision.ConfigurationSource.EntrypointIntents != nil {
		return maps.Clone(revision.ConfigurationSource.EntrypointIntents), nil
	}
	intents := map[string]model.ServiceEntrypointIntent{}
	add := func(entry *model.ServiceEntrypoint) {
		if entry != nil {
			intents[entry.Service] = legacyEntrypointIntent(revision.ApplicationID, *entry)
		}
	}
	for name, head := range revision.ConfigurationSource.ServiceRevisions {
		if head == revision.ID {
			if entry := revision.ConfigurationSource.Entrypoint; entry != nil && entry.Service == name {
				add(entry)
			}
			continue
		}
		previous, err := c.store.GetApplicationRevision(ctx, head)
		if err != nil {
			return nil, err
		}
		if previous.ApplicationID != revision.ApplicationID {
			return nil, storage.ErrConflict
		}
		if entry := previous.ConfigurationSource.Entrypoint; entry != nil && entry.Service == name {
			add(entry)
		}
	}
	if len(revision.ConfigurationSource.ServiceRevisions) == 0 {
		add(revision.ConfigurationSource.Entrypoint)
	}
	return intents, nil
}

func (c *Core) mergeEntrypointIntents(ctx context.Context, source *model.DeploymentSource, baseID, compose string) error {
	_, _, names, err := applicationx.ServiceDelta("", compose, "create")
	if err != nil {
		return err
	}
	intents := map[string]model.ServiceEntrypointIntent{}
	if baseID != "" {
		base, err := c.store.GetApplicationRevision(ctx, baseID)
		if err != nil {
			return err
		}
		intents, err = c.revisionEntrypointIntents(ctx, base)
		if err != nil {
			return err
		}
	}
	if entry := source.Entrypoint; entry != nil && slices.Contains(names, entry.Service) {
		current, exists := intents[entry.Service]
		if !exists || current.Entry != *entry {
			current = model.ServiceEntrypointIntent{ID: id.New("entryintent"), Entry: *entry}
		}
		intents[entry.Service] = current
	}
	for name := range intents {
		if !slices.Contains(names, name) {
			delete(intents, name)
		}
	}
	source.EntrypointIntents = intents
	return nil
}

func (c *Core) operationEntrypointIntent(ctx context.Context, op model.DeploymentOperation) (model.ServiceEntrypointIntent, error) {
	revision, err := c.store.GetApplicationRevision(ctx, op.RevisionID)
	if err != nil {
		return model.ServiceEntrypointIntent{}, err
	}
	if revision.ApplicationID != op.ApplicationID {
		return model.ServiceEntrypointIntent{}, storage.ErrConflict
	}
	if entry := revision.ConfigurationSource.Entrypoint; entry != nil && op.ServiceID == "" {
		if intent, exists := revision.ConfigurationSource.EntrypointIntents[entry.Service]; exists {
			return intent, nil
		}
		return legacyEntrypointIntent(op.ApplicationID, *entry), nil
	}
	intents, err := c.revisionEntrypointIntents(ctx, revision)
	if err != nil {
		return model.ServiceEntrypointIntent{}, err
	}
	name := ""
	if op.ServiceID != "" {
		service, readErr := c.store.GetService(ctx, op.ServiceID)
		if readErr != nil {
			return model.ServiceEntrypointIntent{}, readErr
		}
		if service.ApplicationID != op.ApplicationID || service.RuntimeID != op.RuntimeID {
			return model.ServiceEntrypointIntent{}, storage.ErrConflict
		}
		app, readErr := c.store.GetApplication(ctx, op.ApplicationID)
		if readErr != nil {
			return model.ServiceEntrypointIntent{}, readErr
		}
		name = applicationServiceNameForPolicy(service, app)
	} else if entry := revision.ConfigurationSource.Entrypoint; entry != nil {
		name = entry.Service
	} else if scope := revision.ConfigurationSource.ServiceScope; scope != nil && len(scope.Names) == 1 {
		name = scope.Names[0]
	} else if len(intents) == 1 {
		for key := range intents {
			name = key
		}
	}
	if intent, exists := intents[name]; exists {
		return intent, nil
	}
	return model.ServiceEntrypointIntent{}, storage.ErrNotFound
}

// Only the coordinator migrates receipts. Read endpoints never bootstrap writes.
func (c *Core) readEntrypointReceipt(ctx context.Context, applicationID string, intent model.ServiceEntrypointIntent) (ServiceEntrypointStatus, int64, error) {
	saved, doc, err := readControl[ServiceEntrypointStatus](ctx, c.store, intent.ID, entrypointIntentKind)
	if !errors.Is(err, storage.ErrNotFound) {
		return saved, doc.Revision, err
	}
	if !strings.HasPrefix(intent.ID, "entryintent_legacy_") {
		return saved, 0, nil
	}
	for page := (storage.Page{Limit: 200}); ; {
		operations, err := c.store.ListDeploymentOperations(ctx, applicationID, page)
		if err != nil {
			return saved, 0, err
		}
		for _, op := range operations {
			if op.RevisionID == "" {
				continue
			}
			revision, readErr := c.store.GetApplicationRevision(ctx, op.RevisionID)
			if readErr != nil {
				return saved, 0, readErr
			}
			if entry := revision.ConfigurationSource.Entrypoint; entry == nil || *entry != intent.Entry {
				continue
			}
			previous, _, readErr := readControl[ServiceEntrypointStatus](ctx, c.store, op.ID+":entrypoint", "service-entrypoint")
			if errors.Is(readErr, storage.ErrNotFound) {
				continue
			}
			if readErr != nil {
				return saved, 0, readErr
			}
			if saved.Status == "" || previous.Status == "SUBMITTED" || previous.Attempts > saved.Attempts {
				saved = previous
			}
			if previous.Status == "SUBMITTED" {
				return saved, 0, nil
			}
		}
		if len(operations) < page.Limit {
			return saved, 0, nil
		}
		page.After = operations[len(operations)-1].ID
	}
}
