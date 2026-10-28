package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/net/idna"

	"github.com/yernsun/network-service-composer/internal/cluster"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/projectlife"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type Core struct {
	HAController   func(context.Context) HAEvidence
	HAReports      *nodecontrol.Reports
	catalogMu      sync.Mutex
	catalogSyncs   map[string]*catalogSyncCall
	Cluster        *cluster.Monitor
	ProjectRuntime projectlife.Runtime
	store          storage.Store
	config         config.Config
	keyring        *security.Keyring
	now            func() time.Time
}

func New(store storage.Store, cfg config.Config, keyring *security.Keyring) *Core {
	if configured, ok := store.(interface {
		ConfigureOperationNodes(map[string]model.RuntimeExecutionIdentity)
	}); ok {
		nodes := map[string]model.RuntimeExecutionIdentity{}
		for _, node := range cfg.Controller.Nodes {
			nodes["node://"+node.NodeID] = model.RuntimeExecutionIdentity{NodeID: node.NodeID, EngineID: node.EngineID, ClusterID: node.ClusterID}
		}
		configured.ConfigureOperationNodes(nodes)
	}
	return &Core{Cluster: cluster.New(store, cfg), ProjectRuntime: projectlife.NewDocker(cfg, store, keyring), store: store, config: cfg, keyring: keyring, now: func() time.Time { return time.Now().UTC() }}
}

func (c *Core) Store() storage.Store { return c.store }

// BuildGatewaySnapshot decrypts private key material only at the final
// distribution boundary. Plaintext keys are never persisted or logged.
func (c *Core) BuildGatewaySnapshot(ctx context.Context, poolID string) (model.Snapshot, error) {
	snapshot, err := c.store.BuildSnapshot(ctx, poolID)
	if err != nil {
		return model.Snapshot{}, err
	}
	for certificateID, value := range snapshot.Certificates {
		certificate, err := c.store.GetCertificate(ctx, certificateID)
		if err != nil {
			return model.Snapshot{}, err
		}
		plaintext, _, err := c.keyring.Decrypt(certificate.EncryptedPrivateKey, []byte(certificate.ID))
		if err != nil {
			return model.Snapshot{}, fmt.Errorf("decrypt certificate %s: %w", certificate.ID, err)
		}
		value.PrivateKeyPEM = string(plaintext)
		snapshot.Certificates[certificateID] = value
	}
	return snapshot, nil
}

type CreatePoolInput struct {
	Name              string `json:"name"`
	BackendNetwork    string `json:"backend_network"`
	ChallengeUpstream string `json:"challenge_upstream"`
}

func (c *Core) CreatePool(ctx context.Context, input CreatePoolInput) (model.GatewayPool, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return model.GatewayPool{}, invalid("validation.name_required", "name", "name is required")
	}
	if input.BackendNetwork == "" {
		input.BackendNetwork = "nsc-ingress"
	}
	if input.ChallengeUpstream == "" {
		input.ChallengeUpstream = c.config.Server.PublicURL
	}
	if err := validateHTTPURL(input.ChallengeUpstream); err != nil {
		return model.GatewayPool{}, err
	}
	if input.ChallengeUpstream == "" {
		return model.GatewayPool{}, invalid("validation.challenge_upstream_required", "challenge_upstream", "challenge_upstream is required")
	}
	now := c.now()
	value := model.GatewayPool{ID: id.New(id.Pool), Name: input.Name, BackendNetwork: input.BackendNetwork,
		ChallengeUpstream: strings.TrimRight(input.ChallengeUpstream, "/"), Enabled: true, Revision: 1,
		CreatedAt: now, UpdatedAt: now}
	return value, c.store.CreatePool(ctx, &value)
}

func (c *Core) UpdatePool(ctx context.Context, value model.GatewayPool, expected int64) (model.GatewayPool, error) {
	current, err := c.store.GetPool(ctx, value.ID)
	if err != nil {
		return value, err
	}
	value.Name = strings.TrimSpace(value.Name)
	value.BackendNetwork = strings.TrimSpace(value.BackendNetwork)
	value.ChallengeUpstream = strings.TrimRight(strings.TrimSpace(value.ChallengeUpstream), "/")
	if value.Name == "" || value.BackendNetwork == "" || value.ChallengeUpstream == "" {
		return value, invalid("validation.pool_fields_required", "", "name, backend_network and challenge_upstream are required")
	}
	if err := validateHTTPURL(value.ChallengeUpstream); err != nil {
		return value, err
	}
	if current.BackendNetwork != value.BackendNetwork && (current.Enabled || value.Enabled) {
		return value, invalid("validation.pool_disable_before_network_change", "backend_network", "disable the gateway pool before changing backend_network")
	}
	value.UpdatedAt = c.now()
	if err := c.store.UpdatePool(ctx, &value, expected); err != nil {
		return value, err
	}
	return c.store.GetPool(ctx, value.ID)
}

