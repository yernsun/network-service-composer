package model

import (
	"github.com/yernsun/network-service-composer/internal/composecheck"
	"strings"
	"time"
)

type Source string

const (
	SourceDiscovery Source = "DISCOVERY"
	SourceStatic    Source = "STATIC"
	SourceAPI       Source = "API"
	SourceSystem    Source = "SYSTEM"
)

func (s Source) Priority() int {
	switch s {
	case SourceSystem:
		return 4
	case SourceAPI:
		return 3
	case SourceStatic:
		return 2
	case SourceDiscovery:
		return 1
	default:
		return 0
	}
}

type RuntimeType string

const (
	RuntimeDocker RuntimeType = "docker"
	RuntimeSwarm  RuntimeType = "swarm"
)

type RuntimeStatus string

const (
	RuntimeInitializing RuntimeStatus = "INITIALIZING"
	RuntimeOnline       RuntimeStatus = "ONLINE"
	RuntimeDegraded     RuntimeStatus = "DEGRADED"
	RuntimeOffline      RuntimeStatus = "OFFLINE"
	RuntimeDisabled     RuntimeStatus = "DISABLED"
)

type ServiceStatus string

const (
	ServiceDiscovered  ServiceStatus = "DISCOVERED"
	ServiceAvailable   ServiceStatus = "AVAILABLE"
	ServiceDegraded    ServiceStatus = "DEGRADED"
	ServiceUnavailable ServiceStatus = "UNAVAILABLE"
	ServiceRemoved     ServiceStatus = "REMOVED"
)

type RuntimeState string

const (
	EndpointCreated  RuntimeState = "CREATED"
	EndpointStarting RuntimeState = "STARTING"
	EndpointRunning  RuntimeState = "RUNNING"
	EndpointStopping RuntimeState = "STOPPING"
	EndpointDead     RuntimeState = "DEAD"
)

type HealthState string

const (
	HealthUnknown   HealthState = "UNKNOWN"
	HealthHealthy   HealthState = "HEALTHY"
	HealthUnhealthy HealthState = "UNHEALTHY"
)

type ReadyState string

const (
	ReadyStarting ReadyState = "STARTING"
	Ready         ReadyState = "READY"
	NotReady      ReadyState = "NOT_READY"
	Draining      ReadyState = "DRAINING"
	Removed       ReadyState = "REMOVED"
)

type TLSMode string

const (
	TLSDisabled TLSMode = "disabled"
	TLSManaged  TLSMode = "managed"
	TLSCustom   TLSMode = "custom"
)

type ACMEChallengeType string

const (
	ACMEHTTP01 ACMEChallengeType = "http-01"
	ACMEDNS01  ACMEChallengeType = "dns-01"
)

type DomainStatus string

const (
	DomainPending     DomainStatus = "PENDING"
	DomainDNSPending  DomainStatus = "DNS_PENDING"
	DomainCertPending DomainStatus = "CERT_PENDING"
	DomainReady       DomainStatus = "READY"
	DomainDegraded    DomainStatus = "DEGRADED"
	DomainError       DomainStatus = "ERROR"
	DomainDisabled    DomainStatus = "DISABLED"
)

type CertificateStatus string

const (
	CertificatePending     CertificateStatus = "PENDING"
	CertificateAuthorizing CertificateStatus = "AUTHORIZING"
	CertificateValidating  CertificateStatus = "VALIDATING"
	CertificateIssued      CertificateStatus = "ISSUED"
	CertificateActive      CertificateStatus = "ACTIVE"
	CertificateRenewing    CertificateStatus = "RENEWING"
	CertificateExpired     CertificateStatus = "EXPIRED"
	CertificateRevoked     CertificateStatus = "REVOKED"
	CertificateError       CertificateStatus = "ERROR"
)

type GatewayStatus string

const (
	GatewayInitializing GatewayStatus = "INITIALIZING"
	GatewayReady        GatewayStatus = "READY"
	GatewayStale        GatewayStatus = "STALE"
	GatewayDegraded     GatewayStatus = "DEGRADED"
	GatewayOffline      GatewayStatus = "OFFLINE"
	GatewayApplyFailed  GatewayStatus = "APPLY_FAILED"
	GatewayIncompatible GatewayStatus = "INCOMPATIBLE"
)

type DNSStatus string

const (
	DNSReady       DNSStatus = "READY"
	DNSMissing     DNSStatus = "MISSING"
	DNSConflict    DNSStatus = "CONFLICT"
	DNSUnreachable DNSStatus = "UNREACHABLE"
)

type ReadinessMode string

const (
	ReadinessAuto   ReadinessMode = "auto"
	ReadinessHTTP   ReadinessMode = "http"
	ReadinessDocker ReadinessMode = "docker"
	ReadinessTCP    ReadinessMode = "tcp"
	ReadinessGrace  ReadinessMode = "grace"
)

type Metadata struct {
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Revision  int64     `json:"revision"`
}

