package install

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestInitializationNeverResetsAdministratorCredentials(t *testing.T) {
	i, _ := installerFixture(t, 1)
	infrastructure(t, i)
	if err := i.initializeDatabase(t.Context()); err != nil {
		t.Fatal(err)
	}
	var identity []byte
	if err := i.withStore(t.Context(), false, func(store storage.Store, cfg config.Config) error {
		ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
		if err != nil {
			return err
		}
		c := core.New(store, cfg, ring)
		if _, err = c.ChangePassword(t.Context(), "user_bootstrap", cfg.Security.AdminToken, "isolated-replacement-password-verify-retention"); err != nil {
			return err
		}
		d, err := store.GetControlDocument(t.Context(), "community:identities")
		identity = append([]byte(nil), d.Payload...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := i.initializeDatabase(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := i.withStore(t.Context(), true, func(store storage.Store, _ config.Config) error {
		d, err := store.GetControlDocument(t.Context(), "community:identities")
		if err != nil {
			return err
		}
		if string(d.Payload) != string(identity) {
			return fmt.Errorf("administrator credentials changed during initialization retry")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInitializationRefusesConflictingDatabaseOwnership(t *testing.T) {
	for _, scenario := range []string{"installation", "invalid-record", "pool-disabled", "runtime-cluster", "certificate", "sandbox"} {
		t.Run(scenario, func(t *testing.T) {
			i, _ := installerFixture(t, 1)
			infrastructure(t, i)
			if err := i.initializeDatabase(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := i.withStore(t.Context(), false, func(store storage.Store, cfg config.Config) error {
				update := func(key string, change func(*storage.ControlDocument)) error {
					d, err := store.GetControlDocument(t.Context(), key)
					if err != nil {
						return err
					}
					expected := d.Revision
					change(&d)
					return store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: d, Expected: expected}})
				}
				switch scenario {
				case "installation":
					return update(bootstrap.InstallationDocumentID, func(d *storage.ControlDocument) {
						var r bootstrap.Installation
						json.Unmarshal(d.Payload, &r)
						r.ID = "foreign-installation"
						d.Payload, _ = json.Marshal(r)
					})
				case "invalid-record":
					return update(bootstrap.InstallationDocumentID, func(d *storage.ControlDocument) { d.Payload = json.RawMessage(`{"id":true}`) })
				case "pool-disabled":
					p, err := store.GetPool(t.Context(), i.Record.PoolID)
					if err != nil {
						return err
					}
					p.Enabled = false
					return store.UpdatePool(t.Context(), &p, p.Revision)
				case "runtime-cluster":
					return update("fixture-runtime:"+i.Record.RuntimeID, func(d *storage.ControlDocument) {
						var r map[string]any
						json.Unmarshal(d.Payload, &r)
						r["cluster_id"] = "foreign-cluster"
						d.Payload, _ = json.Marshal(r)
					})
				case "certificate":
					c, err := store.GetCertificate(t.Context(), i.Record.CertificateID)
					if err != nil {
						return err
					}
					c.Fingerprint = "external-fingerprint"
					return store.UpdateCertificate(t.Context(), &c, c.Revision)
				case "sandbox":
					return update(i.Record.PoolID+":sandbox", func(d *storage.ControlDocument) {
						d.Payload = json.RawMessage(`{"gateway_id":"external-sandbox","checks":[]}`)
					})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := i.initializeDatabase(t.Context()); err == nil {
				t.Fatal("conflicting ownership was silently overwritten")
			}
		})
	}
}

type failingStore struct {
	storage.Store
	fail string
}

func (s failingStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if s.fail == key {
		return storage.ControlDocument{}, fmt.Errorf("injected unavailable authority")
	}
	return s.Store.GetControlDocument(ctx, key)
}

func TestInitializationRequiresValidAuthorityAndRetainsFailedEvidence(t *testing.T) {
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
		invalid := i.seedRecord()
		invalid.Database.Volume = ""
		if err = SeedStore(t.Context(), store, cfg, ring, invalid); err == nil {
			return fmt.Errorf("missing volume authority accepted")
		}
		cfg.Storage.AutoMigrate = true
		if err = SeedStore(t.Context(), store, cfg, ring, i.seedRecord()); err == nil {
			return fmt.Errorf("automatic Controller migration accepted")
		}
		cfg.Storage.AutoMigrate = false
		if err = SeedStore(t.Context(), failingStore{Store: store, fail: bootstrap.InstallationDocumentID}, cfg, ring, i.seedRecord()); err == nil {
			return fmt.Errorf("unavailable ownership record treated as empty")
		}
		if err = core.New(store, cfg, ring).BootstrapIdentity(t.Context()); err != nil {
			return err
		}
		if err = SeedStore(t.Context(), store, cfg, ring, i.seedRecord()); err == nil {
			return fmt.Errorf("pre-existing administrators claimed by fresh initialization")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEveryPersistedOwnershipBindingRemainsImmutableDuringSeedRetry(t *testing.T) {
	i, _ := installerFixture(t, 3)
	infrastructure(t, i)
	if err := i.initializeDatabase(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := i.withStore(t.Context(), false, func(store storage.Store, cfg config.Config) error {
		ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
		if err != nil {
			return err
		}
		original, err := store.GetControlDocument(t.Context(), bootstrap.InstallationDocumentID)
		if err != nil {
			return err
		}
		raw := store.(*unitSeedStore).DB()
		for _, field := range []string{"kind", "cluster", "configuration", "pool", "runtime", "volume"} {
			var value bootstrap.Installation
			json.Unmarshal(original.Payload, &value)
			kind := original.Kind
			switch field {
			case "kind":
				kind = "foreign"
			case "cluster":
				value.ClusterID = "foreign"
			case "configuration":
				value.ConfigDigest = "foreign"
			case "pool":
				value.PoolID = "foreign"
			case "runtime":
				value.RuntimeID = "foreign"
			case "volume":
				value.Database.Volume = "foreign"
			}
			encoded, _ := json.Marshal(value)
			if _, err = raw.Exec("UPDATE control_documents SET kind=?,payload=? WHERE id=?", kind, string(encoded), bootstrap.InstallationDocumentID); err != nil {
				return err
			}
			if err = SeedStore(t.Context(), store, cfg, ring, i.seedRecord()); err == nil {
				return fmt.Errorf("changed installation authority accepted: %s", field)
			}
			if _, err = raw.Exec("UPDATE control_documents SET kind=?,payload=? WHERE id=?", original.Kind, string(original.Payload), bootstrap.InstallationDocumentID); err != nil {
				return err
			}
		}
		rt, err := store.GetControlDocument(t.Context(), "fixture-runtime:"+i.Record.RuntimeID)
		if err != nil {
			return err
		}
		for _, field := range []string{"pool_id", "endpoint", "runtime_type"} {
			var value map[string]any
			json.Unmarshal(rt.Payload, &value)
			value[field] = "foreign"
			encoded, _ := json.Marshal(value)
			if _, err = raw.Exec("UPDATE control_documents SET payload=? WHERE id=?", string(encoded), rt.ID); err != nil {
				return err
			}
			if err = SeedStore(t.Context(), store, cfg, ring, i.seedRecord()); err == nil {
				return fmt.Errorf("changed Runtime binding accepted: %s", field)
			}
			if _, err = raw.Exec("UPDATE control_documents SET payload=? WHERE id=?", string(rt.Payload), rt.ID); err != nil {
				return err
			}
		}
		pool, err := store.GetPool(t.Context(), i.Record.PoolID)
		if err != nil {
			return err
		}
		pool.ChallengeUpstream = "https://foreign.example.invalid"
		if err = store.UpdatePool(t.Context(), &pool, pool.Revision); err != nil {
			return err
		}
		if err = SeedStore(t.Context(), store, cfg, ring, i.seedRecord()); err == nil {
			return fmt.Errorf("foreign Pool upstream overwritten")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
