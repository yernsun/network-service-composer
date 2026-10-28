package core

import (
	"context"
	"errors"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"testing"
	"time"
)

type bindingCommitFailureStore struct {
	storage.Store
	mode     string
	calls    int
	accepted *model.DeploymentOperation
	failure  error
}

func (s *bindingCommitFailureStore) GetDeploymentOperationByIdempotency(ctx context.Context, runtime, key string) (model.DeploymentOperation, error) {
	s.calls++
	if s.calls == 1 {
		return model.DeploymentOperation{}, storage.ErrNotFound
	}
	if s.mode == "lookup failed" {
		return model.DeploymentOperation{}, s.failure
	}
	if s.accepted != nil {
		op := *s.accepted
		if s.mode == "different request" {
			op.RequestSHA256 = "different"
		}
		return op, nil
	}
	return model.DeploymentOperation{}, storage.ErrNotFound
}
func (s *bindingCommitFailureStore) CreateServiceDomainBinding(ctx context.Context, d *model.Domain, c *model.Certificate, r *model.Route, o *model.DeploymentOperation, p *model.Service, expected int64) error {
	if s.mode == "accepted" || s.mode == "different request" {
		if e := s.Store.CreateServiceDomainBinding(ctx, d, c, r, o, p, expected); e != nil {
			return e
		}
		s.accepted = o
	}
	if s.mode == "write failure" {
		return s.failure
	}
	return storage.ErrConflict
}
func (s *bindingCommitFailureStore) DisableServiceDomainBinding(ctx context.Context, service, route string, expected int64, now time.Time, o *model.DeploymentOperation) error {
	if s.mode == "accepted" || s.mode == "different request" {
		if e := s.Store.DisableServiceDomainBinding(ctx, service, route, expected, now, o); e != nil {
			return e
		}
		s.accepted = o
	}
	if s.mode == "write failure" {
		return s.failure
	}
	return storage.ErrConflict
}
func TestDomainBindingConcurrentCommitDoesNotDuplicateOrMisattributeOperation(t *testing.T) {
	for _, action := range []string{"attach", "detach"} {
		for _, mode := range []string{"accepted", "different request", "lookup failed", "no matching operation", "write failure", "without key", "external"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				c, service, in := managedBindingFixture(t)
				if mode == "external" {
					c, service, in = externalBindingFixture(t)
				}
				ctx := context.Background()
				base := c.store
				var previous model.DomainBinding
				var e error
				if action == "detach" {
					previous, _, e = c.CreateDomainBinding(ctx, service.ID, in)
					if e != nil {
						t.Fatal(e)
					}
				}
				f := &bindingCommitFailureStore{Store: base, mode: mode, failure: errors.New("binding write unavailable")}
				c.store = f
				key := "new request"
				if mode == "without key" {
					key = ""
				}
				in.IdempotencyKey = key
				var op *model.DeploymentOperation
				var binding model.DomainBinding
				if action == "attach" {
					binding, op, e = c.CreateDomainBinding(ctx, service.ID, in)
				} else {
					op, e = c.DeleteDomainBinding(ctx, service.ID, previous.ID, previous.Route.Revision, key)
				}
				if mode == "accepted" {
					if e != nil || op == nil || f.accepted == nil || op.ID != f.accepted.ID || f.calls != 2 {
						t.Fatal("accepted operation not recovered", op, e)
					}
					if action == "attach" && binding.ID == "" {
						t.Fatal("recovered attachment lacks route")
					}
				} else {
					if e == nil || op != nil {
						t.Fatal("unknown or foreign commit accepted", op, e)
					}
					if mode == "write failure" && !errors.Is(e, f.failure) {
						t.Fatal("write cause lost", e)
					}
				}
				if mode == "external" || mode == "without key" || mode == "write failure" {
					if f.calls != 1 {
						t.Fatal("failure triggered unauthorized retry", f.calls)
					}
				}
				if action == "detach" && mode != "accepted" && mode != "different request" {
					r, err := base.GetRoute(ctx, previous.ID)
					if err != nil || !r.Enabled {
						t.Fatal("failed detach removed entrance", err)
					}
				}
				current, err := base.GetService(ctx, service.ID)
				if err != nil || current.Managed != service.Managed || current.ApplicationID != service.ApplicationID {
					t.Fatal("binding conflict changed application ownership", err)
				}
			})
		}
	}
}
