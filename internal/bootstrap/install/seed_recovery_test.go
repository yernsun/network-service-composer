package install

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/pki"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type interruptedSeed struct {
	storage.Store
	after string
	fired bool
}

func (s *interruptedSeed) interrupt(stage string, err error) error {
	if err == nil && s.after == stage && !s.fired {
		s.fired = true
		return context.DeadlineExceeded
	}
	return err
}
func (s *interruptedSeed) CreatePool(ctx context.Context, v *model.GatewayPool) error {
	return s.interrupt("pool", s.Store.CreatePool(ctx, v))
}
func (s *interruptedSeed) CreateRuntime(ctx context.Context, v *model.Runtime) error {
	return s.interrupt("runtime", s.Store.CreateRuntime(ctx, v))
}
func (s *interruptedSeed) CreateCertificate(ctx context.Context, v *model.Certificate) error {
	return s.interrupt("certificate", s.Store.CreateCertificate(ctx, v))
}
func (s *interruptedSeed) WriteControlDocuments(ctx context.Context, w []storage.ControlWrite) error {
	err := s.Store.WriteControlDocuments(ctx, w)
	for _, v := range w {
		if v.Document.ID == "community:identities" {
			err = s.interrupt("administrator", err)
		}
		if v.Document.Kind == "sandbox" {
			err = s.interrupt("sandbox", err)
		}
	}
	return err
}

