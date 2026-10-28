package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/storage"
)

const notificationEventKind = "notification-event"
const notificationCurrentKind = "notification-current"

type notificationEventRecord struct {
	storage.ControlDocument
	Event            NotificationEvent `json:"event"`
	EventKey         string            `json:"event_key"`
	Status           string            `json:"status"`
	NextAttempt      time.Time         `json:"next_attempt_at"`
	RoutedGeneration int64             `json:"routed_generation"`
	CapturedAt       time.Time         `json:"captured_at"`
	Restricted       bool              `json:"restricted,omitempty"`
	AllowedScopes    []AlertScope      `json:"allowed_scopes,omitempty"`
	TargetReceiverID string            `json:"target_receiver_id,omitempty"`
}
type notificationCurrent struct {
	EventID string `json:"event_id"`
}

func (c *Core) notificationEventWrite(ctx context.Context, event NotificationEvent) (storage.ControlWrite, error) {
	key := digestString(event.ID)
	event.ID = "event_" + key
	record := notificationEventRecord{Event: event, EventKey: key, Status: "PENDING", NextAttempt: c.now(), CapturedAt: c.now()}
	return c.controlWrite("nevent_"+key, notificationEventKind, "", record, 0)
}

func (c *Core) notificationDeliveryWrite(event NotificationEvent, eventKey, receiver string, revision int64, routes []NotificationRouteReceipt, test bool, generation int64) (storage.ControlWrite, error) {
	key := fmt.Sprintf("notice_%012d_%s", int64(253402300799)-event.OccurredAt.Unix(), digestString(eventKey+":"+receiver))
	value := Notification{Event: event, EventKey: eventKey, ReceiverID: receiver, Routes: routes, DirectTest: test, RouteGeneration: generation, Status: "PENDING", SettingsRevision: revision, NextAttempt: c.now()}
	return c.controlWrite(key, notificationDeliveryKind, "", value, 0)
}

func (c *Core) TestNotificationReceiver(ctx context.Context, receiver string, expected int64) error {
	v, d, err := c.notificationReceiver(ctx, receiver)
	if err != nil {
		return err
	}
	if d.Revision != expected {
		return storage.ErrPrecondition
	}
	if !v.Enabled {
		return storage.ErrConflict
	}
	event := NotificationEvent{ID: id.New("receiver-test"), Type: "notification.test", ResourceID: receiver, Severity: "info", OccurredAt: c.now()}
	eventKey := digestString(event.ID)
	event.ID = "event_" + eventKey
	w, err := c.notificationDeliveryWrite(event, eventKey, receiver, d.Revision, nil, true, 0)
	if err != nil {
		return err
	}
	return c.store.WriteControlDocuments(ctx, []storage.ControlWrite{w})
}

func (c *Core) notificationEventExists(ctx context.Context, key string) (bool, error) {
	d, err := c.store.GetControlDocument(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if d.Kind != notificationEventKind {
		return false, storage.ErrConflict
	}
	return true, nil
}