type CreateRuntimeInput struct {
	PoolID            string            `json:"pool_id"`
	Name              string            `json:"name"`
	RuntimeType       model.RuntimeType `json:"runtime_type"`
	Endpoint          string            `json:"endpoint"`
	CredentialRef     string            `json:"credential_ref"`
	ManagementEnabled bool              `json:"management_enabled"`
}

func (c *Core) CreateRuntime(ctx context.Context, input CreateRuntimeInput) (model.Runtime, error) {
	if _, err := c.store.GetPool(ctx, input.PoolID); err != nil {
		return model.Runtime{}, err
	}
	if input.RuntimeType != model.RuntimeDocker && input.RuntimeType != model.RuntimeSwarm {
		return model.Runtime{}, invalid("validation.runtime_type_invalid", "runtime_type", "runtime_type must be docker or swarm")
	}
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Endpoint) == "" {
		return model.Runtime{}, invalid("validation.runtime_fields_required", "", "name and endpoint are required")
	}
	if err := validateDockerEndpoint(input.Endpoint); err != nil {
		return model.Runtime{}, err
	}
	if err := c.validateNodeEndpoint(input.Endpoint, input.CredentialRef); err != nil {
		return model.Runtime{}, err
	}
	if input.CredentialRef != "" {
		if _, ok := c.config.Credentials[input.CredentialRef]; !ok {
			return model.Runtime{}, invalidParams("validation.credential_ref_unconfigured", "credential_ref", "credential_ref is not configured", map[string]any{"reference": input.CredentialRef})
		}
	}
	now := c.now()
	value := model.Runtime{ID: id.New(id.Runtime), PoolID: input.PoolID, Name: strings.TrimSpace(input.Name),
		Type: input.RuntimeType, Endpoint: strings.TrimSpace(input.Endpoint), CredentialRef: input.CredentialRef,
		Enabled: true, ManagementEnabled: input.ManagementEnabled, Status: model.RuntimeInitializing, Revision: 1,
		CreatedAt: now, UpdatedAt: now}
	if value.Type == model.RuntimeSwarm && strings.HasPrefix(value.Endpoint, "node://") {
		for _, node := range c.config.Controller.Nodes {
			if value.Endpoint == "node://"+node.NodeID {
				value.ClusterID = node.ClusterID
				break
			}
		}
		if value.ClusterID == "" {
			return model.Runtime{}, invalid("validation.docker_endpoint_invalid", "endpoint", "Swarm node requires a provisioned cluster identity")
		}
	}
	return value, c.store.CreateRuntime(ctx, &value)
}

func (c *Core) UpdateRuntime(ctx context.Context, value model.Runtime, expected int64) (model.Runtime, error) {
	current, err := c.store.GetRuntime(ctx, value.ID)
	if err != nil {
		return value, err
	}
	if _, err := c.store.GetPool(ctx, value.PoolID); err != nil {
		return value, err
	}
	if value.Type != current.Type {
		return value, invalid("validation.runtime_type_immutable", "runtime_type", "runtime_type is immutable")
	}
	if value.PoolID != current.PoolID && (current.Enabled || value.Enabled) {
		return value, invalid("validation.runtime_move_requires_drain", "pool_id", "disable and drain a runtime before moving pools; re-enable it in a separate update")
	}
	value.Name = strings.TrimSpace(value.Name)
	value.Endpoint = strings.TrimSpace(value.Endpoint)
	value.CredentialRef = strings.TrimSpace(value.CredentialRef)
	if value.Name == "" || value.Endpoint == "" {
		return value, invalid("validation.runtime_fields_required", "", "name and endpoint are required")
	}
	if value.CredentialRef != "" {
		if _, ok := c.config.Credentials[value.CredentialRef]; !ok {
			return value, invalidParams("validation.credential_ref_unconfigured", "credential_ref", "credential_ref is not configured", map[string]any{"reference": value.CredentialRef})
		}
	}
	if err := validateDockerEndpoint(value.Endpoint); err != nil {
		return value, err
	}
	if err := c.validateNodeEndpoint(value.Endpoint, value.CredentialRef); err != nil {
		return value, err
	}
	if value.Type == model.RuntimeSwarm && strings.HasPrefix(value.Endpoint, "node://") {
		value.ClusterID = ""
		for _, node := range c.config.Controller.Nodes {
			if value.Endpoint == "node://"+node.NodeID {
				value.ClusterID = node.ClusterID
				break
			}
		}
		if value.ClusterID == "" {
			return value, invalid("validation.docker_endpoint_invalid", "endpoint", "Swarm node requires a provisioned cluster identity")
		}
	}
	value.UpdatedAt = c.now()
	if !value.Enabled {
		value.Status = model.RuntimeDisabled
	} else if !current.Enabled {
		value.Status = model.RuntimeInitializing
		value.LastError = ""
	}
	if err := c.store.UpdateRuntime(ctx, &value, expected); err != nil {
		return value, err
	}
	return c.store.GetRuntime(ctx, value.ID)
}

