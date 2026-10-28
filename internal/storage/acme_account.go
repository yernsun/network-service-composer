package storage

import (
	"context"
	"errors"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func (s *SQLStore) GetACMEAccount(ctx context.Context, directoryURL, email string) (model.ACMEAccount, error) {
	var value model.ACMEAccount
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT id,directory_url,email,registration,encrypted_private_key,encryption_key_id,created_at,updated_at
        FROM acme_accounts WHERE directory_url=:directory_url AND email=:email`), directoryURL, email).Scan(&value.ID, &value.DirectoryURL, &value.Email,
		&value.Registration, &value.EncryptedPrivateKey, &value.EncryptionKeyID, &value.CreatedAt, &value.UpdatedAt)
	return value, mapError(err)
}

func (s *SQLStore) ReencryptACMEAccounts(ctx context.Context, transform func(model.ACMEAccount) ([]byte, string, error)) (int, error) {
	count := 0
	after := ""
	for {
		rows, err := s.db.QueryContext(ctx, s.bind(`SELECT id,directory_url,email,registration,encrypted_private_key,encryption_key_id,created_at,updated_at
            FROM acme_accounts WHERE id>:id ORDER BY id LIMIT :limit`), after, 200)
		if err != nil {
			return count, err
		}
		var values []model.ACMEAccount
		for rows.Next() {
			var value model.ACMEAccount
			if err = rows.Scan(&value.ID, &value.DirectoryURL, &value.Email, &value.Registration, &value.EncryptedPrivateKey,
				&value.EncryptionKeyID, &value.CreatedAt, &value.UpdatedAt); err != nil {
				rows.Close()
				return count, err
			}
			values = append(values, value)
		}
		if err = rows.Close(); err != nil {
			return count, err
		}
		if err = rows.Err(); err != nil {
			return count, err
		}
		for _, value := range values {
			encrypted, keyID, transformErr := transform(value)
			if transformErr != nil {
				return count, transformErr
			}
			if keyID == value.EncryptionKeyID {
				continue
			}
			result, updateErr := s.db.ExecContext(ctx, s.bind(`UPDATE acme_accounts SET encrypted_private_key=:encrypted_private_key,encryption_key_id=:encryption_key_id,updated_at=:updated_at WHERE id=:id AND encryption_key_id=:encryption_key_id_2`),
				encrypted, keyID, time.Now().UTC(), value.ID, value.EncryptionKeyID)
			if updateErr != nil {
				return count, updateErr
			}
			if err = requireAffected(result, true); err != nil {
				return count, err
			}
			count++
		}
		if len(values) < 200 {
			return count, nil
		}
		after = values[len(values)-1].ID
	}
}

func (s *SQLStore) UpsertACMEAccount(ctx context.Context, value *model.ACMEAccount) error {
	existing, err := s.GetACMEAccount(ctx, value.DirectoryURL, value.Email)
	if errors.Is(err, ErrNotFound) {
		_, err = s.db.ExecContext(ctx, s.bind(`INSERT INTO acme_accounts(id,directory_url,email,registration,encrypted_private_key,encryption_key_id,created_at,updated_at)
            VALUES(:id,:directory_url,:email,:registration,:encrypted_private_key,:encryption_key_id,:created_at,:updated_at)`), value.ID, value.DirectoryURL, value.Email, value.Registration, value.EncryptedPrivateKey, value.EncryptionKeyID, value.CreatedAt, value.UpdatedAt)
		return mapError(err)
	}
	if err != nil {
		return err
	}
	value.ID = existing.ID
	value.CreatedAt = existing.CreatedAt
	_, err = s.db.ExecContext(ctx, s.bind(`UPDATE acme_accounts SET registration=:registration,encrypted_private_key=:encrypted_private_key,encryption_key_id=:encryption_key_id,updated_at=:updated_at WHERE id=:id`),
		value.Registration, value.EncryptedPrivateKey, value.EncryptionKeyID, value.UpdatedAt, value.ID)
	return err
}
