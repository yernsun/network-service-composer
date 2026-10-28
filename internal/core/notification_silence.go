package core

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yernsun/network-service-composer/internal/storage"
)

var silenceIdentity = regexp.MustCompile(`^silence_[a-z0-9_-]{1,80}$`)

type NotificationSilenceInput struct {
	ScopeType string    `json:"scope_type"`
	ScopeID   string    `json:"scope_id"`
	Events    []string  `json:"events"`
	Reason    string    `json:"reason"`
	ExpiresAt time.Time `json:"expires_at"`
}
type NotificationSilence struct {
	storage.ControlDocument
	NotificationSilenceInput
	Actor       string     `json:"actor"`
	CancelledBy string     `json:"cancelled_by,omitempty"`
	CancelledAt *time.Time `json:"cancelled_at,omitempty"`
	Active      bool       `json:"active"`
}

func (c *Core) ListNotificationSilences(ctx context.Context) ([]NotificationSilence, error) {
	result := []NotificationSilence{}
	for page := (storage.Page{Limit: 200}); ; {
		docs, err := c.store.ListControlDocuments(ctx, notificationSilenceKind, "", page)
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			var value NotificationSilence
			if err := json.Unmarshal(d.Payload, &value); err != nil {
				return nil, err
			}
			value.ControlDocument = d
			value.Active = value.CancelledAt == nil && c.now().Before(value.ExpiresAt)
			result = append(result, value)
		}
		if len(docs) < page.Limit {
			return result, nil
		}
		page.After = docs[len(docs)-1].ID
	}
}
func (c *Core) SaveNotificationSilence(ctx context.Context, key string, in NotificationSilenceInput, expected int64, actor string) (NotificationSilence, error) {
	if in.Events == nil {
		in.Events = []string{}
	}
	if !silenceIdentity.MatchString(key) || strings.TrimSpace(in.Reason) == "" || utf8.RuneCountInString(in.Reason) > 512 || actor == "" {
		return NotificationSilence{}, controlInvalid("reason", "provide a silence identity, actor and reason of 1–512 characters")
	}
	if in.ScopeType == "issue" {
		if _, _, err := readControl[Issue](ctx, c.store, in.ScopeID, "issue"); err != nil {
			return NotificationSilence{}, err
		}
	} else if err := c.notificationScopeExists(ctx, in.ScopeType, in.ScopeID); err != nil {
		return NotificationSilence{}, err
	}
	if !validateNoticeChoices(in.Events, notificationGroups) {
		return NotificationSilence{}, controlInvalid("events", "select supported event groups without duplicates")
	}
	if in.ExpiresAt.IsZero() {
		in.ExpiresAt = c.now().Add(time.Hour)
	}
	if !in.ExpiresAt.After(c.now()) || in.ExpiresAt.After(c.now().Add(7*24*time.Hour)) {
		return NotificationSilence{}, controlInvalid("expires_at", "silences must expire within seven days")
	}
	_, d, err := readControl[NotificationSilence](ctx, c.store, key, notificationSilenceKind)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return NotificationSilence{}, err
	}
	if d.Revision != expected {
		return NotificationSilence{}, storage.ErrPrecondition
	}
	value := NotificationSilence{NotificationSilenceInput: in, Actor: actor, Active: true}
	w, encodeErr := c.controlWrite(key, notificationSilenceKind, "", value, expected)
	batch := notificationBatch{}
	batch.prepared(w, encodeErr)
	if err = c.commitNotificationConfiguration(ctx, batch); err != nil {
		return value, err
	}
	value.ControlDocument = w.Document
	value.Revision = expected + 1
	return value, nil
}
func (c *Core) CancelNotificationSilence(ctx context.Context, key string, expected int64, actor string) (NotificationSilence, error) {
	if actor == "" {
		return NotificationSilence{}, storage.ErrConflict
	}
	value, d, err := readControl[NotificationSilence](ctx, c.store, key, notificationSilenceKind)
	if err != nil {
		return value, err
	}
	if d.Revision != expected {
		return value, storage.ErrPrecondition
	}
	now := c.now()
	value.CancelledAt, value.CancelledBy, value.Active = &now, actor, false
	w, encodeErr := c.controlWrite(key, notificationSilenceKind, "", value, expected)
	batch := notificationBatch{}
	batch.prepared(w, encodeErr)
	if err = c.commitNotificationConfiguration(ctx, batch); err != nil {
		return value, err
	}
	value.ControlDocument = d
	value.Revision++
	return value, nil
}

func notificationProblem(kind string) bool {
	return strings.HasSuffix(kind, ".problem") || kind == "gateway.offline" || kind == "deployment.target_lost"
}

func (c *Core) silenceUntil(event NotificationEvent, scope AlertScope, silences []NotificationSilence) time.Time {
	var until time.Time
	for _, silence := range silences {
		if !silence.Active || len(silence.Events) > 0 && !slices.Contains(silence.Events, notificationGroup(event.Type)) {
			continue
		}
		match := silence.ScopeType == "global" || silence.ScopeType == "issue" && event.IssueID != "" && silence.ScopeID == event.IssueID || silence.ScopeType == "project" && scope.ProjectID != "" && silence.ScopeID == scope.ProjectID || silence.ScopeType == "environment" && scope.EnvironmentID != "" && silence.ScopeID == scope.EnvironmentID
		if match && silence.ExpiresAt.After(until) {
			until = silence.ExpiresAt
		}
	}
	return until
}
