package system

import "time"

// ResourceUsage contains host counters, independent of server quotas. A nil
// counter means unavailable; zero is a valid idle measurement.
type ResourceUsage struct {
	CPUPercent       *float64  `json:"cpu_percent"`
	MemoryTotalBytes *uint64   `json:"memory_total_bytes"`
	MemoryUsedBytes  *uint64   `json:"memory_used_bytes"`
	DiskTotalBytes   *uint64   `json:"disk_total_bytes"`
	DiskUsedBytes    *uint64   `json:"disk_used_bytes"`
	SampledAt        time.Time `json:"sampled_at"`
}