type GatewayPool struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	BackendNetwork      string    `json:"backend_network"`
	ChallengeUpstream   string    `json:"challenge_upstream"`
	Enabled             bool      `json:"enabled"`
	DesiredRevision     int64     `json:"config_revision"`
	ProbeRevision       int64     `json:"probe_revision"`
	ProbeGatewayID      string    `json:"probe_gateway_id,omitempty"`
	ProbeEpoch          int64     `json:"probe_epoch"`
	LastSnapshotSHA256  string    `json:"last_snapshot_sha256,omitempty"`
	LastSnapshotBuiltAt time.Time `json:"last_snapshot_built_at,omitempty"`
	Metadata
}

type Runtime struct {
	ID                 string        `json:"id"`
	PoolID             string        `json:"pool_id"`
	Name               string        `json:"name"`
	Type               RuntimeType   `json:"runtime_type"`
	Endpoint           string        `json:"endpoint"`
	CredentialRef      string        `json:"credential_ref,omitempty"`
	ClusterID          string        `json:"cluster_id,omitempty"`
	Enabled            bool          `json:"enabled"`
	ManagementEnabled  bool          `json:"management_enabled"`
	Status             RuntimeStatus `json:"status"`
	ObservationEpoch   int64         `json:"observation_epoch"`
	AppliedObservation int64         `json:"applied_observation"`
	LastSeenAt         *time.Time    `json:"last_seen_at,omitempty"`
	LastError          string        `json:"last_error,omitempty"`
	Metadata
}

type ReadinessPolicy struct {
	Mode               ReadinessMode `json:"mode" yaml:"mode"`
	Path               string        `json:"path,omitempty" yaml:"path,omitempty"`
	Scheme             string        `json:"scheme,omitempty" yaml:"scheme,omitempty"`
	Interval           time.Duration `json:"interval" yaml:"interval"`
	Timeout            time.Duration `json:"timeout" yaml:"timeout"`
	SuccessThreshold   int           `json:"success_threshold" yaml:"success_threshold"`
	FailureThreshold   int           `json:"failure_threshold" yaml:"failure_threshold"`
	StartupGracePeriod time.Duration `json:"startup_grace_period" yaml:"startup_grace_period"`
	StartupTimeout     time.Duration `json:"startup_timeout" yaml:"startup_timeout"`
	TCPFallback        bool          `json:"tcp_fallback" yaml:"tcp_fallback"`
}

type Service struct {
	ManagementMode string                `json:"management_mode"`
	InstallationID string                `json:"installation_id,omitempty"`
	System         bool                  `json:"system"`
	Environment    *EnvironmentReference `json:"environment,omitempty"`
	ID             string                `json:"id"`
	RuntimeID      string                `json:"runtime_id"`
	// ServiceKey is stable across container and Swarm service replacement. For
	// Compose it is "project/service"; for Swarm it is the stable service name.
	ServiceKey            string           `json:"service_key"`
	RuntimeObjectID       string           `json:"runtime_object_id,omitempty"`
	ExternalID            string           `json:"external_id"`
	ApplicationID         string           `json:"application_id,omitempty"`
	Managed               bool             `json:"managed"`
	RoutingEligible       bool             `json:"routing_eligible"`
	RoutingIssues         []string         `json:"routing_issues,omitempty"`
	LifecycleOperations   []string         `json:"lifecycle_operations,omitempty"`
	Name                  string           `json:"name"`
	Namespace             string           `json:"namespace,omitempty"`
	QualifiedName         string           `json:"qualified_name"`
	DNSName               string           `json:"dns_name"`
	DNSStatus             DNSStatus        `json:"dns_status"`
	DNSLastError          string           `json:"dns_last_error,omitempty"`
	DiscoveredProtocol    string           `json:"discovered_protocol"`
	ProtocolOverride      *string          `json:"protocol_override,omitempty"`
	DiscoveredPort        int              `json:"discovered_port"`
	TargetPortOverride    *int             `json:"target_port_override,omitempty"`
	DiscoveredEnabled     bool             `json:"discovered_enabled"`
	EnabledOverride       *bool            `json:"enabled_override,omitempty"`
	Readiness             ReadinessPolicy  `json:"readiness"`
	ReadinessOverride     *ReadinessPolicy `json:"readiness_override,omitempty"`
	DesiredInstances      int              `json:"desired_instances"`
	RunningInstances      int              `json:"running_instances"`
	RuntimeReadyInstances int              `json:"-"`
	ReadyInstances        int              `json:"healthy_instances"`
	Status                ServiceStatus    `json:"status"`
	ConfigurationError    string           `json:"configuration_error,omitempty"`
	LastSeenAt            *time.Time       `json:"last_seen_at,omitempty"`
	// RuntimeCreatedAt and RuntimeUpdatedAt describe the Docker Service object
	// when the runtime exposes one (Swarm). Compose has no Service object, so
	// these remain nil and the API's Metadata records NSC discovery history.
	RuntimeCreatedAt *time.Time `json:"runtime_created_at,omitempty"`
	RuntimeUpdatedAt *time.Time `json:"runtime_updated_at,omitempty"`
	// RemovedAt is when an authoritative NSC transition first confirmed that
	// the Service was removed. Docker does not retain a deleted Service object.
	RemovedAt *time.Time `json:"removed_at,omitempty"`
	Metadata
}

