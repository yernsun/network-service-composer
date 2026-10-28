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

type releaseMembershipStore struct {
	storage.Store
	values []model.Gateway
	pages  int
}

func (s *releaseMembershipStore) ListGateways(_ context.Context, p storage.Page) ([]model.Gateway, error) {
	s.pages++
	result := []model.Gateway{}
	for _, v := range s.values {
		if v.ID > p.After {
			result = append(result, v)
			if len(result) == p.Limit {
				break
			}
		}
	}
	return result, nil
}
func TestReleaseMembershipReadsEveryPageWithoutClaimingForeignGateways(t *testing.T) {
	c, _, pool := controlFixture(t)
	s := &releaseMembershipStore{Store: c.store}
	for i := 0; i < 203; i++ {
		g := model.Gateway{ID: fmt.Sprintf("gateway-%03d", i), PoolID: pool.ID, Enabled: true}
		if i == 2 {
			g.PoolID = "foreign"
		}
		if i == 3 {
			g.Enabled = false
		}
		s.values = append(s.values, g)
	}
	c.store = s
	v, e := c.productionGateways(context.Background(), pool.ID)
	if e != nil || len(v) != 201 || s.pages != 2 || v[len(v)-1].ID != "gateway-202" {
		t.Fatal("membership scope or pagination lost", len(v), s.pages, e)
	}
	for _, g := range v {
		if !g.Enabled || g.PoolID != pool.ID {
			t.Fatal("external gateway claimed", g)
		}
	}
}

func TestReleaseClaimProtectsLaterTargetsAndInconsistentHistory(t *testing.T) {
	for _, stage := range []string{"different unfinished revision", "different completed revision", "completed with running target", "completed with unknown target", "stopped retained history", "completed targets without finish", "empty targets retained history", "same revision inconsistent success", "same revision stopped success", "same revision unfinished success", "same revision empty success"} {
		t.Run(stage, func(t *testing.T) {
			c, r := releaseFixture(t, 2)
			ctx := context.Background()
			gateway := r.Targets[0].GatewayID
			revision := r.ConfigRevision
			held := true
			now := time.Now()
			switch stage {
			case "different unfinished revision":
				revision++
			case "different completed revision", "completed with running target", "completed with unknown target":
				revision++
				r.Status = "SUCCEEDED"
				r.FinishedAt = &now
				for i := range r.Targets {
					r.Targets[i].Status = "SUCCEEDED"
				}
				if stage == "completed with running target" {
					r.Targets[1].Status = "RUNNING"
				} else if stage == "completed with unknown target" {
					r.Targets[1].Status = "UNKNOWN"
				} else {
					held = false
				}
			case "stopped retained history":
				r.Status = "PUBLISHING"
				r.Stopped = true
				r.FinishedAt = &now
			case "completed targets without finish":
				r.Status = "PUBLISHING"
				r.FinishedAt = &now
				for i := range r.Targets {
					r.Targets[i].Status = "SUCCEEDED"
				}
			case "empty targets retained history":
				r.Status = "PUBLISHING"
				r.FinishedAt = &now
				r.Targets = nil
			case "same revision inconsistent success", "same revision stopped success", "same revision unfinished success", "same revision empty success":
				r.Status = "SUCCEEDED"
				r.FinishedAt = &now
				for i := range r.Targets {
					r.Targets[i].Status = "SUCCEEDED"
				}
				switch stage {
				case "same revision inconsistent success":
					r.Targets[1].Status = "UNKNOWN"
				case "same revision stopped success":
					r.Stopped = true
				case "same revision unfinished success":
					r.FinishedAt = nil
				case "same revision empty success":
					r.Targets = nil
				}
			}
			if e := c.saveControl(ctx, r.ID, "nginx-release", r.PoolID, r, r.Revision); e != nil {
				t.Fatal(e)
			}
			before, e := c.store.GetControlDocument(ctx, r.ID)
			if e != nil {
				t.Fatal(e)
			}
			snapshot, managed, e := c.ClaimNginxRelease(ctx, r.PoolID, gateway, revision)
			if e != nil || snapshot != nil || managed != held {
				t.Fatal("claim bypassed fixed execution scope", managed, held, e)
			}
			after, e := c.store.GetControlDocument(ctx, r.ID)
			if e != nil || after.Revision != before.Revision || string(after.Payload) != string(before.Payload) {
				t.Fatal("rejected claim rewrote history", e)
			}
		})
	}
}

