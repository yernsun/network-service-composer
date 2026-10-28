package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestEvidenceStateMatrix(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	now := c.now()
	c.now = func() time.Time { return now }
	for _, tc := range []struct {
		name, startup, disk, rendered  string
		loaded                         bool
		confirmed                      int64
		age                            time.Duration
		seen                           bool
		configuration, expectedStartup string
	}{
		{"confirmed", "CONFIRMED", "same", "same", true, 7, 0, true, "CONFIRMED", "CONFIRMED"},
		{"sent without loading proof", "CONFIRMED", "same", "same", false, 7, 0, true, "UNKNOWN", "CONFIRMED"},
		{"old running version", "CONFIRMED", "same", "same", true, 6, 0, true, "MISMATCH", "CONFIRMED"},
		{"external disk change", "RISK", "different", "same", true, 7, 0, true, "EXTERNAL_CHANGE", "RISK"},
		{"no disk observation", "UNKNOWN", "", "same", true, 7, 0, true, "CONFIRMED", "UNKNOWN"},
		{"just before expiry", "CONFIRMED", "same", "same", true, 7, 3*time.Second - time.Nanosecond, true, "CONFIRMED", "CONFIRMED"},
		{"exact expiry", "CONFIRMED", "same", "same", true, 7, 3 * time.Second, true, "UNKNOWN", "UNKNOWN"},
		{"expired risk", "RISK", "same", "same", true, 7, 3 * time.Second, true, "UNKNOWN", "RISK"},
		{"no heartbeat", "CONFIRMED", "same", "same", true, 7, 0, false, "UNKNOWN", "UNKNOWN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := model.Gateway{ID: "evidence-" + tc.name, PoolID: pool.ID, Enabled: true, DesiredRevision: 7, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			if tc.seen {
				g.LastSeenAt = &now
			}
			if err := c.store.UpsertGateway(ctx, &g); err != nil {
				t.Fatal(err)
			}
			v := GatewayEvidence{StartupState: tc.startup, DiskDigest: tc.disk, RenderedDigest: tc.rendered, LoadingConfirmed: tc.loaded, ConfirmedRevision: tc.confirmed, ObservedAt: now, RefreshIntervalMillis: 1000}
			if err := c.RecordGatewayEvidence(ctx, g.ID, v); err != nil {
				t.Fatal(err)
			}
			c.now = func() time.Time { return now.Add(tc.age) }
			got, err := c.GatewayEvidence(ctx, g.ID)
			c.now = func() time.Time { return now }
			if err != nil || got.ConfigurationState != tc.configuration || got.StartupState != tc.expectedStartup {
				t.Fatalf("unexpected evidence %+v: %v", got, err)
			}
			if tc.loaded && (got.LastConfirmedAt == nil || !got.LastConfirmedAt.Equal(now) || got.LastConfirmedRevision != tc.confirmed) {
				t.Fatal("last confirmation lost", got)
			}
			if tc.configuration == "UNKNOWN" && (tc.age >= 3*time.Second || !tc.seen) && got.LoadingConfirmed {
				t.Fatal("expired proof reported as current")
			}
		})
	}
	if _, err := c.GatewayEvidence(ctx, "absent"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("missing gateway", err)
	}
}