func (s Service) EffectiveQualifiedName() string {
	if s.QualifiedName != "" {
		return s.QualifiedName
	}
	if s.Namespace != "" && !strings.HasPrefix(s.Name, s.Namespace+"_") {
		return s.Namespace + "_" + s.Name
	}
	return s.Name
}

func (s Service) EffectiveDNSName() string {
	// DNS identity is an explicit, discovery-verified value. Falling back to a
	// display name would let an API-created or stale Service inject an arbitrary
	// hostname into the shared Gateway network.
	return strings.TrimSpace(s.DNSName)
}

func (s Service) EffectiveProtocol() string {
	if s.ProtocolOverride != nil {
		return *s.ProtocolOverride
	}
	if s.DiscoveredProtocol == "" {
		return "http"
	}
	return s.DiscoveredProtocol
}

func (s Service) EffectivePort() int {
	if s.TargetPortOverride != nil {
		return *s.TargetPortOverride
	}
	return s.DiscoveredPort
}

func (s Service) EffectiveEnabled() bool {
	if s.EnabledOverride != nil {
		return *s.EnabledOverride
	}
	return s.DiscoveredEnabled
}

func (s Service) EffectiveReadiness() ReadinessPolicy {
	if s.ReadinessOverride != nil {
		return *s.ReadinessOverride
	}
	return s.Readiness
}

type Endpoint struct {
	ID                string        `json:"id"`
	ServiceID         string        `json:"service_id"`
	ExternalID        string        `json:"external_id"`
	Address           string        `json:"address"`
	Port              int           `json:"port"`
	NodeID            string        `json:"node_id,omitempty"`
	RuntimeState      RuntimeState  `json:"runtime_state"`
	HealthState       HealthState   `json:"health_state"`
	ReadyState        ReadyState    `json:"ready_state"`
	ReadinessSource   ReadinessMode `json:"readiness_source"`
	Weight            int           `json:"weight"`
	SuccessCount      int           `json:"success_count"`
	FailureCount      int           `json:"failure_count"`
	LastProbeSequence int64         `json:"last_probe_sequence"`
	LastProbeError    string        `json:"last_probe_error,omitempty"`
	FirstSeenAt       time.Time     `json:"first_seen_at"`
	ReadySince        *time.Time    `json:"ready_since,omitempty"`
	DrainSince        *time.Time    `json:"drain_since,omitempty"`
	StaleSince        *time.Time    `json:"stale_since,omitempty"`
	LastSeenAt        time.Time     `json:"last_seen_at"`
	Metadata
}

func (e Endpoint) Eligible() bool {
	return e.RuntimeState == EndpointRunning && e.ReadyState == Ready && e.HealthState != HealthUnhealthy
}

type Domain struct {
	ID             string            `json:"id"`
	PoolID         string            `json:"pool_id"`
	Host           string            `json:"host"`
	TLSMode        TLSMode           `json:"tls_mode"`
	ACMEChallenge  ACMEChallengeType `json:"acme_challenge,omitempty"`
	DNSProviderRef string            `json:"dns_provider_ref,omitempty"`
	HTTPSRedirect  bool              `json:"https_redirect"`
	HTTPEnabled    bool              `json:"http_enabled"`
	HTTPSEnabled   bool              `json:"https_enabled"`
	HTTP3Enabled   bool              `json:"http3_enabled"`
	Status         DomainStatus      `json:"status"`
	CertificateID  string            `json:"certificate_id,omitempty"`
	Source         Source            `json:"source"`
	Enabled        bool              `json:"enabled"`
	LastError      string            `json:"last_error,omitempty"`
	Metadata
}

type Route struct {
	ID                    string      `json:"id"`
	DomainID              string      `json:"domain_id"`
	ServiceID             string      `json:"service_id"`
	Kind                  string      `json:"kind"`
	Match                 string      `json:"match"`
	ExternalURL           string      `json:"external_url,omitempty"`
	ResponseCode          int         `json:"response_code,omitempty"`
	ResponseValue         string      `json:"response_value,omitempty"`
	ReadTimeoutSeconds    int         `json:"read_timeout_seconds,omitempty"`
	ConnectTimeoutSeconds int         `json:"connect_timeout_seconds,omitempty"`
	ExternalKey           string      `json:"external_key,omitempty"`
	Path                  string      `json:"path"`
	Priority              int         `json:"priority"`
	StripPath             bool        `json:"strip_path"`
	TargetPort            int         `json:"target_port,omitempty"`
	UpstreamProtocol      string      `json:"upstream_protocol"`
	UpstreamServerName    string      `json:"upstream_server_name,omitempty"`
	WebsocketEnabled      bool        `json:"websocket_enabled"`
	Enabled               bool        `json:"enabled"`
	Status                RouteStatus `json:"status"`
	Source                Source      `json:"source"`
	LastError             string      `json:"last_error,omitempty"`
	Metadata
}

type RouteStatus string

const (
	RoutePending  RouteStatus = "PENDING"
	RouteActive   RouteStatus = "ACTIVE"
	RoutePaused   RouteStatus = "PAUSED"
	RouteError    RouteStatus = "ERROR"
	RouteDisabled RouteStatus = "DISABLED"
)

type CatalogSourceType string

