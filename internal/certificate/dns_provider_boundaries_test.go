package certificate

import (
	"github.com/yernsun/network-service-composer/internal/config"
	"strings"
	"testing"
	"time"
)

func TestDNSProviderTimingOverridesAndMissingCredentials(t *testing.T) {
	for _, name := range []string{"alidns", "cloudflare", "tencentcloud", "hurricane"} {
		t.Run(name, func(t *testing.T) {
			profile := config.DNSProviderProfile{Provider: name, Region: "test-region", TTL: 300, PropagationTimeout: config.Duration(73 * time.Second), PollingInterval: config.Duration(3 * time.Second), HTTPTimeout: config.Duration(9 * time.Second), SequenceInterval: config.Duration(4 * time.Second)}
			switch name {
			case "alidns":
				profile.Credentials = config.DNSProviderCredentials{AccessKey: "fixture-access", SecretKey: "fixture-secret", SecurityToken: "fixture-token"}
			case "cloudflare":
				profile.Credentials = config.DNSProviderCredentials{APIToken: "fixture-token", ZoneAPIToken: "fixture-zone"}
			case "tencentcloud":
				profile.Credentials = config.DNSProviderCredentials{SecretID: "fixture-id", SecretKey: "fixture-secret", SessionToken: "fixture-token"}
			case "hurricane":
				profile.Credentials = config.DNSProviderCredentials{Tokens: map[string]string{"example.test": "fixture-token"}}
			}
			provider, e := newDNSProvider(profile)
			if e != nil {
				t.Fatal(e)
			}
			timing, ok := provider.(interface {
				Timeout() (time.Duration, time.Duration)
			})
			if !ok {
				t.Fatal("provider cannot report propagation policy")
			}
			timeout, interval := timing.Timeout()
			if timeout != 73*time.Second || interval != 3*time.Second {
				t.Fatalf("timing overrides lost: %v %v", timeout, interval)
			}
			if name == "hurricane" {
				sequential, ok := provider.(interface{ Sequential() time.Duration })
				if !ok || sequential.Sequential() != 4*time.Second {
					t.Fatal("DNS update sequencing policy lost")
				}
			}
			profile.Credentials = config.DNSProviderCredentials{}
			if provider, e := newDNSProvider(profile); e == nil || provider != nil || !strings.Contains(e.Error(), name) {
				t.Fatal("missing provider credentials accepted", e)
			}
		})
	}
	if provider, e := newDNSProvider(config.DNSProviderProfile{Provider: "unsupported"}); e == nil || provider != nil {
		t.Fatal("unsupported DNS provider accepted")
	}
}
