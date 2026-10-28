package controller

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/pki"
)

// Construction does not start workers or access a Docker socket. Shared-store
// admission is measured against explicitly owned SQL fixtures, separately from
// the real Controller/Engine fault exercises.
func TestNodeExecutionControllerConstructionAcrossDatabases(t *testing.T) {
	for _, driver := range []string{"postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			dsn := os.Getenv(map[string]string{"postgres": "NSC_TEST_EXECUTION_POSTGRES_DSN", "mysql": "NSC_TEST_EXECUTION_MYSQL_DSN"}[driver])
			if dsn == "" {
				t.Skip("isolated execution store not configured")
			}
			cfg := controllerConfigFixture(t)
			cfg.Storage.Driver, cfg.Storage.DSN, cfg.Storage.HA = driver, dsn, true
			cfg.Security.SessionKey = "isolated-controller-session-key-at-least-32-bytes"
			cfg.Controller.InstanceID = "execution-admission-controller"
			directory := t.TempDir()
			ca, caKey := filepath.Join(directory, "ca.pem"), filepath.Join(directory, "ca.key")
			if err := pki.InitCA(ca, caKey, "isolated executor admission", time.Hour); err != nil {
				t.Fatal(err)
			}
			identity, err := controlplane.IdentityURI(cfg.Controller.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			cert, key := filepath.Join(directory, "controller.pem"), filepath.Join(directory, "controller.key")
			if err = pki.Issue(ca, caKey, cert, key, pki.CertificateOptions{CommonName: cfg.Controller.InstanceID, URIs: []*url.URL{identity}, DNSNames: []string{"controller.example.test"}, Client: true, Server: true, Duration: time.Hour}); err != nil {
				t.Fatal(err)
			}
			cfg.Controller.Internal = config.ControllerInternalConfig{Listen: "127.0.0.1:0", URL: "https://controller.example.test:8443", CAFile: ca, CertFile: cert, KeyFile: key}
			cfg.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: cfg.Controller.InstanceID, NodeID: "fixture-node", EngineID: "fixture-engine", ClusterID: "fixture-cluster", URL: cfg.Controller.Internal.URL}}
			cfg.Management.Enabled = true
			controller, err := New(t.Context(), cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer controller.store.Close()
			if controller.deploymentWorker == nil || controller.localCollector == nil || controller.nodeReports == nil {
				t.Fatal("configured node execution was not installed")
			}
			if health := controller.localCollector.Health(t.Context()); health.Execution != "disabled" {
				t.Fatal("unstarted executor advertised capability", health.Execution)
			}
			controller.ready.Store(true)
			if health := controller.localCollector.Health(t.Context()); health.Execution != "enabled" {
				t.Fatal("ready executor did not advertise operation capability", health.Execution)
			}
			invalid := cfg
			invalid.Security.SessionKey = "too-short"
			if rejected, err := New(t.Context(), invalid, nil); err == nil || rejected != nil {
				if rejected != nil {
					rejected.store.Close()
				}
				t.Fatal("unsafe executor admission did not fail closed")
			}
		})
	}
}
