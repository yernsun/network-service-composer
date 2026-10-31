package install

import (
	"context"
	"fmt"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

func (i *Installer) maintenancePlan(ctx context.Context, action bootstrap.Action, o Observation) (bootstrap.Plan, error) {
	if action == bootstrap.Verify {
		return bootstrap.NewPlan(*i.Record, action, o, []bootstrap.Step{{Key: "handoff", Description: "Verify installed identities, Manager evidence, loaded configuration and access"}}, timeNow())
	}
	return bootstrap.Plan{}, &bootstrap.Rejected{Reason: "this installation action requires the maintenance implementation"}
}

func (i *Installer) applyMaintenance(_ context.Context, _ bootstrap.Step) (bootstrap.Resource, error) {
	return bootstrap.Resource{}, &bootstrap.Rejected{Reason: fmt.Sprint("unsupported installation step")}
}
