package core

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/notification"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestNotificationPolicyChangesAfterClaimCannotSendStaleTeamPayload(t *testing.T) {
	for _, change := range []string{"hold", "resolved", "route", "silence", "scope", "unreadable-route", "unreadable-health"} {
		t.Run(change, func(t *testing.T) {
			c, _ := routedFixture(t)
			r := routedReceiver(t, c, "receiver_team")
			rule := routedRule(t, c, "environment", "env-1", r.ID)
			routedProblem(t, c, true)
			flushRoutes(t, c)
			var row Notification
			for _, v := range notices(t, c) {
				if v.ReceiverID == r.ID {
					row = v
				}
			}
			base := c.store
			c.store = notificationAtomicHook{Store: base, afterClaim: func() {
				c.store = base
				switch change {
				case "hold":
					if err := base.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: storage.ControlDocument{ID: storage.RecoveryHoldID("notification", row.ID), Kind: "recovery-hold", Payload: []byte(`{}`), CreatedAt: c.now(), UpdatedAt: c.now()}}}); err != nil {
						t.Fatal(err)
					}
				case "resolved":
					routedProblem(t, c, false)
				case "route":
					rule.Enabled = false
					if _, err := c.SaveNotificationRoute(t.Context(), rule.NotificationRouteInput, rule.Revision); err != nil {
						t.Fatal(err)
					}
				case "silence":
					if _, err := c.SaveNotificationSilence(t.Context(), "silence_claim", NotificationSilenceInput{ScopeType: "issue", ScopeID: row.Event.IssueID, Reason: "work"}, 0, "operator"); err != nil {
						t.Fatal(err)
					}
				case "scope":
					env, err := c.GetEnvironment(t.Context(), "env-1")
					if err != nil {
						t.Fatal(err)
					}
					env.RuntimeID = "other"
					if err = c.saveControl(t.Context(), env.ID, "environment", env.ProjectID, env, env.Revision); err != nil {
						t.Fatal(err)
					}
				case "unreadable-route":
					c.store = routingBoundaryStore{Store: base, get: rule.ID}
				case "unreadable-health":
					c.store = routingBoundaryStore{Store: base, get: row.Event.IssueID}
				}
			}}
			sender := &recordingSender{result: notification.Result{Delivered: true}}
			err := (NotificationWorker{Core: c, Sender: sender}).deliverRouted(t.Context(), row)
			if len(sender.events) != 0 {
				t.Fatal("stale payload was sent", sender.events)
			}
			if change == "unreadable-route" || change == "unreadable-health" {
				if !errors.Is(err, errNoticeStore) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			c.store = base
			latest, _, err := readControl[Notification](t.Context(), base, row.ID, notificationDeliveryKind)
			if err != nil {
				t.Fatal(err)
			}
			if change == "hold" || change == "unreadable-route" || change == "unreadable-health" {
				if latest.Status != "SENDING" || latest.LeaseUntil.IsZero() {
					t.Fatal("uncertain claimed attempt was consumed", latest)
				}
			} else if latest.Status != "CANCELLED" {
				t.Fatal(latest)
			}
		})
	}
}

