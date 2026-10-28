package core

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func (c *Core) collectNotifications(ctx context.Context, s notificationSettings, revision int64) error {
	var result error
	for group, collect := range map[string]func(context.Context, time.Time, int64) error{"images": c.imageNotifications, "deployments": c.deploymentNotifications, "certificates": c.certificateNotifications, "gateways": c.gatewayNotifications, "runtimes": c.runtimeNotifications} {
		if slices.Contains(s.Events, group) {
			result = errors.Join(result, collect(ctx, s.Since, revision))
		}
	}
	return result
}
func (c *Core) imageNotifications(ctx context.Context, since time.Time, revision int64) error {
	page := storage.Page{Limit: 200}
	for {
		docs, err := c.store.ListControlDocuments(ctx, "image-update", "", page)
		if err != nil {
			return err
		}
		for _, d := range docs {
			if d.CreatedAt.Before(since) {
				continue
			}
			var u ImageUpdate
			if err = json.Unmarshal(d.Payload, &u); err != nil {
				return err
			}
			e := NotificationEvent{ID: d.ID + ":available", Type: "image.available", ResourceID: d.ID, EnvironmentID: u.EnvironmentID, OccurredAt: d.CreatedAt}
			if err = c.enqueueNotification(ctx, e, revision); err != nil {
				return err
			}
			if u.Status == "FAILED" && u.OperationID == "" {
				e.ID = d.ID + ":submission-failed"
				e.Type = "image.failed"
				e.Status = "FAILED"
				e.OccurredAt = d.UpdatedAt
				if err = c.enqueueNotification(ctx, e, revision); err != nil {
					return err
				}
			}
		}
		if len(docs) < 200 {
			return nil
		}
		page.After = docs[len(docs)-1].ID
	}
}
func (c *Core) deploymentNotifications(ctx context.Context, since time.Time, revision int64) error {
	return errors.Join(c.deploymentResultNotifications(ctx, since, revision), c.targetNotifications(ctx, since, revision))
}

func (c *Core) deploymentResultNotifications(ctx context.Context, since time.Time, revision int64) error {
	page := storage.Page{Limit: 200}
	types := map[model.DeploymentOperationStatus]string{model.OperationSucceeded: "deployment.succeeded", model.OperationFailed: "deployment.failed", model.OperationRolledBack: "deployment.rolled_back", model.OperationRollbackFailed: "deployment.rollback_failed"}
	for {
		operations, err := c.store.ListDeploymentOperations(ctx, "", page)
		if err != nil {
			return err
		}
		for _, op := range operations {
			kind := types[op.Status]
			if op.Type == "DOMAIN_ATTACH" || kind == "" || op.FinishedAt == nil || op.FinishedAt.Before(since) {
				continue
			}
			e := NotificationEvent{ID: op.ID + ":" + string(op.Status), Type: kind, ResourceID: op.ApplicationID, OperationID: op.ID, Status: string(op.Status), OccurredAt: *op.FinishedAt}
			if err = c.enqueueNotification(ctx, e, revision); err != nil {
				return err
			}
		}
		if len(operations) < 200 {
			return nil
		}
		page.After = operations[len(operations)-1].ID
	}
}

func (c *Core) targetNotifications(ctx context.Context, since time.Time, revision int64) error {
	if err := c.refreshTargetIssues(ctx); err != nil {
		return err
	}
	for page := (storage.Page{Limit: 200}); ; {
		issues, err := c.Issues(ctx, page)
		if err != nil {
			return err
		}
		for _, issue := range issues {
			if issue.Code != "deployment_target_lost" || issue.ObservationState != "CONFIRMED" {
				continue
			}
			kind := "deployment.target_recovered"
			if issue.Active {
				kind = "deployment.target_lost"
			}
			event := NotificationEvent{ID: since.Format(time.RFC3339Nano), Type: kind, ResourceID: issue.ResourceID, Status: issue.ObservationState, OperationID: issue.OperationID}
			if err := c.observeNotification(ctx, event, issue.Active, revision); err != nil {
				return err
			}
		}
		if len(issues) < page.Limit {
			return nil
		}
		page.After = issues[len(issues)-1].ID
	}
}
func (c *Core) certificateNotifications(ctx context.Context, since time.Time, revision int64) error {
	page := storage.Page{Limit: 200}
	for {
		certs, err := c.store.ListCertificates(ctx, page)
		if err != nil {
			return err
		}
		for _, cert := range certs {
			bad, known := certificateCondition(cert, c.now())
			if !known {
				continue
			}
			kind := "certificate.recovered"
			if bad {
				kind = "certificate.problem"
			}
			e := NotificationEvent{ID: since.Format(time.RFC3339Nano), Type: kind, ResourceID: cert.ID, Status: string(cert.Status)}
			if err = c.observeNotification(ctx, e, bad, revision); err != nil {
				return err
			}
		}
		if len(certs) < 200 {
			return nil
		}
		page.After = certs[len(certs)-1].ID
	}
}
func (c *Core) gatewayNotifications(ctx context.Context, since time.Time, revision int64) error {
	page := storage.Page{Limit: 200}
	for {
		gateways, err := c.store.ListGateways(ctx, page)
		if err != nil {
			return err
		}
		for _, gateway := range gateways {
			condition, _ := c.gatewayCondition(gateway)
			if condition == nil {
				continue
			}
			bad := *condition
			kind := "gateway.recovered"
			if bad {
				kind = "gateway.offline"
			}
			e := NotificationEvent{ID: since.Format(time.RFC3339Nano), Type: kind, ResourceID: gateway.ID, Status: string(gateway.Status)}
			if err = c.observeNotification(ctx, e, bad, revision); err != nil {
				return err
			}
		}
		if len(gateways) < 200 {
			return nil
		}
		page.After = gateways[len(gateways)-1].ID
	}
}