type ServicePatch struct {
	ProtocolOverride   Nullable[string]                `json:"protocol_override"`
	TargetPortOverride Nullable[int]                   `json:"target_port_override"`
	EnabledOverride    Nullable[bool]                  `json:"enabled_override"`
	ReadinessOverride  Nullable[model.ReadinessPolicy] `json:"readiness_override"`
}

// Nullable preserves the three states required by JSON Merge Patch style
// overrides: omitted, explicitly null (clear), and a concrete value.
type Nullable[T any] struct {
	Set   bool
	Value *T
}

func (o *Nullable[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		o.Value = nil
		return nil
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

func (c *Core) UpdateService(ctx context.Context, serviceID string, patch ServicePatch, expected int64) (model.Service, error) {
	value, err := c.store.GetService(ctx, serviceID)
	if err != nil {
		return value, err
	}
	if patch.ProtocolOverride.Set {
		if patch.ProtocolOverride.Value == nil {
			value.ProtocolOverride = nil
		} else {
			protocol := strings.ToLower(strings.TrimSpace(*patch.ProtocolOverride.Value))
			if !validProtocol(protocol) {
				return value, invalid("validation.protocol_override_invalid", "protocol_override", "protocol_override must be http or https")
			}
			value.ProtocolOverride = &protocol
		}
	}
	if patch.TargetPortOverride.Set {
		if patch.TargetPortOverride.Value == nil {
			value.TargetPortOverride = nil
		} else {
			if *patch.TargetPortOverride.Value < 1 || *patch.TargetPortOverride.Value > 65535 {
				return value, invalid("validation.target_port_override_invalid", "target_port_override", "target_port_override is outside 1..65535")
			}
			value.TargetPortOverride = patch.TargetPortOverride.Value
		}
	}
	if patch.EnabledOverride.Set {
		value.EnabledOverride = patch.EnabledOverride.Value
	}
	if patch.ReadinessOverride.Set {
		if patch.ReadinessOverride.Value == nil {
			value.ReadinessOverride = nil
		} else {
			readiness := *patch.ReadinessOverride.Value
			readiness.Scheme = strings.ToLower(strings.TrimSpace(readiness.Scheme))
			if err := validateReadiness(readiness); err != nil {
				return value, err
			}
			value.ReadinessOverride = &readiness
		}
	}
	value.UpdatedAt = c.now()
	if err := c.store.UpdateService(ctx, &value, expected); err != nil {
		return value, err
	}
	return c.store.GetService(ctx, serviceID)
}

type CreateDomainInput struct {
	PoolID string `json:"pool_id"`
	DomainConfigInput
}

func (c *Core) CreateDomain(ctx context.Context, input CreateDomainInput) (model.Domain, error) {
	if _, err := c.store.GetPool(ctx, input.PoolID); err != nil {
		return model.Domain{}, err
	}
	now := c.now()
	value, certificate, err := c.buildDomain(ctx, input.PoolID, input.DomainConfigInput, now)
	if err != nil {
		return model.Domain{}, err
	}
	if certificate != nil {
		if err = c.store.CreateManagedDomain(ctx, &value, certificate); err != nil {
			return value, err
		}
		return c.store.GetDomain(ctx, value.ID)
	}
	if err := c.store.CreateDomain(ctx, &value); err != nil {
		return value, err
	}
	return value, nil
}

func (c *Core) UpdateDomain(ctx context.Context, value model.Domain, expected int64) (model.Domain, error) {
	current, err := c.store.GetDomain(ctx, value.ID)
	if err != nil {
		return value, err
	}
	if value.PoolID != current.PoolID || value.Source != current.Source {
		return value, invalid("validation.domain_identity_immutable", "", "domain pool and source are immutable")
	}
	host, err := NormalizeHost(value.Host)
	if err != nil {
		return value, err
	}
	value.Host = host
	if err = c.validateHTTP3(ctx, value.PoolID, value.HTTP3Enabled, value.HTTPSEnabled); err != nil {
		return value, err
	}
	if value.TLSMode != model.TLSDisabled && value.TLSMode != model.TLSManaged && value.TLSMode != model.TLSCustom {
		return value, invalid("validation.tls_mode_invalid", "tls.mode", "tls.mode must be disabled, managed, or custom")
	}
	if value.TLSMode != model.TLSManaged && (value.ACMEChallenge != "" || strings.TrimSpace(value.DNSProviderRef) != "") {
		return value, invalid("validation.acme_requires_managed_tls", "tls.acme_challenge", "ACME settings require managed TLS")
	}
	if !value.HTTPEnabled && !value.HTTPSEnabled {
		return value, invalid("validation.entrypoints_required", "entrypoints", "at least one of http or https is required")
	}
	if value.HTTPSEnabled && value.TLSMode == model.TLSDisabled {
		return value, invalid("validation.https_requires_tls", "entrypoints", "the https entrypoint requires managed or custom TLS")
	}
	if !value.HTTPSEnabled && value.TLSMode != model.TLSDisabled {
		return value, invalid("validation.tls_requires_https", "tls.mode", "managed or custom TLS requires the https entrypoint")
	}
	if value.TLSMode == model.TLSCustom && strings.TrimSpace(value.CertificateID) == "" {
		return value, invalid("validation.custom_tls_certificate_required", "tls.certificate_id", "custom TLS requires a certificate_id")
	}
	if value.HTTPSRedirect && (!value.HTTPEnabled || !value.HTTPSEnabled) {
		return value, invalid("validation.redirect_requires_both_entrypoints", "redirect_http_to_https", "HTTP to HTTPS redirect requires both entrypoints")
	}
	value.ACMEChallenge, value.DNSProviderRef, err = c.validateACMESelection(ctx, value.TLSMode, value.ACMEChallenge, value.DNSProviderRef)
	if err != nil {
		return value, err
	}
	managedNeedsProvision := value.TLSMode == model.TLSManaged && (current.TLSMode != model.TLSManaged || current.Host != value.Host ||
		current.ACMEChallenge != value.ACMEChallenge || current.DNSProviderRef != value.DNSProviderRef)
	if managedNeedsProvision {
		value.CertificateID = ""
		value.Status = model.DomainCertPending
	}
	var linkedCertificate model.Certificate
	if value.CertificateID != "" {
		certificate, certificateErr := c.validateDomainCertificate(ctx, value.PoolID, value.Host, value.CertificateID, value.TLSMode)
		if certificateErr != nil {
			return value, certificateErr
		}
		linkedCertificate = certificate
	}
	if value.TLSMode == model.TLSCustom && value.CertificateID == "" {
		value.Status = model.DomainCertPending
	} else if value.TLSMode == model.TLSCustom && linkedCertificate.ID != "" {
		value.Status = model.DomainReady
	}
	if value.TLSMode == model.TLSDisabled {
		value.HTTPSRedirect = false
		value.CertificateID = ""
		value.Status = model.DomainReady
	}
	if !value.Enabled {
		value.Status = model.DomainDisabled
	} else if current.Status == model.DomainDisabled && value.TLSMode == model.TLSManaged && value.CertificateID != "" {
		if linkedCertificate.Status == model.CertificateActive || linkedCertificate.Status == model.CertificateRenewing {
			value.Status = model.DomainReady
		} else {
			value.Status = model.DomainCertPending
		}
	}
	value.UpdatedAt = c.now()
	if err := c.store.UpdateDomain(ctx, &value, expected); err != nil {
		return value, err
	}
	value, err = c.store.GetDomain(ctx, value.ID)
	if err != nil {
		return value, err
	}
	if value.Enabled && value.TLSMode == model.TLSManaged && value.CertificateID == "" {
		return c.provisionManagedCertificate(ctx, value)
	}
	return value, nil
}

func (c *Core) validateDomainCertificate(ctx context.Context, poolID, host, certificateID string, mode model.TLSMode) (model.Certificate, error) {
	certificate, err := c.store.GetCertificate(ctx, certificateID)
	if err != nil {
		return certificate, err
	}
	if certificate.PoolID != poolID {
		return certificate, invalid("validation.certificate_pool_mismatch", "certificate_id", "certificate and domain must belong to the same gateway pool")
	}
	switch mode {
	case model.TLSManaged:
		if certificate.Provider != "acme" {
			return certificate, invalid("validation.managed_tls_acme_certificate_required", "certificate_id", "managed TLS requires an ACME certificate")
		}
	case model.TLSCustom:
		if (certificate.Provider != "custom" && certificate.Provider != "self-signed") || certificate.Status != model.CertificateActive {
			return certificate, invalid("validation.custom_tls_active_certificate_required", "certificate_id", "custom TLS requires an active custom certificate")
		}
	default:
		return certificate, invalid("validation.disabled_tls_certificate", "certificate_id", "disabled TLS cannot reference a certificate")
	}
	block, _ := pem.Decode([]byte(certificate.CertificatePEM))
	if block == nil || block.Type != "CERTIFICATE" {
		if mode == model.TLSManaged && certificate.Status != model.CertificateActive {
			for _, domain := range certificate.Domains {
				if domain == host {
					return certificate, nil
				}
			}
		}
		return certificate, invalid("validation.certificate_leaf_unusable", "certificate_id", "certificate does not contain a usable leaf certificate")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil || leaf.VerifyHostname(host) != nil {
		return certificate, invalid("validation.certificate_host_not_covered", "certificate_id", "certificate does not cover the domain host")
	}
	if mode == model.TLSCustom && (c.now().Before(leaf.NotBefore) || !c.now().Before(leaf.NotAfter)) {
		return certificate, invalid("validation.custom_tls_active_certificate_required", "certificate_id", "custom certificate is outside its validity period")
	}
	return certificate, nil
}

func (c *Core) provisionManagedCertificate(ctx context.Context, domain model.Domain) (model.Domain, error) {
	now := c.now()
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: domain.PoolID, Provider: "acme",
		ACMEChallenge: domain.ACMEChallenge, DNSProviderRef: domain.DNSProviderRef,
		Status: model.CertificatePending, PrimaryDomain: domain.Host, Domains: []string{domain.Host}, Revision: 1,
		CreatedAt: now, UpdatedAt: now}
	if _, err := c.store.ProvisionManagedCertificate(ctx, &certificate, domain.ID, now); err != nil {
		return domain, err
	}
	return c.store.GetDomain(ctx, domain.ID)
}

func (c *Core) validateACMESelection(ctx context.Context, mode model.TLSMode, challenge model.ACMEChallengeType, providerRef string) (model.ACMEChallengeType, string, error) {
	providerRef = strings.TrimSpace(providerRef)
	if mode != model.TLSManaged {
		return "", "", nil
	}
	if challenge == "" {
		if providerRef == "" {
			challenge = model.ACMEHTTP01
		} else {
			challenge = model.ACMEDNS01
		}
	}
	switch challenge {
	case model.ACMEHTTP01:
		if providerRef != "" {
			return "", "", invalid("validation.dns_ref_forbidden_http01", "tls.dns_provider_ref", "dns_provider_ref must be empty when acme_challenge is http-01")
		}
		return challenge, "", nil
	case model.ACMEDNS01:
		if providerRef == "" {
			return "", "", invalid("validation.dns_ref_required_dns01", "tls.dns_provider_ref", "dns_provider_ref is required when acme_challenge is dns-01")
		}
		if _, err := c.resolveDNSProfile(ctx, providerRef); err != nil {
			return "", "", err
		}
		return challenge, providerRef, nil
	default:
		return "", "", invalid("validation.acme_challenge_invalid", "tls.acme_challenge", "acme_challenge must be http-01 or dns-01")
	}
}

type CreateRouteInput struct {
	DomainID string `json:"domain_id"`
	RouteConfigInput
}

func (c *Core) CreateRoute(ctx context.Context, input CreateRouteInput) (model.Route, error) {
	normalizedPath, err := NormalizePath(input.Path)
	if err != nil {
		return model.Route{}, err
	}
	if input.Backend.ServiceID == "" {
		return model.Route{}, invalid("validation.route_service_required", "backend.service_id", "backend.service_id is required")
	}
	service, err := c.store.GetService(ctx, input.Backend.ServiceID)
	if err != nil {
		return model.Route{}, err
	}
	domain, err := c.store.GetDomain(ctx, input.DomainID)
	if err != nil {
		return model.Route{}, err
	}
	runtime, err := c.store.GetRuntime(ctx, service.RuntimeID)
	if err != nil {
		return model.Route{}, err
	}
	if domain.PoolID != runtime.PoolID {
		return model.Route{}, invalid("validation.route_pool_mismatch", "backend.service_id", "route service and domain must belong to the same gateway pool")
	}
	if service.DNSStatus != model.DNSReady {
		return model.Route{}, invalid("validation.service_dns_not_ready", "backend.service_id", "service DNS must be READY before creating a route")
	}
	if input.Backend.Port == 0 {
		input.Backend.Port = service.EffectivePort()
	}
	if input.Backend.Port < 1 || input.Backend.Port > 65535 {
		return model.Route{}, invalid("validation.target_port_invalid", "backend.port", "backend.port is required and must be in 1..65535")
	}
	if input.Backend.Protocol == "" {
		input.Backend.Protocol = service.EffectiveProtocol()
	}
	input.Backend.Protocol = strings.ToLower(strings.TrimSpace(input.Backend.Protocol))
	if !validProtocol(input.Backend.Protocol) {
		return model.Route{}, invalid("validation.upstream_protocol_invalid", "backend.protocol", "backend.protocol must be http or https")
	}
	if input.Backend.ServerName != "" {
		input.Backend.ServerName, err = NormalizeHost(input.Backend.ServerName)
		if err != nil {
			return model.Route{}, invalid("validation.upstream_server_name_invalid", "backend.server_name", "backend.server_name must be a valid DNS name")
		}
	}
	now := c.now()
	value := model.Route{ID: id.New(id.Route), DomainID: input.DomainID, ServiceID: input.Backend.ServiceID,
		Kind: "proxy", Match: "prefer", ReadTimeoutSeconds: 60, ConnectTimeoutSeconds: 5,
		Path: normalizedPath, Priority: input.Priority, StripPath: input.StripPath, TargetPort: input.Backend.Port,
		UpstreamProtocol: input.Backend.Protocol, UpstreamServerName: input.Backend.ServerName,
		WebsocketEnabled: input.WebsocketEnabled, Enabled: true, Status: model.RouteActive, Source: model.SourceAPI,
		Revision: 1, CreatedAt: now, UpdatedAt: now}
	return value, c.store.CreateRoute(ctx, &value)
}

func (c *Core) UpdateRoute(ctx context.Context, value model.Route, expected int64) (model.Route, error) {
	current, err := c.store.GetRoute(ctx, value.ID)
	if err != nil {
		return value, err
	}
	if value.Source != current.Source {
		return value, invalid("validation.route_source_immutable", "source", "route source is immutable")
	}
	if value.Source != model.SourceAPI {
		return value, invalid("validation.label_binding_read_only", "", "label-sourced routes are read-only")
	}
	service, err := c.store.GetService(ctx, value.ServiceID)
	if err != nil {
		return value, err
	}
	domain, err := c.store.GetDomain(ctx, value.DomainID)
	if err != nil {
		return value, err
	}
	runtime, err := c.store.GetRuntime(ctx, service.RuntimeID)
	if err != nil {
		return value, err
	}
	if domain.PoolID != runtime.PoolID {
		return value, invalid("validation.route_pool_mismatch", "backend.service_id", "route service and domain must belong to the same gateway pool")
	}
	if value.Enabled && service.DNSStatus != model.DNSReady {
		return value, invalid("validation.service_dns_not_ready", "backend.service_id", "service DNS must be READY before enabling a route")
	}
	if value.Enabled {
		value.Status = model.RouteActive
	} else {
		value.Status = model.RoutePaused
	}
	value.Path, err = NormalizePath(value.Path)
	if err != nil {
		return value, err
	}
	value.UpstreamProtocol = strings.ToLower(strings.TrimSpace(value.UpstreamProtocol))
	if !validProtocol(value.UpstreamProtocol) {
		return value, invalid("validation.upstream_protocol_invalid", "backend.protocol", "backend protocol must be http or https")
	}
	if value.TargetPort < 1 || value.TargetPort > 65535 {
		return value, invalid("validation.target_port_invalid", "backend.port", "backend port is required and must be in 1..65535")
	}
	if value.UpstreamServerName != "" {
		value.UpstreamServerName, err = NormalizeHost(value.UpstreamServerName)
		if err != nil {
			return value, invalid("validation.upstream_server_name_invalid", "backend.server_name", "backend.server_name must be a valid DNS name")
		}
	}
	value.UpdatedAt = c.now()
	if err := c.store.UpdateRoute(ctx, &value, expected); err != nil {
		return value, err
	}
	return c.store.GetRoute(ctx, value.ID)
}

type CustomCertificateInput struct {
	PoolID         string   `json:"pool_id"`
	DomainIDs      []string `json:"domain_ids"`
	CertificatePEM string   `json:"certificate_pem"`
	PrivateKeyPEM  string   `json:"private_key_pem"`
}

func (c *Core) CreateCustomCertificate(ctx context.Context, input CustomCertificateInput) (model.Certificate, error) {
	return c.installCertificate(ctx, input, "custom")
}

func (c *Core) installCertificate(ctx context.Context, input CustomCertificateInput, provider string) (model.Certificate, error) {
	if input.PoolID == "" {
		return model.Certificate{}, invalid("validation.certificate_invalid", "pool_id", "certificate requires a gateway pool")
	}
	if _, err := c.store.GetPool(ctx, input.PoolID); err != nil {
		return model.Certificate{}, err
	}
	seenDomainIDs := make(map[string]struct{}, len(input.DomainIDs))
	domains := make([]model.Domain, 0, len(input.DomainIDs))
	for _, domainID := range input.DomainIDs {
		if _, exists := seenDomainIDs[domainID]; exists {
			return model.Certificate{}, invalid("validation.domain_ids_duplicate", "domain_ids", "domain_ids must not contain duplicates")
		}
		seenDomainIDs[domainID] = struct{}{}
		domain, err := c.store.GetDomain(ctx, domainID)
		if err != nil {
			return model.Certificate{}, err
		}
		if domain.PoolID != input.PoolID {
			return model.Certificate{}, invalid("validation.certificate_domains_pool_mismatch", "domain_ids", "all certificate domains must belong to the certificate pool")
		}
		domains = append(domains, domain)
	}
	leaf, err := validateKeyPair([]byte(input.CertificatePEM), []byte(input.PrivateKeyPEM), c.now())
	if err != nil {
		return model.Certificate{}, invalid("validation.certificate_invalid", "certificate_pem", err.Error())
	}
	for _, domain := range domains {
		if err := leaf.VerifyHostname(domain.Host); err != nil {
			return model.Certificate{}, invalidParams("validation.certificate_domain_not_covered", "certificate_pem", fmt.Sprintf("certificate does not cover %s", domain.Host), map[string]any{"domain": domain.Host})
		}
	}
	if len(leaf.DNSNames) == 0 {
		return model.Certificate{}, invalid("validation.certificate_invalid", "certificate_pem", "certificate requires a DNS subject alternative name")
	}
	fingerprint := sha256.Sum256(leaf.Raw)
	now := c.now()
	value := model.Certificate{ID: id.New(id.Certificate), PoolID: input.PoolID, Provider: provider,
		Status: model.CertificateActive, PrimaryDomain: leaf.DNSNames[0], CertificatePEM: input.CertificatePEM,
		Fingerprint: hex.EncodeToString(fingerprint[:]), SerialNumber: leaf.SerialNumber.String(),
		NotBefore: &leaf.NotBefore, NotAfter: &leaf.NotAfter, Revision: 1, CreatedAt: now, UpdatedAt: now}
	value.Domains = append([]string(nil), leaf.DNSNames...)
	encrypted, err := c.keyring.Encrypt([]byte(input.PrivateKeyPEM), []byte(value.ID))
	if err != nil {
		return value, err
	}
	value.EncryptedPrivateKey = encrypted
	value.EncryptionKeyID = c.keyring.PrimaryID()
	if err := c.store.InstallCustomCertificate(ctx, &value, input.DomainIDs, now); err != nil {
		return value, err
	}
	return c.store.GetCertificate(ctx, value.ID)
}

func (c *Core) MarkCertificateForRenewal(ctx context.Context, certificateID string) (model.Certificate, error) {
	value, err := c.store.GetCertificate(ctx, certificateID)
	if err != nil {
		return value, err
	}
	if value.Provider != "acme" {
		return value, invalid("validation.acme_certificate_only", "", "only ACME certificates can be renewed")
	}
	value.Status = model.CertificateRenewing
	now := c.now()
	value.NextAttemptAt = &now
	value.LastError = ""
	value.UpdatedAt = now
	if err := c.store.UpdateCertificate(ctx, &value, value.Revision); err != nil {
		return value, err
	}
	return c.store.GetCertificate(ctx, certificateID)
}

func (c *Core) RevokeCertificate(ctx context.Context, certificateID string) (model.Certificate, error) {
	value, err := c.store.GetCertificate(ctx, certificateID)
	if err != nil {
		return value, err
	}
	value.Status = model.CertificateRevoked
	now := c.now()
	value.NextAttemptAt = &now
	value.UpdatedAt = now
	if err := c.store.UpdateCertificate(ctx, &value, value.Revision); err != nil {
		return value, err
	}
	return c.store.GetCertificate(ctx, certificateID)
}

func NormalizeHost(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(value, ".")))
	if value == "" || strings.ContainsAny(value, "*/\\:#? \t\r\n") {
		return "", invalid("validation.host_port_forbidden", "host", "host must be an exact DNS name without a port")
	}
	ascii, err := idna.Lookup.ToASCII(value)
	if err != nil {
		return "", invalid("validation.host_idna_invalid", "host", "host is not a valid IDNA name")
	}
	if len(ascii) > 253 || net.ParseIP(ascii) != nil {
		return "", invalid("validation.host_dns_required", "host", "host must be a DNS name")
	}
	labels := strings.Split(ascii, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", invalid("validation.host_label_invalid", "host", "host contains an invalid DNS label")
		}
	}
	return ascii, nil
}

