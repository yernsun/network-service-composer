package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/i18n"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"gopkg.in/yaml.v3"
)

type Duration time.Duration

func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	value, err := time.ParseDuration(node.Value)
	if err != nil {
		return err
	}
	*d = Duration(value)
	return nil
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) Std() time.Duration { return time.Duration(d) }

type Config struct {
	Server      ServerConfig                  `yaml:"server"`
	Metrics     MetricsConfig                 `yaml:"metrics"`
	Storage     StorageConfig                 `yaml:"storage"`
	Security    SecurityConfig                `yaml:"security"`
	Management  ManagementConfig              `yaml:"management"`
	Controller  ControllerConfig              `yaml:"controller"`
	Agent       AgentConfig                   `yaml:"agent"`
	NGINX       NGINXConfig                   `yaml:"nginx"`
	ACME        ACMEConfig                    `yaml:"acme"`
	I18N        I18NConfig                    `yaml:"i18n"`
	Credentials map[string]DockerCredential   `yaml:"docker_credentials"`
	Registries  map[string]RegistryCredential `yaml:"registry_credentials"`
}

type I18NConfig struct {
	DefaultLanguage string `yaml:"default_language"`
}

type ServerConfig struct {
	ConsoleHTTPSListen   string   `yaml:"console_https_listen"`
	ConsoleHTTPSCertFile string   `yaml:"console_https_cert_file"`
	ConsoleHTTPSKeyFile  string   `yaml:"console_https_key_file"`
	HTTPListen           string   `yaml:"http_listen"`
	HTTPTLSCertFile      string   `yaml:"http_tls_cert_file"`
	HTTPTLSKeyFile       string   `yaml:"http_tls_key_file"`
	GRPCListen           string   `yaml:"grpc_listen"`
	GRPCTLSCertFile      string   `yaml:"grpc_tls_cert_file"`
	GRPCTLSKeyFile       string   `yaml:"grpc_tls_key_file"`
	GRPCClientCAFile     string   `yaml:"grpc_client_ca_file"`
	PublicURL            string   `yaml:"public_url"`
	ReadTimeout          Duration `yaml:"read_timeout"`
	WriteTimeout         Duration `yaml:"write_timeout"`
	RateLimitRPS         float64  `yaml:"rate_limit_rps"`
	RateLimitBurst       int      `yaml:"rate_limit_burst"`
}

type MetricsConfig struct {
	Listen string `yaml:"listen"`
}

type StorageConfig struct {
	Driver      string `yaml:"driver"`
	DSN         string `yaml:"dsn"`
	AutoMigrate bool   `yaml:"auto_migrate"`
	HA          bool   `yaml:"ha"`
}

type SecurityConfig struct {
	AdminUsername      string            `yaml:"admin_username"`
	AdminToken         string            `yaml:"admin_token"`
	AdminTokenFile     string            `yaml:"admin_token_file"`
	SessionKey         string            `yaml:"session_key"`
	SessionKeyFile     string            `yaml:"session_key_file"`
	PrimaryKeyID       string            `yaml:"primary_key_id"`
	EncryptionKeys     map[string]string `yaml:"encryption_keys"`
	EncryptionKeysFile string            `yaml:"encryption_keys_file"`
}

type SystemApplicationConfig struct {
	Key               string   `yaml:"key"`
	Name              string   `yaml:"name"`
	Environment       string   `yaml:"environment"`
	RuntimeID         string   `yaml:"runtime_id"`
	ObjectIDs         []string `yaml:"object_ids"`
	ReplacesRuntimeID string   `yaml:"replaces_runtime_id"`
	ReplacesObjectIDs []string `yaml:"replaces_object_ids"`
}

type SystemProjectConfig struct {
	ConsoleCertificateID string                    `yaml:"console_certificate_id"`
	ConsoleDomain        string                    `yaml:"console_domain"`
	Applications         []SystemApplicationConfig `yaml:"applications"`
	RuntimeID            string                    `yaml:"runtime_id"`
	ClusterID            string                    `yaml:"cluster_id"`
	Services             []SystemServiceConfig     `yaml:"services"`
}
type SystemServiceConfig struct {
	ObjectID    string `yaml:"object_id"`
	Environment string `yaml:"environment"`
}

type ControllerConfig struct {
	RecoveryMode  bool                     `yaml:"recovery_mode"`
	SystemProject SystemProjectConfig      `yaml:"system_project"`
	InstanceID    string                   `yaml:"instance_id"`
	Nodes         []ControllerNodeConfig   `yaml:"nodes"`
	Internal      ControllerInternalConfig `yaml:"internal"`
	LocalGateway  bool                     `yaml:"local_gateway"`
	HostProc      string                   `yaml:"host_proc"`
	HostRoot      string                   `yaml:"host_root"`
	Discovery     DiscoveryConfig          `yaml:"discovery"`
	RuntimeEvents RuntimeEventsConfig      `yaml:"runtime_events"`
	Endpoint      EndpointConfig           `yaml:"endpoint"`
	Gateway       GatewayConfig            `yaml:"gateway"`
	Certificate   CertificateConfig        `yaml:"certificate"`
	GC            GCConfig                 `yaml:"gc"`
}

type ControllerNodeConfig struct {
	InstanceID string `yaml:"instance_id"`
	NodeID     string `yaml:"node_id"`
	EngineID   string `yaml:"engine_id"`
	ClusterID  string `yaml:"cluster_id"`
	URL        string `yaml:"url"`
}

type ControllerInternalConfig struct {
	Listen      string `yaml:"listen"`
	URL         string `yaml:"url"`
	CAFile      string `yaml:"ca_file"`
	CertFile    string `yaml:"cert_file"`
	KeyFile     string `yaml:"key_file"`
	GRPCAddress string `yaml:"grpc_address"`
}

