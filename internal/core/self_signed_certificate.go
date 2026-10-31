package core

import (
	"context"
	"net"
	"net/netip"
	"strings"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/pki"
)

type SelfSignedCertificateInput struct {
	PoolID        string   `json:"pool_id"`
	Domains       []string `json:"domains"`
	IPAddresses   []string `json:"ip_addresses,omitempty"`
	DomainIDs     []string `json:"domain_ids,omitempty"`
	ValidityYears *int     `json:"validity_years,omitempty"`
}

// Self-signed assets use custom TLS binding and never enter the ACME worker.
func (c *Core) CreateSelfSignedCertificate(ctx context.Context, input SelfSignedCertificateInput) (model.Certificate, error) {
	years := 20
	if input.ValidityYears != nil {
		years = *input.ValidityYears
	}
	if years < 1 || years > 100 || len(input.Domains)+len(input.IPAddresses) < 1 || len(input.Domains)+len(input.IPAddresses) > 100 {
		return model.Certificate{}, invalid("validation.self_signed_invalid", "domains", "provide 1–100 exact DNS names or IP SANs and a validity of 1–100 years")
	}
	if _, err := c.store.GetPool(ctx, input.PoolID); err != nil {
		return model.Certificate{}, err
	}
	names := make([]string, 0, len(input.Domains))
	seen := map[string]bool{}
	for _, name := range input.Domains {
		host, err := NormalizeHost(name)
		if err != nil {
			return model.Certificate{}, invalid("validation.self_signed_invalid", "domains", "provide exact DNS names")
		}
		if !seen[host] {
			names = append(names, host)
			seen[host] = true
		}
	}

	addresses := []net.IP{}
	for _, value := range input.IPAddresses {
		address, err := netip.ParseAddr(strings.TrimSpace(value))
		if err != nil || address.Zone() != "" || address.IsUnspecified() || address.IsMulticast() {
			return model.Certificate{}, invalid("validation.self_signed_invalid", "ip_addresses", "provide exact IP addresses without a zone")
		}
		address = address.Unmap()
		canonical := address.String()
		if !seen[canonical] {
			addresses = append(addresses, net.ParseIP(canonical))
			seen[canonical] = true
		}
	}
	certificate, key, err := pki.SelfSigned(names, addresses, years, c.now())
	if err != nil {
		return model.Certificate{}, err
	}
	defer clear(key)
	return c.installCertificate(ctx, CustomCertificateInput{PoolID: input.PoolID, DomainIDs: input.DomainIDs, CertificatePEM: string(certificate), PrivateKeyPEM: string(key)}, "self-signed")
}
