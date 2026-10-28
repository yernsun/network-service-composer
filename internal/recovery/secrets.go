package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// CheckSecrets reads all persisted envelope categories without exposing their
// plaintext or changing the database. Zero encrypted records is honest evidence
// of absence, rather than a fabricated key verification.
func CheckSecrets(ctx context.Context, store *storage.SQLStore, ring *security.Keyring) (map[string]int, error) {
	counts := map[string]int{"certificate": 0, "acme_account": 0, "application_revision": 0, "operation": 0, "control_document": 0, "hook": 0}
	query := `SELECT 'certificate',id,encrypted_private_key,
        CASE WHEN status IN ('ACTIVE','RENEWING') OR certificate_pem<>'' THEN 1 ELSE 0 END FROM certificates
        UNION ALL SELECT 'acme_account',id,encrypted_private_key,1 FROM acme_accounts
        UNION ALL SELECT 'revision_compose',id,encrypted_compose,1 FROM application_revisions
        UNION ALL SELECT 'revision_environment',id,encrypted_environment,1 FROM application_revisions
        UNION ALL SELECT 'revision_resolved',id,encrypted_resolved,1 FROM application_revisions
        UNION ALL SELECT 'operation',id,encrypted_input,0 FROM deployment_operations`
	rows, err := store.DB().QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind, id string
		var ciphertext []byte
		var required bool
		if err = rows.Scan(&kind, &id, &ciphertext, &required); err != nil {
			rows.Close()
			return nil, err
		}
		if len(ciphertext) == 0 {
			if required {
				rows.Close()
				return nil, fmt.Errorf("recovery %s %s has no encrypted material", kind, id)
			}
			continue
		}
		aad := id
		category := kind
		if strings.HasPrefix(kind, "revision_") {
			aad = "application-revision:" + id + ":" + strings.TrimPrefix(kind, "revision_")
			category = "application_revision"
		} else if kind == "operation" {
			aad = "deployment-operation:" + id
		}
		if _, _, err = ring.Decrypt(ciphertext, []byte(aad)); err != nil {
			rows.Close()
			return nil, fmt.Errorf("recovery cannot decrypt %s %s: %w", kind, id, err)
		}
		counts[category]++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = store.DB().QueryContext(ctx, "SELECT id,kind,payload FROM control_documents ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind string
		var raw []byte
		if err = rows.Scan(&id, &kind, &raw); err != nil {
			return nil, err
		}
		var envelope struct {
			Ciphertext []byte `json:"ciphertext"`
			Secret     []byte `json:"encrypted_secret"`
		}
		if err = json.Unmarshal(raw, &envelope); err != nil {
			if encryptedControlKind(kind) || !json.Valid(raw) {
				return nil, fmt.Errorf("recovery control document %s is invalid", id)
			}
			continue
		}
		ciphertext, aad, category := envelope.Ciphertext, "control:"+id, "control_document"
		if kind == "hook" {
			ciphertext, aad, category = envelope.Secret, "image-hook:"+strings.TrimSuffix(id, ":hook"), "hook"
		}
		if len(ciphertext) == 0 {
			if encryptedControlKind(kind) {
				return nil, fmt.Errorf("recovery control document %s has no encrypted material", id)
			}
			continue
		}
		if _, _, err = ring.Decrypt(ciphertext, []byte(aad)); err != nil {
			return nil, fmt.Errorf("recovery cannot decrypt control document %s: %w", id, err)
		}
		counts[category]++
	}
	return counts, rows.Err()
}

func encryptedControlKind(kind string) bool {
	return slices.Contains(storage.EncryptedControlKinds(), kind)
}
