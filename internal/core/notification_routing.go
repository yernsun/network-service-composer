package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yernsun/network-service-composer/internal/notification"
	"github.com/yernsun/network-service-composer/internal/storage"
)

const defaultNotificationReceiver = "default"
const notificationReceiverKind = "notification-receiver"
const notificationRouteKind = "notification-route"
const notificationSilenceKind = "notification-silence"

var receiverIdentity = regexp.MustCompile(`^receiver_[a-z0-9_-]{1,80}$`)

type NotificationReceiverInput struct {
	Name     string  `json:"name"`
	Enabled  bool    `json:"enabled"`
	Endpoint *string `json:"endpoint,omitempty"`
	Secret   *string `json:"secret,omitempty"`
}
type notificationReceiver struct {
	Name     string    `json:"name"`
	Enabled  bool      `json:"enabled"`
	Endpoint string    `json:"endpoint"`
	Secret   string    `json:"secret"`
	Since    time.Time `json:"since"`
}
type NotificationReceiver struct {
	storage.ControlDocument
	Name               string `json:"name"`
	Enabled            bool   `json:"enabled"`
	EndpointHost       string `json:"endpoint_host"`
	EndpointConfigured bool   `json:"endpoint_configured"`
	SecretConfigured   bool   `json:"secret_configured"`
}
type NotificationRouteInput struct {
	ScopeType            string   `json:"scope_type"`
	ScopeID              string   `json:"scope_id"`
	Enabled              bool     `json:"enabled"`
	ReceiverIDs          []string `json:"receiver_ids"`
	Events               []string `json:"events"`
	Severities           []string `json:"severities"`
	BaseSettingsRevision int64    `json:"base_settings_revision"`
}
type NotificationRoute struct {
	storage.ControlDocument
	NotificationRouteInput
	Since         time.Time            `json:"since"`
	ReceiverSince map[string]time.Time `json:"receiver_since,omitempty"`
}
type NotificationRouteReceipt struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}
type NotificationRouteMatch struct {
	ReceiverID       string                     `json:"receiver_id"`
	ReceiverRevision int64                      `json:"receiver_revision"`
	Scopes           []AlertScope               `json:"scopes"`
	Routes           []NotificationRouteReceipt `json:"routes"`
	Since            time.Time                  `json:"since"`
}

