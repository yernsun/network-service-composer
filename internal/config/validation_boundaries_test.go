package config

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
)

func setConfigField(t *testing.T, config *Config, path string, value any) {
	t.Helper()
	field := reflect.ValueOf(config).Elem()
	for _, name := range strings.Split(path, ".") {
		field = field.FieldByName(name)
	}
	if !field.IsValid() || !field.CanSet() {
		t.Fatalf("invalid fixture field %s", path)
	}
	field.Set(reflect.ValueOf(value).Convert(field.Type()))
}

func TestControllerValidationRejectsEachUnsafeSetting(t *testing.T) {
	tests := []struct {
		field   string
		value   any
		message string
	}{
		{"Server.HTTPListen", " ", "http_listen"}, {"Metrics.Listen", " ", "metrics.listen"},
		{"Server.ReadTimeout", 0, "read_timeout"}, {"Server.WriteTimeout", 0, "write_timeout"},
		{"Storage.Driver", "unknown", "storage.driver"}, {"Storage.DSN", "", "storage.dsn"},
		{"Management.DockerCLI", " ", "management.docker_cli"}, {"Management.WorkDir", " ", "management.work_dir"},
		{"Management.PollInterval", 0, "management intervals"}, {"Management.HeartbeatInterval", 0, "management intervals"},
		{"Management.StaleAfter", 0, "management intervals"}, {"Management.DeployTimeout", 0, "management intervals"},
		{"Security.AdminToken", " ", "admin_token"}, {"Security.AdminUsername", " ", "admin_username"},
		{"Security.AdminUsername", strings.Repeat("a", 129), "admin_username"}, {"Security.SessionKey", strings.Repeat("a", 31), "session_key"},
		{"Security.PrimaryKeyID", "", "primary_key_id"}, {"Security.PrimaryKeyID", "missing", "primary_key_id"},
		{"Controller.Endpoint.Readiness.Mode", "unsupported", "readiness mode"},
		{"Controller.Endpoint.Readiness.Interval", 0, "readiness intervals"}, {"Controller.Endpoint.Readiness.Timeout", 0, "readiness intervals"},
		{"Controller.Endpoint.Readiness.Timeout", time.Hour, "readiness intervals"},
		{"Controller.Endpoint.Readiness.SuccessThreshold", 0, "readiness intervals"}, {"Controller.Endpoint.Readiness.FailureThreshold", 0, "readiness intervals"},
		{"Controller.Endpoint.Readiness.StartupGracePeriod", -1, "readiness intervals"}, {"Controller.Endpoint.Readiness.StartupTimeout", 0, "readiness intervals"},
		{"Controller.Endpoint.Readiness.Scheme", "ftp", "readiness scheme"},
		{"Controller.Discovery.Reconcile.Interval", 0, "discovery reconcile"}, {"Controller.Discovery.FullReconcile.Interval", 0, "discovery reconcile"},
		{"Controller.Discovery.StaleEndpointTimeout", 0, "discovery reconcile"},
		{"Controller.Endpoint.DrainTimeout", -1, "controller lifecycle"}, {"Controller.Gateway.OfflineTimeout", 0, "controller lifecycle"},
		{"Controller.Certificate.ReconcileInterval", 0, "controller lifecycle"}, {"Controller.Certificate.RenewBeforeExpiry", 0, "controller lifecycle"},
		{"Controller.Discovery.Events.Debounce", -1, "event debounce"}, {"Controller.Discovery.Events.MaxDelay", 0, "event debounce"},
		{"Controller.Discovery.Events.Debounce", time.Hour, "event debounce"},
		{"Controller.Gateway.PeriodicInterval", 0, "GC retention"}, {"Controller.GC.EndpointRetention", 0, "GC retention"},
		{"Controller.GC.FailedCertificateRetention", 0, "GC retention"},
		{"Controller.Gateway.Reconcile.Debounce", -1, "gateway reconcile"}, {"Controller.Gateway.Reconcile.MaxDelay", 0, "gateway reconcile"},
		{"Controller.Gateway.Reconcile.Debounce", time.Hour, "gateway reconcile"},
		{"Controller.Discovery.OfflinePolicy", "delete", "offline_policy"},
		{"Server.HTTPTLSCertFile", "cert", "HTTP TLS"}, {"Server.HTTPTLSKeyFile", "key", "HTTP TLS"},
		{"Server.RateLimitRPS", 0, "rate_limit_rps"}, {"Server.RateLimitBurst", 0, "rate_limit_burst"},
		{"ACME.DirectoryURL", "file:///tmp/acme", "acme.directory_url"},
	}
	for _, tc := range tests {
		t.Run(tc.field+"/"+tc.message, func(t *testing.T) {
			c := validConfig()
			setConfigField(t, &c, tc.field, tc.value)
			if err := c.Validate(); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("unsafe %s accepted or misdiagnosed: %v", tc.field, err)
			}
		})
	}
	for _, names := range [][]string{{""}, {" padded"}, {"same", "same"}} {
		for _, field := range []string{"Management.ExternalSecrets", "Management.ExternalConfigs"} {
			c := validConfig()
			setConfigField(t, &c, field, names)
			if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "management.external_") {
				t.Fatalf("invalid external dependency %v accepted: %v", names, err)
			}
		}
	}
	for _, field := range []string{"Server.GRPCTLSCertFile", "Server.GRPCTLSKeyFile", "Server.GRPCClientCAFile"} {
		c := validConfig()
		c.Server.GRPCListen = ":9443"
		c.Server.GRPCTLSCertFile = "cert"
		c.Server.GRPCTLSKeyFile = "key"
		c.Server.GRPCClientCAFile = "ca"
		setConfigField(t, &c, field, "")
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "gRPC requires") {
			t.Fatalf("incomplete mTLS accepted: %v", err)
		}
	}
	for _, missing := range []string{"ACME.Email", "ACME.DirectoryURL"} {
		c := validConfig()
		c.ACME.AcceptTerms = true
		c.ACME.Email = "admin@example.test"
		setConfigField(t, &c, missing, "")
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "accepted ACME terms") {
			t.Fatalf("incomplete ACME accepted: %v", err)
		}
	}
	c := validConfig()
	c.Controller.Endpoint.Readiness.Mode = model.ReadinessHTTP
	c.Controller.Endpoint.Readiness.Path = "relative"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "path must be absolute") {
		t.Fatalf("relative probe path accepted: %v", err)
	}
}

