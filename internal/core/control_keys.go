package core

import (
	"context"
	"encoding/json"
	"github.com/yernsun/network-service-composer/internal/storage"
	"strings"
)

// ReencryptControlSecrets covers staged, historical and webhook secrets with
// the same primary key as application revisions. CAS preserves concurrent edits.
func (c *Core) ReencryptControlSecrets(ctx context.Context) (int, error) {
	count := 0
	for _, kind := range storage.EncryptedControlKinds() {
		page := storage.Page{Limit: 200}
		for {
			docs, e := c.store.ListControlDocuments(ctx, kind, "", page)
			if e != nil {
				return count, e
			}
			for _, d := range docs {
				var payload any
				if kind == "hook" {
					var h Hook
					if e = json.Unmarshal(d.Payload, &h); e != nil {
						return count, e
					}
					aad := []byte("image-hook:" + strings.TrimSuffix(d.ID, ":hook"))
					raw, _, err := c.keyring.Decrypt(h.EncryptedSecret, aad)
					if err != nil {
						return count, err
					}
					h.EncryptedSecret, err = c.keyring.Encrypt(raw, aad)
					if err != nil {
						return count, err
					}
					payload = h
				} else {
					var enc encryptedControl
					if e = json.Unmarshal(d.Payload, &enc); e != nil {
						return count, e
					}
					raw, _, err := c.keyring.Decrypt(enc.Ciphertext, []byte("control:"+d.ID))
					if err != nil {
						return count, err
					}
					enc.Ciphertext, err = c.keyring.Encrypt(raw, []byte("control:"+d.ID))
					if err != nil {
						return count, err
					}
					enc.KeyID = c.keyring.PrimaryID()
					payload = enc
				}
				encoded, e := json.Marshal(payload)
				if e != nil {
					return count, e
				}
				if e = c.store.ReencryptControlDocument(ctx, d, encoded); e != nil {
					return count, e
				}
				count++
			}
			if len(docs) < 200 {
				break
			}
			page.After = docs[len(docs)-1].ID
		}
	}
	return count, nil
}