const (
	CatalogBuiltin  CatalogSourceType = "BUILTIN"
	CatalogGit      CatalogSourceType = "GIT"
	CatalogManifest CatalogSourceType = "STATIC_MANIFEST"
)

type CatalogSource struct {
	ApprovedEndpoint string            `json:"approved_endpoint,omitempty"`
	MarketJSON       string            `json:"-"`
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Type             CatalogSourceType `json:"type"`
	URL              string            `json:"url,omitempty"`
	PinnedRef        string            `json:"pinned_ref,omitempty"`
	Trusted          bool              `json:"trusted"`
	Enabled          bool              `json:"enabled"`
	LastSyncedAt     *time.Time        `json:"last_synced_at,omitempty"`
	LastError        string            `json:"last_error,omitempty"`
	Metadata
}

type CatalogAppVersion struct {
	MarketJSON          string   `json:"-"`
	Withdrawn           bool     `json:"withdrawn"`
	ID                  string   `json:"id"`
	SourceID            string   `json:"source_id"`
	AppKey              string   `json:"app_key"`
	Name                string   `json:"name"`
	Description         string   `json:"description,omitempty"`
	Version             string   `json:"version"`
	Commit              string   `json:"commit,omitempty"`
	ArtifactDigest      string   `json:"artifact_digest"`
	ComposeSHA256       string   `json:"compose_sha256"`
	ManifestJSON        string   `json:"manifest,omitempty"`
	ComposeYAML         string   `json:"compose_yaml,omitempty"`
	EnvExample          string   `json:"env_example,omitempty"`
	README              string   `json:"readme,omitempty"`
	IconURL             string   `json:"icon_url,omitempty"`
	Services            []string `json:"services"`
	ComposeCompatible   bool     `json:"compose_compatible"`
	SwarmCompatible     bool     `json:"swarm_compatible"`
	SwarmYAML           string   `json:"swarm_yaml,omitempty"`
	SwarmSHA256         string   `json:"swarm_sha256,omitempty"`
	CompatibilityIssues []string `json:"compatibility_issues,omitempty"`
	Metadata
}

type ApplicationStatus string

const (
	ApplicationUnknown ApplicationStatus = "UNKNOWN"
	ApplicationPending ApplicationStatus = "PENDING"
	ApplicationRunning ApplicationStatus = "RUNNING"
	ApplicationStopped ApplicationStatus = "STOPPED"
	ApplicationDown    ApplicationStatus = "DOWN"
	ApplicationFailed  ApplicationStatus = "FAILED"
)

type Application struct {
	ManagementMode        string                `json:"management_mode"`
	InstallationID        string                `json:"installation_id,omitempty"`
	System                bool                  `json:"system"`
	SystemKey             string                `json:"system_key,omitempty"`
	Environment           *EnvironmentReference `json:"environment,omitempty"`
	LifecycleOperations   []string              `json:"lifecycle_operations"`
	ID                    string                `json:"id"`
	RuntimeID             string                `json:"runtime_id"`
	Name                  string                `json:"name"`
	ProjectName           string                `json:"project_name"`
	RegistryCredentialRef string                `json:"registry_credential_ref,omitempty"`
	CatalogAppVersionID   string                `json:"catalog_app_version_id,omitempty"`
	ActiveRevisionID      string                `json:"active_revision_id,omitempty"`
	DesiredRevisionID     string                `json:"desired_revision_id,omitempty"`
	ReplicaOverrides      map[string]int        `json:"replica_overrides,omitempty"`
	Status                ApplicationStatus     `json:"status"`
	Managed               bool                  `json:"managed"`
	LastError             string                `json:"last_error,omitempty"`
	Metadata
}

type ServiceEntrypoint struct {
	Host          string `json:"host"`
	Service       string `json:"service"`
	Port          int    `json:"port"`
	GatewayID     string `json:"gateway_id"`
	CertificateID string `json:"certificate_id,omitempty"`
}

// ServiceEntrypointIntent survives configuration-only revisions and execution
// retries. Its identity changes only when the reviewed ingress intent changes.
type ServiceEntrypointIntent struct {
	ID    string            `json:"id"`
	Entry ServiceEntrypoint `json:"entry"`
}

type ServiceDeploymentScope struct {
	TargetServiceRevision    int64                     `json:"target_service_revision,omitempty"`
	TargetRuntimeObjectID    string                    `json:"target_runtime_object_id,omitempty"`
	ObservedReplicaOverrides map[string]int            `json:"observed_replica_overrides"`
	ReplicaOverrides         map[string]int            `json:"replica_overrides"`
	RequestedReplicas        *int                      `json:"requested_replicas,omitempty"`
	DeferredUntilStart       bool                      `json:"deferred_until_start,omitempty"`
	Observed                 map[string]ServiceVersion `json:"observed"`
	Mode                     string                    `json:"mode"`
	Names                    []string                  `json:"names"`
	BaseRevisionID           string                    `json:"base_revision_id"`
	ProjectID                string                    `json:"project_id"`
}

type ServiceVersion struct {
	ID      string `json:"id"`
	Version uint64 `json:"version"`
	Stack   string `json:"stack"`
}

