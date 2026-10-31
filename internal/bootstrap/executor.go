package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
)

// Driver must verify recorded IDs and object versions. It must not resolve a
// missing creation receipt by claiming a similarly named or labelled resource.
type Driver interface {
	Observe(context.Context) (any, error)
	Check(context.Context, Step) error
	Apply(context.Context, Step) (Resource, error)
	Reconcile(context.Context, Step) (*Resource, error)
}

// Rejected is a definite failure before an external write. Every other error
// after intent persistence is UNKNOWN, including timeouts and lost responses.
type Rejected struct{ Reason string }

func (e *Rejected) Error() string { return e.Reason }

type Executor struct {
	Journal interface{ Save(*Record) error }
	Driver  Driver
	Now     func() time.Time
}

func (e Executor) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

func (e Executor) Execute(ctx context.Context, r *Record, p Plan, approval, key string) (Operation, error) {
	if !ValidIdempotencyKey(key) {
		return Operation{}, fmt.Errorf("a bounded idempotency key is required")
	}
	for _, op := range r.Operations {
		if op.Key == key {
			if op.Plan.Hash != p.Hash {
				return op, fmt.Errorf("idempotency key belongs to another plan")
			}
			return op, nil
		}
		if op.State == Pending || op.State == Running || op.State == Unknown || op.State == Blocked {
			return op, fmt.Errorf("reconcile the unfinished operation before starting another")
		}
	}
	observation, err := e.Driver.Observe(ctx)
	if err != nil {
		return Operation{}, err
	}
	if err = p.Validate(*r, approval, observation, e.now()); err != nil {
		return Operation{}, err
	}
	p.Steps = cloneSteps(p.Steps)
	op := Operation{ID: id.New(id.Operation), Key: key, Plan: p, State: Pending, StartedAt: e.now(), UpdatedAt: e.now()}
	r.Operations = append(r.Operations, op)
	if err = e.Journal.Save(r); err != nil {
		return op, err
	}
	return e.run(ctx, r, len(r.Operations)-1)
}

func (e Executor) Resume(ctx context.Context, r *Record, operationID string) (Operation, error) {
	for i := range r.Operations {
		if r.Operations[i].ID != operationID {
			continue
		}
		if r.Operations[i].State == Succeeded {
			return r.Operations[i], nil
		}
		return e.run(ctx, r, i)
	}
	return Operation{}, fmt.Errorf("operation not found")
}

func (e Executor) save(r *Record, op *Operation) error {
	op.UpdatedAt = e.now()
	return e.Journal.Save(r)
}
func (e Executor) run(ctx context.Context, r *Record, index int) (Operation, error) {
	op := &r.Operations[index]
	for i := range op.Plan.Steps {
		s := &op.Plan.Steps[i]
		if s.State == Running || s.State == Unknown {
			receipt, err := e.Driver.Reconcile(ctx, *s)
			if err != nil || !validReceipt(receipt) {
				s.State, op.State, s.Reason = Unknown, Unknown, "outcome_unconfirmed"
				if saveErr := e.save(r, op); saveErr != nil {
					return *op, saveErr
				}
				return *op, fmt.Errorf("step %s has an unknown outcome; preserve resources and reconcile its receipt", s.Key)
			}
			s.Receipt, s.State = receipt, Succeeded
			r.Resources[s.Key] = *receipt
			if err = e.save(r, op); err != nil {
				return *op, err
			}
		}
		if err := e.Driver.Check(ctx, *s); err != nil {
			op.State, s.Reason = Blocked, "evidence_mismatch"
			if saveErr := e.save(r, op); saveErr != nil {
				return *op, saveErr
			}
			return *op, fmt.Errorf("step %s is blocked by missing or changed evidence", s.Key)
		}
		if s.State == Succeeded {
			continue
		}
		if err := ctx.Err(); err != nil {
			return *op, err
		}
		s.State, op.State, s.Reason = Running, Running, ""
		if err := e.save(r, op); err != nil {
			return *op, err
		}
		receipt, err := e.Driver.Apply(ctx, *s)
		if err != nil {
			var rejected *Rejected
			if errors.As(err, &rejected) {
				s.State, op.State, s.Reason = Failed, Failed, "change_rejected"
			} else {
				s.State, op.State, s.Reason = Unknown, Unknown, "outcome_unconfirmed"
			}
			if saveErr := e.save(r, op); saveErr != nil {
				return *op, saveErr
			}
			return *op, fmt.Errorf("step %s: %w", s.Key, err)
		}
		if !validReceipt(&receipt) {
			s.State, op.State = Unknown, Unknown
			if err = e.save(r, op); err != nil {
				return *op, err
			}
			return *op, fmt.Errorf("step %s returned an incomplete receipt", s.Key)
		}
		s.Receipt, s.State = &receipt, Succeeded
		r.Resources[s.Key] = receipt
		if err = e.save(r, op); err != nil {
			return *op, err
		}
	}
	op.State = Succeeded
	r.Revision++
	return *op, e.save(r, op)
}
