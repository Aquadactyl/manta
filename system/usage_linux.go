//go:build linux

package system

import (
	"bufio"
	"context"
	"errors"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const HostUsageSupported = true

func ReadResourceUsage(ctx context.Context, dataPath string) ResourceUsage {
	usage := ResourceUsage{SampledAt: time.Now().UTC()}
	if content, err := os.ReadFile("/proc/meminfo"); err == nil {
		if total, used, err := parseMemoryUsage(string(content)); err == nil {
			usage.MemoryTotalBytes, usage.MemoryUsedBytes = &total, &used
		}
	}
	var disk unix.Statfs_t
	if err := unix.Statfs(dataPath, &disk); err == nil && disk.Bsize > 0 {
		total := disk.Blocks * uint64(disk.Bsize)
		used := (disk.Blocks - min(disk.Bfree, disk.Blocks)) * uint64(disk.Bsize)
		usage.DiskTotalBytes, usage.DiskUsedBytes = &total, &used
	}
	before, err := os.ReadFile("/proc/stat")
	if err != nil {
		return usage
	}
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return usage
	case <-timer.C:
	}
	if after, err := os.ReadFile("/proc/stat"); err == nil {
		if percent, err := cpuUsageBetween(string(before), string(after)); err == nil {
			usage.CPUPercent = &percent
		}
	}
	usage.SampledAt = time.Now().UTC()
	return usage
}

func parseMemoryUsage(content string) (uint64, uint64, error) {
	values := make(map[string]uint64, 2)
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || (fields[0] != "MemTotal:" && fields[0] != "MemAvailable:") {
			continue
		}
		if len(fields) != 3 || fields[2] != "kB" {
			return 0, 0, errors.New("invalid memory counters")
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || value > math.MaxUint64/1024 {
			return 0, 0, errors.New("invalid memory value")
		}
		values[fields[0]] = value * 1024
	}
	total, totalOK := values["MemTotal:"]
	available, availableOK := values["MemAvailable:"]
	if scanner.Err() != nil || !totalOK || !availableOK || total == 0 || available > total {
		return 0, 0, errors.New("memory counters unavailable")
	}
	return total, total - available, nil
}

func cpuUsageBetween(before, after string) (float64, error) {
	read := func(content string) (uint64, uint64, error) {
		line, _, _ := strings.Cut(content, "\n")
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			return 0, 0, errors.New("CPU counters unavailable")
		}
		var total, idle uint64
		// Guest counters are already included in user/nice; do not count twice.
		for i := 1; i < min(len(fields), 9); i++ {
			value, err := strconv.ParseUint(fields[i], 10, 64)
			if err != nil || value > math.MaxUint64-total {
				return 0, 0, errors.New("invalid CPU value")
			}
			total += value
			if i == 4 || i == 5 {
				idle += value
			}
		}
		return total, idle, nil
	}
	previous, previousIdle, err := read(before)
	if err != nil {
		return 0, err
	}
	current, currentIdle, err := read(after)
	if err != nil || current <= previous || currentIdle < previousIdle || currentIdle-previousIdle > current-previous {
		return 0, errors.New("CPU sample unavailable")
	}
	return 100 * (1 - float64(currentIdle-previousIdle)/float64(current-previous)), nil
}
