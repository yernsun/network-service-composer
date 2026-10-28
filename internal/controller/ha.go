package controller

import (
	"context"
	"time"

	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/core"
)

func (c *Controller) haController(ctx context.Context) core.HAEvidence {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	item := core.HAEvidence{ID: c.config.Controller.InstanceID, InstanceID: c.config.Controller.InstanceID, State: "UNKNOWN", Reason: "controller_evidence_unavailable", Source: "live_term_and_readiness"}
	if c.config.Controller.RecoveryMode {
		item.State, item.Reason = "DISABLED", "recovery_mode"
		return item
	}
	if !c.ready.Load() || c.election == nil {
		return item
	}
	prepared, err := c.store.RecoveryPrepared(ctx)
	if err != nil || prepared {
		item.Reason = "recovery_or_storage_unavailable"
		return item
	}
	leader, err := c.election.Store.Current(ctx)
	if err != nil || c.election.Store.Validate(ctx, leader.ID, leader.Term) != nil {
		item.Reason = "leader_unavailable"
		return item
	}
	item.Term = leader.Term
	if leader.ID == c.config.Controller.InstanceID {
		active := c.election.Active()
		if active == nil || active.State.Term != leader.Term || c.schedulerTerm.Load() != leader.Term || active.Check(ctx) != nil {
			item.Reason = "scheduler_unavailable"
			return item
		}
		item.Reason = "leader_scheduling"
	} else {
		forwarder := controlplane.Forwarder{Election: c.election, TLS: c.internalTLS}
		if forwarder.ProbeForwarding(ctx, leader) != nil {
			item.Reason = "leader_forwarding_unavailable"
			return item
		}
		item.Reason = "follower_forwarding"
	}
	now := time.Now().UTC()
	item.State, item.ObservedAt = "READY", &now
	return item
}
