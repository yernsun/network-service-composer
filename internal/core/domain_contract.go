package core

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
)

const (
	EntryPointHTTP  = "http"
	EntryPointHTTPS = "https"
)

// DomainTLSInput is the public TLS contract shared by Domains, bindings, and
// application installation. Certificate material is always referenced by ID;
// private keys never pass through these APIs.
type DomainTLSInput struct {
	Mode           model.TLSMode           `json:"mode"`
	CertificateID  string                  `json:"certificate_id,omitempty"`
	ACMEChallenge  model.ACMEChallengeType `json:"acme_challenge,omitempty"`
	DNSProviderRef string                  `json:"dns_provider_ref,omitempty"`
}

// DomainConfigInput deliberately separates client entrypoints from upstream
// protocol. HTTP and HTTPS can be enabled independently.
type DomainConfigInput struct {
	HTTP3Enabled        bool           `json:"http3_enabled"`
	Host                string         `json:"host"`
	EntryPoints         []string       `json:"entrypoints"`
	RedirectHTTPToHTTPS bool           `json:"redirect_http_to_https"`
	TLS                 DomainTLSInput `json:"tls"`
}

type RouteBackendInput struct {
	ServiceID  string `json:"service_id,omitempty"`
	Port       int    `json:"port"`
	Protocol   string `json:"protocol"`
	ServerName string `json:"server_name,omitempty"`
}

type RouteConfigInput struct {
	Path             string            `json:"path"`
	Priority         int               `json:"priority,omitempty"`
	StripPath        bool              `json:"strip_path,omitempty"`
	WebsocketEnabled bool              `json:"websocket_enabled,omitempty"`
	Backend          RouteBackendInput `json:"backend"`
}

type DomainConfigPatch struct {
	HTTP3Enabled        *bool           `json:"http3_enabled,omitempty"`
	Host                *string         `json:"host,omitempty"`
	EntryPoints         *[]string       `json:"entrypoints,omitempty"`
	RedirectHTTPToHTTPS *bool           `json:"redirect_http_to_https,omitempty"`
	TLS                 *DomainTLSInput `json:"tls,omitempty"`
	Enabled             *bool           `json:"enabled,omitempty"`
}

type RouteBackendPatch struct {
	ServiceID  *string `json:"service_id,omitempty"`
	Port       *int    `json:"port,omitempty"`
	Protocol   *string `json:"protocol,omitempty"`
	ServerName *string `json:"server_name,omitempty"`
}

type RouteConfigPatch struct {
	Path             *string            `json:"path,omitempty"`
	Priority         *int               `json:"priority,omitempty"`
	StripPath        *bool              `json:"strip_path,omitempty"`
	WebsocketEnabled *bool              `json:"websocket_enabled,omitempty"`
	Enabled          *bool              `json:"enabled,omitempty"`
	Backend          *RouteBackendPatch `json:"backend,omitempty"`
}

func NormalizeEntryPoints(values []string) (bool, bool, error) {
	if len(values) == 0 {
		return false, false, invalid("validation.entrypoints_required", "entrypoints", "at least one of http or https is required")
	}
	var httpEnabled, httpsEnabled bool
	for index, raw := range values {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case EntryPointHTTP:
			if httpEnabled {
				return false, false, invalid("validation.entrypoints_duplicate", "entrypoints", "entrypoints must not contain duplicates")
			}
			httpEnabled = true
		case EntryPointHTTPS:
			if httpsEnabled {
				return false, false, invalid("validation.entrypoints_duplicate", "entrypoints", "entrypoints must not contain duplicates")
			}
			httpsEnabled = true
		default:
			return false, false, invalid("validation.entrypoint_invalid", "entrypoints["+strconv.Itoa(index)+"]", "entrypoints may only contain http and https")
		}
	}
	return httpEnabled, httpsEnabled, nil
}

