package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// Request contains references only. All authority and inputs come from the
// durable attempt; possession of a peer certificate cannot supply a command.
type Request struct {
	OperationID string         `json:"operation_id"`
	Attempt     int            `json:"attempt"`
	LeaderTerm  int64          `json:"leader_term"`
	Target      TargetIdentity `json:"target"`
}

type Service struct {
	Verifier *Verifier
	Locker   lease.Locker
	Execute  func(context.Context, model.DeploymentOperation, model.Runtime) error
	mu       sync.Mutex
	wg       sync.WaitGroup
	closed   bool
}

func (s *Service) Wait() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Service) Accept(ctx context.Context, root context.Context, peer string, request Request) error {
	if root == nil || root.Err() != nil || s.Verifier == nil || s.Locker == nil || s.Execute == nil {
		return ErrUnavailable
	}
	record, err := Load(ctx, s.Verifier.Store, request.OperationID, request.Attempt)
	if err != nil {
		return ErrFenced
	}
	ref := record.Reference
	if peer != ref.LeaderID || request.LeaderTerm != ref.LeaderTerm || request.Target != ref.Target.Identity() || ref.Target.InstanceID != s.Verifier.Config.Controller.InstanceID {
		return ErrFenced
	}
	op, rt, err := s.Verifier.CheckClaim(ctx, ref)
	if err != nil {
		return err
	}
	// A duplicate request is observational. It cannot restart an accepted or
	// uncertain attempt, even if its original HTTP response was lost.
	if record.State != "SELECTED" {
		if record.State == "RUNNING" {
			return s.Verifier.CheckExecutor(ctx, record)
		}
		return ErrUnknown
	}
	held, err := s.Locker.TryLock(ctx, ExecutionLock(op.ID, op.Attempt))
	if err != nil {
		return err
	}
	session, ok := held.(lease.SessionLease)
	if !ok {
		_ = held.Release(context.Background())
		return ErrFenced
	}
	owner, err := session.SessionID(ctx)
	if err != nil || owner <= 0 {
		_ = held.Release(context.Background())
		return ErrFenced
	}
	now, err := s.Verifier.Now(ctx)
	if err != nil {
		_ = held.Release(context.Background())
		return err
	}
	record.State, record.AcceptedAt, record.ExecutionOwner = "RUNNING", &now, owner
	if err = Save(ctx, s.Verifier.Store, record, now); err != nil {
		_ = held.Release(context.Background())
		return err
	}
	record.Revision++
	if err = s.Verifier.CheckExecutor(ctx, record); err != nil {
		_ = held.Release(context.Background())
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = held.Release(context.Background())
		return ErrUnavailable
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer held.Release(context.Background())
		run, cancel := context.WithTimeout(root, s.Verifier.Config.Management.DeployTimeout.Std())
		defer cancel()
		run = WithRecord(storage.WithDeploymentOperation(run, op), s.Verifier, record)
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			timer := time.NewTicker(time.Second)
			defer timer.Stop()
			for {
				select {
				case <-stop:
					return
				case <-run.Done():
					return
				case <-timer.C:
					if s.Verifier.CheckExecutor(run, record) != nil || held.Check(run) != nil {
						cancel()
						return
					}
				}
			}
		}()
		err := s.Execute(run, op, rt)
		close(stop)
		<-done
		// The operation row, rather than an HTTP receipt, decides completion.
		// Failure to retain this annotation never permits replay: RUNNING with
		// CommandStartedAt is also treated as uncertain by the next scheduler.
		finish, end := context.WithTimeout(context.WithoutCancel(root), 3*time.Second)
		defer end()
		latest, readErr := Load(finish, s.Verifier.Store, op.ID, op.Attempt)
		if readErr != nil || latest.ExecutionOwner != owner {
			return
		}
		actual, readErr := s.Verifier.Store.GetDeploymentOperation(finish, op.ID)
		if readErr == nil && actual.Attempt == op.Attempt && actual.Status != model.OperationRunning && actual.Status != model.OperationQueued {
			latest.State, latest.Reason = "COMPLETED", ""
		} else {
			latest.State, latest.Reason = "UNKNOWN", "execution_result_unconfirmed"
			if latest.CommandStartedAt == nil && (errors.Is(err, ErrUnavailable) || errors.Is(err, ErrFenced) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
				latest.State, latest.Reason = "WAITING", "verified_executor_unavailable"
			}
			if err == nil {
				latest.Reason = "completion_receipt_unavailable"
			}
		}
		at, clockErr := s.Verifier.Now(finish)
		if clockErr == nil {
			_ = Save(finish, s.Verifier.Store, latest, at)
		}
	}()
	return nil
}

func (s *Service) Handler(root context.Context) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		peer, err := controlplane.CertificateIdentity(r.TLS.PeerCertificates[0])
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request Request
		var extra any
		if decoder.Decode(&request) != nil || !errors.Is(decoder.Decode(&extra), io.EOF) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err = s.Accept(r.Context(), root, peer, request); err != nil {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
}