func (c *Core) notificationReceiver(ctx context.Context, key string) (notificationReceiver, storage.ControlDocument, error) {
	if key == defaultNotificationReceiver {
		s, d, err := c.notificationSettings(ctx)
		d.ID = key
		return notificationReceiver{Name: "Default global receiver", Enabled: s.Enabled, Endpoint: s.Endpoint, Secret: s.Secret, Since: s.Since}, d, err
	}
	var value notificationReceiver
	d, err := c.readEncrypted(ctx, key, notificationReceiverKind, &value)
	return value, d, err
}
func (c *Core) GetNotificationReceiver(ctx context.Context, key string) (NotificationReceiver, error) {
	v, d, err := c.notificationReceiver(ctx, key)
	host := ""
	if endpoint, e := url.Parse(v.Endpoint); e == nil {
		host = endpoint.Host
	}
	return NotificationReceiver{ControlDocument: d, Name: v.Name, Enabled: v.Enabled, EndpointHost: host, EndpointConfigured: v.Endpoint != "", SecretConfigured: v.Secret != ""}, err
}
func (c *Core) ListNotificationReceivers(ctx context.Context) ([]NotificationReceiver, error) {
	first, err := c.GetNotificationReceiver(ctx, defaultNotificationReceiver)
	if err != nil {
		return nil, err
	}
	result := []NotificationReceiver{first}
	for page := (storage.Page{Limit: 200}); ; {
		docs, err := c.store.ListControlDocuments(ctx, notificationReceiverKind, "", page)
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			v, err := c.GetNotificationReceiver(ctx, d.ID)
			if err != nil {
				return nil, err
			}
			result = append(result, v)
		}
		if len(docs) < page.Limit {
			return result, nil
		}
		page.After = docs[len(docs)-1].ID
	}
}
func (c *Core) SaveNotificationReceiver(ctx context.Context, key string, in NotificationReceiverInput, expected int64) (NotificationReceiver, error) {
	if key == defaultNotificationReceiver {
		s, _, err := c.notificationSettings(ctx)
		if err != nil {
			return NotificationReceiver{}, err
		}
		events := s.Events
		if len(events) == 0 {
			events = slices.Clone(notificationGroups)
		}
		_, err = c.SaveNotificationSettings(ctx, NotificationSettingsInput{Enabled: in.Enabled, Events: events, Endpoint: in.Endpoint, Secret: in.Secret}, expected)
		if err != nil {
			return NotificationReceiver{}, err
		}
		return c.GetNotificationReceiver(ctx, key)
	}
	if !receiverIdentity.MatchString(key) || strings.TrimSpace(in.Name) != in.Name || len(in.Name) < 1 || utf8.RuneCountInString(in.Name) > 100 {
		return NotificationReceiver{}, controlInvalid("name", "provide a receiver identity and a name of 1–100 bytes")
	}
	v, d, err := c.notificationReceiver(ctx, key)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return NotificationReceiver{}, err
	}
	if expected != d.Revision {
		return NotificationReceiver{}, storage.ErrPrecondition
	}
	if in.Endpoint != nil {
		v.Endpoint = *in.Endpoint
	}
	if in.Secret != nil {
		v.Secret = *in.Secret
	}
	if v.Endpoint != "" && notification.ValidateEndpoint(v.Endpoint) != nil {
		return NotificationReceiver{}, controlInvalid("endpoint", "a public HTTPS webhook URL is required")
	}
	if v.Secret != "" && (len(v.Secret) < 16 || len(v.Secret) > 256) {
		return NotificationReceiver{}, controlInvalid("secret", "signing secret must be between 16 and 256 bytes")
	}
	if in.Enabled && (v.Endpoint == "" || v.Secret == "") {
		return NotificationReceiver{}, controlInvalid("enabled", "an endpoint and signing secret are required")
	}
	if in.Enabled && !v.Enabled {
		v.Since = c.now()
	}
	v.Enabled, v.Name = in.Enabled, in.Name
	batch := notificationBatch{}
	batch.prepared(c.encryptedWrite(key, notificationReceiverKind, "", v, expected))
	if err = c.commitNotificationConfiguration(ctx, batch); err != nil {
		return NotificationReceiver{}, err
	}
	return c.GetNotificationReceiver(ctx, key)
}

func notificationRouteID(scope, key string) string { return "notice-route:" + scope + ":" + key }

