// Package bootstrap implements the installation journal and the independent,
// evidence-driven executor. It does not depend on the Controller HTTP API.
package bootstrap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Action string

const (
	Init     Action = "init"
	Adopt    Action = "adopt"
	Repair   Action = "repair"
	Upgrade  Action = "upgrade"
	Rollback Action = "rollback"
	Verify   Action = "verify"
)

type State string

const (
	Pending   State = "PENDING"
	Running   State = "RUNNING"
	Succeeded State = "SUCCEEDED"
	Blocked   State = "BLOCKED"
	Failed    State = "FAILED"
	Unknown   State = "UNKNOWN"
)

// Credentials are file references, never values. The config itself is private:
// addresses and local paths are not included in public operation projections.
type Config struct {
	Mode              string    `json:"mode" yaml:"mode"`
	Name              string    `json:"name" yaml:"name"`
	MaintenanceNode   string    `json:"maintenance_node" yaml:"maintenance_node"`
	DataNode          string    `json:"data_node" yaml:"data_node"`
	NodeDir           string    `json:"node_dir" yaml:"node_dir"`
	ConsolePort       int       `json:"console_port" yaml:"console_port"`
	KnownHostsFile    string    `json:"known_hosts_file" yaml:"known_hosts_file"`
	Nodes             []Node    `json:"nodes" yaml:"nodes"`
	Images            Images    `json:"images" yaml:"images"`
	RegistryAuthFile  string    `json:"registry_auth_file,omitempty" yaml:"registry_auth_file,omitempty"`
	AdminPasswordFile string    `json:"admin_password_file" yaml:"admin_password_file"`
	Adoption          *Adoption `json:"adoption,omitempty" yaml:"adoption,omitempty"`
}

type Images struct {
	Controller string `json:"controller" yaml:"controller"`
	Gateway    string `json:"gateway" yaml:"gateway"`
	PostgreSQL string `json:"postgresql" yaml:"postgresql"`
}

type Node struct {
	Name       string `json:"name" yaml:"name"`
	Address    string `json:"address" yaml:"address"`
	SSHUser    string `json:"ssh_user" yaml:"ssh_user"`
	SSHPort    int    `json:"ssh_port" yaml:"ssh_port"`
	SSHKeyFile string `json:"ssh_key_file,omitempty" yaml:"ssh_key_file,omitempty"`
	Manager    bool   `json:"manager" yaml:"manager"`
}

// Adoption requires independent pre-existing registration and storage evidence;
// a Docker resource name/label is never an ownership claim.
type Adoption struct {
	InstallationID       string            `json:"installation_id,omitempty" yaml:"installation_id,omitempty"`
	ControllerConfigFile string            `json:"controller_config_file" yaml:"controller_config_file"`
	ClusterID            string            `json:"cluster_id" yaml:"cluster_id"`
	ServiceIDs           map[string]string `json:"service_ids" yaml:"service_ids"`
	DataNodeID           string            `json:"data_node_id" yaml:"data_node_id"`
	Volume               string            `json:"volume" yaml:"volume"`
}

var safeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)
var immutableImage = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9./_:-]*@sha256:[a-f0-9]{64}$`)

func DecodeConfig(data []byte) (Config, error) {
	var c Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&c); err != nil {
		return c, fmt.Errorf("invalid bootstrap config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return c, fmt.Errorf("exactly one configuration document is required")
	}
	if c.ConsolePort == 0 {
		c.ConsolePort = 8443
	}
	for i := range c.Nodes {
		if c.Nodes[i].SSHPort == 0 {
			c.Nodes[i].SSHPort = 22
		}
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.Mode != "single" && c.Mode != "multi" {
		return fmt.Errorf("mode must be single or multi")
	}
	if !safeName.MatchString(c.Name) || !filepath.IsAbs(c.NodeDir) || filepath.Clean(c.NodeDir) != c.NodeDir || c.NodeDir == "/" {
		return fmt.Errorf("a safe installation name and absolute node_dir are required")
	}
	if c.ConsolePort < 1024 || c.ConsolePort > 65535 || c.ConsolePort == 9443 || c.ConsolePort == 9444 {
		return fmt.Errorf("console_port must be an unprivileged port distinct from internal listeners")
	}
	for _, image := range []string{c.Images.Controller, c.Images.Gateway, c.Images.PostgreSQL} {
		if !immutableImage.MatchString(image) {
			return fmt.Errorf("all images require an immutable sha256 digest")
		}
	}
	if !filepath.IsAbs(c.KnownHostsFile) || (c.AdminPasswordFile != "" && !filepath.IsAbs(c.AdminPasswordFile)) || (c.RegistryAuthFile != "" && !filepath.IsAbs(c.RegistryAuthFile)) {
		return fmt.Errorf("credential and known-host references must be absolute files")
	}
	names, addresses := map[string]bool{}, map[string]bool{}
	managers := 0
	for _, n := range c.Nodes {
		if !safeName.MatchString(n.Name) || !safeName.MatchString(n.SSHUser) || net.ParseIP(n.Address) == nil || n.SSHPort < 1 || n.SSHPort > 65535 || (n.SSHKeyFile != "" && !filepath.IsAbs(n.SSHKeyFile)) || names[n.Name] || addresses[n.Address] {
			return fmt.Errorf("nodes require unique names and IP addresses, valid SSH users, ports and key references")
		}
		names[n.Name], addresses[n.Address] = true, true
		if n.Manager {
			managers++
		}
	}
	if !names[c.DataNode] || !names[c.MaintenanceNode] {
		return fmt.Errorf("data_node and maintenance_node must be inventory nodes")
	}
	if c.Mode == "single" && (len(c.Nodes) != 1 || managers != 1) {
		return fmt.Errorf("single mode requires exactly one Manager")
	}
	if c.Mode == "multi" && ((c.Adoption == nil && (len(c.Nodes) < 3 || managers != 3)) || (c.Adoption != nil && (len(c.Nodes) < 2 || managers < 1))) {
		return fmt.Errorf("new multi mode requires three Managers and at least three nodes")
	}
	for _, n := range c.Nodes {
		if n.Name == c.MaintenanceNode && !n.Manager {
			return fmt.Errorf("maintenance entry must be a Manager")
		}
	}
	if c.Adoption != nil {
		a := c.Adoption
		if (a.InstallationID != "" && !safeName.MatchString(a.InstallationID)) || !filepath.IsAbs(a.ControllerConfigFile) || a.ClusterID == "" || a.DataNodeID == "" || a.Volume == "" || len(a.ServiceIDs) != 4 {
			return fmt.Errorf("adoption requires configuration, cluster, storage and four service IDs")
		}
		seen := map[string]bool{}
		for _, k := range []string{"controller", "nginx", "nginx-sandbox", "postgresql"} {
			v := a.ServiceIDs[k]
			if v == "" || seen[v] {
				return fmt.Errorf("adoption service identities are missing or duplicated")
			}
			seen[v] = true
		}
	}
	return nil
}

func Digest(v any) string {
	data, _ := json.Marshal(v) // All contract types contain only JSON primitives.
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type Resource struct {
	Image      string `json:"image,omitempty"`
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Version    uint64 `json:"version"`
	SpecDigest string `json:"spec_digest"`
}

type Step struct {
	Key         string    `json:"key"`
	Description string    `json:"description"`
	Before      *Resource `json:"before,omitempty"`
	State       State     `json:"state"`
	Receipt     *Resource `json:"receipt,omitempty"`
	// Only stable, non-sensitive reason codes belong in the journal projection.
	Reason string `json:"reason,omitempty"`
}

type Plan struct {
	Scope             PlanScope `json:"scope"`
	InstallationID    string    `json:"installation_id"`
	Revision          int64     `json:"revision"`
	Action            Action    `json:"action"`
	ConfigDigest      string    `json:"config_digest"`
	ObservationDigest string    `json:"observation_digest"`
	CreatedAt         time.Time `json:"created_at"`
	ExpiresAt         time.Time `json:"expires_at"`
	Steps             []Step    `json:"steps"`
	Impact            []string  `json:"impact"`
	Hash              string    `json:"hash"`
}

type PlanTarget struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	Manager  bool   `json:"manager"`
	NodeID   string `json:"node_id,omitempty"`
	EngineID string `json:"engine_id"`
}

type PlanScope struct {
	Topology                     ManagerTopology `json:"topology"`
	Mode                         string          `json:"mode"`
	ClusterID                    string          `json:"cluster_id,omitempty"`
	Targets                      []PlanTarget    `json:"targets"`
	Images                       Images          `json:"images"`
	DataNode                     string          `json:"data_node"`
	DataVolume                   string          `json:"data_volume,omitempty"`
	DatabaseSinglePointOfFailure bool            `json:"database_single_point_of_failure"`
}

func (p Plan) WithScope(scope PlanScope) Plan { p.Scope = scope; p.Hash = p.digest(); return p }

func NewPlan(r Record, action Action, observation any, steps []Step, now time.Time) (Plan, error) {
	if action != Init && action != Adopt && action != Repair && action != Upgrade && action != Rollback && action != Verify {
		return Plan{}, fmt.Errorf("unsupported installation action")
	}
	if r.ID == "" || r.Revision < 1 || len(steps) == 0 {
		return Plan{}, fmt.Errorf("installation identity and nonempty steps are required")
	}
	steps = cloneSteps(steps)
	seen := map[string]bool{}
	for i := range steps {
		if steps[i].Key == "" || seen[steps[i].Key] {
			return Plan{}, fmt.Errorf("step keys must be unique")
		}
		seen[steps[i].Key] = true
		steps[i].State = Pending
		steps[i].Receipt = nil
		steps[i].Reason = ""
	}
	p := Plan{InstallationID: r.ID, Revision: r.Revision, Action: action, ConfigDigest: r.ConfigDigest, ObservationDigest: Digest(observation), CreatedAt: now.UTC(), ExpiresAt: now.UTC().Add(10 * time.Minute), Steps: steps,
		Impact: []string{"PostgreSQL is a single point of failure; its data node and volume are preserved.", "Failed or interrupted changes retain identities, data and diagnostic records."}}
	if action == Upgrade || action == Rollback {
		p.Impact = append(p.Impact, "A maintenance window may interrupt the console; database recovery is separate from image rollback.")
	}
	p.Hash = p.digest()
	return p, nil
}

func cloneSteps(steps []Step) []Step {
	result := append([]Step(nil), steps...)
	for n := range result {
		if result[n].Before != nil {
			value := *result[n].Before
			result[n].Before = &value
		}
		if result[n].Receipt != nil {
			value := *result[n].Receipt
			result[n].Receipt = &value
		}
	}
	return result
}

func validReceipt(receipt *Resource) bool {
	return receipt != nil && receipt.ID != "" && receipt.Kind != "" && receipt.SpecDigest != ""
}

func (p Plan) digest() string { p.Hash = ""; return Digest(p) }
func (p Plan) Validate(r Record, approved string, observation any, now time.Time) error {
	if approved == "" || approved != p.Hash || p.Hash != p.digest() {
		return fmt.Errorf("approval must match the unmodified plan digest")
	}
	if !now.Before(p.ExpiresAt) || now.Before(p.CreatedAt) || p.InstallationID != r.ID || p.Revision != r.Revision || p.ConfigDigest != r.ConfigDigest || p.ObservationDigest != Digest(observation) {
		return fmt.Errorf("plan is stale; inspect state and generate a new plan")
	}
	return nil
}

func ValidIdempotencyKey(key string) bool {
	return len(key) >= 8 && len(key) <= 128 && !strings.ContainsAny(key, "\r\n\x00")
}
