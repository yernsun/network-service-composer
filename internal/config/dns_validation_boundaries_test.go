package config

import (
	"strings"
	"testing"
)

func TestDNSProviderCredentialAlternativesAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name, provider string
		credentials    DNSProviderCredentials
		valid          bool
	}{
		{"Ali role", "alidns", DNSProviderCredentials{RAMRole: "role"}, true},
		{"Ali keypair", "alidns", DNSProviderCredentials{AccessKey: "id", SecretKey: "key"}, true},
		{"Ali missing pair", "alidns", DNSProviderCredentials{}, false},
		{"Ali missing secret", "alidns", DNSProviderCredentials{AccessKey: "id"}, false},
		{"Cloudflare token", "cloudflare", DNSProviderCredentials{APIToken: "token"}, true},
		{"Cloudflare keypair", "cloudflare", DNSProviderCredentials{Email: "admin@example.test", APIKey: "key"}, true},
		{"Cloudflare missing email", "cloudflare", DNSProviderCredentials{APIKey: "key"}, false},
		{"Cloudflare missing key", "cloudflare", DNSProviderCredentials{Email: "admin@example.test"}, false},
		{"Tencent pair", "tencentcloud", DNSProviderCredentials{SecretID: "id", SecretKey: "key"}, true},
		{"Tencent missing id", "tencentcloud", DNSProviderCredentials{SecretKey: "key"}, false},
		{"Tencent missing key", "tencentcloud", DNSProviderCredentials{SecretID: "id"}, false},
		{"Hurricane tokens", "hurricane", DNSProviderCredentials{Tokens: map[string]string{"example.test": "token"}}, true},
		{"Hurricane missing tokens", "hurricane", DNSProviderCredentials{}, false},
		{"Hurricane empty domain", "hurricane", DNSProviderCredentials{Tokens: map[string]string{" ": "token"}}, false},
		{"Hurricane empty value", "hurricane", DNSProviderCredentials{Tokens: map[string]string{"example.test": ""}}, false},
		{"unknown provider", "unknown", DNSProviderCredentials{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := DNSProviderProfile{Provider: tc.provider, CredentialsFile: "/isolated/credentials", Credentials: tc.credentials}
			c := validConfig()
			c.ACME.DNSProviders = map[string]DNSProviderProfile{"production": profile}
			err := c.Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("provider validity=%t, expected %t: %v", err == nil, tc.valid, err)
			}
			if err != nil && !strings.Contains(err.Error(), "DNS provider") {
				t.Fatalf("unrelated failure: %v", err)
			}
		})
	}
}

func TestDNSProviderProfileBoundaries(t *testing.T) {
	valid := DNSProviderProfile{Provider: "cloudflare", CredentialsFile: "/isolated/credentials", Credentials: DNSProviderCredentials{APIToken: "token"}}
	for _, ref := range []string{"Az09-_..", strings.Repeat("a", 128)} {
		if err := validateDNSProviderProfile(ref, valid); err != nil {
			t.Fatalf("valid reference rejected: %v", err)
		}
	}
	for _, ref := range []string{"", " padded", "trailing ", strings.Repeat("a", 129), "invalid/name", "bad!", "é"} {
		if err := validateDNSProviderProfile(ref, valid); err == nil || !strings.Contains(err.Error(), "reference") {
			t.Fatalf("invalid reference accepted: %v", err)
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*DNSProviderProfile)
		want   string
	}{
		{"missing file", func(p *DNSProviderProfile) { p.CredentialsFile = " " }, "credentials_file"},
		{"negative ttl", func(p *DNSProviderProfile) { p.TTL = -1 }, "ttl"},
		{"short ttl", func(p *DNSProviderProfile) { p.TTL = 119 }, "ttl"},
		{"propagation", func(p *DNSProviderProfile) { p.PropagationTimeout = -1 }, "propagation_timeout"},
		{"polling", func(p *DNSProviderProfile) { p.PollingInterval = -1 }, "polling_interval"},
		{"http timeout", func(p *DNSProviderProfile) { p.HTTPTimeout = -1 }, "http_timeout"},
		{"sequence", func(p *DNSProviderProfile) { p.SequenceInterval = -1 }, "sequence_interval"},
		{"invalid URL", func(p *DNSProviderProfile) { p.BaseURL = "file:///tmp" }, "base_url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := valid
			tc.change(&p)
			if err := validateDNSProviderProfile("profile", p); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid profile accepted: %v", err)
			}
		})
	}
	for _, ttl := range []int{0, 120, 3600} {
		p := valid
		p.TTL = ttl
		p.BaseURL = "https://dns.example.test/api"
		if err := validateDNSProviderProfile("profile", p); err != nil {
			t.Fatalf("valid TTL %d rejected: %v", ttl, err)
		}
	}
}

func TestExternalCredentialAndSupportedControllerBoundaries(t *testing.T) {
	for _, tc := range []struct {
		ref        string
		credential DockerCredential
	}{
		{" ", DockerCredential{CAFile: "ca", CertFile: "cert", KeyFile: "key"}},
		{"docker", DockerCredential{CertFile: "cert", KeyFile: "key"}},
		{"docker", DockerCredential{CAFile: "ca", KeyFile: "key"}},
		{"docker", DockerCredential{CAFile: "ca", CertFile: "cert"}},
	} {
		c := validConfig()
		c.Credentials[tc.ref] = tc.credential
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "Docker credential") {
			t.Fatalf("incomplete Docker credential accepted: %v", err)
		}
	}
	for _, tc := range []struct{ ref, path string }{{" ", "/config"}, {"registry", " "}, {"registry", "relative"}} {
		c := validConfig()
		c.Registries[tc.ref] = RegistryCredential{ConfigFile: tc.path}
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "registry credential") {
			t.Fatalf("invalid registry credential accepted: %v", err)
		}
	}
	for _, driver := range []string{"postgres", "mysql", "sqlite"} {
		for _, scheme := range []string{"", "http", "https"} {
			c := validConfig()
			c.Storage.Driver = driver
			c.Storage.HA = driver != "sqlite"
			c.Controller.Endpoint.Readiness.Scheme = scheme
			c.Controller.Discovery.Events.Enabled = false
			c.Controller.Discovery.OfflinePolicy = "drain-after-timeout"
			c.Management.ExternalSecrets = []string{"one", "two"}
			c.Management.ExternalConfigs = []string{"config"}
			c.Credentials["docker"] = DockerCredential{CAFile: "ca", CertFile: "cert", KeyFile: "key"}
			c.Registries["registry"] = RegistryCredential{ConfigFile: "/config"}
			c.Server.HTTPTLSCertFile = "cert"
			c.Server.HTTPTLSKeyFile = "key"
			c.Server.GRPCListen = ":9443"
			c.Server.GRPCTLSCertFile = "cert"
			c.Server.GRPCTLSKeyFile = "key"
			c.Server.GRPCClientCAFile = "ca"
			c.ACME.AcceptTerms = true
			c.ACME.Email = "admin@example.test"
			if err := c.Validate(); err != nil {
				t.Fatalf("supported controller %s/%s rejected: %v", driver, scheme, err)
			}
		}
	}
}
