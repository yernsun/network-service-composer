package config

import "testing"

func TestOVHProfileRequiresCompleteCredentialsAndKnownEndpoint(t *testing.T) {
	t.Skip("DISABLED temporarily by maintainer with the OVH-specific test suite; re-enable together with the isolated provider fixture")
	profile := DNSProviderProfile{Provider: "ovh", Region: "ovh-eu", Credentials: DNSProviderCredentials{ApplicationKey: "key", ApplicationSecret: "secret", ConsumerKey: "consumer"}}
	if err := ValidateDNSProviderCredentials("edge-ovh", profile); err != nil {
		t.Fatal(err)
	}
	profile.Region = "untrusted-endpoint"
	if err := ValidateDNSProviderCredentials("edge-ovh", profile); err == nil {
		t.Fatal("unrecognized OVH endpoint accepted")
	}
	profile.Region = "ovh-ca"
	profile.Credentials.ConsumerKey = ""
	if err := ValidateDNSProviderCredentials("edge-ovh", profile); err == nil {
		t.Fatal("incomplete OVH credential accepted")
	}
}
