package core

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/notification"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestNotificationDispatcherWaitsForQueueEligibilityAndMigration(t *testing.T) {
	for _, state := range []string{"recovery", "already-delivered", "lease-held", "backoff", "unmigrated", "migration-incomplete"} {
		t.Run(state, func(t *testing.T) {
			c, _ := routedFixture(t)
			r := routedReceiver(t, c, "receiver_team")
			if err := c.TestNotificationReceiver(t.Context(), r.ID, r.Revision); err != nil {
				t.Fatal(err)
			}
			row := notices(t, c)[0]
			switch state {
			case "recovery":
				c.config.Controller.RecoveryMode = true
			case "already-delivered":
				row.Status = "DELIVERED"
			case "lease-held":
				row.Status = "SENDING"
				row.LeaseUntil = c.now().Add(time.Minute)
			case "backoff":
				row.NextAttempt = c.now().Add(time.Minute)
			case "unmigrated":
				row.ReceiverID = ""
			case "migration-incomplete":
				row.DirectTest = false
				value, doc, err := c.notificationRoutingState(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				value.Initialized = false
				if err = c.saveControl(t.Context(), doc.ID, doc.Kind, "", value, doc.Revision); err != nil {
					t.Fatal(err)
				}
			}
			sender := &recordingSender{result: notification.Result{Delivered: true}}
			if err := (NotificationWorker{Core: c, Sender: sender}).deliverRouted(t.Context(), row); err != nil || len(sender.events) != 0 {
				t.Fatal("ineligible work was dispatched", state, err)
			}
			if stored := notices(t, c)[0]; stored.Attempts != 0 {
				t.Fatal("ineligible work consumed attempt", stored)
			}
		})
	}
}

func TestNotificationLegacyQueuedPolicyChangesCancelUntilExplicitReview(t *testing.T) {
	c, settings := notificationFixture(t, "runtimes")
	if err := c.observeNotification(t.Context(), NotificationEvent{ID: settingsEventSince(t, c), Type: "runtime.problem", ResourceID: "legacy-target"}, true, settings.Revision); err != nil {
		t.Fatal(err)
	}
	if err := c.initializeNotificationRouting(t.Context()); err != nil {
		t.Fatal(err)
	}
	routedReceiver(t, c, "receiver_new")
	sender := &recordingSender{result: notification.Result{Delivered: true}}
	w := NotificationWorker{Core: c, Sender: sender}
	row := notices(t, c)[0]
	if !row.Legacy {
		t.Fatal(row)
	}
	if err := w.deliverRouted(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	row = notices(t, c)[0]
	if row.Status != "CANCELLED" || row.ErrorCode != "route_changed" || len(sender.events) != 0 {
		t.Fatal("legacy queued payload bypassed route review", row)
	}
	if _, err := c.RetryNotification(t.Context(), row.ID, row.Revision); err != nil {
		t.Fatal("explicit legacy retry could not be reviewed", err)
	}
	if err := w.deliveryPass(t.Context()); err != nil || len(sender.events) != 1 {
		t.Fatal("explicit retry did not use preserved payload", err)
	}
}

func TestNotificationCollectorHealthFailureIsVisibleAndDoesNotRewriteSuccessfulEvidence(t *testing.T) {
	for _, dependency := range []string{"routing-health", "source-health", "route-directory", "source-outage"} {
		t.Run(dependency, func(t *testing.T) {
			c, _ := routedFixture(t)
			base := c.store
			if err := c.saveNotificationHealth(t.Context(), "notice-source:runtimes", notificationCollectionHealth{Group: "runtimes", AttemptedAt: c.now(), SucceededAt: ptrNoticeTime(c.now())}, 0); err != nil {
				t.Fatal(err)
			}
			// Two collectors can finish the same cycle. A stale health write must
			// not replace the newer observation or make the actor fail repeatedly.
			if err := c.saveNotificationHealth(t.Context(), "notice-source:runtimes", notificationCollectionHealth{Group: "runtimes", Failed: true}, 0); err != nil {
				t.Fatal(err)
			}
			switch dependency {
			case "routing-health":
				c.store = routingBoundaryStore{Store: base, get: "notice-source:routing"}
			case "source-health":
				c.store = routingBoundaryStore{Store: base, get: "notice-source:runtimes"}
			case "route-directory":
				c.store = routingBoundaryStore{Store: base, list: notificationRouteKind}
			case "source-outage":
				c.store = collectionCounterStore{Store: base, count: &atomic.Int64{}}
			}
			if err := (NotificationWorker{Core: c}).routeTick(t.Context()); !errors.Is(err, errNoticeStore) {
				t.Fatal("collection outage looked healthy", err)
			}
			c.store = base
			previous, _, err := readControl[notificationCollectionHealth](t.Context(), base, "notice-source:runtimes", "notification-source-health")
			if err != nil || previous.SucceededAt == nil || !previous.SucceededAt.Equal(c.now()) {
				t.Fatal("last successful evidence was discarded", previous, err)
			}
			if dependency == "source-outage" && !previous.Failed {
				t.Fatal("source outage was hidden", previous)
			}
		})
	}
}

func ptrNoticeTime(value time.Time) *time.Time { return &value }

func TestNotificationDefaultSenderRejectsCorruptPrivateEndpointWithoutNetwork(t *testing.T) {
	c, _ := routedFixture(t)
	r := routedReceiver(t, c, "receiver_team")
	v, doc, err := c.notificationReceiver(t.Context(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	v.Endpoint = "https://127.0.0.1/private"
	write, err := c.encryptedWrite(doc.ID, notificationReceiverKind, "", v, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.store.WriteControlDocuments(t.Context(), []storage.ControlWrite{write}); err != nil {
		t.Fatal(err)
	}
	if err = c.TestNotificationReceiver(t.Context(), r.ID, r.Revision+1); err != nil {
		t.Fatal(err)
	}
	if err = (NotificationWorker{Core: c}).deliveryPass(t.Context()); err != nil {
		t.Fatal(err)
	}
	row := notices(t, c)[0]
	if row.Status != "FAILED" || row.DeliveredAt != nil || row.ErrorCode == "" {
		t.Fatal("private destination was accepted", row)
	}
}

type noticeConflictStore struct{ storage.Store }

func (s noticeConflictStore) WriteControlDocuments(context.Context, []storage.ControlWrite) error {
	return storage.ErrPrecondition
}
func TestNotificationBootstrapConflictKeepsWorkerWaiting(t *testing.T) {
	c, _ := notificationFixture(t, "runtimes")
	base := c.store
	c.store = noticeConflictStore{base}
	if err := (NotificationWorker{Core: c}).routeCycle(t.Context()); err != nil {
		t.Fatal("initialization conflict was terminal", err)
	}
	c.store = base
	if state, _, err := c.notificationRoutingState(t.Context()); err != nil || state.Initialized {
		t.Fatal("failed bootstrap looked initialized", state, err)
	}
	c.config.Controller.RecoveryMode = true
	if err := (NotificationWorker{Core: c}).routeTick(t.Context()); err != nil {
		t.Fatal(err)
	}
}
