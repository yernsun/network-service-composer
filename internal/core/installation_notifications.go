package core

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// System maintenance uses the existing deployment notification group and outbox
// deduplication. Only terminal/unknown transitions are collected, never polls.
func (c *Core) installationNotifications(ctx context.Context, since time.Time, revision int64) error {
	owner, err := c.installationOwner(ctx)
	if err != nil {
		return err
	}
	if owner.ID == "" {
		return nil
	}
	for page := (storage.Page{Limit: 200}); ; {
		documents, err := c.store.ListControlDocuments(ctx, "system-installation-job", "", page)
		if err != nil {
			return err
		}
		for _, document := range documents {
			var job bootstrap.MaintenanceJob
			if err = json.Unmarshal(document.Payload, &job); err != nil {
				return err
			}
			if job.InstallationID != owner.ID || document.ParentID != owner.ID {
				return storage.ErrConflict
			}
			if document.UpdatedAt.Before(since) || (job.State != bootstrap.Succeeded && job.State != bootstrap.Failed && job.State != bootstrap.Blocked && job.State != bootstrap.Unknown) {
				continue
			}
			event := NotificationEvent{ID: job.ID + ":" + string(job.State), Type: "deployment.system_" + strings.ToLower(string(job.State)), ResourceID: job.InstallationID, OperationID: job.ID, Status: string(job.State), OccurredAt: document.UpdatedAt, Severity: "info"}
			if job.State != bootstrap.Succeeded {
				event.Severity = "warning"
			}
			if err = c.enqueueNotification(ctx, event, revision); err != nil {
				return err
			}
		}
		if len(documents) < page.Limit {
			return nil
		}
		page.After = documents[len(documents)-1].ID
	}
}