func NormalizePath(value string) (string, error) {
	const acmeChallengePath = "/.well-known/acme-challenge/"
	value = strings.TrimSpace(value)
	if value == "" {
		value = "/"
	}
	if !strings.HasPrefix(value, "/") || !safePathText(value) {
		return "", invalid("validation.path_invalid", "path", "path must be an absolute URL path without query or fragment")
	}
	value = path.Clean(value)
	if value != "/" {
		value = strings.TrimSuffix(value, "/") + "/"
	}
	if strings.HasPrefix(value, acmeChallengePath) {
		return "", invalid("validation.path_acme_reserved", "path", "path is reserved for the ACME HTTP-01 system route")
	}
	return value, nil
}

func validateHTTPURL(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return invalid("validation.challenge_upstream_invalid", "challenge_upstream", "challenge_upstream must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	return nil
}

func (c *Core) validateNodeEndpoint(endpoint, credential string) error {
	endpoint = strings.TrimSpace(endpoint)
	if len(c.config.Controller.Nodes) == 0 && !strings.HasPrefix(endpoint, "node://") {
		return nil
	}
	if credential == "" {
		for _, node := range c.config.Controller.Nodes {
			if endpoint == "node://"+node.NodeID {
				return nil
			}
		}
	}
	return invalid("validation.docker_endpoint_invalid", "endpoint", "cluster runtimes must reference a provisioned Controller node")
}

func validateDockerEndpoint(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return invalid("validation.docker_endpoint_invalid", "endpoint", "endpoint is not a valid Docker API URL")
	}
	switch parsed.Scheme {
	case "node":
		if parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Contains(parsed.Host, ":") {
			return invalid("validation.docker_endpoint_invalid", "endpoint", "node endpoint requires one provisioned node identity")
		}
	case "unix":
		if parsed.Path == "" {
			return invalid("validation.unix_socket_required", "endpoint", "unix Docker endpoint requires a socket path")
		}
	case "tcp":
		if parsed.Host == "" {
			return invalid("validation.docker_host_required", "endpoint", "Docker endpoint requires a host")
		}
	case "npipe":
		if parsed.Host == "" && parsed.Path == "" && parsed.Opaque == "" {
			return invalid("validation.npipe_path_required", "endpoint", "npipe Docker endpoint requires a named pipe path")
		}
	default:
		return invalid("validation.docker_scheme_invalid", "endpoint", "Docker endpoint scheme must be unix, tcp, or npipe")
	}
	return nil
}