func TestEvidenceInvalidOrOlderSamplesCannotRefreshConfirmation(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	now := c.now()
	c.now = func() time.Time { return now }
	g := model.Gateway{ID: "sample-order", PoolID: pool.ID, Enabled: true, DesiredRevision: 8, LastSeenAt: &now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := c.store.UpsertGateway(ctx, &g); err != nil {
		t.Fatal(err)
	}
	for _, interval := range []int64{0, 99, 300001} {
		if err := c.RecordGatewayEvidence(ctx, g.ID, GatewayEvidence{ObservedAt: now, RefreshIntervalMillis: interval, StartupState: "CONFIRMED"}); err == nil {
			t.Fatal("invalid interval accepted", interval)
		}
	}
	if err := c.RecordGatewayEvidence(ctx, g.ID, GatewayEvidence{ObservedAt: now, RefreshIntervalMillis: 100, StartupState: "HEALTHY"}); err == nil {
		t.Fatal("unknown startup claim accepted")
	}
	if err := c.RecordGatewayEvidence(ctx, g.ID, GatewayEvidence{}); err != nil {
		t.Fatal("old agent without evidence should remain readable", err)
	}
	unknown, err := c.GatewayEvidence(ctx, g.ID)
	if err != nil || unknown.ConfigurationState != "UNKNOWN" || unknown.StartupState != "UNKNOWN" {
		t.Fatal(unknown, err)
	}
	v := GatewayEvidence{ObservedAt: now, RefreshIntervalMillis: 100, StartupState: "CONFIRMED", LoadingConfirmed: true, ConfirmedRevision: 8}
	if err = c.RecordGatewayEvidence(ctx, g.ID, v); err != nil {
		t.Fatal(err)
	}
	v.ObservedAt = now.Add(-time.Second)
	v.ConfirmedRevision = 7
	if err = c.RecordGatewayEvidence(ctx, g.ID, v); err != nil {
		t.Fatal(err)
	}
	got, err := c.GatewayEvidence(ctx, g.ID)
	if err != nil || got.ConfirmedRevision != 8 {
		t.Fatal("older sample overwrote evidence", got, err)
	}
	v.ObservedAt = now.Add(time.Second)
	v.LoadingConfirmed = false
	v.StartupState = "UNKNOWN"
	c.now = func() time.Time { return now.Add(time.Second) }
	if err = c.RecordGatewayEvidence(ctx, g.ID, v); err != nil {
		t.Fatal(err)
	}
	got, err = c.GatewayEvidence(ctx, g.ID)
	if err != nil || got.ConfigurationState != "UNKNOWN" || got.LastConfirmedRevision != 8 || got.LastConfirmedAt == nil || !got.LastConfirmedAt.Equal(now) {
		t.Fatal("unconfirmed sample erased last confirmed facts", got, err)
	}
}

func TestIssueResolutionAndRecurrenceResetAcknowledgement(t *testing.T) {
	c, _, _ := controlFixture(t)
	ctx := context.Background()
	if err := c.observeIssue(ctx, "test", "gateway", "g", "/ui/gateways", false); err != nil {
		t.Fatal(err)
	}
	list, err := c.Issues(ctx, storage.Page{Limit: 200})
	if err != nil || len(list) != 0 {
		t.Fatal("inactive first observation created an issue", list, err)
	}
	if err = c.observeIssue(ctx, "test", "gateway", "g", "/ui/gateways", true); err != nil {
		t.Fatal(err)
	}
	list, err = c.Issues(ctx, storage.Page{Limit: 200})
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	one := list[0]
	if _, err = c.AcknowledgeIssue(ctx, one.ID, one.Revision+1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("stale acknowledgement", err)
	}
	if _, err = c.AcknowledgeIssue(ctx, one.ID, one.Revision); err != nil {
		t.Fatal(err)
	}
	if err = c.observeIssue(ctx, "test", "gateway", "g", "/ui/gateways", false); err != nil {
		t.Fatal(err)
	}
	if err = c.observeIssue(ctx, "test", "gateway", "g", "/ui/gateways", false); err != nil {
		t.Fatal(err)
	}
	list, err = c.Issues(ctx, storage.Page{Limit: 200})
	if err != nil || list[0].Active || list[0].ResolvedAt == nil || list[0].Occurrences != 1 {
		t.Fatal("resolution lost", list, err)
	}
	if err = c.observeIssue(ctx, "test", "gateway", "g", "/ui/gateways", true); err != nil {
		t.Fatal(err)
	}
	list, err = c.Issues(ctx, storage.Page{Limit: 200})
	if err != nil || !list[0].Active || list[0].Acknowledged || list[0].ResolvedAt != nil || list[0].Occurrences != 2 {
		t.Fatal("recurrence hidden by acknowledgement", list, err)
	}
	if _, err = c.AcknowledgeIssue(ctx, "absent", 1); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
}
