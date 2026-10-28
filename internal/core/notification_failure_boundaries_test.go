package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/notification"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// Fail authoritative dependencies at business boundaries, rather than allowing
// an unavailable policy or ownership record to become a global fallback.
type routingBoundaryStore struct {
	storage.Store
	get, list, queued, receipt, write string
}

func (s routingBoundaryStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if key == s.get {
		return storage.ControlDocument{}, errNoticeStore
	}
	return s.Store.GetControlDocument(ctx, key)
}
func (s routingBoundaryStore) ListControlDocuments(ctx context.Context, kind, parent string, page storage.Page) ([]storage.ControlDocument, error) {
	if kind == s.list {
		return nil, errNoticeStore
	}
	return s.Store.ListControlDocuments(ctx, kind, parent, page)
}
func (s routingBoundaryStore) ListNotificationWork(ctx context.Context, kind string, at time.Time, exclude []string, limit int) ([]storage.ControlDocument, error) {
	if kind == s.queued {
		return nil, errNoticeStore
	}
	return s.Store.ListNotificationWork(ctx, kind, at, exclude, limit)
}
func (s routingBoundaryStore) GetNotificationWork(ctx context.Context, kind, event, receiver string) (storage.ControlDocument, error) {
	if receiver == s.receipt {
		return storage.ControlDocument{}, errNoticeStore
	}
	return s.Store.GetNotificationWork(ctx, kind, event, receiver)
}
func (s routingBoundaryStore) WriteControlDocuments(ctx context.Context, writes []storage.ControlWrite) error {
	for _, write := range writes {
		if write.Document.Kind == s.write {
			return errNoticeStore
		}
	}
	return s.Store.WriteControlDocuments(ctx, writes)
}

func TestNotificationConfigDependencyFailureNeverPartiallyPublishes(t *testing.T) {
	for _, boundary := range []string{"settings", "receivers", "receiver-credentials", "routes", "silences", "scope", "generation", "write"} {
		t.Run(boundary, func(t *testing.T) {
			c, _ := routedFixture(t)
			receiver := routedReceiver(t, c, "receiver_team")
			base := c.store
			switch boundary {
			case "settings":
				c.store = routingBoundaryStore{Store: base, get: notificationSettingsKey}
			case "receivers":
				c.store = routingBoundaryStore{Store: base, list: notificationReceiverKind}
			case "receiver-credentials":
				c.store = routingBoundaryStore{Store: base, get: receiver.ID}
			case "routes":
				c.store = routingBoundaryStore{Store: base, list: notificationRouteKind}
			case "silences":
				c.store = routingBoundaryStore{Store: base, list: notificationSilenceKind}
			case "scope":
				c.store = routingBoundaryStore{Store: base, get: "env-1"}
			case "generation":
				c.store = routingBoundaryStore{Store: base, get: notificationRoutingKey}
			case "write":
				c.store = routingBoundaryStore{Store: base, write: notificationRouteKind}
			}
			var err error
			switch boundary {
			case "settings", "receivers", "receiver-credentials":
				_, err = c.ListNotificationReceivers(t.Context())
			case "routes":
				_, err = c.ListNotificationRoutes(t.Context())
			case "silences":
				_, err = c.ListNotificationSilences(t.Context())
			default:
				_, err = c.SaveNotificationRoute(t.Context(), NotificationRouteInput{ScopeType: "environment", ScopeID: "env-1", Enabled: true, Events: []string{"runtimes"}, ReceiverIDs: []string{receiver.ID}}, 0)
			}
			if !errors.Is(err, errNoticeStore) {
				t.Fatal("unavailable authority was accepted", err)
			}
			c.store = base
			if _, err = base.GetControlDocument(t.Context(), notificationRouteID("environment", "env-1")); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("failed configuration partially committed", err)
			}
		})
	}
}

