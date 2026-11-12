package system

import (
	"errors"
	"math"
)

// vmStatistics64 mirrors struct vm_statistics64 from <mach/vm_statistics.h>,
// which host_statistics64 fills in. Every counter is a number of pages. The
// layout has to match the C structure exactly, field by field, which the tests
// check against its size and the offsets of the fields that are used.
type vmStatistics64 struct {
	FreeCount     uint32 // pages free
	ActiveCount   uint32 // pages active
	InactiveCount uint32 // pages inactive
	WireCount     uint32 // pages wired down

	ZeroFillCount uint64
	Reactivations uint64
	Pageins       uint64
	Pageouts      uint64
	Faults        uint64
	CowFaults     uint64
	Lookups       uint64
	Hits          uint64
	Purges        uint64

	PurgeableCount   uint32 // pages purgeable
	SpeculativeCount uint32 // pages speculative

	Decompressions uint64
	Compressions   uint64
	Swapins        uint64
	Swapouts       uint64

	CompressorPageCount uint32 // pages used by the compressor to hold compressed data
	ThrottledCount      uint32
	ExternalPageCount   uint32 // pages that are file-backed
	InternalPageCount   uint32 // pages that are anonymous

	TotalUncompressedPagesInCompressor uint64
}

// memoryUsedFromVMStats returns the memory in use, the way Activity Monitor adds
// it up: the anonymous memory of applications that can not simply be dropped
// (anonymous pages without the purgeable ones), memory wired down by the kernel,
// and memory occupied by the compressor. Cached files are not counted, they are
// given back when something needs the memory.
//
// total is the physical memory in bytes and pageSize the size of the pages the
// counters are in.
func memoryUsedFromVMStats(vm vmStatistics64, pageSize, total uint64) (uint64, error) {
	if pageSize == 0 || total == 0 {
		return 0, errors.New("memory counters unavailable")
	}

	anonymous := uint64(vm.InternalPageCount)
	// The counters are not read at the same instant, so a skew between them is
	// not a reason to fail.
	purgeable := min(uint64(vm.PurgeableCount), anonymous)

	pages := anonymous - purgeable + uint64(vm.WireCount) + uint64(vm.CompressorPageCount)
	if pages == 0 {
		// A machine always has wired memory, so nothing at all means that the
		// structure was not filled in.
		return 0, errors.New("memory counters unavailable")
	}
	if pages > math.MaxUint64/pageSize {
		return 0, errors.New("memory counters out of range")
	}

	used := pages * pageSize
	switch {
	case used > total+total/10:
		// More than the machine has by a margin is not skew, it is a wrong reading.
		return 0, errors.New("memory counters out of range")
	case used > total:
		used = total
	}
	return used, nil
}
