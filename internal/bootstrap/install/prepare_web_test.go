package install

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/yernsun/network-service-composer/internal/pki"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/bootstrap/maintenance"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/storage"
	"gopkg.in/yaml.v3"
)

type preparationHost struct {
	*fakeHost
	installer *Installer
	prepared  map[string][]byte
	lostFiles bool
}

func (h *preparationHost) Run(ctx context.Context, command string, input []byte) ([]byte, error) {
	if strings.Contains(command, h.installer.preparedNodeDirectory()) {
		if strings.Contains(command, "tar -xpf") {
			if h.prepared != nil {
				return nil, errors.New("existing prepared directory")
			}
			h.prepared = map[string][]byte{}
			reader := tar.NewReader(bytes.NewReader(input))
			for {
				header, err := reader.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return nil, err
				}
				if header.Typeflag == tar.TypeDir {
					continue
				}
				data, err := io.ReadAll(reader)
				if err != nil {
					return nil, err
				}
				h.prepared[header.Name] = data
			}
			// Match the PKI writer under the normal 022 operator umask even
			// when the enclosing test runner uses a restrictive umask.
			if err := os.Chmod(h.installer.material("maintenance.crt"), 0644); err != nil {
				return nil, err
			}
			if h.lostFiles {
				h.lostFiles = false
				return nil, context.DeadlineExceeded
			}
			return nil, nil
		}
		if strings.Contains(command, "sha256sum -c") {
			var manifest map[string]string
			if json.Unmarshal(h.prepared["manifest.json"], &manifest) != nil {
				return nil, errors.New("missing manifest")
			}
			for name, expected := range manifest {
				sum := sha256.Sum256(h.prepared[name])
				if hex.EncodeToString(sum[:]) != expected {
					return nil, errors.New("changed prepared file")
				}
			}
			return h.prepared["manifest.json"], nil
		}
		if strings.Contains(command, "cat -- ") {
			path := strings.Trim(command[strings.LastIndex(command, "cat -- ")+len("cat -- "):], "'")
			relative := strings.TrimPrefix(path, h.installer.preparedNodeDirectory()+"/")
			if data, ok := h.prepared[relative]; ok {
				return bytes.Clone(data), nil
			}
			return nil, os.ErrNotExist
		}
	}
	return h.fakeHost.Run(ctx, command, input)
}
func (h *preparationHost) Engine(ctx context.Context, method, path string, input, output any) error {
	parsed, _ := url.Parse(path)
	if method == "GET" && parsed.Path == "/tasks" {
		var filters map[string][]string
		json.Unmarshal([]byte(parsed.Query().Get("filters")), &filters)
		selected := filters["service"]
		if len(selected) > 0 {
			service := h.cluster.services[selected[0]]
			task := swarm.Task{ID: "owned-task", ServiceID: service.ID, NodeID: h.nodeID, Spec: service.Spec.TaskTemplate, Status: swarm.TaskStatus{State: swarm.TaskStateRunning, ContainerStatus: &swarm.ContainerStatus{ContainerID: "pinned-data-container"}}}
			return encode([]swarm.Task{task}, output)
		}
	}
	return h.fakeHost.Engine(ctx, method, path, input, output)
}

