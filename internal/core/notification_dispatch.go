package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/notification"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type notificationLane struct {
	Owner string    `json:"owner"`
	Until time.Time `json:"until"`
}

type notificationCollectionHealth struct {
	Group       string     `json:"group"`
	AttemptedAt time.Time  `json:"attempted_at"`
	SucceededAt *time.Time `json:"succeeded_at,omitempty"`
	Failed      bool       `json:"failed"`
	State       string     `json:"state"`
}

func (c *Core) saveNotificationHealth(ctx context.Context, key string, value notificationCollectionHealth, revision int64) error {
	err := c.saveControl(ctx, key, "notification-source-health", "", value, revision)
	if errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrPrecondition) {
		return nil
	}
	return err
}

func (w NotificationWorker) runRouted(ctx context.Context) error {
	if w.Core.config.Controller.RecoveryMode {
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := time.NewTicker(5 * time.Second)
		defer timer.Stop()
		for {
			if err := w.routeTick(ctx); err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx, "notification collection or routing failed")
			}
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
	}()
	// This actor owns the active lanes; HTTP workers report completion without
	// blocking collection. Persistent lane leases also fence competing actors.
	active := map[string]bool{}
	done := make(chan string, 4)
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		if len(active) < 4 {
			excluded := []string{}
			for receiver := range active {
				excluded = append(excluded, receiver)
			}
			docs, err := w.Core.store.ListNotificationWork(ctx, notificationDeliveryKind, w.Core.now(), excluded, 4-len(active))
			if err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx, "notification queue read failed")
			}
			for _, doc := range docs {
				var value Notification
				if err := json.Unmarshal(doc.Payload, &value); err != nil {
					slog.ErrorContext(ctx, "notification queue record is invalid")
					continue
				}
				value.ControlDocument = doc
				receiver := value.ReceiverID
				if receiver == "" {
					receiver = defaultNotificationReceiver
				}
				active[receiver] = true
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := w.deliverRouted(ctx, value); err != nil && ctx.Err() == nil && !errors.Is(err, storage.ErrPrecondition) && !errors.Is(err, storage.ErrConflict) {
						slog.ErrorContext(ctx, "notification delivery state could not be saved")
					}
					done <- receiver
				}()
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case receiver := <-done:
			delete(active, receiver)
		case <-timer.C:
		}
	}
}

func (w NotificationWorker) routeTick(ctx context.Context) error {
	c := w.Core
	if c.config.Controller.RecoveryMode {
		return nil
	}
	if held, err := storage.RecoveryHeld(ctx, c.store, "notification", ""); err != nil {
		return err
	} else if held {
		return nil
	}
	result := w.routeCycle(ctx)
	if ctx.Err() != nil {
		return result
	}
	value, doc, err := readControl[notificationCollectionHealth](ctx, c.store, "notice-source:routing", "notification-source-health")
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return errors.Join(result, err)
	}
	value.Group, value.AttemptedAt, value.Failed = "routing", c.now(), result != nil
	if result == nil {
		now := c.now()
		value.SucceededAt = &now
	}
	return errors.Join(result, c.saveNotificationHealth(ctx, "notice-source:routing", value, doc.Revision))
}

func (w NotificationWorker) routeCycle(ctx context.Context) error {
	c := w.Core
	state, err := c.readyNotificationRouting(ctx)
	if err != nil {
		return err
	}
	if !state.Initialized {
		return nil
	}
	routes, err := c.ListNotificationRoutes(ctx)
	if err != nil {
		return err
	}
	groups := []string{}
	for _, route := range routes {
		if !route.Enabled {
			continue
		}
		for _, group := range route.Events {
			if !slices.Contains(groups, group) {
				groups = append(groups, group)
			}
		}
	}
	var result error
	collectors := map[string]func(context.Context, time.Time, int64) error{"images": c.imageNotifications, "deployments": c.deploymentNotifications, "certificates": c.certificateNotifications, "gateways": c.gatewayNotifications, "runtimes": c.runtimeNotifications}
	for _, group := range groups {
		collector := collectors[group]
		if collector == nil {
			return controlInvalid("events", "stored route contains an unsupported event group")
		}
		collectErr := collector(ctx, state.Since, 0)
		previous, doc, readErr := readControl[notificationCollectionHealth](ctx, c.store, "notice-source:"+group, "notification-source-health")
		if readErr != nil && !errors.Is(readErr, storage.ErrNotFound) {
			result = errors.Join(result, readErr)
			continue
		}
		previous.Group, previous.AttemptedAt, previous.Failed = group, c.now(), collectErr != nil
		if collectErr == nil {
			now := c.now()
			previous.SucceededAt = &now
		}
		result = errors.Join(result, collectErr, c.saveNotificationHealth(ctx, "notice-source:"+group, previous, doc.Revision))
	}
	result = errors.Join(result, c.releaseSilencedNotifications(ctx))
	result = errors.Join(result, c.routePendingNotifications(ctx, state.Generation))
	return errors.Join(result, c.rerouteCurrentNotifications(ctx, state.Generation))
}

