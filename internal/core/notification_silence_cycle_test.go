package core

import (
	"github.com/yernsun/network-service-composer/internal/notification"
	"testing"
	"time"
)

func TestNotificationRepeatedMaintenanceRemindsCurrentOccurrence(t *testing.T) {
	c, _ := routedFixture(t)
	if _, err := c.SaveNotificationSilence(t.Context(), "silence_first", NotificationSilenceInput{ScopeType: "environment", ScopeID: "env-1", Reason: "first"}, 0, "operator"); err != nil {
		t.Fatal(err)
	}
	routedProblem(t, c, true)
	flushRoutes(t, c)
	w := NotificationWorker{Core: c, Sender: &recordingSender{result: notification.Result{Delivered: true}}}
	if err := w.deliveryPass(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := c.now().Add(2 * time.Hour)
	c.now = func() time.Time { return now }
	flushRoutes(t, c)
	if err := w.deliveryPass(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(notices(t, c)) != 2 {
		t.Fatal("first maintenance did not release one reminder")
	}
	if _, err := c.SaveNotificationSilence(t.Context(), "silence_second", NotificationSilenceInput{ScopeType: "environment", ScopeID: "env-1", Reason: "second"}, 0, "operator"); err != nil {
		t.Fatal(err)
	}
	flushRoutes(t, c)
	now = now.Add(2 * time.Hour)
	flushRoutes(t, c)
	if len(notices(t, c)) != 3 {
		t.Fatalf("repeated maintenance lost active reminder: %d deliveries, want 3", len(notices(t, c)))
	}
}

func TestNotificationUnscopedReminderCannotBypassNewEnvironmentOwnership(t *testing.T) {
	c, _ := routedFixture(t)
	if _, err := c.SaveNotificationSilence(t.Context(), "silence_global", NotificationSilenceInput{ScopeType: "global", Reason: "maintenance"}, 0, "operator"); err != nil {
		t.Fatal(err)
	}
	s, _, err := c.notificationRoutingState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = c.observeNotification(t.Context(), NotificationEvent{ID: s.Since.Format(time.RFC3339Nano), Type: "runtime.problem", ResourceID: "runtime-unowned", Status: "OFFLINE"}, true, 0); err != nil {
		t.Fatal(err)
	}
	flushRoutes(t, c)
	env, err := c.GetEnvironment(t.Context(), "env-1")
	if err != nil {
		t.Fatal(err)
	}
	env.RuntimeID = "runtime-unowned"
	if err = c.saveControl(t.Context(), env.ID, "environment", env.ProjectID, env, env.Revision); err != nil {
		t.Fatal(err)
	}
	flushRoutes(t, c)
	now := c.now().Add(2 * time.Hour)
	c.now = func() time.Time { return now }
	flushRoutes(t, c)
	rows := notices(t, c)
	if len(rows) != 1 || len(rows[0].Event.Scopes) != 1 || rows[0].Event.Scopes[0].EnvironmentID != "env-1" {
		t.Fatal("old unscoped reminder bypassed current ownership", rows)
	}
}
