package config

import (
	"bytes"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"os"
)

// LoadOffline verifies an existing private configuration and its explicit secret
// file references without importing the executor shell's NSC_* environment.
// Adoption must inspect the original authority, not accidentally replace it.
func LoadOffline(path string) (Config, error) {
	return LoadOfflineWithReader(path, os.ReadFile)
}

// LoadOfflineWithReader resolves only explicit credential references through an
// authenticated installation transport. It never imports process environment.
func LoadOfflineWithReader(path string, read func(string) ([]byte, error)) (Config, error) {
	config, err := decode(path)
	if err != nil {
		return Config{}, err
	}
	if err = loadSecretFilesUsing(&config, read); err != nil {
		return Config{}, err
	}
	return config, config.Validate()
}

// DecodeOffline inspects one authenticated node configuration without importing
// the operator environment or writing a temporary copy of its private contents.
func DecodeOffline(data []byte, read func(string) ([]byte, error)) (Config, error) {
	value := Defaults()
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&value); err != nil {
		return Config{}, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Config{}, fmt.Errorf("one Controller configuration document is required")
	}
	if err := loadSecretFilesUsing(&value, read); err != nil {
		return Config{}, err
	}
	return value, value.Validate()
}
