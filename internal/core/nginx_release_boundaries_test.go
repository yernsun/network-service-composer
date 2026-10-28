package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestReleaseSummaryPreservesUnknownPartialAndHistoricalOutcomes(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-2 * time.Minute)
	recent := old.Add(time.Nanosecond)
	for _, test := range []struct {
		name    string
		targets []NginxReleaseTarget
		stopped bool
		want    string
	}{
		{"empty", nil, false, "UNKNOWN"},
		{"pending", []NginxReleaseTarget{{Status: "PENDING"}}, false, "PUBLISHING"},
		{"running", []NginxReleaseTarget{{Status: "RUNNING", StartedAt: &recent}}, false, "PUBLISHING"},
		{"exact timeout", []NginxReleaseTarget{{Status: "RUNNING", StartedAt: &old}}, false, "UNKNOWN"},
		{"unknown", []NginxReleaseTarget{{Status: "SUCCEEDED"}, {Status: "UNKNOWN"}}, false, "UNKNOWN"},
		{"success", []NginxReleaseTarget{{Status: "SUCCEEDED"}, {Status: "SUCCEEDED"}}, false, "SUCCEEDED"},
		{"partial", []NginxReleaseTarget{{Status: "SUCCEEDED"}, {Status: "FAILED"}}, false, "PARTIAL"},
		{"failed", []NginxReleaseTarget{{Status: "FAILED"}, {Status: "PENDING"}}, false, "FAILED"},
		{"stopped", []NginxReleaseTarget{{Status: "PENDING"}}, true, "STOPPED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			v := NginxRelease{Targets: test.targets, Stopped: test.stopped}
			summarizeRelease(&v, now)
			if v.Status != test.want {
				t.Fatalf("summary=%s want=%s", v.Status, test.want)
			}
		})
	}
	history := NginxRelease{Status: "SUCCEEDED", FinishedAt: &old, Targets: []NginxReleaseTarget{{Status: "UNKNOWN"}}}
	summarizeRelease(&history, now)
	if history.Status != "SUCCEEDED" {
		t.Fatal("later observations rewrote historical result")
	}
}

