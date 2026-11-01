package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/pki"
	"github.com/yernsun/network-service-composer/internal/storage"
	"gopkg.in/yaml.v3"
)

func (i *Installer) backupPreparation(ctx context.Context, store storage.Store) (bootstrap.Resource, error) {
	if err := i.ensureOfflineDirectory(); err != nil {
		return bootstrap.Resource{}, err
	}
	directory := i.preparationDirectory()
	if err := os.MkdirAll(directory, 0700); err != nil {
		return bootstrap.Resource{}, err
	}
	controller, err := i.inspectService(ctx, i.Config.Adoption.ServiceIDs["controller"])
	if err != nil {
		return bootstrap.Resource{}, err
	}
	configurationPath := agentConfigPath(controller)
	for _, node := range i.controllerNodes() {
		raw, err := i.registeredFile(ctx, node.Name, controller, configurationPath)
		if err != nil {
			return bootstrap.Resource{}, err
		}
		if bootstrap.Digest(raw) != i.Record.Preparation.SourceConfigurationHashes[node.Name] {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "original Controller configuration changed after approval"}
		}
		if err = bootstrap.AtomicPrivateFile(directory, "source-"+node.Name+".yaml", raw); err != nil {
			return bootstrap.Resource{}, err
		}
		cfg := config.Defaults()
		if err = yaml.Unmarshal(raw, &cfg); err != nil {
			return bootstrap.Resource{}, err
		}
		files := map[string][]byte{}
		paths := []string{cfg.Security.AdminTokenFile, cfg.Security.SessionKeyFile, cfg.Security.EncryptionKeysFile, cfg.Server.GRPCTLSCertFile, cfg.Server.GRPCTLSKeyFile, cfg.Server.GRPCClientCAFile, cfg.Controller.Internal.CertFile, cfg.Controller.Internal.KeyFile, cfg.Controller.Internal.CAFile, cfg.Server.ConsoleHTTPSCertFile, cfg.Server.ConsoleHTTPSKeyFile, cfg.Server.HTTPTLSCertFile, cfg.Server.HTTPTLSKeyFile}
		for _, profile := range cfg.ACME.DNSProviders {
			paths = append(paths, profile.CredentialsFile)
		}
		for _, path := range paths {
			if path == "" {
				continue
			}
			data, err := i.registeredFile(ctx, node.Name, controller, path)
			if err != nil {
				return bootstrap.Resource{}, err
			}
			files[path] = data
		}
		data, _ := json.Marshal(files)
		if err = bootstrap.AtomicPrivateFile(directory, "source-"+node.Name+"-files.json", data); err != nil {
			return bootstrap.Resource{}, err
		}
		clear(data)
	}
	for _, key := range []string{"controller", "postgresql", "nginx", "nginx-sandbox"} {
		service, err := i.inspectService(ctx, i.Config.Adoption.ServiceIDs[key])
		if err != nil {
			return bootstrap.Resource{}, err
		}
		data, _ := json.Marshal(service)
		if err = bootstrap.AtomicPrivateFile(directory, "source-"+key+"-service.json", data); err != nil {
			return bootstrap.Resource{}, err
		}
		if key == "controller" || key == "postgresql" {
			continue
		}
		for _, node := range i.Config.Nodes {
			if key == "nginx" && !node.Manager || key == "nginx-sandbox" && node.Name != i.Config.DataNode {
				continue
			}
			raw, err := i.registeredFile(ctx, node.Name, service, agentConfigPath(service))
			if err != nil {
				return bootstrap.Resource{}, err
			}
			cfg := config.Defaults()
			if err = yaml.Unmarshal(raw, &cfg); err != nil {
				return bootstrap.Resource{}, err
			}
			files := map[string][]byte{"configuration": raw}
			for _, path := range []string{cfg.Agent.CACertFile, cfg.Agent.TLSCertFile, cfg.Agent.TLSKeyFile} {
				data, err := i.registeredFile(ctx, node.Name, service, path)
				if err != nil {
					return bootstrap.Resource{}, err
				}
				files[path] = data
			}
			data, _ := json.Marshal(files)
			if err = bootstrap.AtomicPrivateFile(directory, "source-"+node.Name+"-"+key+"-files.json", data); err != nil {
				return bootstrap.Resource{}, err
			}
			clear(data)
		}
	}
	return i.backupDatabase(ctx)
}

