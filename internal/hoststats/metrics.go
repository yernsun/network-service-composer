// Package hoststats measures the host explicitly mounted into a Controller.
// Container statistics and hardware capacity are never substituted for host use.
package hoststats

import "time"

type Metrics struct {
	At          time.Time         `json:"at"`
	CPUPercent  *float64          `json:"cpu_percent"`
	MemoryUsed  *uint64           `json:"memory_used"`
	MemoryTotal *uint64           `json:"memory_total"`
	Disks       []Disk            `json:"disks"`
	Errors      map[string]string `json:"errors"`
	Stale       bool              `json:"stale"`
}

type Disk struct {
	Mount      string `json:"mount"`
	Filesystem string `json:"filesystem"`
	Total      uint64 `json:"total"`
	Used       uint64 `json:"used"`
	Available  uint64 `json:"available"`
	ReadOnly   bool   `json:"read_only"`
}

type Health struct {
	Execution  string    `json:"execution,omitempty"`
	InstanceID string    `json:"instance_id"`
	Controller string    `json:"controller"`
	Role       string    `json:"role"`
	Term       int64     `json:"term"`
	Collector  string    `json:"collector"`
	Gateway    string    `json:"gateway"`
	At         time.Time `json:"at"`
	Stale      bool      `json:"stale"`
}
