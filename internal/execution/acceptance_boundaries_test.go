package execution

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func executionRequest(f *executionFixture) Request {
	return Request{OperationID: f.op.ID, Attempt: f.op.Attempt, LeaderTerm: f.ref.LeaderTerm, Target: f.ref.Target.Identity()}
}
func selectedAttempt(t *testing.T, f *executionFixture) {
	t.Helper()
	if err := Save(t.Context(), f.store, Record{Reference: f.ref, State: "SELECTED", SelectedAt: f.now}, f.now); err != nil {
		t.Fatal(err)
	}
}

type acceptanceLocker struct {
	lease lease.Lease
	err   error
}

func (l acceptanceLocker) TryLock(context.Context, string) (lease.Lease, error) {
	return l.lease, l.err
}

type acceptanceLease struct {
	owner    int64
	err      error
	released *atomic.Bool
}

func (l acceptanceLease) Check(context.Context) error              { return nil }
func (l acceptanceLease) Release(context.Context) error            { l.released.Store(true); return nil }
func (l acceptanceLease) SessionID(context.Context) (int64, error) { return l.owner, l.err }

type nonSessionLease struct{ released *atomic.Bool }

func (l nonSessionLease) Check(context.Context) error   { return nil }
func (l nonSessionLease) Release(context.Context) error { l.released.Store(true); return nil }

type acceptanceStore struct {
	storage.Store
	write func(context.Context, []storage.ControlWrite) error
}

func (s acceptanceStore) WriteControlDocuments(ctx context.Context, writes []storage.ControlWrite) error {
	return s.write(ctx, writes)
}

func TestAcceptanceFailureNeverStartsUnownedWorkAndReleasesLease(t *testing.T) {
	for _, scenario := range []string{"nil-root", "cancelled-root", "missing-verifier", "missing-locker", "missing-runner", "busy", "no-session-proof", "session-unavailable", "invalid-owner", "clock-lost", "database-lost", "lease-lost-before-start", "closed"} {
		t.Run(scenario, func(t *testing.T) {
			f := newExecutionFixture(t)
			selectedAttempt(t, f)
			var calls atomic.Int32
			var released atomic.Bool
			service := &Service{Verifier: f.v, Locker: f.locks, Execute: func(context.Context, model.DeploymentOperation, model.Runtime) error { calls.Add(1); return nil }}
			root := t.Context()
			switch scenario {
			case "nil-root":
				root = nil
			case "cancelled-root":
				ctx, cancel := context.WithCancel(root)
				cancel()
				root = ctx
			case "missing-verifier":
				service.Verifier = nil
			case "missing-locker":
				service.Locker = nil
			case "missing-runner":
				service.Execute = nil
			case "busy":
				service.Locker = acceptanceLocker{err: lease.ErrBusy}
			case "no-session-proof":
				service.Locker = acceptanceLocker{lease: nonSessionLease{&released}}
			case "session-unavailable":
				service.Locker = acceptanceLocker{lease: acceptanceLease{owner: 77, err: lease.ErrLost, released: &released}}
			case "invalid-owner":
				service.Locker = acceptanceLocker{lease: acceptanceLease{owner: 0, released: &released}}
			case "clock-lost":
				var reads atomic.Int32
				f.v.Now = func(context.Context) (time.Time, error) {
					if reads.Add(1) > 1 {
						return time.Time{}, errors.New("clock unavailable")
					}
					return f.now, nil
				}
			case "database-lost":
				f.v.Store = acceptanceStore{Store: f.store, write: func(context.Context, []storage.ControlWrite) error { return errors.New("database unavailable") }}
			case "lease-lost-before-start":
				f.v.Store = acceptanceStore{Store: f.store, write: func(ctx context.Context, writes []storage.ControlWrite) error {
					err := f.store.WriteControlDocuments(ctx, writes)
					f.locks.set(ExecutionLock(f.op.ID, f.op.Attempt), 0)
					return err
				}}
			case "closed":
				service.Wait()
			}
			if err := service.Accept(t.Context(), root, "controller-a", executionRequest(f)); err == nil {
				t.Fatal("unavailable authority admitted work")
			}
			service.Wait()
			if calls.Load() != 0 {
				t.Fatal("failed admission invoked Docker", calls.Load())
			}
			if scenario == "no-session-proof" || scenario == "session-unavailable" || scenario == "invalid-owner" {
				if !released.Load() {
					t.Fatal("failed admission retained lease")
				}
			}
			actual, err := f.store.GetDeploymentOperation(t.Context(), f.op.ID)
			if err != nil || actual.Status != model.OperationRunning || len(actual.EncryptedInput) == 0 {
				t.Fatal("admission failure lost durable intent", actual.Status, err)
			}
		})
	}
}

