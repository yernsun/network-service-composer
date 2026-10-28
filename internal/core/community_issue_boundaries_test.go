package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestIssueResolutionAndRecurrencePreserveAcknowledgementMeaning(t *testing.T) {
	c, _, _ := controlFixture(t)
	ctx := context.Background()
	now := c.now()
	c.now = func() time.Time { return now }
	if err := c.observeIssue(ctx, "gateway_unknown", "gateway", "g", "/gateway/g", false); err != nil {
		t.Fatal(err)
	}
	issues, err := c.Issues(ctx, storage.Page{})
	if err != nil || len(issues) != 0 {
		t.Fatalf("healthy resource created issue: %+v %v", issues, err)
	}
	if err = c.observeIssue(ctx, "gateway_unknown", "gateway", "g", "/gateway/g", true); err != nil {
		t.Fatal(err)
	}
	issues, err = c.Issues(ctx, storage.Page{})
	if err != nil || len(issues) != 1 {
		t.Fatal(err)
	}
	first := issues[0]
	if !first.Active || first.Acknowledged || first.Occurrences != 1 || first.ResolvedAt != nil || !first.FirstSeenAt.Equal(now) {
		t.Fatalf("new issue facts: %+v", first)
	}
	if _, err = c.AcknowledgeIssue(ctx, first.ID, first.Revision+1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("stale acknowledgement accepted")
	}
	if _, err = c.AcknowledgeIssue(ctx, first.ID, first.Revision); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err = c.observeIssue(ctx, first.Code, first.ResourceType, first.ResourceID, first.Link, true); err != nil {
		t.Fatal(err)
	}
	issues, err = c.Issues(ctx, storage.Page{})
	if err != nil {
		t.Fatal(err)
	}
	ongoing := issues[0]
	if !ongoing.Active || !ongoing.Acknowledged || ongoing.Occurrences != 1 || !ongoing.LastSeenAt.Equal(now) {
		t.Fatalf("acknowledgement changed failure facts: %+v", ongoing)
	}
	now = now.Add(time.Minute)
	if err = c.observeIssue(ctx, first.Code, first.ResourceType, first.ResourceID, first.Link, false); err != nil {
		t.Fatal(err)
	}
	issues, err = c.Issues(ctx, storage.Page{})
	if err != nil {
		t.Fatal(err)
	}
	resolved := issues[0]
	if resolved.Active || resolved.ResolvedAt == nil || !resolved.ResolvedAt.Equal(now) {
		t.Fatalf("resolution missing: %+v", resolved)
	}
	if err = c.observeIssue(ctx, first.Code, first.ResourceType, first.ResourceID, first.Link, false); err != nil {
		t.Fatal(err)
	}
	issues, err = c.Issues(ctx, storage.Page{})
	if err != nil || issues[0].Revision != resolved.Revision {
		t.Fatal("repeated healthy scan generated another resolution")
	}
	now = now.Add(time.Minute)
	if err = c.observeIssue(ctx, first.Code, first.ResourceType, first.ResourceID, first.Link, true); err != nil {
		t.Fatal(err)
	}
	issues, err = c.Issues(ctx, storage.Page{})
	if err != nil {
		t.Fatal(err)
	}
	recurred := issues[0]
	if !recurred.Active || recurred.Acknowledged || recurred.Occurrences != 2 || recurred.ResolvedAt != nil || !recurred.FirstSeenAt.Equal(first.FirstSeenAt) {
		t.Fatalf("recurrence lost history: %+v", recurred)
	}
}

type communityBoundaryStore struct{ *nginxBoundaryStore }

func (s *communityBoundaryStore) ListCertificates(ctx context.Context, page storage.Page) ([]model.Certificate, error) {
	if err := s.fail("certificates", ""); err != nil {
		return nil, err
	}
	return s.Store.ListCertificates(ctx, page)
}

