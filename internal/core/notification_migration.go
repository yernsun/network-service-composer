package core

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/storage"
)

// Bootstrap preserves old delivery identities and signed bodies. Queue metadata
// is rebuilt transactionally; completed deliveries remain dedupe receipts.
func (c *Core) initializeNotificationRouting(ctx context.Context) error {
	state, doc, err := c.notificationRoutingState(ctx)
	if err != nil || state.Initialized {
		return err
	}
	if doc.Revision == 0 {
		settings, _, err := c.notificationSettings(ctx)
		if err != nil {
			return err
		}
		state.Since, state.LegacySince = settings.Since, settings.Since
		if state.Since.IsZero() {
			state.Since = c.now()
		}
		if err = c.saveControl(ctx, notificationRoutingKey, notificationRoutingKind, "", state, 0); err != nil {
			return err
		}
	}
	for page := (storage.Page{Limit: 200}); ; {
		rows, err := c.ListNotifications(ctx, page)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.ReceiverID != "" {
				continue
			}
			if err := c.migrateNotification(ctx, row, state.LegacySince); err != nil {
				return err
			}
		}
		if len(rows) < page.Limit {
			break
		}
		page.After = rows[len(rows)-1].ID
	}
	state, doc, err = c.notificationRoutingState(ctx)
	if err != nil {
		return err
	}
	state.Initialized = true
	return c.saveControl(ctx, doc.ID, notificationRoutingKind, "", state, doc.Revision)
}

func (c *Core) migrateNotification(ctx context.Context, row Notification, since time.Time) error {
	event := row.Event
	logical := event.ID
	current := false
	if code, _ := notificationIssue(event.Type); code != "" {
		stateKey := "notice-state:" + digestString(since.Format(time.RFC3339Nano)+":"+event.ResourceID)
		previous, _, err := readControl[struct {
			Bad      bool  `json:"bad"`
			Sequence int64 `json:"sequence"`
		}](ctx, c.store, stateKey, "notification-state")
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		candidate := stateKey + ":" + strconv.FormatInt(previous.Sequence, 10)
		if previous.Sequence > 0 && strings.HasSuffix(event.ID, "_"+digestString(candidate)) {
			logical, current = candidate, previous.Bad && notificationProblem(event.Type)
		}
	} else {
		switch event.Type {
		case "image.available":
			logical = event.ResourceID + ":available"
		case "image.failed":
			logical = event.ResourceID + ":submission-failed"
		case "deployment.succeeded", "deployment.failed", "deployment.rolled_back", "deployment.rollback_failed":
			logical = event.OperationID + ":" + event.Status
		}
	}
	row.EventKey, row.ReceiverID, row.Legacy = digestString(logical), defaultNotificationReceiver, true
	row.DirectTest = event.Type == "notification.test"
	if err := c.notificationScopes(ctx, &event); err != nil {
		return err
	}
	row.LegacyScopes = event.Scopes
	if event.Severity == "" {
		event.Severity = "info"
		if notificationProblem(event.Type) {
			event.Severity = "critical"
		}
	}
	writes := notificationBatch{}
	if current {
		code, kind := notificationIssue(event.Type)
		active := true
		issue, issueWrite, err := c.issueObservation(ctx, code, kind, event.ResourceID, alertLink(kind, event.ResourceID), &active, "UNKNOWN", event.OperationID)
		if err != nil {
			return err
		}
		if issueWrite != nil {
			writes.prepared(*issueWrite, nil)
		}
		event.IssueID, event.Occurrence = issue.ID, issue.Occurrences
		recordID := "nevent_" + row.EventKey
		exists, err := c.notificationEventExists(ctx, recordID)
		if err != nil {
			return err
		}
		if !exists {
			record := notificationEventRecord{Event: event, EventKey: row.EventKey, Status: "PENDING", NextAttempt: c.now(), CapturedAt: c.now()}
			writes.add(c, recordID, notificationEventKind, "", record, 0)
		}
		pointerKey := "notice-current:" + digestString(issue.ID)
		_, pointer, err := readControl[notificationCurrent](ctx, c.store, pointerKey, notificationCurrentKind)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		writes.add(c, pointerKey, notificationCurrentKind, "", notificationCurrent{EventID: recordID}, pointer.Revision)
	}
	writes.add(c, row.ID, notificationDeliveryKind, "", row, row.Revision)
	return writes.commit(ctx, c)
}