func (c *Core) globalNotificationRoute(ctx context.Context) (NotificationRoute, error) {
	v, d, err := readControl[NotificationRoute](ctx, c.store, notificationRouteID("global", ""), notificationRouteKind)
	if !errors.Is(err, storage.ErrNotFound) {
		v.ControlDocument = d
		return v, err
	}
	s, settings, err := c.notificationSettings(ctx)
	v = NotificationRoute{NotificationRouteInput: NotificationRouteInput{ScopeType: "global", Enabled: s.Enabled, Events: s.Events, Severities: []string{}, ReceiverIDs: []string{defaultNotificationReceiver}, BaseSettingsRevision: settings.Revision}, Since: s.Since}
	v.ID = notificationRouteID("global", "")
	return v, err
}
func (c *Core) ListNotificationRoutes(ctx context.Context) ([]NotificationRoute, error) {
	global, err := c.globalNotificationRoute(ctx)
	if err != nil {
		return nil, err
	}
	result := []NotificationRoute{global}
	for page := (storage.Page{Limit: 200}); ; {
		docs, err := c.store.ListControlDocuments(ctx, notificationRouteKind, "", page)
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			if d.ID == global.ID {
				continue
			}
			var v NotificationRoute
			if err := json.Unmarshal(d.Payload, &v); err != nil {
				return nil, err
			}
			v.ControlDocument = d
			result = append(result, v)
		}
		if len(docs) < page.Limit {
			return result, nil
		}
		page.After = docs[len(docs)-1].ID
	}
}
func (c *Core) notificationScopeExists(ctx context.Context, kind, key string) error {
	switch kind {
	case "global":
		if key == "" {
			return nil
		}
	case "project":
		_, err := c.GetProject(ctx, key)
		return err
	case "environment":
		_, err := c.GetEnvironment(ctx, key)
		return err
	}
	return controlInvalid("scope_type", "select a global, project or environment scope")
}
func validateNoticeChoices(values, allowed []string) bool {
	for i, value := range values {
		if !slices.Contains(allowed, value) || slices.Contains(values[:i], value) {
			return false
		}
	}
	return true
}
func (c *Core) SaveNotificationRoute(ctx context.Context, in NotificationRouteInput, expected int64) (NotificationRoute, error) {
	if in.Events == nil {
		in.Events = []string{}
	}
	if in.ReceiverIDs == nil {
		in.ReceiverIDs = []string{}
	}
	if in.Severities == nil {
		in.Severities = []string{}
	}
	if err := c.notificationScopeExists(ctx, in.ScopeType, in.ScopeID); err != nil {
		return NotificationRoute{}, err
	}
	if !validateNoticeChoices(in.Events, notificationGroups) || !validateNoticeChoices(in.Severities, []string{"info", "warning", "critical"}) || len(in.ReceiverIDs) > 20 || !validateNoticeChoices(in.ReceiverIDs, in.ReceiverIDs) {
		return NotificationRoute{}, controlInvalid("events", "select supported events, severities and distinct receivers")
	}
	if in.Enabled && (len(in.Events) == 0 || len(in.ReceiverIDs) == 0) {
		return NotificationRoute{}, controlInvalid("enabled", "enabled routes require event groups and receivers")
	}
	key := notificationRouteID(in.ScopeType, in.ScopeID)
	previous, d, err := readControl[NotificationRoute](ctx, c.store, key, notificationRouteKind)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return NotificationRoute{}, err
	}
	if expected != d.Revision {
		return NotificationRoute{}, storage.ErrPrecondition
	}
	if in.ScopeType == "global" && d.Revision == 0 {
		_, legacy, err := c.notificationSettings(ctx)
		if err != nil {
			return NotificationRoute{}, err
		}
		if in.BaseSettingsRevision != legacy.Revision {
			return NotificationRoute{}, storage.ErrPrecondition
		}
	}
	for _, receiver := range in.ReceiverIDs {
		if _, _, err := c.notificationReceiver(ctx, receiver); err != nil {
			return NotificationRoute{}, err
		}
	}
	v := NotificationRoute{NotificationRouteInput: in, Since: previous.Since, ReceiverSince: map[string]time.Time{}}
	if in.Enabled {
		v.Since = c.now()
	}
	for _, receiver := range in.ReceiverIDs {
		v.ReceiverSince[receiver] = previous.ReceiverSince[receiver]
		if !slices.Contains(previous.ReceiverIDs, receiver) {
			v.ReceiverSince[receiver] = c.now()
		}
	}
	w, encodeErr := c.controlWrite(key, notificationRouteKind, in.ScopeID, v, expected)
	batch := notificationBatch{}
	batch.prepared(w, encodeErr)
	if in.ScopeType == "global" {
		s, legacy, err := c.notificationSettings(ctx)
		if err != nil {
			return v, err
		}
		if d.Revision == 0 && in.BaseSettingsRevision != legacy.Revision {
			return v, storage.ErrPrecondition
		}
		s.Events = slices.Clone(in.Events)
		batch.prepared(c.encryptedWrite(notificationSettingsKey, notificationSettingsKind, "", s, legacy.Revision))
	}
	if err = c.commitNotificationConfiguration(ctx, batch); err != nil {
		return v, err
	}
	v.ControlDocument = w.Document
	v.Revision = expected + 1
	return v, nil
}

func notificationGroup(kind string) string {
	switch {
	case strings.HasPrefix(kind, "image."):
		return "images"
	case strings.HasPrefix(kind, "deployment."):
		return "deployments"
	case strings.HasPrefix(kind, "certificate."):
		return "certificates"
	case strings.HasPrefix(kind, "gateway."):
		return "gateways"
	case strings.HasPrefix(kind, "runtime."):
		return "runtimes"
	}
	return ""
}

