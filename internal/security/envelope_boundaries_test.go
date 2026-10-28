package security

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestCorruptEncryptedEnvelopeReturnsErrorWithoutPlaintext(t *testing.T) {
	ring, e := NewKeyring("primary", map[string]string{"primary": base64.StdEncoding.EncodeToString(make([]byte, 32))})
	if e != nil {
		t.Fatal(e)
	}
	raw, e := ring.Encrypt([]byte("private recovery value"), []byte("resource"))
	if e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"valid", "malformed", "unsupported version", "missing key", "invalid nonce encoding", "invalid ciphertext encoding", "empty nonce", "short nonce", "long nonce", "empty ciphertext", "tampered ciphertext", "wrong AAD"} {
		t.Run(mode, func(t *testing.T) {
			var v envelope
			if e := json.Unmarshal(raw, &v); e != nil {
				t.Fatal(e)
			}
			aad := []byte("resource")
			switch mode {
			case "unsupported version":
				v.Version = 2
			case "missing key":
				v.KeyID = "retired"
			case "invalid nonce encoding":
				v.Nonce = "!"
			case "invalid ciphertext encoding":
				v.Ciphertext = "!"
			case "empty nonce":
				v.Nonce = ""
			case "short nonce":
				v.Nonce = base64.RawStdEncoding.EncodeToString(make([]byte, 11))
			case "long nonce":
				v.Nonce = base64.RawStdEncoding.EncodeToString(make([]byte, 13))
			case "empty ciphertext":
				v.Ciphertext = ""
			case "tampered ciphertext":
				v.Ciphertext = base64.RawStdEncoding.EncodeToString(make([]byte, 64))
			case "wrong AAD":
				aad = []byte("foreign resource")
			}
			encoded, e := json.Marshal(v)
			if e != nil {
				t.Fatal(e)
			}
			if mode == "malformed" {
				encoded = []byte("{")
			}
			plain, key, e := ring.Decrypt(encoded, aad)
			if mode == "valid" {
				if e != nil || string(plain) != "private recovery value" || key != "primary" {
					t.Fatal("valid recovery failed", e)
				}
				return
			}
			if e == nil || plain != nil {
				t.Fatal("corrupt envelope exposed plaintext", mode)
			}
			if strings.Contains(e.Error(), "private recovery value") {
				t.Fatal("diagnostic leaked plaintext")
			}
			if mode != "malformed" && key != v.KeyID {
				t.Fatal("failure lost key identity", key)
			}
		})
	}
}