func TestReleaseVerificationRequiresFreshMatchingEvidence(t *testing.T) {
	for _, kind := range []string{"confirmed", "previous revision", "unconfirmed", "missing confirmation time", "missing start", "old confirmation", "different revision", "missing model", "different model", "startup risk"} {
		t.Run(kind, func(t *testing.T) {
			c, r := releaseFixture(t, 1)
			ctx := context.Background()
			gateway := r.Targets[0].GatewayID
			if snapshot, managed, err := c.ClaimNginxRelease(ctx, r.PoolID, gateway, r.ConfigRevision); err != nil || !managed || snapshot == nil {
				t.Fatalf("claim failed: %v", err)
			}
			if err := c.MarkNginxReleaseSent(ctx, r.PoolID, gateway, r.ConfigRevision, "candidate-model"); err != nil {
				t.Fatal(err)
			}
			r, err := c.GetNginxRelease(ctx, r.PoolID, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			started := *r.Targets[0].StartedAt
			now := started.Add(time.Second)
			c.now = func() time.Time { return now }
			e := GatewayEvidence{LoadingConfirmed: true, ConfirmedRevision: r.ConfigRevision, ModelDigest: "candidate-model", RenderedDigest: "rendered", StartupState: "CONFIRMED", ObservedAt: now, ReceivedAt: now, RefreshIntervalMillis: 5000, LastConfirmedAt: &now}
			want := "PUBLISHING"
			switch kind {
			case "confirmed":
				want = "SUCCEEDED"
			case "previous revision":
				e.ConfirmedRevision = r.Targets[0].BeforeRevision
				want = "FAILED"
			case "unconfirmed":
				e.LoadingConfirmed = false
			case "missing confirmation time":
				e.LastConfirmedAt = nil
			case "missing start":
				r.Targets[0].StartedAt = nil
			case "old confirmation":
				e.LastConfirmedAt = &started
			case "different revision":
				e.ConfirmedRevision = r.ConfigRevision + 20
			case "missing model":
				r.Targets[0].ModelDigest = ""
			case "different model":
				e.ModelDigest = "unrelated-model"
			case "startup risk":
				e.StartupState = "RISK"
			}
			if kind == "missing start" || kind == "missing model" {
				if err = c.saveControl(ctx, r.ID, "nginx-release", r.PoolID, r, r.Revision); err != nil {
					t.Fatal(err)
				}
				r, err = c.GetNginxRelease(ctx, r.PoolID, r.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			// Seed persisted evidence, including legacy/incomplete records that the
			// verifier must treat conservatively rather than filling in missing facts.
			if err = c.saveControl(ctx, gateway+":evidence", "gateway-evidence", gateway, e, 0); err != nil {
				t.Fatal(err)
			}
			got, err := c.VerifyNginxRelease(ctx, r.PoolID, r.ID, r.Revision)
			if err != nil || got.Status != want {
				t.Fatalf("verification=%+v err=%v want=%s", got, err, want)
			}
			if want == "SUCCEEDED" {
				if got.FinishedAt == nil || got.Targets[0].VerifiedAt == nil || got.Targets[0].ConfirmedRevision != r.ConfigRevision || got.Targets[0].RenderedDigest != "rendered" {
					t.Fatal("success omitted confirmation evidence")
				}
			} else if got.FinishedAt != nil {
				t.Fatal("unverified release completed")
			}
		})
	}
}

func TestReleaseClaimAndResultStorageFailuresAreConservative(t *testing.T) {
	failure := errors.New("release storage interrupted")
	ctx := context.Background()
	for _, fault := range []string{"head", "snapshot", "write", "claim conflict"} {
		t.Run("claim/"+fault, func(t *testing.T) {
			c, r := releaseFixture(t, 1)
			original := c.store
			method, key := "document", "nginx-release-head"
			cause := failure
			if fault == "snapshot" {
				key = ":snapshot"
			}
			if fault == "write" || fault == "claim conflict" {
				method = "write"
				key = ""
			}
			if fault == "claim conflict" {
				cause = storage.ErrPrecondition
			}
			c.store = &nginxBoundaryStore{Store: original, method: method, key: key, failure: cause}
			snapshot, managed, err := c.ClaimNginxRelease(ctx, r.PoolID, r.Targets[0].GatewayID, r.ConfigRevision)
			if fault == "claim conflict" {
				if err != nil {
					t.Fatalf("claim race should be held: %v", err)
				}
			} else if !errors.Is(err, failure) {
				t.Fatalf("claim error hidden: %v", err)
			}
			if !managed || snapshot != nil {
				t.Fatal("failed claim authorized a write")
			}
			c.store = original
			got, err := c.GetNginxRelease(ctx, r.PoolID, r.ID)
			if err != nil || got.Targets[0].Status != "PENDING" {
				t.Fatalf("failed claim persisted execution: %+v %v", got, err)
			}
		})
	}
	for _, fault := range []string{"read", "write", "conflicts", "retry"} {
		t.Run("result/"+fault, func(t *testing.T) {
			c, r := releaseFixture(t, 1)
			gateway := r.Targets[0].GatewayID
			if _, _, err := c.ClaimNginxRelease(ctx, r.PoolID, gateway, r.ConfigRevision); err != nil {
				t.Fatal(err)
			}
			original := c.store
			wrapper := &nginxBoundaryStore{Store: original, method: "write", failure: failure}
			want := failure
			if fault == "read" {
				wrapper.method = "document"
				wrapper.key = "nginx-release-head"
			}
			if fault == "conflicts" || fault == "retry" {
				wrapper.failure = storage.ErrPrecondition
				want = storage.ErrPrecondition
			}
			if fault == "retry" {
				wrapper.nth = 1
				want = nil
			}
			c.store = wrapper
			if err := c.RecordNginxReleaseResult(ctx, r.PoolID, gateway, r.ConfigRevision, "SUCCEEDED", "model", "rendered", ""); !errors.Is(err, want) {
				t.Fatalf("result write: %v want %v", err, want)
			}
			c.store = original
			got, err := c.GetNginxRelease(ctx, r.PoolID, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if fault == "retry" {
				if got.Status != "SUCCEEDED" || wrapper.calls != 2 {
					t.Fatalf("retry result lost: %+v calls=%d", got, wrapper.calls)
				}
			} else if got.Targets[0].Status != "RUNNING" {
				t.Fatalf("failed result changed evidence: %+v", got)
			}
			if fault == "conflicts" && wrapper.calls != 5 {
				t.Fatalf("unbounded result retry: %d", wrapper.calls)
			}
		})
	}
}

func TestReleaseLegacyUnrelatedAndStoppedMessages(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	if snapshot, managed, err := c.ClaimNginxRelease(ctx, pool.ID, "gateway", 1); err != nil || managed || snapshot != nil {
		t.Fatalf("legacy traffic incorrectly claimed: %v", err)
	}
	if err := c.MarkNginxReleaseSent(ctx, pool.ID, "gateway", 1, "digest"); err != nil {
		t.Fatal(err)
	}
	if err := c.RecordNginxReleaseResult(ctx, pool.ID, "gateway", 1, "SUCCEEDED", "", "", ""); err != nil {
		t.Fatal(err)
	}
	c, r := releaseFixture(t, 2)
	first := r.Targets[0].GatewayID
	if err := c.MarkNginxReleaseSent(ctx, r.PoolID, first, r.ConfigRevision, "digest"); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("unsent candidate recorded as sent")
	}
	if err := c.MarkNginxReleaseSent(ctx, r.PoolID, first, r.ConfigRevision+1, "digest"); err != nil {
		t.Fatal(err)
	}
	if err := c.RecordNginxReleaseResult(ctx, r.PoolID, first, r.ConfigRevision+1, "SUCCEEDED", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := c.RecordNginxReleaseResult(ctx, r.PoolID, "foreign", r.ConfigRevision, "SUCCEEDED", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.StopNginxRelease(ctx, r.PoolID, r.ID, r.Revision+1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("stale stop accepted")
	}
	if _, err := c.VerifyNginxRelease(ctx, r.PoolID, r.ID, r.Revision+1); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("stale verification accepted")
	}
	stopped, err := c.StopNginxRelease(ctx, r.PoolID, r.ID, r.Revision)
	if err != nil || stopped.Status != "STOPPED" {
		t.Fatalf("stop: %+v %v", stopped, err)
	}
	if snapshot, managed, err := c.ClaimNginxRelease(ctx, r.PoolID, first, r.ConfigRevision); err != nil || !managed || snapshot != nil {
		t.Fatal("stopped release dispatched")
	}
}

func TestFrozenReleaseSnapshotRestoresOnlyItsOwnSecrets(t *testing.T) {
	c, r := releaseFixture(t, 1)
	ctx := context.Background()
	snapshot := model.Snapshot{PoolID: r.PoolID, Revision: r.ConfigRevision, Certificates: map[string]model.SnapshotCertificate{"certificate": {ID: "certificate", PrivateKeyPEM: "private recovery fixture", CertificatePEM: "public certificate"}}}
	w, err := c.freezeReleaseSnapshot("alternate", r.PoolID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(w.Document.Payload), "private recovery fixture") {
		t.Fatal("frozen recovery key stored in plaintext")
	}
	if err = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{w}); err != nil {
		t.Fatal(err)
	}
	var frozen frozenReleaseSnapshot
	if _, err = c.readEncrypted(ctx, w.Document.ID, "release-snapshot", &frozen); err != nil {
		t.Fatal(err)
	}
	if frozen.Keys["certificate"] != "private recovery fixture" || frozen.Snapshot.Certificates["certificate"].PrivateKeyPEM != "" {
		t.Fatal("separate recovery key mapping lost")
	}
	// Replace only the fixture's encrypted candidate to exercise transport
	// reconstruction; the production decision still comes from a claimed task.
	original, err := c.store.GetControlDocument(ctx, r.ID+":snapshot")
	if err != nil {
		t.Fatal(err)
	}
	w, err = c.encryptedWrite(r.ID+":snapshot", "release-snapshot", r.PoolID, frozen, original.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{w}); err != nil {
		t.Fatal(err)
	}
	got, managed, err := c.ClaimNginxRelease(ctx, r.PoolID, r.Targets[0].GatewayID, r.ConfigRevision)
	if err != nil || !managed || got == nil || got.Certificates["certificate"].PrivateKeyPEM != "private recovery fixture" {
		t.Fatalf("claimed snapshot did not restore secret: managed=%t err=%v", managed, err)
	}
}
