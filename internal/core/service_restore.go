package core

import (
	"context"
	"strings"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func (c *Core) serviceRestoreTarget(ctx context.Context, serviceID string) (model.Service, string, error) {
	service, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return service, "", err
	}
	if err = c.store.CheckProjectResource(ctx, service.ID); err != nil {
		return service, "", err
	}
	if service.System || !service.Managed || service.ApplicationID == "" {
		return service, "", storage.ErrSystemProtected
	}
	app, err := c.store.GetApplication(ctx, service.ApplicationID)
	if err != nil {
		return service, "", err
	}
	if app.System || !app.Managed || app.RuntimeID != service.RuntimeID {
		return service, "", storage.ErrSystemProtected
	}
	if app.Status != model.ApplicationRunning {
		return service, "", storage.ErrPrecondition
	}
	name := strings.TrimPrefix(service.ServiceKey, app.ProjectName+"/")
	name = strings.TrimPrefix(name, app.ProjectName+"_")
	if name == service.ServiceKey || name == "" {
		return service, "", storage.ErrConflict
	}
	return service, name, nil
}

func (c *Core) ReviewServiceRestore(ctx context.Context, serviceID, revisionID string) (ApplicationRestoreReview, error) {
	service, name, err := c.serviceRestoreTarget(ctx, serviceID)
	if err != nil {
		return ApplicationRestoreReview{}, err
	}
	return c.ReviewApplicationRestore(ctx, service.ApplicationID, revisionID, name, service.ID)
}

func (c *Core) RestoreService(ctx context.Context, serviceID, reviewID, actor, idem string, expected int64, dataBoundary bool) (model.DeploymentOperation, error) {
	service, name, err := c.serviceRestoreTarget(ctx, serviceID)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	var saved savedApplicationRestore
	doc, err := c.readEncrypted(ctx, reviewID, "application-restore", &saved)
	if err != nil {
		return model.DeploymentOperation{}, err
	}
	if doc.ParentID != service.ApplicationID || saved.ServiceID != service.ID || saved.ServiceRevision != service.Revision || saved.Source == nil ||
		(saved.Source.ServiceScope != nil && (len(saved.Source.ServiceScope.Names) != 1 || saved.Source.ServiceScope.Names[0] != name)) {
		return model.DeploymentOperation{}, storage.ErrPrecondition
	}
	return c.RestoreApplication(ctx, service.ApplicationID, reviewID, actor, idem, expected, dataBoundary, service.ID)
}
