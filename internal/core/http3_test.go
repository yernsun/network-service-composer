package core

import (
	"context"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"testing"
	"time"
)

func TestHTTP3RequiresHTTPSAndAllGatewayCapabilities(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := context.Background()
	if err := c.validateHTTP3(ctx, pool.ID, false, false); err != nil {
		t.Fatal(err)
	}
	if err := c.validateHTTP3(ctx, pool.ID, true, false); err == nil {
		t.Fatal("HTTP3 without HTTPS")
	}
	if err := c.validateHTTP3(ctx, pool.ID, true, true); err == nil {
		t.Fatal("HTTP3 without gateway")
	}
	now := time.Now().UTC()
	g := model.Gateway{ID: "gw_h3", PoolID: pool.ID, Enabled: true, Name: "h3", HTTP3Supported: true, HTTP3AdvertisedPort: 443, RendererVersion: nginxx.RendererVersion, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := c.store.UpsertGateway(ctx, &g); err != nil {
		t.Fatal(err)
	}
	if err := c.validateHTTP3(ctx, pool.ID, true, true); err != nil {
		t.Fatal(err)
	}
	g.ID = "gw_unknown"
	g.HTTP3Supported = false
	if err := c.store.UpsertGateway(ctx, &g); err != nil {
		t.Fatal(err)
	}
	if err := c.validateHTTP3(ctx, pool.ID, true, true); err == nil {
		t.Fatal("unknown gateway accepted")
	}
}
