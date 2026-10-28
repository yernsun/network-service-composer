package core

import (
	"context"
	"errors"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type frozenReleaseSnapshot struct {
	Snapshot model.Snapshot    `json:"snapshot"`
	Keys     map[string]string `json:"keys"`
}

func (c *Core) freezeReleaseSnapshot(key, pool string, snapshot model.Snapshot) (storage.ControlWrite, error) {
	v := frozenReleaseSnapshot{Snapshot: snapshot, Keys: map[string]string{}}
	for id, cert := range snapshot.Certificates {
		v.Keys[id] = cert.PrivateKeyPEM
	}
	return c.encryptedWrite(key+":snapshot", "release-snapshot", pool, v, 0)
}

func (c *Core) productionGateways(ctx context.Context, pool string) ([]model.Gateway, error) {
	result := []model.Gateway{}
	page := storage.Page{Limit: 200}
	for {
		values, err := c.store.ListGateways(ctx, page)
		if err != nil {
			return nil, err
		}
		for _, g := range values {
			if g.PoolID == pool && g.Enabled {
				result = append(result, g)
			}
		}
		if len(values) < 200 {
			return result, nil
		}
		page.After = values[len(values)-1].ID
	}
}

func summarizeRelease(v *NginxRelease, now time.Time) {
	if v.FinishedAt != nil {
		return
	}
	success, failed, unknown := 0, 0, 0
	for i := range v.Targets {
		t := &v.Targets[i]
		if t.Status == "RUNNING" && t.StartedAt != nil && !now.Before(t.StartedAt.Add(2*time.Minute)) {
			t.Status = "UNKNOWN"
		}
		switch t.Status {
		case "SUCCEEDED":
			success++
		case "FAILED":
			failed++
		case "UNKNOWN":
			unknown++
		}
	}
	switch {
	case unknown > 0:
		v.Status = "UNKNOWN"
	case success == len(v.Targets) && success > 0:
		v.Status = "SUCCEEDED"
	case failed > 0 && success > 0:
		v.Status = "PARTIAL"
	case failed > 0:
		v.Status = "FAILED"
	case v.Stopped:
		v.Status = "STOPPED"
	case len(v.Targets) == 0:
		v.Status = "UNKNOWN" // Legacy records have no execution evidence.
	default:
		v.Status = "PUBLISHING"
	}
}

func (c *Core) releaseHead(ctx context.Context, pool string) (NginxRelease, error) {
	head, _, err := readControl[map[string]string](ctx, c.store, pool+":nginx-release-head", "nginx-release-head")
	if err != nil {
		return NginxRelease{}, err
	}
	return c.GetNginxRelease(ctx, pool, head["release_id"])
}

func completedRelease(v NginxRelease) bool {
	if v.Status != "SUCCEEDED" || v.FinishedAt == nil || v.Stopped || len(v.Targets) == 0 {
		return false
	}
	for _, target := range v.Targets {
		if target.Status != "SUCCEEDED" {
			return false
		}
	}
	return true
}

func (c *Core) thawReleaseSnapshot(ctx context.Context, key string) (*model.Snapshot, error) {
	var frozen frozenReleaseSnapshot
	if _, err := c.readEncrypted(ctx, key+":snapshot", "release-snapshot", &frozen); err != nil {
		return nil, err
	}
	for id, cert := range frozen.Snapshot.Certificates {
		cert.PrivateKeyPEM = frozen.Keys[id]
		frozen.Snapshot.Certificates[id] = cert
	}
	return &frozen.Snapshot, nil
}

// ClaimNginxRelease serializes the next target before transport writes. A lost
// response leaves RUNNING/UNKNOWN and is never treated as permission to retry.
// managed distinguishes a held release from pre-existing non-release traffic.
// Completed releases resend their frozen configuration for cold starts and
// renderer upgrades without reopening or rewriting the historical operation.
func (c *Core) ClaimNginxRelease(ctx context.Context, pool, gateway string, revision int64) (*model.Snapshot, bool, error) {
	v, err := c.releaseHead(ctx, pool)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if v.ConfigRevision != revision {
		if v.Status != "SUCCEEDED" {
			return nil, true, nil
		}
		for _, target := range v.Targets {
			if target.Status == "RUNNING" || target.Status == "UNKNOWN" {
				return nil, true, nil
			}
		}
		return nil, false, nil
	}
	if completedRelease(v) {
		snapshot, err := c.thawReleaseSnapshot(ctx, v.ID)
		return snapshot, true, err
	}
	if v.Status != "PUBLISHING" || v.Stopped {
		return nil, true, nil
	}
	index := -1
	for i, target := range v.Targets {
		if target.Status == "SUCCEEDED" {
			continue
		}
		if target.Status != "PENDING" || target.GatewayID != gateway {
			return nil, true, nil
		}
		index = i
		break
	}
	if index < 0 {
		return nil, true, nil
	}
	snapshot, err := c.thawReleaseSnapshot(ctx, v.ID)
	if err != nil {
		return nil, true, err
	}
	now := c.now()
	v.Targets[index].Status, v.Targets[index].StartedAt = "RUNNING", &now
	if err = c.saveControl(ctx, v.ID, "nginx-release", pool, v, v.Revision); errors.Is(err, storage.ErrPrecondition) {
		return nil, true, nil
	}
	if err != nil {
		return nil, true, err
	}
	return snapshot, true, nil
}

// VerifyNginxRelease only adopts fresh per-node observations made after the
// attempt started. A cached Hello or a shared load-balancer response is not proof.
func (c *Core) VerifyNginxRelease(ctx context.Context, pool, key string, expected int64) (NginxRelease, error) {
	v, err := c.GetNginxRelease(ctx, pool, key)
	if err != nil {
		return v, err
	}
	if v.Revision != expected {
		return v, storage.ErrPrecondition
	}
	for i := range v.Targets {
		target := &v.Targets[i]
		if target.Status != "UNKNOWN" && target.Status != "RUNNING" {
			continue
		}
		e, err := c.GatewayEvidence(ctx, target.GatewayID)
		if err != nil {
			return v, err
		}
		if !e.LoadingConfirmed || e.LastConfirmedAt == nil || target.StartedAt == nil || !e.LastConfirmedAt.After(*target.StartedAt) {
			continue
		}
		if e.ConfirmedRevision == v.ConfigRevision && target.ModelDigest != "" && e.ModelDigest == target.ModelDigest && e.StartupState == "CONFIRMED" {
			target.Status, target.ConfirmedRevision, target.VerifiedAt, target.RenderedDigest, target.Error = "SUCCEEDED", e.ConfirmedRevision, e.LastConfirmedAt, e.RenderedDigest, ""
		} else if e.ConfirmedRevision == target.BeforeRevision {
			target.Status, target.Error = "FAILED", "previous version confirmed; candidate was not confirmed loaded"
		}
	}
	summarizeRelease(&v, c.now())
	if v.Status == "SUCCEEDED" {
		now := c.now()
		v.FinishedAt = &now
	}
	if err = c.saveControl(ctx, key, "nginx-release", pool, v, expected); err != nil {
		return v, err
	}
	return c.GetNginxRelease(ctx, pool, key)
}

func (c *Core) MarkNginxReleaseSent(ctx context.Context, pool, gateway string, revision int64, digest string) error {
	v, err := c.releaseHead(ctx, pool)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if v.ConfigRevision != revision {
		return nil
	}
	if completedRelease(v) {
		return nil
	}
	for i := range v.Targets {
		if v.Targets[i].GatewayID == gateway && v.Targets[i].Status == "RUNNING" {
			v.Targets[i].ModelDigest = digest
			return c.saveControl(ctx, v.ID, "nginx-release", pool, v, v.Revision)
		}
	}
	return storage.ErrPrecondition
}

func (c *Core) RecordNginxReleaseResult(ctx context.Context, pool, gateway string, revision int64, status, digest, rendered, message string) error {
	for attempt := 0; attempt < 5; attempt++ {
		v, err := c.releaseHead(ctx, pool)
		if errors.Is(err, storage.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if v.ConfigRevision != revision {
			return nil
		}
		changed := false
		for i := range v.Targets {
			t := &v.Targets[i]
			if t.GatewayID != gateway || (t.Status != "RUNNING" && t.Status != "UNKNOWN") {
				continue
			}
			t.Status, t.Error = status, message
			if status == "SUCCEEDED" {
				now := c.now()
				t.ConfirmedRevision, t.VerifiedAt, t.ModelDigest, t.RenderedDigest = revision, &now, digest, rendered
			}
			changed = true
		}
		if !changed {
			return nil
		}
		summarizeRelease(&v, c.now())
		if v.Status == "SUCCEEDED" {
			now := c.now()
			v.FinishedAt = &now
		}
		err = c.saveControl(ctx, v.ID, "nginx-release", pool, v, v.Revision)
		if !errors.Is(err, storage.ErrPrecondition) {
			return err
		}
	}
	return storage.ErrPrecondition
}

func (c *Core) StopNginxRelease(ctx context.Context, pool, key string, expected int64) (NginxRelease, error) {
	v, err := c.GetNginxRelease(ctx, pool, key)
	if err != nil {
		return v, err
	}
	if v.Revision != expected {
		return v, storage.ErrPrecondition
	}
	v.Stopped = true
	summarizeRelease(&v, c.now())
	if err = c.saveControl(ctx, key, "nginx-release", pool, v, expected); err != nil {
		return v, err
	}
	return c.GetNginxRelease(ctx, pool, key)
}
