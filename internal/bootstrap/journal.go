package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"golang.org/x/sys/unix"
)

type Record struct {
	Preparation                 *PreparationReferences `json:"preparation,omitempty"`
	ConsoleCertificateExpiresAt *time.Time             `json:"console_certificate_expires_at,omitempty"`
	ConsoleFingerprint          string                 `json:"console_fingerprint,omitempty"`
	Database                    DatabasePin            `json:"database"`
	MetadataLayout              int                    `json:"metadata_layout"`
	MaintenanceUID              int                    `json:"maintenance_uid"`
	MaintenanceID               string                 `json:"maintenance_id"`
	TrustSHA256                 string                 `json:"trust_anchor_sha256,omitempty"`
	MaintenanceExpiresAt        *time.Time             `json:"maintenance_expires_at,omitempty"`
	CheckpointRevision          int64                  `json:"checkpoint_revision"`
	Release                     *Release               `json:"release,omitempty"`
	History                     []ReleaseReceipt       `json:"history"`

	ID                  string              `json:"id"`
	Revision            int64               `json:"revision"`
	ConfigDigest        string              `json:"config_digest"`
	MaintenanceAnchor   string              `json:"maintenance_anchor"`
	MaintenanceIdentity string              `json:"maintenance_identity"`
	Topology            ManagerTopology     `json:"topology"`
	ClusterID           string              `json:"cluster_id,omitempty"`
	PoolID              string              `json:"pool_id"`
	RuntimeID           string              `json:"runtime_id"`
	CertificateID       string              `json:"certificate_id,omitempty"`
	Nodes               map[string]Identity `json:"nodes"`
	Resources           map[string]Resource `json:"resources"`
	Operations          []Operation         `json:"operations"`
	UpdatedAt           time.Time           `json:"updated_at"`
}

type Identity struct {
	InstanceID string `json:"instance_id"`
	GatewayID  string `json:"gateway_id"`
	SandboxID  string `json:"sandbox_id"`
	NodeID     string `json:"node_id,omitempty"`
	EngineID   string `json:"engine_id,omitempty"`
	SocketGID  string `json:"socket_gid,omitempty"`
}

type Operation struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	Plan      Plan      `json:"plan"`
	State     State     `json:"state"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Journal is deliberately node-local. An OS lock and the durable maintenance
// identity prevent a copied checkpoint from authorizing a second writer.
type Journal struct {
	dir  string
	lock *os.File
}

func OpenJournal(dir string) (*Journal, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, fmt.Errorf("journal requires an absolute clean directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("journal must be a private directory, not a symlink")
	}
	if _, err := os.Lstat(filepath.Join(dir, "executor.lock")); os.IsNotExist(err) {
		if _, recordErr := os.Lstat(filepath.Join(dir, "installation.json")); !os.IsNotExist(recordErr) {
			return nil, fmt.Errorf("the installation lock is missing; fence previous writers before restoring it")
		}
	}
	fd, err := unix.Open(filepath.Join(dir, "executor.lock"), unix.O_NOFOLLOW|unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "executor.lock")
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("installation already has an active executor: %w", err)
	}
	return &Journal{dir: dir, lock: f}, nil
}

func (j *Journal) Close() error { return j.lock.Close() }
func (j *Journal) Read() (Record, error) {
	return ReadJournal(j.dir)
}

// ReadJournal observes atomic checkpoints without acquiring the writer lock.
// It never creates a directory or changes execution ownership.
func ReadJournal(dir string) (Record, error) {
	var r Record
	info, err := os.Lstat(dir)
	if err != nil {
		return r, err
	}
	if !filepath.IsAbs(dir) || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return r, fmt.Errorf("journal must be an absolute private directory")
	}
	fd, err := unix.Open(filepath.Join(dir, "installation.json"), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return r, err
	}
	f := os.NewFile(uintptr(fd), "installation.json")
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&r)
	if err == nil && decoder.Decode(&struct{}{}) != io.EOF {
		err = fmt.Errorf("installation journal contains trailing data")
	}
	if err == nil && (r.ID == "" || r.Revision < 1) {
		err = fmt.Errorf("invalid installation journal")
	}
	return r, err
}

func (j *Journal) Initialize(c Config, maintenanceIdentity string) (Record, error) {
	if err := c.Validate(); err != nil {
		return Record{}, err
	}
	if maintenanceIdentity == "" {
		return Record{}, fmt.Errorf("maintenance host identity is required")
	}
	r, err := j.Read()
	if err == nil {
		if r.ConfigDigest != Digest(c) || r.MaintenanceIdentity != maintenanceIdentity || (r.MaintenanceAnchor != "" && r.MaintenanceAnchor != Digest([]string{maintenanceIdentity, j.dir})) {
			return r, fmt.Errorf("configuration or maintenance host differs from the durable installation identity")
		}
		if r.MaintenanceAnchor == "" {
			r.MaintenanceAnchor = Digest([]string{maintenanceIdentity, j.dir})
			return r, j.Save(&r)
		}
		return r, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return r, err
	}
	owner, err := os.Stat(j.dir)
	if err != nil {
		return r, err
	}
	uid := int(owner.Sys().(*syscall.Stat_t).Uid)
	r = Record{MetadataLayout: 1, MaintenanceUID: uid, MaintenanceID: id.New("maint"), History: []ReleaseReceipt{}, ID: id.New("install"), Revision: 1, ConfigDigest: Digest(c), MaintenanceIdentity: maintenanceIdentity, MaintenanceAnchor: Digest([]string{maintenanceIdentity, j.dir}), PoolID: id.New(id.Pool), RuntimeID: id.New(id.Runtime), CertificateID: id.New(id.Certificate), Nodes: map[string]Identity{}, Resources: map[string]Resource{}, Operations: []Operation{}}
	if c.Adoption != nil && c.Adoption.InstallationID != "" {
		r.ID = c.Adoption.InstallationID
	}
	for _, n := range c.Nodes {
		r.Nodes[n.Name] = Identity{InstanceID: id.New("ctrl"), GatewayID: id.New(id.Gateway), SandboxID: id.New(id.Gateway)}
	}
	return r, j.Save(&r)
}

func (j *Journal) Save(r *Record) error {
	r.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return AtomicPrivateFile(j.dir, "installation.json", append(data, '\n'))
}

func AtomicPrivateFile(dir, name string, data []byte) error {
	if filepath.Base(name) != name {
		return fmt.Errorf("private file name must be a base name")
	}
	f, err := os.CreateTemp(dir, ".checkpoint-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if os.Geteuid() == 0 {
		info, ownerErr := os.Stat(dir)
		if ownerErr != nil {
			return ownerErr
		}
		owner := info.Sys().(*syscall.Stat_t)
		if ownerErr = f.Chown(int(owner.Uid), int(owner.Gid)); ownerErr != nil {
			return ownerErr
		}
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