func validateReadiness(value model.ReadinessPolicy) error {
	switch value.Mode {
	case model.ReadinessAuto, model.ReadinessHTTP, model.ReadinessDocker, model.ReadinessTCP, model.ReadinessGrace:
	default:
		return invalid("validation.readiness_mode_invalid", "readiness.mode", "unsupported readiness mode")
	}
	if value.Mode == model.ReadinessHTTP && value.Path == "" {
		return invalid("validation.readiness_http_path_required", "readiness.path", "HTTP readiness requires path")
	}
	if value.Path != "" && (!strings.HasPrefix(value.Path, "/") || !safePathText(value.Path)) {
		return invalid("validation.readiness_path_invalid", "readiness.path", "readiness path must be an absolute URL path without query or fragment")
	}
	if value.Scheme != "" && value.Scheme != "http" && value.Scheme != "https" {
		return invalid("validation.readiness_scheme_invalid", "readiness.scheme", "readiness scheme must be http or https")
	}
	if value.Interval <= 0 || value.Timeout <= 0 || value.SuccessThreshold < 1 || value.FailureThreshold < 1 {
		return invalid("validation.readiness_values_positive", "readiness", "readiness interval, timeout and thresholds must be positive")
	}
	if value.Timeout > value.Interval {
		return invalid("validation.readiness_timeout_interval", "readiness.timeout", "readiness timeout cannot exceed interval")
	}
	return nil
}

