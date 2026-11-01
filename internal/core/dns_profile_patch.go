package core

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/dnsprofile"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// DNSProfilePatch retains omitted secrets. JSON null explicitly removes a field;
// Hurricane tokens are merged by their exact provider domain key.
type DNSProfilePatch struct {
	Region      json.RawMessage `json:"region,omitempty"`
	Credentials json.RawMessage `json:"credentials,omitempty"`
}

type DNSProfileDetail struct {
	DNSProfile
	CredentialFields []string `json:"credential_fields"`
	TokenDomains     []string `json:"token_domains"`
}

func dnsCredentialFields(provider string) []string {
	switch provider {
	case "cloudflare":
		return []string{"api_token", "zone_api_token", "email", "api_key"}
	case "alidns":
		return []string{"access_key", "secret_key", "security_token", "ram_role"}
	case "tencentcloud":
		return []string{"secret_id", "secret_key", "session_token"}
	case "hurricane":
		return []string{"tokens"}
	case "ovh":
		return []string{"application_key", "application_secret", "consumer_key"}
	}
	return nil
}

// DNSProfileEditDetail is used only by the administrator detail endpoint. The
// ordinary profile collection remains minimal and contains no domain keys.
func (c *Core) DNSProfileEditDetail(ctx context.Context, reference string) (DNSProfileDetail, error) {
	result := DNSProfileDetail{CredentialFields: []string{}, TokenDomains: []string{}}
	if profile, configured := c.config.ACME.DNSProviders[reference]; configured {
		result.DNSProfile = DNSProfile{Reference: reference, Provider: profile.Provider, Region: profile.Region, Source: "config", Configured: true}
		return result, nil
	}
	stored, doc, err := dnsprofile.Read(ctx, c.store, c.keyring, reference)
	if err != nil {
		return result, err
	}
	// Derive metadata and the concurrency revision from the same stored value.
	result.DNSProfile = DNSProfile{Reference: reference, Provider: stored.Provider, Region: stored.Region, Source: "database", Configured: true, Revision: doc.Revision}
	fields, _ := json.Marshal(stored.Credentials)
	var present map[string]json.RawMessage
	_ = json.Unmarshal(fields, &present)
	for _, key := range dnsCredentialFields(stored.Provider) {
		if _, ok := present[key]; ok {
			result.CredentialFields = append(result.CredentialFields, key)
		}
	}
	for domain := range stored.Credentials.Tokens {
		result.TokenDomains = append(result.TokenDomains, domain)
	}
	sort.Strings(result.TokenDomains)
	return result, nil
}

func (c *Core) PatchDNSProfile(ctx context.Context, reference string, patch DNSProfilePatch, expected int64) (DNSProfile, error) {
	if _, configured := c.config.ACME.DNSProviders[reference]; configured {
		return DNSProfile{}, storage.ErrConflict
	}
	stored, doc, err := dnsprofile.Read(ctx, c.store, c.keyring, reference)
	if err != nil {
		return DNSProfile{}, err
	}
	if expected <= 0 || doc.Revision != expected {
		return DNSProfile{}, storage.ErrPrecondition
	}
	if len(patch.Region) == 0 && len(patch.Credentials) == 0 {
		return DNSProfile{}, controlInvalid("credentials", "at least one profile field must be supplied")
	}
	if len(patch.Region) > 0 {
		if stored.Provider != "ovh" || json.Unmarshal(patch.Region, &stored.Region) != nil {
			return DNSProfile{}, controlInvalid("region", "region is only supported for OVH and must be a string or null")
		}
		if strings.TrimSpace(string(patch.Region)) == "null" {
			stored.Region = ""
		}
	}
	if len(patch.Credentials) > 0 {
		if err = mergeDNSCredentials(&stored, patch.Credentials); err != nil {
			return DNSProfile{}, err
		}
	}
	return c.SaveDNSProfile(ctx, reference, DNSProfileInput{Reference: reference, Provider: stored.Provider, Region: stored.Region, Credentials: stored.Credentials}, expected)
}

func mergeDNSCredentials(stored *dnsprofile.Stored, raw json.RawMessage) error {
	var changes map[string]json.RawMessage
	if json.Unmarshal(raw, &changes) != nil || len(changes) == 0 {
		return controlInvalid("credentials", "credentials must be a non-empty field patch")
	}
	encoded, _ := json.Marshal(stored.Credentials)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &fields)
	allowed := map[string]bool{}
	for _, key := range dnsCredentialFields(stored.Provider) {
		allowed[key] = true
	}
	for key, value := range changes {
		if !allowed[key] {
			return controlInvalid("credentials", "credential field is not supported by this provider")
		}
		if key == "tokens" {
			if err := mergeDNSTokens(stored, value); err != nil {
				return err
			}
			fields[key], _ = json.Marshal(stored.Credentials.Tokens)
			continue
		}
		if strings.TrimSpace(string(value)) == "null" {
			delete(fields, key)
			continue
		}
		var text string
		if json.Unmarshal(value, &text) != nil || strings.TrimSpace(text) == "" {
			return controlInvalid("credentials/"+key, "credential must be a non-empty string or null")
		}
		fields[key] = value
	}
	encoded, _ = json.Marshal(fields)
	stored.Credentials = config.DNSProviderCredentials{}
	return json.Unmarshal(encoded, &stored.Credentials)
}

func mergeDNSTokens(stored *dnsprofile.Stored, raw json.RawMessage) error {
	var changes map[string]*string
	if json.Unmarshal(raw, &changes) != nil || len(changes) == 0 {
		return controlInvalid("credentials/tokens", "tokens must be a non-empty domain patch")
	}
	for domain, token := range changes {
		// Never put a provider token or an arbitrary map key in diagnostics.
		if strings.TrimSpace(domain) == "" || token != nil && strings.TrimSpace(*token) == "" {
			return controlInvalid("credentials/tokens", "domain keys and replacement tokens must be non-empty")
		}
		if token == nil {
			delete(stored.Credentials.Tokens, domain)
		} else {
			if stored.Credentials.Tokens == nil {
				stored.Credentials.Tokens = map[string]string{}
			}
			stored.Credentials.Tokens[domain] = *token
		}
	}
	return nil
}
