package install

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/pki"
	"gopkg.in/yaml.v3"
)

func timeNow() time.Time { return time.Now().UTC() }
func (i *Installer) offlineDir() string {
	if i.Record.MetadataLayout == 0 {
		return i.Directory
	}
	return i.Directory + ".offline"
}
func (i *Installer) materialDir() string { return filepath.Join(i.offlineDir(), "materials") }
func (i *Installer) backupDir() string   { return filepath.Join(i.offlineDir(), "backups") }
func (i *Installer) ensureOfflineDirectory() error {
	info, err := os.Lstat(i.Directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("installation metadata directory must already be private")
	}
	directory := i.offlineDir()
	if err := os.Mkdir(directory, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err = os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&077 != 0 || int(info.Sys().(*syscall.Stat_t).Uid) != i.Record.MaintenanceUID {
		return fmt.Errorf("offline recovery directory must retain its private original owner")
	}
	return nil
}
func (i *Installer) material(name string) string { return filepath.Join(i.materialDir(), name) }

func exclusive(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

func randomFile(path string, n int) error {
	value := make([]byte, n)
	if _, err := rand.Read(value); err != nil {
		return err
	}
	defer clear(value)
	return exclusive(path, []byte(base64.StdEncoding.EncodeToString(value)))
}

func (i *Installer) makeMaterials() error {
	if err := i.ensureOfflineDirectory(); err != nil {
		return err
	}
	// Existence after an interrupted key write is deliberately not a signal to
	// regenerate it. Incomplete material requires private CLI recovery review.
	if _, err := os.Stat(i.materialDir()); err == nil {
		return &bootstrap.Rejected{Reason: "identity directory already exists without a completed creation receipt"}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Mkdir(i.materialDir(), 0o700); err != nil {
		return err
	}
	if err := pki.InitCA(i.material("ca.crt"), i.material("ca.key"), "NSC offline installation CA", 10*365*24*time.Hour); err != nil {
		return err
	}
	for _, n := range i.Config.Nodes {
		identity := i.Record.Nodes[n.Name]
		uri, err := controlplane.IdentityURI(identity.InstanceID)
		if err != nil {
			return err
		}
		if err = pki.Issue(i.material("ca.crt"), i.material("ca.key"), i.material(n.Name+"-controller.crt"), i.material(n.Name+"-controller.key"), pki.CertificateOptions{CommonName: identity.InstanceID, IPAddresses: []net.IP{net.ParseIP(n.Address)}, URIs: []*url.URL{uri}, Client: true, Server: true}); err != nil {
			return err
		}
		for _, role := range []string{"gateway", "sandbox"} {
			gatewayID := identity.GatewayID
			if role == "sandbox" {
				gatewayID = identity.SandboxID
			}
			uri, err := pki.GatewayIdentityURI(i.Record.PoolID, gatewayID)
			if err != nil {
				return err
			}
			if err = pki.Issue(i.material("ca.crt"), i.material("ca.key"), i.material(n.Name+"-"+role+".crt"), i.material(n.Name+"-"+role+".key"), pki.CertificateOptions{CommonName: gatewayID, URIs: []*url.URL{uri}, Client: true}); err != nil {
				return err
			}
		}
	}
	uri, err := controlplane.MaintenanceIdentityURI(i.Record.ID, i.Record.MaintenanceID)
	if err != nil {
		return err
	}
	if err = pki.Issue(i.material("ca.crt"), i.material("ca.key"), i.material("maintenance.crt"), i.material("maintenance.key"), pki.CertificateOptions{CommonName: i.Record.MaintenanceID, URIs: []*url.URL{uri}, Client: true}); err != nil {
		return err
	}
	publicCA, err := os.ReadFile(i.material("ca.crt"))
	if err != nil {
		return err
	}
	block, _ := pem.Decode(publicCA)
	if block == nil {
		return fmt.Errorf("offline CA certificate is invalid")
	}
	trust := sha256.Sum256(block.Bytes)
	i.Record.TrustSHA256 = hex.EncodeToString(trust[:])
	certificatePEM, err := os.ReadFile(i.material("maintenance.crt"))
	if err != nil {
		return err
	}
	block, _ = pem.Decode(certificatePEM)
	if block == nil {
		return fmt.Errorf("maintenance certificate is invalid")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	i.Record.MaintenanceExpiresAt = &leaf.NotAfter
	addresses := []net.IP{}
	for _, n := range i.Config.Nodes {
		addresses = append(addresses, net.ParseIP(n.Address))
	}
	certificate, key, err := pki.SelfSigned(nil, addresses, 20, timeNow())
	if err != nil {
		return err
	}
	defer clear(key)
	consoleBlock, _ := pem.Decode(certificate)
	consoleLeaf, err := x509.ParseCertificate(consoleBlock.Bytes)
	if err != nil {
		return err
	}
	consoleSum := sha256.Sum256(consoleLeaf.Raw)
	i.Record.ConsoleFingerprint = hex.EncodeToString(consoleSum[:])
	i.Record.ConsoleCertificateExpiresAt = &consoleLeaf.NotAfter
	if err = exclusive(i.material("console.crt"), certificate); err != nil {
		return err
	}
	if err = exclusive(i.material("console.key"), key); err != nil {
		return err
	}
	for _, name := range []string{"postgres-password", "session-key", "encryption-key"} {
		if err = randomFile(i.material(name), 32); err != nil {
			return err
		}
	}
	if i.Config.AdminPasswordFile != "" {
		value, err := bootstrap.ReadPrivate(i.Config.AdminPasswordFile)
		if err != nil {
			return err
		}
		defer clear(value)
		if len(strings.TrimSpace(string(value))) < 16 || len(strings.TrimSpace(string(value))) > 72 {
			return &bootstrap.Rejected{Reason: "administrator password requires 16–72 bytes"}
		}
		if err = exclusive(i.material("admin-password"), []byte(strings.TrimSpace(string(value)))); err != nil {
			return err
		}
	} else if err = randomFile(i.material("admin-password"), 32); err != nil {
		return err
	}
	digest, err := i.materialDigest()
	if err != nil {
		return err
	}
	return bootstrap.AtomicPrivateFile(i.materialDir(), "complete.json", []byte(digest))
}

func (i *Installer) materialDigest() (string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(i.materialDir(), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() == "complete.json" {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("identity directory contains a symbolic link")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		defer clear(data)
		name, err := filepath.Rel(i.materialDir(), path)
		if err != nil {
			return err
		}
		files[name] = bootstrap.Digest(data)
		return nil
	})
	return bootstrap.Digest(files), err
}

func (i *Installer) runtimeConfig(n bootstrap.Node) (config.Config, error) {
	c := config.Defaults()
	password, err := bootstrap.ReadPrivate(i.material("postgres-password"))
	if err != nil {
		return c, err
	}
	defer clear(password)
	address := i.name("postgresql") + ":5432"
	dsn := url.URL{Scheme: "postgres", User: url.UserPassword("nsc", string(password)), Host: address, Path: "/nsc", RawQuery: "sslmode=disable"}
	c.Storage = config.StorageConfig{Driver: "postgres", DSN: dsn.String(), HA: true, AutoMigrate: false}
	c.Server.HTTPListen = ":8080"
	c.Server.PublicURL = "http://" + i.name("controller") + ":8080"
	c.Server.ConsoleHTTPSListen = ":8443"
	c.Server.ConsoleHTTPSCertFile = "/run/nsc/console.crt"
	c.Server.ConsoleHTTPSKeyFile = "/run/nsc/console.key"
	c.Server.GRPCTLSCertFile = "/run/nsc/controller.crt"
	c.Server.GRPCTLSKeyFile = "/run/nsc/controller.key"
	c.Server.GRPCClientCAFile = "/run/nsc/ca.crt"
	c.Controller.InstanceID = i.Record.Nodes[n.Name].InstanceID
	c.Controller.LocalGateway = false
	c.Controller.Internal = config.ControllerInternalConfig{Listen: ":9444", URL: "https://" + net.JoinHostPort(n.Address, "9444"), CAFile: "/run/nsc/ca.crt", CertFile: "/run/nsc/controller.crt", KeyFile: "/run/nsc/controller.key", GRPCAddress: net.JoinHostPort(n.Address, "9443")}
	c.Security = config.SecurityConfig{AdminUsername: "admin", AdminTokenFile: "/run/nsc/admin-password", SessionKeyFile: "/run/nsc/session-key", PrimaryKeyID: "v1", EncryptionKeysFile: "/run/nsc/encryption-keys.json"}
	c.Management.Enabled = true
	c.Management.DefaultRuntimeID = i.Record.RuntimeID
	c.Controller.Installation.MetadataDirectory = i.Directory
	c.Controller.SystemProject = config.SystemProjectConfig{RuntimeID: i.Record.RuntimeID, ClusterID: i.Record.ClusterID}
	for _, key := range []string{"controller", "postgresql", "nginx", "nginx-sandbox"} {
		resource := i.Record.Resources["reserve:"+key]
		if key == "postgresql" {
			resource = i.Record.Resources["service:"+key]
		}
		environment, name := "prod", map[string]string{"controller": "Controller", "postgresql": "PostgreSQL", "nginx": "NGINX", "nginx-sandbox": "NGINX Sandbox"}[key]
		if key == "nginx-sandbox" {
			environment = "sandbox"
		}
		if resource.ID == "" {
			return c, fmt.Errorf("service receipt is missing")
		}
		c.Controller.SystemProject.Services = append(c.Controller.SystemProject.Services, config.SystemServiceConfig{ObjectID: resource.ID, Environment: environment})
		c.Controller.SystemProject.Applications = append(c.Controller.SystemProject.Applications, config.SystemApplicationConfig{Key: key, Name: name, Environment: environment, RuntimeID: i.Record.RuntimeID, ObjectIDs: []string{resource.ID}})
		if key == "controller" && i.Record.Resources["reserve:executor"].ID != "" {
			executor := i.Record.Resources["reserve:executor"].ID
			last := len(c.Controller.SystemProject.Applications) - 1
			c.Controller.SystemProject.Applications[last].ObjectIDs = append(c.Controller.SystemProject.Applications[last].ObjectIDs, executor)
			c.Controller.SystemProject.Services = append(c.Controller.SystemProject.Services, config.SystemServiceConfig{ObjectID: executor, Environment: "prod"})
		}
	}
	for _, node := range i.Config.Nodes {
		if !node.Manager {
			continue
		}
		identity := i.Record.Nodes[node.Name]
		c.Controller.Nodes = append(c.Controller.Nodes, config.ControllerNodeConfig{InstanceID: identity.InstanceID, NodeID: identity.NodeID, EngineID: identity.EngineID, ClusterID: i.Record.ClusterID, URL: "https://" + net.JoinHostPort(node.Address, "9444")})
	}
	return c, nil
}

type nodeFile struct {
	data     []byte
	uid, gid int
	mode     int64
}

func (i *Installer) nodeFiles(n bootstrap.Node) (map[string]nodeFile, error) {
	cfg, err := i.runtimeConfig(n)
	if err != nil {
		return nil, err
	}
	files := map[string]nodeFile{}
	gid, err := strconv.Atoi(i.Record.Nodes[n.Name].SocketGID)
	if err != nil {
		return nil, fmt.Errorf("invalid Docker socket GID")
	}
	addMaterial := func(path, source string, uid, gid int) error {
		data, err := os.ReadFile(i.material(source))
		if err == nil {
			files[path] = nodeFile{data, uid, gid, 0o400}
		}
		return err
	}
	for _, role := range []string{"controller", "gateway", "sandbox"} {
		uid, g := 101, 101
		if role == "controller" {
			uid, g = 65532, gid
		}
		for _, extension := range []string{"crt", "key"} {
			target := role
			if role != "controller" {
				target = "agent"
			}
			if err = addMaterial(role+"-secrets/"+target+"."+extension, n.Name+"-"+role+"."+extension, uid, g); err != nil {
				return nil, err
			}
		}
		if err = addMaterial(role+"-secrets/ca.crt", "ca.crt", uid, g); err != nil {
			return nil, err
		}
	}
	for _, name := range []string{"admin-password", "session-key", "console.crt", "console.key"} {
		if err = addMaterial("controller-secrets/"+name, name, 65532, gid); err != nil {
			return nil, err
		}
	}
	key, err := os.ReadFile(i.material("encryption-key"))
	if err != nil {
		return nil, err
	}
	keys, _ := json.Marshal(map[string]string{"v1": string(key)})
	clear(key)
	files["controller-secrets/encryption-keys.json"] = nodeFile{keys, 65532, gid, 0o400}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	files["controller.yaml"] = nodeFile{data, 65532, gid, 0o400}
	for _, role := range []string{"gateway", "sandbox"} {
		agent := config.Defaults()
		agent.Storage = cfg.Storage
		agent.NGINX.MainConfigPath = "/var/lib/nsc/nginx.conf"
		gateway := i.Record.Nodes[n.Name].GatewayID
		mode := "production"
		if role == "sandbox" {
			gateway = i.Record.Nodes[n.Name].SandboxID
			mode = "sandbox"
			agent.NGINX.HTTP3AdvertisedPort = 18443
		}
		entry := i.Config.Nodes[0]
		for _, node := range i.Config.Nodes {
			if node.Name == i.Config.MaintenanceNode {
				entry = node
			}
		}
		agent.Agent = config.AgentConfig{Mode: mode, GatewayID: gateway, PoolID: i.Record.PoolID, Name: n.Name + "-" + role, PublicAddress: n.Address, ControllerAddress: net.JoinHostPort(entry.Address, "9443"), ServerName: entry.Address, CACertFile: "/run/nsc/ca.crt", TLSCertFile: "/run/nsc/agent.crt", TLSKeyFile: "/run/nsc/agent.key", StateDir: "/var/lib/nsc", ProbeWorkers: 32, HeartbeatInterval: config.Duration(5 * time.Second)}
		data, err := yaml.Marshal(agent)
		if err != nil {
			return nil, err
		}
		files[role+".yaml"] = nodeFile{data, 101, 101, 0o400}
	}

	return files, nil
}

func (i *Installer) selectedNode(name string) (bootstrap.Node, error) {
	for _, n := range i.Config.Nodes {
		if n.Name == name {
			return n, nil
		}
	}
	return bootstrap.Node{}, fmt.Errorf("node is outside the installation inventory")
}

func (i *Installer) distribute(ctx context.Context, name string) (bootstrap.Resource, error) {
	n, err := i.selectedNode(name)
	if err != nil {
		return bootstrap.Resource{}, err
	}
	files, err := i.nodeFiles(n)
	if err != nil {
		return bootstrap.Resource{}, err
	}
	archive, digest, err := nodeArchive(files)
	if err != nil {
		return bootstrap.Resource{}, err
	}
	root := bootstrap.ShellQuote(i.nodeDir())
	stage := bootstrap.ShellQuote(i.nodeDir() + ".staging")
	command := "set -eu; test ! -e " + root + "; test ! -L " + root + "; test ! -e " + stage + "; test ! -L " + stage + "; install -d -m 0711 " + stage + "; tar -xpf - -C " + stage + "; mv -T -n " + stage + " " + root
	if _, err = i.Hosts[name].Run(ctx, i.Hosts[name].Privileged(command), archive); err != nil {
		return bootstrap.Resource{}, err
	}
	return bootstrap.Resource{Kind: "node-files", ID: i.Record.ID + ":" + name, SpecDigest: digest}, nil
}

func (i *Installer) checkNodeFiles(ctx context.Context, name, digest string) error {
	root := bootstrap.ShellQuote(i.nodeDir())
	node, err := i.selectedNode(name)
	if err != nil {
		return err
	}
	files, err := i.nodeFiles(node)
	if err != nil {
		return err
	}
	var checks strings.Builder
	for path, file := range files {
		quoted := bootstrap.ShellQuote(path)
		fmt.Fprintf(&checks, "test -f %s; test ! -L %s; test \"$(stat -c '%%u:%%g:%%a' %s)\" = %s; ", quoted, quoted, quoted, bootstrap.ShellQuote(fmt.Sprintf("%d:%d:%o", file.uid, file.gid, file.mode)))
		if dir := filepath.Dir(path); dir != "." {
			quoted = bootstrap.ShellQuote(dir)
			fmt.Fprintf(&checks, "test ! -L %s; test \"$(stat -c '%%u:%%g:%%a' %s)\" = %s; ", quoted, quoted, bootstrap.ShellQuote(fmt.Sprintf("%d:%d:700", file.uid, file.gid)))
		}
	}
	command := `set -eu; cd ` + root + `; ` + checks.String() + `sha256sum -c manifest.sha256 >/dev/null; cat manifest.json`
	data, err := i.Hosts[name].Run(ctx, i.Hosts[name].Privileged(command), nil)
	if err != nil {
		return err
	}
	var manifest map[string]string
	if err = json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if bootstrap.Digest(manifest) != digest {
		return fmt.Errorf("node-local files differ from the installation receipt")
	}
	return nil
}

// archiveBuilder retains the first tar error while assembling deterministic
// executor-authored entries; finish reports it before any distribution occurs.
type archiveBuilder struct {
	writer *tar.Writer
	err    error
}

func (a *archiveBuilder) add(header *tar.Header, data []byte) {
	if a.err != nil {
		return
	}
	a.err = a.writer.WriteHeader(header)
	if a.err == nil && len(data) > 0 {
		_, a.err = a.writer.Write(data)
	}
}
func nodeArchive(files map[string]nodeFile) ([]byte, string, error) {
	var data bytes.Buffer
	archive := archiveBuilder{writer: tar.NewWriter(&data)}
	names := make([]string, 0, len(files))
	manifest := map[string]string{}
	for name, f := range files {
		if f.uid < 0 || f.gid < 0 || f.mode != 0400 {
			return nil, "", fmt.Errorf("provisioning files require explicit owners and private read-only permissions")
		}
		if filepath.IsAbs(name) || filepath.Clean(name) != name || name == "." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\r\n\x00") {
			return nil, "", fmt.Errorf("invalid provisioning file path")
		}
		names = append(names, name)
		sum := sha256.Sum256(f.data)
		manifest[name] = hex.EncodeToString(sum[:])
	}
	sort.Strings(names)
	directories := map[string]bool{}
	for _, name := range names {
		f := files[name]
		dir := filepath.Dir(name)
		if dir != "." && !directories[dir] {
			archive.add(&tar.Header{Name: dir + "/", Typeflag: tar.TypeDir, Mode: 0700, Uid: f.uid, Gid: f.gid}, nil)
			directories[dir] = true
		}
		archive.add(&tar.Header{Name: name, Mode: f.mode, Size: int64(len(f.data)), Uid: f.uid, Gid: f.gid}, f.data)
	}
	for _, role := range []string{"gateway", "sandbox"} {
		for _, suffix := range []string{"-state", "-cache"} {
			archive.add(&tar.Header{Name: role + suffix + "/", Typeflag: tar.TypeDir, Mode: 0700, Uid: 101, Gid: 101}, nil)
		}
	}
	encoded, _ := json.Marshal(manifest)
	archive.add(&tar.Header{Name: "manifest.json", Mode: 0400, Size: int64(len(encoded))}, encoded)
	var checks strings.Builder
	for _, name := range names {
		fmt.Fprintf(&checks, "%s  %s\n", manifest[name], name)
	}
	encoded = []byte(checks.String())
	archive.add(&tar.Header{Name: "manifest.sha256", Mode: 0400, Size: int64(len(encoded))}, encoded)
	if err := archive.writer.Close(); archive.err == nil {
		archive.err = err
	}
	return data.Bytes(), bootstrap.Digest(manifest), archive.err
}