type ManagementConfig struct {
	DefaultRuntimeID  string   `yaml:"default_runtime_id" json:"default_runtime_id"`
	Enabled           bool     `yaml:"enabled" json:"enabled"`
	DockerCLI         string   `yaml:"docker_cli" json:"docker_cli"`
	WorkDir           string   `yaml:"work_dir" json:"work_dir"`
	ExternalSecrets   []string `yaml:"external_secrets" json:"external_secrets"`
	ExternalConfigs   []string `yaml:"external_configs" json:"external_configs"`
	PollInterval      Duration `yaml:"poll_interval" json:"poll_interval"`
	HeartbeatInterval Duration `yaml:"heartbeat_interval" json:"heartbeat_interval"`
	StaleAfter        Duration `yaml:"stale_after" json:"stale_after"`
	DeployTimeout     Duration `yaml:"deploy_timeout" json:"deploy_timeout"`
}

type DiscoveryConfig struct {
	Events               EventsConfig    `yaml:"events"`
	Reconcile            ReconcileConfig `yaml:"reconcile"`
	FullReconcile        ReconcileConfig `yaml:"full_reconcile"`
	StaleEndpointTimeout Duration        `yaml:"stale_endpoint_timeout"`
	OfflinePolicy        string          `yaml:"offline_policy"`
}

type RuntimeEventsConfig struct {
	Enabled   bool     `yaml:"enabled"`
	Retention Duration `yaml:"retention"`
}

type EventsConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Debounce Duration `yaml:"debounce"`
	MaxDelay Duration `yaml:"max_delay"`
}

type ReconcileConfig struct {
	Interval Duration `yaml:"interval"`
	Debounce Duration `yaml:"debounce"`
	MaxDelay Duration `yaml:"max_delay"`
}

type EndpointConfig struct {
	Readiness    model.ReadinessPolicy `yaml:"readiness"`
	DrainTimeout Duration              `yaml:"drain_timeout"`
}

type GatewayConfig struct {
	Reconcile        ReconcileConfig `yaml:"reconcile"`
	PeriodicInterval Duration        `yaml:"periodic_interval"`
	OfflineTimeout   Duration        `yaml:"offline_timeout"`
}

type CertificateConfig struct {
	ReconcileInterval Duration `yaml:"reconcile_interval"`
	RenewBeforeExpiry Duration `yaml:"renew_before_expiry"`
}

type GCConfig struct {
	EndpointRetention          Duration `yaml:"endpoint_retention"`
	FailedCertificateRetention Duration `yaml:"failed_certificate_retention"`
}

type AgentConfig struct {
	Mode              string   `yaml:"mode"`
	GatewayID         string   `yaml:"gateway_id"`
	PoolID            string   `yaml:"pool_id"`
	Name              string   `yaml:"name"`
	PublicAddress     string   `yaml:"public_address"`
	ControllerAddress string   `yaml:"controller_address"`
	ServerName        string   `yaml:"server_name"`
	CACertFile        string   `yaml:"ca_cert_file"`
	TLSCertFile       string   `yaml:"tls_cert_file"`
	TLSKeyFile        string   `yaml:"tls_key_file"`
	StateDir          string   `yaml:"state_dir"`
	ProbeWorkers      int      `yaml:"probe_workers"`
	HeartbeatInterval Duration `yaml:"heartbeat_interval"`
}

type NGINXConfig struct {
	HTTP3AdvertisedPort int    `yaml:"http3_advertised_port"`
	Binary              string `yaml:"binary"`
	GenerationDir       string `yaml:"generation_dir"`
	MainConfigPath      string `yaml:"main_config_path"`
	KeepGenerations     int    `yaml:"keep_generations"`
	WorkerProcesses     string `yaml:"worker_processes"`
	UpstreamCAFile      string `yaml:"upstream_ca_file"`
}

type ACMEConfig struct {
	DirectoryURL string                        `yaml:"directory_url"`
	Email        string                        `yaml:"email"`
	AcceptTerms  bool                          `yaml:"accept_terms"`
	DNSProviders map[string]DNSProviderProfile `yaml:"dns_providers"`
}

// DNSProviderProfile contains only non-secret provider settings. Credentials
// are loaded from CredentialsFile and are deliberately excluded from YAML and
// JSON serialization so they cannot leak through Settings or diagnostics.
type DNSProviderProfile struct {
	Provider           string                 `yaml:"provider" json:"provider"`
	CredentialsFile    string                 `yaml:"credentials_file" json:"credentials_file"`
	Region             string                 `yaml:"region" json:"region,omitempty"`
	BaseURL            string                 `yaml:"base_url" json:"base_url,omitempty"`
	TTL                int                    `yaml:"ttl" json:"ttl,omitempty"`
	PropagationTimeout Duration               `yaml:"propagation_timeout" json:"propagation_timeout,omitempty"`
	PollingInterval    Duration               `yaml:"polling_interval" json:"polling_interval,omitempty"`
	HTTPTimeout        Duration               `yaml:"http_timeout" json:"http_timeout,omitempty"`
	SequenceInterval   Duration               `yaml:"sequence_interval" json:"sequence_interval,omitempty"`
	Credentials        DNSProviderCredentials `yaml:"-" json:"-"`
}

// DNSProviderCredentials is the strict schema accepted by provider Secret
// files. Only the fields used by the selected provider need to be present.
type DNSProviderCredentials struct {
	AccessKey         string            `json:"access_key,omitempty"`
	SecretKey         string            `json:"secret_key,omitempty"`
	SecurityToken     string            `json:"security_token,omitempty"`
	RAMRole           string            `json:"ram_role,omitempty"`
	APIToken          string            `json:"api_token,omitempty"`
	ZoneAPIToken      string            `json:"zone_api_token,omitempty"`
	Email             string            `json:"email,omitempty"`
	APIKey            string            `json:"api_key,omitempty"`
	SecretID          string            `json:"secret_id,omitempty"`
	SessionToken      string            `json:"session_token,omitempty"`
	Tokens            map[string]string `json:"tokens,omitempty"`
	ApplicationKey    string            `json:"application_key,omitempty"`
	ApplicationSecret string            `json:"application_secret,omitempty"`
	ConsumerKey       string            `json:"consumer_key,omitempty"`
}

type DockerCredential struct {
	CAFile   string `yaml:"ca_file" json:"ca_file"`
	CertFile string `yaml:"cert_file" json:"cert_file"`
	KeyFile  string `yaml:"key_file" json:"key_file"`
}

