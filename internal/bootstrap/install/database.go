package install

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type DatabaseConnector interface {
	Open(context.Context, config.Config, Engine, string, bool) (storage.Store, error)
}

type SSHDatabase struct{}

func (i *Installer) localConfig(ctx context.Context) (config.Config, error) {
	if i.Config.Adoption != nil {
		filename := i.Config.Adoption.ControllerConfigFile
		if i.Record.Preparation != nil && i.Record.Preparation.ConfigurationFile != "" {
			filename = i.Record.Preparation.ConfigurationFile
		}
		data, err := bootstrap.ReadPrivate(filename)
		if err != nil {
			return config.Config{}, err
		}
		clear(data)
		service, err := i.inspectService(ctx, i.Config.Adoption.ServiceIDs["controller"])
		if err != nil {
			return config.Config{}, err
		}
		return config.LoadOfflineWithReader(filename, func(path string) ([]byte, error) {
			return i.registeredFile(ctx, i.Config.MaintenanceNode, service, path, true)
		})
	}

	var node bootstrap.Node
	for _, n := range i.Config.Nodes {
		if n.Name == i.Config.MaintenanceNode {
			node = n
		}
	}
	c, err := i.runtimeConfig(node)
	if err != nil {
		return c, err
	}
	read := func(name string) (string, error) {
		data, err := bootstrap.ReadPrivate(i.material(name))
		if err != nil {
			return "", err
		}
		defer clear(data)
		return strings.TrimSpace(string(data)), nil
	}
	c.Security.AdminToken, err = read("admin-password")
	if err != nil {
		return c, err
	}
	c.Security.SessionKey, err = read("session-key")
	if err != nil {
		return c, err
	}
	key, err := read("encryption-key")
	if err != nil {
		return c, err
	}
	c.Security.EncryptionKeys = map[string]string{"v1": key}
	c.Server.ConsoleHTTPSCertFile = i.material("console.crt")
	c.Server.ConsoleHTTPSKeyFile = i.material("console.key")
	return c, nil
}

func (i *Installer) databaseContainer(ctx context.Context) (string, error) {
	service := i.Record.Resources["service:postgresql"].ID
	if i.Config.Adoption != nil {
		service = i.Config.Adoption.ServiceIDs["postgresql"]
	}
	if service == "" {
		return "", fmt.Errorf("database service receipt is missing")
	}
	filters, _ := json.Marshal(map[string][]string{"service": {service}, "desired-state": {"running"}})
	var tasks []swarm.Task
	if err := i.entry().Engine(ctx, "GET", "/tasks?filters="+url.QueryEscape(string(filters)), nil, &tasks); err != nil {
		return "", err
	}
	id := ""
	for _, task := range tasks {
		if task.ServiceID != service || task.Status.State != swarm.TaskStateRunning || task.Status.ContainerStatus == nil {
			continue
		}
		if task.NodeID != i.Record.Nodes[i.Config.DataNode].NodeID {
			return "", fmt.Errorf("database task is running outside its fixed data node")
		}
		if id != "" {
			return "", fmt.Errorf("database has multiple running tasks")
		}
		id = task.Status.ContainerStatus.ContainerID
	}
	if id == "" {
		return "", fmt.Errorf("database task is unavailable; do not relocate its volume")
	}
	return id, nil
}

// Database access stays inside the verified SSH connection and the exact data
// container. No database port is published and no password is passed to exec.
// The PostgreSQL wire protocol is still authenticated with the existing DSN.
func (i *Installer) withStore(ctx context.Context, readOnly bool, fn func(storage.Store, config.Config) error) error {
	cfg, err := i.localConfig(ctx)
	if err != nil {
		return err
	}
	container, err := i.databaseContainer(ctx)
	if err != nil {
		return err
	}
	connector := i.Database
	if connector == nil {
		connector = SSHDatabase{}
	}
	store, err := connector.Open(ctx, cfg, i.Hosts[i.Config.DataNode], container, readOnly)
	if err != nil {
		return err
	}
	defer store.Close()
	return fn(store, cfg)
}

func (SSHDatabase) Open(ctx context.Context, cfg config.Config, host Engine, container string, readOnly bool) (storage.Store, error) {
	connection, err := pgx.ParseConfig(cfg.Storage.DSN)
	if err != nil {
		return nil, fmt.Errorf("database connection reference is invalid")
	}
	connection.Host = "127.0.0.1"
	connection.Port = 5432
	for _, fallback := range connection.Fallbacks {
		fallback.Host = "127.0.0.1"
		fallback.Port = 5432
	}
	connection.ConnectTimeout = 10 * time.Second
	if readOnly {
		connection.RuntimeParams["default_transaction_read_only"] = "on"
	}
	connection.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return host.Stream(ctx, host.Privileged(bootstrap.LocalDockerCLI+" exec -i "+bootstrap.ShellQuote(container)+" /bin/busybox nc 127.0.0.1 5432"))
	}
	store, err := storage.OpenOfflinePostgres(connection)
	if err != nil {
		return nil, err
	}
	if err = store.Ping(ctx); err != nil {
		store.Close()
		return nil, fmt.Errorf("database authentication or TCP readiness failed")
	}
	return store, nil
}

func (i *Installer) initializeDatabase(ctx context.Context) error {
	if err := wait(ctx, func() error {
		return i.withStore(ctx, true, func(store storage.Store, _ config.Config) error { return store.Ping(ctx) })
	}); err != nil {
		return err
	}
	return i.withStore(ctx, false, func(store storage.Store, cfg config.Config) error {
		if err := store.Migrate(ctx); err != nil {
			return err
		}
		ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
		if err != nil {
			return err
		}
		return SeedStore(ctx, store, cfg, ring, i.seedRecord())
	})
}

func (i *Installer) finalizeDatabase(ctx context.Context) error {
	return i.withStore(ctx, false, func(store storage.Store, _ config.Config) error {
		d, err := store.GetControlDocument(ctx, bootstrap.InstallationDocumentID)
		if err != nil {
			return err
		}
		var previous bootstrap.Installation
		if err = json.Unmarshal(d.Payload, &previous); err != nil {
			return err
		}
		if previous.ID != i.Record.ID || previous.MaintenanceAnchor != i.Record.MaintenanceAnchor || previous.ClusterID != i.Record.ClusterID || previous.ConfigDigest != i.Record.ConfigDigest {
			return storage.ErrConflict
		}
		next := i.seedRecord()
		next.State = bootstrap.Succeeded
		next.Health = "VERIFIED"
		next.Revision = i.Record.Revision + 1
		if previous.State == bootstrap.Succeeded && previous.Revision == next.Revision {
			return nil
		}
		return writeInstallation(ctx, store, next, d.Revision)
	})
}
