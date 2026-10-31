package install

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func (i *Installer) seedRecord() bootstrap.Installation {
	r := bootstrap.Installation{ID: i.Record.ID, Revision: i.Record.Revision, Mode: i.Config.Mode, State: bootstrap.Running, ClusterID: i.Record.ClusterID, PoolID: i.Record.PoolID, RuntimeID: i.Record.RuntimeID, CertificateID: i.Record.CertificateID, Components: map[string]bootstrap.Resource{}, Nodes: i.Record.Nodes, ConfigDigest: i.Record.ConfigDigest, IngressNetwork: i.name("ingress"), UpdatedAt: timeNow(), AllowedOperations: []bootstrap.Action{}, Drift: []string{}, ConsoleURLs: []string{}}
	for _, key := range []string{"controller", "postgresql", "nginx", "nginx-sandbox"} {
		receipt := i.Record.Resources["reserve:"+key]
		if key == "postgresql" {
			receipt = i.Record.Resources["service:"+key]
		}
		if started, ok := i.Record.Resources["start:"+key]; ok {
			receipt = started
		}
		r.Components[key] = receipt
	}
	data := i.Record.Nodes[i.Config.DataNode]
	r.Database = bootstrap.DatabasePin{NodeID: data.NodeID, EngineID: data.EngineID, Volume: i.Record.Resources["volume:postgresql"].ID, ServiceID: r.Components["postgresql"].ID, Image: i.Config.Images.PostgreSQL, SinglePointOfFailure: true}
	for _, n := range i.Config.Nodes {
		if n.Manager {
			r.ConsoleURLs = append(r.ConsoleURLs, "https://"+net.JoinHostPort(n.Address, strconv.Itoa(i.Config.ConsolePort)))
		}
	}
	return r
}

