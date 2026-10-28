package dnsprofile

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

const Kind = "dns-provider-profile"

type Stored struct {
	Provider    string                        `json:"provider"`
	Region      string                        `json:"region,omitempty"`
	Credentials config.DNSProviderCredentials `json:"credentials"`
}

type envelope struct {
	Ciphertext []byte `json:"ciphertext"`
	KeyID      string `json:"key_id"`
}

func Key(reference string) string { return Kind + ":" + reference }

func Read(ctx context.Context, store storage.Store, keyring *security.Keyring, reference string) (Stored, storage.ControlDocument, error) {
	var value Stored
	doc, err := store.GetControlDocument(ctx, Key(reference))
	if err != nil {
		return value, doc, err
	}
	if doc.Kind != Kind {
		return value, doc, storage.ErrNotFound
	}
	var wrapped envelope
	if err = json.Unmarshal(doc.Payload, &wrapped); err != nil {
		return value, doc, err
	}
	raw, _, err := keyring.Decrypt(wrapped.Ciphertext, []byte("control:"+doc.ID))
	if err != nil {
		return value, doc, err
	}
	if err = json.Unmarshal(raw, &value); err != nil {
		return Stored{}, doc, err
	}
	return value, doc, nil
}

func Resolve(ctx context.Context, store storage.Store, keyring *security.Keyring, cfg config.Config, reference string) (config.DNSProviderProfile, error) {
	if static, ok := cfg.ACME.DNSProviders[reference]; ok {
		return static, nil
	}
	stored, _, err := Read(ctx, store, keyring, reference)
	if err != nil {
		return config.DNSProviderProfile{}, err
	}
	profile := config.DNSProviderProfile{Provider: stored.Provider, Region: stored.Region, Credentials: stored.Credentials}
	if err = config.ValidateDNSProviderCredentials(reference, profile); err != nil {
		return config.DNSProviderProfile{}, fmt.Errorf("invalid DNS provider profile %q: %w", reference, err)
	}
	return profile, nil
}

func Encrypt(keyring *security.Keyring, reference string, value Stored) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	ciphertext, err := keyring.Encrypt(raw, []byte("control:"+Key(reference)))
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{Ciphertext: ciphertext, KeyID: keyring.PrimaryID()})
}
