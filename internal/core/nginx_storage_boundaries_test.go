package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// Keep real control documents and state transitions; replace only the failing
// database call so a failed read cannot masquerade as missing configuration.
type nginxBoundaryStore struct {
	storage.Store
	method, key string
	failure     error
	nth, calls  int
}

func (s *nginxBoundaryStore) fail(method, key string) error {
	if s.method != method || s.key != "" && !strings.Contains(key, s.key) {
		return nil
	}
	s.calls++
	if s.nth == 0 || s.nth == s.calls {
		return s.failure
	}
	return nil
}
func (s *nginxBoundaryStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if err := s.fail("document", key); err != nil {
		return storage.ControlDocument{}, err
	}
	return s.Store.GetControlDocument(ctx, key)
}
func (s *nginxBoundaryStore) GetPool(ctx context.Context, key string) (model.GatewayPool, error) {
	if err := s.fail("pool", key); err != nil {
		return model.GatewayPool{}, err
	}
	return s.Store.GetPool(ctx, key)
}
func (s *nginxBoundaryStore) GetDomain(ctx context.Context, key string) (model.Domain, error) {
	if err := s.fail("domain", key); err != nil {
		return model.Domain{}, err
	}
	return s.Store.GetDomain(ctx, key)
}
func (s *nginxBoundaryStore) GetRoute(ctx context.Context, key string) (model.Route, error) {
	if err := s.fail("route", key); err != nil {
		return model.Route{}, err
	}
	return s.Store.GetRoute(ctx, key)
}
func (s *nginxBoundaryStore) GetGateway(ctx context.Context, key string) (model.Gateway, error) {
	if err := s.fail("gateway", key); err != nil {
		return model.Gateway{}, err
	}
	return s.Store.GetGateway(ctx, key)
}
func (s *nginxBoundaryStore) ListGateways(ctx context.Context, page storage.Page) ([]model.Gateway, error) {
	if err := s.fail("gateways", ""); err != nil {
		return nil, err
	}
	return s.Store.ListGateways(ctx, page)
}
func (s *nginxBoundaryStore) ListControlDocuments(ctx context.Context, kind, parent string, page storage.Page) ([]storage.ControlDocument, error) {
	if err := s.fail("documents", kind); err != nil {
		return nil, err
	}
	return s.Store.ListControlDocuments(ctx, kind, parent, page)
}
func (s *nginxBoundaryStore) WriteControlDocuments(ctx context.Context, writes []storage.ControlWrite) error {
	if err := s.fail("write", ""); err != nil {
		return err
	}
	return s.Store.WriteControlDocuments(ctx, writes)
}
func (s *nginxBoundaryStore) PublishNginxDocuments(ctx context.Context, pool string, revision int64, writes []storage.ControlWrite) error {
	if err := s.fail("publish", pool); err != nil {
		return err
	}
	return s.Store.PublishNginxDocuments(ctx, pool, revision, writes)
}
func (s *nginxBoundaryStore) BuildSnapshot(ctx context.Context, pool string) (model.Snapshot, error) {
	if err := s.fail("snapshot", pool); err != nil {
		return model.Snapshot{}, err
	}
	return s.Store.BuildSnapshot(ctx, pool)
}

func TestNginxControlStorageFailuresAreNotEmptySuccess(t *testing.T) {
	failure := errors.New("NGINX control storage unavailable")
	ctx := context.Background()
	for _, test := range []struct {
		name, method, key string
		run               func(*Core, string, string) error
	}{
		{"configuration pool", "pool", "", func(c *Core, p, k string) error { _, e := c.NginxConfiguration(ctx, p, "nginx-draft"); return e }},
		{"configuration document", "document", "nginx-draft", func(c *Core, p, k string) error { _, e := c.NginxConfiguration(ctx, p, "nginx-draft"); return e }},
		{"version pool", "pool", "", func(c *Core, p, k string) error { _, e := c.ListNginxVersions(ctx, p, storage.Page{}); return e }},
		{"version list", "documents", "nginx-version", func(c *Core, p, k string) error { _, e := c.ListNginxVersions(ctx, p, storage.Page{}); return e }},
		{"restore read", "document", "", func(c *Core, p, k string) error { _, e := c.RestoreNginxVersion(ctx, p, k, "actor", 0); return e }},
		{"sandbox pool", "pool", "", func(c *Core, p, k string) error { _, e := c.GetSandbox(ctx, p); return e }},
		{"sandbox read", "document", "sandbox", func(c *Core, p, k string) error { _, e := c.GetSandbox(ctx, p); return e }},
		{"sandbox save pool", "pool", "", func(c *Core, p, k string) error { _, e := c.SaveSandbox(ctx, p, SandboxSettings{}, 0); return e }},
		{"sandbox identity", "gateway", "", func(c *Core, p, k string) error {
			_, e := c.SaveSandbox(ctx, p, SandboxSettings{GatewayID: id.New(id.Gateway)}, 0)
			return e
		}},
		{"sandbox role", "document", ":role", func(c *Core, p, k string) error {
			_, e := c.SaveSandbox(ctx, p, SandboxSettings{GatewayID: id.New(id.Gateway)}, 0)
			return e
		}},
		{"sandbox write", "write", "", func(c *Core, p, k string) error { _, e := c.SaveSandbox(ctx, p, SandboxSettings{}, 0); return e }},
		{"gateway mode", "document", "", func(c *Core, p, k string) error { _, e := c.GatewayMode(ctx, k, p); return e }},
		{"sandbox heartbeat", "document", "", func(c *Core, p, k string) error { return c.SandboxHeartbeat(ctx, k, p) }},
		{"sandbox heartbeat write", "write", "", func(c *Core, p, k string) error { return c.SandboxHeartbeat(ctx, k, p) }},
		{"invalidate read", "document", "", func(c *Core, p, k string) error { return c.InvalidateNginxValidation(ctx, p) }},
		{"invalidate write", "write", "", func(c *Core, p, k string) error { return c.InvalidateNginxValidation(ctx, p) }},
		{"validation task", "document", "ngval", func(c *Core, p, k string) error { _, e := c.GetNginxValidation(ctx, p, k); return e }},
		{"validation head", "document", "validation-head", func(c *Core, p, k string) error { _, e := c.GetNginxValidation(ctx, p, k); return e }},
		{"validation pool", "pool", "", func(c *Core, p, k string) error { _, e := c.GetNginxValidation(ctx, p, k); return e }},
		{"cancel read", "document", "ngval", func(c *Core, p, k string) error { return c.CancelNginxValidation(ctx, p, k, 1) }},
		{"cancel write", "write", "", func(c *Core, p, k string) error { return c.CancelNginxValidation(ctx, p, k, 1) }},
		{"next list", "documents", "", func(c *Core, p, k string) error { _, _, e := c.NextNginxValidation(ctx, p, "gateway"); return e }},
		{"next task", "document", "ngval", func(c *Core, p, k string) error { _, _, e := c.NextNginxValidation(ctx, p, "gateway"); return e }},
		{"finish read", "document", "ngval", func(c *Core, p, k string) error {
			return c.FinishNginxValidation(ctx, p, k, "gateway", NginxValidationResult{Status: "SUCCEEDED"})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, pool, _, v := nginxValidationFixture(t, false)
			c.store = &nginxBoundaryStore{Store: c.store, method: test.method, key: test.key, failure: failure}
			if err := test.run(c, pool.ID, v.ID); !errors.Is(err, failure) {
				t.Fatalf("database outage hidden: %v", err)
			}
		})
	}
}

