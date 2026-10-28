package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/storage"
)

type controlRotationStore struct {
	storage.Store
	docs                    []storage.ControlDocument
	kind                    string
	listError, errorOnWrite error
	written                 []storage.ControlDocument
	pages                   []storage.Page
}

func (s *controlRotationStore) ListControlDocuments(_ context.Context, kind, parent string, p storage.Page) ([]storage.ControlDocument, error) {
	if kind != s.kind {
		return nil, nil
	}
	s.pages = append(s.pages, p)
	if s.listError != nil {
		return nil, s.listError
	}
	result := []storage.ControlDocument{}
	for _, d := range s.docs {
		if d.ID > p.After {
			result = append(result, d)
			if len(result) == p.Limit {
				break
			}
		}
	}
	return result, nil
}
func (s *controlRotationStore) ReencryptControlDocument(_ context.Context, d storage.ControlDocument, payload json.RawMessage) error {
	if s.errorOnWrite != nil {
		return s.errorOnWrite
	}
	d.Payload = append([]byte(nil), payload...)
	s.written = append(s.written, d)
	return nil
}
func TestControlKeyRotationPreservesIdentityAcrossPagesAndFailures(t *testing.T) {
	for _, kind := range []string{"variables", "variable-version", "environment-compose", "environment-review", "release-snapshot", "application-restore", "hook", "notification-settings"} {
		for _, mode := range []string{"valid", "corrupt document", "corrupt ciphertext", "list unavailable", "concurrent edit"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				c, _, _ := controlFixture(t)
				failure := errors.New("rotation unavailable")
				store := &controlRotationStore{Store: c.store, kind: kind}
				count := 1
				if kind == "variables" && mode == "valid" {
					count = 201
				}
				for i := 0; i < count; i++ {
					key := fmt.Sprintf("rotation-%03d", i)
					var raw []byte
					if kind == "hook" {
						key += ":hook"
						secret, e := c.keyring.Encrypt([]byte("preserved webhook secret"), []byte("image-hook:"+strings.TrimSuffix(key, ":hook")))
						if e != nil {
							t.Fatal(e)
						}
						raw, e = json.Marshal(Hook{EncryptedSecret: secret})
						if e != nil {
							t.Fatal(e)
						}
					} else {
						w, e := c.encryptedWrite(key, kind, "owner", map[string]string{"value": "preserved secret"}, 7)
						if e != nil {
							t.Fatal(e)
						}
						raw = w.Document.Payload
					}
					if mode == "corrupt document" {
						raw = []byte("{")
					}
					if mode == "corrupt ciphertext" {
						raw = []byte(`{"ciphertext":"AA==","encrypted_secret":"AA=="}`)
					}
					store.docs = append(store.docs, storage.ControlDocument{ID: key, Kind: kind, ParentID: "owner", Payload: raw, Revision: 7, CreatedAt: time.Unix(100, 0), UpdatedAt: time.Unix(200, 0)})
				}
				if mode == "list unavailable" {
					store.listError = failure
				}
				if mode == "concurrent edit" {
					store.errorOnWrite = storage.ErrPrecondition
				}
				c.store = store
				changed, e := c.ReencryptControlSecrets(context.Background())
				if mode != "valid" {
					if e == nil || changed != 0 || len(store.written) != 0 {
						t.Fatal("rotation hid failure or overwrote incompatible data", changed, e)
					}
					if mode == "list unavailable" && !errors.Is(e, failure) || mode == "concurrent edit" && !errors.Is(e, storage.ErrPrecondition) {
						t.Fatal("rotation lost failure identity", e)
					}
					return
				}
				if e != nil || changed != count || len(store.written) != count {
					t.Fatal("rotation skipped historical material", changed, e)
				}
				if count == 201 && (len(store.pages) != 2 || store.pages[1].After != store.docs[199].ID) {
					t.Fatal("rotation pagination skipped or repeated records", store.pages)
				}
				for i, d := range store.written {
					old := store.docs[i]
					if d.ID != old.ID || d.Revision != 7 || d.Kind != kind || d.ParentID != old.ParentID || !d.CreatedAt.Equal(old.CreatedAt) || !d.UpdatedAt.Equal(old.UpdatedAt) {
						t.Fatal("reencryption altered resource identity or revision", d)
					}
					var raw []byte
					var err error
					if kind == "hook" {
						var h Hook
						if err = json.Unmarshal(d.Payload, &h); err != nil {
							t.Fatal(err)
						}
						raw, _, err = c.keyring.Decrypt(h.EncryptedSecret, []byte("image-hook:"+strings.TrimSuffix(d.ID, ":hook")))
					} else {
						var enc encryptedControl
						if err = json.Unmarshal(d.Payload, &enc); err != nil {
							t.Fatal(err)
						}
						if enc.KeyID != c.keyring.PrimaryID() {
							t.Fatal("wrong primary key identity")
						}
						raw, _, err = c.keyring.Decrypt(enc.Ciphertext, []byte("control:"+d.ID))
					}
					if err != nil || !strings.Contains(string(raw), "preserved") {
						t.Fatal("rotation changed or lost plaintext", err)
					}
				}
			})
		}
	}
}