// RegistryCredential points at an administrator-provisioned Docker config
// Secret. The file is copied into an operation-scoped 0700 directory and is
// never persisted in the database or returned by Settings.
type RegistryCredential struct {
	Insecure   bool   `yaml:"insecure" json:"insecure"`
	ConfigFile string `yaml:"config_file" json:"config_file"`
}

func Defaults() Config {
	return Config{
		Server:  ServerConfig{HTTPListen: ":8080", GRPCListen: ":9443", PublicURL: "http://localhost:8080", ReadTimeout: Duration(15 * time.Second), WriteTimeout: Duration(30 * time.Second), RateLimitRPS: 50, RateLimitBurst: 100},
		Metrics: MetricsConfig{Listen: ":9090"},
		Storage: StorageConfig{Driver: "postgres", AutoMigrate: true},
		Management: ManagementConfig{DockerCLI: "/usr/local/bin/docker", WorkDir: "/tmp/nsc-applications",
			PollInterval: Duration(time.Second), HeartbeatInterval: Duration(5 * time.Second),
			StaleAfter: Duration(30 * time.Second), DeployTimeout: Duration(10 * time.Minute)},
		Controller: ControllerConfig{
			Discovery: DiscoveryConfig{
				Events:               EventsConfig{Enabled: true, Debounce: Duration(500 * time.Millisecond), MaxDelay: Duration(3 * time.Second)},
				Reconcile:            ReconcileConfig{Interval: Duration(15 * time.Second)},
				FullReconcile:        ReconcileConfig{Interval: Duration(5 * time.Minute)},
				StaleEndpointTimeout: Duration(60 * time.Second), OfflinePolicy: "retain-ready",
			},
			RuntimeEvents: RuntimeEventsConfig{Enabled: true, Retention: Duration(90 * 24 * time.Hour)},
			Endpoint: EndpointConfig{
				Readiness:    model.ReadinessPolicy{Mode: model.ReadinessAuto, Interval: 2 * time.Second, Timeout: time.Second, SuccessThreshold: 2, FailureThreshold: 3, StartupGracePeriod: 2 * time.Second, StartupTimeout: 60 * time.Second, TCPFallback: true},
				DrainTimeout: Duration(30 * time.Second),
			},
			Gateway:     GatewayConfig{Reconcile: ReconcileConfig{Debounce: Duration(250 * time.Millisecond), MaxDelay: Duration(2 * time.Second)}, PeriodicInterval: Duration(30 * time.Second), OfflineTimeout: Duration(15 * time.Second)},
			Certificate: CertificateConfig{ReconcileInterval: Duration(time.Hour), RenewBeforeExpiry: Duration(30 * 24 * time.Hour)},
			GC:          GCConfig{EndpointRetention: Duration(24 * time.Hour), FailedCertificateRetention: Duration(30 * 24 * time.Hour)},
		},
		Agent:       AgentConfig{StateDir: "/var/lib/nsc-agent", ProbeWorkers: 32, HeartbeatInterval: Duration(5 * time.Second)},
		NGINX:       NGINXConfig{HTTP3AdvertisedPort: 443, Binary: "/usr/sbin/nginx", GenerationDir: "/var/lib/nsc/generations", MainConfigPath: "/etc/nginx/nginx.conf", KeepGenerations: 3, WorkerProcesses: "auto"},
		Security:    SecurityConfig{AdminUsername: "admin"},
		ACME:        ACMEConfig{DirectoryURL: "https://acme-v02.api.letsencrypt.org/directory", DNSProviders: map[string]DNSProviderProfile{}},
		I18N:        I18NConfig{DefaultLanguage: string(i18n.English)},
		Credentials: map[string]DockerCredential{},
		Registries:  map[string]RegistryCredential{},
	}
}

