package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/notification"
	"github.com/yernsun/network-service-composer/internal/storage"
)

const notificationSettingsKey = "notification:settings"
const notificationSettingsKind = "notification-settings"
const notificationDeliveryKind = "notification-delivery"

var notificationGroups = []string{"images", "deployments", "certificates", "gateways", "runtimes"}

type NotificationSettingsInput struct {
	Enabled  bool     `json:"enabled"`
	Events   []string `json:"events"`
	Endpoint *string  `json:"endpoint,omitempty"`
	Secret   *string  `json:"secret,omitempty"`
}
type notificationSettings struct {
	Enabled  bool      `json:"enabled"`
	Events   []string  `json:"events"`
	Endpoint string    `json:"endpoint"`
	Secret   string    `json:"secret"`
	Since    time.Time `json:"since"`
}
type NotificationSettings struct {
	Revision           int64    `json:"revision"`
	Enabled            bool     `json:"enabled"`
	Events             []string `json:"events"`
	EndpointConfigured bool     `json:"endpoint_configured"`
	EndpointHost       string   `json:"endpoint_host"`
	SecretConfigured   bool     `json:"secret_configured"`
}
type NotificationEvent struct {
	ID            string       `json:"id"`
	Type          string       `json:"type"`
	ResourceID    string       `json:"resource_id"`
	EnvironmentID string       `json:"environment_id,omitempty"`
	OperationID   string       `json:"operation_id,omitempty"`
	Status        string       `json:"status,omitempty"`
	OccurredAt    time.Time    `json:"occurred_at"`
	Scopes        []AlertScope `json:"scopes,omitempty"`
	IssueID       string       `json:"issue_id,omitempty"`
	Occurrence    int64        `json:"occurrence,omitempty"`
	Severity      string       `json:"severity,omitempty"`
}
type Notification struct {
	storage.ControlDocument
	Event            NotificationEvent          `json:"event"`
	Status           string                     `json:"status"`
	Attempts         int                        `json:"attempts"`
	ErrorCode        string                     `json:"error_code,omitempty"`
	NextAttempt      time.Time                  `json:"next_attempt_at"`
	DeliveredAt      *time.Time                 `json:"delivered_at,omitempty"`
	SettingsRevision int64                      `json:"settings_revision"`
	LeaseUntil       time.Time                  `json:"lease_until,omitempty"`
	EventKey         string                     `json:"event_key,omitempty"`
	ReceiverID       string                     `json:"receiver_id,omitempty"`
	Routes           []NotificationRouteReceipt `json:"routes,omitempty"`
	RouteGeneration  int64                      `json:"route_generation,omitempty"`
	DirectTest       bool                       `json:"direct_test,omitempty"`
	Legacy           bool                       `json:"legacy,omitempty"`
	LegacyScopes     []AlertScope               `json:"legacy_scopes,omitempty"`
	ManualRetry      bool                       `json:"manual_retry,omitempty"`
}

