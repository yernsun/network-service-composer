package certificate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type maintenanceStore struct {
	storage.Store
	recoveryHolds                                                   map[string]bool
	recoveryError                                                   error
	domains                                                         []model.Domain
	certificates                                                    []model.Certificate
	domainError, certificateError, updateError, gcError, pruneError error
	updated                                                         []model.Certificate
	gcResult                                                        storage.GCResult
	prunedAt                                                        *time.Time
	domainPages, certificatePages                                   []storage.Page
}

func (s *maintenanceStore) GetControlDocument(_ context.Context, key string) (storage.ControlDocument, error) {
	if s.recoveryError != nil {
		return storage.ControlDocument{}, s.recoveryError
	}
	if s.recoveryHolds[key] {
		return storage.ControlDocument{ID: key, Kind: "recovery-hold"}, nil
	}
	return storage.ControlDocument{}, storage.ErrNotFound
}

func (s *maintenanceStore) ListDomains(_ context.Context, page storage.Page) ([]model.Domain, error) {
	s.domainPages = append(s.domainPages, page)
	if s.domainError != nil {
		return nil, s.domainError
	}
	for index, value := range s.domains {
		if value.ID > page.After {
			return s.domains[index:min(index+page.Limit, len(s.domains))], nil
		}
	}
	return nil, nil
}
func (s *maintenanceStore) ListCertificates(_ context.Context, page storage.Page) ([]model.Certificate, error) {
	s.certificatePages = append(s.certificatePages, page)
	if s.certificateError != nil {
		return nil, s.certificateError
	}
	for index, value := range s.certificates {
		if value.ID > page.After {
			return s.certificates[index:min(index+page.Limit, len(s.certificates))], nil
		}
	}
	return nil, nil
}
func (s *maintenanceStore) UpdateCertificate(_ context.Context, value *model.Certificate, _ int64) error {
	s.updated = append(s.updated, *value)
	return s.updateError
}
func (s *maintenanceStore) GarbageCollect(context.Context, time.Time, time.Duration) (storage.GCResult, error) {
	return s.gcResult, s.gcError
}
func (s *maintenanceStore) PruneRuntimeEvents(_ context.Context, before time.Time) error {
	s.prunedAt = &before
	return s.pruneError
}

type maintenanceLocker struct {
	lockError, checkError error
	releases              int
}

func (l *maintenanceLocker) TryLock(context.Context, string) (lease.Lease, error) {
	if l.lockError != nil {
		return nil, l.lockError
	}
	return l, nil
}
func (l *maintenanceLocker) Check(context.Context) error   { return l.checkError }
func (l *maintenanceLocker) Release(context.Context) error { l.releases++; return nil }

func TestMaintenancePaginationIncludesEveryCertificateAndDomain(t *testing.T) {
	store := &maintenanceStore{}
	for index := 0; index < 201; index++ {
		name := fmt.Sprintf("%04d", index)
		store.domains = append(store.domains, model.Domain{ID: name})
		store.certificates = append(store.certificates, model.Certificate{ID: name})
	}
	domains, err := listDomains(context.Background(), store)
	if err != nil || len(domains) != 201 || domains[200].ID != "0200" || len(store.domainPages) != 2 || store.domainPages[1].After != "0199" {
		t.Fatalf("domain pagination %#v %v", store.domainPages, err)
	}
	certificates, err := listCertificates(context.Background(), store)
	if err != nil || len(certificates) != 201 || certificates[200].ID != "0200" || len(store.certificatePages) != 2 || store.certificatePages[1].After != "0199" {
		t.Fatalf("certificate pagination %#v %v", store.certificatePages, err)
	}
	failure := errors.New("list unavailable")
	store.domainError = failure
	store.certificateError = failure
	if values, err := listDomains(context.Background(), store); !errors.Is(err, failure) || values != nil {
		t.Fatalf("partial domain list accepted: %v", err)
	}
	if values, err := listCertificates(context.Background(), store); !errors.Is(err, failure) || values != nil {
		t.Fatalf("partial certificate list accepted: %v", err)
	}
}

