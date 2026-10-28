package cluster

import (
	"github.com/moby/moby/api/types/container"
	"strings"
	"time"
)

const retention = time.Hour
const maxSamples = 360
const maxSeries = 2048

type series struct {
	Previous    *container.StatsResponse
	Points      []Metric
	LastSuccess *Metric
	LastAttempt time.Time
}

func ptr[T any](v T) *T { return &v }

// Linux Docker CLI accounting: subtract inactive file cache, retaining raw usage.
// CPU is a percentage of one core (and may exceed 100 on multicore containers).
func sample(current container.StatsResponse, previous *container.StatsResponse, at time.Time) Metric {
	m := Metric{At: at}
	if current.OSType == "windows" {
		m.Error = "unsupported_platform"
		return m
	}
	if previous != nil && current.CPUStats.CPUUsage.TotalUsage >= previous.CPUStats.CPUUsage.TotalUsage && current.CPUStats.SystemUsage > previous.CPUStats.SystemUsage {
		cpus := current.CPUStats.OnlineCPUs
		if cpus == 0 {
			cpus = uint32(len(current.CPUStats.CPUUsage.PercpuUsage))
		}
		if cpus > 0 {
			cpu := float64(current.CPUStats.CPUUsage.TotalUsage-previous.CPUStats.CPUUsage.TotalUsage) / float64(current.CPUStats.SystemUsage-previous.CPUStats.SystemUsage) * float64(cpus) * 100
			m.CPUPercent = &cpu
		}
	}
	usage := current.MemoryStats.Usage
	cache, ok := current.MemoryStats.Stats["total_inactive_file"]
	if !ok {
		cache = current.MemoryStats.Stats["inactive_file"]
	}
	if cache < usage {
		usage -= cache
	}
	m.MemoryUsage, m.MemoryRaw, m.MemoryLimit = ptr(usage), ptr(current.MemoryStats.Usage), ptr(current.MemoryStats.Limit)
	if current.Networks != nil {
		var rx, tx uint64
		for _, n := range current.Networks {
			rx += n.RxBytes
			tx += n.TxBytes
		}
		m.NetworkRX, m.NetworkTX = ptr(rx), ptr(tx)
	}
	if current.BlkioStats.IoServiceBytesRecursive != nil {
		var read, write uint64
		for _, v := range current.BlkioStats.IoServiceBytesRecursive {
			switch strings.ToLower(v.Op) {
			case "read":
				read += v.Value
			case "write":
				write += v.Value
			}
		}
		m.BlockRead, m.BlockWrite = ptr(read), ptr(write)
	}
	return m
}
func (s *series) append(m Metric, now time.Time) {
	s.LastAttempt = now
	start := 0
	for start < len(s.Points) && s.Points[start].At.Before(now.Add(-retention)) {
		start++
	}
	s.Points = append(s.Points[start:], m)
	if len(s.Points) > maxSamples {
		s.Points = s.Points[len(s.Points)-maxSamples:]
	}
	if m.Error == "" {
		s.LastSuccess = &m
	}
}
func (m *Monitor) Metrics(engineID, containerID string) Metrics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := Metrics{Source: "docker", UnavailableReason: "not_connected", Samples: []Metric{}, RetentionSeconds: 3600}
	s := m.metrics[engineID+"/"+containerID]
	if s == nil {
		if engineID != "" {
			result.UnavailableReason = "collecting"
		}
		return result
	}
	now := m.now()
	result.UnavailableReason = ""
	for _, p := range s.Points {
		if !p.At.Before(now.Add(-retention)) {
			result.Samples = append(result.Samples, p)
		}
	}
	if s.LastSuccess != nil && !s.LastSuccess.At.Before(now.Add(-retention)) {
		v := *s.LastSuccess
		v.Stale = now.Sub(v.At) > 30*time.Second || (len(s.Points) > 0 && s.Points[len(s.Points)-1].Error != "")
		if v.Stale {
			result.UnavailableReason = "stale"
		}
		result.Current = &v
	} else {
		result.UnavailableReason = "collecting"
	}
	return result
}
