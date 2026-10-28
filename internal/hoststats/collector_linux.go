package hoststats

import (
	"bufio"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type Collector struct {
	Proc        string
	Root        string
	mu          sync.Mutex
	previous    [8]uint64
	initialized bool
}

func (c *Collector) Collect() Metrics {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := Metrics{At: time.Now().UTC(), Disks: []Disk{}, Errors: map[string]string{}}
	if c.Proc == "" || c.Root == "" {
		result.Errors["host"] = "not_configured"
		return result
	}
	raw, err := os.ReadFile(filepath.Join(c.Proc, "stat"))
	if err != nil {
		result.Errors["cpu"] = "unavailable"
		c.initialized = false
	} else {
		fields := strings.Fields(strings.SplitN(string(raw), "\n", 2)[0])
		var current [8]uint64
		valid := len(fields) >= 9 && fields[0] == "cpu"
		if valid {
			for i := range current {
				current[i], err = strconv.ParseUint(fields[i+1], 10, 64)
				if err != nil {
					valid = false
					break
				}
			}
		}
		if !valid {
			result.Errors["cpu"] = "invalid"
			c.initialized = false
		} else {
			if c.initialized {
				var total, idle uint64
				for i := range current {
					if current[i] < c.previous[i] {
						valid = false
						break
					}
					delta := current[i] - c.previous[i]
					if math.MaxUint64-total < delta {
						valid = false
						break
					}
					total += delta
					if i == 3 || i == 4 {
						idle += delta
					}
				}
				if valid && total > 0 {
					value := 100 * float64(total-idle) / float64(total)
					result.CPUPercent = &value
				} else {
					result.Errors["cpu"] = "counter_reset"
				}
			} else {
				result.Errors["cpu"] = "warming_up"
			}
			c.previous, c.initialized = current, true
		}
	}
	raw, err = os.ReadFile(filepath.Join(c.Proc, "meminfo"))
	if err != nil {
		result.Errors["memory"] = "unavailable"
	} else {
		values := map[string]uint64{}
		for _, line := range strings.Split(string(raw), "\n") {
			f := strings.Fields(line)
			if len(f) != 3 || f[2] != "kB" {
				continue
			}
			if f[0] != "MemTotal:" && f[0] != "MemAvailable:" {
				continue
			}
			n, e := strconv.ParseUint(f[1], 10, 64)
			if e == nil && n <= math.MaxUint64/1024 {
				values[f[0]] = n * 1024
			}
		}
		total, ok := values["MemTotal:"]
		available, known := values["MemAvailable:"]
		if ok && known && total > 0 && available <= total {
			used := total - available
			result.MemoryTotal = &total
			result.MemoryUsed = &used
		} else {
			result.Errors["memory"] = "invalid"
		}
	}
	mounts, err := os.Open(filepath.Join(c.Proc, "1/mountinfo"))
	if err != nil {
		result.Errors["disk"] = "unavailable"
		return result
	}
	defer mounts.Close()
	root, err := os.OpenRoot(c.Root)
	if err != nil {
		result.Errors["disk"] = "unavailable"
		return result
	}
	defer root.Close()
	seen := map[string]int{}
	scan := bufio.NewScanner(mounts)
	scan.Buffer(make([]byte, 4096), 1024*1024)
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		separator := -1
		for i, f := range fields {
			if f == "-" {
				separator = i
				break
			}
		}
		if len(fields) < 7 || separator < 6 || separator+1 >= len(fields) {
			result.Errors["disk"] = "invalid"
			continue
		}
		fs := fields[separator+1]
		switch fs {
		case "ext2", "ext3", "ext4", "xfs", "btrfs", "zfs", "f2fs", "vfat", "exfat", "ntfs", "ntfs3":
		default:
			continue
		}
		mount := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(fields[4])
		if !strings.HasPrefix(mount, "/") || filepath.Clean(mount) != mount {
			result.Errors["disk"] = "invalid"
			continue
		}
		// Bind mounts and subvolumes share filesystem capacity. Show each
		// filesystem once, preferring its shortest visible mount path.
		device := fields[2] + ":" + fs
		index, exists := seen[device]
		if exists && len(result.Disks[index].Mount) <= len(mount) {
			continue
		}
		path := strings.TrimPrefix(mount, "/")
		if path == "" {
			path = "."
		}
		directory, e := root.Open(path)
		if e != nil {
			result.Errors["disk:"+mount] = "unavailable"
			continue
		}
		var stats unix.Statfs_t
		e = unix.Fstatfs(int(directory.Fd()), &stats)
		_ = directory.Close()
		if e != nil || stats.Bsize <= 0 || stats.Blocks > math.MaxUint64/uint64(stats.Bsize) || stats.Bfree > stats.Blocks || stats.Bavail > stats.Blocks {
			result.Errors["disk:"+mount] = "unavailable"
			continue
		}
		size := uint64(stats.Bsize)
		disk := Disk{Mount: mount, Filesystem: fs, Total: stats.Blocks * size, Used: (stats.Blocks - stats.Bfree) * size, Available: stats.Bavail * size, ReadOnly: strings.Contains(","+fields[5]+",", ",ro,")}
		if exists {
			result.Disks[index] = disk
		} else {
			seen[device] = len(result.Disks)
			result.Disks = append(result.Disks, disk)
		}
	}
	if scan.Err() != nil {
		result.Errors["disk"] = "unavailable"
	}
	if len(result.Disks) == 0 && result.Errors["disk"] == "" {
		result.Errors["disk"] = "unavailable"
	}
	return result
}