func TestCommunityIssueAndEvidenceStorageFailures(t *testing.T) {
	failure := errors.New("community state unavailable")
	ctx := context.Background()
	for _, test := range []struct {
		name, method, key string
		run               func(*Core, string, string) error
	}{
		{"evidence read", "document", ":evidence", func(c *Core, g, issue string) error {
			return c.RecordGatewayEvidence(ctx, g, GatewayEvidence{ObservedAt: c.now(), RefreshIntervalMillis: 1000, StartupState: "UNKNOWN"})
		}},
		{"evidence write", "write", "", func(c *Core, g, issue string) error {
			return c.RecordGatewayEvidence(ctx, g, GatewayEvidence{ObservedAt: c.now(), RefreshIntervalMillis: 1000, StartupState: "UNKNOWN"})
		}},
		{"gateway read", "gateway", "", func(c *Core, g, issue string) error { _, e := c.GatewayEvidence(ctx, g); return e }},
		{"gateway evidence read", "document", ":evidence", func(c *Core, g, issue string) error { _, e := c.GatewayEvidence(ctx, g); return e }},
		{"issue read", "document", "issue_", func(c *Core, g, issue string) error { return c.observeIssue(ctx, "new", "gateway", g, "/", true) }},
		{"issue write", "write", "", func(c *Core, g, issue string) error { return c.observeIssue(ctx, "new", "gateway", g, "/", true) }},
		{"issue list", "documents", "issue", func(c *Core, g, issue string) error { _, e := c.Issues(ctx, storage.Page{}); return e }},
		{"ack read", "document", "issue_", func(c *Core, g, issue string) error { _, e := c.AcknowledgeIssue(ctx, issue, 1); return e }},
		{"ack write", "write", "", func(c *Core, g, issue string) error { _, e := c.AcknowledgeIssue(ctx, issue, 1); return e }},
		{"scan gateways", "gateways", "", func(c *Core, g, issue string) error { return c.RefreshCommunityIssues(ctx) }},
		{"scan gateway evidence", "gateway", "", func(c *Core, g, issue string) error { return c.RefreshCommunityIssues(ctx) }},
		{"scan gateway issue", "write", "", func(c *Core, g, issue string) error { return c.RefreshCommunityIssues(ctx) }},
		{"scan releases", "documents", "nginx-release", func(c *Core, g, issue string) error { return c.RefreshCommunityIssues(ctx) }},
		{"scan release detail", "document", "ngrel_", func(c *Core, g, issue string) error { return c.RefreshCommunityIssues(ctx) }},
		{"scan certificates", "certificates", "", func(c *Core, g, issue string) error { return c.RefreshCommunityIssues(ctx) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, r := releaseFixture(t, 1)
			g := r.Targets[0].GatewayID
			if err := c.observeIssue(ctx, "initial", "gateway", g, "/", true); err != nil {
				t.Fatal(err)
			}
			issues, err := c.Issues(ctx, storage.Page{})
			if err != nil {
				t.Fatal(err)
			}
			c.store = &communityBoundaryStore{&nginxBoundaryStore{Store: c.store, method: test.method, key: test.key, failure: failure}}
			if err = test.run(c, g, issues[0].ID); !errors.Is(err, failure) {
				t.Fatalf("state failure reported success: %v", err)
			}
		})
	}
	for _, conflict := range []error{storage.ErrConflict, storage.ErrPrecondition} {
		c, _, _ := controlFixture(t)
		c.store = &nginxBoundaryStore{Store: c.store, method: "write", failure: conflict}
		if err := c.observeIssue(ctx, "failure", "gateway", "g", "/", true); err != nil {
			t.Fatalf("concurrent issue writer not tolerated: %v", err)
		}
		if err := c.RecordGatewayEvidence(ctx, "g", GatewayEvidence{ObservedAt: c.now(), RefreshIntervalMillis: 1000, StartupState: "UNKNOWN"}); err != nil {
			t.Fatalf("concurrent evidence writer not tolerated: %v", err)
		}
	}
}

func TestCommunityCertificateAndReleaseRisksRemainVisible(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	now := c.now()
	c.now = func() time.Time { return now }
	for _, state := range []string{"UNKNOWN", "PARTIAL", "FAILED", "STOPPED", "SUCCEEDED"} {
		r := NginxRelease{PoolID: pool.ID, Status: state, FinishedAt: &now}
		r.ID = id.New("ngrel")
		if err := c.saveControl(ctx, r.ID, "nginx-release", pool.ID, r, 0); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []string{"failed renewal", "certificate error", "near expiry", "no expiry", "future expiry"} {
		cert := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "custom", Status: model.CertificateActive, Revision: 1, CreatedAt: now, UpdatedAt: now}
		switch state {
		case "failed renewal":
			cert.LastError = "renewal failed"
		case "certificate error":
			cert.Status = model.CertificateError
		case "near expiry":
			at := now.Add(time.Hour)
			cert.NotAfter = &at
		case "future expiry":
			at := now.Add(31 * 24 * time.Hour)
			cert.NotAfter = &at
		}
		if err := c.store.CreateCertificate(ctx, &cert); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.RefreshCommunityIssues(ctx); err != nil {
		t.Fatal(err)
	}
	issues, err := c.Issues(ctx, storage.Page{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, issue := range issues {
		if !issue.Active {
			t.Fatal("new risk was not active")
		}
		counts[issue.Code]++
	}
	if counts["release_attention"] != 4 || counts["certificate_risk"] != 3 {
		t.Fatalf("risk classification: %+v", counts)
	}
}

func TestGatewayEvidencePreservesLastConfirmationAndStartupRisk(t *testing.T) {
	c, r := releaseFixture(t, 1)
	ctx := context.Background()
	g := r.Targets[0].GatewayID
	now := c.now()
	c.now = func() time.Time { return now }
	e := GatewayEvidence{LoadingConfirmed: true, ConfirmedRevision: r.ConfigRevision, ObservedAt: now, RefreshIntervalMillis: 1000, StartupState: "CONFIRMED", RenderedDigest: "same", DiskDigest: "same"}
	if err := c.RecordGatewayEvidence(ctx, g, e); err != nil {
		t.Fatal(err)
	}
	first, err := c.GatewayEvidence(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	e.ObservedAt = now
	e.LoadingConfirmed = false
	e.StartupState = "RISK"
	if err = c.RecordGatewayEvidence(ctx, g, e); err != nil {
		t.Fatal(err)
	}
	got, err := c.GatewayEvidence(ctx, g)
	if err != nil || got.LoadingConfirmed || got.ConfigurationState != "UNKNOWN" || got.LastConfirmedAt == nil || !got.LastConfirmedAt.Equal(*first.LastConfirmedAt) {
		t.Fatalf("unknown replaced last confirmation: %+v %v", got, err)
	}
	now = now.Add(3 * time.Second)
	got, err = c.GatewayEvidence(ctx, g)
	if err != nil || got.StartupState != "RISK" {
		t.Fatalf("expired evidence hid startup risk: %+v %v", got, err)
	}
}
