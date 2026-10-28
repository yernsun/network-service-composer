package core

import (
	"context"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func (c *Core) validateHTTP3(ctx context.Context, poolID string, enabled, https bool) error {
	if !enabled {
		return nil
	}
	if !https {
		return invalid("validation.http3_requires_https", "http3_enabled", "HTTP/3 requires HTTPS")
	}
	found := false
	for after := ""; ; {
		rows, err := c.store.ListGateways(ctx, storage.Page{After: after, Limit: 200})
		if err != nil {
			return err
		}
		for _, g := range rows {
			if g.PoolID != poolID || !g.Enabled {
				continue
			}
			found = true
			if !g.HTTP3Supported || g.RendererVersion != nginxx.RendererVersion || g.HTTP3AdvertisedPort < 1 || g.HTTP3AdvertisedPort > 65535 {
				return invalid("validation.http3_gateway_required", "http3_enabled", "all enabled pool gateways must report HTTP/3 capability")
			}
		}
		if len(rows) < 200 {
			break
		}
		after = rows[len(rows)-1].ID
	}
	if !found {
		return invalid("validation.http3_gateway_required", "http3_enabled", "a capable Gateway is required")
	}
	return nil
}
