//go:build darwin

package system

import (
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
)

// The CPU counters of macOS are only available through Mach, and calling Mach
// without cgo is what purego is for: it looks the functions up in libSystem at
// runtime and calls them, so the binary stays a plain static Go build.

const (
	// hostCPULoadInfo is the flavor of host_statistics that returns the number
	// of ticks every CPU spent in each state.
	hostCPULoadInfo = 3

	// hostCPULoadInfoCount is the number of 32-bit integers in that result: the
	// ticks spent in the user, system, idle and nice states.
	hostCPULoadInfoCount = 4

	kernSuccess = 0
)

var (
	machOnce       sync.Once
	machErr        error
	machHost       uint32
	hostStatistics func(host uint32, flavor int32, info *uint32, count *uint32) int32
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
