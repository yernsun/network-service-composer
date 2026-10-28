package core

import (
	"context"
	"slices"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// A revision's scope describes the submission that created it, not a later
// START, RESTART or other lifecycle operation using the same revision.
func readinessOperationScope(op model.DeploymentOperation, revision model.ApplicationRevision) *model.ServiceDeploymentScope {
	if op.Type == "INSTALL" || op.Type == "UPGRADE" {
		return revision.ConfigurationSource.ServiceScope
	}
	return nil
}

func readinessServiceDeletion(op model.DeploymentOperation, revision model.ApplicationRevision) bool {
	scope := readinessOperationScope(op, revision)
	return scope != nil && scope.Mode == "delete"
}

func readinessServiceHead(revision model.ApplicationRevision, name string) string {
	if revision.ConfigurationSource.ServiceRevisions != nil {
		return revision.ConfigurationSource.ServiceRevisions[name]
	}
	// Legacy full revisions had no per-service map. A delete revision's frozen
	// scope still proves omission; later incremental revisions materialize heads.
	if scope := revision.ConfigurationSource.ServiceScope; scope != nil && scope.Mode == "delete" && slices.Contains(scope.Names, name) {
		return ""
	}
	return revision.ID
}

func (c *Core) readinessServiceHeads(ctx context.Context, baseline *readinessBaseline, op model.DeploymentOperation, app model.Application, requested model.ApplicationRevision, selected []model.Service) string {
	if app.ActiveRevisionID == "" && op.Status != model.OperationSucceeded {
		return ""
	}
	active, err := c.store.GetApplicationRevision(ctx, app.ActiveRevisionID)
	if err != nil || active.ApplicationID != app.ID {
		return "workload_unavailable"
	}
	desired := active
	if app.DesiredRevisionID != app.ActiveRevisionID {
		desired, err = c.store.GetApplicationRevision(ctx, app.DesiredRevisionID)
		if err != nil || desired.ApplicationID != app.ID {
			return "workload_unavailable"
		}
	}
	for _, service := range selected {
		name := applicationServiceNameForPolicy(service, app)
		current, target := readinessServiceHead(active, name), readinessServiceHead(requested, name)
		baseline.Objects = append(baseline.Objects, name+":active-head:"+current, name+":desired-head:"+readinessServiceHead(desired, name))
		if op.Status == model.OperationSucceeded && requested.ID != "" && current != target {
			return "workload_superseded"
		}
	}
	return ""
}

func readinessSelectedEntrypoint(revision model.ApplicationRevision, app model.Application, selected []model.Service) *model.ServiceEntrypoint {
	if len(selected) == 1 {
		name := applicationServiceNameForPolicy(selected[0], app)
		if intent, exists := revision.ConfigurationSource.EntrypointIntents[name]; exists {
			entry := intent.Entry
			return &entry
		}
	}
	entry := revision.ConfigurationSource.Entrypoint
	for _, service := range selected {
		if entry != nil && entry.Service == applicationServiceNameForPolicy(service, app) {
			return entry
		}
	}
	return nil
}

func (c *Core) readinessDeletedService(v *OperationReadiness, op model.DeploymentOperation, revision model.ApplicationRevision, runtime model.Runtime, selected []model.Service, presence storage.RuntimePresence, presenceErr error) {
	scope := revision.ConfigurationSource.ServiceScope
	if len(selected) != 1 || scope.TargetRuntimeObjectID == "" || op.FinishedAt == nil || presenceErr != nil || presence.RuntimeID != runtime.ID || presence.Observation < 1 || presence.Observation != runtime.AppliedObservation || !presence.ObservedAt.Equal(*runtime.LastSeenAt) || !presence.ObservedAt.After(*op.FinishedAt) {
		v.set("workload", ReadinessUnknown, "workload_unavailable")
		return
	}
	service := selected[0]
	if service.RuntimeObjectID != scope.TargetRuntimeObjectID || service.RemovedAt == nil || service.Status != model.ServiceRemoved || slices.Contains(presence.ServiceIDs, service.ID) {
		v.set("workload", ReadinessPending, "workload_converging")
		return
	}
	v.set("workload", ReadinessConfirmed, "")
	v.stage("workload").ObservedAt = runtime.LastSeenAt
}