type DeploymentSource struct {
	// RestrictedHostAccess is frozen by scoped API review and enforced again by execution.
	RestrictedHostAccess bool                               `json:"restricted_host_access,omitempty"`
	Entrypoint           *ServiceEntrypoint                 `json:"entrypoint,omitempty"`
	EntrypointIntents    map[string]ServiceEntrypointIntent `json:"entrypoint_intents,omitempty"`
	DockerResources      []DockerResourceBinding            `json:"docker_resources,omitempty"`

	ServiceScope     *ServiceDeploymentScope `json:"service_scope,omitempty"`
	ServiceRevisions map[string]string       `json:"service_revisions,omitempty"`

	DeploymentPlan              composecheck.Plan `json:"deployment_plan,omitempty"`
	NetworkName                 string            `json:"network_name,omitempty"`
	RestoredFrom                string            `json:"restored_from,omitempty"`
	UpdatePolicyRevision        int64             `json:"update_policy_revision,omitempty"`
	PreflightID                 string            `json:"preflight_id,omitempty"`
	EnvironmentRevision         int64             `json:"environment_revision,omitempty"`
	ImageUpdate                 bool              `json:"image_update,omitempty"`
	EnvironmentID               string            `json:"environment_id,omitempty"`
	PublicVariablesVersion      int64             `json:"public_variables_version,omitempty"`
	EnvironmentVariablesVersion int64             `json:"environment_variables_version,omitempty"`
	Images                      map[string]string `json:"images,omitempty"`
	TaggedImages                map[string]string `json:"tagged_images,omitempty"`
}

type DockerResourceBinding struct {
	ClusterID string `json:"cluster_id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	DockerID  string `json:"docker_id"`
}

type EnvironmentReference struct {
	System      bool   `json:"system"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
}

type ApplicationRevision struct {
	ConfigurationSource  DeploymentSource `json:"configuration_source"`
	ID                   string           `json:"id"`
	ApplicationID        string           `json:"application_id"`
	Sequence             int64            `json:"sequence"`
	ParentRevisionID     string           `json:"parent_revision_id,omitempty"`
	CatalogAppVersionID  string           `json:"catalog_app_version_id,omitempty"`
	ComposeSHA256        string           `json:"compose_sha256"`
	EnvironmentSHA256    string           `json:"environment_sha256"`
	ResolvedSHA256       string           `json:"resolved_sha256"`
	EnvironmentKeys      []string         `json:"environment_keys"`
	SecretKeys           []string         `json:"secret_keys,omitempty"`
	EncryptedCompose     []byte           `json:"-"`
	EncryptedEnvironment []byte           `json:"-"`
	EncryptedResolved    []byte           `json:"-"`
	EncryptionKeyID      string           `json:"encryption_key_id"`
	CreatedBy            string           `json:"created_by,omitempty"`
	CreatedAt            time.Time        `json:"created_at"`
}

type DeploymentOperationStatus string

const (
	OperationQueued         DeploymentOperationStatus = "QUEUED"
	OperationRunning        DeploymentOperationStatus = "RUNNING"
	OperationSucceeded      DeploymentOperationStatus = "SUCCEEDED"
	OperationFailed         DeploymentOperationStatus = "FAILED"
	OperationRolledBack     DeploymentOperationStatus = "ROLLED_BACK"
	OperationRollbackFailed DeploymentOperationStatus = "ROLLBACK_FAILED"
)

type DeploymentOperation struct {
	ExecutionIdentity RuntimeExecutionIdentity  `json:"-"`
	ID                string                    `json:"id"`
	ApplicationID     string                    `json:"application_id,omitempty"`
	RuntimeID         string                    `json:"runtime_id"`
	ServiceID         string                    `json:"service_id,omitempty"`
	RevisionID        string                    `json:"revision_id,omitempty"`
	Type              string                    `json:"type"`
	BusinessAction    string                    `json:"business_action,omitempty"`
	Status            DeploymentOperationStatus `json:"status"`
	IdempotencyKey    string                    `json:"idempotency_key,omitempty"`
	RequestSHA256     string                    `json:"-"`
	EncryptedInput    []byte                    `json:"-"`
	EncryptionKeyID   string                    `json:"-"`
	RedactedLog       string                    `json:"log,omitempty"`
	ErrorCode         string                    `json:"error_code,omitempty"`
	ErrorMessage      string                    `json:"error_message,omitempty"`
	Attempt           int                       `json:"attempt"`
	WorkerID          string                    `json:"worker_id,omitempty"`
	HeartbeatAt       *time.Time                `json:"heartbeat_at,omitempty"`
	StartedAt         *time.Time                `json:"started_at,omitempty"`
	FinishedAt        *time.Time                `json:"finished_at,omitempty"`
	DurationMS        int64                     `json:"duration_ms,omitempty"`
	Metadata
}

// RuntimeExecutionIdentity is captured transactionally when work is queued.
// It never follows a later edit of a Runtime endpoint or node enrollment.
type RuntimeExecutionIdentity struct {
	PoolID        string      `json:"pool_id"`
	Type          RuntimeType `json:"runtime_type"`
	Endpoint      string      `json:"endpoint"`
	CredentialRef string      `json:"credential_ref,omitempty"`
	ClusterID     string      `json:"cluster_id,omitempty"`
	NodeID        string      `json:"node_id,omitempty"`
	EngineID      string      `json:"engine_id,omitempty"`
}

