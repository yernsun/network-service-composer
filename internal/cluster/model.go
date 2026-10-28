// Package cluster provides a read-only Docker inventory independent of discovery
// and routing. The only mutation is an explicitly versioned Swarm label update.
package cluster

import (
	"github.com/yernsun/network-service-composer/internal/hoststats"
	"github.com/yernsun/network-service-composer/internal/model"
	"time"
)

type Node struct {
	Host              *hoststats.Metrics `json:"host,omitempty"`
	Controller        *hoststats.Health  `json:"controller,omitempty"`
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	EngineID          string             `json:"engine_id"`
	Role              string             `json:"role"`
	State             string             `json:"state"`
	Availability      string             `json:"availability"`
	Leader            bool               `json:"leader"`
	Reachability      string             `json:"reachability"`
	EngineVersion     string             `json:"engine_version"`
	Address           string             `json:"address"`
	CPUs              float64            `json:"cpus"`
	MemoryBytes       int64              `json:"memory_bytes"`
	Labels            map[string]string  `json:"labels"`
	EngineLabels      map[string]string  `json:"engine_labels"`
	Version           uint64             `json:"version"`
	Virtual           bool               `json:"virtual"`
	Connected         bool               `json:"connected"`
	InventoryComplete bool               `json:"inventory_complete"`
	ObservedAt        time.Time          `json:"observed_at"`
	Stale             bool               `json:"stale"`
	Error             string             `json:"error"`
	CanEditLabels     bool               `json:"can_edit_labels"`
	ReadOnlyReason    string             `json:"read_only_reason"`
	Workloads         int                `json:"workloads"`
	Instances         int                `json:"instances"`
	Running           int                `json:"running"`
}
type Workload struct {
	Environment *model.EnvironmentReference `json:"environment,omitempty"`
	ID          string                      `json:"id"`
	NodeID      string                      `json:"node_id"`
	Name        string                      `json:"name"`
	Project     string                      `json:"project"`
	Kind        string                      `json:"kind"`
	ServiceID   string                      `json:"service_id,omitempty"`
	Instances   int                         `json:"instances"`
	Running     int                         `json:"running"`
}
type Instance struct {
	ID                 string     `json:"id"`
	NodeID             string     `json:"node_id"`
	WorkloadID         string     `json:"workload_id"`
	Name               string     `json:"name"`
	Kind               string     `json:"kind"`
	TaskID             string     `json:"task_id,omitempty"`
	ContainerID        string     `json:"container_id,omitempty"`
	EngineID           string     `json:"engine_id,omitempty"`
	ServiceID          string     `json:"service_id,omitempty"`
	Image              string     `json:"image"`
	State              string     `json:"state"`
	DesiredState       string     `json:"desired_state"`
	Health             string     `json:"health"`
	Addresses          []string   `json:"addresses"`
	Ports              []string   `json:"ports"`
	Error              string     `json:"error"`
	Historical         bool       `json:"historical"`
	CreatedAt          *time.Time `json:"created_at,omitempty"`
	UpdatedAt          *time.Time `json:"updated_at,omitempty"`
	StateChangedAt     *time.Time `json:"state_changed_at,omitempty"`
	ContainerCreatedAt *time.Time `json:"container_created_at,omitempty"`
	StartedAt          *time.Time `json:"started_at,omitempty"`
	FinishedAt         *time.Time `json:"finished_at,omitempty"`
	RestartCount       *int       `json:"restart_count,omitempty"`
	ExitCode           *int       `json:"exit_code,omitempty"`
	OOMKilled          *bool      `json:"oom_killed,omitempty"`
	ObservedAt         time.Time  `json:"observed_at"`
	Metric             *Metric    `json:"metric"`
}
type Metric struct {
	At          time.Time `json:"at"`
	CPUPercent  *float64  `json:"cpu_percent"`
	MemoryUsage *uint64   `json:"memory_usage"`
	MemoryRaw   *uint64   `json:"memory_raw"`
	MemoryLimit *uint64   `json:"memory_limit"`
	NetworkRX   *uint64   `json:"network_rx"`
	NetworkTX   *uint64   `json:"network_tx"`
	BlockRead   *uint64   `json:"block_read"`
	BlockWrite  *uint64   `json:"block_write"`
	Error       string    `json:"error"`
	Stale       bool      `json:"stale"`
}
type Metrics struct {
	Source            string   `json:"source"`
	UnavailableReason string   `json:"unavailable_reason"`
	Current           *Metric  `json:"current"`
	Samples           []Metric `json:"samples"`
	RetentionSeconds  int      `json:"retention_seconds"`
}
type Overview struct {
	RuntimeID            string    `json:"runtime_id"`
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	Kind                 string    `json:"kind"`
	Nodes                int       `json:"nodes"`
	ReadyNodes           int       `json:"ready_nodes"`
	Managers             int       `json:"managers"`
	ConnectedNodes       int       `json:"connected_nodes"`
	Services             int       `json:"services"`
	Instances            int       `json:"instances"`
	Running              int       `json:"running"`
	ObservedAt           time.Time `json:"observed_at"`
	Stale                bool      `json:"stale"`
	InventoryComplete    bool      `json:"inventory_complete"`
	Error                string    `json:"error"`
	HostMetricsAvailable bool      `json:"host_metrics_available"`
	// ManagerAvailable describes this Runtime endpoint, not node collectors or actor permissions.
	ManagerAvailable bool `json:"manager_available"`
}
type Inventory struct {
	Overview  Overview
	Nodes     []Node
	Workloads []Workload
	Instances []Instance
}