func (c *Core) notificationSettings(ctx context.Context) (notificationSettings, storage.ControlDocument, error) {
	s := notificationSettings{Events: []string{}}
	d, err := c.readEncrypted(ctx, notificationSettingsKey, notificationSettingsKind, &s)
	if errors.Is(err, storage.ErrNotFound) {
		return s, storage.ControlDocument{}, nil
	}
	return s, d, err
}
func (c *Core) GetNotificationSettings(ctx context.Context) (NotificationSettings, error) {
	s, d, err := c.notificationSettings(ctx)
	host := ""
	if u, e := url.Parse(s.Endpoint); e == nil {
		host = u.Host
	}
	if err != nil {
		return NotificationSettings{}, err
	}
	route, routeDoc, routeErr := readControl[NotificationRoute](ctx, c.store, notificationRouteID("global", ""), notificationRouteKind)
	if routeErr != nil && !errors.Is(routeErr, storage.ErrNotFound) {
		return NotificationSettings{}, routeErr
	}
	enabled := s.Enabled
	if routeDoc.Revision != 0 {
		enabled = enabled && route.Enabled && slices.Contains(route.ReceiverIDs, defaultNotificationReceiver)
	}
	return NotificationSettings{Revision: d.Revision, Enabled: enabled, Events: s.Events, EndpointConfigured: s.Endpoint != "", EndpointHost: host, SecretConfigured: s.Secret != ""}, nil
}
func (c *Core) SaveNotificationSettings(ctx context.Context, in NotificationSettingsInput, expected int64) (NotificationSettings, error) {
	s, d, err := c.notificationSettings(ctx)
	if err != nil {
		return NotificationSettings{}, err
	}
	if expected != d.Revision {
		return NotificationSettings{}, storage.ErrPrecondition
	}
	if in.Endpoint != nil {
		s.Endpoint = *in.Endpoint
	}
	if in.Secret != nil {
		s.Secret = *in.Secret
	}
	if s.Endpoint != "" && notification.ValidateEndpoint(s.Endpoint) != nil {
		return NotificationSettings{}, controlInvalid("endpoint", "a public HTTPS webhook URL is required")
	}
	if s.Secret != "" && (len(s.Secret) < 16 || len(s.Secret) > 256) {
		return NotificationSettings{}, controlInvalid("secret", "signing secret must be between 16 and 256 bytes")
	}
	for i, group := range in.Events {
		if !slices.Contains(notificationGroups, group) || slices.Contains(in.Events[:i], group) {
			return NotificationSettings{}, controlInvalid("events", "select supported event groups without duplicates")
		}
	}
	if in.Enabled && (s.Endpoint == "" || s.Secret == "" || len(in.Events) == 0) {
		return NotificationSettings{}, controlInvalid("enabled", "an endpoint, signing secret and event groups are required")
	}
	if in.Enabled && (!s.Enabled || !slices.Equal(s.Events, in.Events)) {
		s.Since = c.now()
	}
	s.Enabled, s.Events = in.Enabled, in.Events
	if s.Events == nil {
		s.Events = []string{}
	}
	w, err := c.encryptedWrite(notificationSettingsKey, notificationSettingsKind, "", s, expected)
	if err != nil {
		return NotificationSettings{}, err
	}
	writes := []storage.ControlWrite{w}
	route, routeDoc, routeErr := readControl[NotificationRoute](ctx, c.store, notificationRouteID("global", ""), notificationRouteKind)
	if routeErr != nil && !errors.Is(routeErr, storage.ErrNotFound) {
		return NotificationSettings{}, routeErr
	}
	if routeDoc.Revision != 0 {
		if in.Enabled {
			route.Since = c.now()
		}
		route.Events = slices.Clone(in.Events)
		route.ReceiverIDs = slices.DeleteFunc(route.ReceiverIDs, func(key string) bool { return key == defaultNotificationReceiver })
		if in.Enabled {
			route.ReceiverIDs = append(route.ReceiverIDs, defaultNotificationReceiver)
			route.Enabled = true
		}
		if len(route.ReceiverIDs) == 0 {
			route.Enabled = false
		}
		update, err := c.controlWrite(routeDoc.ID, notificationRouteKind, "", route, routeDoc.Revision)
		if err != nil {
			return NotificationSettings{}, err
		}
		writes = append(writes, update)
	}
	if enabled, err := c.notificationRoutingEnabled(ctx); err != nil {
		return NotificationSettings{}, err
	} else if enabled {
		writes, err = c.notificationRoutingWrites(ctx, writes)
		if err != nil {
			return NotificationSettings{}, err
		}
	}
	if err = c.store.WriteControlDocuments(ctx, writes); err != nil {
		return NotificationSettings{}, err
	}
	return c.GetNotificationSettings(ctx)
}
func (c *Core) ListNotifications(ctx context.Context, page storage.Page) ([]Notification, error) {
	docs, err := c.store.ListControlDocuments(ctx, notificationDeliveryKind, "", page)
	rows := make([]Notification, 0, len(docs))
	for _, d := range docs {
		var v Notification
		if e := json.Unmarshal(d.Payload, &v); e != nil {
			return nil, e
		}
		v.ControlDocument = d
		rows = append(rows, v)
	}
	return rows, err
}
func (c *Core) notificationWrite(ctx context.Context, e NotificationEvent, revision int64) (storage.ControlWrite, error) {
	if e.Severity == "" {
		e.Severity = "info"
		if e.Status == "FAILED" || e.Status == "ROLLBACK_FAILED" {
			e.Severity = "critical"
		}
	}
	if err := c.notificationScopes(ctx, &e); err != nil {
		return storage.ControlWrite{}, err
	}
	if enabled, err := c.notificationRoutingEnabled(ctx); err != nil {
		return storage.ControlWrite{}, err
	} else if enabled {
		if e.Type == "notification.test" {
			eventKey := digestString(e.ID)
			e.ID = "event_" + eventKey
			return c.notificationDeliveryWrite(e, eventKey, defaultNotificationReceiver, revision, nil, true, 0)
		}
		return c.notificationEventWrite(ctx, e)
	}
	// Reverse the immutable occurrence time so paginated history starts with recent events.
	e.ID = fmt.Sprintf("notice_%012d_%s", int64(253402300799)-e.OccurredAt.Unix(), digestString(e.ID))
	v := Notification{Event: e, Status: "PENDING", SettingsRevision: revision, NextAttempt: c.now()}
	return c.controlWrite(e.ID, notificationDeliveryKind, "", v, 0)
}
func (c *Core) enqueueNotification(ctx context.Context, e NotificationEvent, revision int64) error {
	w, err := c.notificationWrite(ctx, e, revision)
	if err != nil {
		return err
	}
	if err = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{w}); errors.Is(err, storage.ErrConflict) {
		return nil
	}
	return err
}
func (c *Core) TestNotification(ctx context.Context, expected int64) error {
	s, d, err := c.notificationSettings(ctx)
	if err != nil {
		return err
	}
	if d.Revision != expected {
		return storage.ErrPrecondition
	}
	if !s.Enabled {
		return storage.ErrConflict
	}
	return c.enqueueNotification(ctx, NotificationEvent{ID: id.New("notice-test"), Type: "notification.test", ResourceID: notificationSettingsKey, OccurredAt: c.now()}, d.Revision)
}
func (c *Core) RetryNotification(ctx context.Context, key string, expected int64) (Notification, error) {
	v, doc, err := readControl[Notification](ctx, c.store, key, notificationDeliveryKind)
	if err != nil {
		return v, err
	}
	if doc.Revision != expected {
		return v, storage.ErrPrecondition
	}
	if v.ReceiverID != "" {
		return c.retryRoutedNotification(ctx, v, doc)
	}
	s, d, err := c.notificationSettings(ctx)
	if err != nil {
		return v, err
	}
	if !s.Enabled || v.Status != "FAILED" && v.Status != "CANCELLED" {
		return v, storage.ErrConflict
	}
	v.Status, v.Attempts, v.ErrorCode = "PENDING", 0, ""
	v.SettingsRevision, v.NextAttempt = d.Revision, c.now()
	v.LeaseUntil = time.Time{}
	err = c.saveControl(ctx, key, notificationDeliveryKind, "", v, doc.Revision)
	v.ControlDocument = doc
	v.Revision++
	return v, err
}