// SeedStore is called by the locked offline executor after its one migration.
// It retains stable IDs and imports the administrator identity only once.
func SeedStore(ctx context.Context, store storage.Store, cfg config.Config, ring *security.Keyring, r bootstrap.Installation) error {
	if ring == nil || r.ID == "" || r.PoolID == "" || r.RuntimeID == "" || r.ClusterID == "" || r.CertificateID == "" || r.Database.Volume == "" || !r.Database.SinglePointOfFailure || len(r.Components) != 4 || cfg.Storage.Driver != "postgres" || cfg.Storage.AutoMigrate {
		return fmt.Errorf("invalid offline installation authority")
	}
	for _, key := range []string{"controller", "postgresql", "nginx", "nginx-sandbox"} {
		resource := r.Components[key]
		if resource.Kind != "service" || resource.ID == "" || resource.Version == 0 || resource.SpecDigest == "" {
			return fmt.Errorf("offline service ownership receipt is incomplete")
		}
	}
	if cfg.Controller.SystemProject.ClusterID != r.ClusterID || cfg.Controller.SystemProject.RuntimeID != r.RuntimeID || r.Database.NodeID == "" || r.Database.EngineID == "" || r.Database.ServiceID != r.Components["postgresql"].ID {
		return storage.ErrConflict
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("offline Controller configuration is invalid: %w", err)
	}
	old, err := store.GetControlDocument(ctx, bootstrap.InstallationDocumentID)
	if errors.Is(err, storage.ErrNotFound) {
		if _, e := store.GetControlDocument(ctx, "community:identities"); !errors.Is(e, storage.ErrNotFound) {
			return fmt.Errorf("database already contains administrator identities; use verified adoption")
		}
		initial := r
		initial.State = bootstrap.Pending
		if err = writeInstallation(ctx, store, initial, 0); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		var previous bootstrap.Installation
		if err = json.Unmarshal(old.Payload, &previous); err != nil {
			return err
		}
		if old.Kind != bootstrap.InstallationDocumentKind || previous.ID != r.ID || previous.ClusterID != r.ClusterID || previous.ConfigDigest != r.ConfigDigest || previous.PoolID != r.PoolID || previous.RuntimeID != r.RuntimeID || previous.Database != r.Database {
			return storage.ErrConflict
		}
	}
	pool, err := store.GetPool(ctx, r.PoolID)
	if errors.Is(err, storage.ErrNotFound) {
		pool = model.GatewayPool{ID: r.PoolID, Name: "NSC Gateway", BackendNetwork: r.IngressNetwork, ChallengeUpstream: cfg.Server.PublicURL, Enabled: true, DesiredRevision: 1, Revision: 1, CreatedAt: timeNow(), UpdatedAt: timeNow()}
		if pool.BackendNetwork == "" {
			return fmt.Errorf("bootstrap ingress network is required")
		}
		if err = store.CreatePool(ctx, &pool); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if !pool.Enabled || pool.ChallengeUpstream != cfg.Server.PublicURL {
		return storage.ErrConflict
	}
	entry := ""
	for _, n := range cfg.Controller.Nodes {
		if n.InstanceID == cfg.Controller.InstanceID {
			entry = n.NodeID
		}
	}
	if entry == "" {
		return fmt.Errorf("offline executor requires the maintenance Controller identity")
	}
	rt, err := store.GetRuntime(ctx, r.RuntimeID)
	if errors.Is(err, storage.ErrNotFound) {
		rt = model.Runtime{ID: r.RuntimeID, PoolID: r.PoolID, Name: "NSC Swarm", Type: model.RuntimeSwarm, Endpoint: "node://" + entry, ClusterID: r.ClusterID, Enabled: true, ManagementEnabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: timeNow(), UpdatedAt: timeNow()}
		if err = store.CreateRuntime(ctx, &rt); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if rt.PoolID != r.PoolID || rt.ClusterID != r.ClusterID || rt.Endpoint != "node://"+entry || rt.Type != model.RuntimeSwarm {
		return storage.ErrConflict
	}
	application := core.New(store, cfg, ring)
	if err = application.BootstrapIdentity(ctx); err != nil {
		return err
	}
	if err = seedConsoleCertificate(ctx, store, ring, r, cfg.Server.ConsoleHTTPSCertFile, cfg.Server.ConsoleHTTPSKeyFile); err != nil {
		return err
	}
	if err = application.EnsureBuiltinCatalog(ctx); err != nil {
		return err
	}
	sandboxID := ""
	for _, identity := range r.Nodes {
		if identity.NodeID == r.Database.NodeID {
			sandboxID = identity.SandboxID
		}
	}
	settings, err := application.GetSandbox(ctx, r.PoolID)
	if err != nil {
		return err
	}
	if settings.GatewayID == "" {
		_, err = application.SaveSandbox(ctx, r.PoolID, core.SandboxSettings{GatewayID: sandboxID, Checks: []core.SandboxCheck{}}, settings.Revision)
	} else if settings.GatewayID != sandboxID {
		return storage.ErrConflict
	}
	if err != nil {
		return err
	}
	draft, err := application.NginxConfiguration(ctx, r.PoolID, "nginx-draft")
	if err != nil {
		return err
	}
	if draft.Revision == 0 {
		if _, err = application.SaveNginxDraft(ctx, r.PoolID, core.NginxConfiguration{Snippets: []model.ConfigSnippet{}}, "bootstrap", 0); err != nil {
			return err
		}
	}
	saved, err := store.GetControlDocument(ctx, bootstrap.InstallationDocumentID)
	if err != nil {
		return err
	}
	r.State = bootstrap.Running
	return writeInstallation(ctx, store, r, saved.Revision)
}

func seedConsoleCertificate(ctx context.Context, store storage.Store, ring *security.Keyring, r bootstrap.Installation, certFile, keyFile string) error {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return fmt.Errorf("console certificate is invalid")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	fingerprint := sha256.Sum256(leaf.Raw)
	existing, err := store.GetCertificate(ctx, r.CertificateID)
	if err == nil {
		if existing.PoolID != r.PoolID || existing.Fingerprint != hex.EncodeToString(fingerprint[:]) {
			return storage.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	keyPEM, err := bootstrap.ReadPrivate(keyFile)
	if err != nil {
		return err
	}
	defer clear(keyPEM)
	if _, err = tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return fmt.Errorf("console certificate and private key do not match")
	}
	encrypted, err := ring.Encrypt(keyPEM, []byte(r.CertificateID))
	if err != nil {
		return err
	}
	if len(leaf.IPAddresses) == 0 {
		return fmt.Errorf("console certificate requires IP SANs")
	}
	now := timeNow()
	value := model.Certificate{ID: r.CertificateID, PoolID: r.PoolID, Provider: "self-signed", Status: model.CertificateActive, PrimaryDomain: leaf.IPAddresses[0].String(), CertificatePEM: string(certPEM), EncryptedPrivateKey: encrypted, EncryptionKeyID: ring.PrimaryID(), Fingerprint: hex.EncodeToString(fingerprint[:]), SerialNumber: leaf.SerialNumber.String(), NotBefore: &leaf.NotBefore, NotAfter: &leaf.NotAfter, Revision: 1, CreatedAt: now, UpdatedAt: now}
	return store.CreateCertificate(ctx, &value)
}

func writeInstallation(ctx context.Context, store storage.Store, r bootstrap.Installation, expected int64) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	d := storage.ControlDocument{ID: bootstrap.InstallationDocumentID, Kind: bootstrap.InstallationDocumentKind, ParentID: r.ID, Payload: data, CreatedAt: now, UpdatedAt: now}
	return store.WriteControlDocuments(storage.WithSystemBootstrap(ctx), []storage.ControlWrite{{Document: d, Expected: expected}})
}
