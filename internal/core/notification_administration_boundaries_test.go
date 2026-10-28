package core

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestNotificationAdministrativeOutagesKeepReviewedConfiguration(t *testing.T) {
	for _, action := range []string{"receiver", "global-rule", "scoped-rule", "silence", "cancel-silence", "legacy-settings"} {
		for _, dependency := range []string{"configuration", "generation", "commit"} {
			t.Run(action+"/"+dependency, func(t *testing.T) {
				c, settings := routedFixture(t)
				r := routedReceiver(t, c, "receiver_team")
				rule := routedRule(t, c, "global", "", r.ID)
				quiet, err := c.SaveNotificationSilence(t.Context(), "silence_review", NotificationSilenceInput{ScopeType: "global", Reason: "reviewed work"}, 0, "operator")
				if err != nil {
					t.Fatal(err)
				}
				base := c.store
				kind, key := notificationReceiverKind, r.ID
				switch action {
				case "global-rule":
					kind, key = notificationRouteKind, rule.ID
				case "scoped-rule":
					kind, key = notificationRouteKind, notificationRouteID("environment", "env-1")
				case "silence", "cancel-silence":
					kind, key = notificationSilenceKind, quiet.ID
				case "legacy-settings":
					kind, key = notificationSettingsKind, notificationRouteID("global", "")
				}
				before, beforeErr := base.GetControlDocument(t.Context(), key)
				switch dependency {
				case "configuration":
					c.store = routingBoundaryStore{Store: base, get: key}
				case "generation":
					c.store = routingBoundaryStore{Store: base, get: notificationRoutingKey}
				case "commit":
					c.store = routingBoundaryStore{Store: base, write: kind}
				}
				switch action {
				case "receiver":
					_, err = c.SaveNotificationReceiver(t.Context(), r.ID, NotificationReceiverInput{Name: "reviewed edit", Enabled: true}, r.Revision)
				case "global-rule":
					_, err = c.SaveNotificationRoute(t.Context(), rule.NotificationRouteInput, rule.Revision)
				case "scoped-rule":
					_, err = c.SaveNotificationRoute(t.Context(), NotificationRouteInput{ScopeType: "environment", ScopeID: "env-1", Enabled: true, Events: []string{"runtimes"}, ReceiverIDs: []string{r.ID}}, 0)
				case "silence":
					_, err = c.SaveNotificationSilence(t.Context(), quiet.ID, quiet.NotificationSilenceInput, quiet.Revision, "operator")
				case "cancel-silence":
					_, err = c.CancelNotificationSilence(t.Context(), quiet.ID, quiet.Revision, "operator")
				case "legacy-settings":
					// Creating the explicit global rule advances the legacy mirror.
					_, doc, e := c.notificationSettings(t.Context())
					if e != nil {
						t.Fatal(e)
					}
					_, err = c.SaveNotificationSettings(t.Context(), NotificationSettingsInput{Enabled: true, Events: settings.Events}, doc.Revision)
				}
				if !errors.Is(err, errNoticeStore) {
					t.Fatal("unavailable configuration was published", err)
				}
				c.store = base
				after, afterErr := base.GetControlDocument(t.Context(), key)
				if !errors.Is(afterErr, beforeErr) || after.Revision != before.Revision || string(after.Payload) != string(before.Payload) {
					t.Fatal("partial administrative change", before, after)
				}
			})
		}
	}
}