// Observation and outbox insertion share a transaction. Restart or competing
// collectors cannot lose a transition between these two writes.
func (c *Core) observeNotification(ctx context.Context, e NotificationEvent, bad bool, revision int64) error {
	code, kind := notificationIssue(e.Type)
	writes := []storage.ControlWrite{}
	severity := "info"
	if bad {
		severity = "critical"
	}
	if code != "" {
		state := "CONFIRMED"
		if kind == "runtime" {
			state = e.Status
		}
		issue, write, err := c.issueObservation(ctx, code, kind, e.ResourceID, alertLink(kind, e.ResourceID), &bad, state, e.OperationID)
		if err != nil {
			return err
		}
		e.IssueID, e.Occurrence = issue.ID, issue.Occurrences
		if bad {
			severity = issue.Severity
		}
		if write != nil {
			writes = append(writes, *write)
		}
	}
	e.Severity = severity
	key := "notice-state:" + digestString(e.ID+":"+e.ResourceID)
	type state struct {
		Bad      bool  `json:"bad"`
		Sequence int64 `json:"sequence"`
	}
	previous, d, err := readControl[state](ctx, c.store, key, "notification-state")
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if d.Revision != 0 && previous.Bad == bad {
		err = c.store.WriteControlDocuments(ctx, writes)
		if errors.Is(err, storage.ErrPrecondition) || errors.Is(err, storage.ErrConflict) {
			return nil
		}
		return err
	}
	previous.Bad = bad
	previous.Sequence++
	w, err := c.controlWrite(key, "notification-state", "", previous, d.Revision)
	if err != nil {
		return err
	}
	writes = append(writes, w)
	if bad || d.Revision != 0 {
		e.ID = key + ":" + strconv.FormatInt(previous.Sequence, 10)
		e.OccurredAt = c.now()
		notice, err := c.notificationWrite(ctx, e, revision)
		if err != nil {
			return err
		}
		writes = append(writes, notice)
		if notice.Document.Kind == notificationEventKind && e.IssueID != "" {
			pointerKey := "notice-current:" + digestString(e.IssueID)
			_, pointer, err := readControl[notificationCurrent](ctx, c.store, pointerKey, notificationCurrentKind)
			if err != nil && !errors.Is(err, storage.ErrNotFound) {
				return err
			}
			if bad {
				write, err := c.controlWrite(pointerKey, notificationCurrentKind, "", notificationCurrent{EventID: notice.Document.ID}, pointer.Revision)
				if err != nil {
					return err
				}
				writes = append(writes, write)
			} else if pointer.Revision != 0 {
				writes = append(writes, storage.ControlWrite{Document: pointer, Expected: pointer.Revision, Remove: true})
			}
		}
	}
	err = c.store.WriteControlDocuments(ctx, writes)
	if errors.Is(err, storage.ErrPrecondition) || errors.Is(err, storage.ErrConflict) {
		return nil
	}
	return err
}

// NotificationWorker is leader-owned and independent of browser sessions.
type NotificationWorker struct {
	Core   *Core
	Sender notification.Sender
}

func (w NotificationWorker) Run(ctx context.Context) error {
	return w.runRouted(ctx)
}
func (w NotificationWorker) tick(ctx context.Context) error {
	if w.Core.config.Controller.RecoveryMode {
		return nil
	}
	return errors.Join(w.routeTick(ctx), w.deliveryPass(ctx))
}
