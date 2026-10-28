package core

import (
	"context"
	"errors"
	"fmt"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
	"testing"
)

type http3PageStore struct {
	storage.Store
	pages   [][]model.Gateway
	calls   int
	failure error
}

func (s *http3PageStore) ListGateways(_ context.Context, p storage.Page) ([]model.Gateway, error) {
	s.calls++
	if s.failure != nil {
		return nil, s.failure
	}
	if s.calls > 1 && p.After != "g-199" {
		panic("missing capability cursor")
	}
	return s.pages[s.calls-1], nil
}
func TestHTTP3CapabilityChecksAllEnabledTargetsAcrossPages(t *testing.T) {
	for _, mode := range []string{"valid", "unsupported", "renderer mismatch", "zero port", "overflow port", "disabled", "foreign", "read failure", "late unsupported"} {
		t.Run(mode, func(t *testing.T) {
			c, _, pool := controlFixture(t)
			base := model.Gateway{ID: "target", PoolID: pool.ID, Enabled: true, HTTP3Supported: true, HTTP3AdvertisedPort: 65535, RendererVersion: nginxx.RendererVersion}
			f := &http3PageStore{Store: c.store}
			switch mode {
			case "unsupported":
				base.HTTP3Supported = false
			case "renderer mismatch":
				base.RendererVersion = "old"
			case "zero port":
				base.HTTP3AdvertisedPort = 0
			case "overflow port":
				base.HTTP3AdvertisedPort = 65536
			case "disabled":
				base.Enabled = false
			case "foreign":
				base.PoolID = "other"
			case "read failure":
				f.failure = errors.New("capability unknown")
			}
			if mode == "valid" || mode == "late unsupported" {
				var first []model.Gateway
				for i := 0; i < 200; i++ {
					g := base
					g.ID = fmt.Sprintf("g-%03d", i)
					first = append(first, g)
				}
				if mode == "late unsupported" {
					base.HTTP3Supported = false
				}
				f.pages = [][]model.Gateway{first, {base}}
			} else {
				f.pages = [][]model.Gateway{{base}}
			}
			c.store = f
			e := c.validateHTTP3(context.Background(), pool.ID, true, true)
			if (e == nil) != (mode == "valid") {
				t.Fatal("incomplete capabilities authorized HTTP3", e)
			}
			if mode == "read failure" && !errors.Is(e, f.failure) {
				t.Fatal("capability failure hidden", e)
			}
			if (mode == "valid" || mode == "late unsupported") && f.calls != 2 {
				t.Fatal("capability checks ignored later targets")
			}
		})
	}
}