func TestNotificationConsoleRefusesUnknownPreviewAndHealthEvidence(t *testing.T) {
	for _, dependency := range []string{"issue", "delivery", "ownership", "silences", "route", "health-state", "health-sources", "history"} {
		t.Run(dependency, func(t *testing.T) {
			c, _ := routedFixture(t)
			routedProblem(t, c, true)
			flushRoutes(t, c)
			row := notices(t, c)[0]
			base := c.store
			switch dependency {
			case "issue":
				c.store = routingBoundaryStore{Store: base, get: row.Event.IssueID}
			case "delivery":
				c.store = routingBoundaryStore{Store: base, get: row.ID}
			case "ownership":
				c.store = routingBoundaryStore{Store: base, list: "environment"}
			case "silences":
				c.store = routingBoundaryStore{Store: base, list: notificationSilenceKind}
			case "route":
				c.store = routingBoundaryStore{Store: base, get: notificationRouteID("global", "")}
			case "health-state":
				c.store = routingBoundaryStore{Store: base, get: notificationRoutingKey}
			case "health-sources":
				c.store = routingBoundaryStore{Store: base, list: "notification-source-health"}
			case "history":
				c.store = routingBoundaryStore{Store: base, list: notificationDeliveryKind}
			}
			var err error
			switch dependency {
			case "delivery":
				_, err = c.PreviewNotificationRouting(t.Context(), NotificationPreviewInput{NotificationID: row.ID})
			case "health-state", "health-sources":
				_, err = c.NotificationHealth(t.Context())
			case "history":
				_, err = c.ListNotificationsFiltered(t.Context(), storage.Page{Limit: 201}, NotificationFilter{EnvironmentID: "env-1"})
			default:
				_, err = c.PreviewNotificationRouting(t.Context(), NotificationPreviewInput{IssueID: row.Event.IssueID})
			}
			if !errors.Is(err, errNoticeStore) {
				t.Fatal("unknown evidence looked successful", err)
			}
		})
	}
	c, _ := routedFixture(t)
	if err := c.saveControl(t.Context(), "unsupported-issue", "issue", "", Issue{Code: "unrelated"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PreviewNotificationRouting(t.Context(), NotificationPreviewInput{IssueID: "unsupported-issue"}); err == nil {
		t.Fatal("unsupported issue acquired a route")
	}
	if err := c.saveControl(t.Context(), "bad-source", "notification-source-health", "", map[string]string{"failed": "not a boolean"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.NotificationHealth(t.Context()); err == nil {
		t.Fatal("corrupt source health looked healthy")
	}
}

func TestNotificationUnscopedQuietPreviewAndExtendedMaintenance(t *testing.T) {
	c, _ := routedFixture(t)
	if err := c.observeNotification(t.Context(), NotificationEvent{ID: "unscoped", Type: "runtime.problem", ResourceID: "unbound-runtime", Status: "CONFIRMED"}, true, 0); err != nil {
		t.Fatal(err)
	}
	// Runtime without environment ownership is a global issue; client-supplied scope
	// cannot manufacture membership in a team's environment.
	flushRoutes(t, c)
	rows := notices(t, c)
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	quiet, err := c.SaveNotificationSilence(t.Context(), "silence_global", NotificationSilenceInput{ScopeType: "global", Reason: "first window"}, 0, "operator")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := c.PreviewNotificationRouting(t.Context(), NotificationPreviewInput{IssueID: rows[0].Event.IssueID})
	if err != nil || !preview.GloballySilenced || len(preview.Matches) != 0 {
		t.Fatal(preview, err)
	}
	// Re-route the still-active issue so it acquires a durable deferred reminder.
	flushRoutes(t, c)
	quiet.ExpiresAt = c.now().Add(3 * time.Hour)
	if _, err = c.SaveNotificationSilence(t.Context(), quiet.ID, quiet.NotificationSilenceInput, quiet.Revision, "operator"); err != nil {
		t.Fatal(err)
	}
	if err = c.releaseSilencedNotifications(t.Context()); err != nil {
		t.Fatal(err)
	}
	due, err := c.store.ListNotificationWork(t.Context(), notificationSuppressionKind, c.now().Add(2*time.Hour), nil, 20)
	if err != nil || len(due) != 0 {
		t.Fatal("extended maintenance released early", due, err)
	}
	due, err = c.store.ListNotificationWork(t.Context(), notificationSuppressionKind, c.now().Add(4*time.Hour), nil, 20)
	if err != nil || len(due) != 1 {
		t.Fatal("extended reminder was lost", due, err)
	}
	var value notificationSuppression
	if err = json.Unmarshal(due[0].Payload, &value); err != nil || !value.NextAttempt.Equal(quiet.ExpiresAt) {
		t.Fatal(value, err)
	}
	for _, in := range []NotificationReceiverInput{{Name: "name", Enabled: true}, {Name: "name", Secret: ptrNotice(strings.Repeat("a", 257))}, {Name: "name", Endpoint: ptrNotice("https://127.0.0.1/private")}} {
		if _, err = c.SaveNotificationReceiver(t.Context(), "receiver_invalid", in, 0); err == nil {
			t.Fatal("invalid receiver accepted")
		}
	}
	if err = c.TestNotificationReceiver(t.Context(), "receiver_missing", 0); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
}
