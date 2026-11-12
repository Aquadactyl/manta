//go:build darwin

package system

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The CPU and memory counters of macOS are only available through Mach, and
// calling Mach without cgo is what purego is for: it looks the functions up in
// libSystem at runtime and calls them, so the binary stays a plain static Go
// build.

const (
	// hostCPULoadInfo is the flavor of host_statistics that returns the number
	// of ticks every CPU spent in each state.
	hostCPULoadInfo = 3

	// hostCPULoadInfoCount is the number of 32-bit integers in that result: the
	// ticks spent in the user, system, idle and nice states.
	hostCPULoadInfoCount = 4

	// hostVMInfo64 is the flavor of host_statistics64 that returns the virtual
	// memory counters, in pages, as a vm_statistics64.
	hostVMInfo64 = 4

	kernSuccess = 0
)

var (
	machOnce       sync.Once
	machErr        error
	machHost       uint32
	hostStatistics func(host uint32, flavor int32, info *uint32, count *uint32) int32

	hostStatistics64 func(host uint32, flavor int32, info *vmStatistics64, count *uint32) int32
	hostPageSize     func(host uint32, size *uint64) int32
)

// loadMach finds the Mach functions, once. It reports an error rather than
// panicking when they are not there, so the counter is simply left out.
func loadMach() error {
	machOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				machErr = fmt.Errorf("mach is unavailable: %v", r)
			}
		}()

		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			machErr = err
			return
		}

		var hostSelf func() uint32
		purego.RegisterLibFunc(&hostSelf, lib, "mach_host_self")
		purego.RegisterLibFunc(&hostStatistics, lib, "host_statistics")
		purego.RegisterLibFunc(&hostStatistics64, lib, "host_statistics64")
		purego.RegisterLibFunc(&hostPageSize, lib, "host_page_size")

		// Every call of mach_host_self takes another reference to the port, so
		// it is asked for once and kept.
		machHost = hostSelf()
	})
	return machErr
}

// readCPUTicks returns the number of ticks all CPUs together spent in the user,
// system, idle and nice states since boot.
func readCPUTicks() ([4]uint32, error) {
	var ticks [4]uint32
	if err := loadMach(); err != nil {
		return ticks, err
	}

	count := uint32(hostCPULoadInfoCount)
	if kr := hostStatistics(machHost, hostCPULoadInfo, &ticks[0], &count); kr != kernSuccess {
		return ticks, fmt.Errorf("host_statistics failed with status %d", kr)
	}
	return ticks, nil
}

// readVMStats returns the virtual memory counters and the size in bytes of the
// pages they count.
func readVMStats() (vmStatistics64, uint64, error) {
	var vm vmStatistics64
	if err := loadMach(); err != nil {
		return vm, 0, err
	}

	// The count is the size of the structure in 32-bit integers, as Mach wants it.
	count := uint32(unsafe.Sizeof(vm) / 4)
	if kr := hostStatistics64(machHost, hostVMInfo64, &vm, &count); kr != kernSuccess {
		return vm, 0, fmt.Errorf("host_statistics64 failed with status %d", kr)
	}

	var pageSize uint64
	if kr := hostPageSize(machHost, &pageSize); kr != kernSuccess || pageSize == 0 {
		pageSize = uint64(os.Getpagesize())
	}
	return vm, pageSize, nil
}