func Load(path string) (Config, error) {
	config, err := decode(path)
	if err != nil {
		return Config{}, err
	}
	if err := applyEnvironment(&config); err != nil {
		return Config{}, err
	}
	if err := loadSecretFiles(&config); err != nil {
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func LoadAgent(path string) (Config, error) {
	config, err := decode(path)
	if err != nil {
		return Config{}, err
	}
	if err := applyEnvironment(&config); err != nil {
		return Config{}, err
	}
	if err := config.ValidateAgent(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func decode(path string) (Config, error) {
	config := Defaults()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&config); err != nil && !errors.Is(err, io.EOF) {
			return Config{}, fmt.Errorf("decode config: %w", err)
		}
	}
	return config, nil
}

func applyEnvironment(c *Config) error {
	var problems []string
	check := func(err error) {
		if err != nil {
			problems = append(problems, err.Error())
		}
	}
	setString("NSC_SERVER__HTTP_LISTEN", &c.Server.HTTPListen)
	setString("NSC_SERVER__CONSOLE_HTTPS_LISTEN", &c.Server.ConsoleHTTPSListen)
	setString("NSC_SERVER__CONSOLE_HTTPS_CERT_FILE", &c.Server.ConsoleHTTPSCertFile)
	setString("NSC_SERVER__CONSOLE_HTTPS_KEY_FILE", &c.Server.ConsoleHTTPSKeyFile)
	setString("NSC_SERVER__PUBLIC_URL", &c.Server.PublicURL)
	setString("NSC_SERVER__HTTP_TLS_CERT_FILE", &c.Server.HTTPTLSCertFile)
	setString("NSC_SERVER__HTTP_TLS_KEY_FILE", &c.Server.HTTPTLSKeyFile)
	setString("NSC_SERVER__GRPC_LISTEN", &c.Server.GRPCListen)
	setString("NSC_SERVER__GRPC_TLS_CERT_FILE", &c.Server.GRPCTLSCertFile)
	setString("NSC_SERVER__GRPC_TLS_KEY_FILE", &c.Server.GRPCTLSKeyFile)
	setString("NSC_SERVER__GRPC_CLIENT_CA_FILE", &c.Server.GRPCClientCAFile)
	check(setDuration("NSC_SERVER__READ_TIMEOUT", &c.Server.ReadTimeout))
	check(setDuration("NSC_SERVER__WRITE_TIMEOUT", &c.Server.WriteTimeout))
	check(setFloat("NSC_SERVER__RATE_LIMIT_RPS", &c.Server.RateLimitRPS))
	check(setInt("NSC_SERVER__RATE_LIMIT_BURST", &c.Server.RateLimitBurst))
	setString("NSC_METRICS__LISTEN", &c.Metrics.Listen)
	setString("NSC_STORAGE__DRIVER", &c.Storage.Driver)
	setString("NSC_STORAGE__DSN", &c.Storage.DSN)
	check(setBool("NSC_STORAGE__AUTO_MIGRATE", &c.Storage.AutoMigrate))
	check(setBool("NSC_STORAGE__HA", &c.Storage.HA))
	check(setBool("NSC_MANAGEMENT__ENABLED", &c.Management.Enabled))
	setString("NSC_MANAGEMENT__DEFAULT_RUNTIME_ID", &c.Management.DefaultRuntimeID)
	setString("NSC_MANAGEMENT__DOCKER_CLI", &c.Management.DockerCLI)
	setString("NSC_MANAGEMENT__WORK_DIR", &c.Management.WorkDir)
	check(setJSON("NSC_MANAGEMENT__EXTERNAL_SECRETS", &c.Management.ExternalSecrets))
	check(setJSON("NSC_MANAGEMENT__EXTERNAL_CONFIGS", &c.Management.ExternalConfigs))
	check(setDuration("NSC_MANAGEMENT__POLL_INTERVAL", &c.Management.PollInterval))
	check(setDuration("NSC_MANAGEMENT__HEARTBEAT_INTERVAL", &c.Management.HeartbeatInterval))
	check(setDuration("NSC_MANAGEMENT__STALE_AFTER", &c.Management.StaleAfter))
	check(setDuration("NSC_MANAGEMENT__DEPLOY_TIMEOUT", &c.Management.DeployTimeout))
	setString("NSC_SECURITY__ADMIN_USERNAME", &c.Security.AdminUsername)
	setString("NSC_SECURITY__ADMIN_TOKEN", &c.Security.AdminToken)
	setString("NSC_SECURITY__ADMIN_TOKEN_FILE", &c.Security.AdminTokenFile)
	setString("NSC_SECURITY__SESSION_KEY", &c.Security.SessionKey)
	setString("NSC_SECURITY__SESSION_KEY_FILE", &c.Security.SessionKeyFile)
	setString("NSC_SECURITY__PRIMARY_KEY_ID", &c.Security.PrimaryKeyID)
	check(setJSON("NSC_SECURITY__ENCRYPTION_KEYS", &c.Security.EncryptionKeys))
	setString("NSC_SECURITY__ENCRYPTION_KEYS_FILE", &c.Security.EncryptionKeysFile)
	setString("NSC_CONTROLLER__INSTANCE_ID", &c.Controller.InstanceID)
	setString("NSC_CONTROLLER__INTERNAL__LISTEN", &c.Controller.Internal.Listen)
	setString("NSC_CONTROLLER__INTERNAL__URL", &c.Controller.Internal.URL)
	setString("NSC_CONTROLLER__INTERNAL__CA_FILE", &c.Controller.Internal.CAFile)
	setString("NSC_CONTROLLER__INTERNAL__CERT_FILE", &c.Controller.Internal.CertFile)
	setString("NSC_CONTROLLER__INTERNAL__KEY_FILE", &c.Controller.Internal.KeyFile)
	setString("NSC_CONTROLLER__INTERNAL__GRPC_ADDRESS", &c.Controller.Internal.GRPCAddress)
	check(setBool("NSC_CONTROLLER__LOCAL_GATEWAY", &c.Controller.LocalGateway))
	check(setBool("NSC_CONTROLLER__RECOVERY_MODE", &c.Controller.RecoveryMode))
	check(setBool("NSC_CONTROLLER__DISCOVERY__EVENTS__ENABLED", &c.Controller.Discovery.Events.Enabled))
	check(setDuration("NSC_CONTROLLER__DISCOVERY__EVENTS__DEBOUNCE", &c.Controller.Discovery.Events.Debounce))
	check(setDuration("NSC_CONTROLLER__DISCOVERY__EVENTS__MAX_DELAY", &c.Controller.Discovery.Events.MaxDelay))
	check(setDuration("NSC_CONTROLLER__DISCOVERY__RECONCILE__INTERVAL", &c.Controller.Discovery.Reconcile.Interval))
	check(setDuration("NSC_CONTROLLER__DISCOVERY__FULL_RECONCILE__INTERVAL", &c.Controller.Discovery.FullReconcile.Interval))
	check(setDuration("NSC_CONTROLLER__DISCOVERY__STALE_ENDPOINT_TIMEOUT", &c.Controller.Discovery.StaleEndpointTimeout))
	setString("NSC_CONTROLLER__DISCOVERY__OFFLINE_POLICY", &c.Controller.Discovery.OfflinePolicy)
	setReadinessMode("NSC_CONTROLLER__ENDPOINT__READINESS__MODE", &c.Controller.Endpoint.Readiness.Mode)
	setString("NSC_CONTROLLER__ENDPOINT__READINESS__PATH", &c.Controller.Endpoint.Readiness.Path)
	setString("NSC_CONTROLLER__ENDPOINT__READINESS__SCHEME", &c.Controller.Endpoint.Readiness.Scheme)
	check(setStdDuration("NSC_CONTROLLER__ENDPOINT__READINESS__INTERVAL", &c.Controller.Endpoint.Readiness.Interval))
	check(setStdDuration("NSC_CONTROLLER__ENDPOINT__READINESS__TIMEOUT", &c.Controller.Endpoint.Readiness.Timeout))
	check(setInt("NSC_CONTROLLER__ENDPOINT__READINESS__SUCCESS_THRESHOLD", &c.Controller.Endpoint.Readiness.SuccessThreshold))
	check(setInt("NSC_CONTROLLER__ENDPOINT__READINESS__FAILURE_THRESHOLD", &c.Controller.Endpoint.Readiness.FailureThreshold))
	check(setStdDuration("NSC_CONTROLLER__ENDPOINT__READINESS__STARTUP_GRACE_PERIOD", &c.Controller.Endpoint.Readiness.StartupGracePeriod))
	check(setStdDuration("NSC_CONTROLLER__ENDPOINT__READINESS__STARTUP_TIMEOUT", &c.Controller.Endpoint.Readiness.StartupTimeout))
	check(setBool("NSC_CONTROLLER__ENDPOINT__READINESS__TCP_FALLBACK", &c.Controller.Endpoint.Readiness.TCPFallback))
	check(setDuration("NSC_CONTROLLER__ENDPOINT__DRAIN_TIMEOUT", &c.Controller.Endpoint.DrainTimeout))
	check(setDuration("NSC_CONTROLLER__GATEWAY__PERIODIC_INTERVAL", &c.Controller.Gateway.PeriodicInterval))
	check(setDuration("NSC_CONTROLLER__GATEWAY__OFFLINE_TIMEOUT", &c.Controller.Gateway.OfflineTimeout))
	check(setDuration("NSC_CONTROLLER__GATEWAY__RECONCILE__DEBOUNCE", &c.Controller.Gateway.Reconcile.Debounce))
	check(setDuration("NSC_CONTROLLER__GATEWAY__RECONCILE__MAX_DELAY", &c.Controller.Gateway.Reconcile.MaxDelay))
	check(setDuration("NSC_CONTROLLER__CERTIFICATE__RECONCILE_INTERVAL", &c.Controller.Certificate.ReconcileInterval))
	check(setDuration("NSC_CONTROLLER__CERTIFICATE__RENEW_BEFORE_EXPIRY", &c.Controller.Certificate.RenewBeforeExpiry))
	check(setDuration("NSC_CONTROLLER__GC__ENDPOINT_RETENTION", &c.Controller.GC.EndpointRetention))
	check(setDuration("NSC_CONTROLLER__GC__FAILED_CERTIFICATE_RETENTION", &c.Controller.GC.FailedCertificateRetention))
	check(setBool("NSC_CONTROLLER__RUNTIME_EVENTS__ENABLED", &c.Controller.RuntimeEvents.Enabled))
	check(setDuration("NSC_CONTROLLER__RUNTIME_EVENTS__RETENTION", &c.Controller.RuntimeEvents.Retention))
	setString("NSC_AGENT__MODE", &c.Agent.Mode)
	setString("NSC_AGENT__GATEWAY_ID", &c.Agent.GatewayID)
	setString("NSC_AGENT__POOL_ID", &c.Agent.PoolID)
	setString("NSC_AGENT__NAME", &c.Agent.Name)
	setString("NSC_AGENT__PUBLIC_ADDRESS", &c.Agent.PublicAddress)
	setString("NSC_AGENT__CONTROLLER_ADDRESS", &c.Agent.ControllerAddress)
	setString("NSC_AGENT__SERVER_NAME", &c.Agent.ServerName)
	setString("NSC_AGENT__CA_CERT_FILE", &c.Agent.CACertFile)
	setString("NSC_AGENT__TLS_CERT_FILE", &c.Agent.TLSCertFile)
	setString("NSC_AGENT__TLS_KEY_FILE", &c.Agent.TLSKeyFile)
	setString("NSC_AGENT__STATE_DIR", &c.Agent.StateDir)
	check(setInt("NSC_AGENT__PROBE_WORKERS", &c.Agent.ProbeWorkers))
	check(setDuration("NSC_AGENT__HEARTBEAT_INTERVAL", &c.Agent.HeartbeatInterval))
	setString("NSC_NGINX__BINARY", &c.NGINX.Binary)
	setString("NSC_NGINX__GENERATION_DIR", &c.NGINX.GenerationDir)
	setString("NSC_NGINX__UPSTREAM_CA_FILE", &c.NGINX.UpstreamCAFile)
	setString("NSC_NGINX__MAIN_CONFIG_PATH", &c.NGINX.MainConfigPath)
	check(setInt("NSC_NGINX__KEEP_GENERATIONS", &c.NGINX.KeepGenerations))
	check(setInt("NSC_NGINX__HTTP3_ADVERTISED_PORT", &c.NGINX.HTTP3AdvertisedPort))
	setString("NSC_NGINX__WORKER_PROCESSES", &c.NGINX.WorkerProcesses)
	setString("NSC_ACME__DIRECTORY_URL", &c.ACME.DirectoryURL)
	setString("NSC_ACME__EMAIL", &c.ACME.Email)
	check(setBool("NSC_ACME__ACCEPT_TERMS", &c.ACME.AcceptTerms))
	check(setJSON("NSC_ACME__DNS_PROVIDERS", &c.ACME.DNSProviders))
	setString("NSC_I18N__DEFAULT_LANGUAGE", &c.I18N.DefaultLanguage)
	check(setJSON("NSC_DOCKER_CREDENTIALS", &c.Credentials))
	check(setJSON("NSC_REGISTRY_CREDENTIALS", &c.Registries))
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func setString(name string, target *string) {
	if value, ok := os.LookupEnv(name); ok {
		*target = value
	}
}

func setBool(name string, target *bool) error {
	if value, ok := os.LookupEnv(name); ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%s must be a boolean: %w", name, err)
		}
		*target = parsed
	}
	return nil
}

func setDuration(name string, target *Duration) error {
	if value, ok := os.LookupEnv(name); ok {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("%s must be a duration: %w", name, err)
		}
		*target = Duration(parsed)
	}
	return nil
}