func (i *Installer) makePreparationMaterials() error {
	if err := i.ensureOfflineDirectory(); err != nil {
		return err
	}
	if _, err := os.Stat(i.materialDir()); err == nil {
		return fmt.Errorf("preparation identities already exist; reconcile their original receipt instead of regenerating keys")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Mkdir(i.materialDir(), 0700); err != nil {
		return err
	}
	caPEM, err := os.ReadFile(i.Record.Preparation.CACertificateFile)
	if err != nil {
		return err
	}
	ca, err := certificate(caPEM)
	if err != nil {
		return err
	}
	fingerprint := sha256.Sum256(ca.Raw)
	if hex.EncodeToString(fingerprint[:]) != i.Record.TrustSHA256 {
		return fmt.Errorf("original CA changed after approval")
	}
	key, err := bootstrap.ReadPrivate(i.Record.Preparation.CAPrivateKeyFile)
	if err != nil {
		return err
	}
	defer clear(key)
	if _, err = tls.X509KeyPair(caPEM, key); err != nil {
		return fmt.Errorf("original CA key no longer matches")
	}
	if err = exclusive(i.material("ca.crt"), caPEM); err != nil {
		return err
	}
	if err = exclusive(i.material("ca.key"), key); err != nil {
		return err
	}
	uri, err := controlplane.MaintenanceIdentityURI(i.Record.ID, i.Record.MaintenanceID)
	if err != nil {
		return err
	}
	if err = pki.Issue(i.material("ca.crt"), i.material("ca.key"), i.material("maintenance.crt"), i.material("maintenance.key"), pki.CertificateOptions{CommonName: i.Record.MaintenanceID, URIs: []*url.URL{uri}, Client: true}); err != nil {
		return err
	}
	ips := []net.IP{}
	for _, node := range i.Config.Nodes {
		ips = append(ips, net.ParseIP(node.Address))
	}
	console, consoleKey, err := pki.SelfSigned(nil, ips, 20, timeNow())
	if err != nil {
		return err
	}
	defer clear(consoleKey)
	if err = exclusive(i.material("console.crt"), console); err != nil {
		return err
	}
	if err = exclusive(i.material("console.key"), consoleKey); err != nil {
		return err
	}
	if err = i.readPreparationCertificates(); err != nil {
		return err
	}
	digest, err := i.materialDigest()
	if err != nil {
		return err
	}
	return bootstrap.AtomicPrivateFile(i.materialDir(), "complete.json", []byte(digest))
}
func (i *Installer) readPreparationCertificates() error {
	caPEM, err := os.ReadFile(i.material("ca.crt"))
	if err != nil {
		return err
	}
	ca, err := certificate(caPEM)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(ca.Raw)
	if hex.EncodeToString(sum[:]) != i.Record.TrustSHA256 {
		return fmt.Errorf("preparation CA differs")
	}
	leafPEM, err := os.ReadFile(i.material("maintenance.crt"))
	if err != nil {
		return err
	}
	leaf, err := certificate(leafPEM)
	if err != nil {
		return err
	}
	installation, identity, err := controlplane.MaintenanceCertificateIdentity(leaf)
	if err != nil || installation != i.Record.ID || identity != i.Record.MaintenanceID || leaf.CheckSignatureFrom(ca) != nil {
		return fmt.Errorf("preparation maintenance identity differs")
	}
	i.Record.MaintenanceExpiresAt = &leaf.NotAfter
	consolePEM, err := os.ReadFile(i.material("console.crt"))
	if err != nil {
		return err
	}
	console, err := certificate(consolePEM)
	if err != nil {
		return err
	}
	for _, node := range i.Config.Nodes {
		if console.VerifyHostname(node.Address) != nil {
			return fmt.Errorf("preparation console does not cover inventory addresses")
		}
	}
	sum = sha256.Sum256(console.Raw)
	i.Record.ConsoleFingerprint = hex.EncodeToString(sum[:])
	i.Record.ConsoleCertificateExpiresAt = &console.NotAfter
	for _, role := range []string{"maintenance", "console"} {
		certPEM, err := os.ReadFile(i.material(role + ".crt"))
		if err != nil {
			return err
		}
		keyPEM, err := bootstrap.ReadPrivate(i.material(role + ".key"))
		if err != nil {
			return err
		}
		_, pairErr := tls.X509KeyPair(certPEM, keyPEM)
		clear(keyPEM)
		if pairErr != nil {
			return fmt.Errorf("preparation certificate key differs")
		}
	}
	return nil
}
func (i *Installer) checkPreparationMaterials() error {
	complete, err := bootstrap.ReadPrivate(i.material("complete.json"))
	if err != nil {
		return err
	}
	actual, err := i.materialDigest()
	if err != nil {
		return err
	}
	if string(complete) != actual {
		return fmt.Errorf("partial or changed preparation identities require protected CLI recovery")
	}
	return i.readPreparationCertificates()
}

func (i *Installer) preparedNodeFiles(name string) (map[string]nodeFile, error) {
	raw, err := bootstrap.ReadPrivate(filepath.Join(i.preparationDirectory(), "source-"+name+".yaml"))
	if err != nil {
		return nil, err
	}
	if bootstrap.Digest(raw) != i.Record.Preparation.SourceConfigurationHashes[name] {
		return nil, fmt.Errorf("retained Controller source differs from the approved configuration")
	}
	cfg := config.Defaults()
	if err = yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	original, err := bootstrap.ReadPrivate(filepath.Join(i.preparationDirectory(), "source-controller-service.json"))
	if err != nil {
		return nil, err
	}
	var service swarm.Service
	if json.Unmarshal(original, &service) != nil || service.ID != i.Config.Adoption.ServiceIDs["controller"] {
		return nil, fmt.Errorf("retained Controller service differs")
	}
	user := strings.Split(service.Spec.TaskTemplate.ContainerSpec.User, ":")
	if len(user) != 2 {
		return nil, fmt.Errorf("retained Controller user is invalid")
	}
	uid, err := strconv.Atoi(user[0])
	if err != nil {
		return nil, err
	}
	gid, err := strconv.Atoi(user[1])
	if err != nil {
		return nil, err
	}
	cfg.Storage.AutoMigrate = false
	cfg.Controller.Installation.MetadataDirectory = i.Directory
	cfg.Server.ConsoleHTTPSListen = ":8443"
	cfg.Server.ConsoleHTTPSCertFile = "/run/nsc-bootstrap-console/console.crt"
	cfg.Server.ConsoleHTTPSKeyFile = "/run/nsc-bootstrap-console/console.key"
	executor := i.Record.Resources["reserve:executor"].ID
	if executor == "" {
		return nil, fmt.Errorf("preparation executor receipt is missing")
	}
	cfg.Controller.SystemProject.Services = append(cfg.Controller.SystemProject.Services, config.SystemServiceConfig{ObjectID: executor, Environment: "prod"})
	definitions := []config.SystemApplicationConfig{}
	for _, key := range []string{"controller", "postgresql", "nginx", "nginx-sandbox"} {
		environment := "prod"
		if key == "nginx-sandbox" {
			environment = "sandbox"
		}
		objects := []string{i.Config.Adoption.ServiceIDs[key]}
		if key == "controller" {
			objects = append(objects, executor)
		}
		name := map[string]string{"controller": "Controller", "postgresql": "PostgreSQL", "nginx": "NGINX", "nginx-sandbox": "NGINX Sandbox"}[key]
		for _, existing := range cfg.Controller.SystemProject.Applications {
			if existing.Key == key {
				name = existing.Name
			}
		}
		definitions = append(definitions, config.SystemApplicationConfig{Key: key, Name: name, Environment: environment, RuntimeID: i.Record.RuntimeID, ObjectIDs: objects})
	}
	cfg.Controller.SystemProject.Applications = definitions
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	files := map[string]nodeFile{"controller.yaml": {data: encoded, uid: uid, gid: gid, mode: 0400}}
	for _, filename := range []string{"console.crt", "console.key"} {
		data, err := bootstrap.ReadPrivate(i.material(filename))
		if err != nil {
			return nil, err
		}
		files["console/"+filename] = nodeFile{data: data, uid: uid, gid: gid, mode: 0400}
	}
	return files, nil
}
func (i *Installer) distributePreparedFiles(ctx context.Context, name string, save func(*bootstrap.Record) error) (bootstrap.Resource, error) {
	files, err := i.preparedNodeFiles(name)
	if err != nil {
		return bootstrap.Resource{}, err
	}
	archive, digest, err := nodeArchive(files)
	if err != nil {
		return bootstrap.Resource{}, err
	}
	receipt := bootstrap.Resource{Kind: "preparation-files", ID: i.Record.ID + ":" + name, SpecDigest: digest}
	i.Record.Resources["intent:prepare:files:"+name] = receipt
	if err = save(i.Record); err != nil {
		return bootstrap.Resource{}, err
	}
	root := bootstrap.ShellQuote(i.preparedNodeDirectory())
	stage := bootstrap.ShellQuote(i.preparedNodeDirectory() + ".staging")
	command := "set -eu; test ! -e " + root + "; test ! -L " + root + "; test ! -e " + stage + "; test ! -L " + stage + "; install -d -m 0711 " + stage + "; tar -xpf - -C " + stage + "; mv -T -n " + stage + " " + root
	if _, err = i.Hosts[name].Run(ctx, i.Hosts[name].Privileged(command), archive); err != nil {
		return receipt, err
	}
	if err = i.retainPreparedConfiguration(name); err != nil {
		return receipt, err
	}
	return receipt, nil
}
func (i *Installer) checkPreparedFiles(ctx context.Context, name, digest string) error {
	files, err := i.preparedNodeFiles(name)
	if err != nil {
		return err
	}
	_, expected, err := nodeArchive(files)
	if err != nil {
		return err
	}
	if expected != digest {
		return fmt.Errorf("prepared configuration intent changed")
	}
	command := "set -eu; cd " + bootstrap.ShellQuote(i.preparedNodeDirectory()) + "; test ! -L controller.yaml; sha256sum -c manifest.sha256 >/dev/null; cat manifest.json"
	raw, err := i.Hosts[name].Run(ctx, i.Hosts[name].Privileged(command), nil)
	if err != nil {
		return err
	}
	var manifest map[string]string
	if json.Unmarshal(raw, &manifest) != nil || bootstrap.Digest(manifest) != digest {
		return fmt.Errorf("prepared node configuration differs")
	}
	return nil
}
func (i *Installer) updatePreparedController(ctx context.Context, step bootstrap.Step, target *bootstrap.Release, save func(*bootstrap.Record) error) (bootstrap.Resource, error) {
	before, err := i.inspectService(ctx, i.Config.Adoption.ServiceIDs["controller"])
	if err != nil {
		return bootstrap.Resource{}, err
	}
	if step.Before == nil || before.ID != step.Before.ID || bootstrap.Digest(before.Spec) != step.Before.SpecDigest {
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "Controller changed after approval"}
	}
	configTarget := agentConfigPath(before)
	for _, node := range i.controllerNodes() {
		raw, err := i.registeredFile(ctx, node.Name, before, configTarget)
		if err != nil {
			return bootstrap.Resource{}, err
		}
		if bootstrap.Digest(raw) != i.Record.Preparation.SourceConfigurationHashes[node.Name] {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "original Controller files changed after approval"}
		}
	}
	spec := before.Spec
	container := *spec.TaskTemplate.ContainerSpec
	container.Image = target.Images.Controller
	mounts := []mount.Mount{}
	found := false
	for _, value := range container.Mounts {
		if value.Target == configTarget {
			if value.Type != mount.TypeBind {
				return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "Controller configuration mount requires manual preparation"}
			}
			value.Source = i.preparedNodeDirectory() + "/controller.yaml"
			found = true
		}
		if value.Target == "/run/nsc-bootstrap-console" {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "console mount is already occupied"}
		}
		mounts = append(mounts, value)
	}
	if !found {
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "Controller configuration mount is unconfirmed"}
	}
	container.Mounts = append(mounts, bind(i.preparedNodeDirectory()+"/console", "/run/nsc-bootstrap-console", true))
	spec.TaskTemplate.ContainerSpec = &container
	if spec.EndpointSpec == nil {
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "Controller endpoint requires manual preparation"}
	}
	endpoint := *spec.EndpointSpec
	endpoint.Ports = append([]swarm.PortConfig{}, endpoint.Ports...)
	exists := false
	for _, value := range endpoint.Ports {
		if value.PublishedPort == uint32(i.Config.ConsolePort) {
			if value.TargetPort != 8443 {
				return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "console port is occupied"}
			}
			exists = true
		}
	}
	if !exists {
		endpoint.Ports = append(endpoint.Ports, port(8443, uint32(i.Config.ConsolePort), "tcp", swarm.PortConfigPublishModeIngress))
	}
	spec.EndpointSpec = &endpoint
	receipt := bootstrap.Resource{Kind: "service", ID: before.ID, Version: before.Version.Index, SpecDigest: bootstrap.Digest(spec), Image: container.Image}
	i.Record.Resources["intent:prepare:controller"] = receipt
	if err = save(i.Record); err != nil {
		return bootstrap.Resource{}, err
	}
	input, err := i.authorizedSpec(spec)
	if err != nil {
		return bootstrap.Resource{}, err
	}
	path := fmt.Sprintf("/services/%s/update?version=%d", url.PathEscape(before.ID), before.Version.Index)
	if _, explicit := input.(bootstrap.RegistryRequest); !explicit {
		path += "&registryAuthFrom=spec"
	}
	if err = i.entry().Engine(ctx, "POST", path, input, nil); err != nil {
		return receipt, err
	}
	actual, err := i.inspectService(ctx, before.ID)
	if err != nil {
		return receipt, err
	}
	if bootstrap.Digest(actual.Spec) != receipt.SpecDigest {
		return receipt, fmt.Errorf("prepared Controller outcome is unconfirmed")
	}
	return serviceReceipt(actual), nil
}

func (i *Installer) retainPreparedConfiguration(name string) error {
	files, err := i.preparedNodeFiles(name)
	if err != nil {
		return err
	}
	filename := "prepared-" + name + ".yaml"
	path := filepath.Join(i.preparationDirectory(), filename)
	if old, err := bootstrap.ReadPrivate(path); err == nil {
		if !bytes.Equal(old, files["controller.yaml"].data) {
			return fmt.Errorf("local prepared configuration changed externally")
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if err = bootstrap.AtomicPrivateFile(i.preparationDirectory(), filename, files["controller.yaml"].data); err != nil {
		return err
	}
	if name == i.Config.MaintenanceNode {
		i.Record.Preparation.ConfigurationFile = path
	}
	return nil
}
