package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOfflineConfigurationDoesNotImportExecutorEnvironment(t *testing.T) {
	cfg := validConfig()
	file := filepath.Join(t.TempDir(), "controller.yaml")
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NSC_STORAGE__DRIVER", "mysql")
	loaded, err := LoadOffline(file)
	if err != nil || loaded.Storage.Driver != "sqlite" {
		t.Fatal("executor environment changed the existing authority", err)
	}
	cfg.Security.SessionKeyFile = "/run/nsc/session"
	data, err = yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadOfflineWithReader(file, func(path string) ([]byte, error) {
		if path != "/run/nsc/session" {
			t.Fatal("unexpected reference", path)
		}
		return []byte(cfg.Security.SessionKey), nil
	})
	if err != nil || loaded.Security.SessionKey != cfg.Security.SessionKey {
		t.Fatal("explicit credential reference did not use authenticated transport", err)
	}
	if _, err = LoadOfflineWithReader(file, func(string) ([]byte, error) { return nil, os.ErrNotExist }); err == nil {
		t.Fatal("missing original credential did not block adoption")
	}
	if _, err = LoadOffline(file + ".missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestAuthenticatedOfflineBytesRemainStrictAndIgnoreEnvironment(t *testing.T) {
	cfg := validConfig()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("NSC_STORAGE__DRIVER", "mysql")
	loaded, err := DecodeOffline(data, os.ReadFile)
	if err != nil || loaded.Storage.Driver != "sqlite" {
		t.Fatal("environment replaced authenticated node settings", err)
	}
	for _, input := range [][]byte{[]byte("unknown: true"), []byte("["), append(append([]byte{}, data...), []byte("\n---\nserver: {}")...)} {
		if _, err := DecodeOffline(input, os.ReadFile); err == nil {
			t.Fatal("unknown or multiple node configurations accepted")
		}
	}
	cfg.Security.SessionKeyFile = "/private/missing"
	data, _ = yaml.Marshal(cfg)
	if _, err := DecodeOffline(data, func(string) ([]byte, error) { return nil, os.ErrNotExist }); err == nil {
		t.Fatal("missing original credential accepted")
	}
}
