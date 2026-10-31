package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestGeneratedConfigurationDurationRoundTrip(t *testing.T) {
	before := Defaults()
	before.Server.ConsoleHTTPSListen = ":8443"
	before.Server.ConsoleHTTPSCertFile = "/run/nsc/console.crt"
	before.Server.ConsoleHTTPSKeyFile = "/run/nsc/console.key"
	data, err := yaml.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "generated.yaml")
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := decode(path)
	if err != nil {
		t.Fatal("generated config cannot be decoded by the real runtime", err)
	}
	if after.Server.ReadTimeout != Duration(15*time.Second) || after.Controller.Gateway.OfflineTimeout != before.Controller.Gateway.OfflineTimeout || after.Agent.HeartbeatInterval != before.Agent.HeartbeatInterval || after.Server.ConsoleHTTPSListen != ":8443" {
		t.Fatal("configuration values changed on round trip")
	}
}

func TestConsoleHTTPSConfigurationRequiresIndependentCompleteListener(t *testing.T) {
	for _, scenario := range []string{"bad-address", "empty-port", "same-http", "missing-cert", "missing-key", "cert-without-listener", "key-without-listener", "valid"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := validConfig()
			cfg.Server.ConsoleHTTPSListen = ":8443"
			cfg.Server.ConsoleHTTPSCertFile = "/run/nsc/console.crt"
			cfg.Server.ConsoleHTTPSKeyFile = "/run/nsc/console.key"
			switch scenario {
			case "bad-address":
				cfg.Server.ConsoleHTTPSListen = "invalid"
			case "empty-port":
				cfg.Server.ConsoleHTTPSListen = "localhost:"
			case "same-http":
				cfg.Server.ConsoleHTTPSListen = cfg.Server.HTTPListen
			case "missing-cert":
				cfg.Server.ConsoleHTTPSCertFile = ""
			case "missing-key":
				cfg.Server.ConsoleHTTPSKeyFile = ""
			case "cert-without-listener":
				cfg.Server.ConsoleHTTPSListen = ""
			case "key-without-listener":
				cfg.Server.ConsoleHTTPSListen = ""
				cfg.Server.ConsoleHTTPSCertFile = ""
			}
			err := cfg.Validate()
			if scenario == "valid" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("incomplete HTTPS listener accepted")
			}
		})
	}
	t.Setenv("NSC_SERVER__CONSOLE_HTTPS_LISTEN", ":8443")
	t.Setenv("NSC_SERVER__CONSOLE_HTTPS_CERT_FILE", "/run/nsc/console.crt")
	t.Setenv("NSC_SERVER__CONSOLE_HTTPS_KEY_FILE", "/run/nsc/console.key")
	cfg := validConfig()
	if err := applyEnvironment(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.ConsoleHTTPSListen != ":8443" || cfg.Server.ConsoleHTTPSCertFile != "/run/nsc/console.crt" || cfg.Server.ConsoleHTTPSKeyFile != "/run/nsc/console.key" {
		t.Fatal("HTTPS environment configuration lost")
	}
}
