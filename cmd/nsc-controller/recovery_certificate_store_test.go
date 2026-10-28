package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/recovery"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestRecoveryCertificateMaterialOnOwnedExternalStores(t *testing.T) {
	for _, fixture := range []struct{ driver, variable string }{{"postgres", "NSC_TEST_RECOVERY_POSTGRES_DSN"}, {"mysql", "NSC_TEST_RECOVERY_MYSQL_DSN"}} {
		t.Run(fixture.driver, func(t *testing.T) {
			dsn := os.Getenv(fixture.variable)
			if dsn == "" {
				t.Skip("explicit isolated recovery database fixture required")
			}
			store, err := storage.Open(fixture.driver, dsn)
			if err != nil {
				t.Fatal("open isolated recovery database failed")
			}
			defer store.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err = store.Migrate(ctx); err != nil {
				t.Fatal("migrate isolated recovery fixture failed")
			}
			cfg, _ := commandConfigFixture(t)
			ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			pool := model.GatewayPool{ID: id.New(id.Pool), Name: "isolated certificate recovery", Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			pool.Name += " " + pool.ID
			if err = store.CreatePool(ctx, &pool); err != nil {
				t.Fatal(err)
			}
			certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "acme", Status: model.CertificatePending, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			ciphertext, err := ring.Encrypt([]byte("private fixture certificate material"), []byte(certificate.ID))
			if err != nil {
				t.Fatal(err)
			}
			if err = store.CreateCertificate(ctx, &certificate); err != nil {
				t.Fatal(err)
			}
			// Repeated admission runs retain their fixtures. Keep this deliberately
			// incomplete record recoverable even if a negative assertion fails.
			defer func() {
				value, err := store.GetCertificate(ctx, certificate.ID)
				if err != nil {
					t.Error("read owned certificate cleanup failed", err)
					return
				}
				value.EncryptedPrivateKey, value.CertificatePEM, value.Status = ciphertext, "", model.CertificatePending
				if err = store.UpdateCertificate(ctx, &value, value.Revision); err != nil {
					t.Error("restore owned certificate fixture material failed", err)
				}
			}()
			for _, candidate := range []struct {
				name                string
				status              model.CertificateStatus
				pem, key, wantError bool
			}{
				{"unissued pending", model.CertificatePending, false, false, false},
				{"active key missing", model.CertificateActive, false, false, true},
				{"renewing key missing", model.CertificateRenewing, false, false, true},
				{"pending PEM key missing", model.CertificatePending, true, false, true},
				{"error PEM key missing", model.CertificateError, true, false, true},
				{"retained key and PEM", model.CertificateActive, true, true, false},
			} {
				t.Run(candidate.name, func(t *testing.T) {
					value, err := store.GetCertificate(ctx, certificate.ID)
					if err != nil {
						t.Fatal(err)
					}
					value.Status, value.CertificatePEM, value.EncryptedPrivateKey = candidate.status, "", nil
					if candidate.pem {
						value.CertificatePEM = "public fixture certificate material"
					}
					if candidate.key {
						value.EncryptedPrivateKey = ciphertext
					}
					if err = store.UpdateCertificate(ctx, &value, value.Revision); err != nil {
						t.Fatal(err)
					}
					before, err := storage.RecoveryFingerprint(ctx, store.DB())
					if err != nil {
						t.Fatal(err)
					}
					counts, err := recovery.CheckSecrets(ctx, store, ring)
					if candidate.wantError {
						if err == nil || counts != nil || strings.Contains(err.Error(), "fixture certificate material") {
							t.Fatal("incomplete issued certificate verified or plaintext escaped", counts, err)
						}
					} else if err != nil || counts == nil || candidate.key && counts["certificate"] < 1 {
						t.Fatal("honest absence or valid key rejected by native store", counts, err)
					}
					after, err := storage.RecoveryFingerprint(ctx, store.DB())
					if err != nil || after != before {
						t.Fatal("native certificate inspection changed restored state", err)
					}
				})
			}
		})
	}
}
