package certificate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	metricsx "github.com/yernsun/network-service-composer/internal/metrics"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestRecoveryHoldsPreventCertificateProvisioningRenewalAndCleanup(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	domain := model.Domain{ID: "restored-domain", Enabled: true, TLSMode: model.TLSManaged}
	expiry := now.Add(time.Hour)
	store := &schedulingStore{maintenanceStore: &maintenanceStore{
		domains: []model.Domain{domain}, certificates: []model.Certificate{{ID: "restored-certificate", Provider: "acme", Status: model.CertificateActive, NotAfter: &expiry}},
		recoveryHolds: map[string]bool{storage.RecoveryHoldID("domain-certificate", domain.ID): true, storage.RecoveryHoldID("certificate", "restored-certificate"): true},
	}, current: domain}
	worker := NewWorker(store, &maintenanceLocker{}, nil, config.Defaults(), metricsx.New(), nil)
	if err := worker.ensureManagedCertificates(ctx, now); err != nil || len(store.provisioned) != 0 {
		t.Fatal("held missing certificate was provisioned", err)
	}
	if err := worker.scheduleRenewals(ctx, now); err != nil || len(store.updated) != 0 {
		t.Fatal("held certificate was scheduled for renewal", err)
	}
	if err := worker.process(ctx, "restored-certificate"); err != nil || len(store.processed) != 0 {
		t.Fatal("held certificate reached its CA execution", err)
	}
	store.recoveryHolds[storage.RecoveryBarrierID] = true
	if err := worker.maintain(ctx, now); err != nil || store.prunedAt != nil {
		t.Fatal("unreviewed database entered maintenance", err)
	}
	store.recoveryError = errors.New("recovery evidence unavailable")
	if err := worker.process(ctx, "restored-certificate"); !errors.Is(err, store.recoveryError) {
		t.Fatal("unavailable hold state was treated as permission", err)
	}
	if err := worker.maintain(ctx, now); !errors.Is(err, store.recoveryError) {
		t.Fatal("unavailable barrier was treated as permission", err)
	}
	if err := worker.ensureManagedCertificates(ctx, now); !errors.Is(err, store.recoveryError) {
		t.Fatal("unavailable domain hold was treated as permission", err)
	}
	if err := worker.scheduleRenewals(ctx, now); !errors.Is(err, store.recoveryError) {
		t.Fatal("unavailable certificate hold was treated as permission", err)
	}
	worker.config.Controller.RecoveryMode = true
	if err := worker.reconcile(ctx); err != nil {
		t.Fatal("explicit recovery mode entered reconciliation", err)
	}
}