func safePathText(value string) bool {
	if strings.ContainsAny(value, "?#{};\"'\\") {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validProtocol(value string) bool { return value == "http" || value == "https" }

func validateKeyPair(certificatePEM, privateKeyPEM []byte, now time.Time) (*x509.Certificate, error) {
	pair, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse certificate and private key: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return nil, errors.New("certificate_pem does not contain a certificate")
	}
	chain := make([]*x509.Certificate, 0, len(pair.Certificate))
	for _, der := range pair.Certificate {
		certificate, parseErr := x509.ParseCertificate(der)
		if parseErr != nil {
			return nil, fmt.Errorf("parse certificate chain: %w", parseErr)
		}
		chain = append(chain, certificate)
	}
	leaf := chain[0]
	if leaf.IsCA {
		return nil, errors.New("leaf certificate must not be a CA")
	}
	if !allowsServerAuthentication(leaf) {
		return nil, errors.New("leaf certificate does not allow TLS server authentication")
	}
	if now.Before(leaf.NotBefore) {
		return nil, errors.New("certificate is not valid yet")
	}
	if now.After(leaf.NotAfter) {
		return nil, errors.New("certificate is expired")
	}
	for index := 0; index+1 < len(chain); index++ {
		issuer := chain[index+1]
		if !issuer.IsCA || !issuer.BasicConstraintsValid {
			return nil, fmt.Errorf("certificate chain issuer %d is not a CA", index+1)
		}
		if now.Before(issuer.NotBefore) || now.After(issuer.NotAfter) {
			return nil, fmt.Errorf("certificate chain issuer %d is outside its validity period", index+1)
		}
		if issuer.KeyUsage != 0 && issuer.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, fmt.Errorf("certificate chain issuer %d cannot sign certificates", index+1)
		}
		if err := chain[index].CheckSignatureFrom(issuer); err != nil {
			return nil, fmt.Errorf("certificate chain signature %d is invalid: %w", index, err)
		}
	}
	return leaf, nil
}

func allowsServerAuthentication(certificate *x509.Certificate) bool {
	if len(certificate.ExtKeyUsage) == 0 {
		return true
	}
	for _, usage := range certificate.ExtKeyUsage {
		if usage == x509.ExtKeyUsageAny || usage == x509.ExtKeyUsageServerAuth {
			return true
		}
	}
	return false
}

type ValidationError struct {
	Code    string
	Field   string
	Params  map[string]any
	Message string
}

func (e ValidationError) Error() string { return e.Message }

func invalid(code, field, message string) error {
	return ValidationError{Code: code, Field: field, Message: message}
}

func invalidParams(code, field, message string, params map[string]any) error {
	return ValidationError{Code: code, Field: field, Params: params, Message: message}
}

func SortDomains(values []model.Domain) {
	sort.Slice(values, func(i, j int) bool { return values[i].Host < values[j].Host })
}
