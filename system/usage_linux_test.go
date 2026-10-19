//go:build linux

package system

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMemoryUsageUsesAvailableMemoryAndPreservesZero(t *testing.T) {
	for _, test := range []struct {
		available string
		used      uint64
	}{{"1024", 0}, {"256", 768 * 1024}, {"0", 1024 * 1024}} {
		total, used, err := parseMemoryUsage("MemTotal: 1024 kB\nOther: ignored\nMemAvailable: " + test.available + " kB\n")
		require.NoError(t, err)
		require.EqualValues(t, 1024*1024, total)
		require.Equal(t, test.used, used)
	}
}

func TestMemoryUsageRejectsInvalidOrIncompleteSamples(t *testing.T) {
	for _, content := range []string{"", "MemTotal: 100 kB\n", "MemTotal: 0 kB\nMemAvailable: 0 kB", "MemTotal: 10 kB\nMemAvailable: 11 kB", "MemTotal: bad kB", "MemTotal: -1 kB", "MemTotal: 18446744073709551615 kB", "MemTotal: 10 bytes", "MemTotal:", strings.Repeat("a", 70000)} {
		_, _, err := parseMemoryUsage(content)
		require.Error(t, err, content)
	}
}

func TestCPUUsageUsesDeltasExcludesGuestAndCountsIOWaitAsIdle(t *testing.T) {
	for _, test := range []struct {
		before, after string
		want          float64
	}{
		{"cpu 100 0 100 800 0 0 0 0 50 0", "cpu 120 0 130 850 0 0 0 0 70 0", 50},
		{"cpu 0 0 0 100", "cpu 0 0 0 200", 0},
		{"cpu 100 0 0 100", "cpu 200 0 0 100", 100},
		{"cpu 100 0 100 800 0", "cpu 100 0 100 800 100", 0},
	} {
		percent, err := cpuUsageBetween(test.before, test.after)
		require.NoError(t, err)
		require.InDelta(t, test.want, percent, 0.0001)
	}
}

func TestCPUUsageRejectsInvalidOrResetCounters(t *testing.T) {
	const valid = "cpu 100 0 100 800"
	for _, pair := range [][2]string{{"", valid}, {valid, ""}, {"cpu0 1 2 3 4", valid}, {"cpu bad 0 0 0", valid}, {valid, valid}, {valid, "cpu 0 0 0 10"}, {valid, "cpu 1100 0 0 0"}, {valid, "cpu 100 0 0 2000"}, {"cpu 18446744073709551615 1 0 0", valid}} {
		_, err := cpuUsageBetween(pair[0], pair[1])
		require.Error(t, err, pair)
	}
}

func TestReadResourceUsageSamplesHostAndConfiguredDataFilesystem(t *testing.T) {
	usage := ReadResourceUsage(context.Background(), t.TempDir())
	require.NotNil(t, usage.MemoryTotalBytes)
	require.NotNil(t, usage.MemoryUsedBytes)
	require.NotNil(t, usage.CPUPercent)
	require.NotNil(t, usage.DiskTotalBytes)
	require.NotNil(t, usage.DiskUsedBytes)
	const zero uint64 = 0
	require.Greater(t, *usage.MemoryTotalBytes, zero)
	require.LessOrEqual(t, *usage.MemoryUsedBytes, *usage.MemoryTotalBytes)
	require.LessOrEqual(t, *usage.DiskUsedBytes, *usage.DiskTotalBytes)
	require.GreaterOrEqual(t, *usage.CPUPercent, 0.0)
	require.LessOrEqual(t, *usage.CPUPercent, 100.0)
	require.False(t, usage.SampledAt.IsZero())
}

func TestHostUsageCancellationAndMissingFilesystemAreNotZeroMeasurements(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	usage := ReadResourceUsage(ctx, t.TempDir()+"/missing")
	require.Nil(t, usage.CPUPercent)
	require.Nil(t, usage.DiskTotalBytes)
	require.Nil(t, usage.DiskUsedBytes)
	require.NotNil(t, usage.MemoryTotalBytes)
}