type DomainBinding struct {
	ID         string      `json:"id"`
	Service    Service     `json:"service"`
	Domain     Domain      `json:"domain"`
	Route      Route       `json:"route"`
	Source     Source      `json:"source"`
	Status     RouteStatus `json:"status"`
	Editable   bool        `json:"editable"`
	DNSTargets []DNSTarget `json:"dns_targets,omitempty"`
}

type DNSTarget struct {
	GatewayID   string `json:"gateway_id"`
	GatewayName string `json:"gateway_name"`
	RecordType  string `json:"record_type"`
	Value       string `json:"value"`
}

type Certificate struct {
	IPAddresses          []string          `json:"ip_addresses,omitempty"`
	ID                   string            `json:"id"`
	PoolID               string            `json:"pool_id"`
	Provider             string            `json:"provider"`
	ACMEChallenge        ACMEChallengeType `json:"acme_challenge,omitempty"`
	DNSProviderRef       string            `json:"dns_provider_ref,omitempty"`
	Status               CertificateStatus `json:"status"`
	PrimaryDomain        string            `json:"primary_domain"`
	Domains              []string          `json:"domains"`
	CertificatePEM       string            `json:"-"`
	EncryptedPrivateKey  []byte            `json:"-"`
	EncryptionKeyID      string            `json:"encryption_key_id,omitempty"`
	Fingerprint          string            `json:"fingerprint,omitempty"`
	SerialNumber         string            `json:"serial_number,omitempty"`
	NotBefore            *time.Time        `json:"not_before,omitempty"`
	NotAfter             *time.Time        `json:"not_after,omitempty"`
	AttemptCount         int               `json:"attempt_count"`
	NextAttemptAt        *time.Time        `json:"next_attempt_at,omitempty"`
	LastError            string            `json:"last_error,omitempty"`
	DistributionRevision int64             `json:"distribution_revision"`
	Metadata
}

