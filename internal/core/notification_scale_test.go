package core

import (
	"fmt"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestNotificationConfigurationAndActiveIssuePagination(t *testing.T) {
	c, _ := routedFixture(t)
	writes := notificationBatch{}
	for i := 0; i < 201; i++ {
		suffix := fmt.Sprintf("%03d", i)
		receiver := "receiver_scale_" + suffix
		writes.prepared(c.encryptedWrite(receiver, notificationReceiverKind, "", notificationReceiver{Name: receiver, Endpoint: "https://hooks.example.test/notify", Secret: "fixture-signing-secret", Since: c.now()}, 0))
		scope := "scale-env-" + suffix
		writes.add(c, scope, "environment", "project-1", Environment{ProjectID: "project-1", RuntimeID: "unused-runtime"}, 0)
		writes.add(c, notificationRouteID("environment", scope), notificationRouteKind, scope, NotificationRoute{NotificationRouteInput: NotificationRouteInput{ScopeType: "environment", ScopeID: scope, ReceiverIDs: []string{receiver}, Events: []string{"runtimes"}}}, 0)
		writes.add(c, "silence_scale_"+suffix, notificationSilenceKind, "", NotificationSilence{NotificationSilenceInput: NotificationSilenceInput{ScopeType: "environment", ScopeID: scope, Reason: "planned work", ExpiresAt: c.now().Add(time.Hour)}, Actor: "operator"}, 0)
		issue := "issue-scale-" + suffix
		eventID := "nevent_scale_" + suffix
		event := NotificationEvent{ID: "event-scale-" + suffix, Type: "runtime.problem", Severity: "critical", ResourceID: "unbound-" + suffix, IssueID: issue, Occurrence: 1, OccurredAt: c.now()}
		writes.add(c, issue, "issue", "", Issue{Code: "runtime_unavailable", ResourceID: event.ResourceID, ResourceType: "runtime", Active: true, Occurrences: 1, ObservationState: "CONFIRMED"}, 0)
		writes.add(c, eventID, notificationEventKind, "", notificationEventRecord{Event: event, EventKey: digestString(event.ID), Status: "PENDING", NextAttempt: c.now(), CapturedAt: c.now()}, 0)
		writes.add(c, "notice-current:"+digestString(issue), notificationCurrentKind, "", notificationCurrent{EventID: eventID}, 0)
	}
	if err := writes.commit(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	if values, err := c.ListNotificationReceivers(t.Context()); err != nil || len(values) != 202 {
		t.Fatal("receiver directory truncated", len(values), err)
	}
	if values, err := c.ListNotificationRoutes(t.Context()); err != nil || len(values) != 202 {
		t.Fatal("route directory truncated", len(values), err)
	}
	if values, err := c.ListNotificationSilences(t.Context()); err != nil || len(values) != 201 {
		t.Fatal("maintenance directory truncated", len(values), err)
	}
	state, _, err := c.notificationRoutingState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = c.rerouteCurrentNotifications(t.Context(), state.Generation+1); err != nil {
		t.Fatal(err)
	}
	page := storage.Page{Limit: 200}
	count := 0
	for {
		rows, err := c.ListNotifications(t.Context(), page)
		if err != nil {
			t.Fatal(err)
		}
		count += len(rows)
		for _, row := range rows {
			if row.ReceiverID != "default" || len(row.Event.Scopes) != 0 {
				t.Fatal("unrelated directory rules captured an issue", row)
			}
		}
		if len(rows) < page.Limit {
			break
		}
		page.After = rows[len(rows)-1].ID
	}
	if count != 201 {
		t.Fatal("current issue refresh stopped at a page boundary", count)
	}
	if err = c.rerouteCurrentNotifications(t.Context(), state.Generation+1); err != nil {
		t.Fatal(err)
	}
	if err = c.routePendingNotifications(t.Context(), state.Generation+1); err != nil {
		t.Fatal(err)
	}
	if rows, err := c.ListNotifications(t.Context(), storage.Page{Limit: 200}); err != nil || len(rows) != 200 {
		t.Fatal("refresh lost or replayed existing receipts", len(rows), err)
	}
}
