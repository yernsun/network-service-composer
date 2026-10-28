package config

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"gopkg.in/yaml.v3"
)

func configFixtureFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func isolateConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "NSC_") {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func controllerYAML() string {
	return "storage:\n  driver: sqlite\n  dsn: 'file:isolated.db'\nserver:\n  grpc_listen: ''\nsecurity:\n  admin_token: fixture-token\n  session_key: '" + strings.Repeat("s", 32) + "'\n  primary_key_id: v1\n  encryption_keys:\n    v1: '" + base64.StdEncoding.EncodeToString(make([]byte, 32)) + "'\n"
}
func agentYAML() string {
	c := validAgentConfig()
	return "agent:\n  gateway_id: " + c.Agent.GatewayID + "\n  pool_id: " + c.Agent.PoolID + "\n  controller_address: controller:9443\n  server_name: controller\n  ca_cert_file: ca\n  tls_cert_file: cert\n  tls_key_file: key\n"
}

func TestConfigurationLoadPrecedenceAndFailures(t *testing.T) {
	isolateConfigEnvironment(t)
	t.Run("controller environment overrides file and secrets override environment", func(t *testing.T) {
		token := configFixtureFile(t, "  file-token\n")
		session := configFixtureFile(t, strings.Repeat("k", 32)+"\n")
		t.Setenv("NSC_SECURITY__ADMIN_TOKEN", "environment-token")
		t.Setenv("NSC_SECURITY__ADMIN_TOKEN_FILE", token)
		t.Setenv("NSC_SECURITY__SESSION_KEY_FILE", session)
		t.Setenv("NSC_SERVER__HTTP_LISTEN", ":8123")
		t.Setenv("NSC_SERVER__READ_TIMEOUT", "3s")
		t.Setenv("NSC_SERVER__RATE_LIMIT_RPS", "12.5")
		t.Setenv("NSC_SERVER__RATE_LIMIT_BURST", "15")
		t.Setenv("NSC_STORAGE__AUTO_MIGRATE", "false")
		t.Setenv("NSC_MANAGEMENT__EXTERNAL_SECRETS", `["external"]`)
		t.Setenv("NSC_CONTROLLER__ENDPOINT__READINESS__MODE", " HTTP ")
		t.Setenv("NSC_CONTROLLER__ENDPOINT__READINESS__PATH", "/ready")
		t.Setenv("NSC_CONTROLLER__ENDPOINT__READINESS__TIMEOUT", "500ms")
		c, err := Load(configFixtureFile(t, controllerYAML()))
		if err != nil {
			t.Fatal(err)
		}
		if c.Security.AdminToken != "file-token" || c.Security.SessionKey != strings.Repeat("k", 32) || c.Server.HTTPListen != ":8123" || c.Server.ReadTimeout.Std() != 3*time.Second || c.Server.RateLimitRPS != 12.5 || c.Server.RateLimitBurst != 15 || c.Storage.AutoMigrate || len(c.Management.ExternalSecrets) != 1 || c.Management.ExternalSecrets[0] != "external" || c.Controller.Endpoint.Readiness.Mode != model.ReadinessHTTP || c.Controller.Endpoint.Readiness.Timeout != 500*time.Millisecond {
			t.Fatal("configuration precedence or typed environment parsing changed")
		}
	})
	t.Run("agent file and environment", func(t *testing.T) {
		t.Setenv("NSC_AGENT__PROBE_WORKERS", "4")
		c, err := LoadAgent(configFixtureFile(t, agentYAML()))
		if err != nil || c.Agent.ProbeWorkers != 4 {
			t.Fatalf("agent load failed: %v", err)
		}
	})
	for _, entry := range []struct {
		name  string
		load  func(string) (Config, error)
		valid string
	}{{"controller", Load, controllerYAML()}, {"agent", LoadAgent, agentYAML()}} {
		t.Run(entry.name, func(t *testing.T) {
			for _, tc := range []struct{ name, body, want string }{{"unknown key", "unknown_setting: true", "decode config"}, {"invalid YAML", "server: [", "decode config"}, {"invalid duration", "server:\n  read_timeout: soon", "decode config"}, {"empty file", "", "required"}} {
				t.Run(tc.name, func(t *testing.T) {
					_, err := entry.load(configFixtureFile(t, tc.body))
					if err == nil || !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("invalid config accepted or misdiagnosed: %v", err)
					}
				})
			}
			if _, err := entry.load(filepath.Join(t.TempDir(), "missing")); err == nil || !strings.Contains(err.Error(), "read config") {
				t.Fatalf("missing config=%v", err)
			}
			if _, err := entry.load(""); err == nil {
				t.Fatal("unconfigured identity accepted")
			}
			t.Run("invalid environment", func(t *testing.T) {
				t.Setenv("NSC_STORAGE__HA", "not-bool")
				_, err := entry.load(configFixtureFile(t, entry.valid))
				if err == nil || !strings.Contains(err.Error(), "NSC_STORAGE__HA") {
					t.Fatalf("environment failure=%v", err)
				}
			})
		})
	}
	t.Run("secret failure prevents load", func(t *testing.T) {
		t.Setenv("NSC_SECURITY__ADMIN_TOKEN_FILE", filepath.Join(t.TempDir(), "missing"))
		_, err := Load(configFixtureFile(t, controllerYAML()))
		if err == nil || !strings.Contains(err.Error(), "read admin token") {
			t.Fatalf("unreadable secret accepted: %v", err)
		}
	})
}

