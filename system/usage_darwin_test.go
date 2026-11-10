//go:build darwin

package system

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDarwinReadResourceUsage(t *testing.T) {
	assert.True(t, HostUsageSupported)

	usage := ReadResourceUsage(context.Background(), t.TempDir())

	require.NotNil(t, usage.MemoryTotalBytes)
	require.NotNil(t, usage.MemoryUsedBytes)
	assert.Greater(t, *usage.MemoryTotalBytes, uint64(0))
	assert.LessOrEqual(t, *usage.MemoryUsedBytes, *usage.MemoryTotalBytes)

	require.NotNil(t, usage.DiskTotalBytes)
	require.NotNil(t, usage.DiskUsedBytes)
	assert.Greater(t, *usage.DiskTotalBytes, uint64(0))
	assert.LessOrEqual(t, *usage.DiskUsedBytes, *usage.DiskTotalBytes)

	// CPU is not available on macOS without cgo.
	assert.Nil(t, usage.CPUPercent)
	assert.False(t, usage.SampledAt.IsZero())
}

func TestDarwinReadResourceUsageWithMissingDataPath(t *testing.T) {
	usage := ReadResourceUsage(context.Background(), "/nonexistent/path")

	assert.Nil(t, usage.DiskTotalBytes)
	assert.Nil(t, usage.DiskUsedBytes)
	assert.NotNil(t, usage.MemoryTotalBytes)
}
