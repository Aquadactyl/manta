//go:build darwin

package system

import (
	"context"
	"testing"
	"time"

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

	require.NotNil(t, usage.CPUPercent)
	assert.GreaterOrEqual(t, *usage.CPUPercent, 0.0)
	assert.LessOrEqual(t, *usage.CPUPercent, 100.0)
	assert.False(t, usage.SampledAt.IsZero())
}

func TestDarwinReadResourceUsageWithMissingDataPath(t *testing.T) {
	usage := ReadResourceUsage(context.Background(), "/nonexistent/path")

	assert.Nil(t, usage.DiskTotalBytes)
	assert.Nil(t, usage.DiskUsedBytes)
	assert.NotNil(t, usage.MemoryTotalBytes)
}

func TestDarwinReadCPUTicksAdvance(t *testing.T) {
	before, err := readCPUTicks()
	require.NoError(t, err)
	time.Sleep(300 * time.Millisecond)
	after, err := readCPUTicks()
	require.NoError(t, err)

	// A machine that is running this test has spent ticks since a moment ago.
	percent, err := cpuPercentFromTicks(before, after)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, percent, 0.0)
	assert.LessOrEqual(t, percent, 100.0)
}

func TestDarwinReadResourceUsageStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	usage := ReadResourceUsage(ctx, t.TempDir())
	assert.Nil(t, usage.CPUPercent)
	assert.NotNil(t, usage.MemoryTotalBytes)
}
