package install

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/pki"
	"gopkg.in/yaml.v3"
)

func completedPreparation(t *testing.T) (*Installer, *preparationHost) {
	t.Helper()
	installer, host, _ := webPreparationFixture(t)
	plan, err := installer.Plan(t.Context(), bootstrap.Upgrade)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "verified-preparation")
	if err != nil || operation.State != bootstrap.Succeeded {
		t.Fatal(operation.State, err)
	}
	return installer, host
}

// A matching local file checksum is not proof of a trusted identity. Recovery
// must still bind the original CA, installation URI, IP SAN and private key.
func TestPreparationRecoveryRejectsChangedIdentityMaterialEvenWithAValidManifest(t *testing.T) {
	for _, scenario := range []string{"missing-completion", "stale-completion", "missing-ca", "invalid-ca", "foreign-ca", "missing-maintenance", "invalid-maintenance", "foreign-installation", "foreign-maintainer", "foreign-signer", "missing-console", "invalid-console", "foreign-ip", "missing-key", "public-key", "wrong-key", "identity-symlink"} {
		t.Run(scenario, func(t *testing.T) {
			installer, _ := completedPreparation(t)
			originalID, database := installer.Record.ID, installer.Record.Database
			write := func(name string, data []byte) {
				t.Helper()
				if err := os.WriteFile(installer.material(name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			remove := func(name string) {
				t.Helper()
				if err := os.Remove(installer.material(name)); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "missing-completion":
				remove("complete.json")
			case "stale-completion":
				write("maintenance.crt", []byte("changed public certificate"))
			case "missing-ca":
				remove("ca.crt")
			case "invalid-ca":
				write("ca.crt", []byte("invalid public PEM"))
			case "foreign-ca":
				ca := filepath.Join(t.TempDir(), "ca.crt")
				key := filepath.Join(t.TempDir(), "ca.key")
				if err := pki.InitCA(ca, key, "isolated foreign CA", 0); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(ca)
				if err != nil {
					t.Fatal(err)
				}
				write("ca.crt", data)
			case "missing-maintenance":
				remove("maintenance.crt")
			case "invalid-maintenance":
				write("maintenance.crt", []byte("invalid public PEM"))
			case "foreign-installation", "foreign-maintainer", "foreign-signer":
				installation, maintainer := installer.Record.ID, installer.Record.MaintenanceID
				if scenario == "foreign-installation" {
					installation = "other-installation"
				}
				if scenario == "foreign-maintainer" {
					maintainer = "other-maintainer"
				}
				identity, err := controlplane.MaintenanceIdentityURI(installation, maintainer)
				if err != nil {
					t.Fatal(err)
				}
				ca, key := installer.material("ca.crt"), installer.material("ca.key")
				if scenario == "foreign-signer" {
					directory := t.TempDir()
					ca, key = filepath.Join(directory, "ca.crt"), filepath.Join(directory, "ca.key")
					if err = pki.InitCA(ca, key, "isolated foreign signer", 0); err != nil {
						t.Fatal(err)
					}
				}
				remove("maintenance.crt")
				remove("maintenance.key")
				if err = pki.Issue(ca, key, installer.material("maintenance.crt"), installer.material("maintenance.key"), pki.CertificateOptions{CommonName: maintainer, URIs: []*url.URL{identity}, Client: true}); err != nil {
					t.Fatal(err)
				}
			case "missing-console":
				remove("console.crt")
			case "invalid-console":
				write("console.crt", []byte("invalid public PEM"))
			case "foreign-ip":
				crt, key, err := pki.SelfSigned(nil, []net.IP{net.ParseIP("192.0.2.99")}, 20, timeNow())
				if err != nil {
					t.Fatal(err)
				}
				write("console.crt", crt)
				write("console.key", key)
			case "missing-key":
				remove("maintenance.key")
			case "public-key":
				if err := os.Chmod(installer.material("maintenance.key"), 0644); err != nil {
					t.Fatal(err)
				}
			case "wrong-key":
				data, err := os.ReadFile(installer.material("console.key"))
				if err != nil {
					t.Fatal(err)
				}
				write("maintenance.key", data)
			case "identity-symlink":
				remove("maintenance.crt")
				if err := os.Symlink(installer.material("console.crt"), installer.material("maintenance.crt")); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "missing-completion" && scenario != "stale-completion" && scenario != "identity-symlink" {
				digest, err := installer.materialDigest()
				if err != nil {
					t.Fatal(err)
				}
				write("complete.json", []byte(digest))
			}
			if installer.checkPreparationMaterials() == nil {
				t.Fatal("untrusted recovery material passed its identity boundary")
			}
			if installer.Record.ID != originalID || installer.Record.Database != database {
				t.Fatal("recovery silently replaced an identity or data pin")
			}
		})
	}
}

func TestPreparedConfigurationRequiresItsOriginalServiceAndSourceReceipt(t *testing.T) {
	for _, scenario := range []string{"missing-source", "source-changed", "invalid-yaml", "missing-service", "invalid-service", "foreign-service", "invalid-user", "invalid-uid", "invalid-gid", "missing-executor", "missing-console", "changed-local-copy", "linked-local-copy", "changed-node-files", "different-intent"} {
		t.Run(scenario, func(t *testing.T) {
			installer, host := completedPreparation(t)
			before := bootstrap.Digest(host.cluster.services)
			source := filepath.Join(installer.preparationDirectory(), "source-one.yaml")
			servicePath := filepath.Join(installer.preparationDirectory(), "source-controller-service.json")
			service := host.cluster.services[installer.Config.Adoption.ServiceIDs["controller"]]
			write := func(path string, data []byte) {
				t.Helper()
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			switch scenario {
			case "missing-source":
				err = os.Remove(source)
			case "source-changed":
				write(source, []byte("unapproved configuration"))
			case "invalid-yaml":
				raw := []byte("invalid: [")
				write(source, raw)
				installer.Record.Preparation.SourceConfigurationHashes["one"] = bootstrap.Digest(raw)
			case "missing-service":
				err = os.Remove(servicePath)
			case "invalid-service":
				write(servicePath, []byte("{"))
			case "foreign-service":
				service.ID = "unrelated-service"
				write(servicePath, mustPreparationJSON(service))
			case "invalid-user", "invalid-uid", "invalid-gid":
				container := *service.Spec.TaskTemplate.ContainerSpec
				container.User = map[string]string{"invalid-user": "named-user", "invalid-uid": "name:65532", "invalid-gid": "65532:name"}[scenario]
				service.Spec.TaskTemplate.ContainerSpec = &container
				write(servicePath, mustPreparationJSON(service))
			case "missing-executor":
				delete(installer.Record.Resources, "reserve:executor")
			case "missing-console":
				err = os.Remove(installer.material("console.key"))
			case "changed-local-copy":
				write(installer.Record.Preparation.ConfigurationFile, []byte("an external change"))
			case "linked-local-copy":
				target := installer.Record.Preparation.ConfigurationFile
				err = os.Remove(target)
				if err == nil {
					err = os.Symlink(source, target)
				}
			case "changed-node-files":
				host.prepared["controller.yaml"] = append(bytes.Clone(host.prepared["controller.yaml"]), []byte("\n# external change\n")...)
			}
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "changed-local-copy", "linked-local-copy":
				err = installer.retainPreparedConfiguration("one")
			case "changed-node-files":
				err = installer.checkPreparedFiles(t.Context(), "one", installer.Record.Resources["prepare:files:one"].SpecDigest)
			case "different-intent":
				err = installer.checkPreparedFiles(context.Background(), "one", "unapproved-archive")
			default:
				_, err = installer.preparedNodeFiles("one")
			}
			if err == nil || bootstrap.Digest(host.cluster.services) != before {
				t.Fatal("changed source or unowned service was accepted", err)
			}
		})
	}
}

func pausedPreparation(t *testing.T, key string) (*Installer, *preparationHost, bootstrap.Operation) {
	t.Helper()
	installer, host, _ := webPreparationFixture(t)
	fault := &preparationCheckpointFailure{DatabaseConnector: installer.Database, key: key, state: bootstrap.Running}
	installer.Database = fault
	plan, err := installer.Plan(t.Context(), bootstrap.Upgrade)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "paused-preparation")
	if err == nil || !fault.fired {
		t.Fatal("the preparation was not interrupted before its mutation", err)
	}
	return installer, host, operation
}

func TestControllerPreparationNeverOverwritesUnapprovedConfigurationOrEndpoint(t *testing.T) {
	for _, scenario := range []string{"missing-before", "foreign-service", "changed-service", "missing-source", "changed-source", "foreign-mount", "occupied-console-mount", "missing-config-mount", "missing-endpoint", "occupied-port", "checkpoint-failure"} {
		t.Run(scenario, func(t *testing.T) {
			installer, host, operation := pausedPreparation(t, "prepare:controller")
			var step bootstrap.Step
			for _, candidate := range operation.Plan.Steps {
				if candidate.Key == "prepare:controller" {
					step = candidate
				}
			}
			service := host.cluster.services[installer.Config.Adoption.ServiceIDs["controller"]]
			switch scenario {
			case "missing-before":
				step.Before = nil
			case "foreign-service":
				step.Before.ID = "foreign-service"
			case "changed-service":
				service.Spec.TaskTemplate.ContainerSpec.Env = append(service.Spec.TaskTemplate.ContainerSpec.Env, "EXTERNAL_CHANGE=preserved")
			case "missing-source":
				delete(host.files, "controller.yaml")
			case "changed-source":
				host.files["controller.yaml"] = append(bytes.Clone(host.files["controller.yaml"]), []byte("\n# external change\n")...)
			case "foreign-mount":
				for index, value := range service.Spec.TaskTemplate.ContainerSpec.Mounts {
					if value.Target == agentConfigPath(service) {
						service.Spec.TaskTemplate.ContainerSpec.Mounts[index].Type = "volume"
					}
				}
			case "occupied-console-mount":
				service.Spec.TaskTemplate.ContainerSpec.Mounts = append(service.Spec.TaskTemplate.ContainerSpec.Mounts, bind("/external", "/run/nsc-bootstrap-console", true))
			case "missing-config-mount":
				values := service.Spec.TaskTemplate.ContainerSpec.Mounts[:0]
				for _, value := range service.Spec.TaskTemplate.ContainerSpec.Mounts {
					if value.Target != agentConfigPath(service) {
						values = append(values, value)
					}
				}
				service.Spec.TaskTemplate.ContainerSpec.Mounts = values
			case "missing-endpoint":
				service.Spec.EndpointSpec = nil
			case "occupied-port":
				service.Spec.EndpointSpec.Ports = append(service.Spec.EndpointSpec.Ports, port(8080, uint32(installer.Config.ConsolePort), "tcp", "ingress"))
			}
			host.cluster.services[service.ID] = service
			// These layouts can be found in an old installation or after external
			// edits. The final mutation adapter independently validates their layout.
			if scenario != "missing-before" && scenario != "foreign-service" && scenario != "changed-service" {
				value := serviceReceipt(service)
				step.Before = &value
			}
			before := bootstrap.Digest(host.cluster.services)
			save := installer.Journal.Save
			if scenario == "checkpoint-failure" {
				save = func(*bootstrap.Record) error { return context.Canceled }
			}
			if _, err := installer.updatePreparedController(t.Context(), step, installer.Target, save); err == nil {
				t.Fatal("unapproved Controller configuration was overwritten")
			}
			if bootstrap.Digest(host.cluster.services) != before {
				t.Fatal("rejected Controller preparation changed a service")
			}
		})
	}
}

func TestPreparationRejectsOriginalCAOutsideItsValidityWindow(t *testing.T) {
	for _, future := range []bool{false, true} {
		t.Run(map[bool]string{false: "expired", true: "not-yet-valid"}[future], func(t *testing.T) {
			installer, host, _ := webPreparationFixture(t)
			raw, err := os.ReadFile(installer.OriginalCACertificateFile)
			if err != nil {
				t.Fatal(err)
			}
			original, err := certificate(raw)
			if err != nil {
				t.Fatal(err)
			}
			raw, err = bootstrap.ReadPrivate(installer.OriginalCAPrivateKeyFile)
			if err != nil {
				t.Fatal(err)
			}
			block, _ := pem.Decode(raw)
			if block == nil {
				t.Fatal("fixture key is not PEM")
			}
			key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			signer := key.(interface{ Public() crypto.PublicKey })
			value := *original
			if future {
				value.NotBefore = timeNow().Add(time.Hour)
				value.NotAfter = value.NotBefore.Add(365 * 24 * time.Hour)
			} else {
				value.NotBefore = timeNow().Add(-48 * time.Hour)
				value.NotAfter = timeNow().Add(-time.Hour)
			}
			der, err := x509.CreateCertificate(rand.Reader, &value, &value, signer.Public(), key)
			if err != nil {
				t.Fatal(err)
			}
			filename := filepath.Join(t.TempDir(), "ca.crt")
			if err = os.WriteFile(filename, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
				t.Fatal(err)
			}
			installer.OriginalCACertificateFile = filename
			before := bootstrap.Digest(host.cluster.services)
			if _, err = installer.PlanWebPreparation(t.Context(), bootstrap.Upgrade); err == nil {
				t.Fatal("an invalid original CA validity window was accepted")
			}
			if bootstrap.Digest(host.cluster.services) != before {
				t.Fatal("CA rejection changed services")
			}
		})
	}
}

func TestPreparationLoginFailureKeepsDiagnosticsPrivateAndDoesNotClaimCompletion(t *testing.T) {
	installer, _, mode := webPreparationFixture(t)
	mode.Store("session-http-error")
	plan, err := installer.Plan(t.Context(), bootstrap.Upgrade)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "login-failure-evidence")
	if err == nil || operation.State != bootstrap.Failed || installer.Record.Resources["prepare:complete"].ID != "" {
		t.Fatal("login failure became a completed installation", operation.State, err)
	}
	filename := filepath.Join(installer.preparationDirectory(), "login-verification-error.txt")
	raw, err := bootstrap.ReadPrivate(filename)
	if err != nil || !bytes.Contains(raw, []byte("HTTP 401")) {
		t.Fatal("private login failure evidence is missing", err)
	}
	if strings.Contains(operation.Plan.Steps[len(operation.Plan.Steps)-2].Reason, "HTTP") {
		t.Fatal("private transport evidence entered public progress")
	}
}

func TestPreparationWaitsForProtectedSessionAfterAFormerLeaderIsReplaced(t *testing.T) {
	installer, _, mode := webPreparationFixture(t)
	mode.Store("session-cookie-converging")
	plan, err := installer.Plan(t.Context(), bootstrap.Upgrade)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "protected-session-convergence")
	if err != nil || operation.State != bootstrap.Succeeded {
		t.Fatal("an old leader cookie could not converge to protected HTTPS", operation.State, err)
	}
}

func TestPreparedLoginKeepsTheExistingAdministratorUsername(t *testing.T) {
	installer, host, mode := webPreparationFixture(t)
	cfg := config.Defaults()
	if err := yaml.Unmarshal(host.files["controller.yaml"], &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Security.AdminUsername = "original-admin"
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	host.files["controller.yaml"] = raw
	if err = os.WriteFile(installer.Config.Adoption.ControllerConfigFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	mode.Store("custom-admin-login")
	plan, err := installer.Plan(t.Context(), bootstrap.Upgrade)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "original-administrator-name")
	if err != nil || operation.State != bootstrap.Succeeded {
		t.Fatal("original administrator identity was replaced", operation.State, err)
	}
}

func TestPreparedLoginNeverAcceptsAPersistentlyUnprotectedSession(t *testing.T) {
	installer, _, mode := webPreparationFixture(t)
	plan, err := installer.Plan(t.Context(), bootstrap.Upgrade)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = installer.ExecuteMaintenance(t.Context(), plan, plan.Hash, "protected-login-baseline"); err != nil {
		t.Fatal(err)
	}
	mode.Store("session-cookie-insecure")
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	if err = installer.verifyPreparedLogin(ctx); err == nil {
		t.Fatal("an insecure session became a successful HTTPS proof")
	}
}
