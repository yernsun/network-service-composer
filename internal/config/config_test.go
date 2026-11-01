package config

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validConfig() Config {
	c := Defaults()
	c.Storage.Driver = "sqlite"
	c.Storage.DSN = "file:test.db"
	c.Security.AdminToken = "secret"
	c.Security.SessionKey = strings.Repeat("s", 32)
	c.Security.PrimaryKeyID = "v1"
	c.Security.EncryptionKeys = map[string]string{"v1": base64.StdEncoding.EncodeToString(make([]byte, 32))}
	c.Server.GRPCListen = ""
	return c
}

func TestValidateRejectsSQLiteHA(t *testing.T) {
	c := validConfig()
	c.Storage.HA = true
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "SQLite") {
		t.Fatalf("expected SQLite HA error, got %v", err)
	}
}

func TestValidateAcceptsSingleSQLite(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateI18NDefaultLanguage(t *testing.T) {
	for _, value := range []string{"en", "ja", "de", "zh-CN", "zh-TW", "fr", "ru", "es"} {
		c := validConfig()
		c.I18N.DefaultLanguage = value
		if err := c.Validate(); err != nil {
			t.Fatalf("supported language %q was rejected: %v", value, err)
		}
	}
	c := validConfig()
	c.I18N.DefaultLanguage = "ko"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "i18n.default_language") {
		t.Fatalf("expected unsupported language error, got %v", err)
	}
}

func TestValidateRejectsIncompleteDockerCredential(t *testing.T) {
	c := validConfig()
	c.Credentials["remote"] = DockerCredential{CAFile: "/run/secrets/ca.pem"}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "Docker credential") {
		t.Fatalf("expected incomplete Docker credential error, got %v", err)
	}
}

func TestValidateRejectsUnsafeLifecycleConfiguration(t *testing.T) {
	c := validConfig()
	c.Controller.Endpoint.Readiness.Timeout = c.Controller.Endpoint.Readiness.Interval + 1
	c.Controller.GC.EndpointRetention = 0
	c.Server.PublicURL = "file:///tmp/controller"
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "readiness") || !strings.Contains(err.Error(), "GC retention") || !strings.Contains(err.Error(), "public_url") {
		t.Fatalf("expected aggregated validation errors, got %v", err)
	}
}

func TestValidateAgentRequiresTypedIdentityAndSafeNGINXState(t *testing.T) {
	c := Defaults()
	c.Agent.GatewayID = "gateway"
	c.Agent.PoolID = "pool"
	c.Agent.ControllerAddress = "controller:9443"
	c.Agent.ServerName = "controller"
	c.Agent.CACertFile = "/run/nsc/ca.crt"
	c.Agent.TLSCertFile = "/run/nsc/agent.crt"
	c.Agent.TLSKeyFile = "/run/nsc/agent.key"
	c.NGINX.KeepGenerations = 2
	err := c.ValidateAgent()
	if err == nil || !strings.Contains(err.Error(), "gateway") || !strings.Contains(err.Error(), "pool") || !strings.Contains(err.Error(), "keep_generations") {
		t.Fatalf("expected agent identity and generation validation errors, got %v", err)
	}
}

func TestValidateAgentPublicAddress(t *testing.T) {
	for _, value := range []string{"203.0.113.10", "2001:db8::10", "edge.example.com", "edge.example.com."} {
		if !validGatewayPublicAddress(value) {
			t.Errorf("valid public address %q was rejected", value)
		}
	}
	for _, value := range []string{"", "https://edge.example.com", "edge.example.com:443", "*.example.com", "bad_name.example.com", " edge.example.com"} {
		if validGatewayPublicAddress(value) {
			t.Errorf("invalid public address %q was accepted", value)
		}
	}
}

func TestLoadSecretFilesSupportsEncryptionKeyringFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "keyring.json")
	encoded := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := os.WriteFile(path, []byte(`{"v2":"`+encoded+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	value := Defaults()
	value.Security.EncryptionKeysFile = path
	if err := loadSecretFiles(&value); err != nil {
		t.Fatal(err)
	}
	if value.Security.EncryptionKeys["v2"] != encoded {
		t.Fatalf("keyring file was not loaded: %#v", value.Security.EncryptionKeys)
	}
}

func TestLoadSecretFilesSupportsAllDNSProvidersWithoutSerializingCredentials(t *testing.T) {
	directory := t.TempDir()
	tests := map[string]struct {
		provider string
		secret   string
	}{
		"aliyun-prod":     {provider: "alidns", secret: `{"access_key":"access","secret_key":"secret"}`},
		"cloudflare-prod": {provider: "cloudflare", secret: `{"api_token":"token"}`},
		"tencent-prod":    {provider: "tencentcloud", secret: `{"secret_id":"id","secret_key":"secret"}`},
		"he-prod":         {provider: "hurricane", secret: `{"tokens":{"api.example.com":"token"}}`},
	}
	value := validConfig()
	value.ACME.DNSProviders = make(map[string]DNSProviderProfile, len(tests))
	for reference, test := range tests {
		path := filepath.Join(directory, reference+".json")
		if err := os.WriteFile(path, []byte(test.secret), 0o600); err != nil {
			t.Fatal(err)
		}
		value.ACME.DNSProviders[reference] = DNSProviderProfile{Provider: test.provider, CredentialsFile: path}
	}
	if err := loadSecretFiles(&value); err != nil {
		t.Fatal(err)
	}
	if err := value.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value.ACME.DNSProviders)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"access", "secret", "token"} {
		if strings.Contains(string(encoded), `"`+secret+`"`) {
			t.Fatalf("serialized DNS profiles leaked credential %q: %s", secret, encoded)
		}
	}
	if value.ACME.DNSProviders["cloudflare-prod"].Credentials.APIToken != "token" {
		t.Fatal("Cloudflare token was not loaded into memory")
	}
}

func TestLoadSecretFilesRejectsUnknownDNSCredentialField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dns.json")
	if err := os.WriteFile(path, []byte(`{"api_token":"token","unexpected":"value"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	value := validConfig()
	value.ACME.DNSProviders = map[string]DNSProviderProfile{"cloudflare": {Provider: "cloudflare", CredentialsFile: path}}
	if err := loadSecretFiles(&value); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected strict DNS credential error, got %v", err)
	}
}

func TestInstallationMetadataCannotTargetRootOrAnAmbiguousDirectory(t *testing.T) {
	for _, directory := range []string{"", "/var/lib/nsc-bootstrap", "/", "relative", "/var/lib/../nsc", "/var/lib/nsc/"} {
		cfg := validConfig()
		cfg.Controller.Installation.MetadataDirectory = directory
		err := cfg.Validate()
		valid := directory == "" || directory == "/var/lib/nsc-bootstrap"
		if valid && err != nil || !valid && err == nil {
			t.Fatalf("invalid metadata directory acceptance for %q: %v", directory, err)
		}
	}
}
