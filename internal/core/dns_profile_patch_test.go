package core

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/dnsprofile"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func dnsPatch(t *testing.T, raw string) DNSProfilePatch {
	t.Helper()
	var p DNSProfilePatch
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestDNSFieldPatchRetainsSecretsAndRejectsStaleWriters(t *testing.T) {
	c, _, _ := controlFixture(t)
	ctx := context.Background()
	input := DNSProfileInput{Reference: "partial", Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: "fixture-original", ZoneAPIToken: "fixture-zone"}}
	initial, err := c.SaveDNSProfile(ctx, input.Reference, input, 0)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := c.DNSProfileEditDetail(ctx, input.Reference)
	if err != nil || !reflect.DeepEqual(detail.CredentialFields, []string{"api_token", "zone_api_token"}) || len(detail.TokenDomains) != 0 {
		t.Fatal("missing editing metadata", err)
	}
	updated, err := c.PatchDNSProfile(ctx, input.Reference, dnsPatch(t, `{"credentials":{"api_token":"fixture-new"}}`), initial.Revision)
	if err != nil || updated.Revision != initial.Revision+1 {
		t.Fatal("partial rotation failed", err)
	}
	resolved, err := c.resolveDNSProfile(ctx, input.Reference)
	if err != nil || resolved.Credentials.APIToken != "fixture-new" || resolved.Credentials.ZoneAPIToken != "fixture-zone" {
		t.Fatal("omitted credential changed", err)
	}
	before, _ := c.store.GetControlDocument(ctx, dnsprofile.Key(input.Reference))
	if _, err = c.PatchDNSProfile(ctx, input.Reference, dnsPatch(t, `{"credentials":{"zone_api_token":"stale-writer"}}`), initial.Revision); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatal("stale write accepted", err)
	}
	after, _ := c.store.GetControlDocument(ctx, dnsprofile.Key(input.Reference))
	if string(after.Payload) != string(before.Payload) {
		t.Fatal("stale write mutated the profile")
	}
	updated, err = c.PatchDNSProfile(ctx, input.Reference, dnsPatch(t, `{"credentials":{"zone_api_token":null}}`), updated.Revision)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ = c.resolveDNSProfile(ctx, input.Reference)
	if resolved.Credentials.ZoneAPIToken != "" || resolved.Credentials.APIToken != "fixture-new" {
		t.Fatal("clear operation did not preserve required credential")
	}
	detail, err = c.DNSProfileEditDetail(ctx, input.Reference)
	if err != nil || !reflect.DeepEqual(detail.CredentialFields, []string{"api_token"}) {
		t.Fatal("cleared field still configured", err)
	}
	metadata, _ := json.Marshal(detail)
	doc, _ := c.store.GetControlDocument(ctx, dnsprofile.Key(input.Reference))
	for _, raw := range []string{string(metadata), string(doc.Payload)} {
		if strings.Contains(raw, "fixture-new") || strings.Contains(raw, "fixture-zone") {
			t.Fatal("credential leaked")
		}
	}
}
func TestDNSHurricanePatchMergesDomainTokensAtomically(t *testing.T) {
	c, _, _ := controlFixture(t)
	ctx := context.Background()
	p, err := c.SaveDNSProfile(ctx, "he", DNSProfileInput{Reference: "he", Provider: "hurricane", Credentials: config.DNSProviderCredentials{Tokens: map[string]string{"a.example.test": "fixture-a", "b.example.test": "fixture-b"}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.DNSProfileEditDetail(ctx, "he")
	if err != nil || !reflect.DeepEqual(d.TokenDomains, []string{"a.example.test", "b.example.test"}) {
		t.Fatal("domain metadata missing", err)
	}
	p, err = c.PatchDNSProfile(ctx, "he", dnsPatch(t, `{"credentials":{"tokens":{"a.example.test":"fixture-a2","c.example.test":"fixture-c"}}}`), p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := c.resolveDNSProfile(ctx, "he")
	if !reflect.DeepEqual(r.Credentials.Tokens, map[string]string{"a.example.test": "fixture-a2", "b.example.test": "fixture-b", "c.example.test": "fixture-c"}) {
		t.Fatal("unrelated domain token was lost")
	}
	p, err = c.PatchDNSProfile(ctx, "he", dnsPatch(t, `{"credentials":{"tokens":{"a.example.test":null,"missing.example.test":null}}}`), p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := c.store.GetControlDocument(ctx, dnsprofile.Key("he"))
	if _, err = c.PatchDNSProfile(ctx, "he", dnsPatch(t, `{"credentials":{"tokens":{"b.example.test":null,"c.example.test":null}}}`), p.Revision); err == nil {
		t.Fatal("empty Hurricane credentials accepted")
	}
	after, _ := c.store.GetControlDocument(ctx, dnsprofile.Key("he"))
	if string(after.Payload) != string(before.Payload) {
		t.Fatal("invalid merge changed persistent tokens")
	}
}
func TestDNSPatchProviderAuthenticationAlternatives(t *testing.T) {
	for _, tc := range []struct {
		name, provider, patch string
		credentials           config.DNSProviderCredentials
	}{
		{"cloudflare", "cloudflare", `{"credentials":{"api_token":null,"email":"admin@example.test","api_key":"fixture-key"}}`, config.DNSProviderCredentials{APIToken: "fixture-token"}},
		{"alidns", "alidns", `{"credentials":{"ram_role":"fixture-role","access_key":null,"secret_key":null}}`, config.DNSProviderCredentials{AccessKey: "fixture-id", SecretKey: "fixture-secret", SecurityToken: "fixture-security"}},
		{"tencent", "tencentcloud", `{"credentials":{"session_token":"fixture-session"}}`, config.DNSProviderCredentials{SecretID: "fixture-id", SecretKey: "fixture-secret"}},
		{"ovh", "ovh", `{"region":"ovh-ca","credentials":{"consumer_key":"fixture-replacement"}}`, config.DNSProviderCredentials{ApplicationKey: "fixture-id", ApplicationSecret: "fixture-secret", ConsumerKey: "fixture-consumer"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := controlFixture(t)
			ctx := context.Background()
			p, err := c.SaveDNSProfile(ctx, tc.name, DNSProfileInput{Reference: tc.name, Provider: tc.provider, Credentials: tc.credentials}, 0)
			if err != nil {
				t.Fatal(err)
			}
			p, err = c.PatchDNSProfile(ctx, tc.name, dnsPatch(t, tc.patch), p.Revision)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := c.resolveDNSProfile(ctx, tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if tc.provider == "cloudflare" && (resolved.Credentials.APIToken != "" || resolved.Credentials.Email != "admin@example.test") {
				t.Fatal("authentication mode switch failed")
			}
			if tc.provider == "alidns" && (resolved.Credentials.AccessKey != "" || resolved.Credentials.RAMRole != "fixture-role" || resolved.Credentials.SecurityToken != "fixture-security") {
				t.Fatal("role switch lost optional value")
			}
			if tc.provider == "ovh" {
				if resolved.Region != "ovh-ca" {
					t.Fatal("endpoint change lost")
				}
				if _, err = c.PatchDNSProfile(ctx, tc.name, dnsPatch(t, `{"region":null}`), p.Revision); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
func TestDNSInvalidPatchesPreserveTheStoredProfile(t *testing.T) {
	for _, raw := range []string{`{}`, `{"region":"ovh-ca"}`, `{"credentials":null}`, `{"credentials":[]}`, `{"credentials":{}}`, `{"credentials":{"unknown":"never-echo-this"}}`, `{"credentials":{"secret_key":"never-echo-this"}}`, `{"credentials":{"api_token":""}}`, `{"credentials":{"api_token":"  "}}`, `{"credentials":{"api_token":123}}`, `{"credentials":{"api_token":null}}`} {
		t.Run(raw, func(t *testing.T) {
			c, _, _ := controlFixture(t)
			ctx := context.Background()
			p, err := c.SaveDNSProfile(ctx, "invalid", DNSProfileInput{Reference: "invalid", Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: "fixture-token"}}, 0)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := c.store.GetControlDocument(ctx, dnsprofile.Key(p.Reference))
			_, err = c.PatchDNSProfile(ctx, p.Reference, dnsPatch(t, raw), p.Revision)
			if err == nil || strings.Contains(err.Error(), "never-echo-this") {
				t.Fatal("invalid or confidential patch accepted", err)
			}
			after, _ := c.store.GetControlDocument(ctx, dnsprofile.Key(p.Reference))
			if string(after.Payload) != string(before.Payload) {
				t.Fatal("failed patch was partially stored")
			}
		})
	}
	for _, raw := range []string{`{"credentials":{"tokens":null}}`, `{"credentials":{"tokens":{}}}`, `{"credentials":{"tokens":{"":"fixture"}}}`, `{"credentials":{"tokens":{"example.test":" "}}}`, `{"credentials":{"tokens":{"example.test":12}}}`} {
		t.Run(raw, func(t *testing.T) {
			c, _, _ := controlFixture(t)
			ctx := context.Background()
			p, err := c.SaveDNSProfile(ctx, "invalid-he", DNSProfileInput{Reference: "invalid-he", Provider: "hurricane", Credentials: config.DNSProviderCredentials{Tokens: map[string]string{"example.test": "fixture-token"}}}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.PatchDNSProfile(ctx, p.Reference, dnsPatch(t, raw), p.Revision); err == nil {
				t.Fatal("invalid token patch accepted")
			}
		})
	}
}
func TestDNSPatchCannotEditConfigurationOrCreateMissingProfile(t *testing.T) {
	c, _, _ := controlFixture(t)
	ctx := context.Background()
	c.config.ACME.DNSProviders["file"] = config.DNSProviderProfile{Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: "fixture-file"}}
	patch := dnsPatch(t, `{"credentials":{"api_token":"fixture-new"}}`)
	if _, err := c.PatchDNSProfile(ctx, "file", patch, 1); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("file-backed profile changed", err)
	}
	d, err := c.DNSProfileEditDetail(ctx, "file")
	if err != nil || d.Source != "config" || len(d.CredentialFields) != 0 {
		t.Fatal("file metadata was exposed", err)
	}
	if _, err = c.PatchDNSProfile(ctx, "missing", patch, 1); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("patch created a profile", err)
	}
	if _, err = c.DNSProfileEditDetail(ctx, "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
}
