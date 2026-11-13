package system

import (
	"math"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The structure has to match the C one byte for byte, as Mach writes into it.
// These are the size and offsets of struct vm_statistics64 on a 64-bit machine.
func TestVMStatistics64Layout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("the layout is only checked for 64-bit machines, which is all macOS is")
	}

	var vm vmStatistics64
	assert.Equal(t, uintptr(152), unsafe.Sizeof(vm), "38 32-bit integers, HOST_VM_INFO64_COUNT")

	for name, want := range map[string]uintptr{
		"free_count":            0,
		"active_count":          4,
		"inactive_count":        8,
		"wire_count":            12,
		"purges":                80,
		"purgeable_count":       88,
		"speculative_count":     92,
		"decompressions":        96,
		"compressor_page_count": 128,
		"throttled_count":       132,
		"external_page_count":   136,
		"internal_page_count":   140,
		"total_uncompressed":    144,
	} {
		var got uintptr
		switch name {
		case "free_count":
			got = unsafe.Offsetof(vm.FreeCount)
		case "active_count":
			got = unsafe.Offsetof(vm.ActiveCount)
		case "inactive_count":
			got = unsafe.Offsetof(vm.InactiveCount)
		case "wire_count":
			got = unsafe.Offsetof(vm.WireCount)
		case "purges":
			got = unsafe.Offsetof(vm.Purges)
		case "purgeable_count":
			got = unsafe.Offsetof(vm.PurgeableCount)
		case "speculative_count":
			got = unsafe.Offsetof(vm.SpeculativeCount)
		case "decompressions":
			got = unsafe.Offsetof(vm.Decompressions)
		case "compressor_page_count":
			got = unsafe.Offsetof(vm.CompressorPageCount)
		case "throttled_count":
			got = unsafe.Offsetof(vm.ThrottledCount)
		case "external_page_count":
			got = unsafe.Offsetof(vm.ExternalPageCount)
		case "internal_page_count":
			got = unsafe.Offsetof(vm.InternalPageCount)
		case "total_uncompressed":
			got = unsafe.Offsetof(vm.TotalUncompressedPagesInCompressor)
		}
		assert.Equal(t, want, got, name)
	}
}

func TestMemoryUsedFromVMStats(t *testing.T) {
	const (
		gib      = uint64(1 << 30)
		pageSize = uint64(16384)
		total    = 16 * gib
	)

	for _, tt := range []struct {
		name string
		vm   vmStatistics64
		want uint64
	}{
		{
			// App memory 400k pages less 20k purgeable, 150k wired, 100k compressed.
			"anonymous without purgeable, wired and compressed",
			vmStatistics64{InternalPageCount: 400_000, PurgeableCount: 20_000, WireCount: 150_000, CompressorPageCount: 100_000},
			630_000 * pageSize,
		},
		{
			"cached files are not counted",
			vmStatistics64{InternalPageCount: 100_000, WireCount: 50_000, ExternalPageCount: 500_000, InactiveCount: 300_000, FreeCount: 10_000},
			150_000 * pageSize,
		},
		{
			"purgeable larger than anonymous does not underflow",
			vmStatistics64{InternalPageCount: 1_000, PurgeableCount: 5_000, WireCount: 2_000},
			2_000 * pageSize,
		},
		{
			// Slightly over the physical memory is counter skew and is capped.
			"skew is capped at the total",
			vmStatistics64{InternalPageCount: 1_000_000, WireCount: 50_000},
			total,
		},
	} {
		used, err := memoryUsedFromVMStats(tt.vm, pageSize, total)
		require.NoError(t, err, tt.name)
		assert.Equal(t, tt.want, used, tt.name)
	}
}

func TestMemoryUsedFromVMStatsRejectsWrongReadings(t *testing.T) {
	const total = uint64(16 << 30)
	ok := vmStatistics64{InternalPageCount: 100_000, WireCount: 50_000}

	for name, tt := range map[string]struct {
		vm       vmStatistics64
		pageSize uint64
		total    uint64
	}{
		"nothing was filled in": {vmStatistics64{}, 16384, total},
		"no page size":          {ok, 0, total},
		"no memory":             {ok, 16384, 0},
		"far more than there is": {
			vmStatistics64{InternalPageCount: 4_000_000_000, WireCount: 1}, 16384, total,
		},
		"would overflow": {
			vmStatistics64{InternalPageCount: math.MaxUint32, WireCount: math.MaxUint32}, math.MaxUint64, total,
		},
	} {
		_, err := memoryUsedFromVMStats(tt.vm, tt.pageSize, tt.total)
		assert.Error(t, err, name)
	}
}