func TestNginxValidationCreationAndClaimStorageFailures(t *testing.T) {
	failure := errors.New("validation persistence interrupted")
	ctx := context.Background()
	for _, test := range []struct {
		method, key string
		nth         int
	}{
		{"pool", "", 1}, {"pool", "", 2}, {"pool", "", 3}, {"pool", "", 4},
		{"document", "nginx-draft", 0}, {"document", ":sandbox", 0}, {"document", "validation-head", 0},
		{"gateways", "", 0}, {"write", "", 0},
	} {
		t.Run("create/"+test.method+test.key+string(rune('0'+test.nth)), func(t *testing.T) {
			c, pool, _, v := nginxValidationFixture(t, false)
			original := c.store
			c.store = &nginxBoundaryStore{Store: original, method: test.method, key: test.key, nth: test.nth, failure: failure}
			if _, err := c.CreateNginxValidation(ctx, pool.ID, v.DraftRevision); !errors.Is(err, failure) {
				t.Fatalf("creation hid database error: %v", err)
			}
			head, _, err := readControl[map[string]string](ctx, original, pool.ID+":nginx-validation-head", "nginx-validation-head")
			if err != nil || head["task_id"] != v.ID {
				t.Fatalf("failed creation replaced current validation: %+v %v", head, err)
			}
		})
	}
	for _, method := range []string{"snapshot", "write"} {
		t.Run("claim/"+method, func(t *testing.T) {
			c, pool, g, _ := nginxValidationFixture(t, false)
			c.store = &nginxBoundaryStore{Store: c.store, method: method, failure: failure}
			if task, snapshot, err := c.NextNginxValidation(ctx, pool.ID, g.ID); !errors.Is(err, failure) || task != nil || snapshot != nil {
				t.Fatalf("claim without durable state: %+v %+v %v", task, snapshot, err)
			}
		})
	}
	for _, fault := range []string{"write", "conflict", "retry succeeds"} {
		t.Run("finish/"+fault, func(t *testing.T) {
			c, pool, g, v := nginxValidationFixture(t, false)
			if _, _, err := c.NextNginxValidation(ctx, pool.ID, g.ID); err != nil {
				t.Fatal(err)
			}
			wrapper := &nginxBoundaryStore{Store: c.store, method: "write", failure: failure}
			want := failure
			if fault != "write" {
				wrapper.failure = storage.ErrPrecondition
				want = storage.ErrConflict
			}
			if fault == "retry succeeds" {
				wrapper.nth = 1
				want = nil
			}
			c.store = wrapper
			if err := c.FinishNginxValidation(ctx, pool.ID, v.ID, g.ID, NginxValidationResult{Status: "SUCCEEDED"}); !errors.Is(err, want) {
				t.Fatalf("finish: %v want %v", err, want)
			}
			got, err := c.GetNginxValidation(ctx, pool.ID, v.ID)
			if err != nil {
				t.Fatal(err)
			}
			if fault == "retry succeeds" {
				if got.Status != "SUCCEEDED" || wrapper.calls != 2 {
					t.Fatalf("retry lost result: %+v calls=%d", got, wrapper.calls)
				}
			} else if got.Status != "RUNNING" {
				t.Fatalf("failed finish became success: %+v", got)
			}
			if fault == "conflict" && wrapper.calls != 4 {
				t.Fatalf("unbounded retry: %d", wrapper.calls)
			}
		})
	}
}
