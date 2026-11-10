package system

import (
	"errors"
	"math"
)

// memoryUsedFromFreePercent returns the memory in use given the total memory
// and the percentage of it the kernel reports as available, which is how macOS
// exposes its memory pressure.
func memoryUsedFromFreePercent(total uint64, freePercent uint32) (uint64, error) {
	if total == 0 || freePercent > 100 || total > math.MaxUint64/100 {
		return 0, errors.New("memory counters unavailable")
	}
	available := total * uint64(freePercent) / 100
	return total - available, nil
}
