package certificate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/lease"
	metricsx "github.com/yernsun/network-service-composer/internal/metrics"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func certificateJobFixture(t *testing.T, change func(*model.Certificate)) (*Worker, *storage.SQLStore, model.Certificate) {
	t.Helper()
	store := certificateTestStore(t)
	now := time.Now().UTC()
	ctx := context.Background()
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "job", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	value := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "acme", ACMEChallenge: model.ACMEHTTP01, Status: model.CertificatePending, PrimaryDomain: "job.example.test", Domains: []string{"job.example.test"}, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if change != nil {
		change(&value)
	}
	if err := store.CreateCertificate(ctx, &value); err != nil {
		t.Fatal(err)
	}
	return NewWorker(store, lease.New(store.DB(), store.Dialect()), nil, config.Defaults(), metricsx.New(), nil), store, value
}

func TestCertificateJobFailurePreservesExistingAssetsAndSchedulesRetry(t *testing.T) {
	for _, active := range []bool{false, true} {
		worker, store, value := certificateJobFixture(t, func(c *model.Certificate) {
			if active {
				c.CertificatePEM = "old certificate"
				c.EncryptedPrivateKey = []byte("encrypted old key")
				c.Status = model.CertificateActive
			}
		})
		err := worker.process(context.Background(), value.ID)
		if err == nil || !strings.Contains(err.Error(), "terms") {
			t.Fatalf("unexpected issuance result: %v", err)
		}
		updated, err := store.GetCertificate(context.Background(), value.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := model.CertificateError
		if active {
			want = model.CertificateActive
		}
		if updated.Status != want || updated.AttemptCount != 1 || updated.NextAttemptAt == nil || !updated.NextAttemptAt.After(time.Now()) || updated.CertificatePEM != value.CertificatePEM || string(updated.EncryptedPrivateKey) != string(value.EncryptedPrivateKey) {
			t.Fatalf("unsafe failed issuance: %#v", updated)
		}
	}
}

func TestCertificateChallengeAndGatewayEvidenceBlockPrematureIssuance(t *testing.T) {
	for _, tc := range []struct {
		name      string
		challenge model.ACMEChallengeType
		reference string
	}{
		{"unknown", "unknown", ""}, {"HTTP with DNS", model.ACMEHTTP01, "provider"}, {"DNS missing provider", model.ACMEDNS01, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worker, store, value := certificateJobFixture(t, func(c *model.Certificate) { c.ACMEChallenge = tc.challenge; c.DNSProviderRef = tc.reference })
			if err := worker.process(context.Background(), value.ID); err == nil {
				t.Fatal("invalid challenge reached issuance")
			}
			updated, err := store.GetCertificate(context.Background(), value.ID)
			if err != nil || updated.Status != model.CertificateError || updated.AttemptCount != 1 {
				t.Fatalf("invalid challenge not recorded: %#v %v", updated, err)
			}
		})
	}
	worker, store, value := certificateJobFixture(t, func(c *model.Certificate) { c.DistributionRevision = 12 })
	if err := worker.process(context.Background(), value.ID); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetCertificate(context.Background(), value.ID)
	if err != nil || updated.Status != model.CertificatePending || updated.AttemptCount != 0 || updated.NextAttemptAt == nil || !strings.Contains(updated.LastError, "gateway ACK") {
		t.Fatalf("unconfirmed challenge published: %#v %v", updated, err)
	}
}

func TestRevocationWithoutMaterialAndFailedRevocationKeepExplicitState(t *testing.T) {
	for _, material := range []bool{false, true} {
		worker, store, value := certificateJobFixture(t, func(c *model.Certificate) {
			c.Status = model.CertificateRevoked
			c.LastError = "previous attempt"
			next := time.Now()
			c.NextAttemptAt = &next
			if material {
				c.CertificatePEM = "old certificate"
			}
		})
		err := worker.process(context.Background(), value.ID)
		if (err != nil) != material {
			t.Fatalf("revoke material=%t: %v", material, err)
		}
		updated, err := store.GetCertificate(context.Background(), value.ID)
		if err != nil || updated.Status != model.CertificateRevoked || updated.CertificatePEM != value.CertificatePEM {
			t.Fatalf("revocation changed asset: %#v %v", updated, err)
		}
		if material {
			if updated.AttemptCount != 1 || updated.NextAttemptAt == nil || updated.LastError == "" {
				t.Fatal("failed revocation lacks retry evidence")
			}
		} else if updated.NextAttemptAt != nil || updated.LastError != "" {
			t.Fatal("empty revocation retained obsolete attempt")
		}
	}
}

type certificateFailureStore struct {
	storage.Store
	readError, writeError, errorACK error
}

func (s *certificateFailureStore) GetCertificate(ctx context.Context, key string) (model.Certificate, error) {
	if s.readError != nil {
		return model.Certificate{}, s.readError
	}
	return s.Store.GetCertificate(ctx, key)
}
func (s *certificateFailureStore) UpdateCertificate(ctx context.Context, value *model.Certificate, revision int64) error {
	if s.writeError != nil {
		return s.writeError
	}
	return s.Store.UpdateCertificate(ctx, value, revision)
}
func (s *certificateFailureStore) GatewayACKed(ctx context.Context, pool string, revision int64, now time.Time) (bool, error) {
	if s.errorACK != nil {
		return false, s.errorACK
	}
	return s.Store.GatewayACKed(ctx, pool, revision, now)
}

func TestCertificateFailurePersistenceAndCancellationStayVisible(t *testing.T) {
	failure := errors.New("store failed")
	cause := errors.New("order failed")
	for _, boundary := range []string{"read", "write", "ACK"} {
		t.Run(boundary, func(t *testing.T) {
			worker, store, value := certificateJobFixture(t, func(c *model.Certificate) {
				if boundary == "ACK" {
					c.DistributionRevision = 1
				}
			})
			wrapped := &certificateFailureStore{Store: store}
			switch boundary {
			case "read":
				wrapped.readError = failure
			case "write":
				wrapped.writeError = failure
			case "ACK":
				wrapped.errorACK = failure
			}
			worker.store = wrapped
			if err := worker.process(context.Background(), value.ID); !errors.Is(err, failure) {
				t.Fatalf("process failure hidden: %v", err)
			}
			if boundary != "ACK" {
				if err := worker.fail(context.Background(), value, false, cause); !errors.Is(err, failure) {
					t.Fatalf("retry persistence hidden: %v", err)
				}
			}
		})
	}
	worker, store, value := certificateJobFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := worker.failUnlessCanceled(ctx, value, false, cause); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled issuance: %v", err)
	}
	value.CertificatePEM = "existing"
	if err := worker.revoke(ctx, value); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled revocation: %v", err)
	}
	updated, err := store.GetCertificate(context.Background(), value.ID)
	if err != nil || updated.AttemptCount != 0 || updated.Revision != value.Revision {
		t.Fatal("cancellation recorded as a failed order")
	}
	if truncateError(nil) != "" || len(truncateError(errors.New(strings.Repeat("x", 2049)))) != 2048 {
		t.Fatal("unsafe error size")
	}
}
