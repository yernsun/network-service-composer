package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

type resourceBoundaryStore struct{ *nginxBoundaryStore }

func (s *resourceBoundaryStore) GetService(ctx context.Context, key string) (model.Service, error) {
	if err := s.fail("service", key); err != nil {
		return model.Service{}, err
	}
	return s.Store.GetService(ctx, key)
}
func (s *resourceBoundaryStore) GetRuntime(ctx context.Context, key string) (model.Runtime, error) {
	if err := s.fail("runtime", key); err != nil {
		return model.Runtime{}, err
	}
	return s.Store.GetRuntime(ctx, key)
}
func (s *resourceBoundaryStore) GetCertificate(ctx context.Context, key string) (model.Certificate, error) {
	if err := s.fail("certificate", key); err != nil {
		return model.Certificate{}, err
	}
	return s.Store.GetCertificate(ctx, key)
}
func (s *resourceBoundaryStore) UpdateService(ctx context.Context, v *model.Service, expected int64) error {
	if err := s.fail("update service", v.ID); err != nil {
		return err
	}
	return s.Store.UpdateService(ctx, v, expected)
}
func (s *resourceBoundaryStore) UpdateRoute(ctx context.Context, v *model.Route, expected int64) error {
	if err := s.fail("update route", v.ID); err != nil {
		return err
	}
	return s.Store.UpdateRoute(ctx, v, expected)
}
func (s *resourceBoundaryStore) CreateRoute(ctx context.Context, v *model.Route) error {
	if err := s.fail("create route", v.ID); err != nil {
		return err
	}
	return s.Store.CreateRoute(ctx, v)
}
func (s *resourceBoundaryStore) UpdateDomain(ctx context.Context, v *model.Domain, expected int64) error {
	if err := s.fail("update domain", v.ID); err != nil {
		return err
	}
	return s.Store.UpdateDomain(ctx, v, expected)
}
func (s *resourceBoundaryStore) CreateDomain(ctx context.Context, v *model.Domain) error {
	if err := s.fail("create domain", v.ID); err != nil {
		return err
	}
	return s.Store.CreateDomain(ctx, v)
}
func (s *resourceBoundaryStore) UpdateCertificate(ctx context.Context, v *model.Certificate, expected int64) error {
	if err := s.fail("update certificate", v.ID); err != nil {
		return err
	}
	return s.Store.UpdateCertificate(ctx, v, expected)
}
func (s *resourceBoundaryStore) ProvisionManagedCertificate(ctx context.Context, v *model.Certificate, domain string, now time.Time) (int64, error) {
	if err := s.fail("provision", domain); err != nil {
		return 0, err
	}
	return s.Store.ProvisionManagedCertificate(ctx, v, domain, now)
}
func (s *resourceBoundaryStore) CreateManagedDomain(ctx context.Context, d *model.Domain, v *model.Certificate) error {
	if err := s.fail("create managed", d.ID); err != nil {
		return err
	}
	return s.Store.CreateManagedDomain(ctx, d, v)
}
func (s *resourceBoundaryStore) InstallCustomCertificate(ctx context.Context, v *model.Certificate, domains []string, now time.Time) error {
	if err := s.fail("install certificate", v.ID); err != nil {
		return err
	}
	return s.Store.InstallCustomCertificate(ctx, v, domains, now)
}