func TestNotificationFanoutFailureRetainsDurableEventForRetry(t *testing.T) {
	for _, boundary := range []string{"routes", "credentials", "ownership", "silence", "dedupe", "fanout-write"} {
		t.Run(boundary, func(t *testing.T) {
			c, _ := routedFixture(t)
			routedReceiver(t, c, "receiver_team")
			routedRule(t, c, "project", "project-1", "receiver_team")
			routedProblem(t, c, true)
			base := c.store
			switch boundary {
			case "routes":
				c.store = routingBoundaryStore{Store: base, get: notificationRouteID("project", "project-1")}
			case "credentials":
				c.store = routingBoundaryStore{Store: base, get: "receiver_team"}
			case "ownership":
				c.store = routingBoundaryStore{Store: base, list: "environment"}
			case "silence":
				c.store = routingBoundaryStore{Store: base, list: notificationSilenceKind}
			case "dedupe":
				c.store = routingBoundaryStore{Store: base, receipt: "default"}
			case "fanout-write":
				c.store = routingBoundaryStore{Store: base, write: notificationDeliveryKind}
			}
			state, _, err := c.notificationRoutingState(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err = c.routePendingNotifications(t.Context(), state.Generation); !errors.Is(err, errNoticeStore) {
				t.Fatal(err)
			}
			c.store = base
			if len(notices(t, c)) != 0 {
				t.Fatal("partial fanout was visible")
			}
			due, err := base.ListNotificationWork(t.Context(), notificationEventKind, c.now(), nil, 200)
			if err != nil || len(due) != 1 {
				t.Fatal("event lost before retry", due, err)
			}
			flushRoutes(t, c)
			if len(notices(t, c)) != 2 {
				t.Fatal("retry did not complete fanout")
			}
		})
	}
}

func TestNotificationSilenceScopeFiltersOverlapAndRevisionBoundaries(t *testing.T) {
	c, _ := routedFixture(t)
	routedProblem(t, c, true)
	flushRoutes(t, c)
	issue := notices(t, c)[0].Event.IssueID
	for _, scope := range []struct{ kind, key string }{{"global", ""}, {"project", "project-1"}, {"environment", "env-1"}, {"issue", issue}} {
		name := "silence_" + scope.kind
		v, err := c.SaveNotificationSilence(t.Context(), name, NotificationSilenceInput{ScopeType: scope.kind, ScopeID: scope.key, Events: []string{"runtimes"}, Reason: "change window", ExpiresAt: c.now().Add(2 * time.Hour)}, 0, "operator")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.SaveNotificationSilence(t.Context(), name, v.NotificationSilenceInput, 0, "other"); !errors.Is(err, storage.ErrPrecondition) {
			t.Fatal("stale silence overwritten", err)
		}
		if _, err = c.CancelNotificationSilence(t.Context(), name, v.Revision-1, "operator"); !errors.Is(err, storage.ErrPrecondition) {
			t.Fatal(err)
		}
		if _, err = c.CancelNotificationSilence(t.Context(), name, v.Revision, ""); err == nil {
			t.Fatal("anonymous cancel accepted")
		}
	}
	values, err := c.ListNotificationSilences(t.Context())
	if err != nil || len(values) != 4 {
		t.Fatal(values, err)
	}
	e := notices(t, c)[0].Event
	if c.silenceUntil(e, AlertScope{ProjectID: "project-1", EnvironmentID: "env-1"}, values).Sub(c.now()) != 2*time.Hour {
		t.Fatal("overlapping silence shortened maintenance")
	}
	if !c.silenceUntil(NotificationEvent{Type: "certificate.problem"}, AlertScope{}, values).IsZero() {
		t.Fatal("unselected group silenced")
	}
	preview, err := c.PreviewNotificationRouting(t.Context(), NotificationPreviewInput{IssueID: issue})
	if err != nil || len(preview.Matches) != 0 || len(preview.SilencedScopes) != 2 {
		t.Fatal(preview, err)
	}
	for _, input := range []NotificationSilenceInput{{ScopeType: "global", ScopeID: "forged", Reason: "work"}, {ScopeType: "project", ScopeID: "missing", Reason: "work"}, {ScopeType: "environment", ScopeID: "missing", Reason: "work"}, {ScopeType: "global", Reason: strings.Repeat("a", 513)}} {
		if _, err = c.SaveNotificationSilence(t.Context(), "silence_invalid", input, 0, "operator"); err == nil {
			t.Fatal("invalid silence accepted", input)
		}
	}
	if _, err = c.CancelNotificationSilence(t.Context(), "missing", 1, "operator"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestNotificationRouteValidationAndDisabledReceiverFallback(t *testing.T) {
	c, settings := routedFixture(t)
	r := routedReceiver(t, c, "receiver_team")
	for _, in := range []NotificationRouteInput{
		{ScopeType: "global", ScopeID: "forged"},
		{ScopeType: "project", ScopeID: "missing"},
		{ScopeType: "environment", ScopeID: "env-1", Enabled: true, Events: []string{"runtimes"}},
		{ScopeType: "environment", ScopeID: "env-1", Enabled: true, Events: []string{"runtimes"}, ReceiverIDs: []string{"receiver_missing"}},
		{ScopeType: "environment", ScopeID: "env-1", Events: []string{"runtimes"}, ReceiverIDs: make([]string, 21)},
		{ScopeType: "global", Events: []string{"runtimes"}, ReceiverIDs: []string{r.ID}, BaseSettingsRevision: settings.Revision + 1},
	} {
		if _, err := c.SaveNotificationRoute(t.Context(), in, 0); err == nil {
			t.Fatal("invalid policy accepted", in)
		}
	}
	rule := routedRule(t, c, "environment", "env-1", r.ID)
	if _, err := c.SaveNotificationRoute(t.Context(), rule.NotificationRouteInput, 0); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal(err)
	}
	if _, err := c.SaveNotificationReceiver(t.Context(), r.ID, NotificationReceiverInput{Name: r.Name, Enabled: false}, r.Revision); err != nil {
		t.Fatal(err)
	}
	e := NotificationEvent{Type: "runtime.problem", Severity: "critical", ResourceID: "runtime-shared", OccurredAt: c.now()}
	if err := c.notificationScopes(t.Context(), &e); err != nil {
		t.Fatal(err)
	}
	matches, err := c.matchNotificationRoutes(t.Context(), e)
	if err != nil || len(matches) != 1 || matches[0].ReceiverID != "default" || len(matches[0].Scopes) != 2 {
		t.Fatal("disabled lane prevented parent fallback", matches, err)
	}
	if err := c.TestNotificationReceiver(t.Context(), r.ID, r.Revision+1); !errors.Is(err, storage.ErrConflict) {
		t.Fatal(err)
	}
	if _, err := c.SaveNotificationReceiver(t.Context(), "BAD", NotificationReceiverInput{Name: "name"}, 0); err == nil {
		t.Fatal("invalid receiver identity accepted")
	}
	global, err := c.globalNotificationRoute(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SaveNotificationReceiver(t.Context(), "default", NotificationReceiverInput{Enabled: false}, global.BaseSettingsRevision)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := c.ListNotificationReceivers(t.Context()); err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
}

func TestNotificationFilteredHistoryPaginationAndCorruptMetadata(t *testing.T) {
	c, _ := routedFixture(t)
	receiver := routedReceiver(t, c, "receiver_team")
	for i := 0; i < 205; i++ {
		event := NotificationEvent{ID: fmt.Sprintf("activity-%03d", i), Type: "runtime.recovered", ResourceID: "runtime-shared", Scopes: []AlertScope{{ProjectID: "project-1", EnvironmentID: "env-1"}}, IssueID: "issue-query", OccurredAt: c.now()}
		write, err := c.notificationDeliveryWrite(event, digestString(event.ID), receiver.ID, receiver.Revision, nil, false, 0)
		if err != nil {
			t.Fatal(err)
		}
		write.Document.ID = fmt.Sprintf("notice-query-%03d", i)
		var value Notification
		if err = json.Unmarshal(write.Document.Payload, &value); err != nil {
			t.Fatal(err)
		}
		value.Status = "DELIVERED"
		if i == 204 {
			value.Status = "FAILED"
		}
		if err = c.saveControl(t.Context(), write.Document.ID, notificationDeliveryKind, "", value, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, filter := range []NotificationFilter{{ReceiverID: "missing"}, {IssueID: "missing"}, {EnvironmentID: "missing"}, {ProjectID: "missing"}, {Status: "SENDING"}, {EventID: "missing"}} {
		rows, err := c.ListNotificationsFiltered(t.Context(), storage.Page{Limit: 200}, filter)
		if err != nil || len(rows) != 0 {
			t.Fatal(filter, rows, err)
		}
	}
	rows, err := c.ListNotificationsFiltered(t.Context(), storage.Page{}, NotificationFilter{ProjectID: "project-1", EnvironmentID: "env-1", Status: "FAILED"})
	if err != nil || len(rows) != 1 {
		t.Fatal("filter stopped at a history page", len(rows), err)
	}
	preview, err := c.PreviewNotificationRouting(t.Context(), NotificationPreviewInput{NotificationID: rows[0].ID})
	if err != nil || len(preview.Matches) != 1 {
		t.Fatal(preview, err)
	}
	if _, err = c.PreviewNotificationRouting(t.Context(), NotificationPreviewInput{NotificationID: rows[0].ID, IssueID: "issue"}); err == nil {
		t.Fatal("ambiguous preview accepted")
	}
	if _, err = c.PreviewNotificationRouting(t.Context(), NotificationPreviewInput{NotificationID: "missing"}); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
	batch := notificationBatch{}
	batch.add(c, "reject-whole-batch", "notification-event", "", map[string]any{"time": time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}, 0)
	batch.add(c, "must-not-commit", "fixture", "", map[string]string{"value": "kept"}, 0)
	if err = batch.commit(t.Context(), c); err == nil {
		t.Fatal("invalid event timestamp committed")
	}
	if _, err = c.store.GetControlDocument(t.Context(), "must-not-commit"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("failed batch partially committed")
	}
}

func TestNotificationRoutedExhaustedAttemptAndExplicitHistoricalRetry(t *testing.T) {
	c, _ := routedFixture(t)
	receiver := routedReceiver(t, c, "receiver_team")
	if err := c.TestNotificationReceiver(t.Context(), receiver.ID, receiver.Revision); err != nil {
		t.Fatal(err)
	}
	row := notices(t, c)[0]
	if _, err := c.RetryNotification(t.Context(), row.ID, row.Revision); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("pending retry accepted", err)
	}
	row.Attempts, row.Status = 5, "SENDING"
	if err := c.saveControl(t.Context(), row.ID, notificationDeliveryKind, "", row, row.Revision); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{result: notification.Result{Delivered: true}}
	worker := NotificationWorker{Core: c, Sender: sender}
	if err := worker.deliveryPass(t.Context()); err != nil {
		t.Fatal(err)
	}
	row = notices(t, c)[0]
	if row.Status != "FAILED" || row.ErrorCode != "delivery_unconfirmed" || len(sender.events) != 0 {
		t.Fatal(row)
	}
	if _, err := c.RetryNotification(t.Context(), row.ID, row.Revision); err != nil {
		t.Fatal(err)
	}
	if err := worker.deliveryPass(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row = notices(t, c)[0]; row.Status != "DELIVERED" || len(sender.events) != 1 {
		t.Fatal(row)
	}
	if _, err := c.RetryNotification(t.Context(), row.ID, row.Revision); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("delivered receipt retried", err)
	}
}
