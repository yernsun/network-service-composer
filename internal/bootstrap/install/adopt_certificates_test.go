package install

import (
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/pki"
	"github.com/yernsun/network-service-composer/internal/storage"
	"gopkg.in/yaml.v3"
)

func TestAdoptionCertificateTrustAndExpiryAreIndependentOfOwnership(t *testing.T) {
	i, cluster := adoptionFixture(t, 1)
	host := i.Hosts["one"].(*fakeHost)
	ca := host.files["controller-secrets/ca.crt"]
	leaf := host.files["controller-secrets/controller.crt"]
	uri, err := controlplane.IdentityURI(i.Record.Nodes["one"].InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if expired, err := verifyRegisteredCertificate(leaf, ca, uri.String()); err != nil || expired {
		t.Fatal(expired, err)
	}
	invalid := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1, 2, 3}})
	for _, pair := range [][2][]byte{{nil, ca}, {leaf, nil}, {invalid, ca}, {leaf, invalid}, {host.files["gateway-secrets/agent.crt"], ca}} {
		if _, err = verifyRegisteredCertificate(pair[0], pair[1], uri.String()); err == nil {
			t.Fatal("malformed or wrong-role trust was accepted")
		}
	}
	otherCA, _, err := pki.SelfSigned([]string{"unrelated.example.test"}, nil, 20, timeNow())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = verifyRegisteredCertificate(leaf, otherCA, uri.String()); err == nil {
		t.Fatal("unrelated trust authority accepted")
	}
	material := filepath.Join(host.cluster.originalDirectory+".offline", "materials")
	temporary := t.TempDir()
	certFile := filepath.Join(temporary, "expired.crt")
	keyFile := filepath.Join(temporary, "expired.key")
	if err = pki.Issue(filepath.Join(material, "ca.crt"), filepath.Join(material, "ca.key"), certFile, keyFile, pki.CertificateOptions{CommonName: "expired-existing-identity", URIs: []*url.URL{uri}, Duration: time.Nanosecond, Client: true}); err != nil {
		t.Fatal(err)
	}
	expiredPEM, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	if expired, err := verifyRegisteredCertificate(expiredPEM, ca, uri.String()); err != nil || !expired {
		t.Fatal("expired original identity was mistaken for fresh health", err)
	}
	host.files["controller-secrets/controller.crt"] = expiredPEM
	observation, err := i.Observe(t.Context())
	if err != nil {
		t.Fatal("expired identity should retain ownership with explicit drift", err)
	}
	if !contains(observation.(Observation).Adoption.Installation.Drift, "controller_identity_expired:"+i.Record.Nodes["one"].NodeID) {
		t.Fatal("identity expiry was not exposed")
	}
	service := cluster.services[i.Config.Adoption.ServiceIDs["nginx"]]
	host.files["gateway.yaml"] = []byte("invalid: [")
	if _, _, err = i.registeredGateway(t.Context(), "one", service, "production", i.Record.PoolID, ca); err == nil {
		t.Fatal("malformed registered gateway configuration accepted")
	}
}

