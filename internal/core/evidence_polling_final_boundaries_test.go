package core

import (
	"context"
	"encoding/json"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"testing"
	"time"
)

func TestRecentEvidenceCannotHideAnExpiredGatewayHeartbeat(t *testing.T) {
	c, _, pool := controlFixture(t)
	now := c.now()
	c.now = func() time.Time { return now }
	old := now.Add(-c.config.Controller.Gateway.OfflineTimeout.Std())
	g := model.Gateway{ID: "expired-heartbeat", PoolID: pool.ID, Enabled: true, DesiredRevision: 7, LastSeenAt: &old, Metadata: model.Metadata{Revision: 1, CreatedAt: old, UpdatedAt: old}}
	if e := c.store.UpsertGateway(context.Background(), &g); e != nil {
		t.Fatal(e)
	}
	if e := c.RecordGatewayEvidence(context.Background(), g.ID, GatewayEvidence{StartupState: "CONFIRMED", LoadingConfirmed: true, ConfirmedRevision: 7, ObservedAt: now, RefreshIntervalMillis: 1000}); e != nil {
		t.Fatal(e)
	}
	evidence, e := c.GatewayEvidence(context.Background(), g.ID)
	if e != nil || evidence.ConfigurationState != "UNKNOWN" || evidence.StartupState != "UNKNOWN" || evidence.LoadingConfirmed || evidence.LastConfirmedRevision != 7 {
		t.Fatalf("fresh sample hid expired connectivity or lost last confirmation: %+v %v", evidence, e)
	}
}
func TestImagePollingStopsAfterRepeatedFinalStateConflicts(t *testing.T) {
	c, env := imageEnvironmentFixture(t)
	ctx := context.Background()
	base := c.store
	if e := c.saveControl(ctx, env.ID+":image-policy", "image-policy", env.ID, ImagePolicy{Enabled: true, Poll: true, Services: []string{"web"}, IntervalSeconds: 300}, 0); e != nil {
		t.Fatal(e)
	}
	conflicts := 0
	c.store = &imageControlStore{Store: base, write: func(ctx context.Context, writes []storage.ControlWrite) error {
		for _, w := range writes {
			if w.Document.Kind != "image-check" {
				continue
			}
			var state ImageCheck
			if e := json.Unmarshal(w.Document.Payload, &state); e != nil {
				return e
			}
			if state.Worker == "" {
				conflicts++
				return storage.ErrPrecondition
			}
		}
		return base.WriteControlDocuments(ctx, writes)
	}}
	c.checkEnvironmentImages(ctx, env)
	c.store = base
	state, e := c.GetImageCheck(ctx, env.ID)
	if e != nil || conflicts != 4 || state.Worker == "" || state.LeaseUntil.IsZero() || state.LastChecked != nil {
		t.Fatalf("conflicted completion erased lease or spun without bound: %+v count=%d %v", state, conflicts, e)
	}
}

func TestUnknownReleaseCannotBeVerifiedWithoutNewLoadingEvidence(t *testing.T) {
	c, release := releaseFixture(t, 1)
	ctx := context.Background()
	started := c.now()
	release.Targets[0].Status = "UNKNOWN"
	release.Targets[0].StartedAt = &started
	if e := c.saveControl(ctx, release.ID, "nginx-release", release.PoolID, release, release.Revision); e != nil {
		t.Fatal(e)
	}
	current, e := c.GetNginxRelease(ctx, release.PoolID, release.ID)
	if e != nil {
		t.Fatal(e)
	}
	verified, e := c.VerifyNginxRelease(ctx, release.PoolID, release.ID, current.Revision)
	if e != nil || verified.Targets[0].Status != "UNKNOWN" || verified.FinishedAt != nil {
		t.Fatalf("unknown execution became complete without evidence: %+v %v", verified, e)
	}
}

func TestRepeatingManagedCertificateReferencePreservesAsset(t *testing.T) {
	c, service, input := externalBindingFixture(t)
	ctx := context.Background()
	input.Domain.EntryPoints = []string{"https"}
	input.Domain.TLS.Mode = model.TLSManaged
	binding, _, e := c.CreateDomainBinding(ctx, service.ID, input)
	if e != nil {
		t.Fatal(e)
	}
	next, e := c.UpdateDomainBinding(ctx, service.ID, binding.ID, UpdateDomainBindingInput{DomainRevision: binding.Domain.Revision, Domain: &DomainConfigPatch{TLS: &DomainTLSInput{Mode: model.TLSManaged, CertificateID: binding.Domain.CertificateID}}}, binding.Route.Revision)
	if e != nil || next.Domain.CertificateID != binding.Domain.CertificateID {
		t.Fatalf("explicit managed reference changed asset: %+v %v", next, e)
	}
	cert, e := c.store.GetCertificate(ctx, binding.Domain.CertificateID)
	if e != nil || cert.Status != model.CertificatePending || cert.PrimaryDomain != input.Domain.Host {
		t.Fatal("repeat reference changed certificate ownership", e)
	}
}
