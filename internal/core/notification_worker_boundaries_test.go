package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/notification"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type collectionCounterStore struct {
	storage.Store
	count *atomic.Int64
}

func (s collectionCounterStore) ListRuntimes(context.Context, storage.Page) ([]model.Runtime, error) {
	s.count.Add(1)
	return nil, errNoticeStore
}

func TestNotificationRunningActorCollectsWhileFourReceiversAreBlocked(t *testing.T) {
	c, _ := routedFixture(t)
	for i := 0; i < 5; i++ {
		r := routedReceiver(t, c, fmt.Sprintf("receiver_%d", i))
		if err := c.TestNotificationReceiver(t.Context(), r.ID, r.Revision); err != nil {
			t.Fatal(err)
		}
	}
	var counter atomic.Int64
	c.store = collectionCounterStore{Store: c.store, count: &counter}
	sender := &laneSender{entered: make(chan string, 10), release: make(chan struct{}), active: map[string]int{}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (NotificationWorker{Core: c, Sender: sender}).Run(ctx) }()
	for i := 0; i < 4; i++ {
		select {
		case <-sender.entered:
		case <-time.After(8 * time.Second):
			t.Fatal("actor did not start four receivers")
		}
	}
	deadline := time.Now().Add(8 * time.Second)
	for counter.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if counter.Load() < 2 {
		t.Fatal("blocked receiver stopped periodic collection")
	}
	select {
	case <-sender.entered:
		t.Fatal("fifth request exceeded global concurrency")
	default:
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("actor did not drain cancelled sends")
	}
	if sender.maximum != 4 || sender.violation {
		t.Fatal(sender.maximum, sender.violation)
	}
	for _, row := range notices(t, c) {
		if row.Status == "DELIVERED" {
			t.Fatal("cancelled send fabricated an acceptance receipt")
		}
	}
}

func TestNotificationReceiverPolicyDependencyFailuresNeverSend(t *testing.T) {
	for _, boundary := range []string{"recovery-barrier", "receiver", "routing-state", "issue", "ownership", "routes", "silences"} {
		t.Run(boundary, func(t *testing.T) {
			c, _ := routedFixture(t)
			routedProblem(t, c, true)
			flushRoutes(t, c)
			row := notices(t, c)[0]
			base := c.store
			switch boundary {
			case "recovery-barrier":
				c.store = routingBoundaryStore{Store: base, get: storage.RecoveryBarrierID}
			case "receiver":
				c.store = routingBoundaryStore{Store: base, get: notificationSettingsKey}
			case "routing-state":
				c.store = routingBoundaryStore{Store: base, get: notificationRoutingKey}
			case "issue":
				c.store = routingBoundaryStore{Store: base, get: row.Event.IssueID}
			case "ownership":
				c.store = routingBoundaryStore{Store: base, list: "environment"}
			case "routes":
				c.store = routingBoundaryStore{Store: base, get: notificationRouteID("global", "")}
			case "silences":
				c.store = routingBoundaryStore{Store: base, list: notificationSilenceKind}
			}
			sender := &recordingSender{result: notification.Result{Delivered: true}}
			if err := (NotificationWorker{Core: c, Sender: sender}).deliveryPass(t.Context()); !errors.Is(err, errNoticeStore) || len(sender.events) != 0 {
				t.Fatal("unavailable authority permitted send", err)
			}
			c.store = base
			if latest := notices(t, c)[0]; latest.Status != "PENDING" || latest.Attempts != 0 {
				t.Fatal("failed policy advanced the receipt", latest)
			}
		})
	}
}