func TestAcceptanceDuplicateAndUncertainReceiptNeverReplay(t *testing.T) {
	f := newExecutionFixture(t)
	selectedAttempt(t, f)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	service := &Service{Verifier: f.v, Locker: f.locks, Execute: func(ctx context.Context, _ model.DeploymentOperation, _ model.Runtime) error {
		calls.Add(1)
		if err := MarkCommand(ctx); err != nil {
			return err
		}
		close(entered)
		<-release
		return ErrUnknown
	}}
	if err := service.Accept(t.Context(), t.Context(), "controller-a", executionRequest(f)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		service.Wait()
		t.Fatal("executor did not start")
	}
	if err := service.Accept(t.Context(), t.Context(), "controller-a", executionRequest(f)); err != nil {
		t.Fatal("duplicate live acceptance was not observational", err)
	}
	close(release)
	service.Wait()
	if err := service.Accept(t.Context(), t.Context(), "controller-a", executionRequest(f)); !errors.Is(err, ErrUnknown) {
		t.Fatal("unknown acceptance was restarted", err)
	}
	actual, err := f.store.GetDeploymentOperation(t.Context(), f.op.ID)
	if err != nil || actual.Status != model.OperationRunning || len(actual.EncryptedInput) == 0 || calls.Load() != 1 {
		t.Fatal(actual.Status, calls.Load(), err)
	}
}

func TestAcceptanceRetainsWaitingOrUnknownAccordingToActualWriteBoundary(t *testing.T) {
	for _, scenario := range []string{"unavailable", "fenced", "unconfirmed-return", "unexpected-failure", "lease-loss", "root-cancel", "deadline-before-write", "owner-replaced"} {
		t.Run(scenario, func(t *testing.T) {
			f := newExecutionFixture(t)
			selectedAttempt(t, f)
			if scenario == "deadline-before-write" {
				f.v.Config.Management.DeployTimeout = config.Duration(20 * time.Millisecond)
			}
			root, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered := make(chan struct{})
			service := &Service{Verifier: f.v, Locker: f.locks, Execute: func(ctx context.Context, _ model.DeploymentOperation, _ model.Runtime) error {
				close(entered)
				switch scenario {
				case "unavailable":
					return ErrUnavailable
				case "fenced":
					return ErrFenced
				case "unconfirmed-return":
					return nil
				case "unexpected-failure":
					return errors.New("unconfirmed external effect")
				}
				<-ctx.Done()
				if scenario == "root-cancel" || scenario == "deadline-before-write" {
					return ctx.Err()
				}
				return ErrFenced
			}}
			if err := service.Accept(t.Context(), root, "controller-a", executionRequest(f)); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				cancel()
				service.Wait()
				t.Fatal("no executor")
			}
			if scenario == "lease-loss" {
				f.locks.set(ExecutionLock(f.op.ID, f.op.Attempt), 0)
			}
			if scenario == "owner-replaced" {
				record, err := Load(t.Context(), f.store, f.op.ID, f.op.Attempt)
				if err != nil {
					t.Fatal(err)
				}
				record.ExecutionOwner++
				if err = Save(t.Context(), f.store, record, f.now); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "root-cancel" {
				cancel()
			}
			done := make(chan struct{})
			go func() { service.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(4 * time.Second):
				cancel()
				<-done
				t.Fatal("executor was not fenced within its check interval")
			}
			record, err := Load(t.Context(), f.store, f.op.ID, f.op.Attempt)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "owner-replaced" {
				if record.ExecutionOwner != 78 || record.State != "RUNNING" {
					t.Fatal("late executor overwrote replacement", record.State, record.ExecutionOwner)
				}
			} else {
				want := "WAITING"
				if scenario == "unconfirmed-return" || scenario == "unexpected-failure" {
					want = "UNKNOWN"
				}
				if record.State != want {
					t.Fatal("uncertainty was lost", scenario, record.State, record.Reason)
				}
			}
			actual, err := f.store.GetDeploymentOperation(t.Context(), f.op.ID)
			if err != nil || actual.Status != model.OperationRunning || len(actual.EncryptedInput) == 0 {
				t.Fatal(actual.Status, err)
			}
		})
	}
}

func TestSQLVerifierRejectsIncompleteProvisioningBeforeConnecting(t *testing.T) {
	f := newExecutionFixture(t)
	s, err := storage.Open("postgres", "postgres://fixture:unused@127.0.0.1:1/fixture?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, scenario := range []string{"valid", "nil-store", "no-reports", "no-keys", "no-instance", "no-enrollments", "short-session-key"} {
		t.Run(scenario, func(t *testing.T) {
			store, cfg, reports, keys := s, f.v.Config, f.v.Reports, f.v.Keys
			switch scenario {
			case "nil-store":
				store = nil
			case "no-reports":
				reports = nil
			case "no-keys":
				keys = nil
			case "no-instance":
				cfg.Controller.InstanceID = ""
			case "no-enrollments":
				cfg.Controller.Nodes = nil
			case "short-session-key":
				cfg.Security.SessionKey = "short"
			}
			v, err := NewSQLVerifier(store, cfg, reports, keys)
			if scenario == "valid" {
				if err != nil || v == nil {
					t.Fatal(err)
				}
			} else if err == nil || v != nil {
				t.Fatal("incomplete provisioning accepted", v, err)
			}
		})
	}
}
