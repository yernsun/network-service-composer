package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"golang.org/x/sys/unix"
)

type Record struct {
	ID                  string              `json:"id"`
	Revision            int64               `json:"revision"`
	ConfigDigest        string              `json:"config_digest"`
	MaintenanceIdentity string              `json:"maintenance_identity"`
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
	var r Record
	fd, err := unix.Open(filepath.Join(j.dir, "installation.json"), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return r, err
	}
	f := os.NewFile(uintptr(fd), "installation.json")
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&r)
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
		if r.ConfigDigest != Digest(c) || r.MaintenanceIdentity != maintenanceIdentity {
			return r, fmt.Errorf("configuration or maintenance host differs from the durable installation identity")
		}
		return r, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return r, err
	}
	r = Record{ID: id.New("install"), Revision: 1, ConfigDigest: Digest(c), MaintenanceIdentity: maintenanceIdentity, PoolID: id.New(id.Pool), RuntimeID: id.New(id.Runtime), Nodes: map[string]Identity{}, Resources: map[string]Resource{}, Operations: []Operation{}}
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