func TestDirectResourceWriteStorageErrorsRemainVisible(t *testing.T) {
	failure := errors.New("resource store unavailable")
	ctx := context.Background()
	for _, test := range []struct {
		name, method string
		nth          int
		run          func(*Core, model.DomainBinding) error
	}{
		{"service lookup", "service", 1, func(c *Core, b model.DomainBinding) error {
			_, e := c.UpdateService(ctx, b.Route.ServiceID, ServicePatch{}, 1)
			return e
		}},
		{"service update", "update service", 0, func(c *Core, b model.DomainBinding) error {
			s, e := c.store.GetService(ctx, b.Route.ServiceID)
			if e != nil {
				return e
			}
			_, e = c.UpdateService(ctx, s.ID, ServicePatch{}, s.Revision)
			return e
		}},
		{"service refresh", "service", 2, func(c *Core, b model.DomainBinding) error {
			_, e := c.UpdateService(ctx, b.Route.ServiceID, ServicePatch{}, 1)
			return e
		}},
		{"route lookup", "route", 1, func(c *Core, b model.DomainBinding) error {
			_, e := c.UpdateRoute(ctx, b.Route, b.Route.Revision)
			return e
		}},
		{"route service", "service", 0, func(c *Core, b model.DomainBinding) error {
			_, e := c.UpdateRoute(ctx, b.Route, b.Route.Revision)
			return e
		}},
		{"route domain", "domain", 0, func(c *Core, b model.DomainBinding) error {
			_, e := c.UpdateRoute(ctx, b.Route, b.Route.Revision)
			return e
		}},
		{"route runtime", "runtime", 0, func(c *Core, b model.DomainBinding) error {
			_, e := c.UpdateRoute(ctx, b.Route, b.Route.Revision)
			return e
		}},
		{"route update", "update route", 0, func(c *Core, b model.DomainBinding) error {
			_, e := c.UpdateRoute(ctx, b.Route, b.Route.Revision)
			return e
		}},
		{"route refresh", "route", 2, func(c *Core, b model.DomainBinding) error {
			_, e := c.UpdateRoute(ctx, b.Route, b.Route.Revision)
			return e
		}},
		{"domain lookup", "domain", 1, func(c *Core, b model.DomainBinding) error {
			_, e := c.UpdateDomain(ctx, b.Domain, b.Domain.Revision)
			return e
		}},
		{"domain update", "update domain", 0, func(c *Core, b model.DomainBinding) error {
			_, e := c.UpdateDomain(ctx, b.Domain, b.Domain.Revision)
			return e
		}},
		{"domain refresh", "domain", 2, func(c *Core, b model.DomainBinding) error {
			_, e := c.UpdateDomain(ctx, b.Domain, b.Domain.Revision)
			return e
		}},
		{"managed provision", "provision", 0, func(c *Core, b model.DomainBinding) error {
			b.Domain.TLSMode = model.TLSManaged
			b.Domain.HTTPSEnabled = true
			_, e := c.UpdateDomain(ctx, b.Domain, b.Domain.Revision)
			return e
		}},
		{"managed refresh", "domain", 3, func(c *Core, b model.DomainBinding) error {
			b.Domain.TLSMode = model.TLSManaged
			b.Domain.HTTPSEnabled = true
			_, e := c.UpdateDomain(ctx, b.Domain, b.Domain.Revision)
			return e
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, s, input := externalBindingFixture(t)
			binding, _, err := c.CreateDomainBinding(ctx, s.ID, input)
			if err != nil {
				t.Fatal(err)
			}
			c.store = &resourceBoundaryStore{&nginxBoundaryStore{Store: c.store, method: test.method, nth: test.nth, failure: failure}}
			if err = test.run(c, binding); !errors.Is(err, failure) {
				t.Fatalf("resource write hid storage failure: %v", err)
			}
		})
	}
	for _, method := range []string{"service", "domain", "runtime", "create route"} {
		t.Run("create route/"+method, func(t *testing.T) {
			c, s, input := externalBindingFixture(t)
			binding, _, err := c.CreateDomainBinding(ctx, s.ID, input)
			if err != nil {
				t.Fatal(err)
			}
			c.store = &resourceBoundaryStore{&nginxBoundaryStore{Store: c.store, method: method, failure: failure}}
			_, err = c.CreateRoute(ctx, CreateRouteInput{DomainID: binding.Domain.ID, RouteConfigInput: RouteConfigInput{Path: "/new", Backend: RouteBackendInput{ServiceID: s.ID}}})
			if !errors.Is(err, failure) {
				t.Fatalf("route create hid failure: %v", err)
			}
		})
	}
	for _, method := range []string{"pool", "create domain", "create managed"} {
		t.Run("create domain/"+method, func(t *testing.T) {
			c, _, pool := controlFixture(t)
			input := CreateDomainInput{PoolID: pool.ID, DomainConfigInput: DomainConfigInput{Host: "app.test", EntryPoints: []string{EntryPointHTTP}, TLS: DomainTLSInput{Mode: model.TLSDisabled}}}
			if method == "create managed" {
				input.TLS.Mode = model.TLSManaged
				input.EntryPoints = append(input.EntryPoints, EntryPointHTTPS)
			}
			c.store = &resourceBoundaryStore{&nginxBoundaryStore{Store: c.store, method: method, failure: failure}}
			if _, err := c.CreateDomain(ctx, input); !errors.Is(err, failure) {
				t.Fatalf("domain create hid failure: %v", err)
			}
		})
	}
}
