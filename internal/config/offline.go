package config

import "os"

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