func setStdDuration(name string, target *time.Duration) error {
	if value, ok := os.LookupEnv(name); ok {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("%s must be a duration: %w", name, err)
		}
		*target = parsed
	}
	return nil
}

func setInt(name string, target *int) error {
	if value, ok := os.LookupEnv(name); ok {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s must be an integer: %w", name, err)
		}
		*target = parsed
	}
	return nil
}

func setFloat(name string, target *float64) error {
	if value, ok := os.LookupEnv(name); ok {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("%s must be a number: %w", name, err)
		}
		*target = parsed
	}
	return nil
}

func setJSON(name string, target any) error {
	if value, ok := os.LookupEnv(name); ok {
		if err := json.Unmarshal([]byte(value), target); err != nil {
			return fmt.Errorf("%s must be valid JSON: %w", name, err)
		}
	}
	return nil
}

func setReadinessMode(name string, target *model.ReadinessMode) {
	if value, ok := os.LookupEnv(name); ok {
		*target = model.ReadinessMode(strings.ToLower(strings.TrimSpace(value)))
	}
}

func loadSecretFiles(c *Config) error { return loadSecretFilesUsing(c, os.ReadFile) }

func loadSecretFilesUsing(c *Config, read func(string) ([]byte, error)) error {
	readValue := func(path string) (string, error) {
		value, err := read(path)
		if err != nil {
			return "", err
		}
		defer clear(value)
		return strings.TrimSpace(string(value)), nil
	}
	var err error
	if c.Security.AdminTokenFile != "" {
		c.Security.AdminToken, err = readValue(c.Security.AdminTokenFile)
		if err != nil {
			return fmt.Errorf("read admin token: %w", err)
		}
	}
	if c.Security.SessionKeyFile != "" {
		c.Security.SessionKey, err = readValue(c.Security.SessionKeyFile)
		if err != nil {
			return fmt.Errorf("read session key: %w", err)
		}
	}
	if c.Security.EncryptionKeysFile != "" {
		data, readErr := read(c.Security.EncryptionKeysFile)
		if readErr != nil {
			return fmt.Errorf("read encryption keyring: %w", readErr)
		}
		if err = json.Unmarshal(data, &c.Security.EncryptionKeys); err != nil {
			return fmt.Errorf("decode encryption keyring JSON: %w", err)
		}
	}
	for reference, profile := range c.ACME.DNSProviders {
		if strings.TrimSpace(profile.CredentialsFile) == "" {
			return fmt.Errorf("DNS provider %q credentials_file is required", reference)
		}
		data, readErr := read(profile.CredentialsFile)
		if readErr != nil {
			return fmt.Errorf("read DNS provider %q credentials: %w", reference, readErr)
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&profile.Credentials); err != nil {
			return fmt.Errorf("decode DNS provider %q credentials JSON: %w", reference, err)
		}
		if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			if err == nil {
				err = errors.New("multiple JSON values are not allowed")
			}
			return fmt.Errorf("decode DNS provider %q credentials JSON: %w", reference, err)
		}
		c.ACME.DNSProviders[reference] = profile
	}
	return nil
}