func validAgentConfig() Config {
	c := Defaults()
	c.Agent.GatewayID = id.New(id.Gateway)
	c.Agent.PoolID = id.New(id.Pool)
	c.Agent.ControllerAddress = "controller:9443"
	c.Agent.ServerName = "controller"
	c.Agent.CACertFile = "ca"
	c.Agent.TLSCertFile = "cert"
	c.Agent.TLSKeyFile = "key"
	return c
}
func TestAgentValidationBoundaries(t *testing.T) {
	for _, mode := range []string{"", "production", "sandbox"} {
		for _, workers := range []string{"auto", "1", "1024"} {
			c := validAgentConfig()
			c.Agent.Mode = mode
			c.NGINX.WorkerProcesses = workers
			c.Agent.PublicAddress = "edge.example.test"
			if err := c.ValidateAgent(); err != nil {
				t.Fatalf("valid agent rejected: %v", err)
			}
		}
	}
	tests := []struct {
		field   string
		value   any
		message string
	}{
		{"Agent.GatewayID", " ", "gateway_id"}, {"Agent.PoolID", " ", "pool_id"}, {"Agent.Mode", "observer", "agent.mode"},
		{"Agent.ControllerAddress", " ", "controller_address"}, {"Agent.PublicAddress", "https://edge.test", "public_address"},
		{"Agent.ServerName", " ", "server_name"}, {"Agent.CACertFile", "", "ca_cert_file"}, {"Agent.TLSCertFile", "", "tls_cert_file"}, {"Agent.TLSKeyFile", "", "tls_key_file"},
		{"Agent.StateDir", "", "state_dir"}, {"NGINX.GenerationDir", "", "generation_dir"}, {"NGINX.MainConfigPath", "", "generation_dir"}, {"NGINX.Binary", "", "binary"},
		{"Agent.ProbeWorkers", 0, "probe_workers"}, {"Agent.HeartbeatInterval", 0, "heartbeat_interval"},
		{"NGINX.HTTP3AdvertisedPort", 0, "http3_advertised_port"}, {"NGINX.HTTP3AdvertisedPort", 65536, "http3_advertised_port"},
		{"NGINX.KeepGenerations", 2, "keep_generations"}, {"NGINX.WorkerProcesses", "zero", "worker_processes"}, {"NGINX.WorkerProcesses", "0", "worker_processes"}, {"NGINX.WorkerProcesses", "1025", "worker_processes"},
	}
	for _, tc := range tests {
		t.Run(tc.field, func(t *testing.T) {
			c := validAgentConfig()
			setConfigField(t, &c, tc.field, tc.value)
			if err := c.ValidateAgent(); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("unsafe agent accepted or misdiagnosed: %v", err)
			}
		})
	}
}

func TestEndpointAddressValidationBoundaries(t *testing.T) {
	for _, value := range []string{"http://example.test", "https://example.test/path", " http://example.test "} {
		if !validAbsoluteHTTPURL(value) {
			t.Errorf("valid URL rejected: %q", value)
		}
	}
	for _, value := range []string{"%", "ftp://example.test", "http:///path", "http://user@example.test", "http://example.test?q=1", "http://example.test#fragment"} {
		if validAbsoluteHTTPURL(value) {
			t.Errorf("unsafe URL accepted: %q", value)
		}
	}
	for _, value := range []string{".", strings.Repeat("a", 254), "a..test", strings.Repeat("a", 64) + ".test", "-a.test", "a-.test", "a_1.test", "é.test"} {
		if validGatewayPublicAddress(value) {
			t.Errorf("invalid address accepted: %q", value)
		}
	}
	for _, value := range []string{"Az09-az.test", "a", strings.Repeat("a", 63) + ".test"} {
		if !validGatewayPublicAddress(value) {
			t.Errorf("valid address rejected: %q", value)
		}
	}
}
