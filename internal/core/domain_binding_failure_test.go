package core

import (
	"context"
	"errors"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type bindingReadFailureStore struct {
	storage.Store
	resource       string
	failure        error
	getDomainCalls int
	getRouteCalls  int
}

func (s *bindingReadFailureStore) GetService(ctx context.Context, key string) (model.Service, error) {
	if s.resource == "service" {
		return model.Service{}, s.failure
	}
	return s.Store.GetService(ctx, key)
}
func (s *bindingReadFailureStore) GetRuntime(ctx context.Context, key string) (model.Runtime, error) {
	if s.resource == "runtime" {
		return model.Runtime{}, s.failure
	}
	return s.Store.GetRuntime(ctx, key)
}
func (s *bindingReadFailureStore) GetRoute(ctx context.Context, key string) (model.Route, error) {
	s.getRouteCalls++
	if s.resource == "route" || (s.resource == "route refresh" && s.getRouteCalls > 1) {
		return model.Route{}, s.failure
	}
	return s.Store.GetRoute(ctx, key)
}
func (s *bindingReadFailureStore) GetDomain(ctx context.Context, key string) (model.Domain, error) {
	s.getDomainCalls++
	if s.resource == "domain" || (s.resource == "domain refresh" && s.getDomainCalls > 1) {
		return model.Domain{}, s.failure
	}
	return s.Store.GetDomain(ctx, key)
}
func (s *bindingReadFailureStore) GetDeploymentOperationByIdempotency(ctx context.Context, runtime, key string) (model.DeploymentOperation, error) {
	if s.resource == "idempotency" {
		return model.DeploymentOperation{}, s.failure
	}
	return s.Store.GetDeploymentOperationByIdempotency(ctx, runtime, key)
}
func (s *bindingReadFailureStore) ListGatewayDNSTargets(ctx context.Context, key string) ([]model.DNSTarget, error) {
	if s.resource == "DNS hints" {
		return nil, s.failure
	}
	return s.Store.ListGatewayDNSTargets(ctx, key)
}
func (s *bindingReadFailureStore) ListServiceDomainBindings(ctx context.Context, key string) ([]model.DomainBinding, error) {
	if s.resource == "bindings" {
		return nil, s.failure
	}
	return s.Store.ListServiceDomainBindings(ctx, key)
}

func TestDomainBindingReadFailuresDoNotReportSuccess(t *testing.T) {
	failure := errors.New("binding storage unavailable")
	for _, resource := range []string{"service", "runtime", "idempotency"} {
		t.Run("create/"+resource, func(t *testing.T) {
			c, service, input := externalBindingFixture(t)
			c.store = &bindingReadFailureStore{Store: c.store, resource: resource, failure: failure}
			binding, op, err := c.CreateDomainBinding(context.Background(), service.ID, input)
			if !errors.Is(err, failure) || binding.ID != "" || op != nil {
				t.Fatalf("create masked read failure: %#v %#v %v", binding, op, err)
			}
		})
	}
	for _, resource := range []string{"service", "route", "domain", "domain refresh", "route refresh"} {
		t.Run("update/"+resource, func(t *testing.T) {
			c, service, input := externalBindingFixture(t)
			ctx := context.Background()
			binding, _, err := c.CreateDomainBinding(ctx, service.ID, input)
			if err != nil {
				t.Fatal(err)
			}
			c.store = &bindingReadFailureStore{Store: c.store, resource: resource, failure: failure}
			_, err = c.UpdateDomainBinding(ctx, service.ID, binding.ID, UpdateDomainBindingInput{}, binding.Route.Revision)
			if !errors.Is(err, failure) {
				t.Fatalf("update masked read failure: %v", err)
			}
		})
	}
	for _, resource := range []string{"service", "route", "idempotency", "bindings"} {
		t.Run("delete/"+resource, func(t *testing.T) {
			c, service, input := managedBindingFixture(t)
			ctx := context.Background()
			binding, _, err := c.CreateDomainBinding(ctx, service.ID, input)
			if err != nil {
				t.Fatal(err)
			}
			c.store = &bindingReadFailureStore{Store: c.store, resource: resource, failure: failure}
			op, err := c.DeleteDomainBinding(ctx, service.ID, binding.ID, binding.Route.Revision, "delete")
			if !errors.Is(err, failure) || op != nil {
				t.Fatalf("delete masked read failure: %#v %v", op, err)
			}
		})
	}
	t.Run("supplementary DNS failure does not undo committed binding", func(t *testing.T) {
		c, service, input := externalBindingFixture(t)
		original := c.store
		c.store = &bindingReadFailureStore{Store: original, resource: "DNS hints", failure: failure}
		binding, op, err := c.CreateDomainBinding(context.Background(), service.ID, input)
		if err != nil || op != nil || binding.ID == "" || len(binding.DNSTargets) != 0 {
			t.Fatalf("supplementary failure misreported: %#v %#v %v", binding, op, err)
		}
		if _, err := original.GetRoute(context.Background(), binding.ID); err != nil {
			t.Fatal("committed route missing")
		}
	})
}

func TestDomainOperationRecoveryRejectsMissingCorruptAndForeignInput(t *testing.T) {
	c, service, input := externalBindingFixture(t)
	ctx := context.Background()
	binding, _, err := c.CreateDomainBinding(ctx, service.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		plaintext []byte
		raw       []byte
	}{
		{"missing", nil, nil}, {"undecryptable", nil, []byte("bad ciphertext")}, {"malformed JSON", []byte("["), nil}, {"missing route", []byte(`{}`), nil}, {"unknown route", []byte(`{"route_id":"missing"}`), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op := model.DeploymentOperation{ID: "recovery", EncryptedInput: tc.raw}
			if tc.plaintext != nil {
				encrypted, err := c.keyring.Encrypt(tc.plaintext, operationAAD(op.ID))
				if err != nil {
					t.Fatal(err)
				}
				op.EncryptedInput = encrypted
			}
			if result, err := c.domainBindingFromOperation(ctx, service, op); err == nil || result.ID != "" {
				t.Fatalf("corrupt history accepted: %#v %v", result, err)
			}
		})
	}
	op, err := c.newOperation("valid", "", service.RuntimeID, service.ID, "", "DOMAIN_ATTACH", "", map[string]string{"route_id": binding.ID}, c.now())
	if err != nil {
		t.Fatal(err)
	}
	foreign := service
	foreign.ID = "different"
	if _, err = c.domainBindingFromOperation(ctx, foreign, op); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("foreign route recovered: %v", err)
	}
	for _, resource := range []string{"route", "domain"} {
		original := c.store
		c.store = &bindingReadFailureStore{Store: original, resource: resource, failure: storage.ErrNotFound}
		if _, err = c.domainBindingFromOperation(ctx, service, op); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("missing %s recovered: %v", resource, err)
		}
		c.store = original
	}
}

func TestManagementDisabledBlocksNewBindingAndDetach(t *testing.T) {
	c, service, input := managedBindingFixture(t)
	ctx := context.Background()
	binding, _, err := c.CreateDomainBinding(ctx, service.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	c.config.Management.Enabled = false
	input.IdempotencyKey = "new-attach"
	if _, op, err := c.CreateDomainBinding(ctx, service.ID, input); err == nil || op != nil {
		t.Fatal("management-disabled attach queued")
	}
	if op, err := c.DeleteDomainBinding(ctx, service.ID, binding.ID, binding.Route.Revision, "detach"); err == nil || op != nil {
		t.Fatal("management-disabled detach queued")
	}
	stored, err := c.store.GetRoute(ctx, binding.ID)
	if err != nil || stored.Revision != binding.Route.Revision || !stored.Enabled {
		t.Fatal("blocked management changed active binding")
	}
}