func TestRenewalSchedulingPreservesAssetsAndHonorsRetryState(t *testing.T) {
	now := time.Now().UTC()
	expired := now.Add(-time.Hour)
	future := now.Add(90 * 24 * time.Hour)
	retry := now.Add(time.Hour)
	for _, tc := range []struct {
		name              string
		certificate       model.Certificate
		updateError       error
		scheduled, failed bool
	}{
		{"custom", model.Certificate{Provider: "custom", Status: model.CertificateActive, NotAfter: &expired}, nil, false, false},
		{"pending", model.Certificate{Provider: "acme", Status: model.CertificatePending, NotAfter: &expired}, nil, false, false},
		{"no expiration", model.Certificate{Provider: "acme", Status: model.CertificateActive}, nil, false, false},
		{"retry scheduled", model.Certificate{Provider: "acme", Status: model.CertificateActive, NotAfter: &expired, NextAttemptAt: &retry}, nil, false, false},
		{"not due", model.Certificate{Provider: "acme", Status: model.CertificateActive, NotAfter: &future}, nil, false, false},
		{"due", model.Certificate{Provider: "acme", Status: model.CertificateActive, NotAfter: &expired}, nil, true, false},
		{"concurrent change", model.Certificate{Provider: "acme", Status: model.CertificateActive, NotAfter: &expired}, storage.ErrConflict, true, false},
		{"storage failed", model.Certificate{Provider: "acme", Status: model.CertificateActive, NotAfter: &expired}, errors.New("write failed"), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.certificate.ID = "certificate"
			tc.certificate.CertificatePEM = "current certificate"
			tc.certificate.EncryptedPrivateKey = []byte("encrypted key")
			store := &maintenanceStore{certificates: []model.Certificate{tc.certificate}, updateError: tc.updateError}
			cfg := config.Defaults()
			cfg.Controller.Certificate.RenewBeforeExpiry = 0
			worker := NewWorker(store, nil, nil, cfg, nil, nil)
			err := worker.scheduleRenewals(context.Background(), now)
			if (err != nil) != tc.failed || (len(store.updated) > 0) != tc.scheduled {
				t.Fatalf("scheduling updated=%d error=%v", len(store.updated), err)
			}
			if tc.scheduled {
				updated := store.updated[0]
				if updated.Status != model.CertificateRenewing || updated.NextAttemptAt == nil || !updated.NextAttemptAt.Equal(now) || updated.CertificatePEM != tc.certificate.CertificatePEM || string(updated.EncryptedPrivateKey) != "encrypted key" {
					t.Fatalf("renewal destroyed active material: %#v", updated)
				}
			}
		})
	}
}

func TestCertificateGarbageCollectionLeaseAndStoreFailures(t *testing.T) {
	failure := errors.New("GC unavailable")
	for _, tc := range []struct {
		name                                          string
		lockError, checkError, storeError, pruneError error
		result                                        storage.GCResult
		failed                                        bool
	}{
		{"busy", lease.ErrBusy, nil, nil, nil, storage.GCResult{}, false}, {"lock unavailable", failure, nil, nil, nil, storage.GCResult{}, true},
		{"ownership lost", nil, lease.ErrLost, nil, nil, storage.GCResult{}, true}, {"store unavailable", nil, nil, failure, nil, storage.GCResult{}, true},
		{"prune unavailable", nil, nil, nil, failure, storage.GCResult{}, true},
		{"nothing expired", nil, nil, nil, nil, storage.GCResult{}, false}, {"challenges expired", nil, nil, nil, nil, storage.GCResult{ExpiredChallenges: 1}, false}, {"certificates expired", nil, nil, nil, nil, storage.GCResult{Certificates: 1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &maintenanceStore{gcError: tc.storeError, pruneError: tc.pruneError, gcResult: tc.result}
			locker := &maintenanceLocker{lockError: tc.lockError, checkError: tc.checkError}
			worker := NewWorker(store, locker, nil, config.Defaults(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			now := time.Now().UTC()
			err := worker.garbageCollect(context.Background(), now)
			if (err != nil) != tc.failed {
				t.Fatalf("GC result %v", err)
			}
			if (locker.releases == 1) != (tc.lockError == nil) {
				t.Fatalf("GC lease leaked: %d", locker.releases)
			}
			if tc.lockError == nil && tc.checkError == nil && tc.storeError == nil {
				want := now.Add(-90 * 24 * time.Hour)
				if store.prunedAt == nil || !store.prunedAt.Equal(want) {
					t.Fatalf("runtime event retention cutoff = %v, want %v", store.prunedAt, want)
				}
			} else if store.prunedAt != nil {
				t.Fatal("runtime events pruned without a valid GC lease and store result")
			}
		})
	}
}
