package core

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// Installation ownership comes from a typed registration, never a system flag,
// Docker label, project name or an old Managed value.
func (c *Core) installationOwner(ctx context.Context) (bootstrap.Installation, error) {
	document, err := c.store.GetControlDocument(ctx, bootstrap.InstallationDocumentID)
	if errors.Is(err, storage.ErrNotFound) {
		return bootstrap.Installation{}, nil
	}
	if err != nil {
		return bootstrap.Installation{}, err
	}
	var value bootstrap.Installation
	if json.Unmarshal(document.Payload, &value) != nil || document.Kind != bootstrap.InstallationDocumentKind || document.ParentID != value.ID || bootstrap.ValidateInstallation(value) != nil {
		return value, storage.ErrConflict
	}
	return value, nil
}

func (c *Core) ApplicationManagement(ctx context.Context, values []model.Application) error {
	owner, err := c.installationOwner(ctx)
	if err != nil {
		return err
	}
	for n := range values {
		value := &values[n]
		value.ManagementMode = "observed"
		value.InstallationID = ""
		if !value.System {
			if value.Managed {
				value.ManagementMode = "application"
			}
			continue
		}
		component, exists := owner.Components[value.SystemKey]
		if owner.ID == "" || !exists || value.RuntimeID != owner.RuntimeID {
			continue
		}
		document, err := c.store.GetControlDocument(ctx, value.ID)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		var registered struct {
			ClusterID string   `json:"cluster_id"`
			RuntimeID string   `json:"runtime_id"`
			Objects   []string `json:"object_ids"`
		}
		if json.Unmarshal(document.Payload, &registered) != nil || document.Kind != "system-application" {
			return storage.ErrConflict
		}
		if registered.ClusterID == owner.ClusterID && registered.RuntimeID == owner.RuntimeID && registeredComponent(registered.Objects, component.ID, owner.Executor.ID, value.SystemKey) {
			value.ManagementMode = "system"
			value.InstallationID = owner.ID
		}
	}
	return nil
}

func (c *Core) ServiceManagement(ctx context.Context, values []model.Service) error {
	owner, err := c.installationOwner(ctx)
	if err != nil {
		return err
	}
	for n := range values {
		value := &values[n]
		value.ManagementMode = "observed"
		value.InstallationID = ""
		if !value.System {
			if value.Managed {
				value.ManagementMode = "application"
			}
			continue
		}
		if owner.ID == "" || value.RuntimeID != owner.RuntimeID {
			continue
		}
		components := []bootstrap.Resource{owner.Executor}
		for _, receipt := range owner.Components {
			components = append(components, receipt)
		}
		for _, receipt := range components {
			if value.RuntimeObjectID == receipt.ID {
				value.ManagementMode = "system"
				value.InstallationID = owner.ID
				break
			}
		}
	}
	return nil
}

func (c *Core) SystemInstallation(ctx context.Context) (bootstrap.Installation, error) {
	if c.Installation != nil {
		return c.Installation.View(ctx)
	}
	value, err := c.installationOwner(ctx)
	if err != nil {
		return value, err
	}
	value.AllowedOperations = []bootstrap.Action{}
	if value.ID == "" {
		value.Drift = []string{"awaiting_adoption"}
		value.Health = "UNVERIFIED"
		value.State = bootstrap.Unknown
	} else {
		value.Drift = append(value.Drift, "cli_preparation_required")
	}
	if value.Components == nil {
		value.Components = map[string]bootstrap.Resource{}
	}
	if value.Nodes == nil {
		value.Nodes = map[string]bootstrap.Identity{}
	}
	if value.ConsoleURLs == nil {
		value.ConsoleURLs = []string{}
	}
	if value.History == nil {
		value.History = []bootstrap.ReleaseReceipt{}
	}
	if value.Topology.NodeIDs == nil {
		value.Topology.NodeIDs = []string{}
	}
	return value, nil
}
func (c *Core) PlanInstallation(ctx context.Context, input bootstrap.PlanRequest) (bootstrap.Plan, error) {
	if c.Installation == nil {
		return bootstrap.Plan{}, &bootstrap.MaintenanceError{Code: "cli_preparation_required"}
	}
	return c.Installation.Plan(ctx, input)
}
func (c *Core) StartInstallation(ctx context.Context, input bootstrap.ExecuteRequest) (bootstrap.MaintenanceJob, error) {
	if c.Installation == nil {
		return bootstrap.MaintenanceJob{}, &bootstrap.MaintenanceError{Code: "cli_preparation_required"}
	}
	return c.Installation.Start(ctx, input)
}
func (c *Core) InstallationOperation(ctx context.Context, id string) (bootstrap.MaintenanceJob, error) {
	if c.Installation == nil {
		return bootstrap.MaintenanceJob{}, &bootstrap.MaintenanceError{Code: "cli_preparation_required"}
	}
	return c.Installation.Operation(ctx, id)
}

func registeredComponent(objects []string, component, executor, key string) bool {
	expected := map[string]bool{component: true}
	if key == "controller" && executor != "" {
		expected[executor] = true
	}
	if len(expected) != len(objects) {
		return false
	}
	for _, id := range objects {
		if !expected[id] {
			return false
		}
		delete(expected, id)
	}
	return len(expected) == 0
}