func (w NotificationWorker) deliveryPass(ctx context.Context) error {
	docs, err := w.Core.store.ListNotificationWork(ctx, notificationDeliveryKind, w.Core.now(), nil, 4)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	results := make(chan error, len(docs))
	for _, doc := range docs {
		var value Notification
		if err := json.Unmarshal(doc.Payload, &value); err != nil {
			return err
		}
		value.ControlDocument = doc
		wg.Add(1)
		go func() { defer wg.Done(); results <- w.deliverRouted(ctx, value) }()
	}
	wg.Wait()
	close(results)
	var result error
	for err := range results {
		if !errors.Is(err, storage.ErrPrecondition) && !errors.Is(err, storage.ErrConflict) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (c *Core) routedNotificationPolicy(ctx context.Context, value Notification) (notificationReceiver, string, error) {
	if held, err := storage.RecoveryHeld(ctx, c.store, "notification", value.ID); err != nil {
		return notificationReceiver{}, "", err
	} else if held {
		return notificationReceiver{}, "recovery_held", nil
	}
	receiver, doc, err := c.notificationReceiver(ctx, value.ReceiverID)
	if err != nil {
		return receiver, "", err
	}
	if !receiver.Enabled || value.SettingsRevision != doc.Revision {
		return receiver, "receiver_changed", nil
	}
	if value.DirectTest {
		return receiver, "", nil
	}
	state, _, err := c.notificationRoutingState(ctx)
	if err != nil {
		return receiver, "", err
	}
	if !state.Initialized {
		return receiver, "migration_pending", nil
	}
	if value.Legacy && !value.ManualRetry && state.Generation != value.RouteGeneration {
		return receiver, "route_changed", nil
	}
	active, err := c.notificationStillActive(ctx, value.Event)
	if err != nil || !active {
		return receiver, "issue_obsolete", err
	}
	event := value.Event
	if err := c.notificationScopes(ctx, &event); err != nil {
		return receiver, "", err
	}
	expectedScopes := value.Event.Scopes
	if value.Legacy {
		expectedScopes = value.LegacyScopes
	}
	for _, scope := range expectedScopes {
		if !slices.Contains(event.Scopes, scope) {
			return receiver, "ownership_changed", nil
		}
	}
	if len(expectedScopes) == 0 && len(event.Scopes) != 0 {
		return receiver, "ownership_changed", nil
	}
	notificationEventScopes(&event, slices.Clone(expectedScopes))
	if value.ManualRetry {
		event.OccurredAt = c.now()
	}
	// Recompute ownership, then intersect through the current route. A formerly
	// shared receipt cannot expose a removed or moved environment.
	matches, err := c.matchNotificationRoutes(ctx, event)
	if err != nil {
		return receiver, "", err
	}
	match := slices.IndexFunc(matches, func(v NotificationRouteMatch) bool { return v.ReceiverID == value.ReceiverID })
	if match < 0 {
		return receiver, "route_changed", nil
	}
	current := matches[match]
	if !slices.Equal(current.Scopes, expectedScopes) {
		return receiver, "ownership_changed", nil
	}
	if !value.Legacy && !slices.Equal(current.Routes, value.Routes) {
		return receiver, "route_changed", nil
	}
	silences, err := c.ListNotificationSilences(ctx)
	if err != nil {
		return receiver, "", err
	}
	scopes := current.Scopes
	if len(scopes) == 0 {
		scopes = []AlertScope{{}}
	}
	for _, scope := range scopes {
		if !c.silenceUntil(event, scope, silences).IsZero() {
			return receiver, "silenced", nil
		}
	}
	return receiver, "", nil
}

func (w NotificationWorker) cancelRoutedNotification(ctx context.Context, value Notification, reason string) error {
	batch, err := w.prepareNotificationCancellation(ctx, value, reason)
	if err != nil {
		return err
	}
	return batch.commit(ctx, w.Core)
}

func (w NotificationWorker) prepareNotificationCancellation(ctx context.Context, value Notification, reason string) (notificationBatch, error) {
	c := w.Core
	value.Status, value.ErrorCode, value.LeaseUntil = "CANCELLED", reason, time.Time{}
	writes := notificationBatch{}
	// Policy changes may split an unsent shared receipt. Its successor is pinned
	// to this receiver; delivery failures never trigger another channel.
	if !value.DirectTest && !value.Legacy && (reason == "silenced" || reason == "ownership_changed" || reason == "route_changed") {
		state, _, err := c.notificationRoutingState(ctx)
		if err != nil {
			return writes, err
		}
		key := digestString(value.ID + ":" + id.New("policy"))
		event := value.Event
		event.ID = "event_" + key
		record := notificationEventRecord{Event: event, EventKey: key, Status: "PENDING", NextAttempt: c.now(), CapturedAt: c.now(), Restricted: true, AllowedScopes: value.Event.Scopes, TargetReceiverID: value.ReceiverID, RoutedGeneration: state.Generation}
		writes.add(c, "nevent_"+key, notificationEventKind, "", record, 0)
	}
	writes.add(c, value.ID, notificationDeliveryKind, "", value, value.Revision)
	return writes, nil
}

func (w NotificationWorker) deliverRouted(ctx context.Context, value Notification) error {
	c := w.Core
	if c.config.Controller.RecoveryMode || value.Status != "PENDING" && value.Status != "SENDING" || c.now().Before(value.LeaseUntil) || c.now().Before(value.NextAttempt) {
		return nil
	}
	if value.ReceiverID == "" {
		return nil
	} // bootstrap has not indexed this legacy row yet
	receiver, reason, err := c.routedNotificationPolicy(ctx, value)
	if err != nil {
		return err
	}
	if reason == "migration_pending" || reason == "recovery_held" {
		return nil
	}
	if reason != "" {
		return w.cancelRoutedNotification(ctx, value, reason)
	}
	if value.Attempts >= 5 {
		value.Status, value.ErrorCode, value.LeaseUntil = "FAILED", "delivery_unconfirmed", time.Time{}
		return c.saveControl(ctx, value.ID, notificationDeliveryKind, "", value, value.Revision)
	}
	laneKey := "notice-lane:" + digestString(value.ReceiverID)
	lane, laneDoc, err := readControl[notificationLane](ctx, c.store, laneKey, "notification-lane")
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if c.now().Before(lane.Until) {
		return nil
	}
	lane.Owner, lane.Until = id.New("send"), c.now().Add(time.Minute)
	value.Status, value.LeaseUntil, value.Attempts = "SENDING", lane.Until, value.Attempts+1
	var slotKey string
	var slotRevision int64
	claimed := false
	for slot := 0; slot < 4; slot++ {
		slotKey = fmt.Sprintf("notice-slot:%d", slot)
		previous, doc, err := readControl[notificationLane](ctx, c.store, slotKey, "notification-slot")
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		if c.now().Before(previous.Until) {
			continue
		}
		batch := notificationBatch{}
		batch.add(c, value.ID, notificationDeliveryKind, "", value, value.Revision)
		batch.add(c, laneKey, "notification-lane", "", lane, laneDoc.Revision)
		batch.add(c, slotKey, "notification-slot", "", lane, doc.Revision)
		err = batch.commit(ctx, c)
		if errors.Is(err, storage.ErrPrecondition) || errors.Is(err, storage.ErrConflict) {
			continue
		}
		if err != nil {
			return err
		}
		slotRevision, claimed = doc.Revision+1, true
		break
	}
	if !claimed {
		return nil
	}
	value.Revision++
	laneDoc.Revision++
	// Recheck after claiming: settings may change between queue selection and the
	// network request. CAS protects receipts; the sender validates DNS at dial.
	receiver, reason, err = c.routedNotificationPolicy(ctx, value)
	if err != nil {
		return err
	} // retain an uncertain claimed attempt for lease recovery
	if reason == "recovery_held" {
		return nil
	}
	if reason != "" {
		value.Status, value.ErrorCode = "CANCELLED", reason
	} else {
		body, err := json.Marshal(value.Event)
		if err != nil {
			return err
		}
		sender := w.Sender
		if sender == nil {
			sender = notification.Webhook{}
		}
		result := sender.Send(ctx, receiver.Endpoint, receiver.Secret, value.Event.ID, body, c.now())
		value.Status, value.ErrorCode = "FAILED", result.Code
		if result.Delivered {
			value.Status = "DELIVERED"
			now := c.now()
			value.DeliveredAt = &now
		} else if result.Retryable && value.Attempts < 5 {
			value.Status, value.NextAttempt = "PENDING", c.now().Add(max(time.Duration(1<<value.Attempts)*15*time.Second, result.RetryAfter))
		}
	}
	// Cancellation may mean the receiver accepted a request but its receipt was
	// lost. Preserve SENDING and its immutable ID for lease-based retry.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	value.LeaseUntil, lane.Until = time.Time{}, time.Time{}
	batch := notificationBatch{}
	if reason != "" {
		batch, err = w.prepareNotificationCancellation(ctx, value, reason)
		if err != nil {
			return err
		}
	} else {
		batch.add(c, value.ID, notificationDeliveryKind, "", value, value.Revision)
	}
	batch.add(c, laneKey, "notification-lane", "", lane, laneDoc.Revision)
	batch.add(c, slotKey, "notification-slot", "", lane, slotRevision)
	return batch.commit(ctx, c)
}

func (c *Core) retryRoutedNotification(ctx context.Context, value Notification, doc storage.ControlDocument) (Notification, error) {
	if value.Status != "FAILED" && value.Status != "CANCELLED" {
		return value, storage.ErrConflict
	}
	receiver, receiverDoc, err := c.notificationReceiver(ctx, value.ReceiverID)
	if err != nil {
		return value, err
	}
	if !receiver.Enabled {
		return value, storage.ErrConflict
	}
	// Retry keeps the exact signed payload. Ownership and route changes require a
	// new event, so an old receipt cannot be used to disclose removed scopes.
	value.SettingsRevision = receiverDoc.Revision
	value.ManualRetry = true
	if !value.DirectTest {
		event := value.Event
		if err := c.notificationScopes(ctx, &event); err != nil {
			return value, err
		}
		expected := value.Event.Scopes
		if value.Legacy {
			expected = value.LegacyScopes
		}
		for _, scope := range expected {
			if !slices.Contains(event.Scopes, scope) {
				return value, storage.ErrConflict
			}
		}
		notificationEventScopes(&event, expected)
		event.OccurredAt = c.now()
		matches, err := c.matchNotificationRoutes(ctx, event)
		if err != nil {
			return value, err
		}
		match := slices.IndexFunc(matches, func(m NotificationRouteMatch) bool { return m.ReceiverID == value.ReceiverID })
		if match < 0 {
			return value, storage.ErrConflict
		}
		value.Routes = matches[match].Routes
	}
	_, reason, err := c.routedNotificationPolicy(ctx, value)
	if err != nil {
		return value, err
	}
	if reason != "" {
		return value, storage.ErrConflict
	}
	value.Status, value.ErrorCode, value.Attempts, value.LeaseUntil = "PENDING", "", 0, time.Time{}
	value.NextAttempt = c.now()
	err = c.saveControl(ctx, doc.ID, notificationDeliveryKind, "", value, doc.Revision)
	value.ControlDocument = doc
	value.Revision++
	return value, err
}