func webPreparationFixture(t *testing.T) (*Installer, *preparationHost, *consoleMode) {
	t.Helper()
	original, mode := consoleFixture(t)
	originalDirectory := original.Directory
	base := original.Hosts["one"].(*fakeHost)
	cluster := base.cluster
	adopted, _ := adoptionFromOriginal(t, original, cluster)
	rawConfig := config.Defaults()
	if err := yaml.Unmarshal(base.files["controller.yaml"], &rawConfig); err != nil {
		t.Fatal(err)
	}
	executor := original.Record.Resources["reserve:executor"].ID
	services := rawConfig.Controller.SystemProject.Services[:0]
	for _, service := range rawConfig.Controller.SystemProject.Services {
		if service.ObjectID != executor {
			services = append(services, service)
		}
	}
	rawConfig.Controller.SystemProject.Services = services
	for index := range rawConfig.Controller.SystemProject.Applications {
		values := []string{}
		for _, value := range rawConfig.Controller.SystemProject.Applications[index].ObjectIDs {
			if value != executor {
				values = append(values, value)
			}
		}
		rawConfig.Controller.SystemProject.Applications[index].ObjectIDs = values
	}
	rawConfig.Controller.Installation.MetadataDirectory = ""
	rawConfig.Server.ConsoleHTTPSListen = ""
	rawConfig.Server.ConsoleHTTPSCertFile = ""
	rawConfig.Server.ConsoleHTTPSKeyFile = ""
	raw, _ := yaml.Marshal(rawConfig)
	base.files["controller.yaml"] = raw
	if err := os.WriteFile(adopted.Config.Adoption.ControllerConfigFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	service := cluster.services[adopted.Config.Adoption.ServiceIDs["controller"]]
	endpoint := *service.Spec.EndpointSpec
	endpoint.Ports = []swarm.PortConfig{}
	for _, port := range service.Spec.EndpointSpec.Ports {
		if port.TargetPort != 8443 {
			endpoint.Ports = append(endpoint.Ports, port)
		}
	}
	service.Spec.EndpointSpec = &endpoint
	cluster.services[service.ID] = service
	plan, err := adopted.Plan(t.Context(), bootstrap.Adopt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (bootstrap.Executor{Journal: adopted.Journal, Driver: adopted}).Execute(t.Context(), adopted.Record, plan, plan.Hash, "adopt-preparation-fixture"); err != nil {
		t.Fatal(err)
	}
	// The console fixture's handler follows this operator-owned installation. Its
	// TLS certificate is loaded per handshake so the new independent leaf is real.
	*original = *adopted
	installer := original
	installer.OriginalCACertificateFile = filepath.Join(originalDirectory+".offline", "materials", "ca.crt")
	installer.OriginalCAPrivateKeyFile = filepath.Join(originalDirectory+".offline", "materials", "ca.key")
	target := bootstrap.Release{Version: "v0.1.0", Images: installer.Config.Images, Protocol: 2, Schema: 18, Configuration: 1, PostgreSQLMajor: 18, Architectures: []string{"amd64", "arm64"}}
	installer.Target = &target
	installer.PrepareWeb = true
	host := &preparationHost{fakeHost: base, installer: installer}
	installer.Hosts["one"] = host
	base.stream = func(_ context.Context, command string) (net.Conn, error) {
		if strings.Contains(command, "pg_dump") {
			return &archiveConn{Reader: bytes.NewReader([]byte("PGDMPisolated-preparation-fixture"))}, nil
		}
		if !strings.Contains(command, "pg_restore --list") {
			return nil, errors.New("unapproved fixture stream")
		}
		return &archiveConn{Reader: bytes.NewReader(nil), Writer: io.Discard}, nil
	}
	ca, err := os.ReadFile(installer.OriginalCACertificateFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	controllerPair, err := tls.LoadX509KeyPair(filepath.Join(originalDirectory+".offline", "materials", "one-controller.crt"), filepath.Join(originalDirectory+".offline", "materials", "one-controller.key"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:9444")
	if err != nil {
		t.Fatal(err)
	}
	internal := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) == 0 {
			w.WriteHeader(403)
			return
		}
		installation, identity, err := controlplane.MaintenanceCertificateIdentity(r.TLS.PeerCertificates[0])
		if err != nil || installation != installer.Record.ID || identity != installer.Record.MaintenanceID || installer.Record.Resources["prepare:controller"].ID == "" {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	internal.Listener = listener
	internal.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots, Certificates: []tls.Certificate{controllerPair}}
	internal.StartTLS()
	t.Cleanup(internal.Close)
	listenerHTTP, err := net.Listen("tcp", "127.0.0.1:80")
	if err != nil {
		t.Fatal(err)
	}
	ingress := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Header().Set("Server", "nginx"); w.WriteHeader(404) }))
	ingress.Listener = listenerHTTP
	ingress.Start()
	t.Cleanup(ingress.Close)
	return installer, host, mode
}

func TestLegacyHTTPPreparationPreservesDataIdentitiesAndEnablesRealMaintenance(t *testing.T) {
	installer, host, _ := webPreparationFixture(t)
	before := bootstrap.Digest(host.cluster.services)
	originalCA, err := os.ReadFile(installer.OriginalCAPrivateKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	var identities []byte
	if err = installer.withStore(t.Context(), true, func(store storage.Store, _ config.Config) error {
		doc, err := store.GetControlDocument(t.Context(), "community:identities")
		identities = bytes.Clone(doc.Payload)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	database := installer.Record.Database
	plan, err := installer.Plan(t.Context(), bootstrap.Upgrade)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Scope.Preparation == nil || plan.Scope.Preparation.TrustSHA256 == "" || bootstrap.Digest(host.cluster.services) != before || installer.Record.Preparation != nil {
		t.Fatal("planning changed ownership or omitted preparation proof")
	}
	operation, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "prepare-original-installation")
	if err != nil || operation.State != bootstrap.Succeeded {
		t.Fatal(operation.State, err)
	}
	if installer.Record.Database != database || len(installer.Record.History) != 0 || installer.Record.Release.Version != "v0.1.0" {
		t.Fatal("preparation moved data or invented an old rollback baseline")
	}
	after, err := os.ReadFile(installer.OriginalCAPrivateKeyFile)
	if err != nil || !bytes.Equal(originalCA, after) {
		t.Fatal("original CA was replaced", err)
	}
	var output bytes.Buffer
	if err = installer.Verify(t.Context(), &output); err != nil {
		t.Fatal("prepared installation did not verify", err)
	}
	if !strings.Contains(output.String(), "VERIFIED") {
		t.Fatal("prepared health is unverified")
	}
	if info, err := os.Stat(installer.material("maintenance.crt")); err != nil || info.Mode().Perm() != 0644 {
		t.Fatal("the public-certificate permission regression was not exercised", err)
	}
	if err = os.Chmod(installer.material("maintenance.key"), 0644); err != nil {
		t.Fatal(err)
	}
	err = installer.withStore(t.Context(), true, func(store storage.Store, _ config.Config) error {
		return installer.preparedHealth(t.Context(), store)
	})
	if err == nil {
		t.Fatal("a publicly readable private key was accepted")
	}
	if err = os.Chmod(installer.material("maintenance.key"), 0600); err != nil {
		t.Fatal(err)
	}
	for path, data := range host.prepared {
		if strings.Contains(path, "ca.key") || bytes.Contains(data, originalCA) {
			t.Fatal("offline CA entered runtime files")
		}
	}
	if err = installer.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error {
		doc, err := store.GetControlDocument(t.Context(), "community:identities")
		if err != nil {
			return err
		}
		if !bytes.Equal(doc.Payload, identities) {
			return errors.New("existing administrator was reset")
		}
		owner, _, err := (maintenance.Repository{Store: store}).Installation(t.Context())
		if err != nil {
			return err
		}
		if owner.Executor.ID == "" || owner.CredentialSecret.ID == "" || owner.CertificateID != installer.Record.CertificateID || owner.Health != "VERIFIED" {
			return errors.New("prepared maintenance was not registered")
		}
		runtime := func(record *bootstrap.Record) maintenance.Runtime {
			return &maintenance.DockerRuntime{Engine: host, Record: record, Schema: maintenance.SchemaVerifier(store), Physical: func(context.Context) error { return nil }, Health: func(context.Context) error { return nil }}
		}
		provider := maintenance.Provider{Repository: maintenance.Repository{Store: store}, Engine: host, Directory: installer.Directory, Runtime: runtime}
		value, err := provider.View(t.Context())
		if err != nil {
			return err
		}
		if len(value.AllowedOperations) < 3 {
			return errors.New("verified preparation did not enable maintenance plans")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	repeated, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "prepare-original-installation")
	if err != nil || repeated.ID != operation.ID {
		t.Fatal("duplicate preparation changed resources", err)
	}
	installer.PrepareWeb = false
	if _, err = installer.PlanMaintenance(t.Context(), bootstrap.Rollback); err == nil {
		t.Fatal("unverified legacy image gained automatic rollback rights")
	}
}

func TestPreparationReconcilesOnlyItsRecordedNodeFileIntentAfterLostReply(t *testing.T) {
	installer, host, _ := webPreparationFixture(t)
	host.lostFiles = true
	plan, err := installer.Plan(t.Context(), bootstrap.Upgrade)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "interrupted-preparation")
	if err == nil || operation.State != bootstrap.Unknown || host.prepared == nil {
		t.Fatal("lost file acknowledgement was not UNKNOWN", operation.State, err)
	}
	database := installer.Record.Database
	result, err := installer.ResumeMaintenance(t.Context(), operation.ID)
	if err != nil || result.ID != operation.ID || result.State != bootstrap.Succeeded || installer.Record.Database != database {
		t.Fatal("recorded preparation could not resume without replacing identities or data", result.State, err)
	}
}

func TestPreparationRejectsWrongCAStaleSourceAndAlreadyRegisteredCredentials(t *testing.T) {
	installer, host, _ := webPreparationFixture(t)
	plan, err := installer.Plan(t.Context(), bootstrap.Upgrade)
	if err != nil {
		t.Fatal(err)
	}
	original := bytes.Clone(host.files["controller.yaml"])
	host.files["controller.yaml"] = append(original, []byte("\n# external revision\n")...)
	before := bootstrap.Digest(host.cluster.services)
	if _, err = installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "stale-preparation"); err == nil {
		t.Fatal("changed source was overwritten")
	}
	host.files["controller.yaml"] = original
	if bootstrap.Digest(host.cluster.services) != before || installer.Record.Preparation != nil {
		t.Fatal("rejected plan changed system resources")
	}
	installer.OriginalCAPrivateKeyFile = filepath.Join(t.TempDir(), "missing")
	if _, err = installer.Plan(t.Context(), bootstrap.Upgrade); err == nil {
		t.Fatal("missing original CA gained a replacement identity")
	}
}

func TestLegacyPreparationPreflightPreservesResourcesOnUntrustedInputs(t *testing.T) {
	for _, scenario := range []string{"no-release", "no-adoption", "old-layout", "registered-executor", "registered-secret", "registered-control", "registered-identity", "foreign-owner", "foreign-anchor", "foreign-cluster", "foreign-config", "database-change", "wrong-key", "different-ca", "not-a-ca", "missing-ca", "missing-key", "controller-user", "controller-root", "controller-gid", "controller-mode", "environment", "configuration-target", "configuration-copy", "network-missing", "network-ambiguous", "node-drained", "source-layout", "source-identity", "source-listen", "source-database", "source-registration", "controller-user-format", "controller-gid-name", "controller-gid-negative", "controller-mode-empty", "controller-mode-unknown", "data-node-worker", "source-driver", "source-runtime", "source-services", "source-dsn-scheme", "source-dsn-database", "source-dsn-user", "source-dsn-anonymous", "node-down", "quorum-lost"} {
		t.Run(scenario, func(t *testing.T) {
			installer, host, _ := webPreparationFixture(t)
			mutateOwner := func(change func(*bootstrap.Installation)) {
				if err := installer.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error {
					repository := maintenance.Repository{Store: store}
					value, doc, err := repository.Installation(t.Context())
					if err != nil {
						return err
					}
					change(&value)
					if value.ID != doc.ParentID {
						if err := store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: doc, Expected: doc.Revision, Remove: true}}); err != nil {
							return err
						}
						doc.Revision = 0
					}
					return store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: storage.ControlDocument{ID: doc.ID, Kind: doc.Kind, ParentID: value.ID, Payload: mustPreparationJSON(value), CreatedAt: doc.CreatedAt, UpdatedAt: timeNow()}, Expected: doc.Revision}})
				}); err != nil {
					t.Fatal(err)
				}
			}
			service := host.cluster.services[installer.Config.Adoption.ServiceIDs["controller"]]
			cfg := config.Defaults()
			if err := yaml.Unmarshal(host.files["controller.yaml"], &cfg); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "no-release":
				installer.Target = nil
			case "no-adoption":
				installer.Config.Adoption = nil
			case "old-layout":
				installer.Record.MetadataLayout = 0
			case "registered-executor":
				mutateOwner(func(value *bootstrap.Installation) { value.Executor.ID = "existing-executor" })
			case "registered-secret":
				mutateOwner(func(value *bootstrap.Installation) { value.CredentialSecret.ID = "existing-secret" })
			case "registered-control":
				mutateOwner(func(value *bootstrap.Installation) { value.Control.ID = "existing-control" })
			case "registered-identity":
				mutateOwner(func(value *bootstrap.Installation) {
					expiry := timeNow().Add(time.Hour)
					value.MaintenanceExpiresAt = &expiry
				})
			case "foreign-owner":
				mutateOwner(func(value *bootstrap.Installation) { value.ID = "foreign-installation" })
			case "foreign-anchor":
				mutateOwner(func(value *bootstrap.Installation) { value.MaintenanceAnchor = "foreign-anchor" })
			case "foreign-cluster":
				mutateOwner(func(value *bootstrap.Installation) { value.ClusterID = "foreign-cluster" })
			case "foreign-config":
				mutateOwner(func(value *bootstrap.Installation) { value.ConfigDigest = "foreign-configuration" })
			case "database-change":
				installer.Target.Images.PostgreSQL = "registry.example.test/postgres@sha256:" + strings.Repeat("b", 64)
			case "wrong-key", "different-ca":
				other := t.TempDir()
				cert, key := filepath.Join(other, "ca.crt"), filepath.Join(other, "ca.key")
				if err := pki.InitCA(cert, key, "Separate fixture CA", time.Hour); err != nil {
					t.Fatal(err)
				}
				installer.OriginalCAPrivateKeyFile = key
				if scenario == "different-ca" {
					installer.OriginalCACertificateFile = cert
				}
			case "not-a-ca":
				installer.OriginalCACertificateFile = filepath.Join(filepath.Dir(installer.OriginalCACertificateFile), "console.crt")
				installer.OriginalCAPrivateKeyFile = filepath.Join(filepath.Dir(installer.OriginalCAPrivateKeyFile), "console.key")
			case "missing-ca":
				installer.OriginalCACertificateFile = ""
			case "missing-key":
				installer.OriginalCAPrivateKeyFile = ""
			case "controller-user":
				service.Spec.TaskTemplate.ContainerSpec.User = "not-numeric:900"
			case "controller-user-format":
				service.Spec.TaskTemplate.ContainerSpec.User = "65532"
			case "controller-gid-name":
				service.Spec.TaskTemplate.ContainerSpec.User = "65532:docker"
			case "controller-gid-negative":
				service.Spec.TaskTemplate.ContainerSpec.User = "65532:-1"
			case "controller-mode-empty":
				service.Spec.Mode = swarm.ServiceMode{Replicated: &swarm.ReplicatedService{}}
			case "controller-mode-unknown":
				service.Spec.Mode = swarm.ServiceMode{}
			case "data-node-worker":
				installer.Config.Nodes[0].Manager = false
			case "controller-root":
				service.Spec.TaskTemplate.ContainerSpec.User = "0:900"
			case "controller-gid":
				service.Spec.TaskTemplate.ContainerSpec.User = "65532:901"
			case "controller-mode":
				replicas := uint64(2)
				service.Spec.Mode = swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &replicas}}
			case "environment":
				service.Spec.TaskTemplate.ContainerSpec.Env = []string{"NSC_STORAGE__DSN=external"}
			case "configuration-target":
				service.Spec.TaskTemplate.ContainerSpec.Args = []string{"serve"}
			case "configuration-copy":
				os.WriteFile(installer.Config.Adoption.ControllerConfigFile, append(bytes.Clone(host.files["controller.yaml"]), []byte("\n# stale copy\n")...), 0600)
			case "network-missing":
				service.Spec.TaskTemplate.Networks = nil
			case "network-ambiguous":
				pg := host.cluster.services[installer.Record.Database.ServiceID]
				service.Spec.TaskTemplate.Networks = append(service.Spec.TaskTemplate.Networks, pg.Spec.TaskTemplate.Networks[1])
			case "node-drained":
				node := host.cluster.nodes[host.nodeID]
				node.Spec.Availability = swarm.NodeAvailabilityDrain
				host.cluster.nodes[host.nodeID] = node
			case "node-down", "quorum-lost":
				node := host.cluster.nodes[host.nodeID]
				if scenario == "node-down" {
					node.Status.State = swarm.NodeStateDown
				} else {
					node.ManagerStatus.Leader = false
					node.ManagerStatus.Reachability = swarm.ReachabilityUnreachable
				}
				host.cluster.nodes[host.nodeID] = node
			case "source-driver":
				cfg.Storage.Driver = "sqlite"
			case "source-runtime":
				cfg.Controller.SystemProject.RuntimeID = "foreign-runtime"
			case "source-services":
				cfg.Controller.SystemProject.Services = cfg.Controller.SystemProject.Services[:3]
			case "source-dsn-scheme", "source-dsn-database", "source-dsn-user", "source-dsn-anonymous":
				connection, err := url.Parse(cfg.Storage.DSN)
				if err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "source-dsn-scheme":
					connection.Scheme = "mysql"
				case "source-dsn-database":
					connection.Path = "/unrelated"
				case "source-dsn-user":
					connection.User = url.User("unrelated")
				case "source-dsn-anonymous":
					connection.User = nil
				}
				cfg.Storage.DSN = connection.String()
			case "source-layout":
				cfg.Controller.LocalGateway = true
			case "source-identity":
				cfg.Controller.InstanceID = "different-controller"
			case "source-listen":
				cfg.Controller.Internal.Listen = ":9555"
			case "source-database":
				cfg.Storage.DSN = "postgres://nsc:public-fixture-only@external.example.test:5432/nsc"
			case "source-registration":
				cfg.Controller.SystemProject.ClusterID = "different-cluster"
			}
			if strings.HasPrefix(scenario, "source-") {
				raw, _ := yaml.Marshal(cfg)
				host.files["controller.yaml"] = raw
				os.WriteFile(installer.Config.Adoption.ControllerConfigFile, raw, 0600)
			}
			host.cluster.services[service.ID] = service
			before := bootstrap.Digest(host.cluster.services)
			plan, err := installer.PlanWebPreparation(t.Context(), bootstrap.Upgrade)
			if err == nil {
				t.Fatal("untrusted preparation inputs were accepted", scenario, plan.Hash)
			}
			if installer.Record.Preparation != nil || bootstrap.Digest(host.cluster.services) != before {
				t.Fatal("rejected planning modified system resources")
			}
		})
	}
}
func mustPreparationJSON(value any) []byte { data, _ := json.Marshal(value); return data }
