package install

import (
	"context"
	"fmt"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

func (i *Installer) maintenancePlan(ctx context.Context, action bootstrap.Action, o Observation) (bootstrap.Plan, error) {
	if action == bootstrap.Adopt {
		if i.Config.Adoption == nil || o.Adoption == nil {
			return bootstrap.Plan{}, &bootstrap.Rejected{Reason: "adoption requires verified pre-existing registration"}
		}
		p, err := bootstrap.NewPlan(*i.Record, action, o, []bootstrap.Step{{Key: "adopt", Description: "Register the existing installation without changing services, credentials, administrator or data", Before: &bootstrap.Resource{Kind: "adoption-proof", ID: i.Record.ID, SpecDigest: o.Adoption.proof()}}}, timeNow())
		p = i.scopePlan(p, o)
		p.Scope.DataVolume = o.Adoption.Installation.Database.Volume
		p.Scope.Images = bootstrap.Images{Controller: o.Adoption.Installation.Components["controller"].Image, Gateway: o.Adoption.Installation.Components["nginx"].Image, PostgreSQL: o.Adoption.Installation.Database.Image}
		return p.WithScope(p.Scope), err
	}
	if action == bootstrap.Verify {
		return bootstrap.NewPlan(*i.Record, action, o, []bootstrap.Step{{Key: "handoff", Description: "Verify installed identities, Manager evidence, loaded configuration and access"}}, timeNow())
	}
	return bootstrap.Plan{}, &bootstrap.Rejected{Reason: "this installation action requires the maintenance implementation"}
}

func (i *Installer) applyMaintenance(ctx context.Context, step bootstrap.Step) (bootstrap.Resource, error) {
	if step.Key == "adopt" && i.Config.Adoption != nil {
		return i.applyAdoption(ctx)
	}
	return bootstrap.Resource{}, &bootstrap.Rejected{Reason: fmt.Sprint("unsupported installation step")}
}
