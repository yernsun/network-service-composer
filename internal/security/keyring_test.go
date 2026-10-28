package security

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestKeyringRoundTripAndAAD(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	ring, err := NewKeyring("primary", map[string]string{"primary": base64.StdEncoding.EncodeToString(key)})
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := ring.Encrypt([]byte("secret"), []byte("cert_1"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, keyID, err := ring.Decrypt(ciphertext, []byte("cert_1"))
	if err != nil {
		t.Fatal(err)
	}
	if keyID != "primary" || string(plaintext) != "secret" {
		t.Fatalf("unexpected decryption: %q %q", keyID, plaintext)
	}
	if _, _, err := ring.Decrypt(ciphertext, []byte("cert_2")); err == nil {
		t.Fatal("decrypt with different AAD succeeded")
	}
}
