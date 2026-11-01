package install

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

func (i *Installer) createControl(ctx context.Context) (bootstrap.Resource, error) {
	data, _ := json.Marshal(map[string]string{"installation_id": i.Record.ID, "maintenance_anchor": i.Record.MaintenanceAnchor})
	var reply struct {
		ID string `json:"ID"`
	}
	err := i.entry().Engine(ctx, "POST", "/configs/create", map[string]any{"Name": i.name("installation"), "Data": base64.StdEncoding.EncodeToString(data), "Labels": map[string]string{"io.nsc.installation": i.Record.ID}}, &reply)
	return bootstrap.Resource{Kind: "config", ID: reply.ID, SpecDigest: bootstrap.Digest(data)}, err
}
func (i *Installer) createMaintenanceCredential(ctx context.Context) (bootstrap.Resource, error) {
	if err := i.metadataBoundary(); err != nil {
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: err.Error()}
	}
	node, err := i.selectedNode(i.Config.MaintenanceNode)
	if err != nil {
		return bootstrap.Resource{}, err
	}
	cfg, err := i.runtimeConfigForCredential(node)
	if err != nil {
		return bootstrap.Resource{}, err
	}
	value, err := i.readMaintenanceCredential()
	if err != nil {
		return bootstrap.Resource{}, err
	}
	value.DSN = cfg
	value.RegistryAuth, err = i.registryAuthorizations()
	if err != nil {
		return bootstrap.Resource{}, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return bootstrap.Resource{}, err
	}
	defer clear(data)
	var reply struct {
		ID string `json:"ID"`
	}
	err = i.entry().Engine(ctx, "POST", "/secrets/create", map[string]any{"Name": i.name("maintenance"), "Data": base64.StdEncoding.EncodeToString(data), "Labels": map[string]string{"io.nsc.installation": i.Record.ID}}, &reply)
	sum := sha256.Sum256(data)
	return bootstrap.Resource{Kind: "secret", ID: reply.ID, SpecDigest: hex.EncodeToString(sum[:])}, err
}

func (i *Installer) runtimeConfigForCredential(_ bootstrap.Node) (string, error) {
	if i.Config.Adoption != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cfg, err := i.localConfig(ctx)
		return cfg.Storage.DSN, err
	}
	password, err := bootstrap.ReadPrivate(i.material("postgres-password"))
	if err != nil {
		return "", err
	}
	defer clear(password)
	dsn := url.URL{Scheme: "postgres", User: url.UserPassword("nsc", string(password)), Host: i.name("postgresql") + ":5432", Path: "nsc", RawQuery: "sslmode=disable"}
	return dsn.String(), nil
}

func (i *Installer) registryAuthorizations() (map[string]string, error) {
	if i.Config.RegistryAuthFile == "" {
		return nil, nil
	}
	data, err := bootstrap.ReadPrivate(i.Config.RegistryAuthFile)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	images := i.Config.Images
	if i.Target != nil {
		images = i.Target.Images
	}
	return bootstrap.RegistryAuthorizations(data, images.Controller, images.Gateway)
}

func (i *Installer) metadataBoundary(extra ...string) error {
	references := append([]string{i.Config.RegistryAuthFile, i.Config.AdminPasswordFile, i.CredentialFile, i.nodeDir()}, extra...)
	for _, node := range i.Config.Nodes {
		references = append(references, node.SSHKeyFile)
	}
	if i.Config.Adoption != nil {
		references = append(references, i.Config.Adoption.ControllerConfigFile)
	}
	if err := bootstrap.ValidateMetadataReferences(i.Directory, references...); err != nil {
		return err
	}
	return bootstrap.ValidateMetadataContents(i.Directory, *i.Record)
}

// Public certificate PEM permissions do not imply private-key permissions.
// Keep one material-loading boundary for CLI checks and the worker Secret.
func (i *Installer) readMaintenanceCredential() (bootstrap.MaintenanceCredential, error) {
	value := bootstrap.MaintenanceCredential{}
	for filename, target := range map[string]*string{"maintenance.crt": &value.Certificate, "maintenance.key": &value.PrivateKey, "ca.crt": &value.CA, "console.crt": &value.ConsoleCertificate} {
		var data []byte
		var err error
		if strings.HasSuffix(filename, ".key") {
			data, err = bootstrap.ReadPrivate(i.material(filename))
		} else {
			data, err = os.ReadFile(i.material(filename))
		}
		if err != nil {
			return value, err
		}
		*target = string(data)
		clear(data)
	}
	return value, nil
}