type ACMEAccount struct {
	ID                  string    `json:"id"`
	DirectoryURL        string    `json:"directory_url"`
	Email               string    `json:"email"`
	Registration        string    `json:"registration,omitempty"`
	EncryptedPrivateKey []byte    `json:"-"`
	EncryptionKeyID     string    `json:"encryption_key_id"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type Gateway struct {
	Evidence             *GatewayEvidence `json:"evidence,omitempty"`
	HTTP3Supported       bool             `json:"http3_supported"`
	HTTP3AdvertisedPort  int              `json:"http3_advertised_port"`
	ID                   string           `json:"id"`
	PoolID               string           `json:"pool_id"`
	Name                 string           `json:"name"`
	Address              string           `json:"address,omitempty"`
	CertificateHash      string           `json:"certificate_hash,omitempty"`
	Enabled              bool             `json:"enabled"`
	Status               GatewayStatus    `json:"status"`
	DesiredRevision      int64            `json:"desired_config_revision"`
	AppliedRevision      int64            `json:"applied_config_revision"`
	AppliedProbeRevision int64            `json:"applied_probe_revision"`
	AgentVersion         string           `json:"agent_version,omitempty"`
	ProtocolVersion      int32            `json:"protocol_version"`
	RendererVersion      string           `json:"renderer_version,omitempty"`
	ModelDigest          string           `json:"model_digest,omitempty"`
	RenderedDigest       string           `json:"rendered_digest,omitempty"`
	LastError            string           `json:"last_error,omitempty"`
	LastSeenAt           *time.Time       `json:"last_seen_at,omitempty"`
	Metadata
}

type StateChange struct {
	PoolID   string   `json:"pool_id"`
	Revision int64    `json:"revision"`
	Kind     string   `json:"kind"`
	IDs      []string `json:"ids"`
}

type AuditEvent struct {
	ID           string    `json:"id"`
	Actor        string    `json:"actor"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id,omitempty"`
	RequestID    string    `json:"request_id,omitempty"`
	Details      string    `json:"details,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type ConfigSnippet struct {
	Scope      string `json:"scope"`
	ResourceID string `json:"resource_id,omitempty"`
	Content    string `json:"content"`
}

// IPAccessControl retains user input; only the renderer normalizes IP ranges.
type IPAccessControl struct {
	Scope      string          `json:"scope"`
	ResourceID string          `json:"resource_id"`
	Access     []string        `json:"access"`
	Forbidden  []string        `json:"forbidden"`
	ClientIP   *ClientIPSource `json:"client_ip,omitempty"`
}

// ClientIPSource is Domain-scoped; Routes inherit the resolved client address.
type ClientIPSource struct {
	Header         string   `json:"header"`
	TrustedProxies []string `json:"trusted_proxies"`
}

type Snapshot struct {
	AccessControls    []IPAccessControl              `json:"access_controls,omitempty"`
	Snippets          []ConfigSnippet                `json:"snippets,omitempty"`
	PoolID            string                         `json:"pool_id"`
	Revision          int64                          `json:"revision"`
	GeneratedAt       time.Time                      `json:"generated_at"`
	ChallengeUpstream string                         `json:"challenge_upstream"`
	Domains           []SnapshotDomain               `json:"domains"`
	Routes            []SnapshotRoute                `json:"routes"`
	Services          map[string]SnapshotService     `json:"services"`
	Certificates      map[string]SnapshotCertificate `json:"certificates"`
}

type SnapshotDomain struct {
	ID            string            `json:"id"`
	Host          string            `json:"host"`
	TLSMode       TLSMode           `json:"tls_mode"`
	ACMEChallenge ACMEChallengeType `json:"acme_challenge,omitempty"`
	HTTPSRedirect bool              `json:"https_redirect"`
	HTTPEnabled   bool              `json:"http_enabled"`
	HTTPSEnabled  bool              `json:"https_enabled"`
	HTTP3Enabled  bool              `json:"http3_enabled"`
	CertificateID string            `json:"certificate_id,omitempty"`
}

type SnapshotRoute struct {
	ID                    string  `json:"id"`
	Kind                  string  `json:"kind,omitempty"`
	Match                 string  `json:"match,omitempty"`
	ExternalURL           string  `json:"external_url,omitempty"`
	ResponseCode          int     `json:"response_code,omitempty"`
	ResponseValue         string  `json:"response_value,omitempty"`
	ReadTimeoutSeconds    int     `json:"read_timeout_seconds,omitempty"`
	ConnectTimeoutSeconds int     `json:"connect_timeout_seconds,omitempty"`
	Host                  string  `json:"host"`
	Path                  string  `json:"path"`
	Priority              int     `json:"priority"`
	StripPath             bool    `json:"strip_path"`
	ServiceID             string  `json:"service_id"`
	TargetPort            int     `json:"target_port,omitempty"`
	TLSMode               TLSMode `json:"tls_mode"`
	HTTPSRedirect         bool    `json:"https_redirect"`
	CertificateID         string  `json:"certificate_id,omitempty"`
	WebsocketEnabled      bool    `json:"websocket_enabled"`
	UpstreamProtocol      string  `json:"upstream_protocol"`
	UpstreamServerName    string  `json:"upstream_server_name,omitempty"`
	UpstreamName          string  `json:"upstream_name"`
	Paused                bool    `json:"paused"`
}

type SnapshotService struct {
	ID            string `json:"id"`
	QualifiedName string `json:"qualified_name"`
	DNSName       string `json:"dns_name"`
	Protocol      string `json:"protocol"`
}

type SnapshotCertificate struct {
	ID             string   `json:"id"`
	Domains        []string `json:"domains"`
	CertificatePEM string   `json:"certificate_pem"`
	PrivateKeyPEM  string   `json:"-"`
}

type ProbePlan struct {
	PoolID   string        `json:"pool_id"`
	Epoch    int64         `json:"epoch"`
	Revision int64         `json:"revision"`
	Targets  []ProbeTarget `json:"targets"`
}

type ProbeTarget struct {
	ServiceID   string          `json:"service_id"`
	DNSName     string          `json:"dns_name"`
	Port        int             `json:"port"`
	Policy      ReadinessPolicy `json:"policy"`
	TargetToken string          `json:"target_token"`
}

type ProbeResult struct {
	PoolID      string        `json:"pool_id"`
	Epoch       int64         `json:"epoch"`
	Revision    int64         `json:"plan_revision"`
	ServiceID   string        `json:"service_id"`
	Sequence    int64         `json:"sequence"`
	Success     bool          `json:"success"`
	Latency     time.Duration `json:"latency"`
	Error       string        `json:"error,omitempty"`
	ObservedAt  time.Time     `json:"observed_at"`
	TargetToken string        `json:"target_token"`
}

// RuntimeSnapshot is the normalized, provider-independent source of truth
// produced by a Docker Engine or Swarm observation.
type RuntimeSnapshot struct {
	RuntimeID   string              `json:"runtime_id"`
	Observation int64               `json:"observation"`
	ObservedAt  time.Time           `json:"observed_at"`
	Services    []DiscoveredService `json:"services"`
}

type DiscoveredService struct {
	ServiceKey         string               `json:"service_key"`
	RuntimeObjectID    string               `json:"runtime_object_id,omitempty"`
	ExternalID         string               `json:"external_id"`
	Name               string               `json:"name"`
	Namespace          string               `json:"namespace"`
	QualifiedName      string               `json:"qualified_name"`
	DNSName            string               `json:"dns_name"`
	DNSStatus          DNSStatus            `json:"dns_status"`
	Protocol           string               `json:"protocol"`
	TargetPort         int                  `json:"target_port"`
	Enabled            bool                 `json:"enabled"`
	DesiredInstances   int                  `json:"desired_instances"`
	RunningInstances   int                  `json:"running_instances"`
	ReadyInstances     int                  `json:"ready_instances"`
	ConfigurationError string               `json:"configuration_error,omitempty"`
	RoutingEligible    bool                 `json:"routing_eligible"`
	RoutingIssues      []string             `json:"routing_issues,omitempty"`
	RuntimeCreatedAt   *time.Time           `json:"runtime_created_at,omitempty"`
	RuntimeUpdatedAt   *time.Time           `json:"runtime_updated_at,omitempty"`
	Readiness          ReadinessPolicy      `json:"readiness"`
	Endpoints          []DiscoveredEndpoint `json:"endpoints"`
	Routes             []DiscoveredRoute    `json:"routes"`
}

type DiscoveredEndpoint struct {
	ExternalID   string       `json:"external_id"`
	Address      string       `json:"address"`
	Port         int          `json:"port"`
	NodeID       string       `json:"node_id,omitempty"`
	RuntimeState RuntimeState `json:"runtime_state"`
	HealthState  HealthState  `json:"health_state"`
	Weight       int          `json:"weight"`
	StartedAt    *time.Time   `json:"started_at,omitempty"`
	TaskState    string       `json:"task_state,omitempty"`
	TaskStatusAt *time.Time   `json:"task_status_at,omitempty"`
}

type DiscoveredRoute struct {
	ExternalKey        string  `json:"external_key"`
	Host               string  `json:"host"`
	Path               string  `json:"path"`
	TLSMode            TLSMode `json:"tls_mode"`
	HTTPEnabled        bool    `json:"http_enabled"`
	HTTPSEnabled       bool    `json:"https_enabled"`
	HTTP3Enabled       bool    `json:"http3_enabled"`
	HTTPSRedirect      bool    `json:"https_redirect"`
	Priority           int     `json:"priority"`
	StripPath          bool    `json:"strip_path"`
	TargetPort         int     `json:"target_port,omitempty"`
	UpstreamProtocol   string  `json:"upstream_protocol"`
	UpstreamServerName string  `json:"upstream_server_name,omitempty"`
	WebsocketEnabled   bool    `json:"websocket_enabled"`
}

type DiscoverySignal struct {
	RuntimeID       string    `json:"runtime_id"`
	Kind            string    `json:"kind"`
	ExternalID      string    `json:"external_id,omitempty"`
	At              time.Time `json:"at"`
	ActorType       string    `json:"actor_type,omitempty"`
	ServiceKey      string    `json:"service_key,omitempty"`
	ServiceObjectID string    `json:"service_object_id,omitempty"`
	TaskID          string    `json:"task_id,omitempty"`
	NodeID          string    `json:"node_id,omitempty"`
	ExitCode        *int      `json:"exit_code,omitempty"`
	Status          string    `json:"status,omitempty"`
}

// RuntimeEvent contains only allowlisted evidence, never Docker's raw attributes.
type RuntimeEvent struct {
	ID            string     `json:"id"`
	EventKey      string     `json:"-"`
	SourceID      string     `json:"source_id"`
	RuntimeID     string     `json:"runtime_id"`
	ServiceID     string     `json:"service_id,omitempty"`
	ApplicationID string     `json:"application_id,omitempty"`
	ProjectID     string     `json:"project_id,omitempty"`
	EnvironmentID string     `json:"environment_id,omitempty"`
	NodeID        string     `json:"node_id,omitempty"`
	InstanceID    string     `json:"instance_id,omitempty"`
	ActorType     string     `json:"actor_type"`
	ActorID       string     `json:"actor_id,omitempty"`
	Action        string     `json:"action"`
	Category      string     `json:"category"`
	Status        string     `json:"status,omitempty"`
	ExitCode      *int       `json:"exit_code,omitempty"`
	OOMKilled     bool       `json:"oom_killed"`
	OccurredAt    time.Time  `json:"occurred_at"`
	ObservedAt    time.Time  `json:"observed_at"`
	GapEndAt      *time.Time `json:"gap_end_at,omitempty"`
}

type RuntimeEventSource struct {
	ID              string    `json:"id"`
	RuntimeID       string    `json:"runtime_id,omitempty"`
	ClusterID       string    `json:"cluster_id,omitempty"`
	NodeID          string    `json:"node_id,omitempty"`
	EngineID        string    `json:"engine_id,omitempty"`
	State           string    `json:"state"`
	FirstSeenAt     time.Time `json:"first_seen_at"`
	LastHeartbeatAt time.Time `json:"last_heartbeat_at"`
	LastError       string    `json:"last_error,omitempty"`
}

type TimelineRecord struct {
	RuntimeEvent
	Source      string `json:"source"`
	OperationID string `json:"operation_id,omitempty"`
}

type GatewayEvidence struct {
	ConfirmedRevision     int64      `json:"confirmed_revision"`
	LoadingConfirmed      bool       `json:"loading_confirmed"`
	StartupState          string     `json:"startup_state"`
	DiskDigest            string     `json:"disk_digest,omitempty"`
	ModelDigest           string     `json:"model_digest,omitempty"`
	RenderedDigest        string     `json:"rendered_digest,omitempty"`
	ObservedAt            time.Time  `json:"observed_at"`
	ReceivedAt            time.Time  `json:"received_at"`
	LastConfirmedAt       *time.Time `json:"last_confirmed_at,omitempty"`
	LastConfirmedRevision int64      `json:"last_confirmed_revision"`
	RefreshIntervalMillis int64      `json:"refresh_interval_millis"`
	ConfigurationState    string     `json:"configuration_state"`
}
