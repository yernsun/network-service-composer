package core

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/yernsun/network-service-composer/internal/storage"
)

const notificationSuppressionKind = "notification-suppression"

type notificationSuppression struct {
	storage.ControlDocument
	Event       NotificationEvent `json:"event"`
	EventKey    string            `json:"event_key"`
	Scope       AlertScope        `json:"scope"`
	Status      string            `json:"status"`
	NextAttempt time.Time         `json:"next_attempt_at"`
	Cycle       int64             `json:"cycle"`
}

func notificationEventScopes(event *NotificationEvent, scopes []AlertScope) {
	event.Scopes = scopes
	event.EnvironmentID = ""
	if len(scopes) == 1 {
		event.EnvironmentID = scopes[0].EnvironmentID
	}
}

func (c *Core) notificationStillActive(ctx context.Context, event NotificationEvent) (bool, error) {
	if !notificationProblem(event.Type) || event.IssueID == "" {
		return true, nil
	}
	issue, _, err := readControl[Issue](ctx, c.store, event.IssueID, "issue")
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	return issue.Active && issue.Occurrences == event.Occurrence && issue.ObservationState != "UNMONITORED", err
}

func (c *Core) routeNotificationEvent(ctx context.Context, record notificationEventRecord, generation int64) error {
	if held, err := storage.RecoveryHeld(ctx, c.store, "notification", record.ID); err != nil {
		return err
	} else if held {
		return nil
	}
	event := record.Event
	active, err := c.notificationStillActive(ctx, event)
	if err != nil {
		return err
	}
	if err = c.notificationScopes(ctx, &event); err != nil {
		return err
	}
	if record.Restricted {
		if len(record.AllowedScopes) == 0 && len(event.Scopes) != 0 {
			active = false
		}
		event.Scopes = slices.DeleteFunc(event.Scopes, func(scope AlertScope) bool { return !slices.Contains(record.AllowedScopes, scope) })
		if len(record.AllowedScopes) != 0 && len(event.Scopes) == 0 {
			active = false
		}
	}
	authoritativeScopes := slices.Clone(event.Scopes)
	silences, err := c.ListNotificationSilences(ctx)
	if err != nil {
		return err
	}
	writes := notificationBatch{}
	scopes := event.Scopes
	if len(scopes) == 0 {
		scopes = []AlertScope{{}}
	}
	allowed := []AlertScope{}
	muted := false
	for _, scope := range scopes {
		until := c.silenceUntil(event, scope, silences)
		if until.IsZero() {
			if scope.EnvironmentID != "" {
				allowed = append(allowed, scope)
			}
			continue
		}
		muted = true
		if !active || !notificationProblem(event.Type) {
			continue
		}
		baseKey := record.EventKey
		if event.IssueID != "" {
			baseKey = event.IssueID + ":" + strconv.FormatInt(event.Occurrence, 10)
		}
		key := digestString(baseKey + ":" + scope.ProjectID + ":" + scope.EnvironmentID)
		previous, doc, err := readControl[notificationSuppression](ctx, c.store, "nsupp_"+key, notificationSuppressionKind)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		if doc.Revision == 0 || previous.Status == "RELEASED" || previous.Status == "OBSOLETE" {
			value := notificationSuppression{Event: event, EventKey: key, Scope: scope, Status: "PENDING", NextAttempt: until, Cycle: previous.Cycle + 1}
			writes.add(c, "nsupp_"+key, notificationSuppressionKind, "", value, doc.Revision)
		}
	}
	notificationEventScopes(&event, allowed)
	// A global event has no scope rows. All-scoped suppression must never turn it
	// into a global fallback event.
	canRoute := active && (len(allowed) > 0 || len(authoritativeScopes) == 0 && c.silenceUntil(event, AlertScope{}, silences).IsZero())
	matches := []NotificationRouteMatch{}
	if canRoute {
		matches, err = c.matchNotificationRoutes(ctx, event)
		if err != nil {
			return err
		}
	}
	if record.TargetReceiverID != "" {
		matches = slices.DeleteFunc(matches, func(match NotificationRouteMatch) bool { return match.ReceiverID != record.TargetReceiverID })
	}
	for _, match := range matches {
		_, err := c.store.GetNotificationWork(ctx, notificationDeliveryKind, record.EventKey, match.ReceiverID)
		if err == nil {
			continue
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		deliveryEvent := event
		notificationEventScopes(&deliveryEvent, match.Scopes)
		// Recipient bodies have recipient-specific immutable IDs. The source event
		// key joins their delivery histories without leaking another team's scope.
		deliveryEvent.ID = "event_" + digestString(record.EventKey+":"+match.ReceiverID)
		writes.prepared(c.notificationDeliveryWrite(deliveryEvent, record.EventKey, match.ReceiverID, match.ReceiverRevision, match.Routes, false, generation))
	}
	record.Status = "UNROUTED"
	if !active {
		record.Status = "OBSOLETE"
	} else if len(matches) > 0 {
		record.Status = "ROUTED"
	} else if muted {
		record.Status = "SILENCED"
	}
	notificationEventScopes(&record.Event, authoritativeScopes)
	record.RoutedGeneration = generation
	writes.add(c, record.ID, notificationEventKind, "", record, record.Revision)
	return writes.commit(ctx, c)
}

func (c *Core) routePendingNotifications(ctx context.Context, generation int64) error {
	docs, err := c.store.ListNotificationWork(ctx, notificationEventKind, c.now(), nil, 200)
	if err != nil {
		return err
	}
	var result error
	for _, doc := range docs {
		var record notificationEventRecord
		if err := json.Unmarshal(doc.Payload, &record); err != nil {
			return err
		}
		record.ControlDocument = doc
		if err := c.routeNotificationEvent(ctx, record, generation); err != nil && !errors.Is(err, storage.ErrPrecondition) && !errors.Is(err, storage.ErrConflict) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (c *Core) rerouteCurrentNotifications(ctx context.Context, generation int64) error {
	for page := (storage.Page{Limit: 200}); ; {
		docs, err := c.store.ListControlDocuments(ctx, notificationCurrentKind, "", page)
		if err != nil {
			return err
		}
		for _, doc := range docs {
			var pointer notificationCurrent
			if err := json.Unmarshal(doc.Payload, &pointer); err != nil {
				return err
			}
			record, d, err := readControl[notificationEventRecord](ctx, c.store, pointer.EventID, notificationEventKind)
			if err != nil {
				return err
			}
			current := record.Event
			if err = c.notificationScopes(ctx, &current); err != nil {
				return err
			}
			if record.RoutedGeneration == generation && slices.Equal(record.Event.Scopes, current.Scopes) {
				continue
			}
			record.ControlDocument = d
			if err = c.routeNotificationEvent(ctx, record, generation); err != nil && !errors.Is(err, storage.ErrPrecondition) && !errors.Is(err, storage.ErrConflict) {
				return err
			}
		}
		if len(docs) < page.Limit {
			return nil
		}
		page.After = docs[len(docs)-1].ID
	}
}

func (c *Core) releaseSilencedNotifications(ctx context.Context) error {
	silences, err := c.ListNotificationSilences(ctx)
	if err != nil {
		return err
	}
	// Configuration changes wake pending suppressions through the transactional
	// projection. Every other pass selects only due records.
	docs, err := c.store.ListNotificationWork(ctx, notificationSuppressionKind, c.now(), nil, 200)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		var value notificationSuppression
		if err = json.Unmarshal(doc.Payload, &value); err != nil {
			return err
		}
		if until := c.silenceUntil(value.Event, value.Scope, silences); until.After(c.now()) {
			value.NextAttempt = until
			if err = c.saveControl(ctx, doc.ID, notificationSuppressionKind, "", value, doc.Revision); err != nil {
				return err
			}
			continue
		}
		active, err := c.notificationStillActive(ctx, value.Event)
		if err != nil {
			return err
		}
		writes := notificationBatch{}
		value.Status = "OBSOLETE"
		if active {
			event := value.Event
			event.ID = "release:" + doc.ID + ":" + strconv.FormatInt(value.Cycle, 10)
			key := digestString(event.ID)
			event.ID = "event_" + key
			scopes := []AlertScope{}
			if value.Scope.EnvironmentID != "" {
				scopes = append(scopes, value.Scope)
			}
			notificationEventScopes(&event, scopes)
			record := notificationEventRecord{Event: event, EventKey: key, Status: "PENDING", NextAttempt: c.now(), CapturedAt: c.now(), Restricted: true, AllowedScopes: scopes}
			writes.add(c, "nevent_"+key, notificationEventKind, "", record, 0)
			value.Status = "RELEASED"
		}
		writes.add(c, doc.ID, notificationSuppressionKind, "", value, doc.Revision)
		if err = writes.commit(ctx, c); err != nil && !errors.Is(err, storage.ErrPrecondition) && !errors.Is(err, storage.ErrConflict) {
			return err
		}
	}
	return nil
}
