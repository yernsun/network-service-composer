package cluster

import (
	"github.com/moby/moby/api/types/container"
	"testing"
	"time"
)

func TestMetricAccountingPreservesUnknownCountersAndPlatformLimits(t *testing.T) {
	now := time.Now().UTC()
	previous := container.StatsResponse{CPUStats: container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: 100}, SystemUsage: 1000}}
	for _, tc := range []struct {
		name          string
		cpus          uint32
		perCPU        []uint64
		total, system uint64
		want          *float64
	}{
		{"online CPU count", 4, nil, 150, 1200, ptr(100.0)},
		{"per CPU fallback", 0, []uint64{1, 2}, 150, 1200, ptr(50.0)},
		{"unknown CPU count", 0, nil, 150, 1200, nil},
		{"unchanged system counter", 4, nil, 150, 1000, nil},
		{"reset system counter", 4, nil, 150, 999, nil},
		{"reset container counter", 4, nil, 99, 1200, nil},
		{"idle container", 4, nil, 100, 1200, ptr(0.0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := container.StatsResponse{CPUStats: container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: tc.total, PercpuUsage: tc.perCPU}, SystemUsage: tc.system, OnlineCPUs: tc.cpus}}
			m := sample(current, &previous, now)
			if (m.CPUPercent == nil) != (tc.want == nil) || tc.want != nil && *m.CPUPercent != *tc.want {
				t.Fatal("CPU reset or missing evidence became a utilization value", m.CPUPercent, tc.want)
			}
			if m.NetworkRX != nil || m.NetworkTX != nil || m.BlockRead != nil || m.BlockWrite != nil {
				t.Fatal("unreported counters became measured zero")
			}
		})
	}
	current := container.StatsResponse{MemoryStats: container.MemoryStats{Usage: 1000, Limit: 2000, Stats: map[string]uint64{"total_inactive_file": 250, "inactive_file": 800}}, Networks: map[string]container.NetworkStats{"one": {RxBytes: 3, TxBytes: 7}, "two": {RxBytes: 11, TxBytes: 13}}, BlkioStats: container.BlkioStats{IoServiceBytesRecursive: []container.BlkioStatEntry{{Op: "Read", Value: 5}, {Op: "read", Value: 6}, {Op: "WRITE", Value: 8}, {Op: "Total", Value: 1000}}}}
	m := sample(current, nil, now)
	if *m.MemoryUsage != 750 || *m.MemoryRaw != 1000 || *m.MemoryLimit != 2000 || *m.NetworkRX != 14 || *m.NetworkTX != 20 || *m.BlockRead != 11 || *m.BlockWrite != 8 || !m.At.Equal(now) {
		t.Fatal("metric counters double counted or discarded", m)
	}
	current.MemoryStats.Stats["total_inactive_file"] = 1001
	if m = sample(current, nil, now); *m.MemoryUsage != 1000 {
		t.Fatal("cache accounting underflowed memory usage")
	}
	current.OSType = "windows"
	if m = sample(current, &previous, now); m.Error != "unsupported_platform" || m.CPUPercent != nil || m.MemoryUsage != nil || m.NetworkRX != nil {
		t.Fatal("unsupported platform produced Linux accounting", m)
	}
}
