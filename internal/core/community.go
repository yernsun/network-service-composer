package core

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type GatewayEvidence = model.GatewayEvidence

func (c *Core) RecordGatewayEvidence(ctx context.Context, gateway string, v GatewayEvidence) error {
	if v.ObservedAt.Before(time.Unix(1, 0)) {
		return nil
	}
	if v.RefreshIntervalMillis < 100 || v.RefreshIntervalMillis > 300000 {
		return controlInvalid("evidence", "invalid refresh interval")
	}
	if v.StartupState != "CONFIRMED" && v.StartupState != "RISK" && v.StartupState != "UNKNOWN" {
		return controlInvalid("evidence", "invalid startup state")
	}
	previous, d, err := readControl[GatewayEvidence](ctx, c.store, gateway+":evidence", "gateway-evidence")
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if !previous.ObservedAt.IsZero() && !v.ObservedAt.After(previous.ObservedAt) {
		return nil
	}
	v.ReceivedAt = c.now()
	v.LastConfirmedAt, v.LastConfirmedRevision = previous.LastConfirmedAt, previous.LastConfirmedRevision
	if v.LoadingConfirmed {
		now := c.now()
		v.LastConfirmedAt, v.LastConfirmedRevision = &now, v.ConfirmedRevision
	}
	err = c.saveControl(ctx, gateway+":evidence", "gateway-evidence", gateway, v, d.Revision)
	if errors.Is(err, storage.ErrPrecondition) || errors.Is(err, storage.ErrConflict) {
		return nil
	}
	return err
}

func (c *Core) GatewayEvidence(ctx context.Context, gateway string) (GatewayEvidence, error) {
	g, err := c.store.GetGateway(ctx, gateway)
	if err != nil {
		return GatewayEvidence{}, err
	}
	v, _, err := readControl[GatewayEvidence](ctx, c.store, gateway+":evidence", "gateway-evidence")
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return v, err
	}
	v.ConfigurationState = "UNKNOWN"
	if v.StartupState == "" {
		v.StartupState = "UNKNOWN"
	}
	if v.RefreshIntervalMillis == 0 || !haFresh(c.now(), &v.ObservedAt, 3*time.Duration(v.RefreshIntervalMillis)*time.Millisecond) || !haFresh(c.now(), &v.ReceivedAt, 3*time.Duration(v.RefreshIntervalMillis)*time.Millisecond) || !haFresh(c.now(), g.LastSeenAt, c.config.Controller.Gateway.OfflineTimeout.Std()) {
		v.LoadingConfirmed = false
		if v.StartupState != "RISK" {
			v.StartupState = "UNKNOWN"
		}
		return v, nil
	}
	if v.DiskDigest != "" && v.DiskDigest != v.RenderedDigest {
		v.ConfigurationState = "EXTERNAL_CHANGE"
	} else if v.LoadingConfirmed {
		if v.ConfirmedRevision == g.DesiredRevision {
			v.ConfigurationState = "CONFIRMED"
		} else {
			v.ConfigurationState = "MISMATCH"
		}
	}
	return v, nil
}

type Issue struct {
	storage.ControlDocument
	Code                string       `json:"code"`
	ResourceType        string       `json:"resource_type"`
	ResourceID          string       `json:"resource_id"`
	Severity            string       `json:"severity"`
	Active              bool         `json:"active"`
	Acknowledged        bool         `json:"acknowledged"`
	FirstSeenAt         time.Time    `json:"first_seen_at"`
	LastSeenAt          time.Time    `json:"last_seen_at"`
	ResolvedAt          *time.Time   `json:"resolved_at,omitempty"`
	Occurrences         int64        `json:"occurrences"`
	Link                string       `json:"link"`
	Scopes              []AlertScope `json:"scopes"`
	ObservationState    string       `json:"observation_state"`
	OccurrenceStartedAt *time.Time   `json:"occurrence_started_at,omitempty"`
	AcknowledgedBy      string       `json:"acknowledged_by,omitempty"`
	AcknowledgedAt      *time.Time   `json:"acknowledged_at,omitempty"`
	ResponsibleUserID   string       `json:"responsible_user_id,omitempty"`
	OperationID         string       `json:"operation_id,omitempty"`
}