func TestReleaseVerificationAndStopStorageFailuresPreserveTask(t *testing.T) {
	for _, stage := range []string{"verify read", "verify gateway", "verify write", "stop read", "stop write", "sent read", "sent write", "skip terminal targets"} {
		t.Run(stage, func(t *testing.T) {
			c, r := releaseFixture(t, 1)
			ctx := context.Background()
			gateway := r.Targets[0].GatewayID
			now := time.Now()
			r.Targets[0].Status = "RUNNING"
			r.Targets[0].StartedAt = &now
			if stage == "skip terminal targets" {
				r.Targets = append(r.Targets, NginxReleaseTarget{GatewayID: gateway, Status: "FAILED"}, NginxReleaseTarget{GatewayID: gateway, Status: "SUCCEEDED"}, NginxReleaseTarget{GatewayID: gateway, Status: "PENDING"})
			}
			if e := c.saveControl(ctx, r.ID, "nginx-release", r.PoolID, r, r.Revision); e != nil {
				t.Fatal(e)
			}
			base := c.store
			before, e := base.GetControlDocument(ctx, r.ID)
			if e != nil {
				t.Fatal(e)
			}
			failure := errors.New("release storage unavailable")
			wrapper := &nginxBoundaryStore{Store: base, method: "write", failure: failure}
			switch stage {
			case "verify read", "stop read", "sent read":
				wrapper.method = "document"
			case "verify gateway":
				wrapper.method = "gateway"
				wrapper.nth = 2
			case "skip terminal targets":
				wrapper.method = "disabled failure"
			}
			c.store = wrapper
			switch stage {
			case "stop read", "stop write":
				_, e = c.StopNginxRelease(ctx, r.PoolID, r.ID, before.Revision)
			case "sent read", "sent write":
				e = c.MarkNginxReleaseSent(ctx, r.PoolID, gateway, r.ConfigRevision, "digest")
			default:
				_, e = c.VerifyNginxRelease(ctx, r.PoolID, r.ID, before.Revision)
			}
			if stage == "skip terminal targets" {
				if e != nil {
					t.Fatal(e)
				}
				c.store = base
				result, e := c.GetNginxRelease(ctx, r.PoolID, r.ID)
				if e != nil || result.Targets[1].Status != "FAILED" || result.Targets[2].Status != "SUCCEEDED" || result.Targets[3].Status != "PENDING" {
					t.Fatal("verification rewrote terminal targets", e)
				}
				return
			}
			if !errors.Is(e, failure) {
				t.Fatal("release failure hidden", e)
			}
			after, e := base.GetControlDocument(ctx, r.ID)
			if e != nil || after.Revision != before.Revision || string(after.Payload) != string(before.Payload) {
				t.Fatal("failed task action committed", e)
			}
		})
	}
}

func TestReleaseResultIgnoresAlreadyTerminalTargets(t *testing.T) {
	c, r := releaseFixture(t, 2)
	ctx := context.Background()
	gateway := r.Targets[0].GatewayID
	now := time.Now()
	r.Targets[0].Status = "UNKNOWN"
	r.Targets[0].StartedAt = &now
	r.Targets[1].Status = "SUCCEEDED"
	r.Targets = append(r.Targets, NginxReleaseTarget{GatewayID: gateway, Status: "PENDING"}, NginxReleaseTarget{GatewayID: gateway, Status: "FAILED"})
	if e := c.saveControl(ctx, r.ID, "nginx-release", r.PoolID, r, r.Revision); e != nil {
		t.Fatal(e)
	}
	if e := c.RecordNginxReleaseResult(ctx, r.PoolID, gateway, r.ConfigRevision, "FAILED", "", "", "validation failed"); e != nil {
		t.Fatal(e)
	}
	actual, e := c.GetNginxRelease(ctx, r.PoolID, r.ID)
	if e != nil || actual.Targets[0].Status != "FAILED" || actual.Targets[0].Error != "validation failed" || actual.Targets[1].Status != "SUCCEEDED" || actual.Targets[2].Status != "PENDING" || actual.Targets[3].Status != "FAILED" {
		t.Fatal("late result overwrote unrelated evidence", e)
	}
}
