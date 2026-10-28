package core

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type DomainDraftInput struct {
	Domain    DomainConfigInput `json:"domain"`
	ServiceID string            `json:"service_id,omitempty"`
	Route     RouteConfigInput  `json:"route"`
}

type DomainDraft struct {
	storage.ControlDocument
	ProjectID         string            `json:"project_id"`
	EnvironmentID     string            `json:"environment_id"`
	Status            string            `json:"status"`
	Domain            DomainConfigInput `json:"domain"`
	ServiceID         string            `json:"service_id,omitempty"`
	Route             RouteConfigInput  `json:"route"`
	PublishedDomainID string            `json:"published_domain_id,omitempty"`
	PublishedRouteID  string            `json:"published_route_id,omitempty"`
	OperationID       string            `json:"operation_id,omitempty"`
}

func (c *Core) validateDomainDraft(ctx context.Context, environmentID string, input *DomainDraftInput) (Environment, error) {
	env, err := c.GetEnvironment(ctx, environmentID)
	if err != nil {
		return env, err
	}
	if env.System {
		return env, storage.ErrSystemProtected
	}
	runtime, err := c.store.GetRuntime(ctx, env.RuntimeID)
	if err != nil {
		return env, err
	}
	if _, _, err = c.buildDomain(ctx, runtime.PoolID, input.Domain, c.now()); err != nil {
		return env, err
	}
	if input.Route.Path == "" {
		input.Route.Path = "/"
	}
	if input.Route.Path, err = NormalizePath(input.Route.Path); err != nil {
		return env, err
	}
	if input.ServiceID != "" {
		service, err := c.store.GetService(ctx, input.ServiceID)
		if err != nil {
			return env, err
		}
		if service.RuntimeID != env.RuntimeID || service.System {
			return env, invalid("validation.binding_service_mismatch", "service_id", "service must belong to this environment runtime")
		}
		owners, err := c.store.ServiceEnvironments(ctx, []string{service.ID})
		if err != nil {
			return env, err
		}
		if owners[service.ID].ID != env.ID {
			return env, invalid("validation.binding_service_mismatch", "service_id", "service must belong to this environment")
		}
		input.Route.Backend.ServiceID = service.ID
	}
	return env, nil
}

func (c *Core) CreateDomainDraft(ctx context.Context, environmentID string, input DomainDraftInput) (DomainDraft, error) {
	env, err := c.validateDomainDraft(ctx, environmentID, &input)
	if err != nil {
		return DomainDraft{}, err
	}
	value := DomainDraft{ProjectID: env.ProjectID, EnvironmentID: env.ID, Status: "draft", Domain: input.Domain, ServiceID: input.ServiceID, Route: input.Route}
	value.ID = id.New("ddraft")
	if err = c.saveControl(ctx, value.ID, "domain-draft", env.ID, value, 0); err != nil {
		return DomainDraft{}, err
	}
	return c.GetDomainDraft(ctx, value.ID)
}

func (c *Core) GetDomainDraft(ctx context.Context, key string) (DomainDraft, error) {
	value, doc, err := readControl[DomainDraft](ctx, c.store, key, "domain-draft")
	value.ControlDocument = doc
	return value, err
}

func (c *Core) ListDomainDrafts(ctx context.Context, environmentID string, page storage.Page) ([]DomainDraft, error) {
	if _, err := c.GetEnvironment(ctx, environmentID); err != nil {
		return nil, err
	}
	docs, err := c.store.ListControlDocuments(ctx, "domain-draft", environmentID, page)
	if err != nil {
		return nil, err
	}
	values := make([]DomainDraft, 0, len(docs))
	for _, doc := range docs {
		var value DomainDraft
		if err = json.Unmarshal(doc.Payload, &value); err != nil {
			return nil, err
		}
		value.ControlDocument = doc
		values = append(values, value)
	}
	return values, nil
}

func (c *Core) UpdateDomainDraft(ctx context.Context, key string, input DomainDraftInput, expected int64) (DomainDraft, error) {
	current, err := c.GetDomainDraft(ctx, key)
	if err != nil {
		return DomainDraft{}, err
	}
	if current.Status != "draft" {
		return DomainDraft{}, storage.ErrConflict
	}
	if _, err = c.validateDomainDraft(ctx, current.EnvironmentID, &input); err != nil {
		return DomainDraft{}, err
	}
	current.Domain, current.ServiceID, current.Route = input.Domain, input.ServiceID, input.Route
	if err = c.saveControl(ctx, key, "domain-draft", current.EnvironmentID, current, expected); err != nil {
		return DomainDraft{}, err
	}
	return c.GetDomainDraft(ctx, key)
}

func (c *Core) DeleteDomainDraft(ctx context.Context, key string, expected int64) error {
	value, err := c.GetDomainDraft(ctx, key)
	if err != nil {
		return err
	}
	if value.Status != "draft" {
		return storage.ErrConflict
	}
	return c.store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: value.ControlDocument, Expected: expected, Remove: true}})
}

func (c *Core) PublishDomainDraft(ctx context.Context, key string, expected int64) (DomainDraft, error) {
	draft, err := c.GetDomainDraft(ctx, key)
	if err != nil {
		return DomainDraft{}, err
	}
	if draft.Revision != expected {
		return DomainDraft{}, storage.ErrPrecondition
	}
	if draft.Status == "published" {
		return draft, nil
	}
	if draft.Status != "draft" || strings.TrimSpace(draft.ServiceID) == "" {
		return DomainDraft{}, invalid("validation.binding_service_mismatch", "service_id", "select an environment service before publishing")
	}
	input := DomainDraftInput{Domain: draft.Domain, ServiceID: draft.ServiceID, Route: draft.Route}
	env, err := c.validateDomainDraft(ctx, draft.EnvironmentID, &input)
	if err != nil {
		return DomainDraft{}, err
	}
	if env.ProjectID != draft.ProjectID {
		return DomainDraft{}, storage.ErrConflict
	}
	runtime, err := c.store.GetRuntime(ctx, env.RuntimeID)
	if err != nil {
		return DomainDraft{}, err
	}
	targets, err := c.store.ListGatewayDNSTargets(ctx, runtime.PoolID)
	if err != nil {
		return DomainDraft{}, err
	}
	if len(targets) == 0 {
		return DomainDraft{}, invalid("validation.environment_gateway", "gateways", "an enabled gateway with a DNS target is required before publishing")
	}
	_, _, err = c.createDomainBinding(ctx, draft.ServiceID, CreateDomainBindingInput{Domain: &draft.Domain, Route: draft.Route, IdempotencyKey: draft.ID}, &draft)
	if err != nil {
		return DomainDraft{}, err
	}
	return c.GetDomainDraft(ctx, key)
}