func (c *Core) observeIssue(ctx context.Context, code, kind, key, link string, active bool) error {
	return c.observeIssueState(ctx, code, kind, key, link, &active, "CONFIRMED", "")
}

// A missing observation may withdraw health evidence, but cannot resolve a fault.
func (c *Core) observeIssueState(ctx context.Context, code, kind, key, link string, active *bool, state, operation string) error {
	_, write, err := c.issueObservation(ctx, code, kind, key, link, active, state, operation)
	if err != nil || write == nil {
		return err
	}
	err = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{*write})
	if errors.Is(err, storage.ErrPrecondition) || errors.Is(err, storage.ErrConflict) {
		return nil
	}
	return err
}

func (c *Core) issueObservation(ctx context.Context, code, kind, key, link string, active *bool, state, operation string) (Issue, *storage.ControlWrite, error) {
	id := "issue_" + digestString(code+":"+kind+":"+key)
	v, d, err := readControl[Issue](ctx, c.store, id, "issue")
	if errors.Is(err, storage.ErrNotFound) {
		if active == nil || !*active {
			return Issue{}, nil, nil
		}
		v = Issue{Code: code, ResourceType: kind, ResourceID: key, Severity: "warning", FirstSeenAt: c.now(), Link: link}
		if code == "runtime_unavailable" || code == "gateway_offline" || code == "deployment_target_lost" {
			v.Severity = "critical"
		}
	} else if err != nil {
		return v, nil, err
	}
	v.ControlDocument = d
	v.ID = id
	if active != nil && !*active && !v.Active {
		return v, nil, nil
	}
	if active != nil && *active {
		if !v.Active {
			v.Occurrences++
			v.Acknowledged = false
			v.AcknowledgedBy, v.AcknowledgedAt = "", nil
			now := c.now()
			v.OccurrenceStartedAt = &now
		}
		v.LastSeenAt = c.now()
		v.ResolvedAt = nil
	} else if active != nil {
		now := c.now()
		v.ResolvedAt = &now
	}
	if active != nil {
		v.Active = *active
	}
	v.ObservationState = state
	if active != nil || operation != "" {
		v.OperationID = operation
	}
	v.Link = link
	v.Scopes, err = c.alertScopes(ctx, kind, key)
	if err != nil {
		return v, nil, err
	}
	w, err := c.controlWrite(id, "issue", kind, v, d.Revision)
	v.ControlDocument = d
	v.ID = id
	return v, &w, err
}

func (c *Core) Issues(ctx context.Context, page storage.Page) ([]Issue, error) {
	docs, err := c.store.ListControlDocuments(ctx, "issue", "", page)
	values := make([]Issue, 0, len(docs))
	for _, d := range docs {
		var v Issue
		if e := json.Unmarshal(d.Payload, &v); e != nil {
			return nil, e
		}
		v.ControlDocument = d
		values = append(values, v)
	}
	return values, err
}

func (c *Core) AcknowledgeIssue(ctx context.Context, key string, expected int64, actor ...string) (Issue, error) {
	v, d, err := readControl[Issue](ctx, c.store, key, "issue")
	if err != nil {
		return v, err
	}
	if expected != d.Revision {
		return v, storage.ErrPrecondition
	}
	v.Acknowledged = true
	now := c.now()
	v.AcknowledgedAt = &now
	if len(actor) > 0 {
		v.AcknowledgedBy = actor[0]
	}
	err = c.saveControl(ctx, key, "issue", d.ParentID, v, expected)
	v.ControlDocument = d
	v.Revision++
	return v, err
}

