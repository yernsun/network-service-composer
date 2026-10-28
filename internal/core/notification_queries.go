package core

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/yernsun/network-service-composer/internal/storage"
)

type NotificationPreviewInput struct {
	IssueID        string `json:"issue_id"`
	NotificationID string `json:"notification_id"`
}
type NotificationRoutingPreview struct {
	Event            NotificationEvent        `json:"event"`
	Matches          []NotificationRouteMatch `json:"matches"`
	SilencedScopes   []AlertScope             `json:"silenced_scopes"`
	GloballySilenced bool                     `json:"globally_silenced"`
}

func (c *Core) PreviewNotificationRouting(ctx context.Context, in NotificationPreviewInput) (NotificationRoutingPreview, error) {
	result := NotificationRoutingPreview{Matches: []NotificationRouteMatch{}, SilencedScopes: []AlertScope{}}
	if (in.IssueID == "") == (in.NotificationID == "") {
		return result, controlInvalid("issue_id", "select exactly one stored issue or delivery")
	}
	if in.IssueID != "" {
		issue, _, err := readControl[Issue](ctx, c.store, in.IssueID, "issue")
		if err != nil {
			return result, err
		}
		types := map[string]string{"certificate_risk": "certificate.problem", "gateway_offline": "gateway.offline", "runtime_unavailable": "runtime.problem", "deployment_target_lost": "deployment.target_lost"}
		kind := types[issue.Code]
		if kind == "" {
			return result, controlInvalid("issue_id", "this issue has no outbound event group")
		}
		result.Event = NotificationEvent{ID: issue.ID, Type: kind, ResourceID: issue.ResourceID, IssueID: issue.ID, Occurrence: issue.Occurrences, Severity: issue.Severity, OccurredAt: c.now(), OperationID: issue.OperationID}
	} else {
		value, _, err := readControl[Notification](ctx, c.store, in.NotificationID, notificationDeliveryKind)
		if err != nil {
			return result, err
		}
		result.Event = value.Event
	}
	if err := c.notificationScopes(ctx, &result.Event); err != nil {
		return result, err
	}
	silences, err := c.ListNotificationSilences(ctx)
	if err != nil {
		return result, err
	}
	allowed := []AlertScope{}
	for _, scope := range result.Event.Scopes {
		if c.silenceUntil(result.Event, scope, silences).IsZero() {
			allowed = append(allowed, scope)
		} else {
			result.SilencedScopes = append(result.SilencedScopes, scope)
		}
	}
	result.GloballySilenced = len(result.Event.Scopes) == 0 && !c.silenceUntil(result.Event, AlertScope{}, silences).IsZero()
	if !result.GloballySilenced && (len(result.Event.Scopes) == 0 || len(allowed) > 0) {
		event := result.Event
		notificationEventScopes(&event, allowed)
		result.Matches, err = c.matchNotificationRoutes(ctx, event)
	}
	return result, err
}

type NotificationHealth struct {
	Initialized bool                            `json:"initialized"`
	Sources     []notificationCollectionHealth  `json:"sources"`
	Queue       []storage.NotificationWorkCount `json:"queue"`
}

func (c *Core) NotificationHealth(ctx context.Context) (NotificationHealth, error) {
	result := NotificationHealth{Sources: []notificationCollectionHealth{}, Queue: []storage.NotificationWorkCount{}}
	state, _, err := c.notificationRoutingState(ctx)
	if err != nil {
		return result, err
	}
	result.Initialized = state.Initialized
	docs, err := c.store.ListControlDocuments(ctx, "notification-source-health", "", storage.Page{Limit: 200})
	if err != nil {
		return result, err
	}
	for _, doc := range docs {
		var value notificationCollectionHealth
		if err := json.Unmarshal(doc.Payload, &value); err != nil {
			return result, err
		}
		value.State = "UNKNOWN"
		if value.Failed {
			value.State = "FAILED"
		} else if haFresh(c.now(), value.SucceededAt, 2*time.Minute) && haFresh(c.now(), &value.AttemptedAt, 2*time.Minute) {
			value.State = "CURRENT"
		}
		result.Sources = append(result.Sources, value)
	}
	result.Queue, err = c.store.NotificationWorkCounts(ctx)
	return result, err
}

type NotificationFilter struct{ ReceiverID, IssueID, EnvironmentID, ProjectID, Status, EventID, ResourceID string }

func (c *Core) ListNotificationsFiltered(ctx context.Context, page storage.Page, filter NotificationFilter) ([]Notification, error) {
	result := []Notification{}
	limit := page.Limit
	if limit < 1 || limit > 200 {
		limit = 200
	}
	for scan := (storage.Page{Limit: 200, After: page.After}); ; {
		rows, err := c.ListNotifications(ctx, scan)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if filter.ReceiverID != "" && row.ReceiverID != filter.ReceiverID || filter.IssueID != "" && row.Event.IssueID != filter.IssueID || filter.Status != "" && row.Status != filter.Status || filter.EventID != "" && row.EventKey != filter.EventID || filter.ResourceID != "" && row.Event.ResourceID != filter.ResourceID {
				continue
			}
			if filter.EnvironmentID != "" || filter.ProjectID != "" {
				scopes := row.Event.Scopes
				if row.Legacy {
					scopes = row.LegacyScopes
				}
				if !slices.ContainsFunc(scopes, func(scope AlertScope) bool {
					return (filter.ProjectID == "" || scope.ProjectID == filter.ProjectID) && (filter.EnvironmentID == "" || scope.EnvironmentID == filter.EnvironmentID)
				}) {
					continue
				}
			}
			result = append(result, row)
			if len(result) == limit {
				return result, nil
			}
		}
		if len(rows) < scan.Limit {
			return result, nil
		}
		scan.After = rows[len(rows)-1].ID
	}
}
