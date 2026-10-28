package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type Keyring struct {
	primary string
	keys    map[string][]byte
}

type envelope struct {
	Version    int    `json:"v"`
	KeyID      string `json:"kid"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func NewKeyring(primary string, encoded map[string]string) (*Keyring, error) {
	if primary == "" {
		return nil, errors.New("primary encryption key id is required")
	}
	keys := make(map[string][]byte, len(encoded))
	for id, value := range encoded {
		key, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("decode encryption key %q: %w", id, err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("encryption key %q must decode to 32 bytes", id)
		}
		keys[id] = key
	}
	if _, ok := keys[primary]; !ok {
		return nil, fmt.Errorf("primary encryption key %q is not present", primary)
	}
	return &Keyring{primary: primary, keys: keys}, nil
}

func (k *Keyring) PrimaryID() string { return k.primary }

func (k *Keyring) Encrypt(plaintext, aad []byte) ([]byte, error) {
	key := k.keys[k.primary]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	payload := envelope{
		Version:    1,
		KeyID:      k.primary,
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(gcm.Seal(nil, nonce, plaintext, aad)),
	}
	return json.Marshal(payload)
}

func (k *Keyring) Decrypt(value, aad []byte) ([]byte, string, error) {
	var payload envelope
	if err := json.Unmarshal(value, &payload); err != nil {
		return nil, "", fmt.Errorf("decode encrypted envelope: %w", err)
	}
	if payload.Version != 1 {
		return nil, payload.KeyID, fmt.Errorf("unsupported encrypted envelope version %d", payload.Version)
	}
	key, ok := k.keys[payload.KeyID]
	if !ok {
		return nil, payload.KeyID, fmt.Errorf("encryption key %q is unavailable", payload.KeyID)
	}
	nonce, err := base64.RawStdEncoding.DecodeString(payload.Nonce)
	if err != nil {
		return nil, payload.KeyID, fmt.Errorf("decode nonce: %w", err)
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(payload.Ciphertext)
	if err != nil {
		return nil, payload.KeyID, fmt.Errorf("decode ciphertext: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, payload.KeyID, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, payload.KeyID, err
	}
	// Envelopes are persisted input. GCM panics for an invalid nonce length;
	// report corrupt recovery material without crashing the service instead.
	if len(nonce) != gcm.NonceSize() {
		return nil, payload.KeyID, errors.New("decrypt encrypted value: invalid nonce length")
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, payload.KeyID, errors.New("decrypt encrypted value: authentication failed")
	}
	return plaintext, payload.KeyID, nil
}