func (c *Core) AssignIssue(ctx context.Context, key, userID string, expected int64) (Issue, error) {
	if userID != "" {
		user, err := c.User(ctx, userID)
		if err != nil {
			return Issue{}, err
		}
		if !user.Enabled {
			return Issue{}, controlInvalid("user_id", "responsible user must be enabled")
		}
	}
	v, d, err := readControl[Issue](ctx, c.store, key, "issue")
	if err != nil {
		return v, err
	}
	v.ResponsibleUserID = userID
	err = c.saveControl(ctx, key, "issue", d.ParentID, v, expected)
	v.ControlDocument = d
	v.Revision++
	return v, err
}

// Each scan updates durable problems without interpreting acknowledgement as
// resolution. Missing evidence remains a problem even when transport is online.
func (c *Core) RefreshCommunityIssues(ctx context.Context) error {
	return errors.Join(c.refreshGatewayCertificateIssues(ctx), c.refreshRuntimeIssues(ctx), c.refreshTargetIssues(ctx))
}

func (c *Core) refreshGatewayCertificateIssues(ctx context.Context) error {
	page := storage.Page{Limit: 200}
	for {
		gateways, err := c.store.ListGateways(ctx, page)
		if err != nil {
			return err
		}
		for _, g := range gateways {
			condition, state := c.gatewayCondition(g)
			if err := c.observeIssueState(ctx, "gateway_offline", "gateway", g.ID, alertLink("gateway", g.ID), condition, state, ""); err != nil {
				return err
			}
			e, err := c.GatewayEvidence(ctx, g.ID)
			if err != nil {
				return err
			}
			for code, active := range map[string]bool{"gateway_unknown": e.ConfigurationState == "UNKNOWN", "startup_risk": e.StartupState == "RISK", "external_change": e.ConfigurationState == "EXTERNAL_CHANGE"} {
				condition := &active
				if !g.Enabled {
					condition = nil
				}
				if err = c.observeIssueState(ctx, code, "gateway", g.ID, alertLink("gateway", g.ID), condition, state, ""); err != nil {
					return err
				}
			}
		}
		if len(gateways) < 200 {
			break
		}
		page.After = gateways[len(gateways)-1].ID
	}
	page = storage.Page{Limit: 200}
	for {
		docs, err := c.store.ListControlDocuments(ctx, "nginx-release", "", page)
		if err != nil {
			return err
		}
		for _, d := range docs {
			v, err := c.GetNginxRelease(ctx, d.ParentID, d.ID)
			if err != nil {
				return err
			}
			active := v.Status == "UNKNOWN" || v.Status == "PARTIAL" || v.Status == "FAILED" || v.Status == "STOPPED"
			if err = c.observeIssue(ctx, "release_attention", "release", d.ID, "/ui/nginx?pool_id="+v.PoolID+"&release_id="+v.ID, active); err != nil {
				return err
			}
		}
		if len(docs) < 200 {
			break
		}
		page.After = docs[len(docs)-1].ID
	}
	page = storage.Page{Limit: 200}
	for {
		certs, err := c.store.ListCertificates(ctx, page)
		if err != nil {
			return err
		}
		for _, cert := range certs {
			active, known := certificateCondition(cert, c.now())
			condition := &active
			state := "CONFIRMED"
			if !known {
				condition, state = nil, "UNKNOWN"
			}
			if err = c.observeIssueState(ctx, "certificate_risk", "certificate", cert.ID, alertLink("certificate", cert.ID), condition, state, ""); err != nil {
				return err
			}
		}
		if len(certs) < 200 {
			break
		}
		page.After = certs[len(certs)-1].ID
	}
	return nil
}

type CommunityMonitor struct{ Core *Core }

func (m CommunityMonitor) Run(ctx context.Context) error {
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		if err := m.Core.RefreshCommunityIssues(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.ErrorContext(ctx, "refresh community issues", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}
