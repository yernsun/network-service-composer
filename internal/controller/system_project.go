package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// Reconciliation runs under the elected leader, independently of local sampling.
// Missing observations are retried, never converted to empty membership.
type systemProjectReconciler struct{ controller *Controller }

func (r systemProjectReconciler) Run(ctx context.Context) error {
	c := r.controller
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if err := c.bindMissingRuntimeClusters(ctx); err != nil && ctx.Err() == nil {
			c.logger.ErrorContext(ctx, "bind Swarm Runtime cluster identity", "error", err)
		}
		if err := c.reconcileSystemProject(ctx); err != nil && ctx.Err() == nil {
			c.logger.ErrorContext(ctx, "reconcile Gateway system project", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (c *Controller) reconcileSystemProject(ctx context.Context) error {
	for attempt := 0; ; attempt++ {
		err := c.reconcileSystemProjectOnce(ctx)
		if err == nil || attempt >= 5 || (!errors.Is(err, storage.ErrPrecondition) && !errors.Is(err, storage.ErrConflict) && !storage.IsRetryableTransactionError(err)) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(20*(1<<attempt)) * time.Millisecond):
		}
	}
}

func (c *Controller) reconcileSystemProjectOnce(ctx context.Context) error {
	reg := c.config.Controller.SystemProject
	rt, err := c.store.GetRuntime(ctx, reg.RuntimeID)
	if err != nil {
		return err
	}
	nodeID := strings.TrimPrefix(rt.Endpoint, "node://")
	bound := false
	for _, b := range c.config.Controller.Nodes {
		if b.NodeID == nodeID && b.ClusterID == reg.ClusterID {
			bound = true
		}
	}
	if !bound {
		return fmt.Errorf("Gateway runtime is not bound to the registered cluster")
	}
	client, err := c.nodeReports.Client(ctx, rt)
	if err != nil {
		return err
	}
	defer client.Close()
	info, err := client.Info(ctx)
	if err != nil {
		return err
	}
	if info.Swarm.Cluster == nil {
		return fmt.Errorf("Gateway initialization requires Manager cluster evidence")
	}
	services, err := client.ServiceList(ctx)
	if err != nil {
		return err
	}
	objects := map[string]bool{}
	for _, s := range services {
		objects[s.ID] = true
	}
	project, err := c.core.EnsureGatewayProject(ctx, reg, info.Swarm.Cluster.ID, objects)
	if err != nil || len(reg.Applications) == 0 {
		return err
	}
	evidence := map[string]map[string]bool{reg.RuntimeID: objects}
	stopped := map[string]map[string]bool{}
	runtimeIDs := []string{}
	for _, app := range reg.Applications {
		runtimeIDs = append(runtimeIDs, app.RuntimeID)
		if app.ReplacesRuntimeID != "" {
			runtimeIDs = append(runtimeIDs, app.ReplacesRuntimeID)
		}
	}
	for _, runtimeID := range runtimeIDs {
		if _, ok := evidence[runtimeID]; ok {
			continue
		}
		runtime, e := c.store.GetRuntime(ctx, runtimeID)
		if e != nil {
			return e
		}
		node := strings.TrimPrefix(runtime.Endpoint, "node://")
		bound := false
		for _, b := range c.config.Controller.Nodes {
			if b.NodeID == node && b.ClusterID == reg.ClusterID {
				bound = true
			}
		}
		if !bound {
			return fmt.Errorf("system application runtime is outside the registered cluster")
		}
		observed, e := c.nodeReports.Client(ctx, runtime)
		if e != nil {
			return e
		}
		var ids = map[string]bool{}
		stopped[runtimeID] = map[string]bool{}
		if runtime.Type == model.RuntimeDocker {
			containers, e := observed.ContainerList(ctx)
			observed.Close()
			if e != nil {
				return e
			}
			for _, container := range containers {
				if container.State == "running" {
					ids[container.ID] = true
				} else if container.State == "exited" || container.State == "dead" {
					stopped[runtimeID][container.ID] = true
				}
			}
		} else if runtime.Type == model.RuntimeSwarm {
			services, e := observed.ServiceList(ctx)
			observed.Close()
			if e != nil {
				return e
			}
			for _, service := range services {
				ids[service.ID] = true
			}
		} else {
			observed.Close()
			return fmt.Errorf("unsupported system application runtime")
		}
		evidence[runtimeID] = ids
	}
	if err = c.core.ReconcileGatewayApplicationsWithRetirements(ctx, project, reg.ClusterID, reg.Applications, evidence, stopped); err != nil {
		return err
	}
	return c.core.EnsureGatewayConsoleDomain(ctx, project, reg.ConsoleDomain)
}
