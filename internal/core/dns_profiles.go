package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/dnsprofile"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type DNSProfileInput struct {
	Reference   string                        `json:"reference"`
	Provider    string                        `json:"provider"`
	Region      string                        `json:"region,omitempty"`
	Credentials config.DNSProviderCredentials `json:"credentials"`
}

type DNSProfile struct {
	Reference  string `json:"reference"`
	Provider   string `json:"provider"`
	Region     string `json:"region,omitempty"`
	Source     string `json:"source"`
	Configured bool   `json:"configured"`
	Revision   int64  `json:"revision"`
}

func (c *Core) GetDNSProfile(ctx context.Context, reference string) (DNSProfile, error) {
	if value, ok := c.config.ACME.DNSProviders[reference]; ok {
		return DNSProfile{Reference: reference, Provider: value.Provider, Region: value.Region, Source: "config", Configured: true}, nil
	}
	stored, doc, err := dnsprofile.Read(ctx, c.store, c.keyring, reference)
	if err != nil {
		return DNSProfile{}, err
	}
	return DNSProfile{Reference: reference, Provider: stored.Provider, Region: stored.Region, Source: "database", Configured: true, Revision: doc.Revision}, nil
}

func (c *Core) ListDNSProfiles(ctx context.Context) ([]DNSProfile, error) {
	values := make([]DNSProfile, 0, len(c.config.ACME.DNSProviders))
	for reference, profile := range c.config.ACME.DNSProviders {
		values = append(values, DNSProfile{Reference: reference, Provider: profile.Provider, Region: profile.Region, Source: "config", Configured: true})
	}
	page := storage.Page{Limit: 200}
	for {
		docs, err := c.store.ListControlDocuments(ctx, dnsprofile.Kind, "", page)
		if err != nil {
			return nil, err
		}
		for _, doc := range docs {
			reference := strings.TrimPrefix(doc.ID, dnsprofile.Kind+":")
			if _, configured := c.config.ACME.DNSProviders[reference]; configured {
				continue
			}
			value, err := c.GetDNSProfile(ctx, reference)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		if len(docs) < page.Limit {
			return values, nil
		}
		page.After = docs[len(docs)-1].ID
	}
}

func (c *Core) SaveDNSProfile(ctx context.Context, reference string, input DNSProfileInput, expected int64) (DNSProfile, error) {
	if reference != input.Reference || reference == "" {
		return DNSProfile{}, invalid("validation.dns_ref_unconfigured", "reference", "reference must match the profile URL")
	}
	if _, configured := c.config.ACME.DNSProviders[reference]; configured {
		return DNSProfile{}, storage.ErrConflict
	}
	profile := config.DNSProviderProfile{Provider: input.Provider, Region: input.Region, Credentials: input.Credentials}
	if err := config.ValidateDNSProviderCredentials(reference, profile); err != nil {
		return DNSProfile{}, invalid("validation.dns_ref_unconfigured", "credentials", err.Error())
	}
	payload, err := dnsprofile.Encrypt(c.keyring, reference, dnsprofile.Stored{Provider: input.Provider, Region: input.Region, Credentials: input.Credentials})
	if err != nil {
		return DNSProfile{}, err
	}
	now := c.now()
	write := storage.ControlWrite{Document: storage.ControlDocument{ID: dnsprofile.Key(reference), Kind: dnsprofile.Kind, Payload: payload, CreatedAt: now, UpdatedAt: now}, Expected: expected}
	if err = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{write}); err != nil {
		return DNSProfile{}, err
	}
	return c.GetDNSProfile(ctx, reference)
}

func (c *Core) DeleteDNSProfile(ctx context.Context, reference string, expected int64) error {
	if _, configured := c.config.ACME.DNSProviders[reference]; configured {
		return storage.ErrConflict
	}
	_, doc, err := dnsprofile.Read(ctx, c.store, c.keyring, reference)
	if err != nil {
		return err
	}
	if expected != doc.Revision {
		return storage.ErrPrecondition
	}
	page := storage.Page{Limit: 200}
	for {
		domains, err := c.store.ListDomains(ctx, page)
		if err != nil {
			return err
		}
		for _, domain := range domains {
			if domain.DNSProviderRef == reference {
				return storage.ErrConflict
			}
		}
		if len(domains) < page.Limit {
			break
		}
		page.After = domains[len(domains)-1].ID
	}
	page = storage.Page{Limit: 200}
	for {
		certificates, err := c.store.ListCertificates(ctx, page)
		if err != nil {
			return err
		}
		for _, certificate := range certificates {
			if certificate.DNSProviderRef == reference {
				return storage.ErrConflict
			}
		}
		if len(certificates) < page.Limit {
			break
		}
		page.After = certificates[len(certificates)-1].ID
	}
	page = storage.Page{Limit: 200}
	for {
		drafts, err := c.store.ListControlDocuments(ctx, "domain-draft", "", page)
		if err != nil {
			return err
		}
		for _, doc := range drafts {
			var draft DomainDraft
			if err = json.Unmarshal(doc.Payload, &draft); err != nil {
				return err
			}
			if draft.Status == "draft" && draft.Domain.TLS.DNSProviderRef == reference {
				return storage.ErrConflict
			}
		}
		if len(drafts) < page.Limit {
			break
		}
		page.After = drafts[len(drafts)-1].ID
	}
	return c.store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: doc, Expected: expected, Remove: true}})
}

func (c *Core) resolveDNSProfile(ctx context.Context, reference string) (config.DNSProviderProfile, error) {
	if profile, configured := c.config.ACME.DNSProviders[reference]; configured {
		return profile, nil
	}
	if c.store == nil || c.keyring == nil {
		return config.DNSProviderProfile{}, invalidParams("validation.dns_ref_unconfigured", "tls.dns_provider_ref", "dns_provider_ref is not configured", map[string]any{"reference": reference})
	}
	profile, err := dnsprofile.Resolve(ctx, c.store, c.keyring, c.config, reference)
	if errors.Is(err, storage.ErrNotFound) {
		return profile, invalidParams("validation.dns_ref_unconfigured", "tls.dns_provider_ref", "dns_provider_ref is not configured", map[string]any{"reference": reference})
	}
	return profile, err
}