func TestNotificationCurrentIssueCanBecomeGloballyOwnedWithoutLosingAlert(t *testing.T) {
	c, _ := routedFixture(t)
	r := routedReceiver(t, c, "receiver_team")
	routedRule(t, c, "project", "project-1", r.ID)
	routedRule(t, c, "project", "project-2", r.ID)
	routedProblem(t, c, true)
	flushRoutes(t, c)
	for _, id := range []string{"env-1", "env-2"} {
		env, err := c.GetEnvironment(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		env.RuntimeID = "other"
		if err = c.saveControl(t.Context(), env.ID, "environment", env.ProjectID, env, env.Revision); err != nil {
			t.Fatal(err)
		}
	}
	flushRoutes(t, c)
	rows := notices(t, c)
	global := 0
	for _, row := range rows {
		if row.ReceiverID == "default" {
			global++
			if len(row.Event.Scopes) != 0 {
				t.Fatal("former team scope leaked", row)
			}
		}
	}
	if global != 1 {
		t.Fatal("current globally owned issue was lost", rows)
	}
	flushRoutes(t, c)
	if len(notices(t, c)) != len(rows) {
		t.Fatal("ownership refresh replayed alert")
	}
}

func TestNotificationMigrationPreservesPreexistingCanonicalEventAndUnknownHistory(t *testing.T) {
	for _, history := range []string{"active", "recovered", "unknown-sequence", "different-sequence"} {
		t.Run(history, func(t *testing.T) {
			c, settings := notificationFixture(t, "runtimes")
			e := NotificationEvent{ID: settingsEventSince(t, c), Type: "runtime.problem", ResourceID: "legacy-target"}
			if err := c.observeNotification(t.Context(), e, true, settings.Revision); err != nil {
				t.Fatal(err)
			}
			row := notices(t, c)[0]
			key := "notice-state:" + digestString(e.ID+":"+e.ResourceID)
			state, doc, err := readControl[struct {
				Bad      bool  `json:"bad"`
				Sequence int64 `json:"sequence"`
			}](t.Context(), c.store, key, "notification-state")
			if err != nil {
				t.Fatal(err)
			}
			switch history {
			case "active":
				canonical := notificationEventRecord{Event: row.Event, EventKey: digestString(key + ":1"), Status: "PENDING", NextAttempt: c.now(), CapturedAt: c.now()}
				if err = c.saveControl(t.Context(), "nevent_"+canonical.EventKey, notificationEventKind, "", canonical, 0); err != nil {
					t.Fatal(err)
				}
			case "recovered":
				state.Bad = false
			case "unknown-sequence":
				state.Sequence = 0
			case "different-sequence":
				state.Sequence = 2
			}
			if err = c.saveControl(t.Context(), key, "notification-state", "", state, doc.Revision); err != nil {
				t.Fatal(err)
			}
			if err = c.initializeNotificationRouting(t.Context()); err != nil {
				t.Fatal(err)
			}
			latest := notices(t, c)[0]
			before, _ := json.Marshal(row.Event)
			after, _ := json.Marshal(latest.Event)
			if string(before) != string(after) || latest.ID != row.ID || !latest.Legacy {
				t.Fatal("migration rewrote signed history", latest)
			}
			pointers, err := c.store.ListControlDocuments(t.Context(), notificationCurrentKind, "", storage.Page{Limit: 20})
			if err != nil {
				t.Fatal(err)
			}
			if history != "active" && len(pointers) != 0 {
				t.Fatal("historical occurrence became active", pointers)
			}
		})
	}
}

func TestNotificationInitialRoutingEvidenceFailureDoesNotFabricateReadiness(t *testing.T) {
	c, _ := notificationFixture(t, "runtimes")
	base := c.store
	c.store = routingBoundaryStore{Store: base, get: notificationRoutingKey}
	if err := c.initializeNotificationRouting(t.Context()); !errors.Is(err, errNoticeStore) {
		t.Fatal(err)
	}
	c.store = base
	if _, err := base.GetControlDocument(t.Context(), notificationRoutingKey); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("failed bootstrap became visible", err)
	}
	// Fresh disabled installations have no historical cutoff. Initializing them
	// uses the current time and must not replay older activity when enabled.
	settings, doc, err := c.notificationSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	settings.Since = time.Time{}
	write, err := c.encryptedWrite(notificationSettingsKey, notificationSettingsKind, "", settings, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = base.WriteControlDocuments(t.Context(), []storage.ControlWrite{write}); err != nil {
		t.Fatal(err)
	}
	if err = c.initializeNotificationRouting(t.Context()); err != nil {
		t.Fatal(err)
	}
	state, _, err := c.notificationRoutingState(t.Context())
	if err != nil || !state.Initialized || !state.Since.Equal(c.now()) {
		t.Fatal(state, err)
	}
}

func TestNotificationCurrentPointerCorruptionRemainsVisibleWithoutPartialFanout(t *testing.T) {
	for _, corruption := range []string{"pointer", "event", "ownership", "fanout"} {
		t.Run(corruption, func(t *testing.T) {
			c, _ := routedFixture(t)
			routedProblem(t, c, true)
			flushRoutes(t, c)
			before := len(notices(t, c))
			row := notices(t, c)[0]
			pointerKey := "notice-current:" + digestString(row.Event.IssueID)
			pointer, doc, err := readControl[notificationCurrent](t.Context(), c.store, pointerKey, notificationCurrentKind)
			if err != nil {
				t.Fatal(err)
			}
			base := c.store
			switch corruption {
			case "pointer":
				if err = c.saveControl(t.Context(), doc.ID, doc.Kind, "", map[string]any{"event_id": 12}, doc.Revision); err != nil {
					t.Fatal(err)
				}
			case "event":
				event, e := base.GetControlDocument(t.Context(), pointer.EventID)
				if e != nil {
					t.Fatal(e)
				}
				if _, err = base.(*storage.SQLStore).DB().ExecContext(t.Context(), `UPDATE control_documents SET payload='{"event":12}' WHERE id=?`, event.ID); err != nil {
					t.Fatal(err)
				}
			case "ownership":
				c.store = routingBoundaryStore{Store: base, list: "environment"}
			case "fanout":
				routedReceiver(t, c, "receiver_new")
				routedRule(t, c, "project", "project-1", "receiver_new")
				c.store = routingBoundaryStore{Store: base, write: notificationDeliveryKind}
			}
			state, _, err := c.notificationRoutingState(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err = c.rerouteCurrentNotifications(t.Context(), state.Generation+1); err == nil {
				t.Fatal("invalid active evidence was ignored")
			}
			c.store = base
			if len(notices(t, c)) != before {
				t.Fatal("failed current refresh partially published deliveries")
			}
		})
	}
}
