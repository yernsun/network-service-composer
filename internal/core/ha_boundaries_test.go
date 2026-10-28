package core

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type haInventoryBoundary struct {
	storage.Store
	failure string
}

func (s haInventoryBoundary) ListRuntimes(_ context.Context, page storage.Page) ([]model.Runtime, error) {
	if s.failure == "runtimes" {
		return nil, errors.New("inventory unavailable")
	}
	rows := []model.Runtime{}
	for i := 0; i < 201; i++ {
		id := fmt.Sprintf("runtime-%03d", i)
		if id > page.After && len(rows) < page.Limit {
			rows = append(rows, model.Runtime{ID: id, Type: model.RuntimeSwarm, Enabled: false})
		}
	}
	return rows, nil
}
func (s haInventoryBoundary) ListGateways(_ context.Context, page storage.Page) ([]model.Gateway, error) {
	if s.failure == "gateways" {
		return nil, errors.New("inventory unavailable")
	}
	rows := []model.Gateway{}
	for i := 0; i < 201; i++ {
		id := fmt.Sprintf("gateway-%03d", i)
		if id > page.After && len(rows) < page.Limit {
			rows = append(rows, model.Gateway{ID: id, Enabled: false})
		}
	}
	return rows, nil
}

func TestHAOverviewIncludesEveryInventoryPageAndNeverTurnsFailedReadsIntoHealth(t *testing.T) {
	c, _, _ := controlFixture(t)
	original := c.store
	c.store = haInventoryBoundary{Store: original}
	value, err := c.SystemHA(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, layer := range value.Layers {
		if layer.Name == "execution" || layer.Name == "swarm_quorum" || layer.Name == "gateway" {
			if len(layer.Evidence) != 201 {
				t.Fatal("HA omitted a page of configured resources", layer.Name, len(layer.Evidence))
			}
			for _, item := range layer.Evidence {
				if item.State != "DISABLED" {
					t.Fatal(item)
				}
			}
		}
	}
	for _, boundary := range []string{"runtimes", "gateways"} {
		c.store = haInventoryBoundary{Store: original, failure: boundary}
		if _, err = c.SystemHA(t.Context()); err == nil {
			t.Fatal("unavailable inventory became empty healthy layer", boundary)
		}
	}
	c.store = original
	c.config.Controller.RecoveryMode = true
	c.HAController = func(context.Context) HAEvidence { return HAEvidence{State: "READY"} }
	if got := haFind(t, c, "controller"); got.Evidence[0].State != "DISABLED" {
		t.Fatal(got)
	}
}

func TestHAEvidenceFreshnessRejectsZeroFutureAndExpiredTimes(t *testing.T) {
	now := time.Now().UTC()
	zero := time.Time{}
	past := now.Add(-4 * time.Second)
	future := now.Add(6 * time.Second)
	current := now
	for _, at := range []*time.Time{nil, &zero, &past, &future} {
		if haFresh(now, at, 3*time.Second) {
			t.Fatal("invalid evidence was current", at)
		}
	}
	if !haFresh(now, &current, 3*time.Second) {
		t.Fatal("current evidence rejected")
	}
}

func TestHAGatewayNeedsCurrentHeartbeatAndAppliedConfigurationTogether(t *testing.T) {
	for _, mode := range []string{"heartbeat-missing", "heartbeat-old", "heartbeat-future", "applied-behind", "status-failed", "loading-unconfirmed", "observation-old", "observation-future"} {
		t.Run(mode, func(t *testing.T) {
			c, release := releaseFixture(t, 1)
			now := c.now()
			c.now = func() time.Time { return now }
			g, err := c.store.GetGateway(t.Context(), release.Targets[0].GatewayID)
			if err != nil {
				t.Fatal(err)
			}
			g.Status = model.GatewayReady
			g.AppliedRevision = g.DesiredRevision
			at := now
			g.LastSeenAt = &at
			e := GatewayEvidence{ConfirmedRevision: g.DesiredRevision, LoadingConfirmed: true, StartupState: "CONFIRMED", ObservedAt: now, ReceivedAt: now, RefreshIntervalMillis: 1000, RenderedDigest: "same", DiskDigest: "same"}
			switch mode {
			case "heartbeat-missing":
				g.LastSeenAt = nil
			case "heartbeat-old":
				at = now.Add(-time.Hour)
			case "heartbeat-future":
				at = now.Add(time.Minute)
			case "applied-behind":
				g.AppliedRevision = g.DesiredRevision - 1
			case "status-failed":
				g.Status = model.GatewayApplyFailed
			case "loading-unconfirmed":
				e.LoadingConfirmed = false
			case "observation-old":
				e.ObservedAt = now.Add(-time.Minute)
			case "observation-future":
				e.ObservedAt = now.Add(time.Minute)
			}
			if err = c.store.UpdateGateway(t.Context(), &g, g.Revision); err != nil {
				t.Fatal(err)
			}
			var heartbeat any
			if g.LastSeenAt != nil {
				heartbeat = *g.LastSeenAt
			}
			if _, err = c.store.(*storage.SQLStore).DB().ExecContext(t.Context(), `UPDATE gateways SET last_seen_at=?,applied_revision=? WHERE id=?`, heartbeat, g.AppliedRevision, g.ID); err != nil {
				t.Fatal(err)
			}
			if err = c.saveControl(t.Context(), g.ID+":evidence", "gateway-evidence", g.ID, e, 0); err != nil {
				t.Fatal(err)
			}
			if got := haFind(t, c, "gateway"); got.State == "READY" {
				t.Fatal("unconfirmed dependency became ready", mode, got)
			}
		})
	}
}