func (c *Core) buildDomain(ctx context.Context, poolID string, input DomainConfigInput, now time.Time) (model.Domain, *model.Certificate, error) {
	host, err := NormalizeHost(input.Host)
	if err != nil {
		return model.Domain{}, nil, err
	}
	httpEnabled, httpsEnabled, err := NormalizeEntryPoints(input.EntryPoints)
	if err != nil {
		return model.Domain{}, nil, err
	}
	if err = c.validateHTTP3(ctx, poolID, input.HTTP3Enabled, httpsEnabled); err != nil {
		return model.Domain{}, nil, err
	}
	if input.TLS.Mode == "" {
		input.TLS.Mode = model.TLSDisabled
	}
	if input.TLS.Mode != model.TLSDisabled && input.TLS.Mode != model.TLSManaged && input.TLS.Mode != model.TLSCustom {
		return model.Domain{}, nil, invalid("validation.tls_mode_invalid", "tls.mode", "tls.mode must be disabled, managed, or custom")
	}
	if httpsEnabled && input.TLS.Mode == model.TLSDisabled {
		return model.Domain{}, nil, invalid("validation.https_requires_tls", "entrypoints", "the https entrypoint requires managed or custom TLS")
	}
	if !httpsEnabled && input.TLS.Mode != model.TLSDisabled {
		return model.Domain{}, nil, invalid("validation.tls_requires_https", "tls.mode", "managed or custom TLS requires the https entrypoint")
	}
	if input.RedirectHTTPToHTTPS && (!httpEnabled || !httpsEnabled) {
		return model.Domain{}, nil, invalid("validation.redirect_requires_both_entrypoints", "redirect_http_to_https", "HTTP to HTTPS redirect requires both entrypoints")
	}
	if input.TLS.Mode != model.TLSManaged && (input.TLS.ACMEChallenge != "" || strings.TrimSpace(input.TLS.DNSProviderRef) != "") {
		return model.Domain{}, nil, invalid("validation.acme_requires_managed_tls", "tls.acme_challenge", "ACME settings require managed TLS")
	}
	input.TLS.ACMEChallenge, input.TLS.DNSProviderRef, err = c.validateACMESelection(ctx, input.TLS.Mode, input.TLS.ACMEChallenge, input.TLS.DNSProviderRef)
	if err != nil {
		return model.Domain{}, nil, err
	}
	if input.TLS.Mode == model.TLSManaged && input.TLS.CertificateID != "" {
		return model.Domain{}, nil, invalid("validation.managed_certificate_controller_owned", "tls.certificate_id", "managed TLS certificates are provisioned by the controller")
	}
	if input.TLS.Mode == model.TLSDisabled && input.TLS.CertificateID != "" {
		return model.Domain{}, nil, invalid("validation.disabled_tls_certificate", "tls.certificate_id", "disabled TLS cannot reference a certificate")
	}
	if input.TLS.Mode == model.TLSCustom && input.TLS.CertificateID == "" {
		return model.Domain{}, nil, invalid("validation.custom_tls_certificate_required", "tls.certificate_id", "custom TLS requires a certificate_id")
	}

	status := model.DomainReady
	if input.TLS.Mode != model.TLSDisabled {
		status = model.DomainCertPending
	}
	if input.TLS.Mode == model.TLSCustom {
		if _, err = c.validateDomainCertificate(ctx, poolID, host, input.TLS.CertificateID, model.TLSCustom); err != nil {
			return model.Domain{}, nil, err
		}
		status = model.DomainReady
	}
	value := model.Domain{
		ID: id.New(id.Domain), PoolID: poolID, Host: host, TLSMode: input.TLS.Mode,
		ACMEChallenge: input.TLS.ACMEChallenge, DNSProviderRef: input.TLS.DNSProviderRef,
		HTTPSRedirect: input.RedirectHTTPToHTTPS, HTTPEnabled: httpEnabled, HTTPSEnabled: httpsEnabled, HTTP3Enabled: input.HTTP3Enabled,
		Status: status, CertificateID: input.TLS.CertificateID, Source: model.SourceAPI,
		Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if input.TLS.Mode != model.TLSManaged {
		return value, nil, nil
	}
	certificate := model.Certificate{
		ID: id.New(id.Certificate), PoolID: poolID, Provider: "acme",
		ACMEChallenge: value.ACMEChallenge, DNSProviderRef: value.DNSProviderRef,
		Status: model.CertificatePending, PrimaryDomain: value.Host, Domains: []string{value.Host}, Revision: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	value.CertificateID = certificate.ID
	return value, &certificate, nil
}

func entryPoints(httpEnabled, httpsEnabled bool) []string {
	values := make([]string, 0, 2)
	if httpEnabled {
		values = append(values, EntryPointHTTP)
	}
	if httpsEnabled {
		values = append(values, EntryPointHTTPS)
	}
	return values
}
