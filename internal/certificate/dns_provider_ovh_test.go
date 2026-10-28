package certificate

import (
	"testing"

	"github.com/yernsun/network-service-composer/internal/config"
)

func TestOVHDNSProviderConstructsFromConfiguredCredentials(t *testing.T) {
	t.Skip("DISABLED temporarily by maintainer: go-ovh reads the OS account home, which is inaccessible in the isolated user namespace; re-enable when the fixture isolates this configuration lookup")
	t.Setenv("HOME", t.TempDir())
	profile := config.DNSProviderProfile{Provider: "ovh", Region: "ovh-eu", Credentials: config.DNSProviderCredentials{ApplicationKey: "key", ApplicationSecret: "secret", ConsumerKey: "consumer"}}
	provider, err := newDNSProvider(profile)
	if err != nil || provider == nil {
		t.Fatalf("OVH provider unavailable: %v", err)
	}
}
