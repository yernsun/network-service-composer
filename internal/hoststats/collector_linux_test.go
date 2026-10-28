package hoststats

import (
	"os"
	"path/filepath"
	"testing"
)

func hostFixture(t *testing.T) *Collector {
	t.Helper()
	proc, root := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(proc, "1"), 0700); err != nil {
		t.Fatal(err)
	}
	writeHost(t, proc, "stat", "cpu 100 0 50 800 50 0 0 0 1000 1000\n")
	writeHost(t, proc, "meminfo", "MemTotal: 1000 kB\nMemAvailable: 400 kB\n")
	writeHost(t, proc, "1/mountinfo", "30 1 8:1 / / rw - ext4 /dev/root rw\n31 1 0:1 / /proc rw - proc proc rw\n")
	return &Collector{Proc: proc, Root: root}
}
func writeHost(t *testing.T, root, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestHostMeasurementsUseDeltasAvailableMemoryAndRealFilesystems(t *testing.T) {
	c := hostFixture(t)
	first := c.Collect()
	if first.CPUPercent != nil || first.Errors["cpu"] != "warming_up" {
		t.Fatal("first sample invented CPU usage", first)
	}
	if first.MemoryUsed == nil || *first.MemoryUsed != 600*1024 || *first.MemoryTotal != 1000*1024 {
		t.Fatal("host memory did not use MemAvailable", first)
	}
	if len(first.Disks) != 1 || first.Disks[0].Mount != "/" || first.Disks[0].Total == 0 || first.Disks[0].Used > first.Disks[0].Total {
		t.Fatal("host disk observation missing", first.Disks)
	}
	writeHost(t, c.Proc, "stat", "cpu 160 0 60 860 60 0 0 0 90000 90000\n")
	second := c.Collect()
	if second.CPUPercent == nil || *second.CPUPercent != 50 {
		t.Fatal("guest time counted twice or CPU delta incorrect", second.CPUPercent)
	}
	writeHost(t, c.Proc, "stat", "cpu 1 0 1 1 0 0 0 0\n")
	if reset := c.Collect(); reset.CPUPercent != nil || reset.Errors["cpu"] != "counter_reset" {
		t.Fatal("counter reset fabricated usage", reset)
	}
}
func TestMissingAndMalformedHostMetricsStayUnavailable(t *testing.T) {
	if v := (&Collector{}).Collect(); v.CPUPercent != nil || v.MemoryUsed != nil || v.Errors["host"] != "not_configured" {
		t.Fatal(v)
	}
	for _, mode := range []string{"missing", "malformed", "overflow", "memory unavailable"} {
		t.Run(mode, func(t *testing.T) {
			c := hostFixture(t)
			switch mode {
			case "missing":
				_ = os.Remove(filepath.Join(c.Proc, "stat"))
				_ = os.Remove(filepath.Join(c.Proc, "meminfo"))
				_ = os.Remove(filepath.Join(c.Proc, "1/mountinfo"))
			case "malformed":
				writeHost(t, c.Proc, "stat", "cpu bad 1 2\n")
				writeHost(t, c.Proc, "meminfo", "MemTotal: 12 kB\nMemAvailable: 20 kB\n")
			case "overflow":
				writeHost(t, c.Proc, "stat", "cpu 18446744073709551616 0 0 0 0 0 0 0\n")
				writeHost(t, c.Proc, "meminfo", "MemTotal: 18446744073709551615 kB\nMemAvailable: 10 kB\n")
			case "memory unavailable":
				writeHost(t, c.Proc, "meminfo", "MemTotal: 1000 kB\nMemFree: 400 kB\n")
			}
			v := c.Collect()
			if v.CPUPercent != nil || v.MemoryUsed != nil || v.Errors["memory"] == "" {
				t.Fatal("missing host metrics fabricated", v)
			}
		})
	}
}
func TestDiskMountsDeduplicateAndCannotEscapeHostRoot(t *testing.T) {
	c := hostFixture(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(c.Root, "escape")); err != nil {
		t.Fatal(err)
	}
	writeHost(t, c.Proc, "1/mountinfo", "30 1 8:1 / / ro - ext4 /dev/root ro\n31 1 8:1 / /duplicate ro - ext4 /dev/root ro\n32 1 8:2 / /escape ro - ext4 /dev/other ro\n")
	v := c.Collect()
	if len(v.Disks) != 1 || !v.Disks[0].ReadOnly || v.Errors["disk:/escape"] != "unavailable" {
		t.Fatal("unsafe or duplicate mount accepted", v)
	}
}
