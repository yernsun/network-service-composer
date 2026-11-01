package controller

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestInstallationHealthHandlerSeparatesMaintenanceFromControllerAuthority(t *testing.T) {
	cfg := controllerConfigFixture(t)
	cfg.Controller.Installation.MetadataDirectory = "/var/lib/nsc-bootstrap"
	cfg.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: "controller", NodeID: "node", EngineID: "engine", ClusterID: "cluster"}}
	store, err := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
	if err != nil {
		t.Fatal(err)
	}
	application := core.New(store, cfg, ring)
	controller := &Controller{config: cfg, store: store, core: application}
	controller.configureInstallation()
	if application.Installation == nil {
		t.Fatal("configured maintenance provider missing")
	}
	controller.config.Controller.Installation.MetadataDirectory = ""
	controller.core.Installation = nil
	controller.configureInstallation()
	if controller.core.Installation != nil {
		t.Fatal("unprepared installation gained a provider")
	}
	uri, _ := controlplane.MaintenanceIdentityURI("installation", "maintenance")
	peer := &x509.Certificate{URIs: []*url.URL{uri}, Raw: []byte("fixture-ca")}
	sum := sha256.Sum256(peer.Raw)
	owner := bootstrap.Installation{ID: "installation", Revision: 1, ClusterID: "cluster", PoolID: "pool", RuntimeID: "runtime", MaintenanceID: "maintenance", MaintenanceAnchor: "anchor", TrustSHA256: hex.EncodeToString(sum[:]), Database: bootstrap.DatabasePin{NodeID: "node", EngineID: "engine", ServiceID: "postgresql", Volume: "volume", SinglePointOfFailure: true}, Components: map[string]bootstrap.Resource{}}
	for _, key := range []string{"controller", "postgresql", "nginx", "nginx-sandbox"} {
		owner.Components[key] = bootstrap.Resource{Kind: "service", ID: key, SpecDigest: "owned"}
	}
	data, _ := json.Marshal(owner)
	document := storage.ControlDocument{ID: bootstrap.InstallationDocumentID, Kind: bootstrap.InstallationDocumentKind, ParentID: owner.ID, Payload: data, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err = store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: document}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	reports := &nodecontrol.Reports{Store: store, Keys: ring, Bindings: map[string]nodecontrol.Binding{"controller": {InstanceID: "controller", NodeID: "node", EngineID: "engine", ClusterID: "cluster"}}, Now: func(context.Context) (time.Time, error) { return now, nil }, CheckOwner: func(context.Context, string, int64) error { return nil }}
	session, err := reports.Begin(t.Context(), "controller", "node", "engine", "boot", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = reports.Accept(t.Context(), "controller", nodecontrol.Report{NodeID: "node", EngineID: "engine", Generation: session.Generation, Sequence: 1, Observation: nodecontrol.Observation{Info: system.Info{ID: "engine", Swarm: swarm.Info{NodeID: "node", ControlAvailable: true, Cluster: &swarm.ClusterInfo{ID: "cluster"}}}, At: now, Complete: true}}); err != nil {
		t.Fatal(err)
	}
	application.HAReports = reports
	for _, scenario := range []string{"binding", "health", "method", "no-tls", "no-chain", "controller", "foreign-installation", "foreign-maintenance", "foreign-ca", "bad-query", "multiple-query", "no-reports", "no-peer", "metadata-payload", "metadata-kind", "metadata-parent"} {
		t.Run(scenario, func(t *testing.T) {
			if _, err := store.DB().ExecContext(t.Context(), "UPDATE control_documents SET kind=?, parent_id=?, payload=? WHERE id=?", document.Kind, document.ParentID, document.Payload, document.ID); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("GET", "/internal/v1/installation/health?scope=identity", nil)
			request.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{peer}, VerifiedChains: [][]*x509.Certificate{{peer}}}
			application.HAReports = reports
			switch scenario {
			case "health":
				request.URL.RawQuery = ""
			case "method":
				request.Method = "POST"
			case "no-tls":
				request.TLS = nil
			case "no-chain":
				request.TLS.VerifiedChains = nil
			case "controller":
				u, _ := controlplane.IdentityURI("controller")
				request.TLS.PeerCertificates = []*x509.Certificate{{URIs: []*url.URL{u}}}
			case "foreign-installation":
				u, _ := controlplane.MaintenanceIdentityURI("other", "maintenance")
				request.TLS.PeerCertificates = []*x509.Certificate{{URIs: []*url.URL{u}}}
			case "foreign-maintenance":
				u, _ := controlplane.MaintenanceIdentityURI("installation", "other")
				request.TLS.PeerCertificates = []*x509.Certificate{{URIs: []*url.URL{u}}}
			case "foreign-ca":
				request.TLS.VerifiedChains = [][]*x509.Certificate{{{Raw: []byte("other")}}}
			case "bad-query":
				request.URL.RawQuery = "scope=arbitrary"
			case "multiple-query":
				request.URL.RawQuery = "scope=identity&arbitrary=value"
			case "no-peer":
				request.TLS.PeerCertificates = nil
			case "metadata-payload", "metadata-kind", "metadata-parent":
				kind, parent, payload := document.Kind, document.ParentID, document.Payload
				if scenario == "metadata-payload" {
					payload = []byte("{")
				}
				if scenario == "metadata-kind" {
					kind = "foreign"
				}
				if scenario == "metadata-parent" {
					parent = "foreign"
				}
				if _, err := store.DB().ExecContext(t.Context(), "UPDATE control_documents SET kind=?, parent_id=?, payload=? WHERE id=?", kind, parent, payload, document.ID); err != nil {
					t.Fatal(err)
				}
			case "no-reports":
				application.HAReports = nil
			}
			response := httptest.NewRecorder()
			controller.installationHealthHandler().ServeHTTP(response, request)
			if scenario == "binding" {
				if response.Code != http.StatusNoContent {
					t.Fatal("degraded health blocked physical identity verification", response.Code)
				}
			} else if response.Code < 400 {
				t.Fatal("unverified maintenance evidence passed", scenario, response.Code)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("maintenance evidence could be cached")
			}
		})
	}
}
