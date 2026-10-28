package core

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/dnsprofile"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestConfiguredDNSProfileShadowsDatabaseWithoutDestroyingCredentials(t *testing.T) {
	c, _, _ := controlFixture(t)
	ctx := context.Background()
	const reference = "shared-dns"
	input := DNSProfileInput{Reference: reference, Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: "stored-token"}}
	stored, err := c.SaveDNSProfile(ctx, reference, input, 0)
	if err != nil {
		t.Fatal(err)
	}
	before, err := c.store.GetControlDocument(ctx, dnsprofile.Key(reference))
	if err != nil {
		t.Fatal(err)
	}
	c.config.ACME.DNSProviders[reference] = config.DNSProviderProfile{Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: "file-token"}}
	metadata, err := c.GetDNSProfile(ctx, reference)
	if err != nil || metadata.Source != "config" || metadata.Revision != 0 {
		t.Fatalf("configured profile was not selected: %+v %v", metadata, err)
	}
	listed, err := c.ListDNSProfiles(ctx)
	if err != nil || len(listed) != 1 || listed[0] != metadata {
		t.Fatalf("shadowed profile was duplicated: %+v %v", listed, err)
	}
	resolved, err := c.resolveDNSProfile(ctx, reference)
	if err != nil || resolved.Credentials.APIToken != "file-token" {
		t.Fatalf("ACME did not select configured credentials: %v", err)
	}
	if _, err = c.SaveDNSProfile(ctx, reference, input, stored.Revision); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("configured credentials could be overwritten: %v", err)
	}
	if err = c.DeleteDNSProfile(ctx, reference, stored.Revision); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("configured profile could be deleted: %v", err)
	}
	delete(c.config.ACME.DNSProviders, reference)
	metadata, err = c.GetDNSProfile(ctx, reference)
	if err != nil || metadata != stored {
		t.Fatalf("database profile was lost after removing configuration: %+v %v", metadata, err)
	}
	resolved, err = c.resolveDNSProfile(ctx, reference)
	if err != nil || resolved.Credentials.APIToken != input.Credentials.APIToken {
		t.Fatalf("original database credentials were not preserved: %v", err)
	}
	after, err := c.store.GetControlDocument(ctx, dnsprofile.Key(reference))
	if err != nil || after.Revision != before.Revision || string(after.Payload) != string(before.Payload) {
		t.Fatalf("read-only precedence changed the database profile: %v", err)
	}
}

func TestDNSProfileKeyRotationPreservesRevisionAndNeedsMatchingBackupKey(t *testing.T) {
	c, _, _ := controlFixture(t)
	ctx := context.Background()
	const reference = "recoverable-dns"
	const secret = "recoverable-provider-token"
	profile, err := c.SaveDNSProfile(ctx, reference, DNSProfileInput{Reference: reference, Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: secret}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := c.store.GetControlDocument(ctx, dnsprofile.Key(reference))
	if err != nil {
		t.Fatal(err)
	}
	oldKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	newKey := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("n", 32)))
	c.keyring, err = security.NewKeyring("v2", map[string]string{"v1": oldKey, "v2": newKey})
	if err != nil {
		t.Fatal(err)
	}
	count, err := c.ReencryptControlSecrets(ctx)
	if err != nil || count != 1 {
		t.Fatalf("DNS profile was omitted from rotation: %d %v", count, err)
	}
	rotated, err := c.store.GetControlDocument(ctx, dnsprofile.Key(reference))
	if err != nil || rotated.Revision != backup.Revision || !rotated.CreatedAt.Equal(backup.CreatedAt) || !rotated.UpdatedAt.Equal(backup.UpdatedAt) || strings.Contains(string(rotated.Payload), secret) {
		t.Fatalf("rotation changed metadata or exposed a credential: %v", err)
	}
	c.keyring, err = security.NewKeyring("v2", map[string]string{"v2": newKey})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := c.GetDNSProfile(ctx, reference)
	if err != nil || metadata != profile {
		t.Fatalf("rotated profile lost its public revision: %+v %v", metadata, err)
	}
	resolved, err := c.resolveDNSProfile(ctx, reference)
	if err != nil || resolved.Credentials.APIToken != secret {
		t.Fatalf("rotated credential needs the retired live key: %v", err)
	}
	// A retained database archive still contains the old envelope. Rotation
	// must not turn a restore with only the new live key into empty credentials.
	backupStore := &dnsProfileBackupStore{Store: c.store, document: backup}
	if _, _, err = dnsprofile.Read(ctx, backupStore, c.keyring, reference); err == nil {
		t.Fatal("old backup decrypted without its matching key")
	}
	backupRing, err := security.NewKeyring("v1", map[string]string{"v1": oldKey})
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := dnsprofile.Read(ctx, backupStore, backupRing, reference)
	if err != nil || restored.Credentials.APIToken != secret {
		t.Fatalf("matching archived key did not recover the DNS credential: %v", err)
	}
}

type dnsProfileBackupStore struct {
	storage.Store
	document storage.ControlDocument
}

func (s *dnsProfileBackupStore) GetControlDocument(context.Context, string) (storage.ControlDocument, error) {
	return s.document, nil
}
