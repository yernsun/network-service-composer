package core

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/notification"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestNotificationManualRetryRechecksCurrentAuthorityAndKeepsPayload(t *testing.T) {
	for _, scenario := range []string{"same-channel", "changed-route", "resolved", "unmonitored", "new-occurrence", "moved-scope", "disabled-receiver", "silenced", "no-route", "delivered"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := routedFixture(t)
			receiver := routedReceiver(t, c, "receiver_team")
			rule := routedRule(t, c, "project", "project-1", receiver.ID)
			routedProblem(t, c, true)
			flushRoutes(t, c)
			rows := notices(t, c)
			value := rows[slices.IndexFunc(rows, func(row Notification) bool { return row.ReceiverID == receiver.ID })]
			value.Status, value.ErrorCode, value.Attempts = "FAILED", "response_lost", 5
			if err := c.saveControl(t.Context(), value.ID, notificationDeliveryKind, "", value, value.Revision); err != nil {
				t.Fatal(err)
			}
			value.Revision++
			original, _ := json.Marshal(value.Event)
			switch scenario {
			case "changed-route", "no-route":
				rule.Enabled = scenario != "no-route"
				rule.Severities = []string{"critical"}
				if _, err := c.SaveNotificationRoute(t.Context(), rule.NotificationRouteInput, rule.Revision); err != nil {
					t.Fatal(err)
				}
			case "resolved", "unmonitored", "new-occurrence":
				issue, doc, err := readControl[Issue](t.Context(), c.store, value.Event.IssueID, "issue")
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "resolved" {
					issue.Active = false
				} else if scenario == "unmonitored" {
					issue.ObservationState = "UNMONITORED"
				} else {
					issue.Occurrences++
				}
				if err := c.saveControl(t.Context(), doc.ID, "issue", doc.ParentID, issue, doc.Revision); err != nil {
					t.Fatal(err)
				}
			case "moved-scope":
				env, doc, err := readControl[Environment](t.Context(), c.store, "env-1", "environment")
				if err != nil {
					t.Fatal(err)
				}
				env.RuntimeID = "other-runtime"
				if err := c.saveControl(t.Context(), doc.ID, "environment", doc.ParentID, env, doc.Revision); err != nil {
					t.Fatal(err)
				}
			case "disabled-receiver":
				if _, err := c.SaveNotificationReceiver(t.Context(), receiver.ID, NotificationReceiverInput{Name: receiver.Name}, receiver.Revision); err != nil {
					t.Fatal(err)
				}
			case "silenced":
				if _, err := c.SaveNotificationSilence(t.Context(), "silence_retry", NotificationSilenceInput{ScopeType: "issue", ScopeID: value.Event.IssueID, Reason: "maintenance"}, 0, "operator"); err != nil {
					t.Fatal(err)
				}
			case "delivered":
				value.Status = "DELIVERED"
				if err := c.saveControl(t.Context(), value.ID, notificationDeliveryKind, "", value, value.Revision); err != nil {
					t.Fatal(err)
				}
				value.Revision++
			}
			if _, err := c.RetryNotification(t.Context(), value.ID, value.Revision-1); !errors.Is(err, storage.ErrPrecondition) {
				t.Fatal("stale retry changed delivery", err)
			}
			retried, err := c.RetryNotification(t.Context(), value.ID, value.Revision)
			if scenario != "same-channel" && scenario != "changed-route" {
				if !errors.Is(err, storage.ErrConflict) {
					t.Fatal("obsolete or unauthorized retry accepted", err)
				}
				return
			}
			body, _ := json.Marshal(retried.Event)
			if err != nil || string(body) != string(original) || retried.ID != value.ID || retried.Status != "PENDING" || retried.Attempts != 0 || !retried.ManualRetry || !retried.LeaseUntil.IsZero() || retried.ErrorCode != "" {
				t.Fatal("retry changed signed payload or state", retried, err)
			}
			sender := &recordingSender{result: notification.Result{Delivered: true}}
			if err := (NotificationWorker{Core: c, Sender: sender}).deliverRouted(t.Context(), retried); err != nil || len(sender.events) != 1 || sender.events[0] != value.Event.ID {
				t.Fatal("retry did not keep the original receiver event", sender.events, err)
			}
		})
	}
}

func TestNotificationScopedDeliveryDoesNotDependOnUnusedParent(t *testing.T) {
	c, _ := routedFixture(t)
	routedReceiver(t, c, "receiver_team")
	routedRule(t, c, "environment", "env-1", "receiver_team")
	routedProblem(t, c, true)
	flushRoutes(t, c)
	rows := notices(t, c)
	value := rows[slices.IndexFunc(rows, func(row Notification) bool { return row.ReceiverID == "receiver_team" })]
	value.Status = "FAILED"
	if err := c.saveControl(t.Context(), value.ID, notificationDeliveryKind, "", value, value.Revision); err != nil {
		t.Fatal(err)
	}
	value.Revision++
	base := c.store
	c.store = routingBoundaryStore{Store: base, get: notificationSettingsKey, list: notificationRouteKind}
	retried, err := c.RetryNotification(t.Context(), value.ID, value.Revision)
	if err != nil {
		t.Fatal("unreadable unused default blocked a scoped receiver", err)
	}
	sender := &recordingSender{result: notification.Result{Delivered: true}}
	if err := (NotificationWorker{Core: c, Sender: sender}).deliverRouted(t.Context(), retried); err != nil || len(sender.events) != 1 {
		t.Fatal("scoped delivery depended on unused parent", err)
	}
	c.store = routingBoundaryStore{Store: base, get: notificationRouteID("environment", "env-1")}
	if matches, err := c.matchNotificationRoutes(t.Context(), value.Event); !errors.Is(err, errNoticeStore) || len(matches) != 0 {
		t.Fatal("unreadable child became another team's fallback", matches, err)
	}
	c.store = base
	// A persisted scope mismatch is also unknown authority, never permission to
	// use the route's receivers for a different environment.
	_, doc, _ := readControl[NotificationRoute](t.Context(), base, notificationRouteID("environment", "env-1"), notificationRouteKind)
	if err := c.saveControl(t.Context(), doc.ID, notificationRouteKind, doc.ParentID, NotificationRoute{NotificationRouteInput: NotificationRouteInput{ScopeType: "project", ScopeID: "project-2", Enabled: true}}, doc.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := c.matchNotificationRoutes(t.Context(), value.Event); err == nil {
		t.Fatal("mismatched scope was accepted")
	}
	// A no-scope event reads only the global rule.
	c.store = routingBoundaryStore{Store: base, get: notificationRouteID("environment", "")}
	if matches, err := c.matchNotificationRoutes(t.Context(), NotificationEvent{Type: "runtime.problem", OccurredAt: c.now().Add(time.Second)}); err != nil || len(matches) != 1 || matches[0].ReceiverID != "default" {
		t.Fatal(matches, err)
	}
}