func TestAdoptionReadsOnlyExplicitMountedPathsAndMatchingAssets(t *testing.T) {
	i, cluster := adoptionFixture(t, 1)
	host := i.Hosts["one"].(*fakeHost)
	service := cluster.services[i.Config.Adoption.ServiceIDs["controller"]]
	var isolated swarm.Service
	if err := encode(service, &isolated); err != nil {
		t.Fatal(err)
	}
	service = isolated
	for _, filename := range []string{"relative", "/run/nsc/../foreign", "/not-mounted/ca.crt"} {
		if _, err := i.registeredFile(t.Context(), "one", service, filename); err == nil {
			t.Fatal("unregistered path accepted", filename)
		}
	}
	service.Spec.TaskTemplate.ContainerSpec.Mounts = []mount.Mount{{Type: mount.TypeVolume, Source: "public-runtime-certificates", Target: "/run/nsc"}}
	host.volumes["public-runtime-certificates"] = map[string]any{"Name": "public-runtime-certificates", "Mountpoint": "/owned/controller-secrets"}
	if _, err := i.registeredFile(t.Context(), "one", service, "/run/nsc/ca.crt"); err != nil {
		t.Fatal("verified runtime certificate volume could not be read", err)
	}
	host.volumes["public-runtime-certificates"]["Name"] = "different-volume"
	if _, err := i.registeredFile(t.Context(), "one", service, "/run/nsc/ca.crt"); err == nil {
		t.Fatal("different volume identity was accepted")
	}
	delete(host.volumes, "public-runtime-certificates")
	if _, err := i.registeredFile(t.Context(), "one", service, "/run/nsc/ca.crt"); err == nil {
		t.Fatal("missing runtime certificate volume was accepted")
	}
	// An IP certificate may already exist as an asset. Adoption retains its ID;
	// it cannot select another Pool, an ambiguous copy, or a mismatched reference.
	cfg, err := i.localConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	service = cluster.services[i.Config.Adoption.ServiceIDs["controller"]]
	adoptStore(t, i, func(store storage.Store) error {
		id, err := i.adoptedConsoleAsset(t.Context(), store, cfg, service, i.Record.CertificateID)
		if err != nil || id != i.Record.CertificateID {
			return fmt.Errorf("matching asset was not retained: %w", err)
		}
		if _, err = i.adoptedConsoleAsset(t.Context(), store, cfg, service, "unrelated-certificate"); err == nil {
			return fmt.Errorf("mismatched asset reference accepted")
		}
		certificate, err := store.GetCertificate(t.Context(), id)
		if err != nil {
			return err
		}
		certificate.ID = "duplicate-certificate-asset"
		if err = store.CreateCertificate(t.Context(), &certificate); err != nil {
			return err
		}
		if _, err = i.adoptedConsoleAsset(t.Context(), store, cfg, service, ""); err == nil {
			return fmt.Errorf("ambiguous asset was selected")
		}
		return nil
	})
	cfg.Server.ConsoleHTTPSCertFile = ""
	cfg.Server.HTTPTLSCertFile = ""
	adoptStore(t, i, func(store storage.Store) error {
		_, err := i.adoptedConsoleAsset(t.Context(), store, cfg, service, "")
		return err
	})
	cfg.Server.ConsoleHTTPSCertFile = "/run/nsc/console.crt"
	host.files["controller-secrets/console.crt"] = []byte("not a certificate")
	adoptStore(t, i, func(store storage.Store) error {
		if _, err := i.adoptedConsoleAsset(t.Context(), store, cfg, service, ""); err == nil {
			return fmt.Errorf("invalid console certificate accepted")
		}
		return nil
	})
	// Unmounting an identity file remains blocked; a similarly named file outside
	// the registered mounts does not authorize finding a replacement.
	service.Spec.TaskTemplate.ContainerSpec = &swarm.ContainerSpec{}
	if _, err = i.registeredFile(t.Context(), "one", service, "/run/nsc/ca.crt"); err == nil {
		t.Fatal("missing identity mount accepted")
	}
}

func TestAdoptionAgentScopeAndExistingExpiryRemainExplicit(t *testing.T) {
	for _, scenario := range []string{"wrong-pool", "invalid-identity", "missing-certificate", "expired-gateway", "expired-sandbox"} {
		t.Run(scenario, func(t *testing.T) {
			i, cluster := adoptionFixture(t, 1)
			host := i.Hosts["one"].(*fakeHost)
			ca := host.files["controller-secrets/ca.crt"]
			var cfg config.Config
			if err := yaml.Unmarshal(host.files["gateway.yaml"], &cfg); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "wrong-pool":
				cfg.Agent.PoolID = "foreign-pool"
			case "invalid-identity":
				cfg.Agent.GatewayID = "invalid/id"
			case "missing-certificate":
				cfg.Agent.TLSCertFile = "/not-mounted/agent.crt"
			default:
				identity := i.Record.Nodes["one"].GatewayID
				role := "gateway"
				if scenario == "expired-sandbox" {
					identity = i.Record.Nodes["one"].SandboxID
					role = "sandbox"
				}
				uri, err := pki.GatewayIdentityURI(i.Record.PoolID, identity)
				if err != nil {
					t.Fatal(err)
				}
				material := filepath.Join(host.cluster.originalDirectory+".offline", "materials")
				directory := t.TempDir()
				cert := filepath.Join(directory, "agent.crt")
				key := filepath.Join(directory, "agent.key")
				if err = pki.Issue(filepath.Join(material, "ca.crt"), filepath.Join(material, "ca.key"), cert, key, pki.CertificateOptions{CommonName: identity, URIs: []*url.URL{uri}, Client: true, Duration: time.Nanosecond}); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(cert)
				if err != nil {
					t.Fatal(err)
				}
				host.files[role+"-secrets/agent.crt"] = data
			}
			if strings.HasPrefix(scenario, "expired-") {
				observation, err := i.Observe(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				prefix := "gateway_identity_expired:"
				if scenario == "expired-sandbox" {
					prefix = "sandbox_identity_expired:"
				}
				if !contains(observation.(Observation).Adoption.Installation.Drift, prefix+i.Record.Nodes["one"].NodeID) {
					t.Fatal("expired original credential was replaced or reported healthy")
				}
			} else {
				data, err := yaml.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				host.files["gateway.yaml"] = data
				if _, _, err = i.registeredGateway(t.Context(), "one", cluster.services[i.Config.Adoption.ServiceIDs["nginx"]], "production", i.Record.PoolID, ca); err == nil {
					t.Fatal("unverified role/Pool/credential was accepted")
				}
			}
		})
	}
	for _, arguments := range [][]string{{"--config=/etc/nsc/agent.yaml"}, {"--config"}, {}} {
		service := swarm.Service{Spec: swarm.ServiceSpec{TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{Args: arguments}}}}
		if got := agentConfigPath(service); got != "/etc/nsc/agent.yaml" {
			t.Fatal("existing config argument style was lost", got)
		}
	}
}