func TestEnvironmentTypeFailuresAreAggregated(t *testing.T) {
	isolateConfigEnvironment(t)
	keys := []string{"NSC_SERVER__READ_TIMEOUT", "NSC_CONTROLLER__ENDPOINT__READINESS__INTERVAL", "NSC_SERVER__RATE_LIMIT_RPS", "NSC_SERVER__RATE_LIMIT_BURST", "NSC_STORAGE__HA", "NSC_MANAGEMENT__EXTERNAL_SECRETS"}
	for _, key := range keys {
		t.Setenv(key, "invalid")
	}
	c := validConfig()
	before := c
	err := applyEnvironment(&c)
	if err == nil {
		t.Fatal("malformed environment accepted")
	}
	for _, key := range keys {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("missing diagnostic for %s", key)
		}
	}
	if c.Server.ReadTimeout != before.Server.ReadTimeout || c.Controller.Endpoint.Readiness.Interval != before.Controller.Endpoint.Readiness.Interval || c.Server.RateLimitRPS != before.Server.RateLimitRPS || c.Server.RateLimitBurst != before.Server.RateLimitBurst || c.Storage.HA != before.Storage.HA {
		t.Fatal("failed conversion overwrote configured value")
	}
}

func TestDurationDecodingPreservesValueOnFailure(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
		want  time.Duration
	}{{`"1.5s"`, true, 1500 * time.Millisecond}, {`"0s"`, true, 0}, {`"-1s"`, true, -time.Second}, {`12`, false, 0}, {`"soon"`, false, 0}, {`"999999999999999999999h"`, false, 0}} {
		d := Duration(7 * time.Second)
		err := json.Unmarshal([]byte(tc.body), &d)
		if tc.valid {
			if err != nil || d.Std() != tc.want {
				t.Fatalf("duration=%v, %v", d, err)
			}
		} else if err == nil || d.Std() != 7*time.Second {
			t.Fatalf("invalid duration changed value: %v, %v", d, err)
		}
	}
	var d Duration
	if err := yaml.Unmarshal([]byte("2m"), &d); err != nil || d.Std() != 2*time.Minute {
		t.Fatalf("YAML duration=%v, %v", d, err)
	}
}

func TestSecretFileFailureBoundaries(t *testing.T) {
	for _, field := range []string{"AdminTokenFile", "SessionKeyFile", "EncryptionKeysFile"} {
		t.Run(field, func(t *testing.T) {
			c := validConfig()
			setConfigField(t, &c, "Security."+field, filepath.Join(t.TempDir(), "missing"))
			if err := loadSecretFiles(&c); err == nil || !strings.Contains(err.Error(), "read ") {
				t.Fatalf("missing secret accepted: %v", err)
			}
		})
	}
	c := validConfig()
	c.Security.EncryptionKeysFile = configFixtureFile(t, `{"v1":12}`)
	if err := loadSecretFiles(&c); err == nil || !strings.Contains(err.Error(), "decode encryption keyring") {
		t.Fatalf("corrupt keyring accepted: %v", err)
	}
	for _, tc := range []struct{ name, body, want string }{{"multiple values", `{} {}`, "multiple JSON values"}, {"trailing garbage", `{} trailing`, "decode DNS provider"}, {"malformed", `{`, "decode DNS provider"}} {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			c.ACME.DNSProviders = map[string]DNSProviderProfile{"test": {Provider: "cloudflare", CredentialsFile: configFixtureFile(t, tc.body)}}
			if err := loadSecretFiles(&c); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid DNS secret accepted: %v", err)
			}
		})
	}
	for _, path := range []string{" ", filepath.Join(t.TempDir(), "missing")} {
		c := validConfig()
		c.ACME.DNSProviders = map[string]DNSProviderProfile{"test": {CredentialsFile: path}}
		if err := loadSecretFiles(&c); err == nil {
			t.Fatal("unavailable DNS credentials accepted")
		}
	}
}
