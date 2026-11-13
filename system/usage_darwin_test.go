//go:build darwin

package system

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
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
	assert.Greater(t, *usage.MemoryUsedBytes, uint64(0))
	assert.LessOrEqual(t, *usage.MemoryUsedBytes, *usage.MemoryTotalBytes)

	require.NotNil(t, usage.DiskTotalBytes)
	require.NotNil(t, usage.DiskUsedBytes)
	assert.Greater(t, *usage.DiskTotalBytes, uint64(0))
	assert.LessOrEqual(t, *usage.DiskUsedBytes, *usage.DiskTotalBytes)

	// Mach can return identical aggregate counters for a short interval on
	// some macOS runners. CPU usage is optional when the sample is unavailable.
	if usage.CPUPercent != nil {
		assert.GreaterOrEqual(t, *usage.CPUPercent, 0.0)
		assert.LessOrEqual(t, *usage.CPUPercent, 100.0)
	}
	assert.False(t, usage.SampledAt.IsZero())
}

func TestDarwinReadResourceUsageWithMissingDataPath(t *testing.T) {
	usage := ReadResourceUsage(context.Background(), "/nonexistent/path")

	assert.Nil(t, usage.DiskTotalBytes)
	assert.Nil(t, usage.DiskUsedBytes)
	assert.NotNil(t, usage.MemoryTotalBytes)
}

func TestDarwinReadCPUTicksAdvance(t *testing.T) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		before, err := readCPUTicks()
		require.NoError(t, err)
		time.Sleep(300 * time.Millisecond)
		after, err := readCPUTicks()
		require.NoError(t, err)

		percent, err := cpuPercentFromTicks(before, after)
		if err != nil {
			continue
		}
		assert.GreaterOrEqual(t, percent, 0.0)
		assert.LessOrEqual(t, percent, 100.0)
		return
	}
	t.Skip("Darwin CPU counters did not advance during the sampling window")
}

func TestDarwinReadResourceUsageStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	usage := ReadResourceUsage(ctx, t.TempDir())
	assert.Nil(t, usage.CPUPercent)
	assert.NotNil(t, usage.MemoryTotalBytes)
}

var (
	vmStatPageSize = regexp.MustCompile(`page size of (\d+) bytes`)
	vmStatLine     = regexp.MustCompile(`^"?([^":]+?)"?:\s+(\d+)\.?$`)
)

// parseVMStat reads the output of vm_stat into its counters, keyed by their
// label, with the page size under "page size".
func parseVMStat(out string) map[string]uint64 {
	stat := map[string]uint64{}
	for _, line := range regexp.MustCompile(`\r?\n`).Split(out, -1) {
		if m := vmStatPageSize.FindStringSubmatch(line); m != nil {
			stat["page size"], _ = strconv.ParseUint(m[1], 10, 64)
			continue
		}
		if m := vmStatLine.FindStringSubmatch(line); m != nil {
			stat[m[1]], _ = strconv.ParseUint(m[2], 10, 64)
		}
	}
	return stat
}

func TestDarwinParseVMStat(t *testing.T) {
	stat := parseVMStat(`Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                4710.
Pages wired down:                        119345.
"Translation faults":                 1269325337.
File-backed pages:                       207404.
Anonymous pages:                         271332.
Pages occupied by compressor:            148498.
`)
	assert.Equal(t, uint64(16384), stat["page size"])
	assert.Equal(t, uint64(119345), stat["Pages wired down"])
	assert.Equal(t, uint64(1269325337), stat["Translation faults"])
	assert.Equal(t, uint64(271332), stat["Anonymous pages"])
	assert.Equal(t, uint64(148498), stat["Pages occupied by compressor"])
}

// The counters read through Mach are compared with what vm_stat reports, which
// reads them separately, so a mistake in the layout of the structure, which
// Mach fills in without any check, shows up here instead of as wrong numbers.
func TestDarwinVMStatsMatchVMStat(t *testing.T) {
	vm, pageSize, err := readVMStats()
	require.NoError(t, err)

	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		t.Skipf("vm_stat is not available: %v", err)
	}
	stat := parseVMStat(string(out))
	require.NotEmpty(t, stat)

	assert.Equal(t, stat["page size"], pageSize)

	// The counters move while they are read, so allow for some change.
	for label, got := range map[string]uint32{
		"Pages wired down":             vm.WireCount,
		"Anonymous pages":              vm.InternalPageCount,
		"File-backed pages":            vm.ExternalPageCount,
		"Pages purgeable":              vm.PurgeableCount,
		"Pages occupied by compressor": vm.CompressorPageCount,
		"Pages active":                 vm.ActiveCount,
		"Pages inactive":               vm.InactiveCount,
	} {
		want, ok := stat[label]
		require.True(t, ok, "vm_stat has no %q", label)
		assert.InDelta(t, float64(want), float64(got), float64(max(want/10, 20_000)), label)
	}
}

func TestDarwinMemoryUsedIsWithinThePhysicalMemory(t *testing.T) {
	usage := ReadResourceUsage(context.Background(), t.TempDir())
	require.NotNil(t, usage.MemoryUsedBytes)
	require.NotNil(t, usage.MemoryTotalBytes)

	// A machine that is running a test is never completely idle, nor full.
	used, total := float64(*usage.MemoryUsedBytes), float64(*usage.MemoryTotalBytes)
	assert.Greater(t, used/total, 0.02)
	assert.Less(t, used/total, 0.995)
}