func TestInterruptedOfflineSeedingReusesPersistedOwnershipWithoutResettingData(t *testing.T) {
	for _, stage := range []string{"pool", "runtime", "administrator", "certificate", "sandbox"} {
		t.Run(stage, func(t *testing.T) {
			i, _ := installerFixture(t, 1)
			infrastructure(t, i)
			if err := i.withStore(t.Context(), false, func(store storage.Store, cfg config.Config) error {
				if err := store.Migrate(t.Context()); err != nil {
					return err
				}
				ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
				if err != nil {
					return err
				}
				interrupted := &interruptedSeed{Store: store, after: stage}
				err = SeedStore(t.Context(), interrupted, cfg, ring, i.seedRecord())
				if !interrupted.fired || !errors.Is(err, context.DeadlineExceeded) {
					return fmt.Errorf("interruption was hidden: %v", err)
				}
				marker, err := store.GetControlDocument(t.Context(), bootstrap.InstallationDocumentID)
				if err != nil {
					return err
				}
				var saved bootstrap.Installation
				if err = json.Unmarshal(marker.Payload, &saved); err != nil {
					return err
				}
				if saved.ID != i.Record.ID || saved.State != bootstrap.Pending {
					return fmt.Errorf("partial ownership receipt lost")
				}
				if err = SeedStore(t.Context(), store, cfg, ring, i.seedRecord()); err != nil {
					return err
				}
				pools, err := store.ListPools(t.Context(), storage.Page{Limit: 200})
				if err != nil {
					return err
				}
				certificates, err := store.ListCertificates(t.Context(), storage.Page{Limit: 200})
				if err != nil {
					return err
				}
				if len(pools) != 1 || len(certificates) != 1 || pools[0].ID != i.Record.PoolID || certificates[0].ID != i.Record.CertificateID {
					return fmt.Errorf("retry duplicated or replaced resources")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOfflineAuthorityRequiresEveryBindingAndValidExistingCertificate(t *testing.T) {
	i, _ := installerFixture(t, 1)
	infrastructure(t, i)
	if err := i.withStore(t.Context(), false, func(store storage.Store, cfg config.Config) error {
		if err := store.Migrate(t.Context()); err != nil {
			return err
		}
		ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
		if err != nil {
			return err
		}
		for _, field := range []string{"id", "pool", "runtime", "cluster", "certificate", "volume", "single-point", "components", "component-id", "component-version", "data-node", "data-engine", "database-service", "config-cluster", "config-runtime", "config-security", "nil-keyring", "driver", "automatic-migration", "component-kind", "component-digest"} {
			r := i.seedRecord()
			local := cfg
			keyring := ring
			switch field {
			case "id":
				r.ID = ""
			case "pool":
				r.PoolID = ""
			case "runtime":
				r.RuntimeID = ""
			case "cluster":
				r.ClusterID = ""
			case "certificate":
				r.CertificateID = ""
			case "volume":
				r.Database.Volume = ""
			case "single-point":
				r.Database.SinglePointOfFailure = false
			case "components":
				delete(r.Components, "nginx")
			case "component-id":
				entry := r.Components["controller"]
				entry.ID = ""
				r.Components["controller"] = entry
			case "component-version":
				entry := r.Components["controller"]
				entry.Version = 0
				r.Components["controller"] = entry
			case "data-node":
				r.Database.NodeID = ""
			case "data-engine":
				r.Database.EngineID = ""
			case "database-service":
				r.Database.ServiceID = "external"
			case "config-cluster":
				local.Controller.SystemProject.ClusterID = "external"
			case "config-runtime":
				local.Controller.SystemProject.RuntimeID = "external"
			case "config-security":
				local.Security.AdminToken = ""
			case "nil-keyring":
				keyring = nil
			case "driver":
				local.Storage.Driver = "mysql"
			case "automatic-migration":
				local.Storage.AutoMigrate = true
			case "component-kind":
				entry := r.Components["controller"]
				entry.Kind = "foreign"
				r.Components["controller"] = entry
			case "component-digest":
				entry := r.Components["controller"]
				entry.SpecDigest = ""
				r.Components["controller"] = entry
			}
			if err := SeedStore(t.Context(), store, local, keyring, r); err == nil {
				return fmt.Errorf("incomplete authority accepted: %s", field)
			}
		}
		cert, _ := os.ReadFile(i.material("console.crt"))
		key, _ := bootstrap.ReadPrivate(i.material("console.key"))
		for _, bad := range [][]byte{[]byte("invalid"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid-der")})} {
			os.WriteFile(i.material("console.crt"), bad, 0600)
			if err = seedConsoleCertificate(t.Context(), store, ring, i.seedRecord(), i.material("console.crt"), i.material("console.key")); err == nil {
				return fmt.Errorf("invalid certificate imported")
			}
		}
		os.WriteFile(i.material("console.crt"), cert, 0600)
		os.WriteFile(i.material("console.key"), []byte("invalid-key"), 0600)
		if err = seedConsoleCertificate(t.Context(), store, ring, i.seedRecord(), i.material("console.crt"), i.material("console.key")); err == nil {
			return fmt.Errorf("mismatched console key imported")
		}
		cert, key, err = pki.SelfSigned([]string{"console.example.test"}, nil, 20, time.Now())
		if err != nil {
			return err
		}
		os.WriteFile(i.material("console.crt"), cert, 0600)
		os.WriteFile(i.material("console.key"), key, 0600)
		if err = seedConsoleCertificate(t.Context(), store, ring, i.seedRecord(), i.material("console.crt"), i.material("console.key")); err == nil || !strings.Contains(err.Error(), "IP SAN") {
			return fmt.Errorf("console without IP SAN imported: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOfflineSeedPreservesCorruptIdentityAndDraftForExplicitRecovery(t *testing.T) {
	for _, stage := range []string{"administrator", "sandbox", "draft"} {
		t.Run(stage, func(t *testing.T) {
			i, _ := installerFixture(t, 1)
			infrastructure(t, i)
			if err := i.initializeDatabase(t.Context()); err != nil {
				t.Fatal(err)
			}
			err := i.withStore(t.Context(), false, func(store storage.Store, cfg config.Config) error {
				raw := store.(*unitSeedStore).DB()
				key := "community:identities"
				switch stage {
				case "sandbox":
					key = i.Record.PoolID + ":sandbox"
				case "draft":
					key = i.Record.PoolID + ":nginx-draft"
				}
				if _, err := raw.Exec("UPDATE control_documents SET payload=? WHERE id=?", `{"invalid":`, key); err != nil {
					return err
				}
				original, err := store.GetControlDocument(t.Context(), key)
				if err != nil {
					return err
				}
				ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
				if err != nil {
					return err
				}
				err = SeedStore(t.Context(), store, cfg, ring, i.seedRecord())
				if err == nil {
					return fmt.Errorf("corrupt %s silently overwritten", stage)
				}
				after, err := store.GetControlDocument(t.Context(), key)
				if err != nil {
					return err
				}
				if string(after.Payload) != string(original.Payload) {
					return fmt.Errorf("failed seed changed existing %s evidence", stage)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

type unavailableCatalogStore struct{ storage.Store }

func (s unavailableCatalogStore) ListCatalogSources(context.Context, storage.Page) ([]model.CatalogSource, error) {
	return nil, fmt.Errorf("catalog dependency temporarily unavailable")
}
func TestOfflineSeedCatalogFailureKeepsStableResourcesAndCanResume(t *testing.T) {
	i, _ := installerFixture(t, 1)
	infrastructure(t, i)
	if err := i.withStore(t.Context(), false, func(store storage.Store, cfg config.Config) error {
		if err := store.Migrate(t.Context()); err != nil {
			return err
		}
		ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
		if err != nil {
			return err
		}
		if err = SeedStore(t.Context(), unavailableCatalogStore{store}, cfg, ring, i.seedRecord()); err == nil {
			return fmt.Errorf("unavailable catalog ignored")
		}
		certificate, err := store.GetCertificate(t.Context(), i.Record.CertificateID)
		if err != nil {
			return err
		}
		if certificate.Status != model.CertificateActive {
			return fmt.Errorf("partial initialization lost the existing certificate")
		}
		return SeedStore(t.Context(), store, cfg, ring, i.seedRecord())
	}); err != nil {
		t.Fatal(err)
	}
}