func readSecret(path string) (string, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(value)), nil
}

func (c Config) Validate() error {
	var problems []string
	if strings.TrimSpace(c.Server.HTTPListen) == "" || strings.TrimSpace(c.Metrics.Listen) == "" {
		problems = append(problems, "server.http_listen and metrics.listen are required")
	}
	if c.Server.ReadTimeout.Std() <= 0 || c.Server.WriteTimeout.Std() <= 0 {
		problems = append(problems, "server read_timeout and write_timeout must be positive")
	}
	if !validAbsoluteHTTPURL(c.Server.PublicURL) {
		problems = append(problems, "server.public_url must be an absolute HTTP(S) URL")
	}
	switch c.Storage.Driver {
	case "postgres", "mysql", "sqlite":
	default:
		problems = append(problems, "storage.driver must be postgres, mysql, or sqlite")
	}
	if c.Storage.DSN == "" {
		problems = append(problems, "storage.dsn is required")
	}
	if c.Storage.Driver == "sqlite" && c.Storage.HA {
		problems = append(problems, "SQLite cannot be used with HA")
	}
	if c.Controller.RecoveryMode && c.Storage.AutoMigrate {
		problems = append(problems, "controller.recovery_mode requires storage.auto_migrate=false")
	}
	if strings.TrimSpace(c.Management.DockerCLI) == "" || strings.TrimSpace(c.Management.WorkDir) == "" {
		problems = append(problems, "management.docker_cli and management.work_dir are required")
	}
	if c.Management.PollInterval.Std() <= 0 || c.Management.HeartbeatInterval.Std() <= 0 ||
		c.Management.StaleAfter.Std() <= c.Management.HeartbeatInterval.Std() || c.Management.DeployTimeout.Std() <= 0 {
		problems = append(problems, "management intervals are invalid")
	}
	for kind, names := range map[string][]string{"external_secrets": c.Management.ExternalSecrets, "external_configs": c.Management.ExternalConfigs} {
		seen := make(map[string]struct{}, len(names))
		for _, name := range names {
			if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name {
				problems = append(problems, "management."+kind+" entries must be non-empty and have no surrounding whitespace")
				break
			}
			if _, duplicate := seen[name]; duplicate {
				problems = append(problems, "management."+kind+" cannot contain duplicates")
				break
			}
			seen[name] = struct{}{}
		}
	}
	if strings.TrimSpace(c.Security.AdminToken) == "" {
		problems = append(problems, "security.admin_token or admin_token_file is required")
	}
	if strings.TrimSpace(c.Security.AdminUsername) == "" || len(c.Security.AdminUsername) > 128 {
		problems = append(problems, "security.admin_username must contain 1 to 128 characters")
	}
	if len(c.Security.SessionKey) < 32 {
		problems = append(problems, "security.session_key must contain at least 32 bytes")
	}
	if c.Security.PrimaryKeyID == "" || c.Security.EncryptionKeys[c.Security.PrimaryKeyID] == "" {
		problems = append(problems, "security.primary_key_id must reference an encryption key")
	}
	readiness := c.Controller.Endpoint.Readiness
	if !validReadinessMode(readiness.Mode) {
		problems = append(problems, "readiness mode is invalid")
	}
	if readiness.Interval <= 0 || readiness.Timeout <= 0 || readiness.Timeout > readiness.Interval || readiness.SuccessThreshold < 1 || readiness.FailureThreshold < 1 || readiness.StartupGracePeriod < 0 || readiness.StartupTimeout <= 0 {
		problems = append(problems, "readiness intervals, thresholds, grace, or startup timeout are invalid")
	}
	if readiness.Scheme != "" && readiness.Scheme != "http" && readiness.Scheme != "https" {
		problems = append(problems, "readiness scheme must be http or https")
	}
	if readiness.Mode == model.ReadinessHTTP && !strings.HasPrefix(readiness.Path, "/") {
		problems = append(problems, "HTTP readiness path must be absolute")
	}
	if c.Controller.Discovery.Reconcile.Interval.Std() <= 0 || c.Controller.Discovery.FullReconcile.Interval.Std() <= 0 || c.Controller.Discovery.StaleEndpointTimeout.Std() <= 0 {
		problems = append(problems, "discovery reconcile and stale intervals must be positive")
	}
	if c.Controller.Endpoint.DrainTimeout.Std() < 0 || c.Controller.Gateway.OfflineTimeout.Std() <= 0 || c.Controller.Certificate.ReconcileInterval.Std() <= 0 || c.Controller.Certificate.RenewBeforeExpiry.Std() <= 0 {
		problems = append(problems, "controller lifecycle intervals are invalid")
	}
	if c.Controller.Discovery.Events.Enabled && (c.Controller.Discovery.Events.Debounce.Std() < 0 || c.Controller.Discovery.Events.MaxDelay.Std() <= 0 || c.Controller.Discovery.Events.MaxDelay.Std() < c.Controller.Discovery.Events.Debounce.Std()) {
		problems = append(problems, "discovery event debounce/max_delay are invalid")
	}
	if c.Controller.Gateway.PeriodicInterval.Std() <= 0 || c.Controller.GC.EndpointRetention.Std() <= 0 || c.Controller.GC.FailedCertificateRetention.Std() <= 0 {
		problems = append(problems, "gateway periodic and GC retention intervals must be positive")
	}
	if c.Controller.RuntimeEvents.Retention.Std() <= 0 {
		problems = append(problems, "runtime event retention must be positive")
	}
	if c.Controller.Gateway.Reconcile.Debounce.Std() < 0 || c.Controller.Gateway.Reconcile.MaxDelay.Std() <= 0 || c.Controller.Gateway.Reconcile.MaxDelay.Std() < c.Controller.Gateway.Reconcile.Debounce.Std() {
		problems = append(problems, "gateway reconcile debounce/max_delay are invalid")
	}
	if c.Controller.Discovery.OfflinePolicy != "retain-ready" && c.Controller.Discovery.OfflinePolicy != "drain-after-timeout" {
		problems = append(problems, "offline_policy must be retain-ready or drain-after-timeout")
	}
	if (c.Server.HTTPTLSCertFile == "") != (c.Server.HTTPTLSKeyFile == "") {
		problems = append(problems, "HTTP TLS requires both http_tls_cert_file and http_tls_key_file")
	}
	if c.Server.ConsoleHTTPSListen != "" {
		_, port, err := net.SplitHostPort(c.Server.ConsoleHTTPSListen)
		if err != nil || port == "" || c.Server.ConsoleHTTPSListen == c.Server.HTTPListen || c.Server.ConsoleHTTPSCertFile == "" || c.Server.ConsoleHTTPSKeyFile == "" {
			problems = append(problems, "independent console HTTPS requires a distinct listener and a certificate/key pair")
		}
	} else if c.Server.ConsoleHTTPSCertFile != "" || c.Server.ConsoleHTTPSKeyFile != "" {
		problems = append(problems, "console HTTPS certificate files require console_https_listen")
	}
	if !c.Controller.RecoveryMode && c.Server.GRPCListen != "" && (c.Server.GRPCTLSCertFile == "" || c.Server.GRPCTLSKeyFile == "" || c.Server.GRPCClientCAFile == "") {
		problems = append(problems, "gRPC requires grpc_tls_cert_file, grpc_tls_key_file, and grpc_client_ca_file")
	}
	if c.Server.RateLimitRPS <= 0 || c.Server.RateLimitBurst < 1 {
		problems = append(problems, "server rate_limit_rps and rate_limit_burst must be positive")
	}
	if c.ACME.DirectoryURL != "" && !validAbsoluteHTTPURL(c.ACME.DirectoryURL) {
		problems = append(problems, "acme.directory_url must be an absolute HTTP(S) URL")
	}
	if c.ACME.AcceptTerms && (strings.TrimSpace(c.ACME.Email) == "" || c.ACME.DirectoryURL == "") {
		problems = append(problems, "accepted ACME terms require email and directory_url")
	}
	if !i18n.IsSupportedCode(c.I18N.DefaultLanguage) {
		problems = append(problems, "i18n.default_language must be en, ja, de, zh-CN, zh-TW, fr, ru, or es")
	}
	for reference, profile := range c.ACME.DNSProviders {
		if err := validateDNSProviderProfile(reference, profile); err != nil {
			problems = append(problems, err.Error())
		}
	}
	for reference, credential := range c.Credentials {
		if strings.TrimSpace(reference) == "" || credential.CAFile == "" || credential.CertFile == "" || credential.KeyFile == "" {
			problems = append(problems, "each Docker credential requires a non-empty reference, ca_file, cert_file, and key_file")
			break
		}
	}
	for reference, credential := range c.Registries {
		if strings.TrimSpace(reference) == "" || strings.TrimSpace(credential.ConfigFile) == "" || !filepath.IsAbs(credential.ConfigFile) {
			problems = append(problems, "each registry credential requires a non-empty reference and absolute config_file")
			break
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func validateDNSProviderProfile(reference string, profile DNSProviderProfile) error {
	if strings.TrimSpace(profile.CredentialsFile) == "" {
		return fmt.Errorf("DNS provider %q credentials_file is required", reference)
	}
	return ValidateDNSProviderCredentials(reference, profile)
}

// ValidateDNSProviderCredentials validates both file-backed and encrypted
// control-plane profiles before either can be selected for ACME issuance.
func ValidateDNSProviderCredentials(reference string, profile DNSProviderProfile) error {
	if reference == "" || strings.TrimSpace(reference) != reference || len(reference) > 128 {
		return fmt.Errorf("DNS provider reference %q must contain 1 to 128 characters without surrounding whitespace", reference)
	}
	for _, char := range reference {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' && char != '.' {
			return fmt.Errorf("DNS provider reference %q may contain only letters, digits, dot, dash, and underscore", reference)
		}
	}
	if profile.TTL < 0 {
		return fmt.Errorf("DNS provider %q ttl cannot be negative", reference)
	}
	if profile.Provider == "cloudflare" && profile.TTL > 0 && profile.TTL < 120 {
		return fmt.Errorf("DNS provider %q Cloudflare ttl must be at least 120 seconds", reference)
	}
	for name, value := range map[string]Duration{
		"propagation_timeout": profile.PropagationTimeout,
		"polling_interval":    profile.PollingInterval,
		"http_timeout":        profile.HTTPTimeout,
		"sequence_interval":   profile.SequenceInterval,
	} {
		if value.Std() < 0 {
			return fmt.Errorf("DNS provider %q %s cannot be negative", reference, name)
		}
	}
	if profile.BaseURL != "" && !validAbsoluteHTTPURL(profile.BaseURL) {
		return fmt.Errorf("DNS provider %q base_url must be an absolute HTTP(S) URL", reference)
	}
	credentials := profile.Credentials
	switch profile.Provider {
	case "alidns":
		if credentials.RAMRole == "" && (credentials.AccessKey == "" || credentials.SecretKey == "") {
			return fmt.Errorf("DNS provider %q requires ram_role or access_key and secret_key", reference)
		}
	case "cloudflare":
		if credentials.APIToken == "" && (credentials.Email == "" || credentials.APIKey == "") {
			return fmt.Errorf("DNS provider %q requires api_token or email and api_key", reference)
		}
	case "tencentcloud":
		if credentials.SecretID == "" || credentials.SecretKey == "" {
			return fmt.Errorf("DNS provider %q requires secret_id and secret_key", reference)
		}
	case "hurricane":
		if len(credentials.Tokens) == 0 {
			return fmt.Errorf("DNS provider %q requires a non-empty tokens map", reference)
		}
		for domain, token := range credentials.Tokens {
			if strings.TrimSpace(domain) == "" || token == "" {
				return fmt.Errorf("DNS provider %q tokens must use non-empty domain names and values", reference)
			}
		}
	case "ovh":
		if profile.Region != "" && profile.Region != "ovh-eu" && profile.Region != "ovh-ca" {
			return fmt.Errorf("DNS provider %q OVH region must be ovh-eu or ovh-ca", reference)
		}
		if credentials.ApplicationKey == "" || credentials.ApplicationSecret == "" || credentials.ConsumerKey == "" {
			return fmt.Errorf("DNS provider %q requires application_key, application_secret and consumer_key", reference)
		}
	default:
		return fmt.Errorf("DNS provider %q provider must be alidns, cloudflare, tencentcloud, hurricane, or ovh", reference)
	}
	return nil
}

func (c Config) ValidateAgent() error {
	var problems []string
	if strings.TrimSpace(c.Agent.GatewayID) == "" {
		problems = append(problems, "agent.gateway_id is required")
	} else if err := id.Validate(c.Agent.GatewayID, id.Gateway); err != nil {
		problems = append(problems, err.Error())
	}
	if c.Agent.Mode != "" && c.Agent.Mode != "production" && c.Agent.Mode != "sandbox" {
		problems = append(problems, "agent.mode must be production or sandbox")
	}
	if strings.TrimSpace(c.Agent.PoolID) == "" {
		problems = append(problems, "agent.pool_id is required")
	} else if err := id.Validate(c.Agent.PoolID, id.Pool); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(c.Agent.ControllerAddress) == "" {
		problems = append(problems, "agent.controller_address is required")
	}
	if c.Agent.PublicAddress != "" && !validGatewayPublicAddress(c.Agent.PublicAddress) {
		problems = append(problems, "agent.public_address must be an IP address or DNS hostname without a scheme or port")
	}
	if strings.TrimSpace(c.Agent.ServerName) == "" {
		problems = append(problems, "agent.server_name is required for TLS verification")
	}
	if c.Agent.CACertFile == "" || c.Agent.TLSCertFile == "" || c.Agent.TLSKeyFile == "" {
		problems = append(problems, "agent.ca_cert_file, tls_cert_file, and tls_key_file are required")
	}
	if c.Agent.StateDir == "" || c.NGINX.GenerationDir == "" || c.NGINX.MainConfigPath == "" || c.NGINX.Binary == "" {
		problems = append(problems, "agent state_dir and NGINX binary/generation_dir are required")
	}
	if c.Agent.ProbeWorkers < 1 {
		problems = append(problems, "agent.probe_workers must be positive")
	}
	if c.Agent.HeartbeatInterval.Std() <= 0 {
		problems = append(problems, "agent.heartbeat_interval must be positive")
	}
	if c.NGINX.HTTP3AdvertisedPort < 1 || c.NGINX.HTTP3AdvertisedPort > 65535 {
		problems = append(problems, "NGINX http3_advertised_port must be in 1..65535")
	}
	if c.NGINX.KeepGenerations < 3 {
		problems = append(problems, "NGINX keep_generations must be at least 3")
	}
	if c.NGINX.WorkerProcesses != "auto" {
		workers, err := strconv.Atoi(c.NGINX.WorkerProcesses)
		if err != nil || workers < 1 || workers > 1024 {
			problems = append(problems, "NGINX worker_processes must be auto or an integer in 1..1024")
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func validReadinessMode(value model.ReadinessMode) bool {
	switch value {
	case model.ReadinessAuto, model.ReadinessHTTP, model.ReadinessDocker, model.ReadinessTCP, model.ReadinessGrace:
		return true
	default:
		return false
	}
}

func validAbsoluteHTTPURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func validGatewayPublicAddress(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "/\\@#?* \t\r\n") {
		return false
	}
	if net.ParseIP(value) != nil {
		return true
	}
	value = strings.TrimSuffix(value, ".")
	if value == "" || len(value) > 253 || strings.Contains(value, ":") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}
