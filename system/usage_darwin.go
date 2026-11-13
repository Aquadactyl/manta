//go:build darwin

package system

import (
	"context"
	"time"

	"golang.org/x/sys/unix"
)

// HostUsageSupported reports that host counters are available.
const HostUsageSupported = true

// ReadResourceUsage returns the host counters.
//
// Memory is the physical memory of the Mac and what is in use, which is added up
// from the Mach virtual memory counters the way Activity Monitor does it, see
// memoryUsedFromVMStats. Disk is the volume holding the server data. CPU is the
// utilization of all CPUs over a 200 ms sample, as on Linux.
func ReadResourceUsage(ctx context.Context, dataPath string) ResourceUsage {
	usage := ResourceUsage{SampledAt: time.Now().UTC()}

	if total, err := unix.SysctlUint64("hw.memsize"); err == nil {
		if vm, pageSize, err := readVMStats(); err == nil {
			if used, err := memoryUsedFromVMStats(vm, pageSize, total); err == nil {
				usage.MemoryTotalBytes, usage.MemoryUsedBytes = &total, &used
			}
		}
	}

	var disk unix.Statfs_t
	if err := unix.Statfs(dataPath, &disk); err == nil && disk.Bsize > 0 {
		total := disk.Blocks * uint64(disk.Bsize)
		used := (disk.Blocks - min(disk.Bfree, disk.Blocks)) * uint64(disk.Bsize)
		usage.DiskTotalBytes, usage.DiskUsedBytes = &total, &used
	}

	before, err := readCPUTicks()
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
	if after, err := readCPUTicks(); err == nil {
		if percent, err := cpuPercentFromTicks(before, after); err == nil {
			usage.CPUPercent = &percent
		}
	}
	usage.SampledAt = time.Now().UTC()

	return usage
}
