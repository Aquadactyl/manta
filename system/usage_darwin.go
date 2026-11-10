//go:build darwin

package system

import (
	"context"
	"time"

	"golang.org/x/sys/unix"
)

// HostUsageSupported reports that host counters are available. On macOS memory
// and disk are, CPU is not: it is only exposed through Mach calls, which need
// cgo, so it is always reported as unavailable.
const HostUsageSupported = true

// ReadResourceUsage returns the host counters.
//
// Memory is the physical memory of the Mac and the share of it the kernel
// reports as available, the same figure the memory pressure graph of Activity
// Monitor is based on. Disk is the volume holding the server data.
func ReadResourceUsage(_ context.Context, dataPath string) ResourceUsage {
	usage := ResourceUsage{SampledAt: time.Now().UTC()}

	if total, err := unix.SysctlUint64("hw.memsize"); err == nil {
		if free, err := unix.SysctlUint32("kern.memorystatus_level"); err == nil {
			if used, err := memoryUsedFromFreePercent(total, free); err == nil {
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

	return usage
}