func (c *Core) matchNotificationRoutes(ctx context.Context, event NotificationEvent) ([]NotificationRouteMatch, error) {
	// Read only the rules needed for this event. An unavailable parent cannot
	// block an already matched child, while an unreadable child fails closed.
	index := map[string]NotificationRoute{}
	loaded := map[string]bool{}
	load := func(scope, identity string) (NotificationRoute, error) {
		key := notificationRouteID(scope, identity)
		if loaded[key] {
			return index[key], nil
		}
		var value NotificationRoute
		var err error
		if scope == "global" {
			value, err = c.globalNotificationRoute(ctx)
		} else {
			var doc storage.ControlDocument
			value, doc, err = readControl[NotificationRoute](ctx, c.store, key, notificationRouteKind)
			value.ControlDocument = doc
			if errors.Is(err, storage.ErrNotFound) {
				err = nil
			}
		}
		if err == nil && value.ID != "" && (value.ScopeType != scope || value.ScopeID != identity) {
			err = controlInvalid("scope_id", "stored route does not match its scope")
		}
		if err == nil {
			loaded[key], index[key] = true, value
		}
		return value, err
	}
	readers := map[string]notificationReceiver{}
	revisions := map[string]int64{}
	result := []NotificationRouteMatch{}
	scopes := event.Scopes
	if len(scopes) == 0 {
		scopes = []AlertScope{{}}
	}
	for _, scope := range scopes {
		if scope.EnvironmentID != "" {
			env, err := c.GetEnvironment(ctx, scope.EnvironmentID)
			if errors.Is(err, storage.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if env.ProjectID != scope.ProjectID {
				continue
			}
		}
		levels := []struct{ kind, id string }{{"environment", scope.EnvironmentID}, {"project", scope.ProjectID}, {"global", ""}}
		for _, level := range levels {
			if level.kind != "global" && level.id == "" {
				continue
			}
			route, err := load(level.kind, level.id)
			if err != nil {
				return nil, err
			}
			if !route.Enabled || !slices.Contains(route.Events, notificationGroup(event.Type)) || len(route.Severities) > 0 && !slices.Contains(route.Severities, event.Severity) {
				continue
			}
			enabled := []string{}
			for _, id := range route.ReceiverIDs {
				receiver, ok := readers[id]
				if !ok {
					value, d, err := c.notificationReceiver(ctx, id)
					if err != nil {
						return nil, err
					}
					readers[id], revisions[id] = value, d.Revision
					receiver = value
				}
				if receiver.Enabled {
					enabled = append(enabled, id)
				}
			}
			if len(enabled) == 0 {
				continue
			}
			for _, id := range enabled {
				cutoff := route.Since
				for _, since := range []time.Time{route.ReceiverSince[id], readers[id].Since} {
					if since.After(cutoff) {
						cutoff = since
					}
				}
				if !notificationProblem(event.Type) && event.OccurredAt.Before(cutoff) {
					continue
				}
				at := slices.IndexFunc(result, func(match NotificationRouteMatch) bool { return match.ReceiverID == id })
				if at < 0 {
					result = append(result, NotificationRouteMatch{ReceiverID: id, ReceiverRevision: revisions[id], Scopes: []AlertScope{}, Routes: []NotificationRouteReceipt{}})
					at = len(result) - 1
				}
				match := &result[at]
				if scope.EnvironmentID != "" && !slices.Contains(match.Scopes, scope) {
					match.Scopes = append(match.Scopes, scope)
				}
				receipt := NotificationRouteReceipt{ID: route.ID, Revision: route.Revision}
				if !slices.Contains(match.Routes, receipt) {
					match.Routes = append(match.Routes, receipt)
				}
				for _, since := range []time.Time{route.Since, route.ReceiverSince[id], readers[id].Since} {
					if since.After(match.Since) {
						match.Since = since
					}
				}
			}
			break
		}
	}
	return result, nil
}
