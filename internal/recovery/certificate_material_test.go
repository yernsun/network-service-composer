package recovery

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestRecoveryCertificateCompletenessPreservesUnissuedAbsenceAndRequiresIssuedKeys(t *testing.T) {
	for _, candidate := range []struct {
		name                string
		status              model.CertificateStatus
		pem, key, wantError bool
	}{
		{"pending before issuance", model.CertificatePending, false, false, false},
		{"authorizing before issuance", model.CertificateAuthorizing, false, false, false},
		{"validating before issuance", model.CertificateValidating, false, false, false},
		{"failed before issuance", model.CertificateError, false, false, false},
		{"active key missing without PEM", model.CertificateActive, false, false, true},
		{"renewing key missing without PEM", model.CertificateRenewing, false, false, true},
		{"pending with issued PEM key missing", model.CertificatePending, true, false, true},
		{"failed with retained PEM key missing", model.CertificateError, true, false, true},
		{"active encrypted key retained", model.CertificateActive, false, true, false},
		{"renewing PEM and encrypted key retained", model.CertificateRenewing, true, true, false},
	} {
		t.Run(candidate.name, func(t *testing.T) {
			store, ring, rt := recoveryFixture(t)
			ctx, now := context.Background(), time.Now().UTC()
			certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: rt.PoolID, Provider: "acme", Status: candidate.status, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			if candidate.pem {
				certificate.CertificatePEM = "public restored fixture material"
			}
			if candidate.key {
				var err error
				certificate.EncryptedPrivateKey, err = ring.Encrypt([]byte("private restored fixture material"), []byte(certificate.ID))
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := store.CreateCertificate(ctx, &certificate); err != nil {
				t.Fatal(err)
			}
			before, err := storage.RecoveryFingerprint(ctx, store.DB())
			if err != nil {
				t.Fatal(err)
			}
			counts, err := CheckSecrets(ctx, store, ring)
			if candidate.wantError {
				if err == nil || counts != nil || strings.Contains(err.Error(), "restored fixture material") {
					t.Fatal("incomplete issued material passed or plaintext escaped", counts, err)
				}
			} else {
				expected := 0
				if candidate.key {
					expected = 1
				}
				if err != nil || counts["certificate"] != expected {
					t.Fatal("honest absence or retained key was rejected", counts, err)
				}
			}
			after, err := storage.RecoveryFingerprint(ctx, store.DB())
			if err != nil || after != before {
				t.Fatal("certificate inspection changed restored material", err)
			}
		})
	}
}
