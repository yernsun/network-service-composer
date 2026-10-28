package core

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

// Capability configuration is not a live Docker, Gateway or client health check.
type EnvironmentCapabilities struct {
	EnvironmentID string             `json:"environment_id"`
	RuntimeID     string             `json:"runtime_id"`
	Management    Capability         `json:"management"`
	ImageUpdates  Capability         `json:"image_updates"`
	BaseRevision  string             `json:"base_revision_id,omitempty"`
	Services      []ImageServiceBase `json:"services"`
	Certificates  Capability         `json:"certificates"`
	ACME          Capability         `json:"acme"`
	Notifications Capability         `json:"notifications"`
	HA            Capability         `json:"ha"`
}

type Capability struct {
	State           string     `json:"state"`
	Reason          string     `json:"reason,omitempty"`
	ObservedAt      *time.Time `json:"observed_at,omitempty"`
	LastSucceededAt *time.Time `json:"last_succeeded_at,omitempty"`
}

type ImageServiceBase struct {
	Name      string `json:"name"`
	Reference string `json:"reference"`
	Image     string `json:"image"`
}

func (c *Core) GetEnvironmentCapabilities(ctx context.Context, key string) (EnvironmentCapabilities, error) {
	env, err := c.GetEnvironment(ctx, key)
	if err != nil {
		return EnvironmentCapabilities{}, err
	}
	result := EnvironmentCapabilities{EnvironmentID: key, RuntimeID: env.RuntimeID,
		Management: Capability{State: "CONFIGURED"}, ImageUpdates: Capability{State: "BLOCKED", Reason: "baseline_required"}, Services: []ImageServiceBase{}}
	if err := c.environmentDependencies(ctx, env, &result); err != nil {
		return result, err
	}
	if env.System {
		result.Management = Capability{State: "BLOCKED", Reason: "system_resource"}
	} else if _, err = c.requireManagement(ctx, env.RuntimeID); err != nil {
		var validation ValidationError
		if !errors.As(err, &validation) {
			return result, err
		}
		result.Management = Capability{State: "BLOCKED", Reason: validation.Code}
	}
	if result.Management.State == "BLOCKED" {
		result.ImageUpdates = result.Management
		return result, nil
	}
	if env.ApplicationID == "" {
		result.ImageUpdates.Reason = "application_required"
		return result, nil
	}
	app, err := c.store.GetApplication(ctx, env.ApplicationID)
	if err != nil {
		return result, err
	}
	if !app.Managed {
		result.ImageUpdates.Reason = "adoption_required"
		return result, nil
	}
	if app.ActiveRevisionID == "" {
		return result, nil
	}
	_, revision, _, err := c.activeEnvironmentRevision(ctx, env)
	if err != nil {
		var validation ValidationError
		if errors.As(err, &validation) && validation.Field == "baseline" {
			return result, nil
		}
		return result, err
	}
	for name, reference := range revision.ConfigurationSource.TaggedImages {
		if strings.Contains(reference, "@sha256:") {
			continue
		}
		result.Services = append(result.Services, ImageServiceBase{Name: name, Reference: reference, Image: revision.ConfigurationSource.Images[name]})
	}
	sort.Slice(result.Services, func(i, j int) bool { return result.Services[i].Name < result.Services[j].Name })
	if len(result.Services) == 0 {
		result.ImageUpdates.Reason = "tracked_tag_required"
		return result, nil
	}
	result.BaseRevision = revision.ID
	result.ImageUpdates = Capability{State: "BASELINE_READY"}
	return result, nil
}
