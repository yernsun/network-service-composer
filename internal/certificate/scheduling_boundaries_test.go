package certificate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/lease"
	metricsx "github.com/yernsun/network-service-composer/internal/metrics"
	"github.com/yernsun/network-service-composer/internal/model"
)

type schedulingStore struct {
	*maintenanceStore
	current                                                    model.Domain
	pending                                                    []model.Certificate
	getDomainError, provisionError, pendingError, processError error
	provisioned                                                []model.Certificate
	processed                                                  []string
	poll                                                       func()
}

func (s *schedulingStore) GetDomain(context.Context, string) (model.Domain, error) {
	return s.current, s.getDomainError
}
func (s *schedulingStore) ProvisionManagedCertificate(_ context.Context, v *model.Certificate, _ string, _ time.Time) (int64, error) {
	s.provisioned = append(s.provisioned, *v)
	return 1, s.provisionError
}
func (s *schedulingStore) ListPendingCertificates(context.Context, time.Time, int) ([]model.Certificate, error) {
	if s.poll != nil {
		s.poll()
	}
	return s.pending, s.pendingError
}
func (s *schedulingStore) GetCertificate(_ context.Context, key string) (model.Certificate, error) {
	s.processed = append(s.processed, key)
	return model.Certificate{}, s.processError
}

func TestManagedCertificateRecoveryRechecksOwnershipAfterLease(t *testing.T) {
	for _, name := range []string{"success", "busy", "lock failed", "lease lost", "domain read failed", "became disabled", "became custom", "already linked", "provision failed", "provision canceled"} {
		t.Run(name, func(t *testing.T) {
			failure := errors.New("recovery dependency unavailable")
			d := model.Domain{ID: "domain", PoolID: "pool", Host: "app.example.test", Enabled: true, TLSMode: model.TLSManaged, ACMEChallenge: model.ACMEDNS01, DNSProviderRef: "dns"}
			s := &schedulingStore{maintenanceStore: &maintenanceStore{domains: []model.Domain{d}}, current: d}
			locker := &maintenanceLocker{}
			wantErr := false
			wantProvision := false
			wantRelease := 1
			switch name {
			case "success":
				wantProvision = true
			case "busy":
				locker.lockError = lease.ErrBusy
				wantRelease = 0
			case "lock failed":
				locker.lockError = failure
				wantErr = true
				wantRelease = 0
			case "lease lost":
				locker.checkError = failure
				wantErr = true
			case "domain read failed":
				s.getDomainError = failure
				wantErr = true
			case "became disabled":
				s.current.Enabled = false
			case "became custom":
				s.current.TLSMode = model.TLSCustom
			case "already linked":
				s.current.CertificateID = "existing"
			case "provision failed":
				s.provisionError = failure
				wantErr = true
				wantProvision = true
			case "provision canceled":
				s.provisionError = context.Canceled
				wantProvision = true
			}
			w := NewWorker(s, locker, nil, config.Defaults(), metricsx.New(), nil)
			err := w.ensureManagedCertificates(context.Background(), time.Now().UTC())
			if (err != nil) != wantErr {
				t.Fatalf("recovery error=%v wantError=%t", err, wantErr)
			}
			if (len(s.provisioned) > 0) != wantProvision || locker.releases != wantRelease {
				t.Fatalf("ownership violated: provisioned=%d released=%d", len(s.provisioned), locker.releases)
			}
			if wantProvision {
				v := s.provisioned[0]
				if v.PrimaryDomain != d.Host || v.ACMEChallenge != d.ACMEChallenge || v.DNSProviderRef != d.DNSProviderRef || v.Status != model.CertificatePending {
					t.Fatalf("recovery lost issuer requirements: %+v", v)
				}
			}
		})
	}
}

func TestCertificatePollingHandlesFailuresWithoutLosingLaterJobs(t *testing.T) {
	for _, name := range []string{"ensure failed", "pending failed", "busy", "lock failed", "lease lost", "job failed", "job canceled", "no jobs"} {
		t.Run(name, func(t *testing.T) {
			failure := errors.New("poll dependency unavailable")
			s := &schedulingStore{maintenanceStore: &maintenanceStore{}, pending: []model.Certificate{{ID: "custom", Provider: "custom"}, {ID: "first", Provider: "acme"}, {ID: "second", Provider: "acme"}}, processError: failure}
			locker := &maintenanceLocker{}
			wantErr := false
			wantJobs := 2
			wantRelease := 2
			switch name {
			case "ensure failed":
				s.domainError = failure
				wantErr = true
				wantJobs = 0
				wantRelease = 0
			case "pending failed":
				s.pendingError = failure
				wantErr = true
				wantJobs = 0
				wantRelease = 0
			case "busy":
				locker.lockError = lease.ErrBusy
				wantJobs = 0
				wantRelease = 0
			case "lock failed":
				locker.lockError = failure
				wantErr = true
				wantJobs = 0
				wantRelease = 0
			case "lease lost":
				locker.checkError = failure
				wantJobs = 0
			case "job canceled":
				s.processError = context.Canceled
			case "no jobs":
				s.pending = nil
				wantJobs = 0
				wantRelease = 0
			}
			w := NewWorker(s, locker, nil, config.Defaults(), metricsx.New(), nil)
			err := w.processPending(context.Background(), time.Now().UTC())
			if (err != nil) != wantErr || len(s.processed) != wantJobs || locker.releases != wantRelease {
				t.Fatalf("poll outcome=%v processed=%v released=%d", err, s.processed, locker.releases)
			}
			if wantJobs == 2 && (s.processed[0] != "first" || s.processed[1] != "second") {
				t.Fatal("failed first job prevented next job")
			}
		})
	}
}

func TestCertificateWorkerRunKeepsPollingAndCancels(t *testing.T) {
	for _, name := range []string{"normal", "maintenance fails", "poll fails", "default interval"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			s := &schedulingStore{maintenanceStore: &maintenanceStore{}}
			cfg := config.Defaults()
			cfg.Controller.Certificate.ReconcileInterval = config.Duration(time.Millisecond)
			polls := 0
			s.poll = func() {
				polls++
				if polls == 2 {
					cancel()
				}
			}
			switch name {
			case "maintenance fails":
				s.gcError = errors.New("GC unavailable")
			case "poll fails":
				s.pendingError = errors.New("pending unavailable")
			case "default interval":
				cfg.Controller.Certificate.ReconcileInterval = 0
				s.poll = func() { polls++; cancel() }
			}
			w := NewWorker(s, &maintenanceLocker{}, nil, cfg, metricsx.New(), nil)
			if err := w.Run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatal("worker did not cancel", err)
			}
			want := 2
			if name == "default interval" {
				want = 1
			}
			if polls != want {
				t.Fatalf("worker poll count=%d want=%d", polls, want)
			}
		})
	}
}
