package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/notification"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestNotificationDeferredOwnershipAndObservationFailuresRemainPending(t *testing.T) {
	for _, scenario := range []string{"missing-issue", "issue-read", "current-read", "current-list", "pending-list", "suppression-list", "suppression-write", "silence-read", "state-read", "global-hold"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := routedFixture(t)
			quiet, err := c.SaveNotificationSilence(t.Context(), "silence_deferred", NotificationSilenceInput{ScopeType: "global", Reason: "work"}, 0, "operator")
			if err != nil {
				t.Fatal(err)
			}
			routedProblem(t, c, true)
			flushRoutes(t, c)
			due, err := c.store.ListNotificationWork(t.Context(), notificationSuppressionKind, c.now().Add(2*time.Hour), nil, 20)
			if err != nil || len(due) != 2 {
				t.Fatal(due, err)
			}
			var suppression notificationSuppression
			if err := json.Unmarshal(due[0].Payload, &suppression); err != nil {
				t.Fatal(err)
			}
			if _, err := c.CancelNotificationSilence(t.Context(), quiet.ID, quiet.Revision, "operator"); err != nil {
				t.Fatal(err)
			}
			base := c.store
			switch scenario {
			case "missing-issue":
				doc, _ := base.GetControlDocument(t.Context(), suppression.Event.IssueID)
				if err := base.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: doc, Expected: doc.Revision, Remove: true}}); err != nil {
					t.Fatal(err)
				}
			case "issue-read":
				c.store = routingBoundaryStore{Store: base, get: suppression.Event.IssueID}
			case "current-read":
				pointer, _, e := readControl[notificationCurrent](t.Context(), base, "notice-current:"+digestString(suppression.Event.IssueID), notificationCurrentKind)
				if e != nil {
					t.Fatal(e)
				}
				c.store = routingBoundaryStore{Store: base, get: pointer.EventID}
			case "current-list":
				c.store = routingBoundaryStore{Store: base, list: notificationCurrentKind}
			case "pending-list":
				c.store = routingBoundaryStore{Store: base, queued: notificationEventKind}
			case "suppression-list":
				c.store = routingBoundaryStore{Store: base, queued: notificationSuppressionKind}
			case "suppression-write":
				c.store = routingBoundaryStore{Store: base, write: notificationSuppressionKind}
			case "silence-read":
				c.store = routingBoundaryStore{Store: base, list: notificationSilenceKind}
			case "state-read":
				c.store = routingBoundaryStore{Store: base, get: notificationRoutingKey}
			case "global-hold":
				payload := []byte(`{"prepared":true}`)
				if err := base.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: storage.ControlDocument{ID: storage.RecoveryBarrierID, Kind: "recovery-barrier", Payload: payload, CreatedAt: c.now(), UpdatedAt: c.now()}}}); err != nil {
					t.Fatal(err)
				}
			}
			var runErr error
			switch scenario {
			case "current-list", "current-read":
				state, _, _ := c.notificationRoutingState(t.Context())
				runErr = c.rerouteCurrentNotifications(t.Context(), state.Generation+1)
			case "pending-list":
				runErr = c.routePendingNotifications(t.Context(), 1)
			case "state-read", "global-hold":
				runErr = (NotificationWorker{Core: c}).routeTick(t.Context())
			default:
				runErr = c.releaseSilencedNotifications(t.Context())
			}
			if scenario == "missing-issue" || scenario == "global-hold" {
				if runErr != nil {
					t.Fatal(runErr)
				}
			} else if !errors.Is(runErr, errNoticeStore) {
				t.Fatal("unavailable observation was ignored", runErr)
			}
			c.store = base
			if len(notices(t, c)) != 0 {
				t.Fatal("deferred fault was sent without current evidence")
			}
			if scenario == "missing-issue" {
				due, _ = base.ListNotificationWork(t.Context(), notificationSuppressionKind, c.now().Add(2*time.Hour), nil, 20)
				if len(due) != 0 {
					t.Fatal("deleted issue retained a reminder", due)
				}
			}
		})
	}
}

func TestNotificationAllDurableSlotsBusyAndLaneFailuresPreserveQueue(t *testing.T) {
	for _, scenario := range []string{"all-slots-busy", "lane-unreadable", "slot-unreadable", "claim-unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := routedFixture(t)
			r := routedReceiver(t, c, "receiver_team")
			if err := c.TestNotificationReceiver(t.Context(), r.ID, r.Revision); err != nil {
				t.Fatal(err)
			}
			row := notices(t, c)[0]
			base := c.store
			switch scenario {
			case "all-slots-busy":
				for i := 0; i < 4; i++ {
					if err := c.saveControl(t.Context(), fmt.Sprintf("notice-slot:%d", i), "notification-slot", "", notificationLane{Owner: "other", Until: c.now().Add(time.Minute)}, 0); err != nil {
						t.Fatal(err)
					}
				}
			case "lane-unreadable":
				c.store = routingBoundaryStore{Store: base, get: "notice-lane:" + digestString(row.ReceiverID)}
			case "slot-unreadable":
				c.store = routingBoundaryStore{Store: base, get: "notice-slot:0"}
			case "claim-unavailable":
				c.store = routingBoundaryStore{Store: base, write: "notification-lane"}
			}
			sender := &recordingSender{result: notification.Result{Delivered: true}}
			err := (NotificationWorker{Core: c, Sender: sender}).deliverRouted(t.Context(), row)
			if scenario == "all-slots-busy" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, errNoticeStore) {
				t.Fatal(err)
			}
			c.store = base
			if latest := notices(t, c)[0]; latest.Status != "PENDING" || latest.Attempts != 0 || len(sender.events) != 0 {
				t.Fatal("unavailable execution lane consumed task", latest)
			}
		})
	}
}

type noticeProjectionStore struct {
	storage.Store
	doc storage.ControlDocument
}

func (s noticeProjectionStore) ListNotificationWork(context.Context, string, time.Time, []string, int) ([]storage.ControlDocument, error) {
	return []storage.ControlDocument{s.doc}, nil
}

func TestNotificationMalformedQueueCannotDispatchOrConsumeSource(t *testing.T) {
	c, _ := routedFixture(t)
	base := c.store
	for _, kind := range []string{notificationDeliveryKind, notificationEventKind, notificationSuppressionKind} {
		c.store = noticeProjectionStore{Store: base, doc: storage.ControlDocument{ID: "invalid", Kind: kind, Payload: []byte(`{`)}}
		var err error
		switch kind {
		case notificationDeliveryKind:
			err = (NotificationWorker{Core: c}).deliveryPass(t.Context())
		case notificationEventKind:
			err = c.routePendingNotifications(t.Context(), 1)
		case notificationSuppressionKind:
			err = c.releaseSilencedNotifications(t.Context())
		}
		if err == nil {
			t.Fatal("malformed durable work was accepted", kind)
		}
	}
	c.store = base
	if rows := notices(t, c); len(rows) != 0 {
		t.Fatal(rows)
	}
}