func TestNotificationMigrationRestartAndActivityHistory(t *testing.T) {
	c, settings := notificationFixture(t, "images", "deployments")
	for i := 0; i < 205; i++ {
		event := NotificationEvent{ID: fmt.Sprintf("legacy-%03d", i), Type: "notification.test", ResourceID: "global", OccurredAt: c.now()}
		switch i {
		case 0:
			event.ID, event.Type, event.ResourceID = "candidate:available", "image.available", "candidate"
		case 1:
			event.ID, event.Type, event.ResourceID = "candidate:submission-failed", "image.failed", "candidate"
		case 2:
			event.ID, event.Type, event.OperationID, event.Status = "operation:SUCCEEDED", "deployment.succeeded", "operation", "SUCCEEDED"
		case 3:
			event.ID, event.Type, event.ResourceID = "unknown-history", "custom.activity", "fixture"
		}
		if err := c.enqueueNotification(t.Context(), event, settings.Revision); err != nil {
			t.Fatal(err)
		}
	}
	base := c.store
	c.store = routingBoundaryStore{Store: base, list: notificationDeliveryKind}
	if err := c.initializeNotificationRouting(t.Context()); !errors.Is(err, errNoticeStore) {
		t.Fatal(err)
	}
	c.store = base
	state, _, err := c.notificationRoutingState(t.Context())
	if err != nil || state.Initialized {
		t.Fatal("partial migration looked complete", state, err)
	}
	if err = c.initializeNotificationRouting(t.Context()); err != nil {
		t.Fatal(err)
	}
	page := storage.Page{Limit: 200}
	total := 0
	for {
		rows, err := c.ListNotifications(t.Context(), page)
		if err != nil {
			t.Fatal(err)
		}
		total += len(rows)
		for _, row := range rows {
			if !row.Legacy || row.ReceiverID != "default" {
				t.Fatal(row)
			}
		}
		if len(rows) < 200 {
			break
		}
		page.After = rows[len(rows)-1].ID
	}
	if total != 205 {
		t.Fatal("migration pagination lost history", total)
	}
	if err = c.initializeNotificationRouting(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationMigrationUnreadableStateAndForeignAliasesFailClosed(t *testing.T) {
	for _, boundary := range []string{"settings", "metadata-write", "state", "ownership", "issue", "event-alias", "pointer", "history-write"} {
		t.Run(boundary, func(t *testing.T) {
			c, settings := notificationFixture(t, "runtimes")
			e := NotificationEvent{ID: settingsEventSince(t, c), Type: "runtime.problem", ResourceID: "runtime-legacy", Status: "OFFLINE"}
			if err := c.observeNotification(t.Context(), e, true, settings.Revision); err != nil {
				t.Fatal(err)
			}
			legacy := notices(t, c)[0]
			base := c.store
			stateKey := "notice-state:" + digestString(e.ID+":"+e.ResourceID)
			eventKey := digestString(stateKey + ":1")
			switch boundary {
			case "settings":
				c.store = routingBoundaryStore{Store: base, get: notificationSettingsKey}
			case "metadata-write":
				c.store = routingBoundaryStore{Store: base, write: notificationRoutingKind}
			case "state":
				c.store = routingBoundaryStore{Store: base, get: stateKey}
			case "ownership":
				c.store = routingBoundaryStore{Store: base, list: "environment"}
			case "issue":
				c.store = routingBoundaryStore{Store: base, get: legacy.Event.IssueID}
			case "event-alias":
				c.store = routingBoundaryStore{Store: base, get: "nevent_" + eventKey}
			case "pointer":
				c.store = routingBoundaryStore{Store: base, get: "notice-current:" + digestString(legacy.Event.IssueID)}
			case "history-write":
				c.store = routingBoundaryStore{Store: base, write: notificationDeliveryKind}
			}
			if err := c.initializeNotificationRouting(t.Context()); !errors.Is(err, errNoticeStore) {
				t.Fatal(err)
			}
			c.store = base
			if latest := notices(t, c)[0]; latest.Event.ID != legacy.Event.ID || latest.ReceiverID != "" {
				t.Fatal("failed migration rewrote history", latest)
			}
			if err := c.initializeNotificationRouting(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
	c, _ := routedFixture(t)
	if err := c.saveControl(t.Context(), "foreign-event", "fixture", "", map[string]string{"value": "kept"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.notificationEventExists(t.Context(), "foreign-event"); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("foreign alias accepted", err)
	}
	if exists, err := c.notificationEventExists(t.Context(), "missing-event"); err != nil || exists {
		t.Fatal(exists, err)
	}
}

func TestNotificationRecoveryAndUnknownIssuePreventDispatchOrReroute(t *testing.T) {
	c, _ := routedFixture(t)
	routedProblem(t, c, true)
	flushRoutes(t, c)
	row := notices(t, c)[0]
	hold := storage.ControlDocument{ID: storage.RecoveryHoldID("notification", row.ID), Kind: "recovery-hold", ParentID: row.ID, Payload: json.RawMessage(`{}`), CreatedAt: c.now(), UpdatedAt: c.now()}
	if err := c.store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: hold}}); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{result: notification.Result{Delivered: true}}
	worker := NotificationWorker{Core: c, Sender: sender}
	if err := worker.deliverRouted(t.Context(), row); err != nil || len(sender.events) != 0 {
		t.Fatal("hold bypassed at execution boundary", err)
	}
	issue, doc, err := readControl[Issue](t.Context(), c.store, row.Event.IssueID, "issue")
	if err != nil {
		t.Fatal(err)
	}
	issue.ObservationState = "UNMONITORED"
	if err = c.saveControl(t.Context(), doc.ID, "issue", doc.ParentID, issue, doc.Revision); err != nil {
		t.Fatal(err)
	}
	if active, err := c.notificationStillActive(t.Context(), row.Event); err != nil || active {
		t.Fatal("unmonitored issue was replayable", active, err)
	}
	unknown := row.Event
	unknown.IssueID = "missing"
	if active, err := c.notificationStillActive(t.Context(), unknown); err != nil || active {
		t.Fatal(active, err)
	}
	c.config.Controller.RecoveryMode = true
	if err := worker.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := worker.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type notificationAtomicFailure struct {
	storage.Store
	fail atomic.Bool
}

func disableRoutingCollection(t *testing.T, c *Core) {
	t.Helper()
	rule, err := c.globalNotificationRoute(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rule.Enabled = false
	rule.Events = []string{}
	rule.ReceiverIDs = []string{}
	if _, err = c.SaveNotificationRoute(t.Context(), rule.NotificationRouteInput, rule.Revision); err != nil {
		t.Fatal(err)
	}
}

func (s *notificationAtomicFailure) WriteControlDocuments(ctx context.Context, writes []storage.ControlWrite) error {
	if s.fail.Load() {
		return errNoticeStore
	}
	return s.Store.WriteControlDocuments(ctx, writes)
}
func TestNotificationActorKeepsAcceptedButUnpersistedReceiptUncertain(t *testing.T) {
	c, _ := routedFixture(t)
	disableRoutingCollection(t, c)
	receiver := routedReceiver(t, c, "receiver_team")
	if err := c.TestNotificationReceiver(t.Context(), receiver.ID, receiver.Revision); err != nil {
		t.Fatal(err)
	}
	store := &notificationAtomicFailure{Store: c.store}
	c.store = store
	accepted := make(chan struct{})
	sender := &recordingSender{result: notification.Result{Delivered: true}, block: func() { store.fail.Store(true); close(accepted) }}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (NotificationWorker{Core: c, Sender: sender}).Run(ctx) }()
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("background actor did not send")
	}
	// Let the failed completion reach the actor's completion queue.
	time.Sleep(30 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	store.fail.Store(false)
	row := notices(t, c)[0]
	if row.Status != "SENDING" || row.DeliveredAt != nil {
		t.Fatal("unpersisted acceptance became success", row)
	}
}

func TestNotificationActorQueueOutageAndCorruptLaneDoNotStopService(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			c, _ := routedFixture(t)
			disableRoutingCollection(t, c)
			receiver := routedReceiver(t, c, "receiver_team")
			other := routedReceiver(t, c, "receiver_other")
			if err := c.TestNotificationReceiver(t.Context(), receiver.ID, receiver.Revision); err != nil {
				t.Fatal(err)
			}
			if err := c.TestNotificationReceiver(t.Context(), other.ID, other.Revision); err != nil {
				t.Fatal(err)
			}
			base := c.store
			if corrupt {
				for _, row := range notices(t, c) {
					if row.ReceiverID == receiver.ID {
						if _, err := base.(*storage.SQLStore).DB().ExecContext(t.Context(), `UPDATE control_documents SET payload='{' WHERE id=?`, row.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
			} else {
				c.store = routingBoundaryStore{Store: base, queued: notificationDeliveryKind}
			}
			sent := make(chan struct{}, 1)
			sender := &recordingSender{result: notification.Result{Delivered: true}, block: func() { sent <- struct{}{} }}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- (NotificationWorker{Core: c, Sender: sender}).Run(ctx) }()
			if corrupt {
				select {
				case <-sent:
				case <-time.After(5 * time.Second):
					t.Fatal("corrupt receiver blocked another receiver")
				}
			} else {
				time.Sleep(30 * time.Millisecond)
			}
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if !corrupt {
				c.store = base
				for _, row := range notices(t, c) {
					if row.Status != "PENDING" {
						t.Fatal("queue outage changed delivery state", row)
					}
				}
			}
		})
	}
}

func TestNotificationCustomRoutingWorksWithoutLegacyCredentials(t *testing.T) {
	c, _, _ := controlFixture(t)
	receiver := routedReceiver(t, c, "receiver_team")
	rule := routedRule(t, c, "global", "", receiver.ID)
	if len(rule.ReceiverIDs) != 1 {
		t.Fatal(rule)
	}
	worker := NotificationWorker{Core: c, Sender: &recordingSender{result: notification.Result{Delivered: true}}}
	if err := worker.routeTick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := c.TestNotificationReceiver(t.Context(), receiver.ID, receiver.Revision); err != nil {
		t.Fatal(err)
	}
	if err := worker.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := worker.deliveryPass(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows := notices(t, c)
	testDelivered := false
	for _, row := range rows {
		if row.DirectTest && row.ReceiverID == receiver.ID && row.Status == "DELIVERED" {
			testDelivered = true
		}
	}
	if !testDelivered {
		t.Fatal("saved custom receiver did not deliver its test")
	}
	settings, err := c.GetNotificationSettings(t.Context())
	if err != nil || settings.Enabled || settings.EndpointConfigured {
		t.Fatal("custom channel depended on legacy credentials", settings, err)
	}
	if _, err = c.SaveNotificationReceiver(t.Context(), "default", NotificationReceiverInput{Enabled: false}, settings.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = c.SaveNotificationReceiver(t.Context(), "default", NotificationReceiverInput{Enabled: true}, settings.Revision); err == nil {
		t.Fatal("default receiver enabled without credentials")
	}
	if err = c.saveControl(t.Context(), notificationRouteID("project", "invalid"), notificationRouteKind, "invalid", NotificationRoute{NotificationRouteInput: NotificationRouteInput{ScopeType: "project", ScopeID: "invalid", Enabled: true, Events: []string{"unsupported"}}}, 0); err != nil {
		t.Fatal(err)
	}
	if err = worker.routeTick(t.Context()); err == nil {
		t.Fatal("corrupt event group was accepted")
	}
}
